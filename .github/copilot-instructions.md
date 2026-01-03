# Copilot Instructions for MobileShell

## Project Overview

MobileShell is a web-based shell interface built with Go that provides a mobile-friendly way to
access and manage Linux systems. It allows users to execute shell commands remotely through a web
interface, view running processes, and monitor command outputs.

## Technology Stack

- **Backend**: Go 1.25+ with embedded templates and static files
- **Frontend**: htmx for dynamic updates, Bootstrap for responsive UI
- **Deployment**: systemd service, single binary with `go:embed`
- **Authentication**: Session-based with secure password management

## Project Structure

```
mobileshell/
├── cmd/mobileshell/          # Main application entry point
├── internal/
│   ├── auth/                 # Authentication and session management
│   ├── executor/             # Command execution and process management
│   ├── nohup/                # Background process handling
│   ├── workspace/            # Workspace management
│   └── server/               # HTTP server and handlers
│       ├── templates/        # HTML templates (htmx snippets and full pages)
│       └── static/           # Static assets
├── scripts/                  # Build, install, and test scripts
└── systemd/                  # Systemd service templates
```

## Code Conventions

### General Principles

- **No Code Duplication**: Before writing code, search for similar patterns/logic in the codebase
  and extract them into reusable functions. Never duplicate code - if you see existing logic that
  does what you need, refactor it into a shared helper first.
- **Minimal Changes**: Make the smallest possible changes to achieve the goal
- **Test First**: Before fixing bugs, write a test which reproduces the bug
- **Clean Up**: When an existing implementation gets changed, double-check that no old
  code/scripts/templates are left. Remove lines which are no longer needed.

### Naming Conventions

#### Endpoints and Handlers

1. **HTML Endpoints**:
   - **Full pages**: Use regular names (e.g., `/login`, `/processes`)
   - **htmx snippets**: End with `hx-{name}` (e.g., `/processes/hx-list`, `/output/hx-content`)
   - **JSON endpoints**: Use prefix `json-{name}` (e.g., `/json-status`)

2. **Go Handlers**:
   - **Full pages**: Regular function names (e.g., `handleLogin`, `handleProcesses`)
   - **htmx snippets**: Prefix with `hx` (e.g., `hxProcessList`, `hxOutputContent`)
   - **JSON endpoints**: Prefix with `json` (e.g., `jsonStatus`)

3. **Templates**:
   - **Full pages**: Regular names (e.g., `login.html`, `process.html`)
   - **htmx snippets**: Prefix with `hx-` (e.g., `hx-process-list.html`, `hx-output-content.html`)
   - **JSON templates**: Prefix with `json-` (e.g., `json-status.html`)

### Template Patterns

- Use `{{define "name"}}...{{end}}` blocks for reusable components
- Reference defined blocks with `{{template "name" .}}`
- Avoid duplicating template code across files

### Date and Time

- Always use UTC when writing dates and timestamps

## Development Workflow

### Running Tests

After making changes, always run:

```bash
./scripts/test.sh
```

This runs all test scripts in parallel including:
- Go tests (`test-go-test.sh`)
- Linting (`test-golangci-lint.sh`)
- Code duplication checks (`test-code-duplication.sh`)
- Template validation (`test-unused-templates.sh`)
- And more

### Building

```bash
# Build the binary
go build -o mobileshell ./cmd/mobileshell

# Or use the build script
./scripts/build.sh
```

### Local Development

```bash
# Run with a test password
./mobileshell -password test-password-123

# Access at http://localhost:22123
```

### Testing Individual Components

- Go tests: `go test ./...`
- Specific package: `go test ./internal/auth`
- With coverage: `go test -cover ./...`

## Best Practices

### Code Quality

1. **Avoid Duplication**: Search for existing patterns before implementing new code
2. **Refactor First**: Extract common logic into shared helpers
3. **Test Coverage**: Write tests before fixing bugs
4. **Clean Code**: Remove unused code, scripts, and templates after changes

### Security

- Never commit secrets or passwords
- Use secure session management
- Validate all user inputs
- Use proper authentication checks on all protected endpoints

### Performance

- Commands are executed with `nohup` and redirected output
- Process lists auto-refresh every 3 seconds
- Use htmx for efficient partial page updates

## Common Tasks

### Adding a New Endpoint

1. Determine endpoint type (full page, htmx snippet, or JSON)
2. Name the endpoint, handler, and template following conventions
3. Implement the handler in `internal/server/server.go`
4. Create the template in `internal/server/templates/`
5. Add tests in `internal/server/server_test.go`
6. Run `./scripts/test.sh` to verify

### Adding a New Feature

1. Search for similar existing functionality
2. Extract and reuse common patterns
3. Write tests first
4. Implement minimal changes
5. Clean up any old code
6. Run all tests
7. Update documentation if needed

### Debugging

- Check logs in the systemd journal: `journalctl -u mobileshell -f`
- Review process outputs in the web UI
- Use Go's built-in debugging tools
- Check stdout/stderr files created by nohup

## Architecture Notes

- Single binary deployment with `go:embed` for templates and static files
- Session-based authentication with secure password storage
- Asynchronous command execution with process tracking
- Mobile-first responsive design with Bootstrap
- Dynamic updates without page reloads using htmx

## Installation

The project uses an idempotent installation script:

```bash
./scripts/install.sh myserver.example.com myuser
```

This handles building, deploying, and setting up the systemd service on a remote server.
