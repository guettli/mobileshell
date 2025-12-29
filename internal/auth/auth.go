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
	"strconv"
	"time"
)

const MinPasswordLength = 36

type Auth struct {
	stateDir string
}

func New(stateDir string) (*Auth, error) {
	a := &Auth{
		stateDir: stateDir,
	}

	// Create sessions directory if it doesn't exist
	sessionsDir := filepath.Join(stateDir, "sessions")
	if err := os.MkdirAll(sessionsDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create sessions directory: %w", err)
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
	expiry := time.Now().Add(24 * time.Hour)

	// Hash the token for storage (security: don't store raw tokens)
	tokenHash := sha256.Sum256([]byte(token))
	hashedToken := hex.EncodeToString(tokenHash[:])

	// Persist session to disk
	if err := a.saveSession(hashedToken, expiry); err != nil {
		slog.Warn("Failed to persist session", "error", err)
	}

	return token, true
}

// saveSession saves a session to disk
func (a *Auth) saveSession(hashedToken string, expiry time.Time) error {
	sessionsDir := filepath.Join(a.stateDir, "sessions")
	sessionPath := filepath.Join(sessionsDir, hashedToken)

	// Write expiry time as Unix timestamp
	expiryStr := strconv.FormatInt(expiry.Unix(), 10)
	return os.WriteFile(sessionPath, []byte(expiryStr), 0600)
}

func (a *Auth) ValidateSession(token string) bool {
	// Hash the token to look it up
	tokenHash := sha256.Sum256([]byte(token))
	hashedToken := hex.EncodeToString(tokenHash[:])

	sessionsDir := filepath.Join(a.stateDir, "sessions")
	sessionPath := filepath.Join(sessionsDir, hashedToken)

	// Read session file
	data, err := os.ReadFile(sessionPath)
	if err != nil {
		return false
	}

	// Parse expiry time
	expiryUnix, err := strconv.ParseInt(string(data), 10, 64)
	if err != nil {
		return false
	}

	expiry := time.Unix(expiryUnix, 0)

	// Check if expired
	if time.Now().After(expiry) {
		// Clean up expired session
		_ = os.Remove(sessionPath)
		return false
	}

	return true
}

func (a *Auth) CleanExpiredSessions() {
	now := time.Now()
	sessionsDir := filepath.Join(a.stateDir, "sessions")

	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		sessionPath := filepath.Join(sessionsDir, entry.Name())
		data, err := os.ReadFile(sessionPath)
		if err != nil {
			continue
		}

		expiryUnix, err := strconv.ParseInt(string(data), 10, 64)
		if err != nil {
			continue
		}

		expiry := time.Unix(expiryUnix, 0)
		if now.After(expiry) {
			_ = os.Remove(sessionPath)
		}
	}
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
