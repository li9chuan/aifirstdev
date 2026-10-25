// Package service is the sole owner of personal and shared mail state.
package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	base "github.com/li9chuan/aifirstdev/aifd-contracts"
	observe "github.com/li9chuan/aifirstdev/aifd-observe/contracts"
	mail "github.com/li9chuan/aifirstdev/aifd-plugins/mail"
	"github.com/li9chuan/aifirstdev/aifd-plugins/mail/generated/mailpb"
	"github.com/li9chuan/aifirstdev/aifd-plugins/mail/internal/id"
	"github.com/li9chuan/aifirstdev/aifd-plugins/mail/mailbox"
	rdb "github.com/li9chuan/aifirstdev/aifd-rdb"
	rpc "github.com/li9chuan/aifirstdev/aifd-rpc"
	"google.golang.org/protobuf/proto"
)

const (
	defaultCleanupInterval = 5 * time.Minute
	maxCustomTitleRunes    = 100
	maxCustomBodyRunes     = 5000
)

type Options struct {
	DB              *rdb.DB
	Prefix          string
	Awarder         mail.Awarder
	ClaimCD         time.Duration
	AwardTimeout    time.Duration
	MailboxLimit    int
	CacheTTL        time.Duration
	CacheCapacity   int
	MaxBodyRunes    int
	IDGenerator     mail.IDGenerator
	Observe         observe.Sink
	Clock           mail.Clock
	Validator       mail.TemplateValidator
	CleanupInterval time.Duration
	NodeID          int64
	Push            mail.MailPushPort
}

type Module struct {
	mailbox                     *mailbox.Manager
	opts                        Options
	mu                          sync.Mutex
	recoverMu                   sync.Mutex
	recovered, started, closing bool
	requests                    sync.WaitGroup
	shutdown                    sync.Once
	stop, done, drained         chan struct{}
}

// NewModule only validates and constructs; Recover precedes all route admission.
func NewModule(opts Options) (*Module, error) {
	if opts.CleanupInterval < 0 || opts.MaxBodyRunes < 0 {
		return nil, fmt.Errorf("mail durations and limits cannot be negative")
	}
	if opts.Clock == nil {
		opts.Clock = mail.SystemClock{}
	}
	if opts.CleanupInterval == 0 {
		opts.CleanupInterval = defaultCleanupInterval
	}
	if opts.MaxBodyRunes == 0 {
		opts.MaxBodyRunes = maxCustomBodyRunes
	}
	if opts.IDGenerator == nil {
		generator, err := id.NewSnowflake(opts.NodeID)
		if err != nil {
			return nil, err
		}
		opts.IDGenerator = generator
	}
	manager, err := mailbox.NewManager(mailbox.Options{DB: opts.DB, Prefix: opts.Prefix, Clock: opts.Clock, IDGenerator: opts.IDGenerator, Awarder: opts.Awarder,
		MailboxLimit: opts.MailboxLimit, CacheTTL: opts.CacheTTL, CacheCapacity: opts.CacheCapacity, ClaimCD: opts.ClaimCD, AwardTimeout: opts.AwardTimeout})
	if err != nil {
		return nil, err
	}
	return &Module{mailbox: manager, opts: opts, stop: make(chan struct{}), done: make(chan struct{}), drained: make(chan struct{})}, nil
}

func (m *Module) Descriptor() base.ModuleDescriptor {
	topics := []string{mail.TopicMailAdd, mail.TopicMailAddBatch, mail.TopicMailList, mail.TopicMailGet, mail.TopicMailRead, mail.TopicMailClaim, mail.TopicMailDeleteRead}
	descriptor := base.ModuleDescriptor{ID: "aifd.plugins.mail.service", Version: "v0.2.0", Kind: base.ModuleKindService}
	for _, topic := range topics {
		descriptor.Provides = append(descriptor.Provides, base.Capability{Kind: "rpc", Name: topic})
	}
	return descriptor
}

func (m *Module) Recover(ctx context.Context) error {
	m.recoverMu.Lock()
	defer m.recoverMu.Unlock()
	m.mu.Lock()
	if m.closing {
		m.mu.Unlock()
		return mail.ErrClosed
	}
	if m.recovered {
		m.mu.Unlock()
		return nil
	}
	m.requests.Add(1)
	m.mu.Unlock()
	defer m.requests.Done()
	if err := m.mailbox.Recover(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing {
		return mail.ErrClosed
	}
	m.recovered = true
	return nil
}

func (m *Module) enter(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing {
		return nil, mail.ErrClosed
	}
	if !m.recovered {
		return nil, mail.ErrNotReady
	}
	m.requests.Add(1)
	return m.requests.Done, nil
}

func (m *Module) RegisterRPC(reg *rpc.Registry) error {
	done, err := m.enter(context.Background())
	if err != nil {
		return err
	}
	defer done()
	routes := []struct {
		topic   string
		handler any
	}{
		{mail.TopicMailAdd, func(s *rpc.Session, req *mailpb.MailAddRequest) { rsp := m.Add(s.Context(), req); s.Response(&rsp) }},
		{mail.TopicMailAddBatch, func(s *rpc.Session, req *mailpb.MailAddBatchRequest) {
			rsp := m.AddBatch(s.Context(), req)
			s.Response(&rsp)
		}},
		{mail.TopicMailList, func(s *rpc.Session, req *mailpb.MailListRequest) {
			s.ResponseAs(mail.TopicMseMailList, m.List(s.Context(), s.UID(), req))
		}},
		{mail.TopicMailGet, func(s *rpc.Session, req *mailpb.MailGetRequest) {
			s.ResponseAs(mail.TopicMseMailGet, m.Get(s.Context(), s.UID(), req))
		}},
		{mail.TopicMailRead, func(s *rpc.Session, req *mailpb.MailIDsRequest) {
			s.ResponseAs(mail.TopicMseMailRead, m.Read(s.Context(), s.UID(), req))
		}},
		{mail.TopicMailClaim, func(s *rpc.Session, req *mailpb.MailIDsRequest) {
			s.ResponseAs(mail.TopicMseMailClaim, m.Claim(s.Context(), s.UID(), req))
		}},
		{mail.TopicMailDeleteRead, func(s *rpc.Session, req *mailpb.MailIDsRequest) {
			s.ResponseAs(mail.TopicMseMailDeleteRead, m.DeleteRead(s.Context(), s.UID(), req))
		}},
	}
	for _, route := range routes {
		if err := rpc.Server(reg, route.topic, route.handler); err != nil {
			return err
		}
	}
	return nil
}

func (m *Module) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing {
		return mail.ErrClosed
	}
	if !m.recovered {
		return mail.ErrNotReady
	}
	if m.started {
		return nil
	}
	m.started = true
	go m.cleanupLoop(ctx)
	return nil
}

func (m *Module) cleanupLoop(ctx context.Context) {
	defer close(m.done)
	ticker := time.NewTicker(m.opts.CleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-m.stop:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			cleanupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := m.mailbox.CleanupExpired(cleanupCtx, m.opts.Clock.Now().Unix())
			cancel()
			if err != nil {
				m.record(ctx, "mail.cleanup.error", 0, err)
			}
		}
	}
}

// Close stops admission, drains accepted requests, then flushes without closing
// caller-owned DB/bus/Redis. A later Close can retry a failed or timed-out Flush.
func (m *Module) Close(ctx context.Context) error {
	m.shutdown.Do(func() {
		m.mu.Lock()
		m.closing = true
		close(m.stop)
		if !m.started {
			close(m.done)
		}
		m.mu.Unlock()
		go func() { <-m.done; m.requests.Wait(); close(m.drained) }()
	})
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-m.drained:
	}
	return m.mailbox.Close(ctx)
}

func (m *Module) Add(ctx context.Context, req *mailpb.MailAddRequest) mailpb.MailAddResponse {
	done, err := m.enter(ctx)
	if err != nil {
		return failResponse(retCode(err), err.Error())
	}
	defer done()
	normalized, err := m.normalize(ctx, req)
	if err != nil {
		return failResponse(mail.RetInvalid, err.Error())
	}
	return m.addNormalized(ctx, normalized)
}

func (m *Module) AddBatch(ctx context.Context, req *mailpb.MailAddBatchRequest) mailpb.MailAddResponse {
	done, err := m.enter(ctx)
	if err != nil {
		return failResponse(retCode(err), err.Error())
	}
	defer done()
	if req == nil || len(req.GetMailList()) == 0 {
		return failResponse(mail.RetInvalid, "mail batch is empty")
	}
	normalized := make([]*mailpb.MailAddRequest, 0, len(req.GetMailList()))
	for _, item := range req.GetMailList() {
		next, err := m.normalize(ctx, item)
		if err != nil {
			return failResponse(mail.RetInvalid, err.Error())
		}
		normalized = append(normalized, next)
	}
	out := mailpb.MailAddResponse{Ret: mail.RetOK}
	for index, item := range normalized {
		rsp := m.addNormalized(ctx, item)
		out.MailIdList = append(out.MailIdList, rsp.GetMailIdList()...)
		if rsp.GetRet() != mail.RetOK {
			out.Ret = rsp.GetRet()
			if len(out.MailIdList) > 0 {
				out.Ret = mail.RetPartial
			}
			out.Message = fmt.Sprintf("batch[%d]: %s", index, rsp.GetMessage())
			return out
		}
	}
	return out
}

func (m *Module) addNormalized(ctx context.Context, req *mailpb.MailAddRequest) mailpb.MailAddResponse {
	out := mailpb.MailAddResponse{Ret: mail.RetOK}
	targets := req.GetUidList()
	if req.GetType() != mailpb.MailType_MAIL_TYPE_PERSONAL {
		targets = []int64{0}
	}
	for _, uid := range targets {
		var data *mailpb.MailData
		var err error
		if uid > 0 {
			data, err = m.mailbox.AddPersonal(ctx, uid, req)
		} else {
			data, err = m.mailbox.PublishShared(ctx, req)
		}
		if data != nil {
			out.MailIdList = append(out.MailIdList, data.GetMailId())
			if uid > 0 && m.opts.Push != nil {
				if pushErr := m.opts.Push.PushMailReceived(ctx, uid, &mailpb.MseMailReceived{Mail: proto.Clone(data).(*mailpb.MailData)}); pushErr != nil {
					m.record(ctx, "mail.push.error", uid, pushErr)
				}
			}
		}
		if err != nil {
			out.Ret = retCode(err)
			if len(out.MailIdList) > 0 {
				out.Ret = mail.RetPartial
			}
			out.Message = fmt.Sprintf("target uid %d: %s", uid, err)
			m.record(ctx, "mail.add.error", uid, err)
			return out
		}
	}
	m.record(ctx, "mail.add", 0, nil)
	return out
}

func (m *Module) normalize(ctx context.Context, req *mailpb.MailAddRequest) (*mailpb.MailAddRequest, error) {
	if req == nil || req.GetBase() == nil {
		return nil, fmt.Errorf("mail base is required")
	}
	req = proto.Clone(req).(*mailpb.MailAddRequest)
	if err := validateMailBaseLimit(req.GetBase(), m.opts.MaxBodyRunes); err != nil {
		return nil, err
	}
	for _, text := range req.GetBase().GetContentList() {
		if !utf8.ValidString(text) {
			return nil, fmt.Errorf("mail content is not UTF-8")
		}
	}
	for _, item := range req.GetBase().GetItemList() {
		if item.GetTid() <= 0 || item.GetNum() <= 0 {
			return nil, fmt.Errorf("mail attachment identity/count must be positive")
		}
	}
	if req.GetSaveDays() < 0 || req.GetEndTimeUnix() < 0 || (req.GetEndTimeUnix() > 0 && req.GetEndTimeUnix() <= m.opts.Clock.Now().Unix()) {
		return nil, fmt.Errorf("mail expiry is invalid")
	}
	if len(req.GetGroupKeys()) > 0 || len(req.GetTargetRealms()) > 0 {
		return nil, fmt.Errorf("legacy targeting fields are unsupported; use one group_key and separate Mail.AddBatch entries")
	}
	if req.GetGroupKey() != "" && (strings.TrimSpace(req.GetGroupKey()) == "" || !utf8.ValidString(req.GetGroupKey())) {
		return nil, fmt.Errorf("mail group_key is invalid")
	}
	switch req.GetType() {
	case mailpb.MailType_MAIL_TYPE_PERSONAL:
		if len(req.GetUidList()) == 0 || req.GetGroupKey() != "" {
			return nil, fmt.Errorf("personal mail requires fixed UIDs only")
		}
		seen := map[int64]bool{}
		uids := make([]int64, 0, len(req.GetUidList()))
		for _, uid := range req.GetUidList() {
			if uid <= 0 {
				return nil, fmt.Errorf("mail UID must be positive")
			}
			if !seen[uid] {
				uids = append(uids, uid)
				seen[uid] = true
			}
		}
		req.UidList = uids
	case mailpb.MailType_MAIL_TYPE_GLOBAL:
		if len(req.GetUidList()) > 0 || req.GetGroupKey() != "" {
			return nil, fmt.Errorf("global mail cannot contain UIDs/groups")
		}
	case mailpb.MailType_MAIL_TYPE_GROUP:
		if len(req.GetUidList()) > 0 || req.GetGroupKey() == "" {
			return nil, fmt.Errorf("group mail requires one group_key without UIDs")
		}
	default:
		return nil, fmt.Errorf("invalid mail type")
	}
	if req.GetBase().GetTemplateId() > 0 && m.opts.Validator != nil {
		if err := m.opts.Validator.ValidateMail(ctx, proto.Clone(req.GetBase()).(*mailpb.MailBase)); err != nil {
			return nil, err
		}
	}
	return req, nil
}

func (m *Module) List(ctx context.Context, uid int64, req *mailpb.MailListRequest) *mailpb.MseMailList {
	done, err := m.enter(ctx)
	if err != nil {
		return &mailpb.MseMailList{Ret: retCode(err), Message: err.Error()}
	}
	defer done()
	query := req.GetQuery()
	if query.GetAfterMailId() < 0 {
		return &mailpb.MseMailList{Ret: mail.RetInvalid, Message: "invalid mail cursor"}
	}
	list, err := m.mailbox.List(ctx, uid, req.GetGroupKeys(), query.GetAfterMailId(), int(query.GetLimit()))
	if err != nil {
		return &mailpb.MseMailList{Ret: retCode(err), Message: err.Error()}
	}
	if query.GetSummaryOnly() {
		for i := range list {
			list[i] = mailbox.SummaryMail(list[i])
		}
	}
	return &mailpb.MseMailList{Ret: mail.RetOK, TimeUnix: m.opts.Clock.Now().Unix(), MailList: list}
}

func (m *Module) Get(ctx context.Context, uid int64, req *mailpb.MailGetRequest) *mailpb.MseMailGet {
	done, err := m.enter(ctx)
	if err != nil {
		return &mailpb.MseMailGet{Ret: retCode(err), Message: err.Error()}
	}
	defer done()
	data, found, err := m.mailbox.Get(ctx, uid, req.GetGroupKeys(), req.GetMailId())
	if err != nil {
		return &mailpb.MseMailGet{Ret: retCode(err), Message: err.Error()}
	}
	if !found {
		return &mailpb.MseMailGet{Ret: mail.RetNotFound, Message: mail.ErrNotFound.Error()}
	}
	return &mailpb.MseMailGet{Ret: mail.RetOK, Mail: data}
}

func (m *Module) Read(ctx context.Context, uid int64, req *mailpb.MailIDsRequest) *mailpb.MseMailRead {
	done, err := m.enter(ctx)
	if err != nil {
		return &mailpb.MseMailRead{Ret: retCode(err), Message: err.Error()}
	}
	defer done()
	ids, err := m.mailbox.MarkRead(ctx, uid, req.GetGroupKeys(), req.GetMailIdList())
	ret, message := operationResult(err, len(ids))
	return &mailpb.MseMailRead{Ret: ret, MailIdList: ids, Message: message}
}

func (m *Module) Claim(ctx context.Context, uid int64, req *mailpb.MailIDsRequest) *mailpb.MseMailClaim {
	done, err := m.enter(ctx)
	if err != nil {
		return &mailpb.MseMailClaim{Ret: retCode(err), Message: err.Error()}
	}
	defer done()
	result, err := m.mailbox.Claim(ctx, uid, req.GetGroupKeys(), req.GetMailIdList())
	ret, message := operationResult(err, len(result.MailIDs))
	m.record(ctx, "mail.claim", uid, err)
	return &mailpb.MseMailClaim{Ret: ret, MailIdList: result.MailIDs, RetryAtUnixMs: result.RetryAtUnixMs, Message: message}
}

func (m *Module) DeleteRead(ctx context.Context, uid int64, req *mailpb.MailIDsRequest) *mailpb.MseMailDeleteRead {
	done, err := m.enter(ctx)
	if err != nil {
		return &mailpb.MseMailDeleteRead{Ret: retCode(err), Message: err.Error()}
	}
	defer done()
	ids, err := m.mailbox.DeleteRead(ctx, uid, req.GetGroupKeys(), req.GetMailIdList())
	ret, message := operationResult(err, len(ids))
	return &mailpb.MseMailDeleteRead{Ret: ret, MailIdList: ids, Message: message}
}

func operationResult(err error, count int) (int32, string) {
	if err == nil {
		return mail.RetOK, ""
	}
	if count > 0 {
		return mail.RetPartial, err.Error()
	}
	return retCode(err), err.Error()
}
func retCode(err error) int32 {
	switch {
	case err == nil:
		return mail.RetOK
	case errors.Is(err, mail.ErrInvalid):
		return mail.RetInvalid
	case errors.Is(err, mail.ErrNotFound):
		return mail.RetNotFound
	case errors.Is(err, mail.ErrBusy):
		return mail.RetBusy
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return mail.RetTimeout
	default:
		return mail.RetInternal
	}
}

func (m *Module) record(ctx context.Context, name string, uid int64, err error) {
	if m.opts.Observe == nil {
		return
	}
	tags := map[string]string{"uid": fmt.Sprint(uid)}
	if err != nil {
		tags["error"] = err.Error()
	}
	_ = m.opts.Observe.Write(context.WithoutCancel(ctx), []observe.Event{observe.Metric(name, 1, tags)})
}

func validateMailBase(base *mailpb.MailBase) error {
	return validateMailBaseLimit(base, maxCustomBodyRunes)
}

func validateMailBaseLimit(base *mailpb.MailBase, bodyLimit int) error {
	if base == nil {
		return fmt.Errorf("mail base is nil")
	}
	if base.GetTemplateId() < 0 {
		return fmt.Errorf("mail template_id cannot be negative")
	}

	hasTemplate := base.GetTemplateId() > 0
	customContent := base.GetCustomContent()
	if hasTemplate && customContent != nil {
		return fmt.Errorf("mail template and custom content are mutually exclusive")
	}
	if !hasTemplate && customContent == nil {
		return fmt.Errorf("mail content requires template_id or custom_content")
	}
	if customContent == nil {
		return nil
	}
	if len(base.GetContentList()) > 0 {
		return fmt.Errorf("custom mail content_list must be empty")
	}
	if strings.TrimSpace(customContent.GetTitle()) == "" {
		return fmt.Errorf("custom mail title is required")
	}
	if strings.TrimSpace(customContent.GetBody()) == "" {
		return fmt.Errorf("custom mail body is required")
	}
	if !utf8.ValidString(customContent.GetTitle()) || !utf8.ValidString(customContent.GetBody()) {
		return fmt.Errorf("mail custom content is not UTF-8")
	}
	if utf8.RuneCountInString(customContent.GetTitle()) > maxCustomTitleRunes {
		return fmt.Errorf("custom mail title exceeds %d characters", maxCustomTitleRunes)
	}
	if utf8.RuneCountInString(customContent.GetBody()) > bodyLimit {
		return fmt.Errorf("custom mail body exceeds %d characters", bodyLimit)
	}
	return nil
}

func failResponse(ret int32, message string) mailpb.MailAddResponse {
	return mailpb.MailAddResponse{Ret: ret, Message: message}
}
