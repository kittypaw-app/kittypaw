package webapp

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"sync"
	"time"
)

const randomIDBytes = 32

type pendingLogin struct {
	CodeVerifier string
	RedirectURI  string
	ExpiresAt    time.Time
}

type loginStore struct {
	mu   sync.Mutex
	rand io.Reader
	now  func() time.Time
	data map[string]pendingLogin
}

func newLoginStore(randReader io.Reader, now func() time.Time) *loginStore {
	if randReader == nil {
		randReader = rand.Reader
	}
	if now == nil {
		now = time.Now
	}
	return &loginStore{
		rand: randReader,
		now:  now,
		data: make(map[string]pendingLogin),
	}
}

func (s *loginStore) Create(login pendingLogin) (string, error) {
	state, err := randomURLToken(s.rand)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[state] = login
	return state, nil
}

func (s *loginStore) Consume(state string) (pendingLogin, bool) {
	if state == "" {
		return pendingLogin{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	login, ok := s.data[state]
	if ok {
		delete(s.data, state)
	}
	if !ok || !login.ExpiresAt.After(s.now()) {
		return pendingLogin{}, false
	}
	return login, true
}

type tokenSession struct {
	AccessToken      string
	RefreshToken     string
	TokenType        string
	AccessExpiresAt  time.Time
	SessionExpiresAt time.Time
}

type sessionStore struct {
	mu   sync.Mutex
	rand io.Reader
	now  func() time.Time
	data map[string]tokenSession
}

func newSessionStore(randReader io.Reader, now func() time.Time) *sessionStore {
	if randReader == nil {
		randReader = rand.Reader
	}
	if now == nil {
		now = time.Now
	}
	return &sessionStore{
		rand: randReader,
		now:  now,
		data: make(map[string]tokenSession),
	}
}

func (s *sessionStore) Create(session tokenSession) (string, error) {
	if session.TokenType == "" {
		session.TokenType = "Bearer"
	}
	id, err := randomURLToken(s.rand)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[id] = session
	return id, nil
}

func (s *sessionStore) Get(id string) (tokenSession, bool) {
	if id == "" {
		return tokenSession{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.data[id]
	if ok && !session.SessionExpiresAt.IsZero() && !session.SessionExpiresAt.After(s.now()) {
		delete(s.data, id)
		return tokenSession{}, false
	}
	return session, ok
}

func (s *sessionStore) Update(id string, session tokenSession) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[id] = session
}

func (s *sessionStore) Delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, id)
}

func randomURLToken(randReader io.Reader) (string, error) {
	buf := make([]byte, randomIDBytes)
	if _, err := io.ReadFull(randReader, buf); err != nil {
		return "", fmt.Errorf("random token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
