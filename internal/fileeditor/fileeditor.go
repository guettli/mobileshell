package fileeditor

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// FileSession represents an editing session for a file
type FileSession struct {
	FilePath         string    `json:"file_path"`
	OriginalContent  string    `json:"-"` // Not exposed in JSON for security
	OriginalChecksum string    `json:"original_checksum"`
	LastModified     time.Time `json:"last_modified"`
	SessionID        string    `json:"session_id"`
}

// FileEditRequest represents a request to save file content
type FileEditRequest struct {
	FilePath         string `json:"file_path"`
	NewContent       string `json:"new_content"`
	OriginalChecksum string `json:"original_checksum"`
}

// FileEditResult represents the result of a file edit operation
type FileEditResult struct {
	Success          bool     `json:"success"`
	Message          string   `json:"message"`
	ConflictDetected bool     `json:"conflict_detected"`
	ExternalDiff     string   `json:"external_diff,omitempty"`
	ProposedDiff     string   `json:"proposed_diff,omitempty"`
	NewChecksum      string   `json:"new_checksum,omitempty"`
}

// ReadFile reads a file and creates a new editing session
func ReadFile(filePath string) (*FileSession, error) {
	// Check if file exists
	info, err := os.Stat(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			// File doesn't exist, return empty session for new file
			return &FileSession{
				FilePath:         filePath,
				OriginalContent:  "",
				OriginalChecksum: calculateChecksum(""),
				LastModified:     time.Time{},
				SessionID:        generateSessionID(filePath),
			}, nil
		}
		return nil, fmt.Errorf("failed to stat file: %w", err)
	}

	// Read file content
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	contentStr := string(content)
	checksum := calculateChecksum(contentStr)

	return &FileSession{
		FilePath:         filePath,
		OriginalContent:  contentStr,
		OriginalChecksum: checksum,
		LastModified:     info.ModTime(),
		SessionID:        generateSessionID(filePath),
	}, nil
}

// WriteFile writes content to a file with conflict detection
func WriteFile(session *FileSession, newContent string) (*FileEditResult, error) {
	result := &FileEditResult{
		Success: false,
	}

	// Check if file exists
	_, err := os.Stat(session.FilePath)
	fileExists := err == nil

	if fileExists {
		// File exists, check for external changes
		currentContent, err := os.ReadFile(session.FilePath)
		if err != nil {
			return nil, fmt.Errorf("failed to read current file: %w", err)
		}

		currentChecksum := calculateChecksum(string(currentContent))

		// Check if file has been modified externally
		if currentChecksum != session.OriginalChecksum {
			result.ConflictDetected = true
			result.Message = "File has been modified externally. Please review the differences."

			// Generate diff between original and current (external changes)
			result.ExternalDiff = generateDiff(session.OriginalContent, string(currentContent))

			// Generate diff between original and proposed (user's changes)
			result.ProposedDiff = generateDiff(session.OriginalContent, newContent)

			return result, nil
		}
	}

	// No conflict, proceed with write
	// Create parent directories if they don't exist
	dir := filepath.Dir(session.FilePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create parent directories: %w", err)
	}

	// Write the file
	if err := os.WriteFile(session.FilePath, []byte(newContent), 0644); err != nil {
		return nil, fmt.Errorf("failed to write file: %w", err)
	}

	// Auto-chmod if file starts with shebang
	if strings.HasPrefix(newContent, "#!") {
		if err := os.Chmod(session.FilePath, 0755); err != nil {
			return nil, fmt.Errorf("failed to make file executable: %w", err)
		}
		result.Message = "File saved successfully and made executable (starts with shebang)"
	} else {
		result.Message = "File saved successfully"
	}

	result.Success = true
	result.NewChecksum = calculateChecksum(newContent)
	result.ProposedDiff = generateDiff(session.OriginalContent, newContent)

	return result, nil
}

// calculateChecksum calculates SHA256 checksum of content
func calculateChecksum(content string) string {
	hash := sha256.Sum256([]byte(content))
	return hex.EncodeToString(hash[:])
}

// generateSessionID generates a unique session ID for a file path
func generateSessionID(filePath string) string {
	data := fmt.Sprintf("%s:%d", filePath, time.Now().UnixNano())
	hash := sha256.Sum256([]byte(data))
	return hex.EncodeToString(hash[:])[:16]
}

// generateDiff generates a simple unified diff between two strings
func generateDiff(original, current string) string {
	originalLines := strings.Split(original, "\n")
	currentLines := strings.Split(current, "\n")

	var diff strings.Builder
	diff.WriteString("--- original\n")
	diff.WriteString("+++ current\n")

	// Simple line-by-line diff
	maxLen := len(originalLines)
	if len(currentLines) > maxLen {
		maxLen = len(currentLines)
	}

	// Find first difference
	firstDiff := -1
	for i := 0; i < len(originalLines) && i < len(currentLines); i++ {
		if originalLines[i] != currentLines[i] {
			firstDiff = i
			break
		}
	}

	// If no difference in common lines, check if one is longer
	if firstDiff == -1 {
		if len(originalLines) != len(currentLines) {
			firstDiff = min(len(originalLines), len(currentLines))
		} else {
			// Files are identical
			return "No differences"
		}
	}

	// Find last difference
	lastDiff := maxLen
	for i := 0; i < len(originalLines) && i < len(currentLines); i++ {
		origIdx := len(originalLines) - 1 - i
		currIdx := len(currentLines) - 1 - i
		if origIdx < 0 || currIdx < 0 {
			break
		}
		if originalLines[origIdx] != currentLines[currIdx] {
			lastDiff = max(origIdx, currIdx) + 1
			break
		}
	}

	// Show context (3 lines before)
	contextStart := max(0, firstDiff-3)
	contextEnd := min(maxLen, lastDiff+3)

	diff.WriteString(fmt.Sprintf("@@ -%d,%d +%d,%d @@\n",
		contextStart+1, contextEnd-contextStart,
		contextStart+1, contextEnd-contextStart))

	for i := contextStart; i < contextEnd; i++ {
		if i < len(originalLines) && i < len(currentLines) {
			if originalLines[i] == currentLines[i] {
				diff.WriteString(" " + originalLines[i] + "\n")
			} else {
				diff.WriteString("-" + originalLines[i] + "\n")
				diff.WriteString("+" + currentLines[i] + "\n")
			}
		} else if i < len(originalLines) {
			diff.WriteString("-" + originalLines[i] + "\n")
		} else if i < len(currentLines) {
			diff.WriteString("+" + currentLines[i] + "\n")
		}
	}

	return diff.String()
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
