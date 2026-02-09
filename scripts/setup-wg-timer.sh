#!/bin/bash
# Setup WireGuard Status Sync with systemd timer (runs every 10 seconds)

set -e

echo "Setting up WireGuard Status Sync..."

# Copy service and timer files to systemd
sudo cp ~/Anvil/scripts/wg-status-sync.service /etc/systemd/system/
sudo cp ~/Anvil/scripts/wg-status-sync.timer /etc/systemd/system/

# Reload systemd
sudo systemctl daemon-reload

# Enable and start timer
sudo systemctl enable wg-status-sync.timer
sudo systemctl start wg-status-sync.timer

# Stop old cron-based sync if running
crontab -l | grep -v "wg-status-sync.sh" | crontab - 2>/dev/null || true
