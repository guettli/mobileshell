// Package outputlog defines a simple protocol to multiplex several streams into one stream. See
// doc.go for docs.
package outputlog

import (
	"fmt"
	"time"
)

// Chunk represents a single line of output from either stdout or stderr
type Chunk struct {
	Stream    string
	Timestamp time.Time // UTC timestamp
	Line      []byte    // The actual line content (may include trailing newline)
	Error     error
}

// FormatChunk formats an OutputLine into the output.log format
// Format: "stream timestamp length: content"
func FormatChunk(chunk Chunk) []byte {
	timestamp := chunk.Timestamp.UTC().Format("2006-01-02T15:04:05.000000000Z")
	length := len(chunk.Line)
	start := fmt.Appendf(nil, "%s %s %d: ", chunk.Stream, timestamp, length)
	// Append content and always add separator newline
	result := append(start, chunk.Line...)
	result = append(result, byte('\n'))
	return result
}
