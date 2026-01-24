package outputlog

import (
	"bytes"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOutputLogIoWriter_StreamWriter(t *testing.T) {
	var buf bytes.Buffer
	writer := NewOutputLogWriter(&buf, nil)

	stdoutWriter := writer.StreamWriter("stdout")

	n, err := stdoutWriter.Write([]byte("Hello world\n"))
	require.NoError(t, err)
	require.Equal(t, 12, n)

	writer.Close()

	// Parse back to verify
	reader, err := NewOutputLogReader(&buf)
	require.NoError(t, err)

	channel := reader.Channel()
	chunk := <-channel

	require.NoError(t, chunk.Error)
	require.Equal(t, "stdout", chunk.Stream)
	require.Equal(t, "Hello world\n", string(chunk.Line))
}

func TestOutputLogIoWriter_StreamWriter_MultipleWrites(t *testing.T) {
	var buf bytes.Buffer
	writer := NewOutputLogWriter(&buf, nil)

	stdoutWriter := writer.StreamWriter("stdout")
	stderrWriter := writer.StreamWriter("stderr")

	var err error
	_, err = stdoutWriter.Write([]byte("line1\n"))
	require.NoError(t, err)
	_, err = stderrWriter.Write([]byte("error1\n"))
	require.NoError(t, err)
	_, err = stdoutWriter.Write([]byte("line2\n"))
	require.NoError(t, err)

	writer.Close()

	// Parse back and verify
	reader, err := NewOutputLogReader(&buf)
	require.NoError(t, err)

	chunks := []Chunk{}
	for chunk := range reader.Channel() {
		require.NoError(t, chunk.Error)
		chunks = append(chunks, chunk)
	}

	require.Len(t, chunks, 3)
	require.Equal(t, "stdout", chunks[0].Stream)
	require.Equal(t, "line1\n", string(chunks[0].Line))
	require.Equal(t, "stderr", chunks[1].Stream)
	require.Equal(t, "error1\n", string(chunks[1].Line))
	require.Equal(t, "stdout", chunks[2].Stream)
	require.Equal(t, "line2\n", string(chunks[2].Line))
}

func TestOutputLogIoWriter_Channel(t *testing.T) {
	var buf bytes.Buffer
	writer := NewOutputLogWriter(&buf, nil)

	channel := writer.Channel()

	timestamp := time.Date(2025, 1, 7, 12, 34, 56, 789000000, time.UTC)
	chunk := Chunk{
		Stream:    "stdout",
		Timestamp: timestamp,
		Line:      []byte("test message\n"),
	}

	channel <- chunk

	writer.Close()

	// Parse back to verify
	reader, err := NewOutputLogReader(&buf)
	require.NoError(t, err)

	readChannel := reader.Channel()
	parsedChunk := <-readChannel

	require.NoError(t, parsedChunk.Error)
	require.Equal(t, "stdout", parsedChunk.Stream)
	require.Equal(t, "test message\n", string(parsedChunk.Line))
	require.True(t, parsedChunk.Timestamp.Equal(timestamp))
}

func TestOutputLogIoWriter_RoundTrip(t *testing.T) {
	var buf bytes.Buffer
	writer := NewOutputLogWriter(&buf, nil)

	// Write using StreamWriter
	stdoutWriter := writer.StreamWriter("stdout")
	stderrWriter := writer.StreamWriter("stderr")

	var err error
	_, err = stdoutWriter.Write([]byte("stdout line 1\n"))
	require.NoError(t, err)
	_, err = stderrWriter.Write([]byte("stderr line 1\n"))
	require.NoError(t, err)
	_, err = stdoutWriter.Write([]byte("stdout line 2\n"))
	require.NoError(t, err)

	writer.Close()

	// Read back and verify
	reader, err := NewOutputLogReader(&buf)
	require.NoError(t, err)

	result := reader.All()

	require.Len(t, result, 2)
	require.Equal(t, "stdout line 1\nstdout line 2\n", string(result["stdout"]))
	require.Equal(t, "stderr line 1\n", string(result["stderr"]))
}

func TestOutputLogIoWriter_BinaryData(t *testing.T) {
	var buf bytes.Buffer
	writer := NewOutputLogWriter(&buf, nil)

	stdoutWriter := writer.StreamWriter("stdout")

	binaryData := allBytes()
	n, err := stdoutWriter.Write(binaryData)
	require.NoError(t, err)
	require.Equal(t, 256, n)

	writer.Close()

	// Read back and verify
	reader, err := NewOutputLogReader(&buf)
	require.NoError(t, err)

	stdoutReader := reader.StreamReader("stdout")
	result, err := io.ReadAll(stdoutReader)
	require.NoError(t, err)

	require.Equal(t, binaryData, result)
}

func TestOutputLogIoWriter_EmptyWrite(t *testing.T) {
	var buf bytes.Buffer
	writer := NewOutputLogWriter(&buf, nil)

	stdoutWriter := writer.StreamWriter("stdout")

	n, err := stdoutWriter.Write([]byte{})
	require.NoError(t, err)
	require.Equal(t, 0, n)

	// Buffer should be empty since we didn't write anything
	require.Equal(t, 0, buf.Len())
}

func TestOutputLogIoWriter_ConcurrentWrites(t *testing.T) {
	var buf bytes.Buffer
	writer := NewOutputLogWriter(&buf, nil)

	stdoutWriter := writer.StreamWriter("stdout")
	stderrWriter := writer.StreamWriter("stderr")

	// Launch multiple goroutines writing concurrently
	done := make(chan struct{})
	go func() {
		for i := 0; i < 10; i++ {
			_, err := stdoutWriter.Write([]byte("stdout\n"))
			require.NoError(t, err)
		}
		done <- struct{}{}
	}()

	go func() {
		for i := 0; i < 10; i++ {
			_, err := stderrWriter.Write([]byte("stderr\n"))
			require.NoError(t, err)
		}
		done <- struct{}{}
	}()

	<-done
	<-done
	writer.Close()

	// Verify we got all 20 writes
	reader, err := NewOutputLogReader(&buf)
	require.NoError(t, err)

	chunks := []Chunk{}
	for chunk := range reader.Channel() {
		require.NoError(t, chunk.Error)
		chunks = append(chunks, chunk)
	}

	require.Len(t, chunks, 20)

	stdoutCount := 0
	stderrCount := 0
	for _, chunk := range chunks {
		switch chunk.Stream {
		case "stdout":
			stdoutCount++
			require.Equal(t, "stdout\n", string(chunk.Line))
		case "stderr":
			stderrCount++
			require.Equal(t, "stderr\n", string(chunk.Line))
		}
	}

	require.Equal(t, 10, stdoutCount)
	require.Equal(t, 10, stderrCount)
}

func TestOutputLogIoWriter_MixedChannelAndStreamWriter(t *testing.T) {
	var buf bytes.Buffer
	writer := NewOutputLogWriter(&buf, nil)

	stdoutWriter := writer.StreamWriter("stdout")
	channel := writer.Channel()

	// Write via StreamWriter
	var err error
	_, err = stdoutWriter.Write([]byte("via stream writer\n"))
	require.NoError(t, err)

	// Write via Channel
	channel <- Chunk{
		Stream:    "stderr",
		Timestamp: time.Now().UTC(),
		Line:      []byte("via channel\n"),
	}

	// Write via StreamWriter again
	_, err = stdoutWriter.Write([]byte("via stream writer 2\n"))
	require.NoError(t, err)

	writer.Close()

	// Verify all writes
	reader, err := NewOutputLogReader(&buf)
	require.NoError(t, err)

	chunks := []Chunk{}
	for chunk := range reader.Channel() {
		require.NoError(t, chunk.Error)
		chunks = append(chunks, chunk)
	}

	require.Len(t, chunks, 3)
	require.Equal(t, "stdout", chunks[0].Stream)
	require.Equal(t, "via stream writer\n", string(chunks[0].Line))
	require.Equal(t, "stderr", chunks[1].Stream)
	require.Equal(t, "via channel\n", string(chunks[1].Line))
	require.Equal(t, "stdout", chunks[2].Stream)
	require.Equal(t, "via stream writer 2\n", string(chunks[2].Line))
}

func TestOutputLogIoWriter_OrderPreservation(t *testing.T) {
	var buf bytes.Buffer
	writer := NewOutputLogWriter(&buf, nil)

	stdoutWriter := writer.StreamWriter("stdout")

	// Write multiple messages in order
	for i := 1; i <= 100; i++ {
		_, err := fmt.Fprintf(stdoutWriter, "line %d\n", i)
		require.NoError(t, err)
	}

	writer.Close()

	// Verify order is preserved
	reader, err := NewOutputLogReader(&buf)
	require.NoError(t, err)

	i := 1
	for chunk := range reader.Channel() {
		require.NoError(t, chunk.Error)
		require.Equal(t, fmt.Sprintf("line %d\n", i), string(chunk.Line))
		i++
	}

	require.Equal(t, 101, i) // Should have processed 100 lines
}

func TestOutputLogIoWriter_MultipleStreamWriters(t *testing.T) {
	var buf bytes.Buffer
	writer := NewOutputLogWriter(&buf, nil)

	// Create multiple writers for the same stream
	stdout1 := writer.StreamWriter("stdout")
	stdout2 := writer.StreamWriter("stdout")
	stderr1 := writer.StreamWriter("stderr")

	var err error
	_, err = stdout1.Write([]byte("stdout1\n"))
	require.NoError(t, err)
	_, err = stdout2.Write([]byte("stdout2\n"))
	require.NoError(t, err)
	_, err = stderr1.Write([]byte("stderr1\n"))
	require.NoError(t, err)
	_, err = stdout1.Write([]byte("stdout3\n"))
	require.NoError(t, err)

	writer.Close()

	// Verify all writes
	reader, err := NewOutputLogReader(&buf)
	require.NoError(t, err)

	result := reader.All()

	require.Len(t, result, 2)
	require.Equal(t, "stdout1\nstdout2\nstdout3\n", string(result["stdout"]))
	require.Equal(t, "stderr1\n", string(result["stderr"]))
}

func TestOutputLogIoWriter_LargeWrite(t *testing.T) {
	var buf bytes.Buffer
	writer := NewOutputLogWriter(&buf, nil)

	stdoutWriter := writer.StreamWriter("stdout")

	// Write a large chunk of data (larger than typical buffer size)
	largeData := make([]byte, 1024*1024) // 1MB
	for i := range largeData {
		largeData[i] = byte(i % 256)
	}

	n, err := stdoutWriter.Write(largeData)
	require.NoError(t, err)
	require.Equal(t, len(largeData), n)

	writer.Close()

	// Read back and verify
	reader, err := NewOutputLogReader(&buf)
	require.NoError(t, err)

	stdoutReader := reader.StreamReader("stdout")
	result, err := io.ReadAll(stdoutReader)
	require.NoError(t, err)

	require.Equal(t, largeData, result)
}

func TestOutputLogIoWriter_MultipleChunksViaChannel(t *testing.T) {
	var buf bytes.Buffer
	writer := NewOutputLogWriter(&buf, nil)

	channel := writer.Channel()

	timestamp1 := time.Date(2025, 1, 7, 10, 0, 0, 0, time.UTC)
	timestamp2 := time.Date(2025, 1, 7, 10, 0, 1, 0, time.UTC)
	timestamp3 := time.Date(2025, 1, 7, 10, 0, 2, 0, time.UTC)

	channel <- Chunk{Stream: "stdout", Timestamp: timestamp1, Line: []byte("first\n")}
	channel <- Chunk{Stream: "stderr", Timestamp: timestamp2, Line: []byte("error\n")}
	channel <- Chunk{Stream: "stdout", Timestamp: timestamp3, Line: []byte("second\n")}

	writer.Close()

	// Read back and verify
	reader, err := NewOutputLogReader(&buf)
	require.NoError(t, err)

	chunks := []Chunk{}
	for chunk := range reader.Channel() {
		require.NoError(t, chunk.Error)
		chunks = append(chunks, chunk)
	}

	require.Len(t, chunks, 3)
	require.True(t, chunks[0].Timestamp.Equal(timestamp1))
	require.True(t, chunks[1].Timestamp.Equal(timestamp2))
	require.True(t, chunks[2].Timestamp.Equal(timestamp3))
}
