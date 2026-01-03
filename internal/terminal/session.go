package terminal

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
	"mobileshell/internal/workspace"
)

// Session represents an interactive terminal session
type Session struct {
	ws        *websocket.Conn
	ptmx      *os.File
	cmd       *exec.Cmd
	workspace *workspace.Workspace
	processID string
	mu        sync.Mutex
	done      chan struct{}
}

// Message represents a WebSocket message
type Message struct {
	Type string          `json:"type"` // "input", "resize"
	Data string          `json:"data,omitempty"`
	Cols int             `json:"cols,omitempty"`
	Rows int             `json:"rows,omitempty"`
}

// NewSession creates a new interactive terminal session
func NewSession(ws *websocket.Conn, stateDir string, workspaceID string, command string) (*Session, error) {
	// Get workspace
	wsList, err := workspace.ListWorkspaces(stateDir)
	if err != nil {
		return nil, fmt.Errorf("failed to list workspaces: %w", err)
	}

	var targetWorkspace *workspace.Workspace
	for _, w := range wsList {
		if w.ID == workspaceID {
			targetWorkspace = w
			break
		}
	}

	if targetWorkspace == nil {
		return nil, fmt.Errorf("workspace not found: %s", workspaceID)
	}

	// Build the full command with pre-command if specified
	var fullCommand string
	if targetWorkspace.PreCommand != "" {
		fullCommand = targetWorkspace.PreCommand + " && " + command
	} else {
		fullCommand = command
	}

	// Create the command
	cmd := exec.Command("sh", "-c", fullCommand)
	cmd.Dir = targetWorkspace.Directory

	// Start the command with a PTY
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return nil, fmt.Errorf("failed to start command with pty: %w", err)
	}

	// Set PTY size to default (will be updated by client)
	_ = pty.Setsize(ptmx, &pty.Winsize{Rows: 24, Cols: 80})

	session := &Session{
		ws:        ws,
		ptmx:      ptmx,
		cmd:       cmd,
		workspace: targetWorkspace,
		done:      make(chan struct{}),
	}

	return session, nil
}

// Start begins handling the terminal session
func (s *Session) Start() {
	// Read from PTY and send to WebSocket
	go s.readFromPTY()

	// Read from WebSocket and write to PTY
	go s.readFromWebSocket()

	// Wait for process to complete
	go s.waitForProcess()
}

// readFromPTY reads output from the PTY and sends it to the WebSocket
func (s *Session) readFromPTY() {
	buf := make([]byte, 8192)
	for {
		n, err := s.ptmx.Read(buf)
		if err != nil {
			if err != io.EOF {
				slog.Error("Error reading from PTY", "error", err)
			}
			close(s.done)
			return
		}

		if n > 0 {
			s.mu.Lock()
			if err := s.ws.WriteMessage(websocket.TextMessage, buf[:n]); err != nil {
				slog.Error("Error writing to WebSocket", "error", err)
				s.mu.Unlock()
				close(s.done)
				return
			}
			s.mu.Unlock()
		}
	}
}

// readFromWebSocket reads messages from the WebSocket and processes them
func (s *Session) readFromWebSocket() {
	for {
		_, data, err := s.ws.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				slog.Error("WebSocket read error", "error", err)
			}
			close(s.done)
			return
		}

		var msg Message
		if err := json.Unmarshal(data, &msg); err != nil {
			// If it's not JSON, treat it as raw input
			if _, err := s.ptmx.Write(data); err != nil {
				slog.Error("Error writing to PTY", "error", err)
				close(s.done)
				return
			}
			continue
		}

		switch msg.Type {
		case "input":
			if _, err := s.ptmx.Write([]byte(msg.Data)); err != nil {
				slog.Error("Error writing input to PTY", "error", err)
				close(s.done)
				return
			}

		case "resize":
			if msg.Cols > 0 && msg.Rows > 0 {
				if err := pty.Setsize(s.ptmx, &pty.Winsize{
					Rows: uint16(msg.Rows),
					Cols: uint16(msg.Cols),
				}); err != nil {
					slog.Error("Error resizing PTY", "error", err)
				}
			}
		}
	}
}

// waitForProcess waits for the command to complete
func (s *Session) waitForProcess() {
	_ = s.cmd.Wait()
	
	// Give a moment for any final output to be sent
	time.Sleep(100 * time.Millisecond)
	
	// Send exit notification
	s.mu.Lock()
	exitMsg := "\r\n\r\n[Process exited]\r\n"
	_ = s.ws.WriteMessage(websocket.TextMessage, []byte(exitMsg))
	s.mu.Unlock()
	
	close(s.done)
}

// Close cleans up the session
func (s *Session) Close() error {
	// Close WebSocket
	s.mu.Lock()
	_ = s.ws.Close()
	s.mu.Unlock()

	// Close PTY
	_ = s.ptmx.Close()

	// Try to terminate the process gracefully
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Signal(syscall.SIGTERM)
		
		// Wait a bit for graceful shutdown
		done := make(chan struct{})
		go func() {
			_ = s.cmd.Wait()
			close(done)
		}()

		select {
		case <-done:
			// Process exited gracefully
		case <-time.After(2 * time.Second):
			// Force kill if it doesn't exit
			_ = s.cmd.Process.Kill()
		}
	}

	return nil
}

// Wait waits for the session to complete
func (s *Session) Wait() {
	<-s.done
}
