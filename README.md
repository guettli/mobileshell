# MobileShell

Provide a shell like experience via web - Build to access your Linux system with a mobile device.

- Single binary created with Go and `go:embed`
- Install as systemd service on your Linux PC or server.
- Authenticate with strong password.
- Build with htmx and bootstrap.

## Usage

You install MobileShell via `go run ./cmd/install myserver.example.com myuser`. This will connect to
root@myserver via ssh and installs a systemd service as user "myuser" and the `mobileshell` binary.

The systemd service runs the binary which opens a port at localhost:22123.

It is up to you to configure TLS termination. Example snippet for nginx:

```nginx

        location /mobileshell/ {
            proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
            proxy_set_header X-Forwarded-Proto $scheme;
            proxy_set_header Host $http_host;
            proxy_redirect off;
            proxy_pass http://127.0.0.1:22123/;
        }
```

This will give you a login prompt. You need to authenticate with a UUID. After successfull auth, you
are able to execute commands.

Commands are executed with `nohup` and redirecting stdout and stderr to files.

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

- **Authentication**: Secure UUID-based password authentication with session management
- **Command Execution**: Execute shell commands asynchronously
- **Process Management**: View running and completed processes
- **Output Viewing**: View stdout and stderr for each process
- **Auto-refresh**: Process list updates automatically every 3 seconds
- **Mobile-friendly**: Built with Bootstrap for responsive design
- **HTMX Integration**: Dynamic updates without page reloads

## Installation

### Prerequisites

- Go 1.21 or later
- SSH access to the target server with root privileges
- `uuidgen` command available on your local machine

### Remote Installation

```bash
go run ./cmd/install myserver.example.com myuser
```

This will:

1. Build the MobileShell binary
2. Generate a UUID password
3. Copy the binary and systemd service to the remote server
4. Install and start the systemd service
5. Display the UUID password for login

### Manual Installation

1. Build the binary:

   ```bash
   go build -o mobileshell ./cmd/mobileshell
   ```

2. Copy to server and set up systemd service manually

## Project Structure

```text
mobileshell/
├── cmd/
│   ├── mobileshell/      # Main application
│   └── install/          # Installation tool
├── internal/
│   ├── auth/            # Authentication and session management
│   ├── executor/        # Command execution and process management
│   └── server/          # HTTP server and handlers
│       └── templates/   # HTML templates
└── go.mod
```
