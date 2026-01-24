package fileeditor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// FileSession represents an editing session for a file
type FileSession struct {
	FilePath         string    `json:"file_path"`
	OriginalContent  string    `json:"-"` // Not exposed in JSON to avoid large payloads
	OriginalChecksum string    `json:"original_checksum"`
	LastModified     time.Time `json:"last_modified"`
}

// FileEditRequest represents a request to save file content
type FileEditRequest struct {
	FilePath         string `json:"file_path"`
	NewContent       string `json:"new_content"`
	OriginalChecksum string `json:"original_checksum"`
}

// FileEditResult represents the result of a file edit operation
type FileEditResult struct {
	Success          bool   `json:"success"`
	Message          string `json:"message"`
	ConflictDetected bool   `json:"conflict_detected"`
	ExternalDiff     string `json:"external_diff,omitempty"`
	ProposedDiff     string `json:"proposed_diff,omitempty"`
	NewChecksum      string `json:"new_checksum,omitempty"`
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
			result.ExternalDiff = GenerateDiff(session.OriginalContent, string(currentContent))

			// Generate diff between original and proposed (user's changes)
			result.ProposedDiff = GenerateDiff(session.OriginalContent, newContent)

			return result, nil
		}
	}

	// No conflict, proceed with write
	// Create parent directories if they don't exist
	dir := filepath.Dir(session.FilePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create parent directories: %w", err)
	}

	// Write the file
	if err := os.WriteFile(session.FilePath, []byte(newContent), 0o644); err != nil {
		return nil, fmt.Errorf("failed to write file: %w", err)
	}

	// Auto-chmod if file starts with shebang
	if strings.HasPrefix(newContent, "#!") {
		if err := os.Chmod(session.FilePath, 0o755); err != nil {
			return nil, fmt.Errorf("failed to make file executable: %w", err)
		}
		result.Message = "File saved successfully and made executable (starts with shebang)"
	} else {
		result.Message = "File saved successfully"
	}

	result.Success = true
	result.NewChecksum = calculateChecksum(newContent)
	result.ProposedDiff = GenerateDiff(session.OriginalContent, newContent)

	return result, nil
}

// calculateChecksum calculates SHA256 checksum of content
func calculateChecksum(content string) string {
	hash := sha256.Sum256([]byte(content))
	return hex.EncodeToString(hash[:])
}

// GenerateDiff generates a simple unified diff between two strings
func GenerateDiff(original, current string) string {
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

// FileMatch represents a file that matches the autocomplete pattern
type FileMatch struct {
	Path         string    `json:"path"`
	RelativePath string    `json:"relative_path"`
	ModTime      time.Time `json:"mod_time"`
}

// AutocompleteResult represents the result of an autocomplete search
type AutocompleteResult struct {
	Matches      []FileMatch `json:"matches"`
	TotalMatches int         `json:"total_matches"`
	HasMore      bool        `json:"has_more"`
	TimedOut     bool        `json:"timed_out"`
}

// SearchFiles searches for files matching a glob pattern with timeout support
// Supports **, *, ?, and ~ for home directory
func SearchFiles(ctx context.Context, rootDir, pattern string, maxResults int) (*AutocompleteResult, error) {
	// Default max results to 10
	if maxResults <= 0 {
		maxResults = 10
	}

	// Transform pattern to add wildcards for better UX
	// If pattern starts with **, handle it specially
	if strings.HasPrefix(pattern, "**") {
		rest := pattern[2:]
		if len(rest) > 0 && !strings.ContainsAny(rest, "*?") {
			// If the rest doesn't contain wildcards, add them
			pattern = "**/*" + rest + "*"
		}
	} else if !strings.Contains(pattern, "*") && !strings.Contains(pattern, "?") && !strings.Contains(pattern, "~") {
		// Otherwise, add *pattern* for simple wildcard match (only if no wildcards exist)
		pattern = "*" + pattern + "*"
	}

	// Expand ~ to home directory
	if strings.HasPrefix(pattern, "~") {
		homeDir, err := os.UserHomeDir()
		if err == nil {
			pattern = strings.Replace(pattern, "~", homeDir, 1)
		}
	}

	// If pattern is absolute, use it as-is; otherwise join with rootDir
	searchPattern := pattern
	if !filepath.IsAbs(pattern) {
		searchPattern = filepath.Join(rootDir, pattern)
	}

	var matches []FileMatch
	var timedOut bool

	// Check if pattern contains **
	if strings.Contains(searchPattern, "**") {
		// Recursive search
		matches, timedOut = recursiveSearch(ctx, rootDir, searchPattern)
	} else {
		// Simple glob search
		matches, timedOut = simpleGlobSearch(ctx, rootDir, searchPattern)
	}

	// Sort by modification time, most recent first
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].ModTime.After(matches[j].ModTime)
	})

	totalMatches := len(matches)
	hasMore := totalMatches > maxResults

	// Limit to maxResults
	if hasMore {
		matches = matches[:maxResults]
	}

	return &AutocompleteResult{
		Matches:      matches,
		TotalMatches: totalMatches,
		HasMore:      hasMore,
		TimedOut:     timedOut,
	}, nil
}

// simpleGlobSearch performs a simple glob match (no **)
func simpleGlobSearch(ctx context.Context, rootDir, pattern string) ([]FileMatch, bool) {
	matches := []FileMatch{}
	timedOut := false

	// Check for timeout
	select {
	case <-ctx.Done():
		return matches, true
	default:
	}

	globMatches, err := filepath.Glob(pattern)
	if err != nil {
		return matches, false
	}

	for _, path := range globMatches {
		// Check for timeout in loop
		select {
		case <-ctx.Done():
			timedOut = true
			return matches, timedOut
		default:
		}

		info, err := os.Stat(path)
		if err != nil {
			continue
		}

		// Skip directories (only show files)
		if info.IsDir() {
			continue
		}

		relPath, err := filepath.Rel(rootDir, path)
		if err != nil {
			relPath = path
		}

		matches = append(matches, FileMatch{
			Path:         path,
			RelativePath: relPath,
			ModTime:      info.ModTime(),
		})
	}

	return matches, timedOut
}

// recursiveSearch performs a recursive search for ** patterns
func recursiveSearch(ctx context.Context, rootDir, pattern string) ([]FileMatch, bool) {
	matches := []FileMatch{}
	timedOut := false

	// Convert ** pattern to a function that can match paths
	// For example: "**/*.go" should match all .go files recursively
	patternParts := strings.Split(pattern, "**")
	if len(patternParts) != 2 {
		// Invalid ** pattern, fallback to simple glob
		return simpleGlobSearch(ctx, rootDir, pattern)
	}

	prefix := patternParts[0]
	suffix := patternParts[1]

	// Start directory is the prefix (or rootDir if prefix is empty or ends with /)
	startDir := prefix
	if startDir == "" {
		startDir = rootDir
	} else if strings.HasSuffix(startDir, string(filepath.Separator)) {
		startDir = strings.TrimSuffix(startDir, string(filepath.Separator))
	} else {
		// If prefix doesn't end with /, use its directory
		startDir = filepath.Dir(startDir)
	}

	// Walk the directory tree
	err := filepath.WalkDir(startDir, func(path string, d os.DirEntry, err error) error {
		// Check for timeout
		select {
		case <-ctx.Done():
			timedOut = true
			return filepath.SkipAll
		default:
		}

		if err != nil {
			return nil // Skip errors, continue walking
		}

		// Skip directories
		if d.IsDir() {
			return nil
		}

		// Check if the path matches the suffix pattern
		if suffix != "" {
			// Build the pattern to match against
			matchPattern := path
			if prefix != "" {
				// If there was a prefix, only match the part after the prefix
				if strings.HasPrefix(path, prefix) {
					remainingPath := strings.TrimPrefix(path, prefix)
					matchPattern = remainingPath
				} else {
					return nil
				}
			}

			// Match against the suffix pattern
			matched, err := filepath.Match(strings.TrimPrefix(suffix, string(filepath.Separator)), filepath.Base(matchPattern))
			if err != nil || !matched {
				// If simple base match fails, try matching the full remaining path
				if suffix != "" && strings.Contains(suffix, string(filepath.Separator)) {
					// Full path pattern matching
					matched, err = filepath.Match(strings.TrimPrefix(suffix, string(filepath.Separator)), strings.TrimPrefix(matchPattern, string(filepath.Separator)))
					if err != nil || !matched {
						return nil
					}
				} else {
					return nil
				}
			}
		}

		// Get file info for modification time
		info, err := d.Info()
		if err != nil {
			return nil
		}

		relPath, err := filepath.Rel(rootDir, path)
		if err != nil {
			relPath = path
		}

		matches = append(matches, FileMatch{
			Path:         path,
			RelativePath: relPath,
			ModTime:      info.ModTime(),
		})

		return nil
	})

	if err != nil && !timedOut {
		// Error during walk, but not due to timeout
		return matches, false
	}

	return matches, timedOut
}
