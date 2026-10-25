package mailbox

import (
	"context"
	"fmt"
	"sync"
	"time"

	mail "github.com/li9chuan/aifirstdev/aifd-plugins/mail"
	"github.com/li9chuan/aifirstdev/aifd-plugins/mail/generated/mailpb"
	rdb "github.com/li9chuan/aifirstdev/aifd-rdb"
)

// acquire pins the canonical object until release, including award RPC time.
// A slot stays discoverable while loading or draining for eviction.
func (m *Manager) acquire(ctx context.Context, uid int64) (*mailboxEntry, func(), error) {
	if uid <= 0 {
		return nil, nil, mail.ErrInvalid
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		m.cacheMu.Lock()
		if m.closed || !m.recovered {
			err := mail.ErrNotReady
			if m.closed {
				err = mail.ErrClosed
			}
			m.cacheMu.Unlock()
			return nil, nil, err
		}
		slot := m.cache[uid]
		if slot != nil && slot.evicting != nil {
			wait := slot.evicting
			m.cacheMu.Unlock()
			select {
			case <-wait:
				continue
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			}
		}
		if slot != nil && slot.box != nil {
			slot.refs++
			slot.lastAccess = m.Clock.Now()
			m.cacheMu.Unlock()
			var once sync.Once
			release := func() {
				once.Do(func() { m.cacheMu.Lock(); slot.refs--; slot.lastAccess = m.Clock.Now(); m.cacheMu.Unlock() })
			}
			slot.box.mu.Lock()
			err := m.normalizeLocked(ctx, slot.box)
			slot.box.mu.Unlock()
			if err != nil {
				release()
				return nil, nil, err
			}
			return slot.box, release, nil
		}
		if slot == nil {
			if len(m.cache) >= m.CacheCapacity {
				candidateID, candidate := m.oldestIdleLocked()
				m.cacheMu.Unlock()
				if candidate == nil {
					return nil, nil, mail.ErrCacheCapacity
				}
				if err := m.evict(ctx, candidateID, candidate); err != nil {
					return nil, nil, err
				}
				continue
			}
			slot = &cacheSlot{ready: make(chan struct{}), lastAccess: m.Clock.Now()}
			m.cache[uid] = slot
			m.loads.Add(1)
			go m.loadSlot(uid, slot)
		}
		m.cacheMu.Unlock()
		select {
		case <-slot.ready:
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		}
		m.cacheMu.Lock()
		err := slot.err
		m.cacheMu.Unlock()
		if err != nil {
			return nil, nil, err
		}
	}
}

func (m *Manager) loadSlot(uid int64, slot *cacheSlot) {
	defer m.loads.Done()
	ctx, cancel := context.WithTimeout(m.loadCtx, loadTimeout)
	defer cancel()
	box, err := m.loadMailbox(ctx, uid)
	m.cacheMu.Lock()
	slot.box, slot.err = box, err
	if err != nil {
		delete(m.cache, uid)
	}
	close(slot.ready)
	m.cacheMu.Unlock()
}

func (m *Manager) loadMailbox(ctx context.Context, uid int64) (*mailboxEntry, error) {
	// Clear legacy whole-key expiry before taking a payload snapshot. Clearing it
	// afterwards could publish bytes from a key that expired during this load.
	if err := m.DB.Persist(ctx, m.userKey(uid)); err != nil {
		return nil, err
	}
	values, err := rdb.LoadHash(ctx, m.DB, m.userKey(uid), func() *mailpb.MailData { return &mailpb.MailData{} })
	if err != nil {
		return nil, fmt.Errorf("mailbox %d load: %w", uid, err)
	}
	box := &mailboxEntry{mails: make(map[int64]*mailEntry), claiming: make(map[int64]struct{})}
	if err := box.meta.BindDB(m.DB, m.metaKey(uid), "meta", &mailpb.MailboxMeta{}); err != nil {
		return nil, err
	}
	if _, err := box.meta.Load(ctx); err != nil {
		return nil, fmt.Errorf("mailbox %d meta: %w", uid, err)
	}
	if box.meta.DbProto.GetLastGlobalMailId() < 0 {
		return nil, fmt.Errorf("mailbox %d has a negative legacy cursor", uid)
	}
	if len(box.meta.DbProto.ProtoReflect().GetUnknown()) > 0 {
		return nil, fmt.Errorf("mailbox %d contains unsupported metadata; convert the unreleased combination-cursor draft before loading", uid)
	}
	seenGroups := make(map[string]bool)
	for _, cursor := range box.meta.DbProto.GetGroupCursors() {
		if !validGroupKey(cursor.GetGroupKey()) || cursor.GetLastMailId() < 0 || seenGroups[cursor.GetGroupKey()] {
			return nil, fmt.Errorf("mailbox %d has an invalid legacy group cursor", uid)
		}
		seenGroups[cursor.GetGroupKey()] = true
	}
	for field, data := range values {
		if err := validateStored(field, data); err != nil {
			return nil, err
		}
		if !expired(data, m.Clock.Now().Unix()) {
			if err := normalizeStoredGroup(data); err != nil {
				return nil, fmt.Errorf("mailbox %d mail %d: %w", uid, data.GetMailId(), err)
			}
			// v0.4.2 stored shared copies as PERSONAL. Restore their immutable
			// target once so every hot-path operation can check the copy directly.
			if data.GetType() == mailpb.MailType_MAIL_TYPE_PERSONAL {
				m.shared.mu.RLock()
				if source := m.shared.mails[data.GetMailId()]; source != nil && !expired(source.DbProto, m.Clock.Now().Unix()) {
					data.Type = source.DbProto.GetType()
					data.GroupKey = source.DbProto.GetGroupKey()
				}
				m.shared.mu.RUnlock()
			}
		}
		entry, err := m.bindMail(m.userKey(uid), data)
		if err != nil {
			return nil, err
		}
		box.mails[data.GetMailId()] = entry
	}
	return box, nil
}

func (m *Manager) oldestIdleLocked() (int64, *cacheSlot) {
	var id int64
	var oldest *cacheSlot
	for uid, slot := range m.cache {
		if slot.box == nil || slot.refs != 0 || slot.evicting != nil {
			continue
		}
		if oldest == nil || slot.lastAccess.Before(oldest.lastAccess) {
			id, oldest = uid, slot
		}
	}
	return id, oldest
}

func (m *Manager) evict(ctx context.Context, uid int64, slot *cacheSlot) error {
	m.cacheMu.Lock()
	if m.cache[uid] != slot || slot.refs != 0 || slot.evicting != nil {
		m.cacheMu.Unlock()
		return mail.ErrCacheCapacity
	}
	wait := make(chan struct{})
	slot.evicting = wait
	m.cacheMu.Unlock()
	flushCtx, cancel := context.WithTimeout(ctx, loadTimeout)
	err := m.DB.Flush(flushCtx)
	cancel()
	m.cacheMu.Lock()
	if err == nil {
		delete(m.cache, uid)
	}
	slot.evicting = nil
	close(wait)
	m.cacheMu.Unlock()
	return err
}

func (m *Manager) evictIdle(ctx context.Context, now time.Time) error {
	for {
		m.cacheMu.Lock()
		uid, slot := m.oldestIdleLocked()
		if slot == nil || now.Sub(slot.lastAccess) < m.CacheTTL {
			m.cacheMu.Unlock()
			return nil
		}
		m.cacheMu.Unlock()
		if err := m.evict(ctx, uid, slot); err != nil {
			return err
		}
	}
}

// pinCached never causes a concurrently evicted mailbox to reload for cleanup.
func (m *Manager) pinCached(uid int64) (*mailboxEntry, func()) {
	m.cacheMu.Lock()
	defer m.cacheMu.Unlock()
	slot := m.cache[uid]
	if slot == nil || slot.box == nil || slot.evicting != nil {
		return nil, func() {}
	}
	slot.refs++
	return slot.box, func() { m.cacheMu.Lock(); slot.refs--; m.cacheMu.Unlock() }
}
