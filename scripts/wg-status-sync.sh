#!/bin/bash
# WireGuard Status Sync Script
# Updates vpn_configs table with real-time peer status from WireGuard
# Run via cron or systemd timer every 30 seconds

set -euo pipefail

# Database connection (use environment variables or defaults)
DB_HOST="${DB_HOST:-localhost}"
DB_PORT="${DB_PORT:-5432}"
DB_USER="${DB_USER:-anvil}"
DB_NAME="${DB_NAME:-anvil}"
DB_PASSWORD="${DB_PASSWORD:-}"

# WireGuard interface
WG_INTERFACE="${WG_INTERFACE:-wg0}"

# Check if WireGuard interface exists
if ! ip link show "$WG_INTERFACE" &>/dev/null; then
    echo "WireGuard interface $WG_INTERFACE not found"
    exit 0
fi

# Get WireGuard dump (format: public_key, preshared_key, endpoint, allowed_ips, latest_handshake, rx, tx, keepalive)
wg_dump=$(wg show "$WG_INTERFACE" dump | tail -n +2)

if [ -z "$wg_dump" ]; then
    echo "No peers found"
    exit 0
fi

# Build SQL update statements
sql_updates=""

while IFS=$'\t' read -r public_key preshared_key endpoint allowed_ips latest_handshake rx tx keepalive; do
    # Skip if no handshake (never connected)
    if [ "$latest_handshake" = "0" ]; then
        continue
    fi
    
    # Convert Unix timestamp to PostgreSQL timestamp
    handshake_ts="to_timestamp($latest_handshake)"
    
    # Build update query
    sql_updates+="UPDATE vpn_configs SET 
        last_handshake = $handshake_ts,
        bytes_received = $rx,
        bytes_sent = $tx
        WHERE public_key = '$public_key';
    "
done <<< "$wg_dump"

if [ -n "$sql_updates" ]; then
    # Execute updates
    PGPASSWORD="$DB_PASSWORD" psql -h "$DB_HOST" -p "$DB_PORT" -U "$DB_USER" -d "$DB_NAME" -c "$sql_updates" 2>/dev/null || true
    echo "$(date): Synced $(echo "$wg_dump" | wc -l) peers"
fi
