package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const MinPasswordLength = 36

type Auth struct {
	sessions map[string]time.Time
	mu       sync.RWMutex
	stateDir string
}

func New(stateDir string) (*Auth, error) {
	a := &Auth{
		stateDir: stateDir,
		sessions: make(map[string]time.Time),
	}

	return a, nil
}

func (a *Auth) Authenticate(ctx context.Context, password string) (string, bool) {
	if len(password) < MinPasswordLength {
		slog.Debug("Password too short")
		return "", false
	}
	// Hash the password
	hash := sha256.Sum256([]byte(password))
	hashedPassword := hex.EncodeToString(hash[:])

	// Check if file exists in stateDir/hashed-passwords/
	passwordFilePath := filepath.Join(a.stateDir, "hashed-passwords", hashedPassword)
	if _, err := os.Stat(passwordFilePath); os.IsNotExist(err) {
		slog.Debug("password file not found. Authenticate failed", "path", passwordFilePath)
		return "", false
	}

	token := generateToken()

	// Store the session
	a.mu.Lock()
	a.sessions[token] = time.Now().Add(24 * time.Hour)
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
	// TODO
}

func generateToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// AddPassword adds a password to the hashed-passwords directory
func AddPassword(stateDir, password string) error {
	if len(password) < MinPasswordLength {
		return fmt.Errorf("password must be at least %d characters long", MinPasswordLength)
	}

	// Hash the password
	hash := sha256.Sum256([]byte(password))
	hashedPassword := hex.EncodeToString(hash[:])

	// Create the hashed-passwords directory if it doesn't exist
	hashedPasswordsDir := filepath.Join(stateDir, "hashed-passwords")
	if err := os.MkdirAll(hashedPasswordsDir, 0700); err != nil {
		return fmt.Errorf("failed to create hashed-passwords directory: %w", err)
	}

	// Create the password file
	passwordFilePath := filepath.Join(hashedPasswordsDir, hashedPassword)
	if err := os.WriteFile(passwordFilePath, []byte{}, 0600); err != nil {
		return fmt.Errorf("failed to write password file: %w", err)
	}

	return nil
}
