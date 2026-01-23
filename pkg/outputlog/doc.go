// Package outputlog defines a simple protocol to multiplex several streams into one stream.
//
// # OutputLog Format
//
// # Overview
//
// Goals:
//
//  1. Preserve the exact output including binary data
//  2. Differentiate between streams (For example: stdout, stderr, stdin)
//  3. Include timestamps for each output line
//  4. Preserve information about trailing newlines
//  5. Detects unfinished writes
//
// # Format Specification
//
// Each line follows this format:
//
//	stream timestamp length: content
//
// Where a separator \n is added after content only if content doesn't already end with \n.
//
// # Fields
//
//   - stream: Matches regex [a-zA-Z0-9_./-]{1,64}. For example: stdout, stderr, or stdin.
//   - timestamp: UTC timestamp in ISO 8601 format: 2006-01-02T15:04:05.000000000Z
//   - length: Integer byte length of the content (may include a trailing
//     newline if present in original output)
//   - `: ` Literal separator between length and content
//   - content: The actual output bytes (exactly length bytes). Content can contain newlines.
//   - separator \n: Added only if content doesn't already end with \n
//
// # Examples
//
// Example 1: Line with trailing newline
//
//	  stdout 2025-01-07T12:34:56.789Z 12: Hello world\n
//
//	- Content is "Hello world\n" (12 bytes, including the newline)
//	- No separator is added because content already ends with \n
//
// Example 2: Line without trailing newline
//
//	  stdout 2025-01-07T12:34:56.789Z 7: prompt>\n
//
//	- Content is "prompt>" (7 bytes, no trailing newline)
//	- Separator \n is added after the content
//
// Example 3: Multiple lines
//
//	stdout 2025-01-07T12:00:00.000Z 4: foo\n
//	stdout 2025-01-07T12:00:01.000Z 4: bar\n
//	stderr 2025-01-07T12:00:02.000Z 14: error message\n
//	stdin 2025-01-07T12:00:03.000Z 11: user input\n
//
// # Binary Data Support
//
// The format supports binary data including:
//
//   - Null bytes (\0)
//   - Non-printable characters
//   - Any byte value from 0-255
package outputlog
