package outputtype

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
	OutputTypeMarkdown   OutputType = "markdown"
)

// Detector continuously analyzes stdout to detect output type
// Note: This is accessed only from a single goroutine,
// so no synchronization is needed
type Detector struct {
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

	// Markdown detection counters
	markdownHeaderCount    int
	markdownCodeBlockCount int
	markdownListCount      int
	markdownLinkCount      int
	markdownBoldCount      int
	markdownBlockquoteCount int
}

// NewDetector creates a new output type detector
func NewDetector() *Detector {
	return &Detector{
		detectedType:  OutputTypeUnknown,
		maxBufferSize: 8192, // Analyze up to 8KB of initial output
	}
}

// AnalyzeLine analyzes a line of output and updates detection state
// Returns true if type has been detected (stop calling after this)
func (d *Detector) AnalyzeLine(line string) bool {
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

	// Check for markdown patterns
	d.detectMarkdownPatterns(line)

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
		// Check if markdown patterns are strong enough to classify as markdown
		markdownScore := d.markdownHeaderCount + d.markdownCodeBlockCount +
			d.markdownListCount + d.markdownLinkCount +
			d.markdownBoldCount + d.markdownBlockquoteCount

		// If we have at least 3 markdown indicators, classify as markdown
		// Priority: markdown > ink > text (since markdown can coexist with ANSI codes)
		if markdownScore >= 3 {
			d.detectedType = OutputTypeMarkdown
			d.detectionReason = "significant markdown formatting detected"
			d.detected = true
			return true
		}

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
func (d *Detector) GetDetectedType() (OutputType, string) {
	return d.detectedType, d.detectionReason
}

// IsDetected returns true if type has been determined
func (d *Detector) IsDetected() bool {
	return d.detected
}

// isBinaryData checks if a line contains binary data
func (d *Detector) isBinaryData(line string) bool {
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
func (d *Detector) detectANSISequences(line string) {
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

// detectMarkdownPatterns scans for markdown formatting patterns in the line
func (d *Detector) detectMarkdownPatterns(line string) {
	trimmedLine := strings.TrimSpace(line)
	if len(trimmedLine) == 0 {
		return
	}

	// Check for headers (# Header, ## Header, etc.)
	if strings.HasPrefix(trimmedLine, "#") {
		// Count consecutive # characters
		hashCount := 0
		for i := 0; i < len(trimmedLine) && i < 6; i++ {
			if trimmedLine[i] == '#' {
				hashCount++
			} else {
				break
			}
		}
		// Valid markdown header if followed by space or at end of line
		if hashCount > 0 && hashCount <= 6 {
			if hashCount == len(trimmedLine) || (hashCount < len(trimmedLine) && trimmedLine[hashCount] == ' ') {
				d.markdownHeaderCount++
			}
		}
	}

	// Check for code blocks (```language or ~~~ or just ```)
	if strings.HasPrefix(trimmedLine, "```") || strings.HasPrefix(trimmedLine, "~~~") {
		d.markdownCodeBlockCount++
	}

	// Check for unordered lists (- item, * item, + item)
	if strings.HasPrefix(trimmedLine, "- ") || strings.HasPrefix(trimmedLine, "* ") || strings.HasPrefix(trimmedLine, "+ ") {
		d.markdownListCount++
	}

	// Check for ordered lists (1. item, 2. item, etc.)
	if len(trimmedLine) >= 3 {
		if trimmedLine[0] >= '0' && trimmedLine[0] <= '9' {
			// Find where digits end
			i := 1
			for i < len(trimmedLine) && trimmedLine[i] >= '0' && trimmedLine[i] <= '9' {
				i++
			}
			// Check if followed by ". "
			if i < len(trimmedLine)-1 && trimmedLine[i] == '.' && trimmedLine[i+1] == ' ' {
				d.markdownListCount++
			}
		}
	}

	// Check for markdown links [text](url)
	if strings.Contains(line, "](") {
		// Look for [text](url) pattern
		openBracket := strings.Index(line, "[")
		for openBracket != -1 {
			closeBracket := strings.Index(line[openBracket:], "]")
			if closeBracket != -1 {
				closeBracket += openBracket
				if closeBracket+1 < len(line) && line[closeBracket+1] == '(' {
					// Found potential link, look for closing paren
					closeParen := strings.Index(line[closeBracket+2:], ")")
					if closeParen != -1 {
						d.markdownLinkCount++
						break
					}
				}
			}
			// Look for next opening bracket
			remaining := line[openBracket+1:]
			nextIdx := strings.Index(remaining, "[")
			if nextIdx != -1 {
				openBracket = openBracket + 1 + nextIdx
			} else {
				break
			}
		}
	}

	// Check for bold/italic (**bold**, __bold**, *italic*, _italic_)
	if strings.Contains(line, "**") || strings.Contains(line, "__") {
		d.markdownBoldCount++
	}

	// Check for blockquotes (> quote)
	if strings.HasPrefix(trimmedLine, "> ") {
		d.markdownBlockquoteCount++
	}
}
