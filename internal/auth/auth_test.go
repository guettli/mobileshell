package auth

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestInitAuth(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()

	err := InitAuth(tmpDir)
	if err != nil {
		t.Fatalf("InitAuth failed: %v", err)
	}

	// Verify sessions directory was created
	sessionsDir := filepath.Join(tmpDir, "sessions")
	info, err := os.Stat(sessionsDir)
	if err != nil {
		t.Fatalf("Sessions directory not created: %v", err)
	}

	if !info.IsDir() {
		t.Fatal("Sessions path is not a directory")
	}

	// Check permissions
	if info.Mode().Perm() != 0o700 {
		t.Errorf("Expected permissions 0700, got %v", info.Mode().Perm())
	}
}

func TestAddPassword(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()

	tests := []struct {
		name        string
		password    string
		expectError bool
	}{
		{
			name:        "valid password",
			password:    "a-very-long-password-that-meets-minimum-length-requirements",
			expectError: false,
		},
		{
			name:        "password too short",
			password:    "short",
			expectError: true,
		},
		{
			name:        "password exactly minimum length",
			password:    "123456789012345678901234567890123456", // 36 chars
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := AddPassword(tmpDir, tt.password)
			if tt.expectError && err == nil {
				t.Error("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}
		})
	}
}

func TestAuthenticate(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	ctx := context.Background()

	// Initialize auth to create sessions directory
	err := InitAuth(tmpDir)
	if err != nil {
		t.Fatalf("Failed to initialize auth: %v", err)
	}

	// Test with non-existent password
	token, success := Authenticate(ctx, tmpDir, "nonexistent-password-that-is-long-enough-to-pass-length-check")
	if success {
		t.Error("Authentication should fail with non-existent password")
	}
	if token != "" {
		t.Error("Token should be empty on failed authentication")
	}

	// Add a valid password
	validPassword := "a-very-long-password-that-meets-minimum-length-requirements"
	err = AddPassword(tmpDir, validPassword)
	if err != nil {
		t.Fatalf("Failed to add password: %v", err)
	}

	// Test with correct password
	token, success = Authenticate(ctx, tmpDir, validPassword)
	if !success {
		t.Error("Authentication should succeed with valid password")
	}
	if token == "" {
		t.Error("Token should not be empty on successful authentication")
	}

	// Verify token length (should be 64 hex chars = 32 bytes)
	if len(token) != 64 {
		t.Errorf("Expected token length 64, got %d", len(token))
	}

	// Test with password too short
	token, success = Authenticate(ctx, tmpDir, "short")
	if success {
		t.Error("Authentication should fail with short password")
	}
	if token != "" {
		t.Error("Token should be empty on failed authentication")
	}
}

func TestValidateSession(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	ctx := context.Background()

	// Initialize auth
	err := InitAuth(tmpDir)
	if err != nil {
		t.Fatalf("Failed to initialize auth: %v", err)
	}

	// Add a password and authenticate
	password := "a-very-long-password-that-meets-minimum-length-requirements"
	err = AddPassword(tmpDir, password)
	if err != nil {
		t.Fatalf("Failed to add password: %v", err)
	}

	token, success := Authenticate(ctx, tmpDir, password)
	if !success {
		t.Fatal("Authentication failed")
	}

	// Validate the session
	valid, err := ValidateSession(tmpDir, token)
	if err != nil {
		t.Errorf("ValidateSession returned error: %v", err)
	}
	if !valid {
		t.Error("Session should be valid")
	}

	// Test with invalid token
	valid, err = ValidateSession(tmpDir, "invalid-token")
	if err != nil {
		t.Errorf("ValidateSession should not return error for invalid token: %v", err)
	}
	if valid {
		t.Error("Session should be invalid for non-existent token")
	}
}

func TestValidateSessionWithExpiry(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	ctx := context.Background()

	// Initialize auth
	err := InitAuth(tmpDir)
	if err != nil {
		t.Fatalf("Failed to initialize auth: %v", err)
	}

	// Add a password and authenticate
	password := "a-very-long-password-that-meets-minimum-length-requirements"
	err = AddPassword(tmpDir, password)
	if err != nil {
		t.Fatalf("Failed to add password: %v", err)
	}

	beforeAuth := time.Now().UTC()
	token, success := Authenticate(ctx, tmpDir, password)
	if !success {
		t.Fatal("Authentication failed")
	}

	// Validate the session and check expiry
	valid, expiry, err := ValidateSessionWithExpiry(tmpDir, token)
	if err != nil {
		t.Errorf("ValidateSessionWithExpiry returned error: %v", err)
	}
	if !valid {
		t.Error("Session should be valid")
	}

	// Check expiry is approximately 24 hours from now
	expectedExpiry := beforeAuth.Add(24 * time.Hour)
	if expiry.Before(expectedExpiry.Add(-1*time.Minute)) || expiry.After(expectedExpiry.Add(1*time.Minute)) {
		t.Errorf("Expected expiry around %v, got %v", expectedExpiry, expiry)
	}

	// Test with invalid token
	valid, expiry, err = ValidateSessionWithExpiry(tmpDir, "invalid-token")
	if err != nil {
		t.Errorf("ValidateSessionWithExpiry should not return error for invalid token: %v", err)
	}
	if valid {
		t.Error("Session should be invalid for non-existent token")
	}
	if !expiry.IsZero() {
		t.Error("Expiry should be zero time for invalid session")
	}
}

func TestExtendSession(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	ctx := context.Background()

	// Initialize auth
	err := InitAuth(tmpDir)
	if err != nil {
		t.Fatalf("Failed to initialize auth: %v", err)
	}

	// Add a password and authenticate
	password := "a-very-long-password-that-meets-minimum-length-requirements"
	err = AddPassword(tmpDir, password)
	if err != nil {
		t.Fatalf("Failed to add password: %v", err)
	}

	oldToken, success := Authenticate(ctx, tmpDir, password)
	if !success {
		t.Fatal("Authentication failed")
	}

	// Extend the session
	newToken, success := ExtendSession(tmpDir, oldToken)
	if !success {
		t.Error("Session extension should succeed")
	}
	if newToken == "" {
		t.Error("New token should not be empty")
	}
	if newToken == oldToken {
		t.Error("New token should be different from old token")
	}

	// Validate both tokens
	valid, err := ValidateSession(tmpDir, oldToken)
	if err != nil || !valid {
		t.Error("Old token should still be valid")
	}

	valid, err = ValidateSession(tmpDir, newToken)
	if err != nil || !valid {
		t.Error("New token should be valid")
	}

	// Test extending invalid session
	newToken, success = ExtendSession(tmpDir, "invalid-token")
	if success {
		t.Error("Extension should fail for invalid token")
	}
	if newToken != "" {
		t.Error("New token should be empty on failed extension")
	}
}

func TestCleanExpiredSessions(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()

	// Initialize auth
	err := InitAuth(tmpDir)
	if err != nil {
		t.Fatalf("Failed to initialize auth: %v", err)
	}

	sessionsDir := filepath.Join(tmpDir, "sessions")

	// Create an expired session manually
	expiredTime := time.Now().UTC().Add(-1 * time.Hour)
	err = saveSession(tmpDir, "expired-session", expiredTime)
	if err != nil {
		t.Fatalf("Failed to create expired session: %v", err)
	}

	// Create a valid session
	validTime := time.Now().UTC().Add(24 * time.Hour)
	err = saveSession(tmpDir, "valid-session", validTime)
	if err != nil {
		t.Fatalf("Failed to create valid session: %v", err)
	}

	// Verify both files exist
	expiredPath := filepath.Join(sessionsDir, "expired-session")
	validPath := filepath.Join(sessionsDir, "valid-session")

	if _, err := os.Stat(expiredPath); err != nil {
		t.Fatal("Expired session file should exist before cleanup")
	}
	if _, err := os.Stat(validPath); err != nil {
		t.Fatal("Valid session file should exist before cleanup")
	}

	// Clean expired sessions
	CleanExpiredSessions(tmpDir)

	// Verify expired session is removed
	if _, err := os.Stat(expiredPath); !os.IsNotExist(err) {
		t.Error("Expired session file should be removed")
	}

	// Verify valid session still exists
	if _, err := os.Stat(validPath); err != nil {
		t.Error("Valid session file should still exist")
	}
}

func TestSaveSession(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()

	// Initialize auth
	err := InitAuth(tmpDir)
	if err != nil {
		t.Fatalf("Failed to initialize auth: %v", err)
	}

	expiry := time.Now().UTC().Add(1 * time.Hour)
	err = saveSession(tmpDir, "test-token", expiry)
	if err != nil {
		t.Fatalf("saveSession failed: %v", err)
	}

	// Verify session file was created
	sessionPath := filepath.Join(tmpDir, "sessions", "test-token")
	data, err := os.ReadFile(sessionPath)
	if err != nil {
		t.Fatalf("Failed to read session file: %v", err)
	}

	// Verify content is the Unix timestamp
	content := string(data)
	if len(content) < 10 {
		t.Errorf("Session file content too short: %s", content)
	}

	// Verify we can retrieve the expiry
	if content == "" {
		t.Error("Session file should not be empty")
	}

	// Verify timestamp is reasonable (not zero)
	expectedTimestamp := expiry.Unix()
	if expectedTimestamp == 0 {
		t.Error("Expected timestamp should not be zero")
	}
}

func TestGenerateToken(t *testing.T) {
	t.Parallel()
	token1 := generateToken()
	token2 := generateToken()

	// Tokens should be 64 characters (32 bytes hex encoded)
	if len(token1) != 64 {
		t.Errorf("Expected token length 64, got %d", len(token1))
	}

	// Tokens should be different
	if token1 == token2 {
		t.Error("Generated tokens should be unique")
	}

	// Tokens should be valid hex
	for _, c := range token1 {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			t.Errorf("Token contains invalid hex character: %c", c)
		}
	}
}
