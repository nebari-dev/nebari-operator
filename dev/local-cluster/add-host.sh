#!/usr/bin/env bash

# Helper script to add NIC operator hostnames to /etc/hosts
# This makes it easier to test applications

set -euo pipefail

# Color codes
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

log_info() {
    echo -e "${BLUE}ℹ️  $1${NC}"
}

log_success() {
    echo -e "${GREEN}✅ $1${NC}"
}

log_warning() {
    echo -e "${YELLOW}⚠️  $1${NC}"
}

HOSTNAME="${1:-}"

if [ -z "$HOSTNAME" ]; then
    log_info "Usage: $0 <hostname>"
    echo ""
    echo "Examples:"
    echo "  $0 nginx-demo.nic.local"
    echo "  $0 nginx-demo-auth.nic.local"
    echo ""
    echo "Current .nic.local entries in /etc/hosts:"
    grep "\.nic\.local" /etc/hosts 2>/dev/null || echo "  (none)"
    exit 0
fi

# Check if entry already exists
if grep -q "$HOSTNAME" /etc/hosts 2>/dev/null; then
    CURRENT_IP=$(grep "$HOSTNAME" /etc/hosts | awk '{print $1}' | head -1)
    if [ "$CURRENT_IP" = "127.0.0.1" ]; then
        log_success "Hostname $HOSTNAME already in /etc/hosts"
        exit 0
    else
        log_warning "Hostname $HOSTNAME exists with IP $CURRENT_IP"
        read -p "Update to 127.0.0.1? (y/N): " -n 1 -r
        echo
        if [[ ! $REPLY =~ ^[Yy]$ ]]; then
            exit 0
        fi
        sudo sed -i '' "/$HOSTNAME/d" /etc/hosts
    fi
fi

# Add entry
log_info "Adding $HOSTNAME to /etc/hosts..."
echo "127.0.0.1  $HOSTNAME" | sudo tee -a /etc/hosts >/dev/null
log_success "Added $HOSTNAME to /etc/hosts"

log_info "You can now access: http://$HOSTNAME"
