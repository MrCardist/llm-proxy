#!/bin/bash

set -e

SERVICE_NAME="llm-proxy"

GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
BLUE='\033[0;34m'
NC='\033[0m'

echo -e "${BLUE}========================================${NC}"
echo -e "${BLUE}LLM Proxy SystemD Service Uninstaller${NC}"
echo -e "${BLUE}========================================${NC}"
echo

if [[ "$EUID" -eq 0 ]]; then
    echo -e "${GREEN}Running as root - uninstalling system-wide service${NC}"
    IS_USER_SERVICE=false
    SERVICE_DIR="/etc/systemd/system"
    SYSTEMCTL_CMD="systemctl"
else
    echo -e "${GREEN}Running as non-root - uninstalling user service${NC}"
    IS_USER_SERVICE=true
    SERVICE_DIR="${HOME}/.config/systemd/user"
    SYSTEMCTL_CMD="systemctl --user"
fi

SERVICE_FILE="${SERVICE_DIR}/${SERVICE_NAME}.service"

if [[ ! -f "$SERVICE_FILE" ]]; then
    echo -e "${YELLOW}Warning: Service file not found at ${SERVICE_FILE}${NC}"
    echo -e "${YELLOW}Service may not be installed or was installed in a different mode${NC}"
    echo
    echo -e "${BLUE}Checking alternate location...${NC}"
    
    if [[ "$IS_USER_SERVICE" = true ]]; then
        ALT_SERVICE_FILE="/etc/systemd/system/${SERVICE_NAME}.service"
        if [[ -f "$ALT_SERVICE_FILE" ]]; then
            echo -e "${RED}Found system-wide service at ${ALT_SERVICE_FILE}${NC}"
            echo -e "${RED}Please run this script with sudo to uninstall the system-wide service${NC}"
            exit 1
        fi
    else
        ALT_SERVICE_FILE="${HOME}/.config/systemd/user/${SERVICE_NAME}.service"
        if [[ -f "$ALT_SERVICE_FILE" ]]; then
            echo -e "${RED}Found user service at ${ALT_SERVICE_FILE}${NC}"
            echo -e "${RED}Please run this script without sudo to uninstall the user service${NC}"
            exit 1
        fi
    fi
    
    echo -e "${YELLOW}No service file found - nothing to uninstall${NC}"
    exit 0
fi

echo -e "${YELLOW}Checking if service is running...${NC}"
if $SYSTEMCTL_CMD is-active --quiet ${SERVICE_NAME}.service; then
    echo -e "${YELLOW}Service is running - stopping it...${NC}"
    $SYSTEMCTL_CMD stop ${SERVICE_NAME}.service
    echo -e "${GREEN}Service stopped${NC}"
else
    echo -e "${GREEN}Service is not running${NC}"
fi

echo -e "${YELLOW}Checking if service is enabled...${NC}"
if $SYSTEMCTL_CMD is-enabled --quiet ${SERVICE_NAME}.service 2>/dev/null; then
    echo -e "${YELLOW}Service is enabled - disabling it...${NC}"
    $SYSTEMCTL_CMD disable ${SERVICE_NAME}.service
    echo -e "${GREEN}Service disabled${NC}"
else
    echo -e "${GREEN}Service is not enabled${NC}"
fi

echo -e "${YELLOW}Removing service file: ${SERVICE_FILE}${NC}"
rm -f "$SERVICE_FILE"
echo -e "${GREEN}Service file removed${NC}"

echo -e "${YELLOW}Reloading systemd daemon...${NC}"
$SYSTEMCTL_CMD daemon-reload
echo -e "${GREEN}Daemon reloaded${NC}"

echo
echo -e "${GREEN}========================================${NC}"
echo -e "${GREEN}Uninstallation Complete!${NC}"
echo -e "${GREEN}========================================${NC}"
echo

echo -e "${BLUE}Post-Uninstallation Information:${NC}"
echo
echo -e "  ${YELLOW}Check for any remaining logs:${NC}"
echo -e "    journalctl $([[ "$IS_USER_SERVICE" = true ]] && echo "--user") -u ${SERVICE_NAME}"
echo
echo -e "  ${YELLOW}Clean up old logs (optional):${NC}"
echo -e "    journalctl $([[ "$IS_USER_SERVICE" = true ]] && echo "--user") --vacuum-time=1s"

if [[ "$IS_USER_SERVICE" = true ]]; then
    echo
    echo -e "${BLUE}User Service Note:${NC}"
    echo -e "  If you had enabled lingering and no longer need it, you can disable it:"
    echo -e "    ${YELLOW}loginctl disable-linger $USER${NC}"
    echo
    echo -e "  Check current linger status:"
    echo -e "    ${YELLOW}loginctl show-user $USER | grep Linger${NC}"
fi

echo
echo -e "${GREEN}The service has been successfully uninstalled.${NC}"
echo -e "${GREEN}The binary and configuration files remain intact at:${NC}"
echo -e "  Binary: ${YELLOW}$(pwd)/bin/llm-proxy${NC}"
echo -e "  Config: ${YELLOW}$(pwd)/.env${NC}"
echo