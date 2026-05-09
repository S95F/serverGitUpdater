# serverGitUpdater

A small Go service that runs on your VPS and pulls the latest code from a Git
remote into a working copy on disk — either on demand from a web UI, or on a
schedule. Useful for keeping a deployed app in sync with `main` without
hand-rolling another CI runner.

Single static binary, embedded frontend, JSON config, no database.

## Features

- **Multi-app dashboard.** Manage as many independent deployments as you
  want from one binary. The landing page lists every app with its repo,
  branch, port, service and Caddy state, and one-click access to Update,
  Edit and Delete.
- Per-app web UI to:
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
- Dedicated **Settings** page (linked from the header) for changing the
  admin password.
- Dedicated **Administration** page with overall and per-app analytics
  (total runs, success rate, 24h activity, average build duration) plus a
  filterable update history.
- Authenticated, session-based login (bcrypt password, signed cookie).
- CSRF protection on every state-changing request.
- Login rate limiting per client IP.
- Strict security headers and a tight Content-Security-Policy.
- Optional built-in TLS, or run behind nginx/Caddy.
- Single binary — frontend assets are compiled in via `embed`.

## Requirements

- Go 1.22+ to build.
- `git` available on `PATH` on the host where the binary runs.
- A working copy of your target repo on disk. The service can clone it
  for you when you fill in **Git clone URL** in the **+ New app** dialog
  (or the per-app edit page); otherwise it expects an existing clone at
  `repo_path`.
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

2. Start the server. With no apps configured yet you can just run it:

   ```sh
   ./serverGitUpdater --config config.json
   ```

3. Open `http://<your-server>:8080/` and sign in. The landing page is your
   list of apps; click **+ New app** to add one, then click into it to
   configure repo path, build, port, service and Caddy. Everything else can
   be edited from the UI; the on-disk `config.json` is rewritten atomically
   on every save.

## UI map

| URL | What it does |
|-----|--------------|
| `/` | Landing — list of apps with quick state and Update / Edit / Delete actions, plus a **+ New app** button. |
| `/apps/{id}` | Per-app edit. Left sidebar shows live repo status; the 2/3-width main panel holds Repo, Build, Port, systemd service and Caddy fields. |
| `/settings` | Server-level settings. A dropdown picks between **Reset password** and **Server configuration** (listen address, repos directory, log path, TLS files, cookie security, etc.). |
| `/admin` | Administration. Aggregate counters, per-app stats and a filterable update history. |
| `/login` | Sign-in form. |

The header on every authenticated page links to **Apps**, **Administration**
and **Settings**, plus a **Sign out** button.

## Configuration

`config.json` is the source of truth. The app rewrites it (atomically) when
you save settings, change your password, or create / edit / delete an app
from the UI, so don't put comments in it.

It has three layers:

1. **Server-wide** keys at the top level (auth, listen address, log path).
2. An `apps` array of independent deployments.
3. A legacy single-app config (the top-level keys this README used to list)
   is auto-migrated into `apps[0]` on first load.

### Server-wide keys

| Key | Default | Notes |
|-----|---------|-------|
| `listen_addr` | `:8080` | Address to bind. |
| `tls_cert_file`, `tls_key_file` | empty | If both set, the app serves HTTPS directly. |
| `session_secret` | auto-generated | HMAC key for session cookies. Rotating it logs everyone out. |
| `session_ttl_hours` | `12` | Session lifetime. |
| `cookie_secure` | `false` | Set to `true` when serving over HTTPS (directly or behind a TLS proxy). |
| `username` / `password_hash` | — | Set via `--init-user` or the Settings page. |
| `repos_dir` | empty | If set, acts as a hard namespace: every per-app `repo_path` is interpreted as relative to it (leading slashes and `..` segments are stripped so you can't escape the namespace). When empty, an absolute `repo_path` is used as-is and a relative one is resolved against the binary's working directory. |
| `public_base_url` | empty | Externally-reachable base URL of this binary, e.g. `https://updater.example.com`. Used to build the webhook URLs registered with GitHub. |
| `github_token` | empty | Personal Access Token with `admin:repo_hook` scope (or fine-grained PAT with *Webhooks: Read and write*). Used to create / update / delete repo webhooks. **SSH keys don't grant API access**, so you have to issue a token even if your git remotes are over SSH. |
| `log_path` | `updates.log` | JSONL file; one entry per run, tagged with `app_id`. |
| `max_log_rows` | `500` | Rows fetched into the UI by default. |

### Per-app keys (each entry in `apps[]`)

| Key | Default | Notes |
|-----|---------|-------|
| `id` | auto | Generated by the server; do not edit. |
| `name` | — | Human-friendly identifier shown in the UI. Must be unique. |
| `repo_path` | — | Path to the working copy on disk. When `repos_dir` is set, `repo_path` is always interpreted as a relative slot under it; leading slashes are ignored, `..` segments are stripped. When `repos_dir` is empty, an absolute path is used as-is. |
| `clone_url` | empty | Optional Git URL. If set, **+ New app** clones the repo into `repo_path` automatically; the per-app edit page also has a **Clone repo** button to do this on demand later. |
| `branch` | `main` | Branch to track. Also passed to `git clone --branch`. |
| `remote` | `origin` | Remote name. |
| `post_update_command` | empty | Optional command to run after a successful update. |
| `post_update_args` | `[]` | Argv for the hook. |
| `git_env` | `[]` | Extra `KEY=VALUE` env vars for `git` and the hook. |
| `auto_update_enabled` | `false` | Per-app master switch for the scheduler. |
| `auto_update_minutes` | `15` | Per-app interval (minutes). |
| `auto_build_enabled` | `false` | Run a build step after every successful pull. |
| `build_command` | empty | Override the auto-detected build tool. |
| `build_args` | `[]` | Argv for `build_command`; supports `{port}` / `{repo_path}` / `{name}`. |
| `app_port` | `0` | Port the deployed app listens on. `0` disables port plumbing. |
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
| `caddy_mode` | `proxy` | `proxy` to reverse-proxy to `caddy_upstream`, or `file_server` to host the directory directly. |
| `caddy_domain` | empty | Hostname for the site block. |
| `caddy_upstream` | `127.0.0.1:{port}` | Proxy mode: upstream `reverse_proxy` target. |
| `caddy_root` | empty | File-server mode: directory to serve. Defaults to the resolved repo path. |
| `caddy_browse` | `false` | File-server mode: enable directory listings (`file_server browse`). |
| `caddy_try_files` | empty | File-server mode: optional `try_files` directive (e.g. `{path} /index.html` for SPAs). |
| `caddy_extra` | empty | Extra Caddyfile directives inside the site block. |
| `caddy_snippet_dir` | `/etc/caddy/sites.d` | Directory the snippet is written to. |
| `caddy_snippet_name` | empty | Snippet filename (without extension); defaults to `service_name`. |
| `caddy_reload_command` | `["systemctl","reload","caddy"]` | Command run to reload Caddy. |
| `caddy_files_user` | empty | File-server mode: user to chown served files to (optional). Requires root or `CAP_CHOWN`. |
| `caddy_files_group` | empty | File-server mode: group to chown served files to (optional). |
| `caddy_files_dir_mode` | `0755` | File-server mode: directory mode (octal) applied by **Fix permissions**. |
| `caddy_files_file_mode` | `0644` | File-server mode: file mode (octal) applied by **Fix permissions**. |
| `import_from_repo_file` | `false` | After each successful pull, read the in-repo config file and override build/service/caddy settings for that pipeline run. |
| `repo_file_path` | `.servergitupdater.json` | Path of the in-repo config file, relative to the working copy. |
| `webhook_enabled` | `false` | Accept inbound POSTs at `/api/apps/{id}/webhook` to trigger updates. |
| `webhook_secret` | empty | Shared secret for HMAC-SHA256 signature verification. Generate from the UI or set manually. |
| `webhook_branch_only` | `false` | Ignore webhook deliveries whose `ref` doesn't match `branch`. |
| `webhook_auto_register` | `false` | When on, the server creates / updates / deletes the webhook on GitHub via the REST API every time the app config is saved or the binary starts. Requires `github_token` and `public_base_url` server-side. |
| `webhook_remote_id` | `0` | GitHub-assigned hook ID once registered. Server-managed; don't edit. |

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

## In-repo config file

A project can ship its own deploy settings alongside its source by
checking in a `.servergitupdater.json` at the repo root (the path is
configurable per-app as `repo_file_path`). Every key is optional;
keys that are present override the matching app field for the pipeline
run, keys that are absent leave the app's existing value alone.

```json
{
  "auto_build_enabled": true,
  "build_command": "go",
  "build_args": ["build", "-o", "{repo_path}/{name}", "./..."],

  "app_port": 8081,
  "port_flag": "-port",

  "service_manage_enabled": true,
  "service_name": "myapp",
  "service_scope": "user",
  "service_exec_args": ["--config", "{repo_path}/config.toml"],
  "service_restart": "on-failure",

  "caddy_enabled": true,
  "caddy_auto_apply": true,
  "caddy_mode": "proxy",
  "caddy_domain": "myapp.example.com",
  "caddy_upstream": "127.0.0.1:{port}"
}
```

Two ways to apply it:

- **One-shot import**: on the per-app edit page click **Import &amp; save**
  in the *Auto-import config from repo* fieldset. The values from the
  file are merged into the persisted app and saved.
- **Auto-import on every update**: toggle *After each successful pull,
  read this file and override…* on. The updater re-reads the file at
  the start of each pipeline (after the pull, before the build) and
  applies the values for that run only — the saved app config is left
  alone, so the file remains the single source of truth.

The full set of accepted keys is the build / port / service / caddy
fields documented in [the per-app table](#per-app-keys-each-entry-in-apps).
Repository identity (`repo_path`, `clone_url`, `branch`, `remote`) and
scheduling/webhook keys are server-side only and not honored from the
file.

## Inbound webhooks (auto-update on push)

> GitHub doesn't push via WebSockets — it posts via HTTP webhooks.
> serverGitUpdater receives those at a per-app endpoint and runs the
> usual update pipeline.

For each app you can enable a public endpoint at:

```
POST https://<your-server>/api/apps/{id}/webhook
```

Toggle **Enable inbound webhook for this app** in the **Webhook** tab,
generate a secret, then copy the URL + secret into the GitHub repo's
**Settings → Webhooks → Add webhook**:

- Payload URL: the URL above
- Content type: `application/json`
- Secret: the generated secret
- Events: *Just the push event* (or whatever subset you want)
- Active: yes

Each delivery is verified with HMAC-SHA256 against the
`X-Hub-Signature-256` header. Mismatches return 401 and are logged.
Successful deliveries enqueue a `RunUpdate` (recorded as `source:
"webhook"` in the history) and respond `{"ok": true, "queued": true}`.
GitHub's `ping` event is acknowledged with `{"ok": true, "pong": true}`.

If `webhook_branch_only` is on, deliveries whose `ref` field doesn't
match `refs/heads/<branch>` are acknowledged but skipped — useful when
you only deploy `main` from a multi-branch repo.

The endpoint **does not** require login or CSRF; it's secured by HMAC
alone. Keep the secret out of version control. Behind a reverse proxy,
make sure the proxy preserves the request body unchanged so the HMAC
verifies.

### Auto-registering on GitHub

You can let the binary register the webhook on GitHub for you, end to
end:

1. In **Settings → Server configuration**, fill in:
   - **Public base URL**: e.g. `https://updater.example.com`. The binary
     uses this to build the webhook URL it sends to GitHub. The token
     and public URL are server-wide; one of each is enough for all apps.
   - **GitHub token**: a Personal Access Token with `admin:repo_hook`
     scope, or a fine-grained PAT scoped to your repo(s) with *Webhooks:
     Read and write*. SSH keys are **not** sufficient — they're a
     separate auth mechanism, only valid for git operations.
2. On the per-app **Webhook** tab, set a `clone_url` that points at
   GitHub (HTTPS or SSH form), tick **Enable inbound webhook** and
   **Auto-register on GitHub**, click **Generate new secret**, and
   **Save settings**.

The binary then:

- On save, creates the hook on GitHub (`POST /repos/{owner}/{repo}/hooks`)
  and stores the returned hook ID in `webhook_remote_id`.
- On a later save it `PATCH`es the existing hook to keep URL / events /
  secret / `active` in sync.
- Toggling `webhook_enabled` off (with auto-register still on) deletes
  the hook on GitHub.
- On every server start, a reconcile pass runs the same logic for every
  app with `webhook_auto_register`, so a freshly-restored config gets
  re-registered automatically.

A **Sync now** button on the Webhook tab triggers the same logic on
demand and surfaces the GitHub API response.

## Caddy

The **Caddy** tab on the per-app edit page writes a Caddyfile snippet for
the app and reloads Caddy. It supports two modes via `caddy_mode`:

### Proxy mode (default)

`caddy_mode: "proxy"` reverse-proxies the domain to the app's port:

```caddy
myapp.example.com {
    reverse_proxy 127.0.0.1:8081
}
```

### File-server mode

`caddy_mode: "file_server"` hosts a directory directly. The document root
defaults to the resolved repo path; set `caddy_root` (placeholders
allowed: `{repo_path}`, `{port}`, `{name}`) to serve a subdirectory like
`{repo_path}/dist`. Optional `caddy_browse` enables directory listings,
and `caddy_try_files` adds an SPA-style fallback:

```caddy
myapp.example.com {
    root * /srv/apps/myapp/dist
    try_files {path} /index.html
    file_server
}
```

### Permissions

When using file-server mode the directory has to be readable by the user
Caddy runs as. The Caddy tab includes a **Fix permissions** action that
walks the served directory (skipping `.git`) and applies:

- `caddy_files_dir_mode` (default `0755`) to directories,
- `caddy_files_file_mode` (default `0644`) to files,
- and, if `caddy_files_user` / `caddy_files_group` are set, `chown` to
  that user/group.

`chmod` works on anything the running user owns; `chown` requires the
binary to run as root or hold `CAP_CHOWN`.

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

The repo ships an installer script that does the whole setup for you on
a Linux host with systemd:

```sh
sudo ./scripts/install.sh           # install + start
sudo ./scripts/install.sh status    # systemctl status + journal tail
sudo ./scripts/install.sh uninstall  # stop & remove the unit (keeps config/data)
sudo ./scripts/install.sh purge     # uninstall + remove config, logs, data, user
```

It builds the binary, creates a system user (`sgu` by default), installs
to `/usr/local/bin/serverGitUpdater`, writes a hardened unit at
`/etc/systemd/system/servergitupdater.service` pointing at
`/etc/servergitupdater/config.json`, runs `--init-user` once for the
admin password, then `daemon-reload` + `enable --now`. Re-running
`install` rebuilds and restarts cleanly.

Paths and the listen address are overridable via env vars (`SGU_USER`,
`SGU_BIN`, `SGU_CONFIG_DIR`, `SGU_LOG_DIR`, `SGU_DATA_DIR`,
`SGU_REPOS_DIR`, `SGU_LISTEN`); see the comment at the top of the
script for the full list.

If you'd rather hand-roll the unit, here's the minimum (adjust paths
and `User=`):

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
│   ├── git/                 update runner + scheduler + JSONL log + clone
│   ├── githubhook/          minimal GitHub repo-webhooks REST client
│   ├── repofile/            .servergitupdater.json reader / Apply()
│   ├── server/              HTTP routes, middleware, handlers
│   ├── service/             systemd unit generator + systemctl wrapper
│   └── tmpl/                {port}/{repo_path}/{name} substitution helper
└── web/
    ├── embed.go             go:embed of static/
    └── static/              login + landing + per-app edit + settings + admin
                             (vanilla HTML/CSS/ES modules; no framework)
```

## License

MIT.
