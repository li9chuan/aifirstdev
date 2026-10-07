package store

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/li9chuan/aifirstdev/aifd-plugins/mail/generated/mailpb"
)

// MemoryStore is an in-memory truth source for tests and demos.
type MemoryStore struct {
	mu       sync.RWMutex
	personal map[int64]map[int64]*mailpb.MailData
	meta     map[int64]*mailpb.MailboxMeta
	versions map[int64]uint64
	shared   map[int64]*mailpb.MailData
	global   map[int64]struct{}
	groups   map[string]map[int64]struct{}
}

// NewMemoryStore creates an empty memory mail store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		personal: make(map[int64]map[int64]*mailpb.MailData),
		meta:     make(map[int64]*mailpb.MailboxMeta),
		versions: make(map[int64]uint64),
		shared:   make(map[int64]*mailpb.MailData),
		global:   make(map[int64]struct{}),
		groups:   make(map[string]map[int64]struct{}),
	}
}

// PersonalMailboxVersion reads the current durable revision.
func (s *MemoryStore) PersonalMailboxVersion(_ context.Context, rootID int64) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.versions[rootID], nil
}

// LoadPersonalMailbox loads one user's durable mailbox in one operation.
func (s *MemoryStore) LoadPersonalMailbox(_ context.Context, rootID int64) (*PersonalMailbox, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	box := &PersonalMailbox{
		Version: s.versions[rootID],
		Meta:    CloneMeta(s.meta[rootID]),
		Mails:   make([]*mailpb.MailData, 0, len(s.personal[rootID])),
	}
	for _, data := range s.personal[rootID] {
		box.Mails = append(box.Mails, CloneMail(data))
	}
	return box, nil
}

// ApplyPersonalMailbox atomically applies a mailbox delta when the version matches.
func (s *MemoryStore) ApplyPersonalMailbox(_ context.Context, rootID int64, expectedVersion uint64, change PersonalMailboxChange) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.versions[rootID] != expectedVersion {
		return s.versions[rootID], ErrVersionConflict
	}
	box := s.ensurePersonal(rootID)
	for _, data := range change.Upsert {
		if data != nil {
			box[data.GetMailId()] = CloneMail(data)
		}
	}
	for _, mailID := range change.DeleteIDs {
		delete(box, mailID)
	}
	if change.Meta != nil {
		s.meta[rootID] = CloneMeta(change.Meta)
	}
	if change.MailCount == 0 {
		delete(s.personal, rootID)
	}
	s.versions[rootID]++
	return s.versions[rootID], nil
}

// SaveShared stores a global or group mail and updates its indexes.
func (s *MemoryStore) SaveShared(_ context.Context, data *mailpb.MailData) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := CloneMail(data)
	s.shared[cp.GetMailId()] = cp
	if cp.GetType() == mailpb.MailType_MAIL_TYPE_GLOBAL {
		s.global[cp.GetMailId()] = struct{}{}
	}
	for _, groupKey := range cp.GetGroupKeys() {
		if groupKey == "" {
			continue
		}
		if s.groups[groupKey] == nil {
			s.groups[groupKey] = make(map[int64]struct{})
		}
		s.groups[groupKey][cp.GetMailId()] = struct{}{}
	}
	return nil
}

// ListGlobalAfter lists global mails after a cursor.
func (s *MemoryStore) ListGlobalAfter(_ context.Context, afterMailID int64, realmID int32, limit int) ([]*mailpb.MailData, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listSharedLocked(s.global, afterMailID, realmID, limit), nil
}

// ListGroupAfter lists group mails after a cursor.
func (s *MemoryStore) ListGroupAfter(_ context.Context, groupKey string, afterMailID int64, limit int) ([]*mailpb.MailData, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listSharedLocked(s.groups[groupKey], afterMailID, 0, limit), nil
}

// CleanupSharedExpired removes expired shared mails.
func (s *MemoryStore) CleanupSharedExpired(_ context.Context, nowUnix int64) (int, error) {
	if nowUnix <= 0 {
		nowUnix = time.Now().Unix()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var count int
	for mailID, data := range s.shared {
		if !expired(data, nowUnix) {
			continue
		}
		s.deleteSharedLocked(mailID, data)
		count++
	}
	return count, nil
}

func (s *MemoryStore) ensurePersonal(rootID int64) map[int64]*mailpb.MailData {
	if s.personal[rootID] == nil {
		s.personal[rootID] = make(map[int64]*mailpb.MailData)
	}
	return s.personal[rootID]
}

func (s *MemoryStore) listSharedLocked(index map[int64]struct{}, afterMailID int64, realmID int32, limit int) []*mailpb.MailData {
	now := time.Now().Unix()
	ids := sortedSetIDs(index, afterMailID)
	out := make([]*mailpb.MailData, 0, boundedLimit(limit, len(ids)))
	for _, id := range ids {
		data := s.shared[id]
		if expired(data, now) {
			s.deleteSharedLocked(id, data)
			continue
		}
		if realmID > 0 && !matchRealm(data.GetTargetRealms(), realmID) {
			continue
		}
		out = append(out, CloneMail(data))
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func (s *MemoryStore) deleteSharedLocked(mailID int64, data *mailpb.MailData) {
	delete(s.shared, mailID)
	delete(s.global, mailID)
	for _, groupKey := range data.GetGroupKeys() {
		if index := s.groups[groupKey]; index != nil {
			delete(index, mailID)
			if len(index) == 0 {
				delete(s.groups, groupKey)
			}
		}
	}
}

func sortedIDs(in map[int64]*mailpb.MailData, after int64) []int64 {
	ids := make([]int64, 0, len(in))
	for id := range in {
		if id > after {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func sortedSetIDs(in map[int64]struct{}, after int64) []int64 {
	ids := make([]int64, 0, len(in))
	for id := range in {
		if id > after {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func boundedLimit(limit int, fallback int) int {
	if limit > 0 {
		return limit
	}
	return fallback
}

func expired(data *mailpb.MailData, nowUnix int64) bool {
	return data != nil && data.GetEndTimeUnix() > 0 && data.GetEndTimeUnix() <= nowUnix
}

func matchRealm(targets []int32, realmID int32) bool {
	if len(targets) == 0 {
		return true
	}
	for _, target := range targets {
		if target == realmID {
			return true
		}
	}
	return false
}
