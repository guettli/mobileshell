// Package outputlog defines a simple protocol to multiplex several streams into one stream. See
// doc.go for docs.
package outputlog

import (
	"bytes"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestReadToChunk_ValidFormat(t *testing.T) {
	input := "stdout 2025-01-07T12:34:56.789Z 12: Hello world\n\n"
	reader := bytes.NewReader([]byte(input))

	chunk, eof := readToChunk(reader)

	require.False(t, eof)
	require.NoError(t, chunk.Error)
	require.Equal(t, "stdout", chunk.Stream)

	expectedTime := time.Date(2025, 1, 7, 12, 34, 56, 789000000, time.UTC)
	require.True(t, chunk.Timestamp.Equal(expectedTime))

	expectedLine := "Hello world\n"
	require.Equal(t, expectedLine, string(chunk.Line))
}

func TestReadToChunk_NoTrailingNewline(t *testing.T) {
	input := "stderr 2025-01-07T10:20:30Z 5: Error\n"
	reader := bytes.NewReader([]byte(input))

	chunk, eof := readToChunk(reader)

	require.False(t, eof)
	require.NoError(t, chunk.Error)
	require.Equal(t, "stderr", chunk.Stream)

	expectedLine := "Error"
	require.Equal(t, expectedLine, string(chunk.Line))
}

func TestReadToChunk_BinaryData(t *testing.T) {
	timestamp := time.Date(2025, 1, 7, 12, 34, 56, 0, time.UTC)
	chunk := Chunk{
		Stream:    "stdout",
		Timestamp: timestamp,
		Line:      []byte{0x00, 0x01, 0xFF, 0x0A},
	}

	formatted := FormatChunk(chunk)
	reader := bytes.NewReader(formatted)

	parsedChunk, eof := readToChunk(reader)

	require.False(t, eof)
	require.NoError(t, parsedChunk.Error)
	require.Equal(t, chunk.Line, parsedChunk.Line)
}

func TestReadToChunk_EOF(t *testing.T) {
	reader := bytes.NewReader([]byte{})

	_, eof := readToChunk(reader)

	require.True(t, eof)
}

func TestReadToChunk_InvalidTimestamp(t *testing.T) {
	input := "stdout invalid-timestamp 5: hello\n"
	reader := bytes.NewReader([]byte(input))

	chunk, eof := readToChunk(reader)

	require.True(t, eof)
	require.Error(t, chunk.Error)
}

func TestReadToChunk_InvalidLength(t *testing.T) {
	input := "stdout 2025-01-07T12:34:56Z notanumber: hello\n"
	reader := bytes.NewReader([]byte(input))

	chunk, eof := readToChunk(reader)

	require.True(t, eof)
	require.Error(t, chunk.Error)
}

func TestReadToChunk_MissingFinalNewline(t *testing.T) {
	input := "stdout 2025-01-07T12:34:56Z 5: hello"
	reader := bytes.NewReader([]byte(input))

	chunk, eof := readToChunk(reader)

	require.True(t, eof)
	require.Error(t, chunk.Error)
}

func TestChannelReader_SingleChunk(t *testing.T) {
	channel := make(chan Chunk, 1)
	channel <- Chunk{
		Stream: "stdout",
		Line:   []byte("Hello world\n"),
	}
	close(channel)

	reader := &ChannelReader{
		stream:  "stdout",
		channel: channel,
		buffer:  []byte{},
	}

	buf := make([]byte, 100)
	n, err := reader.Read(buf)

	require.NoError(t, err)
	require.Equal(t, "Hello world\n", string(buf[:n]))

	// Next read should return EOF
	n, err = reader.Read(buf)
	require.ErrorIs(t, err, io.EOF)
	require.Equal(t, 0, n)
}

func TestChannelReader_FilterStream(t *testing.T) {
	channel := make(chan Chunk, 3)
	channel <- Chunk{Stream: "stdout", Line: []byte("out1\n")}
	channel <- Chunk{Stream: "stderr", Line: []byte("err1\n")}
	channel <- Chunk{Stream: "stdout", Line: []byte("out2\n")}
	close(channel)

	reader := &ChannelReader{
		stream:  "stdout",
		channel: channel,
		buffer:  []byte{},
	}

	buf := make([]byte, 100)

	// First read should get "out1\n"
	n, err := reader.Read(buf)
	require.NoError(t, err)
	require.Equal(t, "out1\n", string(buf[:n]))

	// Second read should skip stderr and get "out2\n"
	n, err = reader.Read(buf)
	require.NoError(t, err)
	require.Equal(t, "out2\n", string(buf[:n]))

	// Third read should return EOF
	n, err = reader.Read(buf)
	require.ErrorIs(t, err, io.EOF)
}

func TestChannelReader_SmallBuffer(t *testing.T) {
	channel := make(chan Chunk, 1)
	channel <- Chunk{
		Stream: "stdout",
		Line:   []byte("Hello world"),
	}
	close(channel)

	reader := &ChannelReader{
		stream:  "stdout",
		channel: channel,
		buffer:  []byte{},
	}

	// Read with a small buffer (only 5 bytes)
	buf := make([]byte, 5)
	n, err := reader.Read(buf)

	require.NoError(t, err)
	require.Equal(t, "Hello", string(buf[:n]))

	// Second read should get the rest from buffer
	n, err = reader.Read(buf)
	require.NoError(t, err)
	require.Equal(t, " worl", string(buf[:n]))

	// Third read should get the last character
	n, err = reader.Read(buf)
	require.NoError(t, err)
	require.Equal(t, "d", string(buf[:n]))

	// Fourth read should return EOF
	n, err = reader.Read(buf)
	require.ErrorIs(t, err, io.EOF)
}

func TestOutputLogIoReader_RoundTrip(t *testing.T) {
	// Create some chunks and format them
	timestamp1 := time.Date(2025, 1, 7, 12, 0, 0, 0, time.UTC)
	timestamp2 := time.Date(2025, 1, 7, 12, 0, 1, 0, time.UTC)
	timestamp3 := time.Date(2025, 1, 7, 12, 0, 2, 0, time.UTC)

	chunk1 := Chunk{Stream: "stdout", Timestamp: timestamp1, Line: []byte("line1\n")}
	chunk2 := Chunk{Stream: "stderr", Timestamp: timestamp2, Line: []byte("error1\n")}
	chunk3 := Chunk{Stream: "stdout", Timestamp: timestamp3, Line: []byte("line2\n")}

	var buf bytes.Buffer
	buf.Write(FormatChunk(chunk1))
	buf.Write(FormatChunk(chunk2))
	buf.Write(FormatChunk(chunk3))

	// Parse back using OutputLogIoReader
	reader, err := NewOutputLogReader(&buf)
	require.NoError(t, err)

	channel := reader.Channel()

	// Read all chunks from channel
	chunks := []Chunk{}
	for chunk := range channel {
		require.NoError(t, chunk.Error)
		chunks = append(chunks, chunk)
	}

	require.Len(t, chunks, 3)

	// Verify chunk1
	require.Equal(t, "stdout", chunks[0].Stream)
	require.Equal(t, "line1\n", string(chunks[0].Line))

	// Verify chunk2
	require.Equal(t, "stderr", chunks[1].Stream)
	require.Equal(t, "error1\n", string(chunks[1].Line))

	// Verify chunk3
	require.Equal(t, "stdout", chunks[2].Stream)
	require.Equal(t, "line2\n", string(chunks[2].Line))
}

func TestOutputLogIoReader_StreamReader(t *testing.T) {
	// Create mixed stdout/stderr content
	timestamp1 := time.Date(2025, 1, 7, 12, 0, 0, 0, time.UTC)
	timestamp2 := time.Date(2025, 1, 7, 12, 0, 1, 0, time.UTC)
	timestamp3 := time.Date(2025, 1, 7, 12, 0, 2, 0, time.UTC)

	chunk1 := Chunk{Stream: "stdout", Timestamp: timestamp1, Line: []byte("stdout1\n")}
	chunk2 := Chunk{Stream: "stderr", Timestamp: timestamp2, Line: []byte("stderr1\n")}
	chunk3 := Chunk{Stream: "stdout", Timestamp: timestamp3, Line: []byte("stdout2\n")}

	var buf bytes.Buffer
	buf.Write(FormatChunk(chunk1))
	buf.Write(FormatChunk(chunk2))
	buf.Write(FormatChunk(chunk3))

	// Create reader and get stdout stream only
	logReader, err := NewOutputLogReader(&buf)
	require.NoError(t, err)

	stdoutReader := logReader.StreamReader("stdout")

	// Read all stdout content
	result, err := io.ReadAll(stdoutReader)
	require.NoError(t, err)

	expected := "stdout1\nstdout2\n"
	require.Equal(t, expected, string(result))
}

func TestOutputLogIoReader_StreamReader_Stderr(t *testing.T) {
	// Create mixed stdout/stderr content
	timestamp1 := time.Date(2025, 1, 7, 12, 0, 0, 0, time.UTC)
	timestamp2 := time.Date(2025, 1, 7, 12, 0, 1, 0, time.UTC)
	timestamp3 := time.Date(2025, 1, 7, 12, 0, 2, 0, time.UTC)

	chunk1 := Chunk{Stream: "stdout", Timestamp: timestamp1, Line: []byte("stdout1\n")}
	chunk2 := Chunk{Stream: "stderr", Timestamp: timestamp2, Line: []byte("stderr1\n")}
	chunk3 := Chunk{Stream: "stdout", Timestamp: timestamp3, Line: []byte("stdout2\n")}

	var buf bytes.Buffer
	buf.Write(FormatChunk(chunk1))
	buf.Write(FormatChunk(chunk2))
	buf.Write(FormatChunk(chunk3))

	// Create reader and get stderr stream only
	logReader, err := NewOutputLogReader(&buf)
	require.NoError(t, err)

	stderrReader := logReader.StreamReader("stderr")

	// Read all stderr content
	result, err := io.ReadAll(stderrReader)
	require.NoError(t, err)

	expected := "stderr1\n"
	require.Equal(t, expected, string(result))
}

func TestReadToChunk_LargeBinaryData(t *testing.T) {
	timestamp := time.Date(2025, 1, 7, 12, 34, 56, 0, time.UTC)

	binaryData := allBytes()

	chunk := Chunk{
		Stream:    "stdout",
		Timestamp: timestamp,
		Line:      binaryData,
	}

	formatted := FormatChunk(chunk)
	reader := bytes.NewReader(formatted)

	parsedChunk, eof := readToChunk(reader)

	require.False(t, eof)
	require.NoError(t, parsedChunk.Error)
	require.Equal(t, chunk.Line, parsedChunk.Line)
}

func TestOutputLogIoReader_BinaryDataRoundTrip(t *testing.T) {
	timestamp1 := time.Date(2025, 1, 7, 12, 0, 0, 0, time.UTC)
	timestamp2 := time.Date(2025, 1, 7, 12, 0, 1, 0, time.UTC)

	// Mix of binary and text data
	binaryData1 := []byte{0x00, 0x01, 0x02, 0xFF, 0xFE, 0xFD}
	binaryData2 := []byte{0x89, 0x50, 0x4E, 0x47} // PNG header

	chunk1 := Chunk{Stream: "stdout", Timestamp: timestamp1, Line: binaryData1}
	chunk2 := Chunk{Stream: "stdout", Timestamp: timestamp2, Line: binaryData2}

	var buf bytes.Buffer
	buf.Write(FormatChunk(chunk1))
	buf.Write(FormatChunk(chunk2))

	// Parse back using OutputLogIoReader
	logReader, err := NewOutputLogReader(&buf)
	require.NoError(t, err)

	stdoutReader := logReader.StreamReader("stdout")

	// Read all binary content
	result, err := io.ReadAll(stdoutReader)
	require.NoError(t, err)

	expected := append(binaryData1, binaryData2...)
	require.Equal(t, expected, result)
}

func TestOutputLogIoReader_All_MixedStreams(t *testing.T) {
	timestamp1 := time.Date(2025, 1, 7, 12, 0, 0, 0, time.UTC)
	timestamp2 := time.Date(2025, 1, 7, 12, 0, 1, 0, time.UTC)
	timestamp3 := time.Date(2025, 1, 7, 12, 0, 2, 0, time.UTC)
	timestamp4 := time.Date(2025, 1, 7, 12, 0, 3, 0, time.UTC)

	chunk1 := Chunk{Stream: "stdout", Timestamp: timestamp1, Line: []byte("line1\n")}
	chunk2 := Chunk{Stream: "stderr", Timestamp: timestamp2, Line: []byte("error1\n")}
	chunk3 := Chunk{Stream: "stdout", Timestamp: timestamp3, Line: []byte("line2\n")}
	chunk4 := Chunk{Stream: "stderr", Timestamp: timestamp4, Line: []byte("error2\n")}

	var buf bytes.Buffer
	buf.Write(FormatChunk(chunk1))
	buf.Write(FormatChunk(chunk2))
	buf.Write(FormatChunk(chunk3))
	buf.Write(FormatChunk(chunk4))

	logReader, err := NewOutputLogReader(&buf)
	require.NoError(t, err)

	result := logReader.All()

	require.Len(t, result, 2)
	require.Equal(t, "line1\nline2\n", string(result["stdout"]))
	require.Equal(t, "error1\nerror2\n", string(result["stderr"]))
}

func TestOutputLogIoReader_All_SingleStream(t *testing.T) {
	timestamp1 := time.Date(2025, 1, 7, 12, 0, 0, 0, time.UTC)
	timestamp2 := time.Date(2025, 1, 7, 12, 0, 1, 0, time.UTC)

	chunk1 := Chunk{Stream: "stdout", Timestamp: timestamp1, Line: []byte("first\n")}
	chunk2 := Chunk{Stream: "stdout", Timestamp: timestamp2, Line: []byte("second\n")}

	var buf bytes.Buffer
	buf.Write(FormatChunk(chunk1))
	buf.Write(FormatChunk(chunk2))

	logReader, err := NewOutputLogReader(&buf)
	require.NoError(t, err)

	result := logReader.All()

	require.Len(t, result, 1)
	require.Equal(t, "first\nsecond\n", string(result["stdout"]))
}

func TestOutputLogIoReader_All_Empty(t *testing.T) {
	var buf bytes.Buffer

	logReader, err := NewOutputLogReader(&buf)
	require.NoError(t, err)

	result := logReader.All()

	require.Empty(t, result)
}

func TestOutputLogIoReader_All_BinaryData(t *testing.T) {
	timestamp1 := time.Date(2025, 1, 7, 12, 0, 0, 0, time.UTC)
	timestamp2 := time.Date(2025, 1, 7, 12, 0, 1, 0, time.UTC)

	binaryData1 := []byte{0x00, 0x01, 0x02, 0xFF}
	binaryData2 := []byte{0x89, 0x50, 0x4E, 0x47}

	chunk1 := Chunk{Stream: "stdout", Timestamp: timestamp1, Line: binaryData1}
	chunk2 := Chunk{Stream: "stdout", Timestamp: timestamp2, Line: binaryData2}

	var buf bytes.Buffer
	buf.Write(FormatChunk(chunk1))
	buf.Write(FormatChunk(chunk2))

	logReader, err := NewOutputLogReader(&buf)
	require.NoError(t, err)

	result := logReader.All()

	require.Len(t, result, 1)
	expected := append(binaryData1, binaryData2...)
	require.Equal(t, expected, result["stdout"])
}
