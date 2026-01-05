# File Editor Feature

The File Editor in MobileShell allows you to create and edit files directly within your
workspaces through a web interface.

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

If the file starts with `#!/`, it will automatically be made executable.

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

```text
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

```text
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

```text
File path: scripts/deploy/production.sh
Creates: workspace/scripts/ and workspace/scripts/deploy/
```

### Auto-chmod Logic

After writing a file, the content is checked:

- If it starts with `#!`: `chmod 0755` (rwxr-xr-x)
- Otherwise: `chmod 0644` (rw-r--r--)

### Security Measures

File operations require valid authentication session. File access is controlled by Unix file
permissions.

