package nohup

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"mobileshell/internal/workspace"
)

// TestIssue20_PromptWithoutNewline demonstrates issue #20:
// When a command writes output without a newline character (like a prompt)
// and waits for input, the output is not visible because readLines waits
// for a newline character before sending output to the channel.
//
// This test verifies that the issue exists by:
// 1. Creating a test command that outputs a prompt without newline
// 2. Running it through the nohup mechanism
// 3. Checking if the prompt appears in the output.log file
// 4. The test should FAIL initially, proving issue #20 exists
func TestIssue20_PromptWithoutNewline(t *testing.T) {
	// Create a temporary state directory
	stateDir := t.TempDir()

	// Create a workspace
	ws, err := workspace.CreateWorkspace(stateDir, "test-ws", stateDir, "")
	if err != nil {
		t.Fatalf("Failed to create workspace: %v", err)
	}

	// Create a simple script that outputs a prompt without newline and then exits after delay
	// This simulates a real interactive program that prompts for input
	scriptPath := filepath.Join(stateDir, "test-prompt.sh")
	scriptContent := `#!/bin/sh
# Output prompt without newline
printf "Enter filename: "
# Sleep to give time to check if output appears
sleep 2
# Exit without reading input
exit 0
`
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0755); err != nil {
		t.Fatalf("Failed to create test script: %v", err)
	}

	// Create a process that runs the script
	hash, err := workspace.CreateProcess(ws, "sh "+scriptPath)
	if err != nil {
		t.Fatalf("Failed to create process: %v", err)
	}

	processDir := workspace.GetProcessDir(ws, hash)
	outputFile := filepath.Join(processDir, "output.log")

	// Run the nohup process
	runDone := make(chan error, 1)
	go func() {
		runDone <- Run(stateDir, ws.ID, hash, []string{"sh", scriptPath})
	}()

	// Give the process some time to start and output the prompt
	time.Sleep(500 * time.Millisecond)

	// Read what's been written to output.log so far
	// If issue #20 exists, the prompt "Enter filename: " should NOT be visible yet
	// because it doesn't end with a newline
	content, err := os.ReadFile(outputFile)
	if err != nil {
		t.Fatalf("Failed to read output file: %v", err)
	}

	contentStr := string(content)

	// Check if the prompt appears in the output
	// This test demonstrates the bug: the prompt should appear but doesn't
	if !strings.Contains(contentStr, "Enter filename:") {
		t.Logf("Issue #20 confirmed: Prompt without newline is NOT visible in output")
		t.Logf("Output so far: %q", contentStr)
		t.Errorf("ISSUE #20 EXISTS: The prompt 'Enter filename: ' (without newline) is not visible in the output.log file.\n" +
			"This happens because readLines() uses bufio.Scanner which waits for a newline character.\n" +
			"Expected: Prompt should be visible even without a newline character.\n" +
			"Actual: Output is buffered until a newline is received.")
	} else {
		t.Logf("Prompt is visible in output: %q", contentStr)
		t.Log("Issue #20 appears to be fixed!")
	}

	// Wait for the process to complete (with timeout)
	select {
	case err := <-runDone:
		if err != nil {
			t.Logf("Process completed with error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Process did not complete within timeout")
	}

	// Read the final output
	finalContent, err := os.ReadFile(outputFile)
	if err != nil {
		t.Fatalf("Failed to read final output file: %v", err)
	}

	finalContentStr := string(finalContent)
	t.Logf("Final output.log content:\n%s", finalContentStr)

	// After process completion, the prompt should definitely be there
	// (because the process exit causes the pipe to close and scanner to return)
	if !strings.Contains(finalContentStr, "Enter filename:") {
		t.Errorf("Even after process completion, prompt is not in output: %q", finalContentStr)
	}
}

// TestIssue20_BufferedReading demonstrates that the fix for issue #20 works
// by showing how the new readLines implementation handles partial lines
func TestIssue20_BufferedReading(t *testing.T) {
	// Create a test pipe
	tmpDir := t.TempDir()
	pipePath := filepath.Join(tmpDir, "test.pipe")

	// Create the named pipe
	if err := syscall.Mkfifo(pipePath, 0600); err != nil {
		t.Fatalf("Failed to create pipe: %v", err)
	}

	// Start a goroutine to read from the pipe using the new readLines implementation
	outputChan := make(chan OutputLine, 10)
	done := make(chan struct{})
	go func() {
		file, _ := os.OpenFile(pipePath, os.O_RDONLY, 0)
		defer file.Close()
		readLines(file, "stdout", outputChan, done)
	}()

	// Give the reader time to start
	time.Sleep(100 * time.Millisecond)

	// Write data WITHOUT a newline
	writer, err := os.OpenFile(pipePath, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("Failed to open pipe for writing: %v", err)
	}

	_, err = writer.WriteString("Prompt without newline: ")
	if err != nil {
		t.Fatalf("Failed to write to pipe: %v", err)
	}
	writer.Sync()

	// Check if the line is read (with the fix, it should be after timeout)
	select {
	case line := <-outputChan:
		t.Logf("Successfully read line without newline: %q", line.Line)
		if line.Line != "Prompt without newline: " {
			t.Errorf("Expected 'Prompt without newline: ', got %q", line.Line)
		}
		t.Log("Issue #20 is fixed! Partial lines are now flushed after timeout.")
	case <-time.After(500 * time.Millisecond):
		t.Errorf("Failed to read line without newline within timeout.\n" +
			"The fix should flush partial lines after ~100ms.")
	}

	// Close the writer
	writer.Close()

	// Wait for reader to finish
	select {
	case <-done:
		t.Log("Reader finished cleanly")
	case <-time.After(1 * time.Second):
		t.Error("Reader did not finish within timeout")
	}
}

// TestIssue20_RealWorldScenario tests a realistic scenario where issue #20 would occur
func TestIssue20_RealWorldScenario(t *testing.T) {
	// This test simulates a common real-world scenario:
	// A program that prompts for a password or filename without outputting a newline

	stateDir := t.TempDir()

	ws, err := workspace.CreateWorkspace(stateDir, "test-ws", stateDir, "")
	if err != nil {
		t.Fatalf("Failed to create workspace: %v", err)
	}

	// Create a script that mimics interactive programs like:
	// - password prompts: "Enter password: "
	// - file selection: "Enter filename: "
	// - menu choices: "Select option (1-3): "
	scriptPath := filepath.Join(stateDir, "interactive.sh")
	scriptContent := `#!/bin/sh
# Simulate an interactive program
printf "Username: "
sleep 0.5
printf "\nPassword: "
sleep 0.5
printf "\nSelect option (1-3): "
sleep 1
# Exit after timeout
exit 0
`
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0755); err != nil {
		t.Fatalf("Failed to create test script: %v", err)
	}

	hash, err := workspace.CreateProcess(ws, "sh "+scriptPath)
	if err != nil {
		t.Fatalf("Failed to create process: %v", err)
	}

	processDir := workspace.GetProcessDir(ws, hash)
	outputFile := filepath.Join(processDir, "output.log")

	// Run the nohup process
	runDone := make(chan error, 1)
	go func() {
		runDone <- Run(stateDir, ws.ID, hash, []string{"sh", scriptPath})
	}()

	// Check output at different time points
	checks := []struct {
		delay    time.Duration
		expected []string
		name     string
	}{
		{
			delay:    300 * time.Millisecond,
			expected: []string{"Username:"},
			name:     "first prompt",
		},
		{
			delay:    600 * time.Millisecond,
			expected: []string{"Username:", "Password:"},
			name:     "second prompt",
		},
	}

	for _, check := range checks {
		time.Sleep(check.delay)
		content, err := os.ReadFile(outputFile)
		if err != nil {
			t.Fatalf("Failed to read output file: %v", err)
		}

		contentStr := string(content)
		allFound := true
		for _, expected := range check.expected {
			if !strings.Contains(contentStr, expected) {
				allFound = false
				t.Logf("At check '%s': Expected prompt '%s' not found in output", check.name, expected)
			}
		}

		if !allFound {
			t.Errorf("Issue #20: At check '%s', not all expected prompts are visible yet.\n"+
				"Current output: %q", check.name, contentStr)
		}
	}

	// Wait for completion
	select {
	case <-runDone:
	case <-time.After(5 * time.Second):
		t.Fatal("Process did not complete within timeout")
	}
}
