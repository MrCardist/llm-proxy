#!/bin/bash

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BINARY_PATH="${SCRIPT_DIR}/bin/llm-proxy"
ENV_FILE="${SCRIPT_DIR}/.env"
SERVICE_NAME="llm-proxy"

GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
BLUE='\033[0;34m'
NC='\033[0m'

echo -e "${BLUE}========================================${NC}"
echo -e "${BLUE}LLM Proxy SystemD Service Installer${NC}"
echo -e "${BLUE}========================================${NC}"
echo

if [[ ! -f "$BINARY_PATH" ]]; then
    echo -e "${YELLOW}Binary not found at $BINARY_PATH${NC}"
    echo -e "${YELLOW}Attempting to build the binary...${NC}"
    echo
    
    if ! command -v go &> /dev/null; then
        echo -e "${RED}Error: Go is not installed${NC}"
        echo -e "${YELLOW}Please install Go from https://go.dev/dl/ or via your package manager${NC}"
        echo -e "${YELLOW}Example: sudo apt install golang-go (Ubuntu/Debian)${NC}"
        echo -e "${YELLOW}Example: sudo yum install golang (RHEL/CentOS)${NC}"
        echo -e "${YELLOW}Example: brew install go (macOS)${NC}"
        exit 1
    fi
    
    echo -e "${BLUE}Go version:${NC}"
    go version
    echo
    
    if [[ -f "${SCRIPT_DIR}/Makefile" ]] && command -v make &> /dev/null; then
        echo -e "${YELLOW}Building with make...${NC}"
        cd "${SCRIPT_DIR}"
        if make build; then
            echo -e "${GREEN}Binary built successfully with make${NC}"
        else
            echo -e "${RED}Make build failed${NC}"
            exit 1
        fi
    elif [[ -f "${SCRIPT_DIR}/go.mod" ]]; then
        echo -e "${YELLOW}Building with go build...${NC}"
        cd "${SCRIPT_DIR}"
        mkdir -p bin
        if go build -o bin/llm-proxy ./cmd/llm-proxy; then
            echo -e "${GREEN}Binary built successfully with go build${NC}"
        else
            echo -e "${RED}Go build failed${NC}"
            exit 1
        fi
    else
        echo -e "${RED}Error: Cannot find go.mod or Makefile in ${SCRIPT_DIR}${NC}"
        echo -e "${YELLOW}Please ensure you're running this script from the project root${NC}"
        exit 1
    fi
    
    if [[ ! -f "$BINARY_PATH" ]]; then
        echo -e "${RED}Error: Binary still not found after build attempt${NC}"
        exit 1
    fi
    
    echo -e "${GREEN}Binary is now available at $BINARY_PATH${NC}"
    echo
fi

if [[ ! -f "$ENV_FILE" ]]; then
    echo -e "${RED}Error: .env file not found at $ENV_FILE${NC}"
    echo -e "${YELLOW}Please create a .env file with your configuration${NC}"
    exit 1
fi

if [[ "$EUID" -eq 0 ]]; then
    echo -e "${GREEN}Running as root - installing system-wide service${NC}"
    IS_USER_SERVICE=false
    SERVICE_DIR="/etc/systemd/system"
    SYSTEMCTL_CMD="systemctl"
else
    echo -e "${GREEN}Running as non-root - installing user service${NC}"
    IS_USER_SERVICE=true
    SERVICE_DIR="${HOME}/.config/systemd/user"
    SYSTEMCTL_CMD="systemctl --user"
    
    mkdir -p "$SERVICE_DIR"
fi

SERVICE_FILE="${SERVICE_DIR}/${SERVICE_NAME}.service"

echo -e "${YELLOW}Creating service file: ${SERVICE_FILE}${NC}"

if [[ "$IS_USER_SERVICE" = true ]]; then
    cat > "$SERVICE_FILE" << EOF
[Unit]
Description=LLM Proxy Service
After=network.target

[Service]
Type=simple
WorkingDirectory=${SCRIPT_DIR}
EnvironmentFile=${ENV_FILE}
ExecStart=${BINARY_PATH}
Restart=on-failure
RestartSec=10
StandardOutput=journal
StandardError=journal
SyslogIdentifier=${SERVICE_NAME}

[Install]
WantedBy=default.target
EOF
else
    cat > "$SERVICE_FILE" << EOF
[Unit]
Description=LLM Proxy Service
After=network.target

[Service]
Type=simple
User=${SUDO_USER:-$USER}
Group=${SUDO_USER:-$USER}
WorkingDirectory=${SCRIPT_DIR}
EnvironmentFile=${ENV_FILE}
ExecStart=${BINARY_PATH}
Restart=on-failure
RestartSec=10
StandardOutput=journal
StandardError=journal
SyslogIdentifier=${SERVICE_NAME}

[Install]
WantedBy=multi-user.target
EOF
fi

echo -e "${GREEN}Service file created successfully${NC}"
echo

echo -e "${YELLOW}Reloading systemd daemon...${NC}"
$SYSTEMCTL_CMD daemon-reload

echo -e "${YELLOW}Enabling service to start on boot...${NC}"
$SYSTEMCTL_CMD enable ${SERVICE_NAME}.service

echo
echo -e "${GREEN}========================================${NC}"
echo -e "${GREEN}Installation Complete!${NC}"
echo -e "${GREEN}========================================${NC}"
echo

echo -e "${BLUE}Service Management Commands:${NC}"
echo -e "  ${YELLOW}Start the service:${NC}"
echo -e "    $SYSTEMCTL_CMD start ${SERVICE_NAME}"
echo
echo -e "  ${YELLOW}Stop the service:${NC}"
echo -e "    $SYSTEMCTL_CMD stop ${SERVICE_NAME}"
echo
echo -e "  ${YELLOW}Restart the service:${NC}"
echo -e "    $SYSTEMCTL_CMD restart ${SERVICE_NAME}"
echo
echo -e "  ${YELLOW}Check service status:${NC}"
echo -e "    $SYSTEMCTL_CMD status ${SERVICE_NAME}"
echo
echo -e "  ${YELLOW}View service logs:${NC}"
echo -e "    journalctl $([[ "$IS_USER_SERVICE" = true ]] && echo "--user") -u ${SERVICE_NAME} -f"
echo
echo -e "  ${YELLOW}View last 100 log lines:${NC}"
echo -e "    journalctl $([[ "$IS_USER_SERVICE" = true ]] && echo "--user") -u ${SERVICE_NAME} -n 100"

if [[ "$IS_USER_SERVICE" = true ]]; then
    echo
    echo -e "${BLUE}========================================${NC}"
    echo -e "${BLUE}Important for User Services:${NC}"
    echo -e "${BLUE}========================================${NC}"
    echo
    echo -e "${YELLOW}Enable lingering to keep service running when logged out:${NC}"
    echo -e "  loginctl enable-linger $USER"
    echo
    echo -e "${YELLOW}Check if lingering is enabled:${NC}"
    echo -e "  loginctl show-user $USER | grep Linger"
    echo
    echo -e "${YELLOW}Note:${NC} User services run only when you're logged in unless lingering is enabled."
fi

echo
echo -e "${GREEN}To start the service now, run:${NC}"
echo -e "  ${YELLOW}$SYSTEMCTL_CMD start ${SERVICE_NAME}${NC}"
echo