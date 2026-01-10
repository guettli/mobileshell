package claude

import (
	"os/exec"
)

// CommandOptions configures how the Claude CLI command should be built
type CommandOptions struct {
	// StreamJSON enables streaming JSON output format with verbose mode
	// This allows real-time display of Claude's response as it's generated
	StreamJSON bool

	// NoSession prevents session persistence, useful for one-off commands
	NoSession bool

	// WorkDir specifies the working directory for Claude's execution context
	WorkDir string
}

// BuildCommand creates a Claude CLI command for interactive dialog sessions.
// It returns a slice of strings suitable for exec.Command().
//
// The command always runs in interactive dialog mode (no `-p` flag) to enable
// multi-turn conversations via stdin.
//
// The command uses:
// - Interactive dialog mode (no `-p` flag) for multi-turn conversations
// - `--output-format=stream-json --verbose` for real-time streaming (if StreamJSON is true)
// - `--no-session-persistence` to avoid creating session files (if NoSession is true)
//
// Example command:
//   claude --output-format=stream-json --verbose --no-session-persistence "prompt text"
func BuildCommand(prompt string, opts CommandOptions) []string {
	var args []string

	// Always run in interactive dialog mode (no -p flag)
	// This enables multi-turn conversations via stdin

	// Add streaming JSON format if requested
	if opts.StreamJSON {
		args = append(args, "--output-format=stream-json", "--verbose")
	}

	// Add no-session flag if requested
	if opts.NoSession {
		args = append(args, "--no-session-persistence")
	}

	// Add the prompt as the final argument
	args = append(args, prompt)

	return args
}

// IsClaudeAvailable checks if the claude CLI is available in the system PATH.
// Returns true if the claude executable can be found, false otherwise.
func IsClaudeAvailable() bool {
	_, err := exec.LookPath("claude")
	return err == nil
}
