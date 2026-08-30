#!/bin/bash
# Reverses scripts/install.sh. Config and data are preserved by default;
# pass --purge to also delete them (and the gdhcp system user).
set -euo pipefail

INSTALL_DIR="/usr/local/bin"
CONFIG_DIR="/etc/gdhcp"
DATA_DIR="/var/lib/gdhcp"
LOG_DIR="/var/log/gdhcp"
SYSTEMD_DIR="/etc/systemd/system"
PURGE=0

for arg in "$@"; do
    case "$arg" in
        --purge) PURGE=1 ;;
        *) echo "unknown argument: $arg" >&2; exit 1 ;;
    esac
done

[ "$(id -u)" -eq 0 ] || { echo "error: must be run as root (try: sudo $0)" >&2; exit 1; }

echo "==> stopping and disabling gdhcp.service"
systemctl stop gdhcp.service 2>/dev/null || true
systemctl disable gdhcp.service 2>/dev/null || true

rm -f "${SYSTEMD_DIR}/gdhcp.service"
systemctl daemon-reload

rm -f "${INSTALL_DIR}/gdhcp"

if [ "$PURGE" -eq 1 ]; then
    echo "==> --purge: removing config, data, logs, and the gdhcp user"
    rm -rf "$CONFIG_DIR" "$DATA_DIR" "$LOG_DIR"
    userdel gdhcp 2>/dev/null || true
    echo "gdhcp fully removed (config/data/logs deleted). The database itself was NOT touched."
else
    echo "gdhcp uninstalled."
    echo "Config and data preserved at: ${CONFIG_DIR}, ${DATA_DIR}, ${LOG_DIR}"
    echo "To remove them too, re-run: $0 --purge"
fi
