#!/bin/bash

set -euo pipefail

if [ "$#" -ne 1 ]; then
    echo "Usage: $0 <username>"
    exit 1
fi

USERNAME="$1"

echo "Installing MobileShell for user: $USERNAME"

# Create user if it doesn't exist (idempotent)
if ! id "$USERNAME" &>/dev/null; then
    echo "Creating user $USERNAME..."
    useradd -m -s /bin/bash "$USERNAME"
else
    echo "User $USERNAME already exists, skipping user creation"
fi

# Ensure home directory exists and has correct ownership
HOME_DIR="/home/$USERNAME"
if [ ! -d "$HOME_DIR" ]; then
    echo "Creating home directory $HOME_DIR..."
    mkdir -p "$HOME_DIR"
fi

# Copy binary to user's home directory
echo "Installing binary to $HOME_DIR/mobileshell..."
cp /tmp/mobileshell-install/mobileshell "$HOME_DIR/mobileshell"
chown "$USERNAME:$USERNAME" "$HOME_DIR/mobileshell"
chmod +x "$HOME_DIR/mobileshell"

# Install systemd service file
echo "Installing systemd service..."
cp /tmp/mobileshell-install/mobileshell.service /etc/systemd/system/mobileshell.service
chmod 644 /etc/systemd/system/mobileshell.service

# Reload systemd and enable/start service
echo "Enabling and starting service..."
systemctl daemon-reload
systemctl enable mobileshell

# Restart service (idempotent - will start if not running, restart if already running)
systemctl restart mobileshell

# Show service status
echo ""
echo "Service status:"
systemctl status mobileshell --no-pager || true

echo ""
echo "Installation completed successfully!"
