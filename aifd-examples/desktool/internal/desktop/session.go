package desktop

import (
	"sync"

	deskproxy "github.com/li9chuan/aifirstdev/aifd-examples/desktool/internal/services/proxy"
)

type sessionStore struct {
	mu      sync.RWMutex
	session deskproxy.SessionState
}

func (s *sessionStore) Snapshot() deskproxy.SessionState {
	if s == nil {
		return deskproxy.SessionState{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.session
}

func (s *sessionStore) Set(session deskproxy.SessionState) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.session = session
	s.mu.Unlock()
}

func (s *sessionStore) Clear() deskproxy.SessionState {
	if s == nil {
		return deskproxy.SessionState{}
	}
	s.mu.Lock()
	s.session = deskproxy.SessionState{}
	s.mu.Unlock()
	return deskproxy.SessionState{}
}
