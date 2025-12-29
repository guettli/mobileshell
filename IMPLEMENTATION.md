# MobileShell Implementation Summary

## Overview

MobileShell is now fully implemented as a web-based shell access tool built with Go, htmx, and
Bootstrap.

## Components Implemented

### 1. Core Packages

#### `internal/executor` ([executor.go](internal/executor/executor.go))

- Manages command execution using shell subprocess
- Tracks running and completed processes
- Stores stdout/stderr to separate files
- Persists process state to JSON for persistence across restarts
- Thread-safe process management with mutex locks

#### `internal/auth` ([auth.go](internal/auth/auth.go))

- UUID-based password authentication
- Session token generation using crypto/rand
- 24-hour session expiration
- Session persistence to disk
- Automatic cleanup of expired sessions

#### `internal/server` ([server.go](internal/server/server.go))

- HTTP server with multiple endpoints
- Authentication middleware
- htmx integration for dynamic updates
- Template rendering with go:embed
- Cookie-based session management

### 2. Command Line Tools

#### `cmd/mobileshell` ([main.go](cmd/mobileshell/main.go))

- Main server binary
- Configurable port (default: 22123)
- Configurable data directory (default: ~/.mobileshell)
- Password authentication required

#### `cmd/install` ([main.go](cmd/install/main.go))

- Automated remote installation via SSH
- Builds binary locally
- Generates UUID password
- Copies binary and systemd service
- Installs and starts systemd service

### 3. Templates

All templates use Bootstrap 5 and htmx 1.9.10:

- **login.html**: Login form with password input
- **dashboard.html**: Main interface with command input and process list
- **processes.html**: Dynamic process list (refreshes every 3s)
- **output.html**: Stdout/stderr viewer for individual processes

### 4. Systemd Integration

- **mobileshell.service**: systemd unit file template
- Runs as specified user (not root)
- Auto-restart on failure
- Binds to localhost:22123

## Key Features

1. **Single Binary Deployment**: All templates embedded using go:embed
2. **Asynchronous Execution**: Commands run in background, output streamed to files
3. **Real-time Updates**: Process list auto-refreshes every 3 seconds
4. **Mobile Optimized**: Bootstrap responsive design
5. **Secure Sessions**: HttpOnly cookies with 24-hour expiration
6. **Process Persistence**: State saved to JSON, survives restarts

## How to Use

### Local Testing

```bash
./run-local.sh
# Access: http://localhost:22123
# Password: test-password-123
```

### Remote Installation

```bash
go run ./cmd/install myserver.example.com myuser
```

## Security Considerations

- Application binds to localhost only (requires reverse proxy)
- Password authentication required
- HttpOnly cookies prevent XSS attacks
- Sessions expire after 24 hours
- TLS termination should be handled by nginx/apache

## File Structure

```tree
mobileshell/
├── cmd/
│   ├── mobileshell/main.go        # Main server
│   └── install/
│       ├── main.go                # Installation tool
│       └── mobileshell.service    # systemd template
├── internal/
│   ├── auth/auth.go              # Authentication
│   ├── executor/executor.go      # Command execution
│   └── server/
│       ├── server.go             # HTTP server
│       └── templates/            # HTML templates
│           ├── login.html
│           ├── dashboard.html
│           ├── processes.html
│           └── output.html
├── go.mod
├── .gitignore
├── run-local.sh                  # Local test script
└── README.md
```

## Next Steps

To use in production:

1. Set up nginx reverse proxy with TLS
2. Run installation command on target server
3. Configure firewall rules
4. Save the UUID password securely
5. Access via HTTPS through nginx

## Technologies Used

- **Go 1.21+**: Backend language
- **htmx 1.9.10**: Dynamic HTML updates
- **Bootstrap 5.3**: Responsive UI
- **systemd**: Service management
- **go:embed**: Template embedding
