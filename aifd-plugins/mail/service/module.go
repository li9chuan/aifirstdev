// Package service implements the mail plugin service-level module.
package service

import (
	"context"
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
	"github.com/li9chuan/aifirstdev/aifd-plugins/mail/store"
	rpc "github.com/li9chuan/aifirstdev/aifd-rpc"
)

const (
	defaultSaveDays        = 20
	defaultCleanupInterval = 5 * time.Minute
	maxCustomTitleRunes    = 100
	maxCustomBodyRunes     = 5000
)

// Options configures the mail service module.
type Options struct {
	MaxBodyRunes    int // Zero uses the default 5000 Unicode characters.
	Mailbox         *mailbox.Manager
	IDGenerator     mail.IDGenerator
	Observe         observe.Sink
	Clock           mail.Clock
	Validator       mail.TemplateValidator
	CleanupInterval time.Duration
	NodeID          int64
	Push            mail.MailPushPort
}

// Module owns shared mail truth, internal send-mail RPCs, and cleanup lifecycle.
type Module struct {
	maxBodyRunes    int
	mu              sync.RWMutex
	mailbox         *mailbox.Manager
	idGenerator     mail.IDGenerator
	observe         observe.Sink
	clock           mail.Clock
	validator       mail.TemplateValidator
	cleanupInterval time.Duration
	stop            chan struct{}
	done            chan struct{}
	recovered       bool
	started         bool
	push            mail.MailPushPort
}

// NewModule creates a mail service module. Runtime work starts only in Recover/Start.
func NewModule(opts Options) *Module {
	if opts.MaxBodyRunes <= 0 {
		opts.MaxBodyRunes = maxCustomBodyRunes
	}
	if opts.Clock == nil {
		opts.Clock = mail.SystemClock{}
	}
	if opts.CleanupInterval == 0 {
		opts.CleanupInterval = defaultCleanupInterval
	}
	generator := opts.IDGenerator
	if generator == nil {
		generator, _ = id.NewSnowflake(opts.NodeID)
	}
	return &Module{
		maxBodyRunes:    opts.MaxBodyRunes,
		mailbox:         opts.Mailbox,
		idGenerator:     generator,
		observe:         opts.Observe,
		clock:           opts.Clock,
		validator:       opts.Validator,
		cleanupInterval: opts.CleanupInterval,
		push:            opts.Push,
		stop:            make(chan struct{}),
		done:            make(chan struct{}),
	}
}

// Descriptor returns the service module descriptor.
func (m *Module) Descriptor() base.ModuleDescriptor {
	return base.ModuleDescriptor{
		ID:      "aifd.plugins.mail.service",
		Version: "v0.1.0",
		Kind:    base.ModuleKindService,
		Provides: []base.Capability{
			{Kind: "rpc", Name: mail.TopicMailAdd},
			{Kind: "rpc", Name: mail.TopicMailAddBatch},
		},
	}
}

// Recover validates dependencies and performs an initial cleanup pass before writes are accepted.
func (m *Module) Recover(ctx context.Context) error {
	if m == nil {
		return fmt.Errorf("mail service module is nil")
	}
	if m.mailbox == nil {
		return fmt.Errorf("mail service mailbox manager is nil")
	}
	if m.idGenerator == nil {
		return fmt.Errorf("mail service id generator is nil")
	}
	if _, err := m.mailbox.CleanupSharedExpired(ctx, m.nowUnix()); err != nil {
		return fmt.Errorf("mail service cleanup: %w", err)
	}
	m.mu.Lock()
	m.recovered = true
	m.mu.Unlock()
	m.record(ctx, "mail.service.recover", nil)
	return nil
}

// RegisterRPC registers internal mail send routes.
func (m *Module) RegisterRPC(reg *rpc.Registry) error {
	if err := m.ensureRecovered(); err != nil {
		return err
	}
	if err := rpc.Server(reg, mail.TopicMailAdd, func(s *rpc.Session, req *mailpb.MailAddRequest) {
		rsp := m.Add(s.Context(), req)
		s.Response(&rsp)
	}); err != nil {
		return err
	}
	return rpc.Server(reg, mail.TopicMailAddBatch, func(s *rpc.Session, req *mailpb.MailAddBatchRequest) {
		rsp := m.AddBatch(s.Context(), req)
		s.Response(&rsp)
	})
}

// Start starts cleanup and cache janitor loops.
func (m *Module) Start(ctx context.Context) error {
	if err := m.ensureRecovered(); err != nil {
		return err
	}
	m.mu.Lock()
	if m.started {
		m.mu.Unlock()
		return nil
	}
	m.started = true
	m.mu.Unlock()
	if err := m.mailbox.Start(); err != nil {
		m.mu.Lock()
		m.started = false
		m.mu.Unlock()
		return err
	}
	go m.cleanupLoop()
	m.record(ctx, "mail.service.start", nil)
	return nil
}

// Close stops background work. The caller owns Store and Redis client lifecycles.
func (m *Module) Close(ctx context.Context) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	started := m.started
	if started {
		m.started = false
		close(m.stop)
	}
	m.mu.Unlock()
	if started {
		<-m.done
		m.mailbox.Close()
	}
	m.record(ctx, "mail.service.close", nil)
	return nil
}

// AddBatch persists each requested mail in order and stops on the first failure.
func (m *Module) AddBatch(ctx context.Context, req *mailpb.MailAddBatchRequest) mailpb.MailAddResponse {
	if req == nil {
		return failResponse(mail.RetInvalid, "mail add batch request is nil")
	}
	out := mailpb.MailAddResponse{Ret: mail.RetOK}
	for _, item := range req.GetMailList() {
		rsp := m.Add(ctx, item)
		if rsp.GetRet() != mail.RetOK {
			return rsp
		}
		out.MailIdList = append(out.MailIdList, rsp.GetMailIdList()...)
	}
	return out
}

// Add validates and persists one mail request.
func (m *Module) Add(ctx context.Context, req *mailpb.MailAddRequest) mailpb.MailAddResponse {
	if err := m.ensureRecovered(); err != nil {
		return failResponse(mail.RetInternal, err.Error())
	}
	if req == nil || req.GetBase() == nil {
		return failResponse(mail.RetInvalid, "mail add request base is nil")
	}
	if err := validateMailBaseLimit(req.GetBase(), m.maxBodyRunes); err != nil {
		return failResponse(mail.RetInvalid, err.Error())
	}
	if req.GetBase().GetTemplateId() > 0 && m.validator != nil {
		if err := m.validator.ValidateMail(ctx, req.GetBase()); err != nil {
			return failResponse(mail.RetInvalid, err.Error())
		}
	}
	switch req.GetType() {
	case mailpb.MailType_MAIL_TYPE_PERSONAL:
		return m.addPersonal(ctx, req)
	case mailpb.MailType_MAIL_TYPE_GLOBAL, mailpb.MailType_MAIL_TYPE_GROUP:
		return m.addShared(ctx, req)
	default:
		return failResponse(mail.RetInvalid, "mail type is invalid")
	}
}

func (m *Module) addPersonal(ctx context.Context, req *mailpb.MailAddRequest) mailpb.MailAddResponse {
	if len(req.GetRootIdList()) == 0 {
		return failResponse(mail.RetInvalid, "personal mail requires root ids")
	}
	out := mailpb.MailAddResponse{Ret: mail.RetOK}
	for _, rootID := range req.GetRootIdList() {
		if rootID <= 0 {
			continue
		}
		data, err := m.newMail(req, mailpb.MailType_MAIL_TYPE_PERSONAL)
		if err != nil {
			return failResponse(mail.RetInternal, err.Error())
		}
		if err := m.mailbox.Add(ctx, rootID, data); err != nil {
			return failResponse(mail.RetInternal, err.Error())
		}
		out.MailIdList = append(out.MailIdList, data.GetMailId())
		if m.push != nil {
			if err := m.push.PushMailReceived(ctx, rootID, &mailpb.MseMailReceived{Mail: store.CloneMail(data)}); err != nil {
				m.record(ctx, "mail.push.error", map[string]string{"root_id": fmt.Sprintf("%d", rootID)})
			}
		}
	}
	m.record(ctx, "mail.add.personal", map[string]string{"count": fmt.Sprintf("%d", len(out.MailIdList))})
	return out
}

func (m *Module) addShared(ctx context.Context, req *mailpb.MailAddRequest) mailpb.MailAddResponse {
	data, err := m.newMail(req, req.GetType())
	if err != nil {
		return failResponse(mail.RetInternal, err.Error())
	}
	if req.GetType() == mailpb.MailType_MAIL_TYPE_GROUP && len(data.GetGroupKeys()) == 0 {
		return failResponse(mail.RetInvalid, "group mail requires group keys")
	}
	if err := m.mailbox.SaveShared(ctx, data); err != nil {
		return failResponse(mail.RetInternal, err.Error())
	}
	m.record(ctx, "mail.add.shared", map[string]string{"type": data.GetType().String()})
	return mailpb.MailAddResponse{Ret: mail.RetOK, MailIdList: []int64{data.GetMailId()}}
}

func (m *Module) newMail(req *mailpb.MailAddRequest, typ mailpb.MailType) (*mailpb.MailData, error) {
	mailID, err := m.idGenerator.NextID()
	if err != nil {
		return nil, err
	}
	now := m.nowUnix()
	endTime := req.GetEndTimeUnix()
	if endTime <= 0 {
		saveDays := req.GetSaveDays()
		if saveDays <= 0 {
			saveDays = defaultSaveDays
		}
		endTime = now + int64(saveDays)*int64(24*time.Hour/time.Second)
	}
	return &mailpb.MailData{
		MailId:       mailID,
		Type:         typ,
		Base:         cloneBase(req.GetBase()),
		SendTimeUnix: now,
		EndTimeUnix:  endTime,
		GroupKeys:    append([]string(nil), req.GetGroupKeys()...),
		TargetRealms: append([]int32(nil), req.GetTargetRealms()...),
	}, nil
}

func (m *Module) cleanupLoop() {
	defer close(m.done)
	ticker := time.NewTicker(m.cleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-m.stop:
			return
		case <-ticker.C:
			ctx := context.Background()
			count, err := m.mailbox.CleanupSharedExpired(ctx, m.nowUnix())
			if err != nil {
				m.record(ctx, "mail.cleanup.error", nil)
				continue
			}
			if count > 0 {
				m.record(ctx, "mail.cleanup", map[string]string{"count": fmt.Sprintf("%d", count)})
			}
		}
	}
}

func (m *Module) ensureRecovered() error {
	if m == nil {
		return fmt.Errorf("mail service module is nil")
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if !m.recovered {
		return fmt.Errorf("mail service module is not recovered")
	}
	return nil
}

func (m *Module) nowUnix() int64 {
	if m.clock == nil {
		return time.Now().Unix()
	}
	return m.clock.Now().Unix()
}

func (m *Module) record(ctx context.Context, name string, dimensions map[string]string) {
	if m == nil || m.observe == nil {
		return
	}
	_ = m.observe.Write(ctx, []observe.Event{observe.Metric(name, 1, dimensions)})
}

func cloneBase(base *mailpb.MailBase) *mailpb.MailBase {
	if base == nil {
		return nil
	}
	out := *base
	out.ContentList = append([]string(nil), base.ContentList...)
	if base.CustomContent != nil {
		out.CustomContent = &mailpb.MailCustomContent{
			Title: base.CustomContent.GetTitle(),
			Body:  base.CustomContent.GetBody(),
		}
	}
	out.ItemList = make([]*mailpb.MailItem, 0, len(base.ItemList))
	for _, item := range base.ItemList {
		if item == nil {
			continue
		}
		cp := *item
		out.ItemList = append(out.ItemList, &cp)
	}
	return &out
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
