#!/bin/bash

# Install systemd service for Bedrock Usage Dashboard
# Usage: ./install-systemd.sh bedrock-usage 5000

set -e

# Check arguments
if [ $# -ne 2 ]; then
    echo "Usage: $0 <dashboard-name> <port>"
    echo "Example: $0 bedrock-usage 5000"
    exit 1
fi

DASHBOARD_NAME="$1"
PORT="$2"
SERVICE_NAME="$DASHBOARD_NAME"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DASHBOARD_DIR="$SCRIPT_DIR/$DASHBOARD_NAME"
PYTHON_FILE="app.py"

# Validate dashboard directory exists
if [ ! -d "$DASHBOARD_DIR" ]; then
    echo "❌ Error: Dashboard directory '$DASHBOARD_DIR' not found"
    exit 1
fi

# Validate app.py exists in dashboard
if [ ! -f "$DASHBOARD_DIR/$PYTHON_FILE" ]; then
    echo "❌ Error: $PYTHON_FILE not found in $DASHBOARD_DIR"
    exit 1
fi

# Validate port is a number
if ! [[ "$PORT" =~ ^[0-9]+$ ]] || [ "$PORT" -lt 1 ] || [ "$PORT" -gt 65535 ]; then
    echo "❌ Error: Port must be a number between 1 and 65535"
    exit 1
fi

echo "🔧 Installing systemd service for $DASHBOARD_NAME on port $PORT..."

# Detect if running as root
IS_ROOT=false
if [ "$EUID" -eq 0 ]; then
    IS_ROOT=true
fi

# Setup .env file in main directory
if [ ! -f "$SCRIPT_DIR/.env" ]; then
    echo "📋 .env file not found, copying from .env.default..."
    if [ -f "$SCRIPT_DIR/.env.default" ]; then
        cp "$SCRIPT_DIR/.env.default" "$SCRIPT_DIR/.env"
        echo "✅ Created .env from .env.default"
    else
        echo "⚠️  Warning: .env.default not found, creating minimal .env"
        touch "$SCRIPT_DIR/.env"
    fi
else
    echo "📋 .env file already exists, skipping copy"
fi

# Create .venv in root directory if it doesn't exist
if [ ! -d "$SCRIPT_DIR/.venv" ]; then
    echo "🐍 Creating Python virtual environment..."
    python3 -m venv "$SCRIPT_DIR/.venv"
    echo "✅ Virtual environment created"

    echo "📦 Installing dependencies..."
    "$SCRIPT_DIR/.venv/bin/pip" install -q -r "$SCRIPT_DIR/requirements.txt"
    echo "✅ Dependencies installed"
else
    echo "✅ Virtual environment already exists"
fi

# Create systemd service file
SERVICE_FILE="/tmp/$SERVICE_NAME.service"

if [ "$IS_ROOT" = true ]; then
    SERVICE_DIR="/etc/systemd/system"
    ENABLE_ARGS=""
    DAEMON_RELOAD="systemctl daemon-reload"
    ENABLE_CMD="systemctl enable $SERVICE_NAME"
    START_CMD="systemctl start $SERVICE_NAME"
    STATUS_CMD="systemctl status $SERVICE_NAME"
    JOURNAL_CMD="journalctl -u $SERVICE_NAME -f"
else
    SERVICE_DIR="$HOME/.config/systemd/user"
    ENABLE_ARGS="--user"
    DAEMON_RELOAD="systemctl --user daemon-reload"
    ENABLE_CMD="systemctl --user enable $SERVICE_NAME"
    START_CMD="systemctl --user start $SERVICE_NAME"
    STATUS_CMD="systemctl --user status $SERVICE_NAME"
    JOURNAL_CMD="journalctl --user -u $SERVICE_NAME -f"

    # Ensure user systemd directory exists
    mkdir -p "$SERVICE_DIR"
fi

cat > "$SERVICE_FILE" << 'SERVICEEOF'
[Unit]
Description=DASHBOARD_NAME Dashboard - PORT
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=USERNAME
WorkingDirectory=DASHDIR
Environment="PATH=ROOTDIR/.venv/bin:/usr/local/bin:/usr/bin:/bin"
ExecStart=ROOTDIR/.venv/bin/python DASHDIR/PYTHONFILE --port PORT
Restart=on-failure
RestartSec=10
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
SERVICEEOF

# Replace placeholders
sed -i "s|DASHDIR|$DASHBOARD_DIR|g" "$SERVICE_FILE"
sed -i "s|ROOTDIR|$SCRIPT_DIR|g" "$SERVICE_FILE"
sed -i "s|PYTHONFILE|$PYTHON_FILE|g" "$SERVICE_FILE"
sed -i "s|PORT|$PORT|g" "$SERVICE_FILE"
sed -i "s|USERNAME|$(whoami)|g" "$SERVICE_FILE"
sed -i "s|DASHBOARD_NAME|$DASHBOARD_NAME|g" "$SERVICE_FILE"
sed -i "s|Description=.*|Description=$DASHBOARD_NAME Dashboard on port $PORT|" "$SERVICE_FILE"

if [ "$IS_ROOT" = true ]; then
    sed -i "s|WantedBy=.*|WantedBy=multi-user.target|" "$SERVICE_FILE"
else
    sed -i "s|WantedBy=.*|WantedBy=default.target|" "$SERVICE_FILE"
fi

# Copy service file to systemd directory
if [ "$IS_ROOT" = true ]; then
    echo "📝 Installing service file to $SERVICE_DIR/$SERVICE_NAME.service..."
    cp "$SERVICE_FILE" "$SERVICE_DIR/$SERVICE_NAME.service"
else
    echo "📝 Installing service file to $SERVICE_DIR/$SERVICE_NAME.service..."
    cp "$SERVICE_FILE" "$SERVICE_DIR/$SERVICE_NAME.service"
fi

# Reload systemd
echo "🔄 Reloading systemd..."
eval "$DAEMON_RELOAD"

# Enable service
echo "✅ Enabling service..."
eval "$ENABLE_CMD"

# Start service
echo "🚀 Starting service..."
eval "$START_CMD"

# Print status
echo ""
echo "═══════════════════════════════════════════════════════════"
echo "✅ Service installed successfully!"
echo "═══════════════════════════════════════════════════════════"
echo ""
echo "📋 Service Details:"
echo "   Service name: $SERVICE_NAME"
echo "   Dashboard: $DASHBOARD_NAME"
echo "   Dashboard directory: $DASHBOARD_DIR"
echo "   Python file: $PYTHON_FILE"
echo "   Port: $PORT"
echo "   Virtual environment: $SCRIPT_DIR/.venv (shared)"
echo "   Service file: $SERVICE_DIR/$SERVICE_NAME.service"
echo ""
echo "📝 Useful Commands:"
echo "   View status:    $STATUS_CMD"
echo "   View logs:      $JOURNAL_CMD"
echo "   Stop service:   systemctl $ENABLE_ARGS stop $SERVICE_NAME"
echo "   Restart service: systemctl $ENABLE_ARGS restart $SERVICE_NAME"
echo "   Disable service: systemctl $ENABLE_ARGS disable $SERVICE_NAME"
echo ""

if [ "$IS_ROOT" = false ]; then
    echo "👤 User-Mode Systemd Installation:"
    echo "   - Service is installed in: $SERVICE_DIR"
    echo "   - Service runs as user: $(whoami)"
    echo "   - Service starts automatically on login"
    echo ""
    echo "⏱️  To enable linger (service runs even when not logged in):"
    echo "   loginctl enable-linger"
    echo ""
    echo "📖 For more info on user services, see:"
    echo "   https://wiki.archlinux.org/title/Systemd/User"
    echo ""
fi

echo "🎉 Dashboard URL: http://localhost:$PORT"
echo ""
