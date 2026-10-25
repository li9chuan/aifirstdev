package mailbox

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	mail "github.com/li9chuan/aifirstdev/aifd-plugins/mail"
	"github.com/li9chuan/aifirstdev/aifd-plugins/mail/generated/mailpb"
)

func validGroupKey(key string) bool { return strings.TrimSpace(key) != "" && utf8.ValidString(key) }

// Normalize only the published v0.4.2 storage format. Multi-group or realm-
// restricted shared mail cannot be flattened without changing its recipients.
func normalizeStoredGroup(data *mailpb.MailData) error {
	switch data.GetType() {
	case mailpb.MailType_MAIL_TYPE_PERSONAL:
		if data.GetGroupKey() != "" {
			return fmt.Errorf("personal mail cannot target a group")
		}
		// Legacy PERSONAL recipient snapshots did not apply these restrictions.
	case mailpb.MailType_MAIL_TYPE_GLOBAL:
		if data.GetGroupKey() != "" || len(data.GetTargetRealms()) > 0 {
			return fmt.Errorf("legacy restricted global mail requires explicit conversion")
		}
	case mailpb.MailType_MAIL_TYPE_GROUP:
		if len(data.GetTargetRealms()) > 0 {
			return fmt.Errorf("legacy realm-restricted group mail requires explicit conversion")
		}
		key := data.GetGroupKey()
		for _, legacy := range data.GetGroupKeys() {
			if !validGroupKey(legacy) {
				return fmt.Errorf("legacy group key is invalid")
			}
			if key == "" {
				key = legacy
			} else if key != legacy {
				return fmt.Errorf("legacy multi-group mail requires separate sends")
			}
		}
		if !validGroupKey(key) {
			return fmt.Errorf("group mail requires one group_key")
		}
		data.GroupKey = key
	default:
		return mail.ErrInvalid
	}
	data.GroupKeys = nil
	data.TargetRealms = nil
	return nil
}

func indexShared(state *sharedState, data *mailpb.MailData) {
	if data.GetType() == mailpb.MailType_MAIL_TYPE_GLOBAL {
		state.global = append(state.global, data)
	} else {
		state.groups[data.GetGroupKey()] = append(state.groups[data.GetGroupKey()], data)
	}
}

func matchGroups(data *mailpb.MailData, groups []string) bool {
	switch data.GetType() {
	case mailpb.MailType_MAIL_TYPE_PERSONAL, mailpb.MailType_MAIL_TYPE_GLOBAL:
		return true
	case mailpb.MailType_MAIL_TYPE_GROUP:
		if data.GetGroupKey() == "" {
			return false
		}
		for _, group := range groups {
			if group == data.GetGroupKey() {
				return true
			}
		}
	}
	return false
}

type sharedSnapshot struct {
	global []*mailpb.MailData
	groups map[string][]*mailpb.MailData
}

// Payloads and targets are immutable. List only needs the global stream and the
// caller's current groups, never the complete shared pool or cloned bodies.
func (m *Manager) snapshotShared(groups []string) sharedSnapshot {
	m.shared.mu.RLock()
	defer m.shared.mu.RUnlock()
	out := sharedSnapshot{groups: make(map[string][]*mailpb.MailData, len(groups))}
	now := m.Clock.Now().Unix()
	for _, data := range m.shared.global {
		if !expired(data, now) {
			out.global = append(out.global, data)
		}
	}
	for _, group := range groups {
		if _, exists := out.groups[group]; exists {
			continue
		}
		out.groups[group] = nil
		for _, data := range m.shared.groups[group] {
			if !expired(data, now) {
				out.groups[group] = append(out.groups[group], data)
			}
		}
	}
	return out
}

func (m *Manager) PublishShared(ctx context.Context, req *mailpb.MailAddRequest) (*mailpb.MailData, error) {
	if err := m.ready(); err != nil {
		return nil, err
	}
	if len(req.GetGroupKeys()) > 0 || len(req.GetTargetRealms()) > 0 {
		return nil, fmt.Errorf("%w: use one group_key, not legacy targeting fields", mail.ErrInvalid)
	}
	switch req.GetType() {
	case mailpb.MailType_MAIL_TYPE_GLOBAL:
		if req.GetGroupKey() != "" {
			return nil, mail.ErrInvalid
		}
	case mailpb.MailType_MAIL_TYPE_GROUP:
		if !validGroupKey(req.GetGroupKey()) {
			return nil, mail.ErrInvalid
		}
	default:
		return nil, mail.ErrInvalid
	}
	m.shared.mu.Lock()
	defer m.shared.mu.Unlock()
	data, err := m.newMail(req)
	if err != nil {
		return nil, err
	}
	if data.GetMailId() <= m.shared.maxMailID {
		return nil, fmt.Errorf("mail ID must exceed recovered/published shared maximum")
	}
	entry, err := m.bindMail(m.sharedKey(), data)
	if err != nil {
		return nil, err
	}
	if err := entry.Save(ctx); err != nil {
		return nil, err
	}
	m.shared.mails[data.GetMailId()] = entry
	indexShared(&m.shared, data)
	m.shared.maxMailID = data.GetMailId()
	return cloneMail(data), nil
}

func (m *Manager) SyncShared(ctx context.Context, uid int64, groups []string) error {
	box, release, err := m.acquire(ctx, uid)
	if err != nil {
		return err
	}
	defer release()
	return m.syncShared(ctx, uid, box, groups, m.snapshotShared(groups))
}

func (m *Manager) syncShared(ctx context.Context, uid int64, box *mailboxEntry, groups []string, snapshot sharedSnapshot) error {
	box.mu.Lock()
	defer box.mu.Unlock()
	box.meta.Lock()
	defer box.meta.Unlock()
	meta := box.meta.DbProto
	cursors := make(map[string]*mailpb.MailGroupCursor, len(meta.GetGroupCursors()))
	for _, cursor := range meta.GetGroupCursors() {
		cursors[cursor.GetGroupKey()] = cursor
	}
	keys := make([]string, 0, len(groups))
	selected := make(map[string]bool, len(groups))
	for _, group := range groups {
		if selected[group] {
			continue
		}
		selected[group] = true
		mails := snapshot.groups[group]
		var after int64
		if cursor := cursors[group]; cursor != nil {
			after = cursor.GetLastMailId()
		}
		if len(mails) > 0 && mails[len(mails)-1].GetMailId() > after {
			keys = append(keys, group)
		}
	}
	syncGlobal := len(snapshot.global) > 0 && snapshot.global[len(snapshot.global)-1].GetMailId() > meta.GetLastGlobalMailId()
	if !syncGlobal && len(keys) == 0 {
		return m.normalizeLocked(ctx, box)
	}
	// Worker serialization waits for the meta lock and sees the accepted prefix.
	if err := box.meta.SaveUnsafe(ctx); err != nil {
		return err
	}
	if syncGlobal {
		if err := m.importShared(ctx, uid, box, snapshot.global, &meta.LastGlobalMailId); err != nil {
			return err
		}
	}
	sort.Strings(keys)
	for _, group := range keys {
		cursor := cursors[group]
		if cursor == nil {
			cursor = &mailpb.MailGroupCursor{GroupKey: group}
			meta.GroupCursors = append(meta.GroupCursors, cursor)
		}
		if err := m.importShared(ctx, uid, box, snapshot.groups[group], &cursor.LastMailId); err != nil {
			return err
		}
	}
	// Keep progress across departure, empty groups and stale snapshots. Clearing a
	// cursor based on an old source snapshot could resurrect a newer deleted mail.
	return m.normalizeLocked(ctx, box)
}

func (m *Manager) importShared(ctx context.Context, uid int64, box *mailboxEntry, mails []*mailpb.MailData, after *int64) error {
	for _, source := range mails {
		if source.GetMailId() <= *after {
			continue
		}
		if !expired(source, m.Clock.Now().Unix()) && box.mails[source.GetMailId()] == nil {
			data := cloneMail(source)
			data.Read = false
			data.Claimed = false
			data.ClaimRetryAtUnixMs = 0
			entry, err := m.bindMail(m.userKey(uid), data)
			if err != nil {
				return err
			}
			if err := entry.Save(ctx); err != nil {
				return err
			}
			box.mails[data.GetMailId()] = entry
		}
		*after = source.GetMailId()
	}
	return nil
}

func (m *Manager) CleanupExpired(ctx context.Context, nowUnix int64) error {
	if err := m.ready(); err != nil {
		return err
	}
	m.shared.mu.Lock()
	for id, entry := range m.shared.mails {
		if expired(entry.DbProto, nowUnix) {
			if !entry.Delete(ctx) {
				m.shared.mu.Unlock()
				return fmt.Errorf("mail shared delete was rejected")
			}
			delete(m.shared.mails, id)
		}
	}
	m.shared.global = m.keepShared(m.shared.global)
	for group, mails := range m.shared.groups {
		kept := m.keepShared(mails)
		if len(kept) == 0 {
			delete(m.shared.groups, group)
		} else {
			m.shared.groups[group] = kept
		}
	}
	m.shared.mu.Unlock()
	m.cacheMu.Lock()
	uids := make([]int64, 0, len(m.cache))
	for uid := range m.cache {
		uids = append(uids, uid)
	}
	m.cacheMu.Unlock()
	for _, uid := range uids {
		box, release := m.pinCached(uid)
		if box == nil {
			continue
		}
		box.mu.Lock()
		err := m.normalizeLocked(ctx, box)
		box.mu.Unlock()
		release()
		if err != nil {
			return err
		}
	}
	return m.evictIdle(ctx, m.Clock.Now())
}

// Caller holds the shared write lock.
func (m *Manager) keepShared(mails []*mailpb.MailData) []*mailpb.MailData {
	kept := mails[:0]
	for _, data := range mails {
		if m.shared.mails[data.GetMailId()] != nil {
			kept = append(kept, data)
		}
	}
	clear(mails[len(kept):])
	return kept
}
