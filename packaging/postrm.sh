#!/bin/bash
# gdhcp package post-remove script (deb: postrm, rpm: %postun).
#
# Called after the package's files are removed.
# Debian: $1 = "remove" (normal removal, keep config/data) or "purge" (delete all)
# RPM:    $1 = 0 (uninstall) or 1 (upgrade)
#
# We only delete config, data, logs, and the gdhcp user on explicit purge/uninstall,
# not on normal removal (deb) or upgrade (rpm).

set -e

CONFIG_DIR="/etc/gdhcp"
DATA_DIR="/var/lib/gdhcp"
LOG_DIR="/var/log/gdhcp"

# Reload systemd to drop the gdhcp.service unit from cache.
systemctl daemon-reload 2>/dev/null || true

# Determine if we should purge (delete config/data/logs/user).
# deb postrm: purge on $1 = "purge"
# rpm %postun: purge on $1 = 0 (uninstall, not upgrade)
case "$1" in
    purge)
        # Debian purge removal
        rm -rf "$CONFIG_DIR" "$DATA_DIR" "$LOG_DIR"
        userdel gdhcp 2>/dev/null || true
        ;;
    0)
        # RPM uninstall (not an upgrade)
        rm -rf "$CONFIG_DIR" "$DATA_DIR" "$LOG_DIR"
        userdel gdhcp 2>/dev/null || true
        ;;
    remove|1|disappear|abort-install|abort-upgrade)
        # Debian normal removal (1=upgrade case in rpm, keep data for rollback)
        # Do nothing; preserve config, data, logs, and user.
        ;;
    *)
        # Unknown argument; play it safe and preserve everything.
        ;;
esac
