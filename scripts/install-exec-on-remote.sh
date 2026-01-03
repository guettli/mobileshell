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

usermod -aG nix-users "$USERNAME"

# Copy binary opt/. Use prefix of $USER, so that the service can be installed several times (for
# different Linux users).
EXE=/opt/$USERNAME-mobileshell
echo "Installing binary to $EXE..."
# Delete exe first, otherwise: cp: cannot create regular file: Text file busy
rm -f "$EXE"
cp /tmp/mobileshell-install/mobileshell "$EXE"
chown "$USERNAME:$USERNAME" "$EXE"
chmod +x "$EXE"

# Install systemd service file
SERVICE="$USERNAME-mobileshell"
echo "Installing systemd service..."
cp /tmp/mobileshell-install/mobileshell.service /etc/systemd/system/"$SERVICE".service
chmod 644 /etc/systemd/system/"$SERVICE".service

# Reload systemd and enable/start service
echo "Enabling and starting service..."
systemctl daemon-reload
systemctl enable "$SERVICE"

# Restart service (idempotent - will start if not running, restart if already running)
systemctl restart "$SERVICE"

# Show service status
echo ""
echo "Service status:"
systemctl status "$SERVICE" --no-pager || true

# Check if hashed-passwords directory is empty
STATE_DIR="/var/lib/$SERVICE"
HASHED_PASSWORDS_DIR="$STATE_DIR/hashed-passwords"

echo ""
if [ -d "$HASHED_PASSWORDS_DIR" ] && [ -z "$(ls -A "$HASHED_PASSWORDS_DIR" 2>/dev/null)" ]; then
    echo "⚠️  WARNING: No passwords configured!"
    echo ""
    echo "To add a password, run as user $USERNAME:"
    echo "  $EXE add-password"
    echo ""
    echo "Or run as root:"
    echo "  sudo -u $USERNAME $EXE add-password"
elif [ ! -d "$HASHED_PASSWORDS_DIR" ]; then
    echo "⚠️  WARNING: hashed-passwords directory not yet created!"
    echo ""
    echo "To add a password, run as user $USERNAME:"
    echo "  $EXE add-password"
    echo ""
    echo "Or run as root:"
    echo "  sudo -u $USERNAME $EXE add-password"
fi

echo ""
echo "Installation completed successfully!"
