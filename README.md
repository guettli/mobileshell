# MobileShell

Provide a shell like experience via web - Build to access your Linux system with a mobile device.

- Single binary created with Go and `go:embed`
- Authenticate with strong password.
- Build with htmx and bootstrap.
- Optional: Install as systemd service on your Linux PC or server.

This was build with the help of AI agents. This whole project is more a less a playground for me
playing with agents.

## Usage

You install MobileShell via `./scripts/install.sh myserver.example.com myuser`. This will connect to
root@myserver via ssh and installs a systemd service as user "myuser" and the `mobileshell` binary.

The systemd service runs the binary which opens a port at localhost:22123.

It is up to you to configure TLS termination. Example snippet for nginx:

```nginx

        location /mobileshell/ {
            proxy_pass http://127.0.0.1:22123/;
            proxy_set_header Host $host;
            proxy_set_header X-Real-IP $remote_addr;
            proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
            proxy_set_header X-Forwarded-Proto $scheme;
            proxy_set_header X-Forwarded-Prefix /mobileshell;

            # WebSocket support
            proxy_http_version 1.1;
            proxy_set_header Upgrade $http_upgrade;
            proxy_set_header Connection "upgrade";

            # Increase timeouts for long-lived WebSocket connections
            proxy_read_timeout 3600s;
            proxy_send_timeout 3600s;
        }
```

This will give you a login prompt. You need to authenticate with a password. After successfull auth,
you are able to execute commands.

Commands are executed with `nohup` using a pseudo-terminal (PTY), which provides full TTY support.
This allows running interactive commands and programs that check for terminal capabilities.

The web UI shows the running processes, and you are able to look at the results.

## Development

To run MobileShell locally for testing:

```bash
# Build the binary
go build -o mobileshell ./cmd/mobileshell

# Run with a test password
./mobileshell -password test-password-123

# Access at http://localhost:22123
```

## Features

- **Authentication**: Secure password authentication with session management
- **Command Execution**: Execute shell commands asynchronously with full TTY support
- **TTY Support**: Commands run with a pseudo-terminal (PTY), enabling interactive
  programs (see [TTY_SUPPORT.md](TTY_SUPPORT.md) for details)
- **Process Management**: View running and completed processes
- **Output Viewing**: View stdout and stderr for each process
- **File Editor**: Create and edit files directly in the workspace with conflict detection
  - Auto-creates parent directories
  - Detects external file modifications
  - Shows diffs for changes and conflicts
  - Auto-chmod +x for scripts starting with shebang (`#!/`)
  - Security: Files restricted to workspace directory
- **Auto-refresh**: Process list updates automatically every 3 seconds
- **Mobile-friendly**: Built with Bootstrap for responsive design
- **HTMX Integration**: Dynamic updates without page reloads

## Installation

### Prerequisites

- Go 1.21 or later
- SSH access to the target server with root privileges

### Remote Installation

```bash
./scripts/install.sh myserver.example.com myuser
```

This will:

1. Build the MobileShell binary
2. Render the systemd service file with the username
3. Copy the binary, systemd service, and installation script to the remote server via rsync
4. Create the user if it doesn't exist
5. Install and start the systemd service
6. Create a password with `mobileshell add-password`

The installation is idempotent and can be run multiple times safely.

### Manual Installation

1. Build the binary:

   ```bash
   go build -o mobileshell ./cmd/mobileshell
   ```

2. Copy to server and set up systemd service manually

## CI/CD

All pull requests to the `main` branch automatically run the full test suite
via GitHub Actions.

On every push to the `main` branch, tests are run and if they pass, the code
is automatically deployed to production via SSH. Email notifications are sent
on failure.

## Project Structure

```text
mobileshell/
├── cmd/
│   └── mobileshell/      # Main application
├── internal/
│   ├── auth/            # Authentication and session management
│   ├── executor/        # Command execution and process management
│   └── server/          # HTTP server and handlers
│       └── templates/   # HTML templates
├── scripts/
│   ├── install.sh                  # Main installation orchestration script
│   └── install-exec-on-remote.sh   # Remote execution script
├── systemd/
│   └── mobileshell.service         # Systemd service template
└── go.mod
```
