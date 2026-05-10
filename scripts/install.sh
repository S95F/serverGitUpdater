#!/usr/bin/env bash
# Install or upgrade serverGitUpdater as a systemd service.
#
# Usage:
#   sudo ./scripts/install.sh [install|update|uninstall|purge|status]
#
# `install` and `update` do the same thing — both rebuild the binary,
# re-install it, re-write the unit, and restart the service. Existing
# config, logs, and managed apps are preserved on every run.
# `update` also takes a single rolling backup of $CONFIG_FILE before
# touching anything (saved to $CONFIG_FILE.bak) and prints the version
# that was running before / after the swap.
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
Install or upgrade serverGitUpdater as a systemd service.

Usage: sudo $0 [install|update|uninstall|purge|status]
  install     Build, install the binary, create user/dirs, write the unit,
              run --init-user the first time, and start the service.
              Safe to re-run.
  update      Same as install but with upgrade-aware output: takes a
              rolling backup of $CONFIG_FILE.bak first and prints the
              before/after binary version. Use this for upgrades.
  uninstall   Stop & disable the service and remove the unit. Config,
              logs, repos, and the system user are preserved.
  purge       Uninstall, then remove config, logs, data dir, and the user.
  status      systemctl status servergitupdater + journalctl tail.

Existing config is always preserved. The binary's loader fills in
defaults for new fields and migrates legacy single-app configs into
apps[]; you don't need to edit config.json by hand between releases.

Paths can be overridden via env vars; see the top of $0.
EOF
}

bin_version() {
  local p="$1"
  if [ -x "$p" ]; then
    "$p" --version 2>/dev/null || echo "(unknown)"
  else
    echo "(not installed)"
  fi
}

# find_go locates a `go` executable even when sudo has stripped the
# caller's PATH (which is the default on most distros). Echoes the
# absolute path on success, returns non-zero otherwise.
find_go() {
  if command -v go >/dev/null 2>&1; then
    command -v go
    return 0
  fi
  # Common system install paths.
  local cand
  for cand in /usr/local/go/bin/go /usr/lib/go/bin/go /opt/go/bin/go /snap/bin/go; do
    if [ -x "$cand" ]; then
      echo "$cand"
      return 0
    fi
  done
  # Distro-versioned packages: /usr/lib/go-1.22/bin/go etc.
  for cand in /usr/lib/go-*/bin/go; do
    if [ -x "$cand" ]; then
      echo "$cand"
      return 0
    fi
  done
  # Calling user's home (when running under sudo).
  if [ -n "${SUDO_USER:-}" ]; then
    local user_home
    user_home="$(getent passwd "$SUDO_USER" 2>/dev/null | cut -d: -f6)"
    if [ -n "$user_home" ] && [ -x "$user_home/go/bin/go" ]; then
      echo "$user_home/go/bin/go"
      return 0
    fi
  fi
  return 1
}

build_binary() {
  local go_bin=""
  if [ -f "$REPO_ROOT/go.mod" ]; then
    go_bin="$(find_go || true)"
  fi
  if [ -n "$go_bin" ]; then
    log "building from $REPO_ROOT using $go_bin ($("$go_bin" version | awk '{print $3}'))"
    # `go build` embeds VCS info by shelling out to `git`. When this script
    # runs under sudo, git is invoked as root against a repo whose worktree
    # is typically owned by the calling user, which trips git's "dubious
    # ownership" safety check (exit 128). Pin safe.directory to this one
    # repo just for this command, and bail loudly if the build itself
    # fails so the next step doesn't try to install a missing binary.
    if ! ( cd "$REPO_ROOT" && \
           GIT_CONFIG_COUNT=1 \
           GIT_CONFIG_KEY_0=safe.directory \
           GIT_CONFIG_VALUE_0="$REPO_ROOT" \
           "$go_bin" build -o "$REPO_ROOT/.sgu-build" . ); then
      die "go build failed in $REPO_ROOT (see error above); refusing to install a stale binary"
    fi
    if [ ! -x "$REPO_ROOT/.sgu-build" ]; then
      die "go build did not produce $REPO_ROOT/.sgu-build (see error above)"
    fi
    rm -f "$REPO_ROOT/serverGitUpdater"
    echo "$REPO_ROOT/.sgu-build"
    return 0
  fi
  if [ -x "$REPO_ROOT/serverGitUpdater" ]; then
    warn "no Go toolchain found in PATH or common locations (/usr/local/go/bin, /usr/lib/go*, /opt/go, /snap/bin, \$SUDO_USER/go/bin)"
    warn "FALLING BACK to existing pre-built binary at $REPO_ROOT/serverGitUpdater"
    warn "this binary may be stale — to force a rebuild run:"
    warn "    cd $REPO_ROOT && go build -o serverGitUpdater . && sudo $0 update"
    warn "or invoke the installer with PATH preserved:"
    warn "    sudo env \"PATH=\$PATH\" $0 update"
    echo "$REPO_ROOT/serverGitUpdater"
    return 0
  fi
  die "no Go toolchain and no pre-built binary at $REPO_ROOT/serverGitUpdater; install Go >= 1.22 or pre-build the binary"
}

cmd_install() { run_install_or_update install; }
cmd_update()  { run_install_or_update update; }

run_install_or_update() {
  local mode="$1" # "install" or "update"
  need_root "$mode"

  local before_version=""
  if [ "$mode" = "update" ] && [ -x "$INSTALL_BIN" ]; then
    before_version="$(bin_version "$INSTALL_BIN")"
    log "currently installed: $before_version"
  fi

  if [ "$mode" = "update" ] && [ -f "$CONFIG_FILE" ]; then
    log "backing up config to ${CONFIG_FILE}.bak"
    install -o "$USER_NAME" -g "$GROUP_NAME" -m 0600 "$CONFIG_FILE" "${CONFIG_FILE}.bak" 2>/dev/null \
      || cp -p "$CONFIG_FILE" "${CONFIG_FILE}.bak"
  fi

  local source_bin
  source_bin="$(build_binary)"

  if ! id "$USER_NAME" >/dev/null 2>&1; then
    log "creating system user $USER_NAME"
    useradd --system --home-dir "$DATA_DIR" --create-home --shell /usr/sbin/nologin "$USER_NAME"
  else
    log "user $USER_NAME already exists"
  fi

  # Enable lingering so a per-user systemd manager exists for $USER_NAME
  # at boot. Without this, `systemctl --user` invocations from the
  # daemon (used when an app's service_scope is "user") fail with
  # "Failed to connect to bus: No medium found".
  if command -v loginctl >/dev/null 2>&1; then
    if loginctl show-user "$USER_NAME" 2>/dev/null | grep -q '^Linger=yes'; then
      log "lingering already enabled for $USER_NAME"
    else
      log "enabling lingering for $USER_NAME (lets systemctl --user work from the daemon)"
      loginctl enable-linger "$USER_NAME" || warn "loginctl enable-linger failed; user-scope service management may not work"
    fi
  fi
  USER_UID="$(id -u "$USER_NAME")"

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
After=network-online.target user@$USER_UID.service
Wants=network-online.target

[Service]
Type=simple
User=$USER_NAME
Group=$GROUP_NAME
# Daemons inherit a sparse PATH from systemd; add /usr/local/go/bin and
# a couple of common toolchain dirs so build commands like \`go\`,
# \`cargo\`, \`npm\` and \`make\` resolve from a bare name.
Environment=PATH=/usr/local/go/bin:/usr/local/cargo/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:/snap/bin
ExecStart=$INSTALL_BIN --config $CONFIG_FILE
# always: also revives clean self-shutdowns triggered from
# POST /api/server/restart in the UI; systemctl stop / disable still
# stop the unit normally.
Restart=always
RestartSec=2s

# Hardening (sandbox the daemon as much as we can while still letting it
# write to its own dirs and shell out to git/build/systemctl/caddy).
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=full
# tmpfs (not "true") so /run/user is empty in the namespace EXCEPT for
# the bind below — needed so \`systemctl --user\` from the daemon can
# reach its own per-user manager when an app is service_scope=user.
ProtectHome=tmpfs
BindPaths=-/run/user/$USER_UID
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

  # Wait for /run/user/$USER_UID to exist before we restart. enable-linger
  # kicks off user@$USER_UID.service asynchronously, and our unit's
  # BindPaths=-/run/user/$USER_UID silently skips the mount if the
  # source doesn't exist YET. That'd leave us stuck without the user
  # bus until the next manual restart. The After=user@$USER_UID.service
  # in the unit also helps, but only after the user manager unit is
  # known to systemd — this loop is the belt to that brace.
  for _ in 1 2 3 4 5 6 7 8 9 10; do
    [ -d "/run/user/$USER_UID" ] && break
    sleep 0.5
  done
  if [ ! -d "/run/user/$USER_UID" ]; then
    warn "/run/user/$USER_UID never showed up; user-scope service management may not work until you 'sudo systemctl start user@$USER_UID.service' and 'sudo systemctl restart $UNIT_NAME'"
  fi

  log "enabling and (re)starting $UNIT_NAME.service"
  systemctl enable "$UNIT_NAME.service" >/dev/null
  systemctl restart "$UNIT_NAME.service"

  if [ "$mode" = "update" ]; then
    local after_version
    after_version="$(bin_version "$INSTALL_BIN")"
    if [ -n "$before_version" ] && [ "$before_version" != "$after_version" ]; then
      log "upgraded: $before_version → $after_version"
    elif [ -n "$before_version" ]; then
      log "version unchanged: $after_version (binary re-installed and service restarted)"
    else
      log "installed: $after_version"
    fi
    log "config preserved; rolling backup at ${CONFIG_FILE}.bak"
  fi

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
  install)             cmd_install ;;
  update|upgrade)      cmd_update ;;
  uninstall)           cmd_uninstall ;;
  purge)               cmd_purge ;;
  status)              cmd_status ;;
  -h|--help|help)      usage ;;
  *)                   usage; exit 1 ;;
esac
