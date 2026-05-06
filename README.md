# serverGitUpdater

A small Go service that runs on your VPS and pulls the latest code from a Git
remote into a working copy on disk — either on demand from a web UI, or on a
schedule. Useful for keeping a deployed app in sync with `main` without
hand-rolling another CI runner.

Single static binary, embedded frontend, JSON config, no database.

## Features

- Web UI to:
  - View current branch, HEAD commit, working-tree state, and how far behind
    the remote you are.
  - Trigger a manual update (`fetch` + `reset --hard origin/<branch>` + an
    optional build + post-update hook + service restart + Caddy reload).
  - Toggle scheduled auto-updates and change the interval.
  - Edit the repo path, branch, remote, and post-update command.
  - **Auto-detect the project's toolchain (Go, Cargo, Make, npm) and build
    the binary after every pull**, with an optional manual override.
  - **Pick the app's listen port from the UI** and have it plumbed into the
    build/run command automatically (`-port <N>` by default, configurable).
  - **Generate, install, restart and uninstall a systemd unit** for the
    deployed binary — system-wide or as a user unit.
  - **Generate a Caddy site snippet** that reverse-proxies a domain to the
    app's port, write it to `/etc/caddy/sites.d/<name>.caddy` (or any
    directory of your choosing), and reload Caddy. Reload happens
    automatically after every successful update if you opt in.
  - Change your admin password.
  - Browse the recent update history with stdout/stderr from each run.
- Authenticated, session-based login (bcrypt password, signed cookie).
- CSRF protection on every state-changing request.
- Login rate limiting per client IP.
- Strict security headers and a tight Content-Security-Policy.
- Optional built-in TLS, or run behind nginx/Caddy.
- Single binary — frontend assets are compiled in via `embed`.

## Requirements

- Go 1.22+ to build.
- `git` available on `PATH` on the host where the binary runs.
- A working copy of your target repo already cloned somewhere on disk
  (the service does not perform the initial clone).
- For private repos, a deploy key or credential helper that the user running
  the binary can read (see [Authenticating with the Git remote](#authenticating-with-the-git-remote)).

## Install

```sh
git clone https://github.com/s95f/servergitupdater.git
cd servergitupdater
go mod tidy
go build -o serverGitUpdater .
```

## First run

1. Create the initial admin user. This writes `config.json` in the current
   directory if it does not exist yet, then sets the username and password
   hash:

   ```sh
   ./serverGitUpdater --init-user admin
   # prompts for password (min 8 chars)
   ```

   Or non-interactively:

   ```sh
   ./serverGitUpdater --init-user admin --init-pass 'your-strong-password'
   ```

2. Edit `config.json` to point at your repository:

   ```json
   {
     "listen_addr": ":8080",
     "repo_path": "/srv/myapp",
     "branch": "main",
     "remote": "origin",
     "post_update_command": "/srv/myapp/deploy.sh",
     "auto_update_enabled": false,
     "auto_update_minutes": 15,
     "cookie_secure": true
   }
   ```

   See [`config.example.json`](./config.example.json) for the full schema.

3. Start the server:

   ```sh
   ./serverGitUpdater --config config.json
   ```

4. Open `http://<your-server>:8080/` and sign in. You can change everything
   else from the UI.

## Configuration

`config.json` is the source of truth. The app rewrites it (atomically) when
you save settings or change your password from the UI, so don't put comments
in it.

| Key | Default | Notes |
|-----|---------|-------|
| `listen_addr` | `:8080` | Address to bind. |
| `tls_cert_file`, `tls_key_file` | empty | If both set, the app serves HTTPS directly. |
| `session_secret` | auto-generated | HMAC key for session cookies. Rotating it logs everyone out. |
| `session_ttl_hours` | `12` | Session lifetime. |
| `cookie_secure` | `false` | Set to `true` when serving over HTTPS (directly or behind a TLS proxy). |
| `username` / `password_hash` | — | Set via `--init-user` or the in-app password change. |
| `repo_path` | — | Absolute path to the working copy on disk. |
| `branch` | `main` | Branch to track. |
| `remote` | `origin` | Remote name. |
| `post_update_command` | empty | Optional command to run after a successful update (e.g. `systemctl restart myapp`, `./deploy.sh`). |
| `post_update_args` | `[]` | Argv for the hook. |
| `git_env` | `[]` | Extra `KEY=VALUE` env vars for `git` and the hook (e.g. `GIT_SSH_COMMAND=...`). |
| `auto_update_enabled` | `false` | Master switch for the scheduler. |
| `auto_update_minutes` | `15` | Minimum 1. Changes take effect on the next tick. |
| `auto_build_enabled` | `false` | Run a build step after every successful pull. |
| `build_command` | empty | Override the auto-detected build tool (e.g. `go`, `cargo`). |
| `build_args` | `[]` | Argv for `build_command`; supports `{port}` / `{repo_path}` / `{name}`. |
| `app_port` | `0` | Port the deployed app should listen on. `0` disables port plumbing. |
| `port_flag` | `-port` | Flag used when auto-appending the port to run args. |
| `service_manage_enabled` | `false` | Restart `service_name` after every successful update. |
| `service_name` | empty | systemd unit name (without `.service`). |
| `service_scope` | `system` | `system` or `user`. |
| `service_description` | empty | `Description=` line. |
| `service_exec_start` | empty | Path to the binary; falls back to detected build output. |
| `service_exec_args` | `[]` | Argv appended to `ExecStart=`. |
| `service_working_dir` | empty | `WorkingDirectory=`; defaults to `repo_path`. |
| `service_user` | empty | `User=` (system scope only). |
| `service_env` | `[]` | One `Environment=KEY=VALUE` per entry. |
| `service_restart` | `on-failure` | `on-failure`, `always`, or `no`. |
| `caddy_enabled` | `false` | Manage a Caddy site for this app. |
| `caddy_auto_apply` | `false` | Re-apply the snippet and reload after every successful update. |
| `caddy_domain` | empty | Hostname for the site block. |
| `caddy_upstream` | `127.0.0.1:{port}` | Upstream `reverse_proxy` target. |
| `caddy_extra` | empty | Extra Caddyfile directives inside the site block. |
| `caddy_snippet_dir` | `/etc/caddy/sites.d` | Directory the snippet is written to. |
| `caddy_snippet_name` | empty | Snippet filename (without extension); defaults to `service_name`. |
| `caddy_reload_command` | `["systemctl","reload","caddy"]` | Command run to reload Caddy. |
| `log_path` | `updates.log` | JSONL file with one entry per run. |
| `max_log_rows` | `500` | How many rows the UI loads. |

## What an update actually does

When you press **Update now** or the scheduler fires, the service runs the
following inside `repo_path`, in order, stopping at the first failure:

1. `git fetch --prune <remote>`
2. `git checkout <branch>`
3. `git reset --hard <remote>/<branch>`
4. **Build step** — if `auto_build_enabled` is on (or `build_command` is set
   explicitly). See [Auto-build](#auto-build) below.
5. `post_update_command` (if any).
6. **Service restart** — if `service_manage_enabled` is on, runs
   `systemctl restart <service_name>` (or `systemctl --user restart …` for
   user-scope units).
7. **Caddy apply** — if `caddy_enabled` and `caddy_auto_apply` are on,
   re-renders the site snippet and reloads Caddy (skips reload if the
   snippet on disk is already up to date).

This is **destructive to local changes** in the working copy by design — the
goal is to make the deployed tree exactly match the remote branch. Don't
point this at a working copy you also edit by hand.

Each run is appended to `log_path` as a single JSON line and shown in the
**Update history** panel.

## Auto-build

When `auto_build_enabled` is on, after every successful pull the updater
inspects `repo_path` and picks the first matching toolchain that's also
available on `PATH`:

| Marker file in repo | Tool   | Default command                           |
|---------------------|--------|-------------------------------------------|
| `go.mod`            | `go`   | `go build -o <repo>/<name> ./...`         |
| `Cargo.toml`        | `cargo`| `cargo build --release`                   |
| `Makefile`          | `make` | `make`                                    |
| `package.json`      | `npm`  | `npm run build`                           |

The detection result (kind, tool path, version, suggested command) is shown
live in the UI under **Build → Detection**. You can override the command
entirely by filling in **Build command** and **Build args** — when
`build_command` is non-empty it always wins over auto-detection.

The build runs with `cwd = repo_path`, inherits the process environment, and
gets any extra `KEY=VALUE` pairs from `git_env` appended.

There's a **Build now** button to run the build step on its own without
pulling.

## Port plumbing

The dashboard has an **App port** field and a **Port flag** field. They drive
where and how the port is passed to your app:

- Anywhere `{port}` appears in `build_args`, `service_exec_args`, or
  `caddy_upstream`, it's substituted with the configured port. The same
  goes for `{repo_path}` and `{name}` (the service name).
- If `app_port` is set and **none** of the run args (`service_exec_args`)
  reference `{port}`, the updater appends `<port_flag> <app_port>` to the
  rendered ExecStart line. Default flag is `-port`, but you can change it
  to `--port`, `--listen`, etc.

So a Go app whose flag is the conventional `-port` needs zero extra config
once you fill in the port; an app that wants `--listen=:8081` can put
`--listen=:{port}` in its run args and skip the auto-append.

## Managed systemd service

When `service_manage_enabled` is on with a `service_name` set, the updater
runs `systemctl restart <service>` after a successful build. The
**Install &amp; start** button writes a unit file with the values from the
form, runs `daemon-reload`, `enable`, and `restart`. **Uninstall** does the
reverse and removes the file.

Two scopes are supported:

- **`system`** — writes `/etc/systemd/system/<name>.service` and runs
  `systemctl …`. Requires the updater process to have permission to write
  there and to talk to the system bus (running as root, or with a sudoers
  rule, or as a user with the right polkit policy).
- **`user`** — writes `~/.config/systemd/user/<name>.service` and runs
  `systemctl --user …`. Runs entirely as the updater's own user, which is
  the recommended setup for a single-user VPS. You may need
  `loginctl enable-linger <user>` so the user manager runs without an
  interactive session.

The generated unit looks roughly like:

```ini
[Unit]
Description=<service_description>
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=<service_user>                 ; system scope only
WorkingDirectory=<service_working_dir or repo_path>
Environment=KEY=VALUE               ; one line per service_env entry
ExecStart=<service_exec_start> <service_exec_args...>
Restart=<service_restart>           ; on-failure | always | no
RestartSec=5s
NoNewPrivileges=true

[Install]
WantedBy=multi-user.target          ; default.target for user scope
```

If `service_exec_start` is empty, the updater falls back to the build
output suggested by detection (e.g. `<repo>/<dirname>` for Go,
`<repo>/target/release/<dirname>` for Cargo). Use **Preview unit** in the UI
to see exactly what will be written before clicking **Install &amp; start**.

> Heads up: any account that can log into the web UI can install and run a
> systemd service as the updater's user (and as `root` for system-scope
> units if the updater has that privilege). Treat the admin login
> accordingly and put the UI behind HTTPS.

## Caddy reverse proxy

When `caddy_enabled` is on with a `caddy_domain`, the **Caddy** panel writes
a Caddyfile snippet that proxies the domain to the app's port:

```caddy
myapp.example.com {
    reverse_proxy 127.0.0.1:8081
}
```

The default snippet path is `/etc/caddy/sites.d/<service_name>.caddy`. To
make Caddy pick it up, add this once in your main `/etc/caddy/Caddyfile`:

```caddy
import sites.d/*.caddy
```

Then either click **Apply &amp; reload** in the UI or turn on
`caddy_auto_apply` so every successful update re-renders and reloads.

The default reload command is `systemctl reload caddy`. You can change it
to e.g. `caddy reload --config /etc/caddy/Caddyfile` if you don't run Caddy
under systemd.

You can drop arbitrary directives inside the site block via `caddy_extra`
(supports `{port}`, `{name}`, `{repo_path}`):

```
encode zstd gzip
header /api/* Cache-Control no-store
```

Permissions are the same story as the systemd unit: writing to
`/etc/caddy/sites.d` and running `systemctl reload caddy` need the
appropriate privileges. For a single-user setup you can point
`caddy_snippet_dir` at a path your deploy user owns and adjust your
Caddyfile to import from there.

## Running as a service

A minimal systemd unit (adjust paths and `User=`):

```ini
# /etc/systemd/system/servergitupdater.service
[Unit]
Description=serverGitUpdater
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=deploy
Group=deploy
WorkingDirectory=/srv/servergitupdater
ExecStart=/srv/servergitupdater/serverGitUpdater --config /srv/servergitupdater/config.json
Restart=on-failure
RestartSec=5s
NoNewPrivileges=true
ProtectSystem=full
ProtectHome=true
PrivateTmp=true

[Install]
WantedBy=multi-user.target
```

Then:

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now servergitupdater
```

## Putting it behind nginx / Caddy

The service speaks plain HTTP by default. If you terminate TLS at a reverse
proxy, set `"cookie_secure": true` so the browser only sends the session
cookie over HTTPS, and forward the real client IP for accurate rate limiting:

**Caddy:**

```caddyfile
updater.example.com {
    reverse_proxy 127.0.0.1:8080 {
        header_up X-Real-IP {remote_host}
    }
}
```

**nginx:**

```nginx
location / {
    proxy_pass http://127.0.0.1:8080;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
}
```

## Authenticating with the Git remote

The service runs `git` as whatever user owns the process. For private repos
you have a few options:

- **Deploy key over SSH.** Add the public key as a read-only deploy key on
  the repo, and point `git_env` at it:

  ```json
  "git_env": [
    "GIT_SSH_COMMAND=ssh -i /home/deploy/.ssh/id_ed25519 -o StrictHostKeyChecking=yes"
  ]
  ```

- **HTTPS with a credential helper.** Configure `git config --global
  credential.helper store` for the service user and prime it once.

- **HTTPS with a token in the remote URL.** Set the remote URL on disk to
  `https://x-access-token:<TOKEN>@github.com/owner/repo.git`. Keep in mind
  the token is then visible in `.git/config` on the host.

The app never sees or stores Git credentials.

## Security notes

- Run the binary as a **dedicated, unprivileged user** that owns
  `repo_path`. Don't run it as root.
- Always serve it over HTTPS in production (either built-in TLS or a TLS
  proxy) and set `cookie_secure: true`.
- `config.json` contains your bcrypt hash and session secret; it is written
  with mode `0600`. Make sure the parent directory is not world-readable.
- Login attempts are rate-limited (8 failures per 15 minutes per IP). If
  you're behind a proxy, make sure `X-Real-IP` or `X-Forwarded-For` is set
  so the limiter sees real client IPs.
- The post-update command runs as the same user as the service. Treat the
  admin login as equivalent to shell access.

## CLI flags

```
--config string     path to config file (default "config.json")
--addr string       override listen address (e.g. ":8080")
--init-user string  create or reset the admin user with this username and exit
--init-pass string  password for --init-user (read from stdin if empty)
```

## Project layout

```
.
├── main.go                  entrypoint, flag parsing, lifecycle
├── internal/
│   ├── auth/                bcrypt, signed-cookie sessions, CSRF, rate limit
│   ├── build/               toolchain detection (go/cargo/make/npm) + runner
│   ├── caddy/               Caddyfile snippet renderer + reload wrapper
│   ├── config/              JSON config load/save with atomic replace
│   ├── git/                 update runner + scheduler + JSONL log
│   ├── server/              HTTP routes, middleware, handlers
│   ├── service/             systemd unit generator + systemctl wrapper
│   └── tmpl/                {port}/{repo_path}/{name} substitution helper
└── web/
    ├── embed.go             go:embed of static/
    └── static/              login + dashboard (vanilla HTML/CSS/JS)
```

## License

MIT.
