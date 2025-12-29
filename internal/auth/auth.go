package auth

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Auth struct {
	password     string
	sessions     map[string]time.Time
	mu           sync.RWMutex
	sessionFile  string
}

func New(password, dataDir string) (*Auth, error) {
	sessionFile := filepath.Join(dataDir, "sessions.json")

	a := &Auth{
		password:    password,
		sessions:    make(map[string]time.Time),
		sessionFile: sessionFile,
	}

	a.loadSessions()

	return a, nil
}

func (a *Auth) Authenticate(password string) (string, bool) {
	if password != a.password {
		return "", false
	}

	token := generateToken()

	a.mu.Lock()
	a.sessions[token] = time.Now().Add(24 * time.Hour)
	a.saveSessions()
	a.mu.Unlock()

	return token, true
}

func (a *Auth) ValidateSession(token string) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()

	expiry, ok := a.sessions[token]
	if !ok {
		return false
	}

	return time.Now().Before(expiry)
}

func (a *Auth) CleanExpiredSessions() {
	a.mu.Lock()
	defer a.mu.Unlock()

	now := time.Now()
	for token, expiry := range a.sessions {
		if now.After(expiry) {
			delete(a.sessions, token)
		}
	}
	a.saveSessions()
}

func (a *Auth) saveSessions() {
	data, err := json.MarshalIndent(a.sessions, "", "  ")
	if err != nil {
		return
	}
	os.WriteFile(a.sessionFile, data, 0600)
}

func (a *Auth) loadSessions() {
	data, err := os.ReadFile(a.sessionFile)
	if err != nil {
		return
	}
	json.Unmarshal(data, &a.sessions)
}

func generateToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}
