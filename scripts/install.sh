#!/bin/bash
# gdhcp bare-metal installer.
#
# Usage:
#   sudo ./scripts/install.sh                     # install ./dist/gdhcp (built locally)
#   sudo GDHCP_RELEASE_URL=https://.../gdhcp-linux-amd64.tar.gz ./scripts/install.sh
#
# Idempotent: safe to re-run (e.g. to pick up a new binary) — existing
# config.yaml and gdhcp.env are never overwritten.
set -euo pipefail

INSTALL_DIR="/usr/local/bin"
CONFIG_DIR="/etc/gdhcp"
DATA_DIR="/var/lib/gdhcp"
LOG_DIR="/var/log/gdhcp"
SYSTEMD_DIR="/etc/systemd/system"
REPO_ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." >/dev/null 2>&1 && pwd)"

log() { printf '==> %s\n' "$1"; }
die() { printf 'error: %s\n' "$1" >&2; exit 1; }

[ "$(id -u)" -eq 0 ] || die "must be run as root (try: sudo $0)"

# --- gdhcp system user -------------------------------------------------
if ! id -u gdhcp >/dev/null 2>&1; then
    useradd --system --shell /usr/sbin/nologin --home-dir "$DATA_DIR" --no-create-home gdhcp
    log "created gdhcp system user"
else
    log "gdhcp user already exists"
fi

# --- directories ---------------------------------------------------------
mkdir -p "$CONFIG_DIR" "$DATA_DIR" "$LOG_DIR"
chown gdhcp:gdhcp "$CONFIG_DIR" "$DATA_DIR" "$LOG_DIR"
chmod 750 "$CONFIG_DIR" "$DATA_DIR" "$LOG_DIR"

# --- binary ---------------------------------------------------------------
BIN_SRC=""
if [ -n "${GDHCP_RELEASE_URL:-}" ]; then
    log "downloading binary from ${GDHCP_RELEASE_URL}"
    TMP_DL="$(mktemp -d)"
    trap 'rm -rf "$TMP_DL"' EXIT
    case "$GDHCP_RELEASE_URL" in
        *.tar.gz|*.tgz)
            curl -fsSL "$GDHCP_RELEASE_URL" -o "$TMP_DL/gdhcp.tar.gz"
            tar -xzf "$TMP_DL/gdhcp.tar.gz" -C "$TMP_DL"
            BIN_SRC="$(find "$TMP_DL" -maxdepth 2 -type f -name gdhcp | head -n1)"
            [ -n "$BIN_SRC" ] || die "no 'gdhcp' binary found inside ${GDHCP_RELEASE_URL}"
            ;;
        *)
            curl -fsSL "$GDHCP_RELEASE_URL" -o "$TMP_DL/gdhcp"
            BIN_SRC="$TMP_DL/gdhcp"
            ;;
    esac
elif [ -x "$REPO_ROOT/dist/gdhcp" ]; then
    BIN_SRC="$REPO_ROOT/dist/gdhcp"
    log "using locally built binary at $BIN_SRC"
else
    die "no binary found: build with 'make build' first (see ./dist/gdhcp), or set GDHCP_RELEASE_URL"
fi

install -m 0755 -o root -g root "$BIN_SRC" "${INSTALL_DIR}/gdhcp"
log "installed ${INSTALL_DIR}/gdhcp ($("${INSTALL_DIR}/gdhcp" version 2>/dev/null || echo 'version unknown'))"

# --- config ----------------------------------------------------------------
if [ ! -f "$CONFIG_DIR/config.yaml" ]; then
    EXAMPLE="$REPO_ROOT/config.yaml.example"
    [ -f "$EXAMPLE" ] || die "config.yaml.example not found at $EXAMPLE"

    read -r -p "PostgreSQL DSN [postgres://gdhcp:gdhcp@localhost:5432/gdhcp?sslmode=disable]: " DSN
    DSN="${DSN:-postgres://gdhcp:gdhcp@localhost:5432/gdhcp?sslmode=disable}"

    sed -E "s#^(\s*dsn:).*#\1 \"${DSN//\#/\\#}\"#" "$EXAMPLE" > "$CONFIG_DIR/config.yaml"
    chown gdhcp:gdhcp "$CONFIG_DIR/config.yaml"
    chmod 600 "$CONFIG_DIR/config.yaml"
    log "wrote ${CONFIG_DIR}/config.yaml — review it, especially dhcp.interfaces and ha.* before starting"
else
    log "${CONFIG_DIR}/config.yaml already exists, leaving it untouched"
fi

if [ ! -f "$CONFIG_DIR/gdhcp.env" ]; then
    install -m 0600 -o gdhcp -g gdhcp "$REPO_ROOT/deploy/systemd/gdhcp.env" "$CONFIG_DIR/gdhcp.env"
fi

# --- systemd unit -----------------------------------------------------------
install -m 0644 "$REPO_ROOT/deploy/systemd/gdhcp.service" "${SYSTEMD_DIR}/gdhcp.service"
systemctl daemon-reload

# Fallback if AmbientCapabilities is unsupported (systemd < 229 / very old
# distros): grant the same capabilities directly on the binary via setcap.
# Harmless to apply even when AmbientCapabilities does work — the two
# mechanisms grant the same three capabilities redundantly in that case.
SYSTEMD_VERSION="$(systemctl --version 2>/dev/null | awk 'NR==1{print $2}')"
if command -v setcap >/dev/null 2>&1 && { [ -z "$SYSTEMD_VERSION" ] || [ "$SYSTEMD_VERSION" -lt 229 ] 2>/dev/null; }; then
    log "systemd ${SYSTEMD_VERSION:-unknown} may predate AmbientCapabilities support (v229); applying setcap fallback"
    setcap 'cap_net_admin,cap_net_raw,cap_net_bind_service=+eip' "${INSTALL_DIR}/gdhcp" \
        || log "warning: setcap fallback failed; gdhcp may be unable to bind port 67 / manage the VIP"
fi

systemctl enable gdhcp.service

echo ""
echo "gdhcp installed."
echo "  1. Review ${CONFIG_DIR}/config.yaml (dhcp.interfaces, ha.*, server.jwt_secret)"
echo "  2. Ensure the database is reachable and migrated: gdhcp migrate --config-file ${CONFIG_DIR}/config.yaml"
echo "  3. Start:   systemctl start gdhcp"
echo "  4. Status:  systemctl status gdhcp"
echo "  5. Logs:    journalctl -u gdhcp -f"
echo ""
echo "Default admin login (migration 0003_seed_admin.sql): username 'admin', password 'admin'."
echo "*** CHANGE THIS PASSWORD IMMEDIATELY after first login — it is a well-known default. ***"
