#!/usr/bin/env bash
# Install serverGitUpdater as a systemd service.
#
# Usage:
#   sudo ./scripts/install.sh [install|uninstall|purge|status]
#
# Re-running `install` is safe: the binary is rebuilt and re-installed,
# the unit is re-written, and the service is restarted. Existing config,
# logs, and managed apps are preserved.
#
# Override paths/users via env vars before running:
#   SGU_USER          system user to run the daemon as (default: sgu)
#   SGU_BIN           install path for the binary (default: /usr/local/bin/serverGitUpdater)
#   SGU_CONFIG_DIR    config directory (default: /etc/servergitupdater)
#   SGU_LOG_DIR       log directory (default: /var/log/servergitupdater)
#   SGU_DATA_DIR      data root (default: /srv/servergitupdater)
#   SGU_REPOS_DIR     where managed repos live (default: $SGU_DATA_DIR/repos)
#   SGU_LISTEN        listen address (default: 127.0.0.1:8080)

set -euo pipefail

USER_NAME="${SGU_USER:-sgu}"
GROUP_NAME="${SGU_GROUP:-$USER_NAME}"
INSTALL_BIN="${SGU_BIN:-/usr/local/bin/serverGitUpdater}"
CONFIG_DIR="${SGU_CONFIG_DIR:-/etc/servergitupdater}"
CONFIG_FILE="${SGU_CONFIG_FILE:-$CONFIG_DIR/config.json}"
LOG_DIR="${SGU_LOG_DIR:-/var/log/servergitupdater}"
DATA_DIR="${SGU_DATA_DIR:-/srv/servergitupdater}"
REPOS_DIR="${SGU_REPOS_DIR:-$DATA_DIR/repos}"
LISTEN_ADDR="${SGU_LISTEN:-127.0.0.1:8080}"
UNIT_NAME="servergitupdater"
UNIT_FILE="/etc/systemd/system/${UNIT_NAME}.service"

# Resolve the repo root from the script's location so this works whether you
# run it as `./scripts/install.sh` or with an absolute path.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

log()  { printf '\033[1;34m::\033[0m %s\n' "$*" >&2; }
warn() { printf '\033[1;33m::\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31m::\033[0m %s\n' "$*" >&2; exit 1; }

need_root() {
  [ "$(id -u)" -eq 0 ] || die "must run as root (try: sudo $0 $*)"
}

usage() {
  cat <<EOF
Install serverGitUpdater as a systemd service.

Usage: sudo $0 [install|uninstall|purge|status]
  install     Build, install the binary, create user/dirs, write the unit,
              run --init-user the first time, and start the service.
              Re-running upgrades the binary in place and restarts.
  uninstall   Stop & disable the service and remove the unit. Config,
              logs, repos, and the system user are preserved.
  purge       Uninstall, then remove config, logs, data dir, and the user.
  status      systemctl status servergitupdater + journalctl tail.

Paths can be overridden via env vars; see the top of $0.
EOF
}

build_binary() {
  if [ -f "$REPO_ROOT/go.mod" ] && command -v go >/dev/null 2>&1; then
    log "building from $REPO_ROOT (go $(go version | awk '{print $3}'))"
    ( cd "$REPO_ROOT" && go build -o "$REPO_ROOT/.sgu-build" . )
    echo "$REPO_ROOT/.sgu-build"
  elif [ -x "$REPO_ROOT/serverGitUpdater" ]; then
    echo "$REPO_ROOT/serverGitUpdater"
  else
    die "no Go toolchain and no pre-built binary at $REPO_ROOT/serverGitUpdater; build it first or install Go (>= 1.22)"
  fi
}

cmd_install() {
  need_root install

  local source_bin
  source_bin="$(build_binary)"

  if ! id "$USER_NAME" >/dev/null 2>&1; then
    log "creating system user $USER_NAME"
    useradd --system --home-dir "$DATA_DIR" --create-home --shell /usr/sbin/nologin "$USER_NAME"
  else
    log "user $USER_NAME already exists"
  fi

  log "creating directories"
  install -d -o "$USER_NAME" -g "$GROUP_NAME" -m 0750 "$CONFIG_DIR"
  install -d -o "$USER_NAME" -g "$GROUP_NAME" -m 0750 "$LOG_DIR"
  install -d -o "$USER_NAME" -g "$GROUP_NAME" -m 0755 "$DATA_DIR"
  install -d -o "$USER_NAME" -g "$GROUP_NAME" -m 0755 "$REPOS_DIR"

  log "installing binary to $INSTALL_BIN"
  install -o root -g root -m 0755 "$source_bin" "$INSTALL_BIN"
  rm -f "$REPO_ROOT/.sgu-build"

  if [ ! -f "$CONFIG_FILE" ]; then
    log "writing initial config to $CONFIG_FILE"
    cat > "$CONFIG_FILE" <<EOF
{
  "listen_addr": "$LISTEN_ADDR",
  "session_ttl_hours": 12,
  "cookie_secure": false,
  "repos_dir": "$REPOS_DIR",
  "log_path": "$LOG_DIR/updates.log",
  "max_log_rows": 500,
  "apps": []
}
EOF
    chown "$USER_NAME:$GROUP_NAME" "$CONFIG_FILE"
    chmod 0600 "$CONFIG_FILE"

    if [ -t 0 ]; then
      local admin
      printf 'Admin username: '
      read -r admin
      [ -n "$admin" ] || die "admin username is required"
      log "running --init-user $admin (will prompt for a password)"
      "$INSTALL_BIN" --init-user "$admin" --config "$CONFIG_FILE"
    else
      warn "no TTY attached — skipping --init-user."
      warn "before starting the service, run:"
      warn "  $INSTALL_BIN --init-user <name> --config $CONFIG_FILE"
    fi
    chown "$USER_NAME:$GROUP_NAME" "$CONFIG_FILE"
    chmod 0600 "$CONFIG_FILE"
  else
    log "keeping existing config at $CONFIG_FILE"
  fi

  log "writing $UNIT_FILE"
  cat > "$UNIT_FILE" <<EOF
[Unit]
Description=serverGitUpdater (multi-app git pull / build / service / Caddy controller)
Documentation=https://github.com/s95f/servergitupdater
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=$USER_NAME
Group=$GROUP_NAME
ExecStart=$INSTALL_BIN --config $CONFIG_FILE
Restart=on-failure
RestartSec=5s

# Hardening (sandbox the daemon as much as we can while still letting it
# write to its own dirs and shell out to git/build/systemctl/caddy).
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=full
ProtectHome=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectKernelLogs=true
ProtectControlGroups=true
RestrictSUIDSGID=true
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6
ReadWritePaths=$CONFIG_DIR $LOG_DIR $DATA_DIR $REPOS_DIR

[Install]
WantedBy=multi-user.target
EOF
  chmod 0644 "$UNIT_FILE"

  log "reloading systemd"
  systemctl daemon-reload

  log "enabling and (re)starting $UNIT_NAME.service"
  systemctl enable "$UNIT_NAME.service" >/dev/null
  systemctl restart "$UNIT_NAME.service"

  log "done."
  log "  service:    systemctl status $UNIT_NAME"
  log "  follow log: journalctl -u $UNIT_NAME -f"
  log "  config:     $CONFIG_FILE"
  log "  open:       http://${LISTEN_ADDR}/"
  if [ "$LISTEN_ADDR" = "127.0.0.1:8080" ]; then
    log "  (listen address is loopback-only by default; put Caddy / nginx in front, or override SGU_LISTEN)"
  fi
}

cmd_uninstall() {
  need_root uninstall
  if systemctl list-unit-files | grep -q "^${UNIT_NAME}\.service"; then
    log "stopping and disabling $UNIT_NAME"
    systemctl disable --now "$UNIT_NAME.service" 2>/dev/null || true
  fi
  if [ -f "$UNIT_FILE" ]; then
    log "removing $UNIT_FILE"
    rm -f "$UNIT_FILE"
    systemctl daemon-reload
  fi
  log "uninstalled. Config / logs / repos kept under:"
  log "  $CONFIG_DIR"
  log "  $LOG_DIR"
  log "  $DATA_DIR"
  log "  (run '$0 purge' to remove them too)"
}

cmd_purge() {
  need_root purge
  cmd_uninstall
  log "removing $INSTALL_BIN"
  rm -f "$INSTALL_BIN"
  log "removing $CONFIG_DIR $LOG_DIR $DATA_DIR"
  rm -rf "$CONFIG_DIR" "$LOG_DIR" "$DATA_DIR"
  if id "$USER_NAME" >/dev/null 2>&1; then
    log "deleting user $USER_NAME"
    userdel "$USER_NAME" 2>/dev/null || true
  fi
  log "purged."
}

cmd_status() {
  systemctl status "$UNIT_NAME.service" --no-pager || true
  echo
  journalctl -u "$UNIT_NAME.service" --no-pager -n 30 || true
}

case "${1:-install}" in
  install)         cmd_install ;;
  uninstall)       cmd_uninstall ;;
  purge)           cmd_purge ;;
  status)          cmd_status ;;
  -h|--help|help)  usage ;;
  *)               usage; exit 1 ;;
esac
