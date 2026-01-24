// Package outputlog defines a simple protocol to multiplex several streams into one stream.
//
// # OutputLog Format
//
// Goals:
//
//  1. Preserve the exact output including binary data
//  2. Differentiate between streams (For example: stdout, stderr, stdin)
//  3. Include timestamps for each output line
//  4. Detects unfinished writes
//
// # Format Specification
//
// Each line follows this format:
//
//	stream timestamp length: content
//
// # Fields
//
//   - stream: Matches regex [a-zA-Z0-9_./-]{1,64}. For example: stdout, stderr, or stdin.
//   - timestamp: UTC timestamp in ISO 8601 format: 2006-01-02T15:04:05.999999999Z
//   - length: Integer byte length of the content (may include a trailing
//     newline if present in original output)
//   - `: ` Literal separator between length and content
//   - content: The actual output bytes (exactly length bytes). Content can contain newlines.
//   - separator \n:
//
// # Examples
//
// Example 1: Line with trailing newline
//
//	  stdout 2025-01-07T12:34:56.789Z 12: Hello world\n\n
//
//	- Content is "Hello world\n" (12 bytes, including the first newline)
//
// # Binary Data Support
//
// The format supports binary data including:
//
//   - Null bytes (\0)
//   - Non-printable characters
//   - Any byte value from 0-255
package outputlog
