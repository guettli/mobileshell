package nohup

import (
	"strings"
)

// OutputType represents the detected type of output
type OutputType string

const (
	OutputTypeUnknown    OutputType = "unknown"
	OutputTypeBinary     OutputType = "binary"
	OutputTypeText       OutputType = "text"
	OutputTypeFullscreen OutputType = "fullscreen"
	OutputTypeInk        OutputType = "ink"
)

// OutputTypeDetector continuously analyzes stdout to detect output type
// Note: This is accessed only from a single goroutine (readLinesWithDetection),
// so no synchronization is needed
type OutputTypeDetector struct {
	detectedType    OutputType
	detectionReason string
	detected        bool
	buffer          []byte
	maxBufferSize   int

	// Counters for heuristics
	hasAlternateScreen bool
	hasClearScreen     bool
	hasCursorMovement  bool
	hasColorCodes      bool
	lineCount          int
}

// NewOutputTypeDetector creates a new output type detector
func NewOutputTypeDetector() *OutputTypeDetector {
	return &OutputTypeDetector{
		detectedType:  OutputTypeUnknown,
		maxBufferSize: 8192, // Analyze up to 8KB of initial output
	}
}

// AnalyzeLine analyzes a line of output and updates detection state
// Returns true if type has been detected (stop calling after this)
func (d *OutputTypeDetector) AnalyzeLine(line string) bool {
	if d.detected {
		return true
	}

	// Add to buffer for analysis
	d.buffer = append(d.buffer, []byte(line)...)
	d.lineCount++

	// Check for binary data first (highest priority)
	if d.isBinaryData(line) {
		d.detectedType = OutputTypeBinary
		d.detectionReason = "null bytes or high proportion of non-printable characters detected"
		d.detected = true
		return true
	}

	// Check for ANSI escape sequences
	d.detectANSISequences(line)

	// Determine type based on detected patterns
	if d.hasAlternateScreen || d.hasClearScreen {
		// Fullscreen applications use alternate screen buffer or clear entire screen
		d.detectedType = OutputTypeFullscreen
		if d.hasAlternateScreen {
			d.detectionReason = "alternate screen buffer escape sequence detected"
		} else {
			d.detectionReason = "clear screen escape sequence detected"
		}
		d.detected = true
		return true
	}

	// If we've seen enough output, make a determination
	if len(d.buffer) >= d.maxBufferSize || d.lineCount >= 50 {
		if d.hasColorCodes || d.hasCursorMovement {
			// Ink-based applications use ANSI codes but not fullscreen sequences
			d.detectedType = OutputTypeInk
			d.detectionReason = "ANSI color codes or cursor movement without fullscreen sequences"
		} else {
			// No special sequences detected, treat as plain text
			d.detectedType = OutputTypeText
			d.detectionReason = "no special terminal control sequences detected"
		}
		d.detected = true
		return true
	}

	return false
}

// GetDetectedType returns the detected type and reason
func (d *OutputTypeDetector) GetDetectedType() (OutputType, string) {
	return d.detectedType, d.detectionReason
}

// IsDetected returns true if type has been determined
func (d *OutputTypeDetector) IsDetected() bool {
	return d.detected
}

// isBinaryData checks if a line contains binary data
func (d *OutputTypeDetector) isBinaryData(line string) bool {
	if len(line) == 0 {
		return false
	}

	nonPrintableCount := 0
	for _, r := range line {
		// Check for null bytes - definitive indicator of binary data
		if r == 0 {
			return true
		}
		// Count non-printable characters (excluding ANSI escape sequences and common whitespace)
		// Note: We exclude 0x1B (ESC) from non-printable count since it's used in ANSI sequences
		if r < 32 && r != '\t' && r != '\n' && r != '\r' && r != 0x1B {
			nonPrintableCount++
		} else if r > 126 && r < 160 {
			// Control characters in extended ASCII
			nonPrintableCount++
		}
	}

	// If more than 30% of characters are non-printable, consider it binary
	threshold := float64(len(line)) * 0.3
	return float64(nonPrintableCount) > threshold
}

// detectANSISequences scans for ANSI escape sequences in the line
func (d *OutputTypeDetector) detectANSISequences(line string) {
	// Look for ESC character (0x1B or \x1b)
	if !strings.Contains(line, "\x1b[") {
		return
	}

	// Check for alternate screen buffer sequences
	// \x1b[?1049h = switch to alternate screen buffer
	// \x1b[?1047h = another alternate screen sequence
	// \x1b[?47h = yet another alternate screen sequence
	if strings.Contains(line, "\x1b[?1049h") ||
		strings.Contains(line, "\x1b[?1047h") ||
		strings.Contains(line, "\x1b[?47h") {
		d.hasAlternateScreen = true
		return
	}

	// Check for clear screen sequences
	// \x1b[2J = clear entire screen
	// \x1b[H\x1b[2J = move cursor to home and clear screen (common pattern)
	if strings.Contains(line, "\x1b[2J") || strings.Contains(line, "\x1b[3J") {
		d.hasClearScreen = true
		return
	}

	// Check for cursor positioning (common in both fullscreen and ink apps)
	// \x1b[H = cursor home
	// \x1b[<n>;<m>H = cursor position
	// \x1b[A/B/C/D = cursor up/down/forward/back
	if strings.Contains(line, "\x1b[H") ||
		strings.Contains(line, "\x1b[A") ||
		strings.Contains(line, "\x1b[B") ||
		strings.Contains(line, "\x1b[C") ||
		strings.Contains(line, "\x1b[D") ||
		containsCursorPosition(line) {
		d.hasCursorMovement = true
	}

	// Check for color codes (common in ink-based apps)
	// \x1b[<n>m = SGR (Select Graphic Rendition)
	if containsSGR(line) {
		d.hasColorCodes = true
	}
}

// containsCursorPosition checks for cursor position escape sequences like \x1b[<n>;<m>H
func containsCursorPosition(line string) bool {
	idx := strings.Index(line, "\x1b[")
	for idx != -1 && idx+2 < len(line) {
		// Look for pattern: ESC [ <digits> ; <digits> H
		j := idx + 2
		hasDigit := false
		for j < len(line) && line[j] >= '0' && line[j] <= '9' {
			hasDigit = true
			j++
		}
		if hasDigit && j < len(line) && (line[j] == ';' || line[j] == 'H') {
			if line[j] == ';' {
				j++
				for j < len(line) && line[j] >= '0' && line[j] <= '9' {
					j++
				}
			}
			if j < len(line) && line[j] == 'H' {
				return true
			}
		}
		// Look for next ESC[
		idx = strings.Index(line[idx+2:], "\x1b[")
		if idx != -1 {
			idx += idx + 2
		}
	}
	return false
}

// containsSGR checks for SGR (Select Graphic Rendition) escape sequences like \x1b[<n>m
func containsSGR(line string) bool {
	idx := strings.Index(line, "\x1b[")
	for idx != -1 && idx+2 < len(line) {
		// Look for pattern: ESC [ <digits or semicolons> m
		j := idx + 2
		hasContent := false
		for j < len(line) && (line[j] >= '0' && line[j] <= '9' || line[j] == ';') {
			hasContent = true
			j++
		}
		if hasContent && j < len(line) && line[j] == 'm' {
			return true
		}
		// Look for next ESC[
		remaining := line[idx+2:]
		nextIdx := strings.Index(remaining, "\x1b[")
		if nextIdx != -1 {
			idx = idx + 2 + nextIdx
		} else {
			idx = -1
		}
	}
	return false
}
