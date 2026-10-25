// Package user authenticates mailbox clients and forwards trusted requests to
// the sole mail owner. It owns no mailbox storage, claim state or award logic.
package user

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	base "github.com/li9chuan/aifirstdev/aifd-contracts"
	mail "github.com/li9chuan/aifirstdev/aifd-plugins/mail"
	"github.com/li9chuan/aifirstdev/aifd-plugins/mail/generated/mailpb"
	rpc "github.com/li9chuan/aifirstdev/aifd-rpc"
	runtimeuser "github.com/li9chuan/aifirstdev/aifd-runtime/user"
	"google.golang.org/protobuf/proto"
)

type GroupResolver[T proto.Message] interface {
	MailGroupKeys(context.Context, *runtimeuser.User[T]) ([]string, error)
}

type GroupResolverFunc[T proto.Message] func(context.Context, *runtimeuser.User[T]) ([]string, error)

func (f GroupResolverFunc[T]) MailGroupKeys(ctx context.Context, owner *runtimeuser.User[T]) ([]string, error) {
	if f == nil {
		return nil, nil
	}
	return f(ctx, owner)
}

type Options[T proto.Message] struct {
	GroupResolver GroupResolver[T]
}

type Module[T proto.Message] struct {
	resolver  GroupResolver[T]
	moduleCtx runtimeuser.ModuleContext[T]
}

func NewModule[T proto.Message](opts Options[T]) *Module[T] {
	return &Module[T]{resolver: opts.GroupResolver}
}

func (m *Module[T]) Descriptor() base.ModuleDescriptor {
	out := base.ModuleDescriptor{ID: "aifd.plugins.mail.user", Version: "v0.2.0", Kind: base.ModuleKindUser}
	for _, topic := range []string{mail.TopicMceMailList, mail.TopicMceMailGet, mail.TopicMceMailRead, mail.TopicMceMailClaim, mail.TopicMceMailDeleteRead} {
		out.Provides = append(out.Provides, base.Capability{Kind: "rpc", Name: topic})
	}
	return out
}

func (m *Module[T]) BindModuleContext(ctx runtimeuser.ModuleContext[T]) error {
	if m == nil {
		return fmt.Errorf("mail user module is nil")
	}
	m.moduleCtx = ctx
	return nil
}

func (m *Module[T]) OnLoad(context.Context, *runtimeuser.User[T]) error { return nil }

func (m *Module[T]) RegisterRPC(_ *rpc.Registry) error {
	rt := m.moduleCtx.Runtime()
	if rt == nil {
		return fmt.Errorf("mail user runtime is missing")
	}
	if err := rt.Client(mail.TopicMceMailList, func(s *rpc.Session, owner *runtimeuser.User[T], req *mailpb.MceMailList) {
		m.forward(s, owner, mail.TopicMailList, func(a []string) proto.Message {
			// Never forward client group claims, even when a resolver is installed.
			query := &mailpb.MceMailList{AfterMailId: req.GetAfterMailId(), Limit: req.GetLimit(), SummaryOnly: req.GetSummaryOnly()}
			return &mailpb.MailListRequest{GroupKeys: a, Query: query}
		})
	}); err != nil {
		return err
	}
	if err := rt.Client(mail.TopicMceMailGet, func(s *rpc.Session, owner *runtimeuser.User[T], req *mailpb.MceMailGet) {
		m.forward(s, owner, mail.TopicMailGet, func(a []string) proto.Message {
			return &mailpb.MailGetRequest{GroupKeys: a, MailId: req.GetMailId()}
		})
	}); err != nil {
		return err
	}
	if err := rt.Client(mail.TopicMceMailRead, func(s *rpc.Session, owner *runtimeuser.User[T], req *mailpb.MceMailRead) {
		m.forward(s, owner, mail.TopicMailRead, func(a []string) proto.Message {
			return &mailpb.MailIDsRequest{GroupKeys: a, MailIdList: append([]int64(nil), req.GetMailIdList()...)}
		})
	}); err != nil {
		return err
	}
	if err := rt.Client(mail.TopicMceMailClaim, func(s *rpc.Session, owner *runtimeuser.User[T], req *mailpb.MceMailClaim) {
		m.forward(s, owner, mail.TopicMailClaim, func(a []string) proto.Message {
			return &mailpb.MailIDsRequest{GroupKeys: a, MailIdList: append([]int64(nil), req.GetMailIdList()...)}
		})
	}); err != nil {
		return err
	}
	return rt.Client(mail.TopicMceMailDeleteRead, func(s *rpc.Session, owner *runtimeuser.User[T], req *mailpb.MceMailDeleteRead) {
		m.forward(s, owner, mail.TopicMailDeleteRead, func(a []string) proto.Message {
			return &mailpb.MailIDsRequest{GroupKeys: a, MailIdList: append([]int64(nil), req.GetMailIdList()...)}
		})
	})
}

func (m *Module[T]) groupKeys(ctx context.Context, owner *runtimeuser.User[T]) ([]string, error) {
	if m.resolver == nil {
		return nil, nil
	}
	groups, err := m.resolver.MailGroupKeys(ctx, owner)
	if err != nil {
		return nil, err
	}
	for _, group := range groups {
		if strings.TrimSpace(group) == "" || !utf8.ValidString(group) {
			return nil, fmt.Errorf("mail group is invalid")
		}
	}
	return append([]string(nil), groups...), nil
}

func (m *Module[T]) forward(s *rpc.Session, owner *runtimeuser.User[T], topic string, request func([]string) proto.Message) {
	groups, err := m.groupKeys(s.Context(), owner)
	if err != nil {
		s.Error(rpc.CodeInternal, err.Error())
		return
	}
	// Forward releases the caller's UID worker before Mail can call Game for awards.
	if result := s.Forward(topic, request(groups)); !result.OK() {
		s.Error(result.Code(), result.Message())
	}
}
