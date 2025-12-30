# Example Workspace Directory Structure

## Example Directory Tree

After running a few commands in different workspaces, the state directory will look like this:

```text
.mobileshell/                              # State directory
├── hashed-passwords/                      # Authentication
│   └── a1b2c3d4...                       # Hashed password files
├── sessions/                              # Session tokens
│   └── session-xyz123                    # Session files
└── workspaces/                           # All workspaces (global, shared across sessions)
    ├── 2025-12-30_14:23:45.123/         # First workspace (timestamp-based name)
    │   ├── name                         # Plain text: "frontend"
    │   ├── directory                    # Plain text: "/home/user/project/frontend"
    │   ├── pre-command                  # Plain text: "source .env"
    │   ├── created-at                   # RFC3339Nano: "2025-12-30T14:23:45.123Z"
    │   └── processes/                   # All processes in this workspace
    │       ├── a1b2c3d4e5f6g7h8/        # First process (hash-based name)
    │       │   ├── cmd                  # Plain text: "npm run build"
    │       │   ├── starttime            # RFC3339Nano: "2025-12-30T14:24:00Z"
    │       │   ├── endtime              # RFC3339Nano: "2025-12-30T14:24:30Z"
    │       │   ├── status               # Plain text: "completed"
    │       │   ├── pid                  # Plain text: "12345"
    │       │   ├── exit-status          # Plain text: "0"
    │       │   ├── stdout               # Raw output: "Build complete..."
    │       │   └── stderr               # Raw errors: "warning: deprecated..."
    │       └── b2c3d4e5f6g7h8i9/        # Second process
    │           ├── cmd                  # "npm test"
    │           ├── starttime            # "2025-12-30T14:25:00Z"
    │           ├── endtime              # "2025-12-30T14:25:05Z"
    │           ├── status               # "completed"
    │           ├── pid                  # "12346"
    │           ├── exit-status          # "1"
    │           ├── stdout               # Test output
    │           └── stderr               # Test failures
    └── 2025-12-30_15:30:00.456/         # Second workspace (different time)
        ├── name                         # "backend"
        ├── directory                    # "/home/user/project/backend"
        ├── created-at                   # "2025-12-30T15:30:00.456Z"
        └── processes/
            └── c3d4e5f6g7h8i9j0/
                ├── cmd                  # "go test ./..."
                ├── starttime            # "2025-12-30T15:30:15Z"
                ├── status               # "running"
                ├── pid                  # "12347"
                ├── stdout               # Test output (growing)
                └── stderr               # Error output
```

## File Descriptions

All metadata is stored as **individual plain text files**. No JSON.

### Workspace Level

- **`YYYY-MM-DD_HH:MM:SS.ZZZ/`**: Timestamp-based directory name ensures uniqueness
  and chronological ordering
- **`name`**: Plain text file with workspace name (e.g., "frontend", "backend")
- **`directory`**: Plain text file with working directory path
- **`pre-command`**: (optional) Plain text file with command to run before each command
- **`created-at`**: Plain text file with RFC3339Nano timestamp

### Process Level

- **`HASH/`**: Hash-based directory name (16 characters from SHA256 of command + timestamp)
- **`cmd`**: Plain text file with the command to execute
- **`starttime`**: Plain text file with RFC3339Nano timestamp when process started
- **`endtime`**: (optional) Plain text file with RFC3339Nano timestamp when process ended
- **`status`**: Plain text file: "pending", "running", or "completed"
- **`pid`**: Plain text file with process ID (written when process starts)
- **`exit-status`**: Plain text file with exit code (written when process completes, empty if still running)
- **`stdout`**: Raw standard output stream (can be large, read incrementally)
- **`stderr`**: Raw standard error stream (can be large, read incrementally)

## Benefits of This Structure

1. **No JSON parsing required**: Everything is plain text files
2. **Shell-friendly**: Easy to inspect with `cat`, `grep`, `find`
3. **Time-ordered**: Workspace directories are naturally sorted by creation time
4. **Unique IDs**: Hash ensures no collisions, even for identical commands run at different times
5. **Easy cleanup**: Delete old workspace directories to free space
6. **Debugging**: All process information preserved for forensics
7. **Resumable**: Server can restart and reload all workspace/process state from disk
8. **Human-readable**: Directory and file names are self-explanatory

## Process States

A process goes through these states (stored in `status` file):

1. **`pending`**: Process metadata created, waiting for nohup to start
2. **`running`**: Process started, PID written, still executing
3. **`completed`**: Process finished, exit code written

The state can be determined by:

- No `pid` file → pending
- `pid` file exists, no `exit-status` → running
- `exit-status` file has content → completed

## Example Shell Commands

Inspect workspace and process data using standard Unix tools:

```bash
# List all workspaces
ls -1 .mobileshell/workspaces/

# View workspace details
cat .mobileshell/workspaces/2025-12-30_14:23:45.123/name
cat .mobileshell/workspaces/2025-12-30_14:23:45.123/directory

# List processes in a workspace
ls -1 .mobileshell/workspaces/2025-12-30_14:23:45.123/processes/

# Check process status
cat .mobileshell/workspaces/2025-12-30_14:23:45.123/processes/a1b2c3d4e5f6g7h8/status

# View process output
tail -f .mobileshell/workspaces/2025-12-30_14:23:45.123/processes/a1b2c3d4e5f6g7h8/stdout

# Find all running processes
find .mobileshell/workspaces -name status -exec grep -l "running" {} \;

# Find all completed processes with non-zero exit code
find .mobileshell/workspaces -name exit-status -exec grep -v "^0$" {} \;
```

## Multi-Session Behavior

- **Global Workspaces**: All sessions see and share the same workspaces
- **Session-Specific Selection**: Each session can select different workspace
- **Concurrent Access**: Multiple sessions can execute commands in the same workspace simultaneously
- **Process Visibility**: All processes from all sessions are visible to everyone

Example scenario:

- User A (session 1) creates "frontend" workspace and selects it
- User B (session 2) sees "frontend" workspace in their workspace list
- User B can select "frontend" and execute commands in it
- Both users see all processes running in "frontend" workspace
- User A can also create "backend" workspace
- User B can select "backend" workspace while User A stays in "frontend"
