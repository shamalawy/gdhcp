#!/bin/bash
# gdhcp package post-install script (deb: postinst, rpm: %post).
#
# Called after the package's files are installed. Creates the system user,
# directories, handles capabilities, and enables the systemd service.
# Idempotent: safe to re-run on upgrade (existing config.yaml is never overwritten by this script).

set -e

CONFIG_DIR="/etc/gdhcp"
DATA_DIR="/var/lib/gdhcp"
LOG_DIR="/var/log/gdhcp"

# Create gdhcp system user if it doesn't exist (idempotent).
if ! id -u gdhcp >/dev/null 2>&1; then
    useradd --system --shell /usr/sbin/nologin --home-dir "$DATA_DIR" --no-create-home gdhcp || true
fi

# Create directories with proper ownership and permissions.
mkdir -p "$CONFIG_DIR" "$DATA_DIR" "$LOG_DIR"
chown gdhcp:gdhcp "$CONFIG_DIR" "$DATA_DIR" "$LOG_DIR"
chmod 750 "$CONFIG_DIR" "$DATA_DIR" "$LOG_DIR"

# Reload systemd to pick up the newly installed gdhcp.service unit.
systemctl daemon-reload || true

# Fallback if AmbientCapabilities is unsupported (systemd < v229, extremely rare now).
# The gdhcp.service unit already declares AmbientCapabilities, which is sufficient
# for modern systemd (v229+, released 2016). For ancient distros without it, we apply
# setcap directly on the binary as a belt-and-suspenders guard (harmless no-op on modern systems).
if command -v setcap >/dev/null 2>&1; then
    SYSTEMD_VERSION=$(systemctl --version 2>/dev/null | awk 'NR==1{print $2}' || echo 0)
    if [ "$SYSTEMD_VERSION" -lt 229 ] 2>/dev/null; then
        setcap 'cap_net_admin,cap_net_raw,cap_net_bind_service=+eip' /usr/local/bin/gdhcp || true
    fi
fi

# Enable the service for auto-start on boot, but do NOT start it.
# The operator must review /etc/gdhcp/config.yaml (especially dhcp.interfaces and ha.*)
# and run 'gdhcp migrate --config-file /etc/gdhcp/config.yaml' before first start.
systemctl enable gdhcp.service || true

# Print next steps (for interactive installs; package managers may suppress this).
cat >&2 <<'EOF'

==> gdhcp installed.

NEXT STEPS:
1. Review /etc/gdhcp/config.yaml (especially dhcp.interfaces, ha.*, server.jwt_secret)
2. Ensure PostgreSQL is reachable and migrated:
     gdhcp migrate --config-file /etc/gdhcp/config.yaml
3. Start the service:
     systemctl start gdhcp
4. View status and logs:
     systemctl status gdhcp
     journalctl -u gdhcp -f

SECURITY: The default admin password (from migration 0003_seed_admin.sql) is
'admin'. Change it immediately after first login at http://<host>:8080.

EOF
