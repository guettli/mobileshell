# TTY Support Examples

MobileShell now supports commands that require a TTY (pseudo-terminal).
This document provides examples of commands that benefit from TTY support.

## What is TTY Support?

A TTY (teletypewriter) or pseudo-terminal (PTY) is what programs use to interact
with terminals. Many commands check if they're connected to a TTY using `isatty()`
and behave differently based on the result.

With PTY support, MobileShell can now run:

- Interactive editors
- Terminal-based applications
- Commands that use terminal control codes
- Programs that check for TTY capabilities

## Examples

### Color Output

Many commands automatically enable colored output when they detect a TTY:

```bash
# These commands now show colors by default
ls --color=auto
grep --color=auto pattern file.txt
diff --color=auto file1 file2
```

### Interactive Programs

Programs that require a TTY can now be used:

```bash
# Text editors (though limited by web interface)
nano
vim  # Use :q! to exit if input is limited

# Interactive shells: bash, fish, zsh

# Process monitoring (use kill command to stop)
top
htop
```

## Important Notes

### Stdin Input

Input to running processes is sent via the web interface through a named pipe.
This means:

- Interactive prompts work
- Programs waiting for input can receive it
- Multi-line input is supported

### Limitations

Some limitations exist due to the web-based nature:

- No job control (Ctrl+C, Ctrl+Z)
- Terminal size is fixed at 80x24
- No terminal resizing
- Some very interactive programs may have issues

### Binary Output

PTY can pass through binary data, but the web interface may not display it
correctly. Use the download feature for binary outputs.

## Technical Details

### Implementation

- Uses `github.com/creack/pty` library
- PTY size: 80 columns × 24 rows
- Combines stdout and stderr (as real terminals do)
- Stdin forwarded via named pipe
- Process runs with `Setsid: true` (detached session)

### Testing

Run the test suite to verify TTY support:

```bash
go test ./internal/nohup -v -run TestTTY
```

This runs tests for:

- TTY detection (`test -t 0`)
- Terminal echo behavior
- ANSI color code support
