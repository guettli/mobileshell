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

	"mobileshell/internal/workspace"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
)

// Manager manages active terminal sessions
type Manager struct {
	sessions map[string]*Session
	mu       sync.RWMutex
}

// NewManager creates a new session manager
func NewManager() *Manager {
	return &Manager{
		sessions: make(map[string]*Session),
	}
}

// GetOrCreateSession retrieves an existing session or creates a new one
func (m *Manager) GetOrCreateSession(ws *websocket.Conn, stateDir string, workspaceID string, command string, processID string) (*Session, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if session already exists and is alive
	if session, exists := m.sessions[processID]; exists {
		if session.IsAlive() {
			// Reconnect to existing session
			if err := session.Reconnect(ws); err != nil {
				return nil, false, fmt.Errorf("failed to reconnect to session: %w", err)
			}
			slog.Info("Reconnected to existing terminal session", "processID", processID)
			return session, false, nil
		}
		// Session exists but process is dead, remove it
		delete(m.sessions, processID)
	}

	// Create new session
	session, err := NewSession(ws, stateDir, workspaceID, command, processID)
	if err != nil {
		return nil, false, err
	}

	m.sessions[processID] = session
	slog.Info("Created new terminal session", "processID", processID)
	return session, true, nil
}

// RemoveSession removes a session from the manager
func (m *Manager) RemoveSession(processID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, processID)
	slog.Info("Removed terminal session", "processID", processID)
}

// Session represents an interactive terminal session
type Session struct {
	ws         *websocket.Conn
	wsMutex    sync.Mutex // Protects ws field during reconnection
	ptmx       *os.File
	cmd        *exec.Cmd
	workspace  *workspace.Workspace
	done       chan struct{}
	closeOnce  sync.Once
	writeChan  chan []byte
	processID  string
	isRunning  bool
	runningMux sync.Mutex
}

// Message represents a WebSocket message
type Message struct {
	Type string `json:"type"` // "input", "resize"
	Data string `json:"data,omitempty"`
	Cols int    `json:"cols,omitempty"`
	Rows int    `json:"rows,omitempty"`
}

// NewSession creates a new interactive terminal session
func NewSession(ws *websocket.Conn, stateDir string, workspaceID string, command string, processID string) (*Session, error) {
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

	// Create the command with pre-command if specified
	var cmd *exec.Cmd
	if targetWorkspace.PreCommand != "" {
		// If there's a pre-command, combine and run via sh -c
		fullCommand := targetWorkspace.PreCommand + " && " + command
		cmd = exec.Command("sh", "-c", fullCommand)
	} else {
		// No pre-command: run the command directly with PTY
		// For bash, run directly - the PTY makes it interactive
		if command == "bash" {
			cmd = exec.Command("bash")
		} else {
			cmd = exec.Command("sh", "-c", command)
		}
	}
	cmd.Dir = targetWorkspace.Directory

	// Set up environment variables for interactive terminal
	cmd.Env = append(os.Environ(),
		"TERM=xterm-256color",
	)

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
		writeChan: make(chan []byte, 100),
		processID: processID,
		isRunning: false,
	}

	return session, nil
}

// Start begins handling the terminal session
func (s *Session) Start() {
	s.runningMux.Lock()
	if s.isRunning {
		s.runningMux.Unlock()
		return
	}
	s.isRunning = true
	s.runningMux.Unlock()

	// Read from PTY and send to WebSocket
	go s.readFromPTY()

	// Read from WebSocket and write to PTY
	go s.readFromWebSocket()

	// Handle WebSocket writes via channel
	go s.writeToWebSocket()

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
			s.closeOnce.Do(func() { close(s.done) })
			return
		}

		if n > 0 {
			// Send data to write channel
			data := make([]byte, n)
			copy(data, buf[:n])
			select {
			case s.writeChan <- data:
			case <-s.done:
				return
			}
		}
	}
}

// writeToWebSocket handles all WebSocket writes through a channel
func (s *Session) writeToWebSocket() {
	for {
		select {
		case data := <-s.writeChan:
			s.wsMutex.Lock()
			if s.ws != nil {
				if err := s.ws.WriteMessage(websocket.TextMessage, data); err != nil {
					slog.Error("Error writing to WebSocket", "error", err)
					s.ws = nil // Clear the WebSocket on error
				}
			}
			s.wsMutex.Unlock()
		case <-s.done:
			return
		}
	}
}

// readFromWebSocket reads messages from the WebSocket and processes them
func (s *Session) readFromWebSocket() {
	for {
		s.wsMutex.Lock()
		ws := s.ws
		s.wsMutex.Unlock()

		if ws == nil {
			// No active WebSocket, wait for reconnection
			select {
			case <-s.done:
				return
			case <-time.After(100 * time.Millisecond):
				continue
			}
		}

		_, data, err := ws.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				slog.Info("WebSocket disconnected (may reconnect)", "error", err)
			}
			// Don't close the session, just clear the WebSocket and wait for reconnection
			s.wsMutex.Lock()
			s.ws = nil
			s.wsMutex.Unlock()
			continue
		}

		var msg Message
		if err := json.Unmarshal(data, &msg); err != nil {
			// If it's not JSON, treat it as raw input
			if _, err := s.ptmx.Write(data); err != nil {
				slog.Error("Error writing to PTY", "error", err)
				s.closeOnce.Do(func() { close(s.done) })
				return
			}
			continue
		}

		switch msg.Type {
		case "input":
			if _, err := s.ptmx.Write([]byte(msg.Data)); err != nil {
				slog.Error("Error writing input to PTY", "error", err)
				s.closeOnce.Do(func() { close(s.done) })
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

	// Send exit notification via channel
	exitMsg := []byte("\r\n\r\n[Process exited]\r\n")
	select {
	case s.writeChan <- exitMsg:
	case <-time.After(1 * time.Second):
		// If we can't send, continue with cleanup
	}

	s.closeOnce.Do(func() { close(s.done) })
}

// Close cleans up the session
func (s *Session) Close() error {
	// Close WebSocket
	s.wsMutex.Lock()
	if s.ws != nil {
		_ = s.ws.Close()
		s.ws = nil
	}
	s.wsMutex.Unlock()

	// Close PTY
	_ = s.ptmx.Close()

	// Try to terminate the process gracefully
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Signal(syscall.SIGTERM)

		// Wait a bit for graceful shutdown
		waitDone := make(chan struct{})
		go func() {
			_ = s.cmd.Wait()
			close(waitDone)
		}()

		select {
		case <-waitDone:
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

// Reconnect attaches a new WebSocket connection to this session
func (s *Session) Reconnect(ws *websocket.Conn) error {
	s.wsMutex.Lock()
	defer s.wsMutex.Unlock()

	// Close old WebSocket if it exists
	if s.ws != nil {
		_ = s.ws.Close()
	}

	// Set new WebSocket
	s.ws = ws

	slog.Info("WebSocket reconnected to terminal session", "processID", s.processID)
	return nil
}

// IsAlive checks if the terminal process is still running
// Note: This uses Unix-specific syscall.Signal(0) which is consistent with
// the PTY implementation that is also Unix-specific
func (s *Session) IsAlive() bool {
	if s.cmd == nil || s.cmd.Process == nil {
		return false
	}

	// Check if process is still running using signal 0 (doesn't actually send a signal)
	err := s.cmd.Process.Signal(syscall.Signal(0))
	return err == nil
}
