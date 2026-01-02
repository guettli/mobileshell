#!/bin/bash

set -euo pipefail

if [ "$#" -ne 2 ]; then
    echo "Usage: $0 <hostname> <username>"
    echo "Example: $0 myserver.example.com myuser"
    exit 1
fi

HOSTNAME="$1"
USERNAME="$2"

echo "Installing MobileShell to $HOSTNAME as user $USERNAME"

# Build the binary
echo "Building mobileshell binary..."
./scripts/build.sh

# Render the systemd service file
echo "Rendering systemd service file..."
TMP_SERVICE_FILE="/tmp/mobileshell.service"
sed "s/{{USER}}/$USERNAME/g" systemd/mobileshell.service >"$TMP_SERVICE_FILE"

# Create temporary directory for rsync
TMP_DIR=$(mktemp -d)
trap "rm -rf $TMP_DIR $TMP_SERVICE_FILE" EXIT

# Copy files to temporary directory
cp mobileshell "$TMP_DIR/"
cp "$TMP_SERVICE_FILE" "$TMP_DIR/mobileshell.service"
cp scripts/install-exec-on-remote.sh "$TMP_DIR/"
chmod +x "$TMP_DIR/install-exec-on-remote.sh"

# Rsync files to remote server
echo "Copying files to remote server..."
rsync -av --progress "$TMP_DIR/" "root@$HOSTNAME:/tmp/mobileshell-install/"

# Execute installation script on remote server
echo "Installing and starting systemd service..."
ssh "root@$HOSTNAME" "/tmp/mobileshell-install/install-exec-on-remote.sh $USERNAME"

# Clean up remote temporary directory
ssh "root@$HOSTNAME" "rm -rf /tmp/mobileshell-install"

echo ""
echo "=== Installation Complete ==="
echo "MobileShell is now running on $HOSTNAME"
echo "Access it at: http://localhost:22123/"
echo ""
echo "Make sure to configure TLS termination (e.g., nginx) for production use."
