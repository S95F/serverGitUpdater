#!/usr/bin/env bash
# Restart the serverGitUpdater systemd service.
#
# Usage: sudo ./scripts/restart.sh
#
# Override the unit name with SGU_UNIT if you installed it under a
# non-default name.

set -euo pipefail

UNIT_NAME="${SGU_UNIT:-servergitupdater}"

log()  { printf '\033[1;34m::\033[0m %s\n' "$*" >&2; }
warn() { printf '\033[1;33m::\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31m::\033[0m %s\n' "$*" >&2; exit 1; }

if [ "$(id -u)" -ne 0 ]; then
  die "must run as root (try: sudo $0)"
fi

if ! systemctl list-unit-files | grep -q "^${UNIT_NAME}\.service"; then
  die "unit ${UNIT_NAME}.service not found; install it first with ./scripts/install.sh"
fi

log "restarting ${UNIT_NAME}.service"
systemctl restart "${UNIT_NAME}.service"

# Give the unit a moment to come back up before reporting.
sleep 0.5

log "status:"
systemctl status "${UNIT_NAME}.service" --no-pager --lines=5 || true
