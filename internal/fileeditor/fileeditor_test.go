package fileeditor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadFileNonExistent(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.txt")

	session, err := ReadFile(filePath)
	if err != nil {
		t.Fatalf("Expected no error for non-existent file, got: %v", err)
	}

	if session.FilePath != filePath {
		t.Errorf("Expected FilePath %s, got %s", filePath, session.FilePath)
	}

	if session.OriginalContent != "" {
		t.Errorf("Expected empty content for non-existent file, got: %s", session.OriginalContent)
	}

	if session.OriginalChecksum != calculateChecksum("") {
		t.Errorf("Expected checksum of empty string, got: %s", session.OriginalChecksum)
	}
}

func TestReadFileExisting(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.txt")
	content := "Hello, World!"

	// Create test file
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	session, err := ReadFile(filePath)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if session.OriginalContent != content {
		t.Errorf("Expected content %s, got %s", content, session.OriginalContent)
	}

	expectedChecksum := calculateChecksum(content)
	if session.OriginalChecksum != expectedChecksum {
		t.Errorf("Expected checksum %s, got %s", expectedChecksum, session.OriginalChecksum)
	}
}

func TestWriteFileNewFile(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "newdir", "test.txt")
	content := "New file content"

	// Create session for non-existent file
	session, err := ReadFile(filePath)
	if err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	// Write file
	result, err := WriteFile(session, content)
	if err != nil {
		t.Fatalf("Failed to write file: %v", err)
	}

	if !result.Success {
		t.Errorf("Expected success, got: %v", result.Message)
	}

	if result.ConflictDetected {
		t.Errorf("Expected no conflict for new file")
	}

	// Verify file was created
	readContent, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("Failed to read created file: %v", err)
	}

	if string(readContent) != content {
		t.Errorf("Expected content %s, got %s", content, string(readContent))
	}

	// Verify parent directory was created
	if _, err := os.Stat(filepath.Dir(filePath)); os.IsNotExist(err) {
		t.Errorf("Parent directory was not created")
	}
}

func TestWriteFileShebang(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "script.sh")
	content := "#!/bin/bash\necho 'Hello'"

	session, err := ReadFile(filePath)
	if err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	result, err := WriteFile(session, content)
	if err != nil {
		t.Fatalf("Failed to write file: %v", err)
	}

	if !result.Success {
		t.Errorf("Expected success, got: %v", result.Message)
	}

	// Check if file is executable
	info, err := os.Stat(filePath)
	if err != nil {
		t.Fatalf("Failed to stat file: %v", err)
	}

	mode := info.Mode()
	if mode&0111 == 0 {
		t.Errorf("Expected file to be executable, got mode: %v", mode)
	}

	if !strings.Contains(result.Message, "executable") {
		t.Errorf("Expected message to mention executable, got: %s", result.Message)
	}
}

func TestWriteFileConflictDetection(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.txt")
	originalContent := "Original content"
	externalContent := "Externally modified content"
	userContent := "User's modified content"

	// Create original file
	if err := os.WriteFile(filePath, []byte(originalContent), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Create session
	session, err := ReadFile(filePath)
	if err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	// Simulate external modification
	if err := os.WriteFile(filePath, []byte(externalContent), 0644); err != nil {
		t.Fatalf("Failed to modify file externally: %v", err)
	}

	// Try to write user's content
	result, err := WriteFile(session, userContent)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if result.Success {
		t.Errorf("Expected failure due to conflict")
	}

	if !result.ConflictDetected {
		t.Errorf("Expected conflict to be detected")
	}

	if result.ExternalDiff == "" {
		t.Errorf("Expected external diff to be generated")
	}

	if result.ProposedDiff == "" {
		t.Errorf("Expected proposed diff to be generated")
	}

	// Verify file was NOT modified
	currentContent, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("Failed to read file: %v", err)
	}

	if string(currentContent) != externalContent {
		t.Errorf("Expected file to remain as externally modified, got: %s", string(currentContent))
	}
}

func TestWriteFileNoConflict(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.txt")
	originalContent := "Original content"
	newContent := "New content"

	// Create original file
	if err := os.WriteFile(filePath, []byte(originalContent), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Create session
	session, err := ReadFile(filePath)
	if err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	// Write new content (no external modification)
	result, err := WriteFile(session, newContent)
	if err != nil {
		t.Fatalf("Failed to write file: %v", err)
	}

	if !result.Success {
		t.Errorf("Expected success, got: %v", result.Message)
	}

	if result.ConflictDetected {
		t.Errorf("Expected no conflict")
	}

	// Verify file was modified
	currentContent, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("Failed to read file: %v", err)
	}

	if string(currentContent) != newContent {
		t.Errorf("Expected content %s, got %s", newContent, string(currentContent))
	}

	// Verify diff was generated
	if result.ProposedDiff == "" {
		t.Errorf("Expected proposed diff to be generated")
	}
}

func TestGenerateDiff(t *testing.T) {
	original := "line1\nline2\nline3"
	current := "line1\nmodified line2\nline3"

	diff := GenerateDiff(original, current)

	if !strings.Contains(diff, "-line2") {
		t.Errorf("Expected diff to contain removed line, got: %s", diff)
	}

	if !strings.Contains(diff, "+modified line2") {
		t.Errorf("Expected diff to contain added line, got: %s", diff)
	}
}

func TestGenerateDiffIdentical(t *testing.T) {
	content := "line1\nline2\nline3"
	diff := GenerateDiff(content, content)

	if diff != "No differences" {
		t.Errorf("Expected 'No differences', got: %s", diff)
	}
}

func TestCalculateChecksum(t *testing.T) {
	content := "test content"
	checksum1 := calculateChecksum(content)
	checksum2 := calculateChecksum(content)

	if checksum1 != checksum2 {
		t.Errorf("Expected same checksum for same content")
	}

	differentContent := "different content"
	checksum3 := calculateChecksum(differentContent)

	if checksum1 == checksum3 {
		t.Errorf("Expected different checksums for different content")
	}
}
