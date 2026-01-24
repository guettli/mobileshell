package outputlog

import (
	"bytes"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// allBytes returns a byte slice containing all values from 0 to 255
func allBytes() []byte {
	data := make([]byte, 256)
	for i := range data {
		data[i] = byte(i)
	}
	return data
}

func TestFormatChunk_WithTrailingNewline(t *testing.T) {
	timestamp := time.Date(2025, 1, 7, 12, 34, 56, 789000000, time.UTC)
	chunk := Chunk{
		Stream:    "stdout",
		Timestamp: timestamp,
		Line:      []byte("Hello world\n"),
	}

	result := FormatChunk(chunk)
	expected := "stdout 2025-01-07T12:34:56.789Z 12: Hello world\n\n"

	require.Equal(t, expected, string(result))
}

func TestFormatChunk_WithoutTrailingNewline(t *testing.T) {
	timestamp := time.Date(2025, 1, 7, 12, 34, 56, 789000000, time.UTC)
	chunk := Chunk{
		Stream:    "stderr",
		Timestamp: timestamp,
		Line:      []byte("Error message"),
	}

	result := FormatChunk(chunk)
	expected := "stderr 2025-01-07T12:34:56.789Z 13: Error message\n"

	require.Equal(t, expected, string(result))
}

func TestFormatChunk_EmptyLine(t *testing.T) {
	timestamp := time.Date(2025, 1, 7, 12, 34, 56, 789000000, time.UTC)
	chunk := Chunk{
		Stream:    "stdout",
		Timestamp: timestamp,
		Line:      []byte{},
	}

	result := FormatChunk(chunk)
	expected := "stdout 2025-01-07T12:34:56.789Z 0: \n"

	require.Equal(t, expected, string(result))
}

func TestFormatChunk_BinaryData(t *testing.T) {
	timestamp := time.Date(2025, 1, 7, 12, 34, 56, 0, time.UTC)
	chunk := Chunk{
		Stream:    "stdout",
		Timestamp: timestamp,
		Line:      []byte{0x00, 0x01, 0xFF, 0x0A}, // null, 1, 255, newline
	}

	result := FormatChunk(chunk)
	// Length is 4 bytes
	expectedPrefix := "stdout 2025-01-07T12:34:56Z 4: "
	expectedContent := []byte{0x00, 0x01, 0xFF, 0x0A, 0x0A} // content + separator newline

	require.True(t, bytes.HasPrefix(result, []byte(expectedPrefix)))
	require.Equal(t, expectedContent, result[len(expectedPrefix):])
}

func TestFormatChunk_MultilineContent(t *testing.T) {
	timestamp := time.Date(2025, 1, 7, 12, 34, 56, 0, time.UTC)
	chunk := Chunk{
		Stream:    "stdout",
		Timestamp: timestamp,
		Line:      []byte("line1\nline2\nline3\n"),
	}

	result := FormatChunk(chunk)
	expected := "stdout 2025-01-07T12:34:56Z 18: line1\nline2\nline3\n\n"

	require.Equal(t, expected, string(result))
}

func TestFormatChunk_LargeBinaryData(t *testing.T) {
	timestamp := time.Date(2025, 1, 7, 12, 34, 56, 0, time.UTC)

	binaryData := allBytes()

	chunk := Chunk{
		Stream:    "stdout",
		Timestamp: timestamp,
		Line:      binaryData,
	}

	result := FormatChunk(chunk)
	expectedPrefix := "stdout 2025-01-07T12:34:56Z 256: "

	require.True(t, bytes.HasPrefix(result, []byte(expectedPrefix)))

	// Verify content: should be the binary data followed by separator newline
	content := result[len(expectedPrefix):]
	require.Len(t, content, 257)
	require.Equal(t, binaryData, content[:256])
	require.Equal(t, byte('\n'), content[256])
}

func TestFormatChunk_BinaryDataWithFormatMarkers(t *testing.T) {
	timestamp := time.Date(2025, 1, 7, 12, 34, 56, 0, time.UTC)

	// Binary data that contains text that looks like format markers
	// This ensures the parser doesn't get confused by content that looks like the protocol
	binaryData := []byte("stdout 2025-01-07T12:34:56Z 42: fake data\n")

	chunk := Chunk{
		Stream:    "stderr",
		Timestamp: timestamp,
		Line:      binaryData,
	}

	formatted := FormatChunk(chunk)
	reader := bytes.NewReader(formatted)

	parsedChunk, eof := readToChunk(reader)

	require.False(t, eof)
	require.NoError(t, parsedChunk.Error)

	// Should parse as stderr (the actual stream), not stdout (from the content)
	require.Equal(t, "stderr", parsedChunk.Stream)
	require.Equal(t, binaryData, parsedChunk.Line)
}
