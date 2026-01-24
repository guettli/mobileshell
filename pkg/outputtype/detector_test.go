package outputtype

import (
	"strings"
	"testing"
)

func TestDetector_Binary(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "null byte",
			input: "binary\x00data",
		},
		{
			name:  "many non-printable characters",
			input: "\x01\x02\x03\x04\x05\x06\x07\x08",
		},
		{
			name:  "mixed binary and text (over 30% non-printable)",
			input: "text\x01\x02\x03\x04\x05",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := NewDetector()
			detected := d.AnalyzeLine(tt.input)

			if !detected {
				t.Error("Expected binary to be detected immediately")
			}

			outputType, reason := d.GetDetectedType()
			if outputType != OutputTypeBinary {
				t.Errorf("Expected OutputTypeBinary, got %s", outputType)
			}
			if !strings.Contains(reason, "non-printable") && !strings.Contains(reason, "null") {
				t.Errorf("Expected reason to mention non-printable or null, got: %s", reason)
			}
		})
	}
}

func TestDetector_Text(t *testing.T) {
	tests := []struct {
		name  string
		lines []string
	}{
		{
			name:  "plain text single line",
			lines: []string{"Hello, world!\n"},
		},
		{
			name: "plain text multiple lines",
			lines: []string{
				"Line 1\n",
				"Line 2\n",
				"Line 3\n",
			},
		},
		{
			name: "text with common punctuation",
			lines: []string{
				"This is a test.\n",
				"It has punctuation, and numbers: 123.\n",
				"Even some symbols: @#$%\n",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := NewDetector()

			// Feed many lines to trigger text detection
			for i := 0; i < 51; i++ {
				for _, line := range tt.lines {
					d.AnalyzeLine(line)
				}
			}

			if !d.IsDetected() {
				t.Error("Expected detection after many lines")
			}

			outputType, reason := d.GetDetectedType()
			if outputType != OutputTypeText {
				t.Errorf("Expected OutputTypeText, got %s (reason: %s)", outputType, reason)
			}
		})
	}
}

func TestDetector_Fullscreen_AlternateBuffer(t *testing.T) {
	tests := []struct {
		name     string
		sequence string
	}{
		{
			name:     "xterm alternate screen",
			sequence: "\x1b[?1049h",
		},
		{
			name:     "alternate screen 1047",
			sequence: "\x1b[?1047h",
		},
		{
			name:     "alternate screen 47",
			sequence: "\x1b[?47h",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := NewDetector()
			input := "Starting app" + tt.sequence + "content\n"
			detected := d.AnalyzeLine(input)

			if !detected {
				t.Error("Expected fullscreen to be detected immediately with alternate screen")
			}

			outputType, reason := d.GetDetectedType()
			if outputType != OutputTypeFullscreen {
				t.Errorf("Expected OutputTypeFullscreen, got %s", outputType)
			}
			if !strings.Contains(reason, "alternate screen") {
				t.Errorf("Expected reason to mention alternate screen, got: %s", reason)
			}
		})
	}
}

func TestDetector_Fullscreen_ClearScreen(t *testing.T) {
	tests := []struct {
		name     string
		sequence string
	}{
		{
			name:     "clear screen 2J",
			sequence: "\x1b[2J",
		},
		{
			name:     "clear screen 3J",
			sequence: "\x1b[3J",
		},
		{
			name:     "clear with cursor home",
			sequence: "\x1b[H\x1b[2J",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := NewDetector()
			input := "Starting" + tt.sequence + "\n"
			detected := d.AnalyzeLine(input)

			if !detected {
				t.Error("Expected fullscreen to be detected immediately with clear screen")
			}

			outputType, reason := d.GetDetectedType()
			if outputType != OutputTypeFullscreen {
				t.Errorf("Expected OutputTypeFullscreen, got %s", outputType)
			}
			if !strings.Contains(reason, "clear screen") {
				t.Errorf("Expected reason to mention clear screen, got: %s", reason)
			}
		})
	}
}

func TestDetector_Ink(t *testing.T) {
	tests := []struct {
		name  string
		lines []string
	}{
		{
			name: "color codes",
			lines: []string{
				"\x1b[32mGreen text\x1b[0m\n",
				"\x1b[1;31mBold red\x1b[0m\n",
				"Plain text\n",
			},
		},
		{
			name: "cursor movement",
			lines: []string{
				"Line 1\n",
				"\x1b[AMove up\n",
				"\x1b[BMove down\n",
			},
		},
		{
			name: "cursor positioning",
			lines: []string{
				"\x1b[1;1HTop left\n",
				"\x1b[10;20HPosition\n",
				"More text\n",
			},
		},
		{
			name: "mixed ANSI codes",
			lines: []string{
				"\x1b[1m\x1b[32mBold green\x1b[0m\n",
				"\x1b[DHere\n",
				"Normal text\n",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := NewDetector()

			// Feed lines multiple times to reach detection threshold
			for i := 0; i < 20; i++ {
				for _, line := range tt.lines {
					if d.AnalyzeLine(line) {
						break
					}
				}
				if d.IsDetected() {
					break
				}
			}

			if !d.IsDetected() {
				t.Error("Expected detection after processing lines")
			}

			outputType, reason := d.GetDetectedType()
			if outputType != OutputTypeInk {
				t.Errorf("Expected OutputTypeInk, got %s (reason: %s)", outputType, reason)
			}
			if !strings.Contains(reason, "ANSI") {
				t.Errorf("Expected reason to mention ANSI, got: %s", reason)
			}
		})
	}
}

func TestDetector_StopsAfterDetection(t *testing.T) {
	d := NewDetector()

	// Detect binary first
	d.AnalyzeLine("binary\x00data")

	if !d.IsDetected() {
		t.Fatal("Expected detection after binary data")
	}

	outputType, _ := d.GetDetectedType()
	if outputType != OutputTypeBinary {
		t.Fatalf("Expected OutputTypeBinary, got %s", outputType)
	}

	// Try to feed fullscreen sequences - should not change detection
	d.AnalyzeLine("\x1b[?1049h")

	outputType, _ = d.GetDetectedType()
	if outputType != OutputTypeBinary {
		t.Errorf("Detection should not change after being set, got %s", outputType)
	}
}

func TestDetector_EmptyInput(t *testing.T) {
	d := NewDetector()
	d.AnalyzeLine("")
	d.AnalyzeLine("")

	if d.IsDetected() {
		t.Error("Empty input should not trigger immediate detection")
	}
}

func TestDetector_BufferLimit(t *testing.T) {
	d := NewDetector()

	// Create a large line that exceeds buffer size
	largeLine := strings.Repeat("a", 9000)
	detected := d.AnalyzeLine(largeLine)

	if !detected {
		t.Error("Expected detection when buffer limit is exceeded")
	}

	outputType, _ := d.GetDetectedType()
	if outputType != OutputTypeText {
		t.Errorf("Expected OutputTypeText for plain text exceeding buffer, got %s", outputType)
	}
}

func TestDetector_LineCountLimit(t *testing.T) {
	d := NewDetector()

	// Feed exactly 50 lines of plain text
	for i := 0; i < 50; i++ {
		detected := d.AnalyzeLine("line\n")
		if i < 49 && detected {
			t.Errorf("Should not detect before line 50, detected at line %d", i+1)
		}
	}

	if !d.IsDetected() {
		t.Error("Expected detection after 50 lines")
	}

	outputType, _ := d.GetDetectedType()
	if outputType != OutputTypeText {
		t.Errorf("Expected OutputTypeText, got %s", outputType)
	}
}

func TestDetector_SGRDetection(t *testing.T) {
	tests := []struct {
		name      string
		line      string
		shouldHit bool
	}{
		{
			name:      "simple color",
			line:      "\x1b[31m",
			shouldHit: true,
		},
		{
			name:      "reset",
			line:      "\x1b[0m",
			shouldHit: true,
		},
		{
			name:      "multiple params",
			line:      "\x1b[1;32;40m",
			shouldHit: true,
		},
		{
			name:      "not SGR - cursor",
			line:      "\x1b[10H",
			shouldHit: false,
		},
		{
			name:      "text with SGR",
			line:      "Hello \x1b[31mRed\x1b[0m World",
			shouldHit: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := containsSGR(tt.line)
			if result != tt.shouldHit {
				t.Errorf("containsSGR(%q) = %v, want %v", tt.line, result, tt.shouldHit)
			}
		})
	}
}

func TestDetector_CursorPositionDetection(t *testing.T) {
	tests := []struct {
		name      string
		line      string
		shouldHit bool
	}{
		{
			name:      "simple position",
			line:      "\x1b[10;20H",
			shouldHit: true,
		},
		{
			name:      "position with single digit",
			line:      "\x1b[1H",
			shouldHit: true,
		},
		{
			name:      "position in text",
			line:      "prefix\x1b[5;10Hsuffix",
			shouldHit: true,
		},
		{
			name:      "not cursor position - color",
			line:      "\x1b[31m",
			shouldHit: false,
		},
		{
			name:      "cursor home alone",
			line:      "\x1b[H",
			shouldHit: false, // This is detected separately
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := containsCursorPosition(tt.line)
			if result != tt.shouldHit {
				t.Errorf("containsCursorPosition(%q) = %v, want %v", tt.line, result, tt.shouldHit)
			}
		})
	}
}

func TestDetector_RealWorldExamples(t *testing.T) {
	tests := []struct {
		name         string
		lines        []string
		expectedType OutputType
	}{
		{
			name: "grep output",
			lines: []string{
				"file.go:10:func main() {\n",
				"file.go:11:    fmt.Println(\"hello\")\n",
				"file.go:12:}\n",
			},
			expectedType: OutputTypeText,
		},
		{
			name: "npm install with colors",
			lines: []string{
				"\x1b[32madded\x1b[0m 150 packages\n",
				"\x1b[33mwarning\x1b[0m deprecated package\n",
				"npm notice created package.json\n",
			},
			expectedType: OutputTypeInk,
		},
		{
			name: "vim editor",
			lines: []string{
				"\x1b[?1049h\x1b[H\x1b[2J",
				"\x1b[1;1H~\n",
				"\x1b[2;1H~\n",
			},
			expectedType: OutputTypeFullscreen,
		},
		{
			name: "top command",
			lines: []string{
				"\x1b[H\x1b[2J",
				"top - 10:30:00 up 5 days\n",
			},
			expectedType: OutputTypeFullscreen,
		},
		{
			name: "gatsby develop",
			lines: []string{
				"\x1b[1G\x1b[2K\x1b[32m●\x1b[0m Starting development server\n",
				"\x1b[1G\x1b[2K\x1b[32m●\x1b[0m Compiling...\n",
				"\x1b[1G\x1b[2K\x1b[32m✓\x1b[0m Ready\n",
			},
			expectedType: OutputTypeInk,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := NewDetector()

			// Feed lines multiple times if needed to reach threshold
			for i := 0; i < 60; i++ {
				for _, line := range tt.lines {
					if d.AnalyzeLine(line) {
						break
					}
				}
				if d.IsDetected() {
					break
				}
			}

			if !d.IsDetected() {
				t.Fatal("Expected detection to occur")
			}

			outputType, reason := d.GetDetectedType()
			if outputType != tt.expectedType {
				t.Errorf("Expected %s, got %s (reason: %s)", tt.expectedType, outputType, reason)
			}
		})
	}
}

func TestDetector_Markdown(t *testing.T) {
	tests := []struct {
		name  string
		lines []string
	}{
		{
			name: "markdown with headers and lists",
			lines: []string{
				"# Main Header\n",
				"\n",
				"This is some content.\n",
				"\n",
				"## Subheader\n",
				"\n",
				"- Item 1\n",
				"- Item 2\n",
				"- Item 3\n",
			},
		},
		{
			name: "markdown with code blocks",
			lines: []string{
				"# Code Example\n",
				"\n",
				"Here's some code:\n",
				"\n",
				"```go\n",
				"func main() {\n",
				"    fmt.Println(\"Hello\")\n",
				"}\n",
				"```\n",
			},
		},
		{
			name: "markdown with links and bold",
			lines: []string{
				"Check out [this link](https://example.com)\n",
				"\n",
				"This is **bold text** and this is *italic*.\n",
				"\n",
				"## Another header\n",
				"\n",
				"1. First item\n",
				"2. Second item\n",
			},
		},
		{
			name: "markdown with blockquotes",
			lines: []string{
				"# Quote Section\n",
				"\n",
				"> This is a quote\n",
				"> with multiple lines\n",
				"\n",
				"And some **bold** text.\n",
				"\n",
				"## Subsection\n",
			},
		},
		{
			name: "markdown typical AI response",
			lines: []string{
				"# Analysis Results\n",
				"\n",
				"Here are my findings:\n",
				"\n",
				"## Key Points\n",
				"\n",
				"1. First observation\n",
				"2. Second observation\n",
				"3. Third observation\n",
				"\n",
				"### Code Example\n",
				"\n",
				"```python\n",
				"def hello():\n",
				"    print('world')\n",
				"```\n",
				"\n",
				"For more information, see [documentation](https://example.com).\n",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := NewDetector()

			// Feed lines until detection
			for _, line := range tt.lines {
				if d.AnalyzeLine(line) {
					break
				}
			}

			// If not detected after initial lines, feed more to trigger buffer limit
			if !d.IsDetected() {
				for i := 0; i < 10; i++ {
					for _, line := range tt.lines {
						if d.AnalyzeLine(line) {
							break
						}
					}
				}
			}

			if !d.IsDetected() {
				t.Fatal("Expected markdown to be detected")
			}

			outputType, reason := d.GetDetectedType()
			if outputType != OutputTypeMarkdown {
				t.Errorf("Expected OutputTypeMarkdown, got %s (reason: %s)", outputType, reason)
			}

			if !strings.Contains(reason, "markdown") {
				t.Errorf("Expected reason to mention markdown, got: %s", reason)
			}
		})
	}
}

func TestDetector_MarkdownNotConfusedWithText(t *testing.T) {
	// Test that plain text without markdown patterns is not detected as markdown
	d := NewDetector()

	plainTextLines := []string{
		"This is just plain text.\n",
		"It has no markdown formatting.\n",
		"Just regular sentences.\n",
		"Maybe some numbers like 123.\n",
		"And punctuation!\n",
	}

	// Feed enough lines to trigger detection
	for i := 0; i < 15; i++ {
		for _, line := range plainTextLines {
			d.AnalyzeLine(line)
		}
	}

	outputType, _ := d.GetDetectedType()
	if outputType == OutputTypeMarkdown {
		t.Errorf("Plain text should not be detected as markdown, got %s", outputType)
	}
}

func TestDetector_MarkdownPriorityOverInk(t *testing.T) {
	// Test that markdown takes priority over Ink detection
	// Some output might contain both ANSI codes and markdown.
	d := NewDetector()

	// Mix of markdown and ANSI color codes
	lines := []string{
		"# \x1b[1mHeader with Colors\x1b[0m\n",
		"\n",
		"This has \x1b[32mgreen text\x1b[0m but also **bold markdown**.\n",
		"\n",
		"## Subheader\n",
		"\n",
		"- List item with \x1b[34mblue\x1b[0m\n",
		"- Another item\n",
		"\n",
		"```\n",
		"code block\n",
		"```\n",
	}

	for _, line := range lines {
		d.AnalyzeLine(line)
	}

	// Feed more if not detected
	if !d.IsDetected() {
		for i := 0; i < 10; i++ {
			for _, line := range lines {
				if d.AnalyzeLine(line) {
					break
				}
			}
		}
	}

	outputType, _ := d.GetDetectedType()
	if outputType != OutputTypeMarkdown {
		t.Errorf("Expected markdown to take priority over Ink, got %s", outputType)
	}
}

func TestDetector_MarkdownEdgeCases(t *testing.T) {
	tests := []struct {
		name         string
		lines        []string
		shouldDetect bool
	}{
		{
			name: "hash without space is not markdown header",
			lines: []string{
				"#notaheader\n",
				"#stilln otaheader\n",
				"Just text\n",
			},
			shouldDetect: false,
		},
		{
			name: "single markdown indicator not enough",
			lines: []string{
				"# Just one header\n",
				"And then plain text.\n",
				"No other markdown.\n",
			},
			shouldDetect: false, // Need at least 3 indicators
		},
		{
			name: "multiple headers should detect",
			lines: []string{
				"# Header 1\n",
				"## Header 2\n",
				"### Header 3\n",
				"#### Header 4\n",
			},
			shouldDetect: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := NewDetector()

			// Feed lines multiple times to reach buffer limit
			for i := 0; i < 15; i++ {
				for _, line := range tt.lines {
					d.AnalyzeLine(line)
				}
			}

			outputType, _ := d.GetDetectedType()
			isMarkdown := (outputType == OutputTypeMarkdown)

			if isMarkdown != tt.shouldDetect {
				t.Errorf("Expected markdown detection: %v, got %v (type: %s)", tt.shouldDetect, isMarkdown, outputType)
			}
		})
	}
}
