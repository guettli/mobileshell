package claude

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestBuildCommand_Basic(t *testing.T) {
	prompt := "Explain this code"
	opts := CommandOptions{
		StreamJSON: false,
		NoSession:  false,
	}

	result := BuildCommand(prompt, opts)

	// Should have at least the prompt
	if len(result) < 1 {
		t.Errorf("Expected at least 1 argument, got %d", len(result))
	}

	// Should NOT have -p flag (always dialog mode)
	for _, arg := range result {
		if arg == "-p" {
			t.Error("Should not have -p flag (always interactive dialog mode)")
		}
	}

	if result[len(result)-1] != prompt {
		t.Errorf("Expected last arg to be prompt '%s', got '%s'", prompt, result[len(result)-1])
	}
}

func TestBuildCommand_WithStreamJSON(t *testing.T) {
	prompt := "Test prompt"
	opts := CommandOptions{
		StreamJSON: true,
		NoSession:  false,
	}

	result := BuildCommand(prompt, opts)

	// Check for streaming flags
	hasStreamFlag := false
	hasVerboseFlag := false

	for _, arg := range result {
		if arg == "--output-format=stream-json" {
			hasStreamFlag = true
		}
		if arg == "--verbose" {
			hasVerboseFlag = true
		}
	}

	if !hasStreamFlag {
		t.Error("Expected --output-format=stream-json flag when StreamJSON is true")
	}

	if !hasVerboseFlag {
		t.Error("Expected --verbose flag when StreamJSON is true")
	}
}

func TestBuildCommand_WithNoSession(t *testing.T) {
	prompt := "Test prompt"
	opts := CommandOptions{
		StreamJSON: false,
		NoSession:  true,
	}

	result := BuildCommand(prompt, opts)

	// Check for no-session flag
	hasNoSessionFlag := false
	for _, arg := range result {
		if arg == "--no-session-persistence" {
			hasNoSessionFlag = true
			break
		}
	}

	if !hasNoSessionFlag {
		t.Error("Expected --no-session-persistence flag when NoSession is true")
	}
}

func TestBuildCommand_AllOptions(t *testing.T) {
	prompt := "Complex prompt"
	opts := CommandOptions{
		StreamJSON: true,
		NoSession:  true,
		WorkDir:    "/some/path",
	}

	result := BuildCommand(prompt, opts)

	// Verify all flags are present (no -p flag in dialog mode)
	expectedFlags := map[string]bool{
		"--output-format=stream-json": false,
		"--verbose":                   false,
		"--no-session-persistence":    false,
	}

	for _, arg := range result {
		if _, exists := expectedFlags[arg]; exists {
			expectedFlags[arg] = true
		}
	}

	for flag, found := range expectedFlags {
		if !found {
			t.Errorf("Expected flag '%s' not found in command", flag)
		}
	}

	// Verify prompt is last
	if result[len(result)-1] != prompt {
		t.Errorf("Expected prompt to be last argument, got '%s'", result[len(result)-1])
	}
}

func TestBuildCommand_OrderMatters(t *testing.T) {
	prompt := "Test order"
	opts := CommandOptions{
		StreamJSON: true,
		NoSession:  true,
	}

	result := BuildCommand(prompt, opts)

	// Should not have -p flag
	if len(result) > 0 && result[0] == "-p" {
		t.Error("Should not have -p flag (always dialog mode)")
	}

	// Last arg should be prompt
	if result[len(result)-1] != prompt {
		t.Errorf("Expected last arg to be prompt, got '%s'", result[len(result)-1])
	}
}

func TestIsClaudeAvailable_MockExecutable(t *testing.T) {
	// Create a temporary directory for our mock claude
	tmpDir := t.TempDir()

	// Create a mock claude executable
	mockClaudePath := filepath.Join(tmpDir, "claude")
	mockScript := `#!/bin/bash
echo "Mock Claude CLI"
`
	err := os.WriteFile(mockClaudePath, []byte(mockScript), 0755)
	if err != nil {
		t.Fatalf("Failed to create mock claude: %v", err)
	}

	// Temporarily modify PATH to include our mock directory
	originalPath := os.Getenv("PATH")
	newPath := tmpDir + ":" + originalPath
	if err := os.Setenv("PATH", newPath); err != nil {
		t.Fatalf("Failed to set PATH: %v", err)
	}
	defer func() {
		_ = os.Setenv("PATH", originalPath)
	}()

	// Now IsClaudeAvailable should return true
	if !IsClaudeAvailable() {
		t.Error("Expected IsClaudeAvailable to return true with mock executable in PATH")
	}
}

func TestIsClaudeAvailable_NotInPath(t *testing.T) {
	// Save original PATH
	originalPath := os.Getenv("PATH")

	// Set PATH to empty (claude definitely won't be here)
	if err := os.Setenv("PATH", ""); err != nil {
		t.Fatalf("Failed to set PATH: %v", err)
	}
	defer func() {
		_ = os.Setenv("PATH", originalPath)
	}()

	// IsClaudeAvailable should return false
	if IsClaudeAvailable() {
		t.Error("Expected IsClaudeAvailable to return false when PATH is empty")
	}
}

func TestMockClaudeExecution(t *testing.T) {
	// Create a mock claude that outputs markdown
	tmpDir := t.TempDir()
	mockClaudePath := filepath.Join(tmpDir, "claude")
	mockScript := `#!/bin/bash
# Mock Claude CLI that outputs markdown
cat <<'EOF'
# Mock Response

This is a **mock** response from Claude.

## Features

- Item 1
- Item 2
- Item 3

` + "```" + `go
func main() {
    fmt.Println("Hello")
}
` + "```" + `
EOF
`
	err := os.WriteFile(mockClaudePath, []byte(mockScript), 0755)
	if err != nil {
		t.Fatalf("Failed to create mock claude: %v", err)
	}

	// Build command
	opts := CommandOptions{
		StreamJSON: false,
		NoSession:  true,
	}
	args := BuildCommand("test prompt", opts)

	// Execute mock claude
	cmd := exec.Command(mockClaudePath, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Failed to execute mock claude: %v", err)
	}

	// Verify output contains markdown
	outputStr := string(output)
	if !contains(outputStr, "# Mock Response") {
		t.Error("Expected output to contain markdown header")
	}
	if !contains(outputStr, "**mock**") {
		t.Error("Expected output to contain bold markdown")
	}
	if !contains(outputStr, "- Item") {
		t.Error("Expected output to contain list items")
	}
}

// Helper function to check if string contains substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 || findSubstring(s, substr))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
