# Output Log Format

This document describes the format of the `output.log` file created by
mobileshell processes.

## Overview

When a process runs in mobileshell, all output (stdout, stderr, stdin)
is captured and written to an `output.log` file in the process
directory. The format is designed to:

1. Preserve the exact output including binary data
2. Differentiate between output streams (stdout, stderr, stdin)
3. Include timestamps for each output line
4. **Preserve information about trailing newlines** - this is critical
   for correctly reconstructing the original output

## Format Specification

Each line in `output.log` follows this format:

```text
> stream timestamp length: content\n
```

### Fields

- **`>`**: Literal prefix character that marks the start of a log entry
- **`stream`**: The output stream - one of: `stdout`, `stderr`, or
  `stdin`
- **`timestamp`**: UTC timestamp in ISO 8601 format:
  `2006-01-02T15:04:05.000Z`
- **`length`**: Integer representing the byte length of the content
  (may include a trailing newline if present in original output)
- **`:`**: Literal separator between length and content
- **`content`**: The actual output bytes (length is `length` bytes)
- **`\n`**: Log line separator (NOT counted in the length field)

### Examples

#### Example 1: Line with trailing newline

```text
> stdout 2025-01-07T12:34:56.789Z 12: Hello world\n
\n
```

- Content is `Hello world\n` (12 bytes, including the newline)
- The second `\n` is the log line separator

#### Example 2: Line without trailing newline

```text
> stdout 2025-01-07T12:34:56.789Z 8: prompt> \n
```

- Content is `prompt>` (8 bytes, no trailing newline)
- The `\n` at the end is the log line separator

#### Example 3: Multiple lines

```text
> stdout 2025-01-07T12:00:00.000Z 4: foo\n
\n
> stdout 2025-01-07T12:00:01.000Z 4: bar\n
\n
> stderr 2025-01-07T12:00:02.000Z 14: error message\n
\n
> stdin 2025-01-07T12:00:03.000Z 11: user input\n
\n
```

## Newline Preservation

The critical feature of this format is that it preserves whether the
original output had a trailing newline or not:

- **If the original output line ended with `\n`**: The content includes
  the `\n`, and the length reflects this (e.g., `"foo\n"` has length 4)
- **If the original output line did NOT end with `\n`**: The content
  has no trailing `\n`, and the length is just the content (e.g.,
  `"prompt>"` has length 8)

This is essential for:

- Interactive programs that output prompts without newlines (e.g.,
  `Enter filename:`)
- Correctly determining if a file ends with a newline
- Binary data preservation

## Binary Data Support

The format supports binary data including:

- Null bytes (`\0`)
- Non-printable characters
- Any byte value from 0-255

When binary data is detected, a `binary-data` marker file is created in
the process directory, and the web UI offers to download the raw output
instead of displaying it.

## Parsing Logic

To correctly parse this format:

1. Split the file by the log line separator `\n`
2. For each line starting with `>`:
   - Extract the stream type
   - Extract the timestamp
   - Extract the length field
   - Read exactly `length` bytes as the content (may include `\n`)
   - Skip the log line separator `\n`

## Signal Events

Signal events use a slightly different format:

```text
signal-sent timestamp: signal_number signal_name
```

Example:

```text
signal-sent 2025-01-07T12:34:56.789Z: 15 SIGTERM
```

These are displayed in the stdin section of the output for visibility.

## Implementation

The format is written by the `nohup.FormatOutputLine()` function in
`internal/nohup/nohup.go` and parsed by:

- `executor.ReadCombinedOutput()` for text display
- `executor.ReadRawStdout()` for binary data extraction
- `workspace.readRawStdoutBytes()` for internal processing

## Why This Format?

Previous formats didn't preserve newline information, which caused
issues like:

- `cat foo.txt` would lose the final newline character
- Interactive prompts would get unwanted newlines added
- Binary files couldn't be perfectly reconstructed

The length-based format with explicit newline inclusion solves these
problems while remaining human-readable in simple cases.
