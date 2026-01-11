# Workspace Implementation

## Overview

Every process in mobileshell is now part of a workspace. A workspace has:

- A unique URL-safe ID (immutable)
- A display name (can be changed)
- A working directory (must exist before creating workspace)
- An optional pre-command (executed before running the command)

## Directory Structure

```text
statDir/
└── workspaces/
    └── YYYY-MM-DD_ID/
        ├── id
        ├── name
        ├── directory
        ├── pre-command (optional)
        ├── created-at
        └── processes/
            └── HASH/
                ├── cmd
                ├── starttime
                ├── endtime (if exited)
                ├── completed
                ├── pid (if started)
                ├── exit-status (if exited)
                ├── stdout
                └── stderr
```

All metadata is stored as **individual files** (no JSON files).

## Workspace ID

- Generated from the workspace name
- URL-safe: lowercase letters, numbers, and hyphens only
- Immutable once created
- Used in URLs: `/workspaces/{id}`
- Maximum 50 characters
- Examples:
  - "Frontend" → "frontend"
  - "My Project" → "my-project"
  - "API Backend v2" → "api-backend-v2"

## File Descriptions

### Workspace Level

- **`YYYY-MM-DD_ID/`**: Date + ID directory name (e.g., `2025-12-30_frontend`)
- **`id`**: Plain text file with URL-safe workspace ID (immutable)
- **`name`**: Plain text file with display name (can be changed)
- **`directory`**: Plain text file with working directory path
- **`pre-command`**: (optional) Plain text file with command to run before each command
- **`created-at`**: RFC3339Nano timestamp when workspace was created

### Process Level

- **`HASH/`**: Hash-based directory name (16 characters from SHA256 of command + timestamp)
- **`cmd`**: Plain text file with the command to execute
- **`starttime`**: RFC3339Nano timestamp when process started
- **`endtime`**: (optional) RFC3339Nano timestamp when process ended
- **`completed`**: Plain text: "true" or "false"
- **`pid`**: Plain text file with process ID (written when process starts)
- **`exit-status`**: Plain text file with exit code (written when process completes, empty if still running)
- **`output.log`**: Combined output file containing stdout, stderr, and
  stdin streams with timestamps (see OUTPUT_LOG_FORMAT.md)

## Implementation Details

### 1. Workspace Package (`internal/workspace/`)

New package that handles workspace and process management using individual files:

- `Manager`: Manages workspaces and processes
- `Workspace`: Represents a workspace with ID, name, directory, and pre-command
- `Process`: Represents a process within a workspace

Key functions:

- `CreateWorkspace(name, directory, preCommand)`: Creates a new workspace
  - **Validates directory exists** before creating
  - Generates URL-safe ID from name
  - Returns error if directory doesn't exist
  - Returns error if workspace with same ID already exists
- `GetWorkspaceByID(id)`: Gets workspace by ID
- `ListWorkspaces()`: Lists all workspaces
- `ListProcesses(ws)`: Lists all processes in a workspace

All metadata is read from individual files, not JSON.

### 2. Process Package (`internal/process/`)

A low-level package shared by `workspace` and `nohup` that handles writing process state files:

- `InitializeProcessDir(processDir, command)`: Creates the process directory and initial metadata
  files (`cmd`, `starttime`, `completed`, `stdin.pipe`, `output.log`)
- `UpdateProcessPIDInDir(processDir, pid)`: Writes the PID file and updates status to "running"
- `UpdateProcessExitInDir(processDir, exitCode, signal)`: Writes exit status, signal, end time and
  marks as completed

### 3. Nohup Package (`internal/nohup/`)

Handles actual process execution in detached mode:

- `Run(processDir, command, workDir, preCommand)`: Executes a command in nohup mode
  - **Creates the process directory** and initializes files (via `process` package)
  - Runs in the specified `workDir`
  - Executes `preCommand` before the actual command
  - Detaches from parent process using `Setsid`
  - Updates PID, exit status, and metadata files (via `process` package)

### 4. Nohup Command

CLI command: `mobileshell nohup [flags] PROCESS_DIR COMMAND`

Flags:

- `--work-dir DIR`: Working directory for the process
- `--pre-command SCRIPT`: Script to run before the command

This command is used internally by the executor to spawn processes. It's hidden
from the help menu as it's for internal use only.

### 5. Updated Executor (`internal/executor/`)

The executor now:

- Uses workspace manager to create and manage processes
- Does NOT create a default workspace on startup
- Requires explicit workspace selection before executing commands
- Spawns processes by calling `mobileshell nohup` as a subprocess
- Process spawning is now fully detached and persistent

Key methods:

- `New(stateDir)`: Takes stateDir, creates workspace manager (NO default workspace)
- `SetWorkspace(name, directory, preCommand)`: Creates and selects a new workspace
- `SelectWorkspaceByID(id)`: Selects an existing workspace by ID
- `Execute(command)`: Creates process in current workspace, spawns via nohup
- `ListProcesses()`: Returns processes from all workspaces
- `GetProcess(id)`: Searches all workspaces for a process

### 6. Updated Server (`internal/server/`)

Major UI and workflow changes:

**New Workflow:**

1. User must create or select a workspace before executing commands
2. Dashboard renamed to "Workspaces"
3. Workspaces are global (all sessions see all workspaces)
4. Each session tracks which workspace ID is currently selected
5. URLs use workspace ID: `/workspaces/{id}`

**New Routes:**

- `/workspaces` - Main workspaces page (list or creation form)
- `/workspaces/create` - Create new workspace (validates directory exists)
- `/workspaces/{id}` - View/work in specific workspace
- `/workspace/clear` - Clear workspace selection
- `/workspace/list` - List all workspaces (HTMX partial)

**Updated Handlers:**

- `handleWorkspaces()`: Shows workspace creation UI if no workspace selected
- `handleWorkspaceCreate()`: Creates workspace, validates directory, redirects to `/workspaces/{id}`
- `handleWorkspaceByID()`: Displays workspace with command execution UI
- `handleWorkspaceClear()`: Clears workspace selection, returns to list
- `handleWorkspaceList()`: Returns HTMX partial with workspace list
- `handleExecute()`: **Requires workspace selection**, validates before executing

**Session Management:**

- Server tracks which workspace ID each session has selected
- Map: `sessionWorkspaces[sessionToken] -> workspaceID`
- Workspaces themselves are global (not per-session)
- All sessions can see and select from all workspaces

### 7. New Templates

- **`workspaces.html`**: Main workspaces page with conditional rendering
  - Shows workspace creation form + workspace list when no workspace selected
  - Shows command execution form when workspace is selected
- **`workspace-list.html`**: HTMX partial for displaying workspace list with links to `/workspaces/{id}`

## Process Spawning Flow

1. User creates or selects workspace via web UI (navigates to `/workspaces/{id}`)
2. Workspace ID stored in session
3. User submits command via web UI
4. Server validates workspace ID is selected for session
5. Server calls `executor.SelectWorkspaceByID(id)` to ensure correct workspace
6. Server calls `executor.Execute(command)`
7. Executor generates process hash and determines directory path
8. Executor spawns `mobileshell nohup --work-dir ... PROCESS_DIR COMMAND` as subprocess
9. Nohup subprocess:
   - **Creates process directory and initializes metadata files**
   - Changes to specified working directory
   - Runs pre-command (if specified)
   - Runs actual command
   - Captures stdout/stderr/stdin to files
   - Updates PID and status files
   - Waits for completion
   - Updates exit status, end time, and marks as completed

## Benefits

1. **No JSON Parsing**: All metadata stored as simple text files
2. **Easy Inspection**: `cat` any file to see its value
3. **Shell-Friendly**: Easy to manipulate with standard Unix tools
4. **URL-Safe IDs**: Clean URLs like `/workspaces/frontend` instead of timestamps
5. **Immutable IDs**: ID never changes, but name can be updated
6. **Directory Validation**: Prevents creating workspaces for non-existent directories
7. **Organized Storage**: Processes grouped by workspace and date
8. **Pre-commands**: Support for environment setup before each command
9. **Working Directory**: Each workspace has its own working directory
10. **Persistence**: All process data stored in files for durability
11. **Detached Execution**: Processes run independently via nohup subprocess
12. **Clean Architecture**: Separation of concerns between workspace management and execution
13. **Required Workspace**: Users must explicitly create/select workspace before executing commands
14. **Multi-Session Support**: Each session can work in a different workspace simultaneously
15. **Global Workspaces**: All sessions share the same set of workspaces

## Example Directory Tree

```text
.mobileshell/
├── hashed-passwords/
│   └── a1b2c3d4...
├── sessions/
│   └── session-xyz123
└── workspaces/
    ├── 2025-12-30_frontend/
    │   ├── id                          # "frontend"
    │   ├── name                        # "Frontend"
    │   ├── directory                   # "/home/user/project/frontend"
    │   ├── pre-command                 # "source .env"
    │   ├── created-at                  # "2025-12-30T14:23:45.123Z"
    │   └── processes/
    │       └── a1b2c3d4e5f6g7h8/
    │           ├── cmd                 # "npm run build"
    │           ├── starttime           # "2025-12-30T14:24:00Z"
    │           ├── endtime             # "2025-12-30T14:24:30Z"
    │           ├── completed           # "true"
    │           ├── pid                 # "12345"
    │           ├── exit-status         # "0"
    │           ├── stdout              # Build output
    │           └── stderr              # Build warnings
    └── 2025-12-30_backend/
        ├── id                          # "backend"
        ├── name                        # "Backend API"
        ├── directory                   # "/home/user/project/backend"
        ├── created-at                  # "2025-12-30T15:30:00.456Z"
        └── processes/
            └── b2c3d4e5f6g7h8i9/
                ├── cmd                 # "go test ./..."
                ├── starttime           # "2025-12-30T15:30:15Z"
                ├── completed           # "false"
                ├── pid                 # "12346"
                ├── stdout              # Test output (growing)
                └── stderr              # Error output
```
