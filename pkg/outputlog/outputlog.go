package outputlog

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// OutputLine represents a single line of output from either stdout or stderr
type OutputLine struct {
	Stream    string    // "stdout", "stderr", "stdin", or "signal-sent"
	Timestamp time.Time // UTC timestamp
	Line      string    // The actual line content (may include trailing newline)
}

// FormatOutputLine formats an OutputLine into the output.log format
// Format: "stream timestamp length: content" (with separator \n only if content doesn't end with \n)
// where length is the byte length of content (which may include a trailing newline)
func FormatOutputLine(line OutputLine) string {
	timestamp := line.Timestamp.UTC().Format("2006-01-02T15:04:05.000Z")
	length := len(line.Line)
	// Add separator newline only if content doesn't already end with one
	if len(line.Line) > 0 && line.Line[len(line.Line)-1] == '\n' {
		return fmt.Sprintf("%s %s %d: %s", line.Stream, timestamp, length, line.Line)
	}
	return fmt.Sprintf("%s %s %d: %s\n", line.Stream, timestamp, length, line.Line)
}

// ReadCombinedOutput reads and parses the combined output.log file
// Returns stdout, stderr, stdin, nohupStdout, nohupStderr lines separately
func ReadCombinedOutput(filename string) (stdout string, stderr string, stdin string, err error) { // TODO: Remove
	data, err := os.ReadFile(filename)
	if err != nil {
		return "", "", "", err
	}

	var stdoutParts, stderrParts, stdinParts []string
	i := 0

	for i < len(data) {
		// Find the ": " separator
		separatorIdx := -1
		for j := i; j < len(data)-1; j++ {
			if data[j] == ':' && data[j+1] == ' ' {
				separatorIdx = j + 2
				break
			}
		}

		if separatorIdx != -1 {
			// Extract the stream type from the beginning to the first space
			streamStart := i
			streamEnd := streamStart
			for streamEnd < len(data) && data[streamEnd] != ' ' {
				streamEnd++
			}
			stream := string(data[streamStart:streamEnd])

			// Only process if it's a valid stream type
			if stream == "stdout" || stream == "stderr" || stream == "stdin" || stream == "signal-sent" || stream == "nohup-stdout" || stream == "nohup-stderr" {
				// Extract length from the format
				// Find the space before the colon to get the length field
				lengthStart := -1
				for j := separatorIdx - 3; j >= i; j-- {
					if data[j] == ' ' {
						lengthStart = j + 1
						break
					}
				}

				if lengthStart != -1 {
					lengthStr := string(data[lengthStart : separatorIdx-2])
					var length int
					if _, scanErr := fmt.Sscanf(lengthStr, "%d", &length); scanErr == nil {
						// Read exactly 'length' bytes of content
						if separatorIdx+length <= len(data) {
							content := string(data[separatorIdx : separatorIdx+length])

							switch stream {
							case "stdout":
								stdoutParts = append(stdoutParts, content)
							case "stderr":
								stderrParts = append(stderrParts, content)
							case "stdin":
								stdinParts = append(stdinParts, content)
							case "signal-sent":
								// Signal events are prefixed and shown in stdin
								stdinParts = append(stdinParts, "Signal sent: "+content)
								stdinParts = append(stdinParts, "\n")
							case "nohup-stdout", "nohup-stderr":
								// Ignore nohup streams in this function for backwards compatibility
								// They are handled separately by ReadCombinedOutputWithNohup
							}

							// Move past content
							i = separatorIdx + length
							// Skip separator \n if present (only if content doesn't end with \n)
							if i < len(data) && data[i] == '\n' {
								i++
							}
							continue
						}
					}
				}
			}
		}

		// If parsing failed or not a recognized format, skip to next line
		for i < len(data) && data[i] != '\n' {
			i++
		}
		i++ // Skip the newline
	}

	// Concatenate parts as-is (they already include newlines where appropriate)
	stdout = strings.Join(stdoutParts, "")
	stderr = strings.Join(stderrParts, "")
	stdin = strings.Join(stdinParts, "")

	return stdout, stderr, stdin, nil
}

// ReadCombinedOutputWithNohup reads and parses the combined output.log file
// Returns stdout, stderr, stdin, nohupStdout, nohupStderr lines separately
func ReadCombinedOutputWithNohup(filename string) (stdout string, stderr string, stdin string, nohupStdout string, nohupStderr string, err error) { // TODO: Remove
	data, err := os.ReadFile(filename)
	if err != nil {
		return "", "", "", "", "", err
	}

	var stdoutParts, stderrParts, stdinParts, nohupStdoutParts, nohupStderrParts []string
	i := 0

	for i < len(data) {
		// Find the ": " separator
		separatorIdx := -1
		for j := i; j < len(data)-1; j++ {
			if data[j] == ':' && data[j+1] == ' ' {
				separatorIdx = j + 2
				break
			}
		}

		if separatorIdx != -1 {
			// Extract the stream type from the beginning to the first space
			streamStart := i
			streamEnd := streamStart
			for streamEnd < len(data) && data[streamEnd] != ' ' {
				streamEnd++
			}
			stream := string(data[streamStart:streamEnd])

			// Only process if it's a valid stream type
			if stream == "stdout" || stream == "stderr" || stream == "stdin" || stream == "signal-sent" || stream == "nohup-stdout" || stream == "nohup-stderr" {
				// Extract length from the format
				// Find the space before the colon to get the length field
				lengthStart := -1
				for j := separatorIdx - 3; j >= i; j-- {
					if data[j] == ' ' {
						lengthStart = j + 1
						break
					}
				}

				if lengthStart != -1 {
					lengthStr := string(data[lengthStart : separatorIdx-2])
					var length int
					if _, scanErr := fmt.Sscanf(lengthStr, "%d", &length); scanErr == nil {
						// Read exactly 'length' bytes of content
						if separatorIdx+length <= len(data) {
							content := string(data[separatorIdx : separatorIdx+length])

							switch stream {
							case "stdout":
								stdoutParts = append(stdoutParts, content)
							case "stderr":
								stderrParts = append(stderrParts, content)
							case "stdin":
								stdinParts = append(stdinParts, content)
							case "signal-sent":
								// Signal events are prefixed and shown in stdin
								stdinParts = append(stdinParts, "Signal sent: "+content)
								stdinParts = append(stdinParts, "\n")
							case "nohup-stdout":
								nohupStdoutParts = append(nohupStdoutParts, content)
							case "nohup-stderr":
								nohupStderrParts = append(nohupStderrParts, content)
							}

							// Move past content
							i = separatorIdx + length
							// Skip separator \n if present (only if content doesn't end with \n)
							if i < len(data) && data[i] == '\n' {
								i++
							}
							continue
						}
					}
				}
			}
		}

		// If parsing failed or not a recognized format, skip to next line
		for i < len(data) && data[i] != '\n' {
			i++
		}
		i++ // Skip the newline
	}

	// Concatenate parts as-is (they already include newlines where appropriate)
	stdout = strings.Join(stdoutParts, "")
	stderr = strings.Join(stderrParts, "")
	stdin = strings.Join(stdinParts, "")
	nohupStdout = strings.Join(nohupStdoutParts, "")
	nohupStderr = strings.Join(nohupStderrParts, "")

	return stdout, stderr, stdin, nohupStdout, nohupStderr, nil
}

// ReadRawStdout extracts raw stdout bytes from the combined output log file
// This function preserves binary data including newlines and null bytes
func ReadRawStdout(filename string) ([]byte, error) { // TODO: Remove
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}

	var stdoutBytes []byte
	i := 0
	for i < len(data) {
		// Check for format: "stdout ..."
		if i+7 <= len(data) && string(data[i:i+7]) == "stdout " {
			// Format: "stdout timestamp length: content"
			// Find the ": " separator
			separatorIdx := -1
			for j := i + 7; j < len(data)-1; j++ {
				if data[j] == ':' && data[j+1] == ' ' {
					separatorIdx = j + 2
					break
				}
			}

			if separatorIdx != -1 {
				// Extract length from the format
				// Find the space before the colon to get the length field
				lengthStart := -1
				for j := separatorIdx - 3; j >= i+7; j-- {
					if data[j] == ' ' {
						lengthStart = j + 1
						break
					}
				}

				if lengthStart != -1 {
					lengthStr := string(data[lengthStart : separatorIdx-2])
					var length int
					if _, scanErr := fmt.Sscanf(lengthStr, "%d", &length); scanErr == nil {
						// Read exactly 'length' bytes of content
						if separatorIdx+length <= len(data) {
							content := data[separatorIdx : separatorIdx+length]
							stdoutBytes = append(stdoutBytes, content...)

							// Move past content
							i = separatorIdx + length
							// Skip separator \n if present
							if i < len(data) && data[i] == '\n' {
								i++
							}
							continue
						}
					}
				}
			}
		}

		// Skip to next line if parsing failed or not a stdout line
		nextLine := i
		for nextLine < len(data) && data[nextLine] != '\n' {
			nextLine++
		}
		i = nextLine + 1
	}

	return stdoutBytes, nil
}
