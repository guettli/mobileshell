package claude

import (
	"testing"
)

func TestParseStreamJSON(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name: "single assistant message",
			input: `{"type":"system","subtype":"init","session_id":"test"}
{"type":"assistant","message":{"content":[{"type":"text","text":"Hello, world!"}]}}
{"type":"result","subtype":"success"}`,
			expected: "Hello, world!",
		},
		{
			name: "multiple assistant messages",
			input: `{"type":"system","subtype":"init"}
{"type":"assistant","message":{"content":[{"type":"text","text":"First message"}]}}
{"type":"assistant","message":{"content":[{"type":"text","text":"Second message"}]}}
{"type":"result","subtype":"success"}`,
			expected: "First message\n\nSecond message",
		},
		{
			name: "markdown content",
			input: `{"type":"system","subtype":"init"}
{"type":"assistant","message":{"content":[{"type":"text","text":"# Hello\n\nThis is **bold** text."}]}}`,
			expected: "# Hello\n\nThis is **bold** text.",
		},
		{
			name: "mixed content with non-text blocks",
			input: `{"type":"system","subtype":"init"}
{"type":"assistant","message":{"content":[{"type":"text","text":"Here is some text"},{"type":"tool_use","id":"123"},{"type":"text","text":"More text"}]}}`,
			expected: "Here is some text\n\nMore text",
		},
		{
			name:     "empty input",
			input:    "",
			expected: "",
		},
		{
			name:     "no assistant messages",
			input:    `{"type":"system","subtype":"init"}`,
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseStreamJSON(tt.input)
			if result != tt.expected {
				t.Errorf("ParseStreamJSON() = %q, want %q", result, tt.expected)
			}
		})
	}
}
