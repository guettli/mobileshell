// Package outputlog defines a simple protocol to multiplex several streams into one stream. See
// doc.go for docs.
package outputlog

import (
	"fmt"
	"io"
	"time"
)

type OutputLogReader interface {
	// StreamReader returns an io.Reader for reading one stream. Example: You want to read only the
	// stream "stdout". Other stream and the timestamps get ignored.
	StreamReader(stream string) io.Reader

	// Channel returns a channel which emits Chunks.
	Channel() <-chan Chunk

	// All returns a map with stream as key and the data as bytes. Timestamps get ignored.
	All() map[string][]byte
}

type OutputLogIoReader struct {
	reader io.Reader
}

var _ OutputLogReader = &OutputLogIoReader{}

type ChannelReader struct {
	stream  string
	channel <-chan Chunk
	buffer  []byte // Buffer for partial chunk data
}

func (cr *ChannelReader) Read(p []byte) (n int, err error) {
	// First, copy any buffered data from previous reads
	if len(cr.buffer) > 0 {
		n = copy(p, cr.buffer)
		cr.buffer = cr.buffer[n:]
		if n == len(p) {
			// Buffer filled p completely
			return n, nil
		}
	}

	// Read chunks from channel
	for chunk := range cr.channel {
		if chunk.Stream != cr.stream {
			continue
		}

		// Copy as much as fits into remaining space in p
		copied := copy(p[n:], chunk.Line)
		n += copied

		// If there's leftover data, buffer it for next read
		if copied < len(chunk.Line) {
			cr.buffer = append(cr.buffer, chunk.Line[copied:]...)
		}

		// Return as soon as we have some data
		return n, nil
	}

	// Channel closed
	if n > 0 {
		// Return any data we copied from buffer before EOF
		return n, nil
	}
	return 0, io.EOF
}

func (o *OutputLogIoReader) Channel() <-chan Chunk {
	channel := make(chan Chunk)
	go readToChannel(o.reader, channel)
	return channel
}

func readToChannel(reader io.Reader, channel chan<- Chunk) {
	defer close(channel)
	for {
		chunk, eof := readToChunk(reader)
		if eof {
			break
		}
		channel <- chunk
	}
}

func readToChunk(reader io.Reader) (Chunk, bool) {
	var chunk Chunk
	buf := make([]byte, 1)

	// Helper to read one byte
	readByte := func() (byte, error) {
		n, err := reader.Read(buf)
		if n == 0 {
			return 0, err
		}
		return buf[0], nil
	}

	// Helper to read until delimiter
	readUntil := func(delim byte) (string, error) {
		var result []byte
		for {
			b, err := readByte()
			if err != nil {
				return "", err
			}
			if b == delim {
				return string(result), nil
			}
			result = append(result, b)
		}
	}

	// Read stream name (until space)
	stream, err := readUntil(' ')
	if err != nil {
		if err == io.EOF {
			return chunk, true
		}
		chunk.Error = fmt.Errorf("reading stream: %w", err)
		return chunk, true
	}
	chunk.Stream = stream

	// Read timestamp (until space)
	timestampStr, err := readUntil(' ')
	if err != nil {
		chunk.Error = fmt.Errorf("reading timestamp: %w", err)
		return chunk, true
	}
	timestamp, err := time.Parse("2006-01-02T15:04:05.000000000Z", timestampStr)
	if err != nil {
		chunk.Error = fmt.Errorf("parsing timestamp: %w", err)
		return chunk, true
	}
	chunk.Timestamp = timestamp

	// Read length (until colon)
	lengthStr, err := readUntil(':')
	if err != nil {
		chunk.Error = fmt.Errorf("reading length: %w", err)
		return chunk, true
	}
	var length int64
	_, err = fmt.Sscanf(lengthStr, "%d", &length)
	if err != nil {
		chunk.Error = fmt.Errorf("parsing length: %w", err)
		return chunk, true
	}

	// Skip the space after colon
	b, err := readByte()
	if err != nil {
		chunk.Error = fmt.Errorf("reading space after colon: %w", err)
		return chunk, true
	}
	if b != ' ' {
		chunk.Error = fmt.Errorf("expected space after colon, got %q", b)
		return chunk, true
	}

	// Read exactly `length` bytes of content
	chunk.Line = make([]byte, length)
	_, err = io.ReadFull(reader, chunk.Line)
	if err != nil {
		chunk.Error = fmt.Errorf("reading content (%d bytes): %w", length, err)
		return chunk, true
	}

	// Read the final newline separator
	b, err = readByte()
	if err != nil {
		chunk.Error = fmt.Errorf("reading final newline: %w", err)
		return chunk, true
	}
	if b != '\n' {
		chunk.Error = fmt.Errorf("expected newline separator, got %q", b)
		return chunk, true
	}

	return chunk, false
}

func (o *OutputLogIoReader) StreamReader(stream string) io.Reader {
	return &ChannelReader{
		stream:  stream,
		channel: o.Channel(),
		buffer:  []byte{},
	}
}

func (o *OutputLogIoReader) All() map[string][]byte {
	result := make(map[string][]byte)
	channel := o.Channel()

	for chunk := range channel {
		result[chunk.Stream] = append(result[chunk.Stream], chunk.Line...)
	}

	return result
}

func NewOutputLogReader(reader io.Reader) (OutputLogReader, error) {
	return &OutputLogIoReader{
		reader: reader,
	}, nil
}
