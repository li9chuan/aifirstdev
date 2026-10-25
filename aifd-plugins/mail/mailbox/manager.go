// Package mailbox owns the mail service's canonical in-memory state.
// Exactly one Manager may write a Redis namespace. Other processes use RPC.
package mailbox

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"time"

	mail "github.com/li9chuan/aifirstdev/aifd-plugins/mail"
	"github.com/li9chuan/aifirstdev/aifd-plugins/mail/generated/mailpb"
	rdb "github.com/li9chuan/aifirstdev/aifd-rdb"
	"google.golang.org/protobuf/proto"
)

const (
	DefaultMailboxLimit  = 100
	DefaultCacheTTL      = 20 * time.Minute
	DefaultCacheCapacity = 10_000
	DefaultClaimCD       = 5 * time.Second
	DefaultAwardTimeout  = 3 * time.Second
	loadTimeout          = 5 * time.Second
)

type Options struct {
	DB            *rdb.DB
	Prefix        string
	Clock         mail.Clock
	IDGenerator   mail.IDGenerator
	Awarder       mail.Awarder
	MailboxLimit  int
	CacheTTL      time.Duration
	CacheCapacity int
	ClaimCD       time.Duration
	AwardTimeout  time.Duration
}

type mailEntry struct {
	rdb.HashSaver[*mailpb.MailData]
}

type mailboxEntry struct {
	mu       sync.RWMutex // Always before meta/mail saver locks.
	meta     rdb.HashSaver[*mailpb.MailboxMeta]
	mails    map[int64]*mailEntry
	claiming map[int64]struct{}
}

type sharedState struct {
	mu        sync.RWMutex
	mails     map[int64]*mailEntry
	global    []*mailpb.MailData
	groups    map[string][]*mailpb.MailData
	maxMailID int64
}

type cacheSlot struct {
	ready      chan struct{}
	box        *mailboxEntry
	err        error
	refs       int
	lastAccess time.Time
	evicting   chan struct{}
}

// Manager is constructed and exclusively owned by the service Module. The module
// drains operations before Close; no external writer may share its namespace.
type Manager struct {
	Options
	idMu       sync.Mutex
	shared     sharedState
	cacheMu    sync.Mutex
	cache      map[int64]*cacheSlot
	recovered  bool
	closed     bool
	loadCtx    context.Context
	cancelLoad context.CancelFunc
	loads      sync.WaitGroup
}

func NewManager(opts Options) (*Manager, error) {
	if opts.DB == nil || opts.IDGenerator == nil {
		return nil, fmt.Errorf("mail DB and ID generator are required")
	}
	if opts.MailboxLimit < 0 || opts.CacheTTL < 0 || opts.CacheCapacity < 0 || opts.ClaimCD < 0 || opts.AwardTimeout < 0 {
		return nil, fmt.Errorf("mail limits and durations cannot be negative")
	}
	if opts.Prefix == "" {
		opts.Prefix = "aifd:mail"
	}
	if opts.Clock == nil {
		opts.Clock = mail.SystemClock{}
	}
	if opts.MailboxLimit == 0 {
		opts.MailboxLimit = DefaultMailboxLimit
	}
	if opts.CacheTTL == 0 {
		opts.CacheTTL = DefaultCacheTTL
	}
	if opts.CacheCapacity == 0 {
		opts.CacheCapacity = DefaultCacheCapacity
	}
	if opts.ClaimCD == 0 {
		opts.ClaimCD = DefaultClaimCD
	}
	if opts.AwardTimeout == 0 {
		opts.AwardTimeout = DefaultAwardTimeout
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{Options: opts, cache: make(map[int64]*cacheSlot), loadCtx: ctx, cancelLoad: cancel,
		shared: sharedState{mails: make(map[int64]*mailEntry), groups: make(map[string][]*mailpb.MailData)}}, nil
}

// Recover rebuilds shared indexes from hash payloads, never legacy ZSETs.
func (m *Manager) Recover(ctx context.Context) error {
	m.cacheMu.Lock()
	if m.closed {
		m.cacheMu.Unlock()
		return mail.ErrClosed
	}
	if m.recovered {
		m.cacheMu.Unlock()
		return nil
	}
	m.cacheMu.Unlock()
	values, err := rdb.LoadHash(ctx, m.DB, m.sharedKey(), func() *mailpb.MailData { return &mailpb.MailData{} })
	if err != nil {
		return fmt.Errorf("mail shared recovery: %w", err)
	}
	state := sharedState{mails: make(map[int64]*mailEntry), groups: make(map[string][]*mailpb.MailData)}
	for field, data := range values {
		if err := validateStored(field, data); err != nil {
			return err
		}
		if data.GetType() != mailpb.MailType_MAIL_TYPE_GLOBAL && data.GetType() != mailpb.MailType_MAIL_TYPE_GROUP {
			return fmt.Errorf("mail shared hash contains personal mail %s", field)
		}
		if !expired(data, m.Clock.Now().Unix()) {
			if err := normalizeStoredGroup(data); err != nil {
				return fmt.Errorf("shared mail %d: %w", data.GetMailId(), err)
			}
		}
		entry, err := m.bindMail(m.sharedKey(), data)
		if err != nil {
			return err
		}
		state.mails[data.GetMailId()] = entry
		if data.GetMailId() > state.maxMailID {
			state.maxMailID = data.GetMailId()
		}
		if !expired(data, m.Clock.Now().Unix()) {
			indexShared(&state, data)
		}
	}
	sort.Slice(state.global, func(i, j int) bool { return state.global[i].GetMailId() < state.global[j].GetMailId() })
	for _, mails := range state.groups {
		sort.Slice(mails, func(i, j int) bool { return mails[i].GetMailId() < mails[j].GetMailId() })
	}
	m.cacheMu.Lock()
	defer m.cacheMu.Unlock()
	if m.closed {
		return mail.ErrClosed
	}
	if m.recovered {
		return nil
	}
	m.shared.mails, m.shared.global, m.shared.groups, m.shared.maxMailID = state.mails, state.global, state.groups, state.maxMailID
	m.recovered = true
	return nil
}

// Close follows service admission/drain. DB and client remain caller-owned.
// Failed Flush leaves canonical objects intact for a later Close/Flush retry.
func (m *Manager) Close(ctx context.Context) error {
	m.cacheMu.Lock()
	m.closed = true
	m.cancelLoad()
	m.cacheMu.Unlock()
	m.loads.Wait()
	return m.DB.Flush(ctx)
}

func (m *Manager) ready() error {
	m.cacheMu.Lock()
	defer m.cacheMu.Unlock()
	if m.closed {
		return mail.ErrClosed
	}
	if !m.recovered {
		return mail.ErrNotReady
	}
	return nil
}

func (m *Manager) userKey(uid int64) string { return fmt.Sprintf("%s:user:%d:data", m.Prefix, uid) }
func (m *Manager) metaKey(uid int64) string { return fmt.Sprintf("%s:user:%d:meta", m.Prefix, uid) }
func (m *Manager) sharedKey() string        { return m.Prefix + ":shared:data" }

func (m *Manager) bindMail(key string, data *mailpb.MailData) (*mailEntry, error) {
	e := &mailEntry{}
	if err := e.BindDB(m.DB, key, strconv.FormatInt(data.GetMailId(), 10), data); err != nil {
		return nil, err
	}
	return e, nil
}

func validateStored(field string, data *mailpb.MailData) error {
	if data.GetMailId() <= 0 || field != strconv.FormatInt(data.GetMailId(), 10) || data.GetBase() == nil || data.GetClaimRetryAtUnixMs() < 0 || data.GetEndTimeUnix() < 0 {
		return fmt.Errorf("mail corrupt identity/payload in field %q", field)
	}
	switch data.GetType() {
	case mailpb.MailType_MAIL_TYPE_PERSONAL, mailpb.MailType_MAIL_TYPE_GLOBAL, mailpb.MailType_MAIL_TYPE_GROUP:
		return nil
	default:
		return fmt.Errorf("mail corrupt type in field %q", field)
	}
}

func (m *Manager) newMail(req *mailpb.MailAddRequest) (*mailpb.MailData, error) {
	m.idMu.Lock()
	id, err := m.IDGenerator.NextID()
	m.idMu.Unlock()
	if err != nil {
		return nil, err
	}
	if id <= 0 {
		return nil, fmt.Errorf("mail generator returned non-positive ID")
	}
	now := m.Clock.Now().Unix()
	end := req.GetEndTimeUnix()
	if end == 0 {
		days := req.GetSaveDays()
		if days == 0 {
			days = 20
		}
		end = now + int64(days)*86400
	}
	return &mailpb.MailData{MailId: id, Type: req.GetType(), Base: proto.Clone(req.GetBase()).(*mailpb.MailBase), SendTimeUnix: now, EndTimeUnix: end,
		GroupKey: req.GetGroupKey()}, nil
}

func expired(data *mailpb.MailData, now int64) bool {
	return data.GetEndTimeUnix() > 0 && data.GetEndTimeUnix() <= now
}
func hasAttachment(data *mailpb.MailData) bool         { return len(data.GetBase().GetItemList()) > 0 }
func cloneMail(data *mailpb.MailData) *mailpb.MailData { return proto.Clone(data).(*mailpb.MailData) }

// SummaryMail returns an independent client view, never the stored body.
func SummaryMail(data *mailpb.MailData) *mailpb.MailData {
	if data == nil {
		return nil
	}
	out := cloneMail(data)
	if out.Base == nil {
		return out
	}
	if custom := out.GetBase().GetCustomContent(); custom != nil {
		preview := []rune(custom.GetBody())
		if len(preview) > 160 {
			preview = preview[:160]
		}
		out.Preview = string(preview)
		custom.Body = ""
	}
	out.Base.ContentList = nil
	return out
}
