# `output.log` Format

The `output.log` file stores the combined output of a process, including stdout,
stderr, and stdin. Each line in the log file represents a chunk of output from one
of these streams.

## Format

The format of each line in `output.log` is:

```bash
<stream> <timestamp> <length>: <content>
```

- `<stream>`: The source of the output. Can be `stdout`, `stderr`, or `stdin`.
- `<timestamp>`: An ISO 8601 formatted timestamp in UTC
  (e.g., `2025-01-02T15:04:05.000Z`).
- `<length>`: The length of the `<content>` in bytes. This is important for
  determining if the content originally included a trailing newline.
- `<content>`: The actual output from the stream.

## Newlines

The `<length>` field is key to correctly interpreting the output.

- If the length of the `<content>` string is equal to `<length>`, the original
  output did **not** have a trailing newline.
- If the length of the `<content>` string is one less than `<length>`, the
  original output **did** have a trailing newline.

### Example

- **Original output:** `Hello` (no newline)
- **Log entry:** `stdout 2025-01-02T15:04:05.000Z 5: Hello`
  - The content `Hello` has a length of 5, which matches the `<length>` field.

- **Original output:** `Hello\n` (with a newline)
- **Log entry:** `stdout 2025-01-02T15:04:05.000Z 6: Hello\n`
  - When reading this line from the file, parsing tools might strip the trailing
    `\n`, resulting in the content `Hello` (length 5). Since this is less than
    the specified `<length>` of 6, the newline should be re-appended to
    reconstruct the original output.

## Special Entries

### Signals

When a signal is sent to the process via the web UI, it is also logged:

```bash
signal-sent <timestamp>: <signal_number> <signal_name>
```

This does not follow the standard length-prefixed format.
