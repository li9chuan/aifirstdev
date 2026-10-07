// Package mailbox owns process-local mailbox behavior and caching.
package mailbox

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	mail "github.com/li9chuan/aifirstdev/aifd-plugins/mail"
	"github.com/li9chuan/aifirstdev/aifd-plugins/mail/generated/mailpb"
	"github.com/li9chuan/aifirstdev/aifd-plugins/mail/store"
	"github.com/jellydator/ttlcache/v3"
	"google.golang.org/protobuf/proto"
)

const (
	DefaultMailboxLimit  = 100
	DefaultCacheTTL      = 20 * time.Minute
	DefaultCacheCapacity = 10_000

	loadTimeout  = 5 * time.Second
	loadErrorTTL = time.Second
)

// Options configures one process-local mailbox manager. Zero values select
// defaults; values are not capped by the framework.
type Options struct {
	Store         store.Store
	Clock         mail.Clock
	MailboxLimit  int
	CacheTTL      time.Duration
	CacheCapacity int
}

type mailboxEntry struct {
	mu      sync.RWMutex
	version uint64
	meta    *mailpb.MailboxMeta
	mails   map[int64]*mailpb.MailData
	synced  map[string]struct{}
	loadErr error
}

// Manager dynamically loads and caches one bounded mailbox per user.
type Manager struct {
	store        store.Store
	clock        mail.Clock
	mailboxLimit int
	cacheTTL     time.Duration
	cache        *ttlcache.Cache[int64, *mailboxEntry]
	loadCtx      context.Context
	cancelLoad   context.CancelFunc
	lifecycleMu  sync.Mutex
	started      bool
	closed       bool
}

// NewManager creates a manager without starting background cache cleanup.
func NewManager(opts Options) (*Manager, error) {
	if opts.Store == nil {
		return nil, fmt.Errorf("mailbox store is nil")
	}
	if opts.Clock == nil {
		opts.Clock = mail.SystemClock{}
	}
	if opts.MailboxLimit == 0 {
		opts.MailboxLimit = DefaultMailboxLimit
	}
	if opts.MailboxLimit < 0 {
		return nil, fmt.Errorf("mailbox limit cannot be negative")
	}
	if opts.CacheTTL == 0 {
		opts.CacheTTL = DefaultCacheTTL
	}
	if opts.CacheTTL < 0 && opts.CacheTTL != ttlcache.NoTTL {
		return nil, fmt.Errorf("mailbox cache ttl is invalid")
	}
	if opts.CacheCapacity == 0 {
		opts.CacheCapacity = DefaultCacheCapacity
	}
	if opts.CacheCapacity < -1 {
		return nil, fmt.Errorf("mailbox cache capacity is invalid")
	}

	loadCtx, cancel := context.WithCancel(context.Background())
	m := &Manager{
		store:        opts.Store,
		clock:        opts.Clock,
		mailboxLimit: opts.MailboxLimit,
		cacheTTL:     opts.CacheTTL,
		loadCtx:      loadCtx,
		cancelLoad:   cancel,
	}
	loader := ttlcache.LoaderFunc[int64, *mailboxEntry](m.loadMailbox)
	cacheOpts := []ttlcache.Option[int64, *mailboxEntry]{
		ttlcache.WithTTL[int64, *mailboxEntry](opts.CacheTTL),
		ttlcache.WithLoader[int64, *mailboxEntry](ttlcache.NewSuppressedLoader[int64, *mailboxEntry](loader, nil)),
	}
	if opts.CacheCapacity > 0 {
		cacheOpts = append(cacheOpts, ttlcache.WithCapacity[int64, *mailboxEntry](uint64(opts.CacheCapacity)))
	}
	m.cache = ttlcache.New[int64, *mailboxEntry](cacheOpts...)
	return m, nil
}

// Start starts ttlcache expiration cleanup. It is idempotent.
func (m *Manager) Start() error {
	if m == nil {
		return fmt.Errorf("mailbox manager is nil")
	}
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()
	if m.closed {
		return fmt.Errorf("mailbox manager is closed")
	}
	if m.started {
		return nil
	}
	m.started = true
	go m.cache.Start()
	return nil
}

// Close stops cache cleanup and cancels in-flight dynamic loads.
func (m *Manager) Close() {
	if m == nil {
		return
	}
	m.lifecycleMu.Lock()
	if m.closed {
		m.lifecycleMu.Unlock()
		return
	}
	m.closed = true
	started := m.started
	m.lifecycleMu.Unlock()
	m.cancelLoad()
	if started {
		m.cache.Stop()
	}
}

// List returns live personal mail after a cursor from process-local state.
func (m *Manager) List(rootID int64, afterMailID int64, limit int) ([]*mailpb.MailData, error) {
	entry, err := m.get(rootID)
	if err != nil {
		return nil, err
	}
	now := m.nowUnix()
	entry.mu.RLock()
	ids := make([]int64, 0, len(entry.mails))
	for mailID, data := range entry.mails {
		if mailID > afterMailID && !expired(data, now) {
			ids = append(ids, mailID)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	if limit > 0 && len(ids) > limit {
		ids = ids[:limit]
	}
	out := make([]*mailpb.MailData, 0, len(ids))
	for _, mailID := range ids {
		out = append(out, store.CloneMail(entry.mails[mailID]))
	}
	entry.mu.RUnlock()
	return out, nil
}

// Get returns one live personal mail from process-local state.
func (m *Manager) Get(rootID int64, mailID int64) (*mailpb.MailData, bool, error) {
	entry, err := m.get(rootID)
	if err != nil {
		return nil, false, err
	}
	entry.mu.RLock()
	data := entry.mails[mailID]
	if data == nil || expired(data, m.nowUnix()) {
		entry.mu.RUnlock()
		return nil, false, nil
	}
	out := store.CloneMail(data)
	entry.mu.RUnlock()
	return out, true, nil
}

// Add persists a personal mail before committing it to process-local state.
func (m *Manager) Add(ctx context.Context, rootID int64, data *mailpb.MailData) error {
	if data == nil || data.GetMailId() <= 0 {
		return fmt.Errorf("personal mail is invalid")
	}
	return m.mutate(ctx, rootID, func(mails map[int64]*mailpb.MailData, _ *mailpb.MailboxMeta) bool {
		mails[data.GetMailId()] = store.CloneMail(data)
		return true
	})
}

// MarkRead marks eligible mails as read and returns the changed IDs.
func (m *Manager) MarkRead(ctx context.Context, rootID int64, mailIDs []int64) ([]int64, error) {
	var changed []int64
	err := m.mutate(ctx, rootID, func(mails map[int64]*mailpb.MailData, _ *mailpb.MailboxMeta) bool {
		changed = changed[:0]
		for _, mailID := range mailIDs {
			data := mails[mailID]
			if data == nil || data.GetRead() || (hasAttachment(data) && !data.GetClaimed()) {
				continue
			}
			data.Read = true
			changed = append(changed, mailID)
		}
		return len(changed) > 0
	})
	return append([]int64(nil), changed...), err
}

// MarkClaimed marks one mail read and claimed if it is still claimable.
func (m *Manager) MarkClaimed(ctx context.Context, rootID int64, mailID int64) (bool, error) {
	changed := false
	err := m.mutate(ctx, rootID, func(mails map[int64]*mailpb.MailData, _ *mailpb.MailboxMeta) bool {
		changed = false
		data := mails[mailID]
		if data == nil || data.GetClaimed() || !hasAttachment(data) {
			return false
		}
		data.Read = true
		data.Claimed = true
		changed = true
		return true
	})
	return changed, err
}

// DeleteRead deletes requested read mails, or every read mail for an empty list.
func (m *Manager) DeleteRead(ctx context.Context, rootID int64, mailIDs []int64) ([]int64, error) {
	var deleted []int64
	err := m.mutate(ctx, rootID, func(mails map[int64]*mailpb.MailData, _ *mailpb.MailboxMeta) bool {
		deleted = deleted[:0]
		if len(mailIDs) == 0 {
			for mailID, data := range mails {
				if data.GetRead() {
					deleted = append(deleted, mailID)
				}
			}
			sort.Slice(deleted, func(i, j int) bool { return deleted[i] < deleted[j] })
		} else {
			for _, mailID := range mailIDs {
				if data := mails[mailID]; data != nil && data.GetRead() {
					deleted = append(deleted, mailID)
				}
			}
		}
		for _, mailID := range deleted {
			delete(mails, mailID)
		}
		return len(deleted) > 0
	})
	return append([]int64(nil), deleted...), err
}

// SyncShared materializes new global and group mail into one personal mailbox.
func (m *Manager) SyncShared(ctx context.Context, rootID int64, realmID int32, groupKeys []string, limit int) error {
	entry, err := m.get(rootID)
	if err != nil {
		return err
	}
	groupKeys = append([]string(nil), groupKeys...)
	sort.Strings(groupKeys)
	syncKey := fmt.Sprintf("%d:%q", realmID, groupKeys)
	entry.mu.RLock()
	_, alreadySynced := entry.synced[syncKey]
	meta := store.CloneMeta(entry.meta)
	entry.mu.RUnlock()
	if alreadySynced {
		return nil
	}

	globalList, err := m.store.ListGlobalAfter(ctx, meta.GetLastGlobalMailId(), realmID, limit)
	if err != nil {
		return err
	}
	groupLists := make(map[string][]*mailpb.MailData, len(groupKeys))
	cursors := groupCursorMap(meta)
	for _, groupKey := range groupKeys {
		list, err := m.store.ListGroupAfter(ctx, groupKey, cursors[groupKey], limit)
		if err != nil {
			return err
		}
		groupLists[groupKey] = list
	}

	if err := m.mutate(ctx, rootID, func(mails map[int64]*mailpb.MailData, current *mailpb.MailboxMeta) bool {
		changed := false
		for _, data := range globalList {
			mails[data.GetMailId()] = personalCopy(data)
			if data.GetMailId() > current.GetLastGlobalMailId() {
				current.LastGlobalMailId = data.GetMailId()
			}
			changed = true
		}
		currentCursors := groupCursorMap(current)
		for groupKey, list := range groupLists {
			for _, data := range list {
				mails[data.GetMailId()] = personalCopy(data)
				if data.GetMailId() > currentCursors[groupKey] {
					currentCursors[groupKey] = data.GetMailId()
				}
				changed = true
			}
		}
		if changed {
			current.GroupCursors = groupCursorList(currentCursors)
		}
		return changed
	}); err != nil {
		return err
	}
	current, err := m.get(rootID)
	if err != nil {
		return err
	}
	current.mu.Lock()
	if current.synced == nil {
		current.synced = make(map[string]struct{})
	}
	current.synced[syncKey] = struct{}{}
	current.mu.Unlock()
	return nil
}

// SaveShared stores one global or group mail.
func (m *Manager) SaveShared(ctx context.Context, data *mailpb.MailData) error {
	if err := m.store.SaveShared(ctx, data); err != nil {
		return err
	}
	m.cache.DeleteAll()
	return nil
}

// CleanupSharedExpired removes expired shared mail. Personal mail expires by
// per-user key TTL and is filtered whenever its mailbox is loaded or read.
func (m *Manager) CleanupSharedExpired(ctx context.Context, nowUnix int64) (int, error) {
	return m.store.CleanupSharedExpired(ctx, nowUnix)
}

// Invalidate evicts one user's process-local mailbox without touching Redis.
func (m *Manager) Invalidate(rootID int64) {
	if m != nil && m.cache != nil {
		m.cache.Delete(rootID)
	}
}

func (m *Manager) get(rootID int64) (*mailboxEntry, error) {
	if m == nil || m.cache == nil {
		return nil, fmt.Errorf("mailbox manager is nil")
	}
	if rootID <= 0 {
		return nil, fmt.Errorf("root id is invalid")
	}
	item := m.cache.Get(rootID)
	if item == nil || item.Value() == nil {
		return nil, fmt.Errorf("mailbox load returned no state")
	}
	entry := item.Value()
	if entry.loadErr != nil {
		return nil, entry.loadErr
	}
	ctx, cancel := context.WithTimeout(m.loadCtx, loadTimeout)
	defer cancel()
	version, err := m.store.PersonalMailboxVersion(ctx, rootID)
	if err != nil {
		return nil, err
	}
	entry.mu.RLock()
	stale := entry.version != version
	entry.mu.RUnlock()
	if stale {
		m.cache.Delete(rootID)
		item = m.cache.Get(rootID)
		if item == nil || item.Value() == nil {
			return nil, fmt.Errorf("mailbox reload returned no state")
		}
		entry = item.Value()
		if entry.loadErr != nil {
			return nil, entry.loadErr
		}
	}
	return entry, nil
}

func (m *Manager) loadMailbox(cache *ttlcache.Cache[int64, *mailboxEntry], rootID int64) *ttlcache.Item[int64, *mailboxEntry] {
	ctx, cancel := context.WithTimeout(m.loadCtx, loadTimeout)
	defer cancel()
	for ctx.Err() == nil {
		loaded, err := m.store.LoadPersonalMailbox(ctx, rootID)
		if err != nil {
			return cache.Set(rootID, &mailboxEntry{loadErr: err}, loadErrorTTL)
		}
		original := mailboxMap(loaded.Mails)
		normalized := cloneMailMap(original)
		m.normalize(normalized)
		needsMigration := loaded.Version == 0 && len(normalized) > 0
		change := buildChange(original, normalized, loaded.Meta, loaded.Meta)
		if needsMigration || len(change.Upsert) > 0 || len(change.DeleteIDs) > 0 {
			version, err := m.store.ApplyPersonalMailbox(ctx, rootID, loaded.Version, change)
			if errors.Is(err, store.ErrVersionConflict) {
				continue
			}
			if err != nil {
				return cache.Set(rootID, &mailboxEntry{loadErr: err}, loadErrorTTL)
			}
			loaded.Version = version
		}
		return cache.Set(rootID, &mailboxEntry{
			version: loaded.Version,
			meta:    store.CloneMeta(loaded.Meta),
			mails:   normalized,
			synced:  make(map[string]struct{}),
		}, m.cacheTTL)
	}
	return cache.Set(rootID, &mailboxEntry{loadErr: ctx.Err()}, loadErrorTTL)
}

func (m *Manager) mutate(ctx context.Context, rootID int64, fn func(map[int64]*mailpb.MailData, *mailpb.MailboxMeta) bool) error {
	for ctx.Err() == nil {
		entry, err := m.get(rootID)
		if err != nil {
			return err
		}
		entry.mu.RLock()
		expectedVersion := entry.version
		original := cloneMailMap(entry.mails)
		originalMeta := store.CloneMeta(entry.meta)
		entry.mu.RUnlock()

		candidate := cloneMailMap(original)
		candidateMeta := store.CloneMeta(originalMeta)
		m.normalize(candidate)
		requestedChange := fn(candidate, candidateMeta)
		m.normalize(candidate)
		change := buildChange(original, candidate, originalMeta, candidateMeta)
		if !requestedChange && len(change.Upsert) == 0 && len(change.DeleteIDs) == 0 && change.Meta == nil {
			return nil
		}
		newVersion, err := m.store.ApplyPersonalMailbox(ctx, rootID, expectedVersion, change)
		if errors.Is(err, store.ErrVersionConflict) {
			m.cache.Delete(rootID)
			continue
		}
		if err != nil {
			return err
		}
		entry.mu.Lock()
		if entry.version == expectedVersion {
			entry.version = newVersion
			entry.meta = candidateMeta
			entry.mails = candidate
		}
		entry.mu.Unlock()
		return nil
	}
	return ctx.Err()
}

func (m *Manager) normalize(mails map[int64]*mailpb.MailData) {
	now := m.nowUnix()
	ids := make([]int64, 0, len(mails))
	for mailID, data := range mails {
		if data == nil || mailID <= 0 || expired(data, now) {
			delete(mails, mailID)
			continue
		}
		ids = append(ids, mailID)
	}
	if len(ids) <= m.mailboxLimit {
		return
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, mailID := range ids[:len(ids)-m.mailboxLimit] {
		delete(mails, mailID)
	}
}

func buildChange(original, candidate map[int64]*mailpb.MailData, originalMeta, candidateMeta *mailpb.MailboxMeta) store.PersonalMailboxChange {
	change := store.PersonalMailboxChange{MailCount: len(candidate)}
	persistent := false
	for mailID, data := range candidate {
		if old := original[mailID]; old == nil || !proto.Equal(old, data) {
			change.Upsert = append(change.Upsert, store.CloneMail(data))
		}
		if data.GetEndTimeUnix() <= 0 {
			persistent = true
		} else if data.GetEndTimeUnix() > change.DataExpiresAt {
			change.DataExpiresAt = data.GetEndTimeUnix()
		}
	}
	if persistent {
		change.DataExpiresAt = 0
	}
	for mailID := range original {
		if candidate[mailID] == nil {
			change.DeleteIDs = append(change.DeleteIDs, mailID)
		}
	}
	sort.Slice(change.DeleteIDs, func(i, j int) bool { return change.DeleteIDs[i] < change.DeleteIDs[j] })
	if !proto.Equal(store.CloneMeta(originalMeta), store.CloneMeta(candidateMeta)) {
		change.Meta = store.CloneMeta(candidateMeta)
	}
	return change
}

func mailboxMap(mails []*mailpb.MailData) map[int64]*mailpb.MailData {
	out := make(map[int64]*mailpb.MailData, len(mails))
	for _, data := range mails {
		if data != nil {
			out[data.GetMailId()] = store.CloneMail(data)
		}
	}
	return out
}

func cloneMailMap(in map[int64]*mailpb.MailData) map[int64]*mailpb.MailData {
	out := make(map[int64]*mailpb.MailData, len(in))
	for mailID, data := range in {
		out[mailID] = store.CloneMail(data)
	}
	return out
}

func expired(data *mailpb.MailData, nowUnix int64) bool {
	return data != nil && data.GetEndTimeUnix() > 0 && data.GetEndTimeUnix() <= nowUnix
}

func hasAttachment(data *mailpb.MailData) bool {
	return data.GetBase() != nil && len(data.GetBase().GetItemList()) > 0
}

func personalCopy(data *mailpb.MailData) *mailpb.MailData {
	out := store.CloneMail(data)
	out.Type = mailpb.MailType_MAIL_TYPE_PERSONAL
	out.Read = false
	out.Claimed = false
	return out
}

func groupCursorMap(meta *mailpb.MailboxMeta) map[string]int64 {
	out := make(map[string]int64, len(meta.GetGroupCursors()))
	for _, cursor := range meta.GetGroupCursors() {
		if cursor.GetGroupKey() != "" {
			out[cursor.GetGroupKey()] = cursor.GetLastMailId()
		}
	}
	return out
}

func groupCursorList(cursors map[string]int64) []*mailpb.MailGroupCursor {
	keys := make([]string, 0, len(cursors))
	for key := range cursors {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]*mailpb.MailGroupCursor, 0, len(keys))
	for _, key := range keys {
		out = append(out, &mailpb.MailGroupCursor{GroupKey: key, LastMailId: cursors[key]})
	}
	return out
}

func (m *Manager) nowUnix() int64 {
	if m.clock == nil {
		return time.Now().Unix()
	}
	return m.clock.Now().Unix()
}
