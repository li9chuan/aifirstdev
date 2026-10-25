package mailbox

import (
	"context"
	"fmt"
	"sort"

	mail "github.com/li9chuan/aifirstdev/aifd-plugins/mail"
	"github.com/li9chuan/aifirstdev/aifd-plugins/mail/generated/mailpb"
)

func (m *Manager) AddPersonal(ctx context.Context, uid int64, req *mailpb.MailAddRequest) (*mailpb.MailData, error) {
	box, release, err := m.acquire(ctx, uid)
	if err != nil {
		return nil, err
	}
	defer release()
	box.mu.Lock()
	defer box.mu.Unlock()
	data, err := m.newMail(req)
	if err != nil {
		return nil, err
	}
	if box.mails[data.GetMailId()] != nil {
		return nil, fmt.Errorf("mail ID already exists in mailbox")
	}
	entry, err := m.bindMail(m.userKey(uid), data)
	if err != nil {
		return nil, err
	}
	if err := entry.Save(ctx); err != nil {
		return nil, err
	}
	box.mails[data.GetMailId()] = entry
	if err := m.normalizeLocked(ctx, box); err != nil {
		return cloneMail(data), err
	}
	return cloneMail(data), nil
}

// Copies retain the immutable target; legacy copies are restored during load.
func visible(data *mailpb.MailData, groups []string, now int64) bool {
	if data == nil || expired(data, now) {
		return false
	}
	return matchGroups(data, groups)
}

func (m *Manager) List(ctx context.Context, uid int64, groups []string, after int64, limit int) ([]*mailpb.MailData, error) {
	box, release, err := m.acquire(ctx, uid)
	if err != nil {
		return nil, err
	}
	defer release()
	snapshot := m.snapshotShared(groups)
	if err := m.syncShared(ctx, uid, box, groups, snapshot); err != nil {
		return nil, err
	}
	box.mu.RLock()
	defer box.mu.RUnlock()
	ids := make([]int64, 0, len(box.mails))
	now := m.Clock.Now().Unix()
	for id, entry := range box.mails {
		if id > after && visible(entry.DbProto, groups, now) {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	if limit <= 0 || limit > m.MailboxLimit {
		limit = m.MailboxLimit
	}
	if len(ids) > limit {
		ids = ids[:limit]
	}
	out := make([]*mailpb.MailData, 0, len(ids))
	for _, id := range ids {
		out = append(out, cloneMail(box.mails[id].DbProto))
	}
	return out, nil
}

func (m *Manager) Get(ctx context.Context, uid int64, groups []string, id int64) (*mailpb.MailData, bool, error) {
	if id <= 0 {
		return nil, false, mail.ErrInvalid
	}
	box, release, err := m.acquire(ctx, uid)
	if err != nil {
		return nil, false, err
	}
	defer release()
	box.mu.RLock()
	defer box.mu.RUnlock()
	entry := box.mails[id]
	if entry == nil || !visible(entry.DbProto, groups, m.Clock.Now().Unix()) {
		return nil, false, nil
	}
	return cloneMail(entry.DbProto), true, nil
}

func (m *Manager) MarkRead(ctx context.Context, uid int64, groups []string, ids []int64) ([]int64, error) {
	if err := validateIDs(ids); err != nil {
		return nil, err
	}
	box, release, err := m.acquire(ctx, uid)
	if err != nil {
		return nil, err
	}
	defer release()
	box.mu.Lock()
	defer box.mu.Unlock()
	out := []int64{}
	for _, id := range uniqueIDs(ids) {
		e := box.mails[id]
		if e == nil || !visible(e.DbProto, groups, m.Clock.Now().Unix()) || e.DbProto.GetRead() || (hasAttachment(e.DbProto) && !e.DbProto.GetClaimed()) {
			continue
		}
		e.Lock()
		e.DbProto.Read = true
		err := e.SaveUnsafe(ctx)
		if err != nil {
			e.DbProto.Read = false
		}
		e.Unlock()
		if err != nil {
			return out, err
		}
		out = append(out, id)
	}
	return out, nil
}

func (m *Manager) DeleteRead(ctx context.Context, uid int64, groups []string, ids []int64) ([]int64, error) {
	if err := validateIDs(ids); err != nil {
		return nil, err
	}
	box, release, err := m.acquire(ctx, uid)
	if err != nil {
		return nil, err
	}
	defer release()
	box.mu.Lock()
	defer box.mu.Unlock()
	if len(ids) == 0 {
		for id := range box.mails {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	}
	out := []int64{}
	for _, id := range uniqueIDs(ids) {
		e := box.mails[id]
		_, claiming := box.claiming[id]
		if e == nil || claiming || !e.DbProto.GetRead() || (hasAttachment(e.DbProto) && !e.DbProto.GetClaimed()) || !visible(e.DbProto, groups, m.Clock.Now().Unix()) {
			continue
		}
		if !e.Delete(ctx) {
			return out, fmt.Errorf("mail delete was rejected")
		}
		delete(box.mails, id)
		out = append(out, id)
	}
	return out, nil
}

func (m *Manager) normalizeLocked(ctx context.Context, box *mailboxEntry) error {
	if len(box.claiming) > 0 {
		return nil
	}
	ids := make([]int64, 0, len(box.mails))
	now := m.Clock.Now().Unix()
	for id, e := range box.mails {
		if expired(e.DbProto, now) {
			if !e.Delete(ctx) {
				return fmt.Errorf("mail expiry delete was rejected")
			}
			delete(box.mails, id)
		} else {
			ids = append(ids, id)
		}
	}
	if len(ids) > m.MailboxLimit {
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		for _, id := range ids[:len(ids)-m.MailboxLimit] {
			if !box.mails[id].Delete(ctx) {
				return fmt.Errorf("mail capacity delete was rejected")
			}
			delete(box.mails, id)
		}
	}
	return nil
}

func validateIDs(ids []int64) error {
	for _, id := range ids {
		if id <= 0 {
			return mail.ErrInvalid
		}
	}
	return nil
}
func uniqueIDs(ids []int64) []int64 {
	out := make([]int64, 0, len(ids))
	seen := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; !ok {
			out = append(out, id)
			seen[id] = struct{}{}
		}
	}
	return out
}
