package outputlog

import (
	"io"
	"log"
	"time"
)

type OutputLogWriter interface {
	// StreamWriter writes to on io.Writer in OutputLog format. Timestamps get added
	// automatically.
	StreamWriter(stream string) io.Writer

	// Channel returns a channel to write Chunks
	Channel() chan<- Chunk

	// Close closes the writer and waits for all pending writes to complete
	Close()
}

type OutputLogIoWriter struct {
	chunks chan Chunk
	done   chan struct{}
}

var _ OutputLogWriter = &OutputLogIoWriter{}

// StreamWriter returns an io.Writer that writes Chunks to the channel.
func (o *OutputLogIoWriter) StreamWriter(stream string) io.Writer {
	return &streamWriter{
		stream: stream,
		chunks: o.chunks,
	}
}

// Channel returns a channel for writing Chunks
// Do not close the returned channel. Call Close() on the writer instead.
func (o *OutputLogIoWriter) Channel() chan<- Chunk {
	return o.chunks
}

// Close closes the writer and waits for all pending writes to complete
func (o *OutputLogIoWriter) Close() {
	close(o.chunks)
	<-o.done
}

// streamWriter implements io.Writer for a specific stream
type streamWriter struct {
	stream string
	chunks chan<- Chunk
}

func (sw *streamWriter) Write(p []byte) (n int, err error) {
	if len(p) == 0 {
		return 0, nil
	}

	chunk := Chunk{
		Stream:    sw.stream,
		Timestamp: time.Now().UTC(),
		Line:      append([]byte(nil), p...), // Make a copy of the data
	}

	sw.chunks <- chunk

	return len(p), nil
}

// NewOutputLogWriter creates a new OutputLogWriter that writes to the given io.Writer
// The internal goroutine will run until Close() is called
func NewOutputLogWriter(writer io.Writer, onChunk func(*Chunk)) *OutputLogIoWriter {
	chunks := make(chan Chunk, 100)
	done := make(chan struct{})

	// Single goroutine that owns the io.Writer
	go func() {
		for chunk := range chunks {
			if onChunk != nil {
				onChunk(&chunk)
			}
			formatted := FormatChunk(chunk)
			if _, err := writer.Write(formatted); err != nil {
				log.Printf("outputlog: failed to write chunk: %v", err)
			}
		}
		close(done)
	}()

	return &OutputLogIoWriter{
		chunks: chunks,
		done:   done,
	}
}
