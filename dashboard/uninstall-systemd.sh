#!/bin/bash

# Uninstall systemd service for Dashboard
# Usage: ./uninstall-systemd.sh bedrock-usage

set -e

# Check arguments
if [ $# -ne 1 ]; then
    echo "Usage: $0 <dashboard-name>"
    echo "Example: $0 bedrock-usage"
    exit 1
fi

DASHBOARD_NAME="$1"
SERVICE_NAME="$DASHBOARD_NAME"

# Detect if running as root
IS_ROOT=false
if [ "$EUID" -eq 0 ]; then
    IS_ROOT=true
fi

if [ "$IS_ROOT" = true ]; then
    SERVICE_DIR="/etc/systemd/system"
    ENABLE_ARGS=""
    DAEMON_RELOAD="systemctl daemon-reload"
    STOP_CMD="systemctl stop $SERVICE_NAME"
    DISABLE_CMD="systemctl disable $SERVICE_NAME"
else
    SERVICE_DIR="$HOME/.config/systemd/user"
    ENABLE_ARGS="--user"
    DAEMON_RELOAD="systemctl --user daemon-reload"
    STOP_CMD="systemctl --user stop $SERVICE_NAME"
    DISABLE_CMD="systemctl --user disable $SERVICE_NAME"
fi

SERVICE_FILE="$SERVICE_DIR/$SERVICE_NAME.service"

# Check if service file exists
if [ ! -f "$SERVICE_FILE" ]; then
    echo "❌ Error: Service file not found at $SERVICE_FILE"
    echo ""
    echo "Available services:"
    if [ "$IS_ROOT" = true ]; then
        ls -1 /etc/systemd/system/ | grep -E "^[a-z-]+\.service$" || echo "   (no services found)"
    else
        ls -1 "$SERVICE_DIR" 2>/dev/null | grep -E "^[a-z-]+\.service$" || echo "   (no services found)"
    fi
    exit 1
fi

echo "🛑 Uninstalling systemd service: $SERVICE_NAME"
echo ""

# Confirm uninstall
read -p "Are you sure you want to uninstall the $SERVICE_NAME service? (y/N) " -n 1 -r
echo
if [[ ! $REPLY =~ ^[Yy]$ ]]; then
    echo "❌ Uninstall cancelled"
    exit 0
fi

# Stop service
echo "⏹️  Stopping service..."
if eval "$STOP_CMD" 2>/dev/null; then
    echo "✅ Service stopped"
else
    echo "⚠️  Service was not running (this is OK)"
fi

# Disable service
echo "🔌 Disabling service..."
eval "$DISABLE_CMD"

# Remove service file
echo "🗑️  Removing service file..."
rm -f "$SERVICE_FILE"

# Reload systemd
echo "🔄 Reloading systemd..."
eval "$DAEMON_RELOAD"

echo ""
echo "═══════════════════════════════════════════════════════════"
echo "✅ Service uninstalled successfully!"
echo "═══════════════════════════════════════════════════════════"
echo ""
echo "📋 Details:"
echo "   Service name: $SERVICE_NAME"
echo "   Service file removed from: $SERVICE_FILE"
echo ""

if [ "$IS_ROOT" = false ]; then
    echo "👤 User-Mode Systemd:"
    echo "   - Service was in: $SERVICE_DIR"
    echo ""
    echo "💡 Note: If linger was enabled, you can disable it with:"
    echo "   loginctl disable-linger"
    echo ""
fi

echo "ℹ️  To view remaining systemd services:"
if [ "$IS_ROOT" = true ]; then
    echo "   systemctl list-unit-files --state=enabled"
else
    echo "   systemctl --user list-unit-files --state=enabled"
fi
echo ""
