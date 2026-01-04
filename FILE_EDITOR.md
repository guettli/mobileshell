# File Editor Feature

The File Editor in MobileShell allows you to create and edit files directly within your workspaces through a web interface.

## Features

### 1. Create and Edit Files
- Open any file within your workspace directory
- Create new files if they don't exist
- Parent directories are automatically created when saving

### 2. Conflict Detection
- Detects if a file has been modified externally since you started editing
- Shows current file content on disk
- Displays a diff of your proposed changes vs. current content
- Prevents accidental overwrites

### 3. Auto-chmod for Scripts
- Automatically makes files executable (`chmod +x`) if they start with a shebang (`#!/`)
- Useful for shell scripts, Python scripts, etc.

### 4. Security
- All file paths are relative to the workspace directory
- Directory traversal attacks are prevented
- Files outside the workspace cannot be accessed

## Usage

### Accessing the File Editor

1. Navigate to a workspace
2. Click the "Edit Files" button in the command execution section
3. Enter a file path relative to the workspace directory
4. Click "Open File"

### Creating a New File

1. Enter the path for the new file (e.g., `scripts/deploy.sh`)
2. The editor will open with a "New File" badge
3. Enter your content
4. Click "Save File"

If the file starts with `#!/`, it will automatically be made executable:

```bash
#!/bin/bash
echo "This file will be executable after saving"
```

### Editing an Existing File

1. Enter the path to the existing file
2. The editor loads with the current content
3. Make your changes
4. Click "Save File"

### Handling Conflicts

If someone (or some process) modifies the file while you're editing:

1. The save will be rejected
2. You'll see:
   - The current file content (what's on disk)
   - A diff showing changes from current to your version
3. You can:
   - Click "Reload File" to see the current version and merge changes manually
   - Go back to the workspace

## API Endpoints

### GET `/workspaces/{id}/files`
Shows the file editor page for a workspace.

### POST `/workspaces/{id}/files/read`
Reads a file and returns its content with metadata.

**Request:**
```
file_path=relative/path/to/file.txt
```

**Response:**
- File content
- Original checksum (for conflict detection)
- Session ID
- Whether it's a new file

### POST `/workspaces/{id}/files/save`
Saves a file with conflict detection.

**Request:**
```
file_path=relative/path/to/file.txt
content=<file content>
original_checksum=<checksum from read>
```

**Response:**
- Success/failure status
- Message
- Conflict information (if detected)
- Diff of changes made

## Implementation Details

### Conflict Detection Mechanism

1. When a file is opened, its content is read and a SHA256 checksum is calculated
2. The checksum is stored in a hidden form field
3. When saving:
   - The current file is read again and its checksum calculated
   - If checksums match: file is saved
   - If checksums differ: conflict is detected, save is refused

### Directory Creation

Parent directories are created automatically using `os.MkdirAll()` with permissions `0755`.

Example:
```
File path: scripts/deploy/production.sh
Creates: workspace/scripts/ and workspace/scripts/deploy/
```

### Auto-chmod Logic

After writing a file, the content is checked:
- If it starts with `#!`: `chmod 0755` (rwxr-xr-x)
- Otherwise: `chmod 0644` (rw-r--r--)

### Security Measures

1. **Path Sanitization**: All paths are resolved to absolute paths
2. **Directory Validation**: Ensures file path is within workspace directory
3. **No Directory Traversal**: Paths like `../../../etc/passwd` are rejected
4. **Session-based Auth**: File operations require valid authentication session

## Examples

### Creating a Shell Script

1. Path: `deploy.sh`
2. Content:
```bash
#!/bin/bash
set -e

echo "Deploying application..."
git pull origin main
npm install
npm run build
pm2 restart app
echo "Deployment complete!"
```
3. Result: File created at `workspace/deploy.sh` with executable permissions

### Creating a Configuration File

1. Path: `config/app.conf`
2. Content:
```ini
[database]
host = localhost
port = 5432
name = myapp

[server]
port = 8080
host = 0.0.0.0
```
3. Result: File created at `workspace/config/app.conf` (directories created automatically)

### Handling a Conflict

**Scenario:**
1. User A opens `config.yaml` for editing
2. Meanwhile, a deployment script updates `config.yaml`
3. User A tries to save their changes

**Result:**
- Save is rejected
- User A sees:
  - Current content (from deployment script)
  - Their proposed changes as a diff
- User A can reload and merge their changes with the new content

## Technical Stack

- **Backend**: Go with `internal/fileeditor` package
- **Frontend**: HTMX for dynamic updates, Bootstrap for styling
- **File Operations**: Standard Go `os` and `filepath` packages
- **Diff Generation**: Custom unified diff implementation
- **Checksum**: SHA256 for content verification

## Future Enhancements

Potential improvements for the file editor:

- Syntax highlighting for different file types
- Integration with CodeMirror or Monaco Editor
- Multi-file editing tabs
- File tree browser
- Git integration (show git status, diff against HEAD)
- Auto-save drafts
- Collaborative editing (WebSocket-based)
- File search and replace
- Backup/restore functionality
