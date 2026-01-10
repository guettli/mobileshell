package claude

import (
	"bufio"
	"encoding/json"
	"strings"
)

// StreamEvent represents a single event in Claude's stream-json output
type StreamEvent struct {
	Type    string          `json:"type"`
	Message *MessageContent `json:"message,omitempty"`
}

// MessageContent represents the message field in assistant events
type MessageContent struct {
	Content []ContentBlock `json:"content"`
}

// ContentBlock represents a single content block (text, tool use, etc.)
type ContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// ParseStreamJSON parses Claude's stream-json output and extracts text content
// It returns the extracted markdown text from all assistant messages
func ParseStreamJSON(streamOutput string) string {
	var textParts []string
	scanner := bufio.NewScanner(strings.NewReader(streamOutput))

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		// Each line should be a JSON event
		var event StreamEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			// Not valid JSON, skip this line
			continue
		}

		// Extract text from assistant messages
		if event.Type == "assistant" && event.Message != nil {
			for _, block := range event.Message.Content {
				if block.Type == "text" && block.Text != "" {
					textParts = append(textParts, block.Text)
				}
			}
		}
	}

	// Join all text parts
	return strings.Join(textParts, "\n\n")
}
