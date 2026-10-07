// Package store contains persistence contracts and adapters for the mail plugin.
package store

import (
	"context"
	"errors"

	"github.com/li9chuan/aifirstdev/aifd-plugins/mail/generated/mailpb"
)

// ErrVersionConflict indicates that a mailbox changed after it was loaded.
var ErrVersionConflict = errors.New("mailbox version conflict")

// PersonalMailbox is the durable state loaded for one user.
type PersonalMailbox struct {
	Version uint64
	Meta    *mailpb.MailboxMeta
	Mails   []*mailpb.MailData
}

// PersonalMailboxChange is one atomic, version-checked mailbox mutation.
type PersonalMailboxChange struct {
	Upsert        []*mailpb.MailData
	DeleteIDs     []int64
	Meta          *mailpb.MailboxMeta
	MailCount     int
	DataExpiresAt int64
}

// Store is the mail plugin durable persistence contract. Mailbox behavior is
// owned by mailbox.Manager; adapters only load and atomically apply state.
type Store interface {
	PersonalMailboxVersion(context.Context, int64) (uint64, error)
	LoadPersonalMailbox(context.Context, int64) (*PersonalMailbox, error)
	ApplyPersonalMailbox(context.Context, int64, uint64, PersonalMailboxChange) (uint64, error)

	SaveShared(context.Context, *mailpb.MailData) error
	ListGlobalAfter(context.Context, int64, int32, int) ([]*mailpb.MailData, error)
	ListGroupAfter(context.Context, string, int64, int) ([]*mailpb.MailData, error)
	CleanupSharedExpired(context.Context, int64) (int, error)
}

// SummaryMail strips payloads from an independent copy for list/push consumers.
func SummaryMail(data *mailpb.MailData) *mailpb.MailData {
	out := CloneMail(data)
	if out == nil || out.Base == nil {
		return out
	}
	if custom := out.Base.CustomContent; custom != nil {
		preview := []rune(custom.Body)
		if len(preview) > 160 {
			preview = preview[:160]
		}
		out.Preview = string(preview)
		custom.Body = ""
	}
	out.Base.ContentList = nil
	return out
}

// ClonePersonalMailbox returns a deep copy of durable mailbox state.
func ClonePersonalMailbox(box *PersonalMailbox) *PersonalMailbox {
	if box == nil {
		return &PersonalMailbox{Meta: &mailpb.MailboxMeta{}}
	}
	out := &PersonalMailbox{
		Version: box.Version,
		Meta:    CloneMeta(box.Meta),
		Mails:   make([]*mailpb.MailData, 0, len(box.Mails)),
	}
	for _, data := range box.Mails {
		if data != nil {
			out.Mails = append(out.Mails, CloneMail(data))
		}
	}
	return out
}

// CloneMail returns a protobuf deep copy that callers can safely mutate.
func CloneMail(data *mailpb.MailData) *mailpb.MailData {
	if data == nil {
		return nil
	}
	out := *data
	if data.Base != nil {
		base := *data.Base
		base.ContentList = append([]string(nil), data.Base.ContentList...)
		if data.Base.CustomContent != nil {
			base.CustomContent = &mailpb.MailCustomContent{
				Title: data.Base.CustomContent.GetTitle(),
				Body:  data.Base.CustomContent.GetBody(),
			}
		}
		base.ItemList = make([]*mailpb.MailItem, 0, len(data.Base.ItemList))
		for _, item := range data.Base.ItemList {
			if item == nil {
				continue
			}
			cp := *item
			base.ItemList = append(base.ItemList, &cp)
		}
		out.Base = &base
	}
	out.GroupKeys = append([]string(nil), data.GroupKeys...)
	out.TargetRealms = append([]int32(nil), data.TargetRealms...)
	return &out
}

// CloneMeta returns a deep copy of mailbox metadata.
func CloneMeta(meta *mailpb.MailboxMeta) *mailpb.MailboxMeta {
	if meta == nil {
		return &mailpb.MailboxMeta{}
	}
	out := *meta
	out.GroupCursors = make([]*mailpb.MailGroupCursor, 0, len(meta.GroupCursors))
	for _, cursor := range meta.GroupCursors {
		if cursor == nil {
			continue
		}
		cp := *cursor
		out.GroupCursors = append(out.GroupCursors, &cp)
	}
	return &out
}
