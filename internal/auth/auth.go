package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	mathrand "math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

const MinPasswordLength = 36

func InitAuth(stateDir string) error {
	// Create sessions directory if it doesn't exist
	sessionsDir := filepath.Join(stateDir, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o700); err != nil {
		return fmt.Errorf("failed to create sessions directory: %w", err)
	}
	return nil
}

func Authenticate(ctx context.Context, stateDir, password string) (string, bool) {
	if len(password) < MinPasswordLength {
		slog.Debug("Password too short")
		return "", false
	}
	// Hash the password
	hash := sha256.Sum256([]byte(password))
	hashedPassword := hex.EncodeToString(hash[:])

	// Check if file exists in stateDir/hashed-passwords/
	passwordFilePath := filepath.Join(stateDir, "hashed-passwords", hashedPassword)
	if _, err := os.Stat(passwordFilePath); os.IsNotExist(err) {
		// Add random delay to mitigate timing attacks
		time.Sleep(time.Duration(10+mathrand.Int32N(1000)) * time.Microsecond)
		slog.Debug("password file not found. Authenticate failed", "path", passwordFilePath)
		return "", false
	}

	token := generateToken()
	expiry := time.Now().UTC().Add(24 * time.Hour)

	// Hash the token for storage (security: don't store raw tokens)
	tokenHash := sha256.Sum256([]byte(token))
	hashedToken := hex.EncodeToString(tokenHash[:])

	// Persist session to disk
	if err := saveSession(stateDir, hashedToken, expiry); err != nil {
		slog.Warn("Failed to persist session", "error", err)
	}

	return token, true
}

// saveSession saves a session to disk
func saveSession(stateDir, hashedToken string, expiry time.Time) error {
	sessionsDir := filepath.Join(stateDir, "sessions")
	sessionPath := filepath.Join(sessionsDir, hashedToken)

	// Write expiry time as Unix timestamp
	expiryStr := strconv.FormatInt(expiry.Unix(), 10)
	return os.WriteFile(sessionPath, []byte(expiryStr), 0o600)
}

func ValidateSession(stateDir, token string) (bool, error) {
	valid, _, err := ValidateSessionWithExpiry(stateDir, token)
	return valid, err
}

// ValidateSessionWithExpiry validates a session and returns the expiry time
func ValidateSessionWithExpiry(stateDir, token string) (bool, time.Time, error) {
	// Hash the token to look it up
	tokenHash := sha256.Sum256([]byte(token))
	hashedToken := hex.EncodeToString(tokenHash[:])

	sessionsDir := filepath.Join(stateDir, "sessions")
	sessionPath := filepath.Join(sessionsDir, hashedToken)

	// Read session file
	data, err := os.ReadFile(sessionPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Add random delay to mitigate timing attacks
			time.Sleep(time.Duration(10+mathrand.Int32N(1000)) * time.Microsecond)
			return false, time.Time{}, nil
		}
		return false, time.Time{}, fmt.Errorf("failed to read session file: %w", err)
	}

	// Parse expiry time
	expiryUnix, err := strconv.ParseInt(string(data), 10, 64)
	if err != nil {
		return false, time.Time{}, fmt.Errorf("failed to parse session expiry: %w", err)
	}

	expiry := time.Unix(expiryUnix, 0)

	// Check if expired
	if time.Now().UTC().After(expiry) {
		// Clean up expired session
		_ = os.Remove(sessionPath)
		return false, time.Time{}, nil
	}

	return true, expiry, nil
}

// ExtendSession extends an existing session by creating a new token
// The old token remains valid until its original expiry time
func ExtendSession(stateDir, oldToken string) (string, bool) {
	// Validate the old session first
	valid, _, err := ValidateSessionWithExpiry(stateDir, oldToken)
	if err != nil || !valid {
		return "", false
	}

	// Create new token with new expiry
	newToken := generateToken()
	expiry := time.Now().UTC().Add(24 * time.Hour)

	// Hash the new token for storage
	tokenHash := sha256.Sum256([]byte(newToken))
	hashedToken := hex.EncodeToString(tokenHash[:])

	// Persist new session to disk
	if err := saveSession(stateDir, hashedToken, expiry); err != nil {
		slog.Warn("Failed to persist extended session", "error", err)
		return "", false
	}

	return newToken, true
}

func CleanExpiredSessions(stateDir string) {
	now := time.Now().UTC()
	sessionsDir := filepath.Join(stateDir, "sessions")

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
	if err := os.MkdirAll(hashedPasswordsDir, 0o700); err != nil {
		return fmt.Errorf("failed to create hashed-passwords directory: %w", err)
	}

	// Create the password file
	passwordFilePath := filepath.Join(hashedPasswordsDir, hashedPassword)
	if err := os.WriteFile(passwordFilePath, []byte{}, 0o600); err != nil {
		return fmt.Errorf("failed to write password file: %w", err)
	}

	return nil
}
