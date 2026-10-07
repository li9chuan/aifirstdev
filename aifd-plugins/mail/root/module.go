// Package root implements the mail plugin root-side mailbox module.
package root

import (
	"context"
	"fmt"
	"sort"
	"strconv"

	base "github.com/li9chuan/aifirstdev/aifd-contracts"
	observe "github.com/li9chuan/aifirstdev/aifd-observe/contracts"
	mail "github.com/li9chuan/aifirstdev/aifd-plugins/mail"
	"github.com/li9chuan/aifirstdev/aifd-plugins/mail/generated/mailpb"
	"github.com/li9chuan/aifirstdev/aifd-plugins/mail/mailbox"
	"github.com/li9chuan/aifirstdev/aifd-plugins/mail/store"
	rpc "github.com/li9chuan/aifirstdev/aifd-rpc"
	runtimeroot "github.com/li9chuan/aifirstdev/aifd-runtime/root"
	"google.golang.org/protobuf/proto"
)

const (
	defaultListLimit = 100
)

// GroupResolver returns the group mail keys a root should receive.
type GroupResolver[T proto.Message] interface {
	MailGroupKeys(context.Context, *runtimeroot.Root[T]) ([]string, error)
}

// GroupResolverFunc adapts a function into GroupResolver.
type GroupResolverFunc[T proto.Message] func(context.Context, *runtimeroot.Root[T]) ([]string, error)

// MailGroupKeys calls the wrapped function.
func (f GroupResolverFunc[T]) MailGroupKeys(ctx context.Context, root *runtimeroot.Root[T]) ([]string, error) {
	if f == nil {
		return nil, nil
	}
	return f(ctx, root)
}

// Options configures the root-side mail module.
type Options[T proto.Message] struct {
	Mailbox       *mailbox.Manager
	Awarder       mail.Awarder
	Observe       observe.Sink
	Clock         mail.Clock
	GroupResolver GroupResolver[T]
	RealmID       int32
	ListLimit     int
}

// Module owns root-side mailbox RPC and personal mail state transitions.
type Module[T proto.Message] struct {
	mailbox       *mailbox.Manager
	awarder       mail.Awarder
	observe       observe.Sink
	clock         mail.Clock
	groupResolver GroupResolver[T]
	moduleCtx     runtimeroot.ModuleContext[T]
	realmID       int32
	listLimit     int
}

// NewModule creates a root-side mail module.
func NewModule[T proto.Message](opts Options[T]) *Module[T] {
	if opts.Clock == nil {
		opts.Clock = mail.SystemClock{}
	}
	if opts.ListLimit <= 0 {
		opts.ListLimit = defaultListLimit
	}
	module := &Module[T]{
		mailbox:       opts.Mailbox,
		awarder:       opts.Awarder,
		observe:       opts.Observe,
		clock:         opts.Clock,
		groupResolver: opts.GroupResolver,
		realmID:       opts.RealmID,
		listLimit:     opts.ListLimit,
	}
	return module
}

// Descriptor returns the root module descriptor.
func (m *Module[T]) Descriptor() base.ModuleDescriptor {
	return base.ModuleDescriptor{
		ID:      "aifd.plugins.mail.root",
		Version: "v0.1.0",
		Kind:    base.ModuleKindRoot,
		Provides: []base.Capability{
			{Kind: "rpc", Name: mail.TopicMceMailGet},
			{Kind: "rpc", Name: mail.TopicMceMailList},
			{Kind: "rpc", Name: mail.TopicMceMailRead},
			{Kind: "rpc", Name: mail.TopicMceMailClaim},
			{Kind: "rpc", Name: mail.TopicMceMailDeleteRead},
		},
	}
}

// BindModuleContext stores the active root lookup for optional group resolution.
func (m *Module[T]) BindModuleContext(moduleCtx runtimeroot.ModuleContext[T]) error {
	if m == nil {
		return fmt.Errorf("mail root module is nil")
	}
	m.moduleCtx = moduleCtx
	return nil
}

// OnLoad validates dependencies before a root can use mailbox RPCs.
func (m *Module[T]) OnLoad(context.Context, *runtimeroot.Root[T]) error {
	if m == nil || m.mailbox == nil {
		return fmt.Errorf("mail root mailbox manager is nil")
	}
	return nil
}

// OnActivate currently has no root activation side effects.
func (m *Module[T]) OnActivate(context.Context, *runtimeroot.Root[T]) error {
	return nil
}

// RegisterRPC registers client mailbox routes.
func (m *Module[T]) RegisterRPC(reg *rpc.Registry) error {
	if m == nil || m.mailbox == nil {
		return fmt.Errorf("mail root mailbox manager is nil")
	}
	if err := rpc.Client(reg, mail.TopicMceMailGet, func(s *rpc.Session, req *mailpb.MceMailGet) {
		s.ResponseAs(mail.TopicMseMailGet, m.Get(s.Context(), s.UID(), req))
	}); err != nil {
		return err
	}
	if err := rpc.Client(reg, mail.TopicMceMailList, func(s *rpc.Session, req *mailpb.MceMailList) {
		activeRoot, _ := m.moduleCtx.GetRoot(s.UID())
		s.ResponseAs(mail.TopicMseMailList, m.List(s.Context(), s.UID(), activeRoot, req))
	}); err != nil {
		return err
	}
	if err := rpc.Client(reg, mail.TopicMceMailRead, func(s *rpc.Session, req *mailpb.MceMailRead) {
		s.ResponseAs(mail.TopicMseMailRead, m.Read(s.Context(), s.UID(), req))
	}); err != nil {
		return err
	}
	if err := rpc.Client(reg, mail.TopicMceMailClaim, func(s *rpc.Session, req *mailpb.MceMailClaim) {
		s.ResponseAs(mail.TopicMseMailClaim, m.Claim(s.Context(), s.UID(), req))
	}); err != nil {
		return err
	}
	return rpc.Client(reg, mail.TopicMceMailDeleteRead, func(s *rpc.Session, req *mailpb.MceMailDeleteRead) {
		s.ResponseAs(mail.TopicMseMailDeleteRead, m.DeleteRead(s.Context(), s.UID(), req))
	})
}

// List syncs shared mail into a root mailbox and returns personal mail after the requested cursor.
func (m *Module[T]) List(ctx context.Context, rootID int64, activeRoot *runtimeroot.Root[T], req *mailpb.MceMailList) *mailpb.MseMailList {
	if rootID <= 0 {
		return &mailpb.MseMailList{Ret: mail.RetInvalid, Message: "root id is invalid"}
	}
	if req == nil {
		req = &mailpb.MceMailList{}
	}
	limit := int(req.GetLimit())
	if limit <= 0 || limit > m.listLimit {
		limit = m.listLimit
	}
	groupKeys, err := m.groupKeys(ctx, activeRoot, req.GetGroupKeys())
	if err != nil {
		return &mailpb.MseMailList{Ret: mail.RetInternal, Message: err.Error()}
	}
	if err := m.mailbox.SyncShared(ctx, rootID, m.realmID, groupKeys, m.listLimit); err != nil {
		return &mailpb.MseMailList{Ret: mail.RetInternal, Message: err.Error()}
	}
	list, err := m.mailbox.List(rootID, req.GetAfterMailId(), limit)
	if err != nil {
		return &mailpb.MseMailList{Ret: mail.RetInternal, Message: err.Error()}
	}
	m.record(ctx, "mail.list", rootID)
	if req.GetSummaryOnly() {
		for i := range list {
			list[i] = store.SummaryMail(list[i])
		}
	}
	return &mailpb.MseMailList{Ret: mail.RetOK, TimeUnix: m.nowUnix(), MailList: list}
}

// Get returns a live mail owned by the authenticated root without marking it read.
func (m *Module[T]) Get(ctx context.Context, rootID int64, req *mailpb.MceMailGet) *mailpb.MseMailGet {
	if rootID <= 0 || req.GetMailId() <= 0 {
		return &mailpb.MseMailGet{Ret: mail.RetInvalid, Message: "invalid mail identity"}
	}
	data, found, err := m.mailbox.Get(rootID, req.GetMailId())
	if err != nil {
		return &mailpb.MseMailGet{Ret: mail.RetInternal, Message: "mail read failed"}
	}
	if !found {
		return &mailpb.MseMailGet{Ret: mail.RetNotFound, Message: "mail expired or deleted"}
	}
	return &mailpb.MseMailGet{Ret: mail.RetOK, Mail: data}
}

// Read marks mails as read when they do not have unclaimed attachments.
func (m *Module[T]) Read(ctx context.Context, rootID int64, req *mailpb.MceMailRead) *mailpb.MseMailRead {
	if req == nil {
		req = &mailpb.MceMailRead{}
	}
	readIDs, err := m.mailbox.MarkRead(ctx, rootID, req.GetMailIdList())
	if err != nil {
		return &mailpb.MseMailRead{Ret: mail.RetInternal, Message: err.Error()}
	}
	return &mailpb.MseMailRead{Ret: mail.RetOK, MailIdList: readIDs}
}

// Claim grants unclaimed attachments and marks mails as read and claimed.
func (m *Module[T]) Claim(ctx context.Context, rootID int64, req *mailpb.MceMailClaim) *mailpb.MseMailClaim {
	if req == nil {
		req = &mailpb.MceMailClaim{}
	}
	claimedIDs := make([]int64, 0, len(req.GetMailIdList()))
	for _, mailID := range req.GetMailIdList() {
		data, ok, err := m.mailbox.Get(rootID, mailID)
		if err != nil {
			return &mailpb.MseMailClaim{Ret: mail.RetInternal, Message: err.Error()}
		}
		if !ok || data.GetClaimed() || !hasAttachment(data) {
			continue
		}
		if m.awarder != nil {
			if err := m.awarder.AwardMail(ctx, rootID, store.CloneMail(data)); err != nil {
				return &mailpb.MseMailClaim{Ret: mail.RetInternal, Message: err.Error()}
			}
		}
		changed, err := m.mailbox.MarkClaimed(ctx, rootID, mailID)
		if err != nil {
			return &mailpb.MseMailClaim{Ret: mail.RetInternal, Message: err.Error()}
		}
		if changed {
			claimedIDs = append(claimedIDs, mailID)
		}
	}
	m.record(ctx, "mail.claim", rootID)
	return &mailpb.MseMailClaim{Ret: mail.RetOK, MailIdList: claimedIDs}
}

// DeleteRead deletes read mails. An empty request list means all read mails.
func (m *Module[T]) DeleteRead(ctx context.Context, rootID int64, req *mailpb.MceMailDeleteRead) *mailpb.MseMailDeleteRead {
	if req == nil {
		req = &mailpb.MceMailDeleteRead{}
	}
	deleted, err := m.mailbox.DeleteRead(ctx, rootID, req.GetMailIdList())
	if err != nil {
		return &mailpb.MseMailDeleteRead{Ret: mail.RetInternal, Message: err.Error()}
	}
	return &mailpb.MseMailDeleteRead{Ret: mail.RetOK, MailIdList: deleted}
}

func (m *Module[T]) groupKeys(ctx context.Context, root *runtimeroot.Root[T], requestGroups []string) ([]string, error) {
	seen := make(map[string]struct{}, len(requestGroups))
	out := make([]string, 0, len(requestGroups))
	for _, groupKey := range requestGroups {
		if groupKey == "" {
			continue
		}
		if _, ok := seen[groupKey]; ok {
			continue
		}
		seen[groupKey] = struct{}{}
		out = append(out, groupKey)
	}
	if root != nil && m.groupResolver != nil {
		resolved, err := m.groupResolver.MailGroupKeys(ctx, root)
		if err != nil {
			return nil, err
		}
		for _, groupKey := range resolved {
			if groupKey == "" {
				continue
			}
			if _, ok := seen[groupKey]; ok {
				continue
			}
			seen[groupKey] = struct{}{}
			out = append(out, groupKey)
		}
	}
	sort.Strings(out)
	return out, nil
}

func (m *Module[T]) nowUnix() int64 {
	return m.clock.Now().Unix()
}

func (m *Module[T]) record(ctx context.Context, name string, rootID int64) {
	if m == nil || m.observe == nil {
		return
	}
	_ = m.observe.Write(ctx, []observe.Event{observe.Metric(name, 1, map[string]string{
		"root_id": strconv.FormatInt(rootID, 10),
	})})
}

func hasAttachment(data *mailpb.MailData) bool {
	return data.GetBase() != nil && len(data.GetBase().GetItemList()) > 0
}
