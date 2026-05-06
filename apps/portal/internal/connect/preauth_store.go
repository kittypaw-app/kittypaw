package connect

import (
	"fmt"
	"sync"
	"time"
)

const (
	defaultPreauthTTL        = 5 * time.Minute
	defaultPreauthMaxEntries = 10000
)

type PreauthStoreOptions struct {
	TTL        time.Duration
	MaxEntries int
	Now        func() time.Time
}

type PreauthSession struct {
	UserID   string
	Provider string
	Mode     string
	Port     string
}

type preauthEntry struct {
	session   PreauthSession
	createdAt time.Time
}

type PreauthStore struct {
	mu         sync.Mutex
	entries    map[string]preauthEntry
	ttl        time.Duration
	maxEntries int
	now        func() time.Time
}

func NewPreauthStore(opts PreauthStoreOptions) *PreauthStore {
	ttl := opts.TTL
	if ttl <= 0 {
		ttl = defaultPreauthTTL
	}
	maxEntries := opts.MaxEntries
	if maxEntries <= 0 {
		maxEntries = defaultPreauthMaxEntries
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &PreauthStore{
		entries:    make(map[string]preauthEntry),
		ttl:        ttl,
		maxEntries: maxEntries,
		now:        now,
	}
}

func (s *PreauthStore) Create(session PreauthSession) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	s.evictExpiredLocked(now)
	if len(s.entries) >= s.maxEntries {
		return "", fmt.Errorf("connect preauth store full")
	}

	code, err := generateCode()
	if err != nil {
		return "", err
	}
	for {
		if _, exists := s.entries[code]; !exists {
			break
		}
		code, err = generateCode()
		if err != nil {
			return "", err
		}
	}
	s.entries[code] = preauthEntry{session: session, createdAt: now}
	return code, nil
}

func (s *PreauthStore) Consume(code string) (PreauthSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.entries[code]
	if !ok {
		return PreauthSession{}, fmt.Errorf("unknown or expired connect preauth session")
	}
	delete(s.entries, code)
	if s.now().Sub(entry.createdAt) > s.ttl {
		return PreauthSession{}, fmt.Errorf("connect preauth session expired")
	}
	return entry.session, nil
}

func (s *PreauthStore) evictExpiredLocked(now time.Time) {
	for code, entry := range s.entries {
		if now.Sub(entry.createdAt) > s.ttl {
			delete(s.entries, code)
		}
	}
}
