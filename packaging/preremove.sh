#!/bin/bash
# gdhcp package pre-remove script (deb: prerm, rpm: %preun).
#
# Called before the package's files are removed. Stops and disables the systemd service.
# Errors are swallowed (this may be a re-install, an upgrade, or removal after failure).

set -e

# Stop the service if it is running.
systemctl stop gdhcp.service 2>/dev/null || true

# Disable the service for auto-start on boot.
systemctl disable gdhcp.service 2>/dev/null || true
