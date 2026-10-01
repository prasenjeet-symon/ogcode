---
title: "Remote deployment"
description: "Reach a remote server safely — SSH tunnel, reverse proxy with HTTPS, or Docker."
---

ogcode runs as a normal web server: one Go binary with the UI embedded, listening on a port (9595 by default). Running it on the machine where your code lives and reaching it from a browser elsewhere is a supported setup — but the agent can read and modify files and execute shell commands, so the way you expose it matters.

This guide covers the three most common ways to reach a remote ogcode safely:

1. [SSH tunnel](#1-ssh-tunnel) — nothing exposed; reach it as if it were local.
2. [Reverse proxy with HTTPS and authentication](#2-reverse-proxy-with-https-and-authentication) — a public URL, gated by a password.
3. [Docker](#3-docker) — run it in a container, compose with either of the above.

It is about the **standalone server only**. If you are hosting ogcode for several people, with per-user accounts, workspace allowlists, and isolated workers, that is the control plane — see [controlplane/docs/deploy.md](https://github.com/prasenjeet-symon/ogcode/blob/main/controlplane/docs/deploy.md). The two are different things: the control plane authenticates and scopes users itself; the standalone server does not.


## Before you start: what ogcode exposes

**It binds all interfaces by default.** `ogcode serve` listens on `0.0.0.0:<port>` (or `::`) — there is no `--host` or `--bind` flag. The only option is the port:

```bash
ogcode serve --port 9595      # or: ogcode -p 9595
```

If the port is busy the server tries the next one, up to 50 times, and prints `port in use, trying next`. On a remote host you want a **fixed, known port** so your tunnel or proxy points at the right place — pass `--port` explicitly and make sure it is free. The server also remembers the port it used per project in `~/.ogcode/ports.json`; an explicit `--port` overrides and is saved.

**There is no built-in authentication.** No login page, no basic auth, no bearer token — every route, including the file, shell, and settings APIs, is open to anyone who can reach the port. CORS is `Access-Control-Allow-Origin: *`. **Never expose the port directly to the public internet.** Authentication has to come from something in front of it: an SSH tunnel, or a reverse proxy with a password.

The startup banner prints `ogcode is running at http://localhost:<port>`; on a headless host the attempt to open a browser fails harmlessly.


## 1. SSH tunnel

The simplest and safest option: ogcode listens on the remote host's loopback only, and you forward the port over SSH. Nothing is exposed to the network.

On the remote host, start ogcode bound to localhost. The server binds all interfaces, so keep the firewall closed to the port (or run it behind a proxy), and forward from your machine:

```bash
# On the remote host
ogcode serve --port 9595

# On your local machine — forward local 9595 to the remote 9595
ssh -N -L 9595:localhost:9595 user@your-server
```

Then open <http://localhost:9595> in your local browser. All traffic is encrypted by SSH; there is no public port and no password to manage.

Keep the tunnel running while you work. Add `-N` so the SSH session only forwards and does not open a shell, and drop it into `~/.ssh/config` if you use it often:

```
Host ogcode-server
    HostName your-server
    User user
    LocalForward 9595 localhost:9595
```

The one limitation is reach: it works from the machine you run `ssh` on. For a browser on a different machine, use a reverse proxy.


## 2. Reverse proxy with HTTPS and authentication

Put nginx or Caddy in front: it terminates TLS and asks for a password before anything reaches ogcode. ogcode keeps listening on localhost.

### Start ogcode

```bash
ogcode serve --port 9595
```

Keep it reachable only from the proxy. Since the server binds all interfaces, use the host firewall / security group to allow only the proxy and SSH ports, or run it in Docker with a loopback-only publish (see [Docker](#3-docker)).

### nginx

Create a password file (install `apache2-utils` for `htpasswd`), then a site:

```bash
sudo htpasswd -c /etc/nginx/.htpasswd yourname
```

```nginx
server {
    listen 443 ssl;
    server_name ogcode.example.com;

    ssl_certificate     /etc/letsencrypt/live/ogcode.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/ogcode.example.com/privkey.pem;

    # Everything behind the proxy needs a password.
    auth_basic           "ogcode";
    auth_basic_user_file /etc/nginx/.htpasswd;

    location / {
        proxy_pass http://127.0.0.1:9595;
        proxy_http_version 1.1;
        proxy_set_header Host              $host;
        proxy_set_header X-Real-IP         $remote_addr;
        proxy_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header Upgrade           $http_upgrade;
        proxy_set_header Connection        "";

        # The UI streams live updates over Server-Sent Events at GET /api/event.
        # Buffering holds those messages until the buffer fills, so the UI appears
        # frozen. ogcode already sends X-Accel-Buffering: no; this is belt and braces.
        proxy_buffering off;
        proxy_read_timeout 3600s;
    }
}

server {
    listen 80;
    server_name ogcode.example.com;
    return 301 https://$host$request_uri;
}
```

### Caddy

Caddy obtains and renews the certificate automatically and does not buffer `text/event-stream` responses, so the config is much shorter:

```caddyfile
ogcode.example.com {
    basic_auth {
        yourname $2a$14$<bcrypt-hash>
    }
    reverse_proxy 127.0.0.1:9595 {
        flush_interval -1
    }
}
```

Generate the hash with `caddy hash-password`. `flush_interval -1` forces streaming for the SSE endpoint (Caddy would auto-detect it, but being explicit is harmless). `Host` and the forwarded headers are set for you.

### If it feels frozen

The symptom is a UI that loads but never updates — no new messages, no tool output. That is Server-Sent Events being buffered. ogcode serves progress on `GET /api/event` as `text/event-stream`; make sure the proxy does not buffer that response (`proxy_buffering off` in nginx, `flush_interval -1` in Caddy) and that the read timeout is generous (`proxy_read_timeout 3600s`). The client reconnects every 200 ms, so once buffering is off it recovers on its own.

### Stronger than a password

Basic auth over HTTPS is the minimum. Because the agent can execute commands, consider one more layer if the URL is broadly reachable:

- **Restrict by IP** — `allow`/`deny` in nginx, or a security-group rule.
- **An identity-aware proxy** — [oauth2-proxy](https://oauth2-proxy.github.io/oauth2-proxy/), [Cloudflare Access](https://developers.cloudflare.com/cloudflare-one/applications/), or Tailscale Serve — so access is tied to your SSO rather than a shared password.


## 3. Docker

The published image runs the server with the UI and git baked in. Reach it directly by publishing a port, or combine it with a tunnel or proxy.

```bash
docker run -d --name ogcode \
  -p 127.0.0.1:9595:9595 \
  -v ~/.ogcode:/root/.ogcode \
  -v /path/to/your/project:/workspace \
  -w /workspace \
  ghcr.io/prasenjeet-symon/ogcode:latest
```

- `-p 127.0.0.1:9595:9595` publishes on the host's **loopback only** — pair this with an SSH tunnel or a reverse proxy on the host. Use `-p 9595:9595` only if you intend the port to be reachable on the network directly (which, again, has no authentication).
- `-v ~/.ogcode:/root/.ogcode` persists the **global config DB** (`config.db`: providers, model preferences, theme, model catalogue), logs, and MCP tokens across container restarts.
- `-v /path/to/your/project:/workspace` mounts the code the agent will work on, and `-w /workspace` starts it there. git is installed in the image, so diffs and status work.
- The image is also mirrored to Docker Hub as `prasenjeetsimon/ogcode:latest` — either reference works.
- The container runs as root; `~/.ogcode` inside it is `/root/.ogcode`.

Health: the image ships a `HEALTHCHECK` that probes `GET /api/config` (JSON 200). A running container with `(healthy)` is serving.

### Compose

```yaml
services:
  ogcode:
    image: ghcr.io/prasenjeet-symon/ogcode:latest
    restart: unless-stopped
    ports:
      - "127.0.0.1:9595:9595"
    volumes:
      - ~/.ogcode:/root/.ogcode
      - /path/to/your/project:/workspace
    working_dir: /workspace
```

### Persistence matters

ogcode keeps **two** databases: the global config DB at `~/.ogcode/config.db`, and a **per-project** DB at `<project>/.ogcode/ogcode.db` (sessions, messages, memory, notes). Mount both consistently: if the workspace path changes between runs, the project DB moves with it and previously-created sessions and notes appear to vanish. Bind-mount the same host paths every time.


## Live service preview, remotely

When the agent starts a local server while working (a dev server, a dashboard), ogcode can hand you a URL for it. Locally those URLs look like `http://3000.preview.localhost:9595/`. That works because a browser resolves `*.localhost` to the machine the browser runs on — which is why it reaches the server only from its own machine or through an SSH tunnel.

To open previews from a browser on a **different** machine, give ogcode a real domain:

```bash
OGCODE_PREVIEW_DOMAIN=preview.example.com ogcode serve --port 9595
```

Use a **wildcard DNS record** `*.preview.example.com` pointing at the host, and a wildcard TLS certificate for `*.preview.example.com` if the proxy serves HTTPS. Your reverse proxy must forward the original `Host` header to ogcode so it can route `3000.preview.example.com` to the service on port 3000, and it must pass WebSocket upgrades for apps that use them.

Pick a **dedicated** domain for this — everything under it is treated as preview-only and never routes to the ogcode UI. Keeping it off the UI's own domain (not `ogcode.example.com`'s subtree) also keeps a previewed app from reading or setting cookies on the UI's origin. See the live-preview section of [OUTLINE.md](/docs/architecture/) for the routing details.


## Logs and diagnostics

By default the terminal shows only errors; full logs go to a rotated file:

- `<project>` server: `~/.ogcode/logs/<project>-<hash>/ogcode.log`
- worker: `~/.ogcode/logs/worker.log`

Directories are `0700`, files `0600`, and secrets are redacted. Useful environment variables:

| Variable | Default | Purpose |
|----------|---------|---------|
| `OGCODE_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `OGCODE_LOG_CONSOLE` | `error` | What also prints to the terminal |
| `OGCODE_LOG_FORMAT` | `text` | `text` or `json` |
| `OGCODE_LOG_DIR` | `~/.ogcode/logs` | Where logs are written |
| `OGCODE_LOG_MAX_SIZE_MB` | `10` | Rotation size |
| `OGCODE_LOG_MAX_FILES` | `5` | Files kept |
| `OGCODE_LOG_MAX_AGE_DAYS` | `14` | Age limit |
| `OGCODE_LOG_COMPRESS` | `on` | Compress rotated files |

To watch what the server is doing, run with `OGCODE_LOG_CONSOLE=info` (or set the UI log level in settings).


## Checklist

- [ ] ogcode is **not** reachable on its port from the public internet.
- [ ] Access is through an SSH tunnel **or** a reverse proxy with HTTPS **and** a password.
- [ ] The proxy does not buffer `GET /api/event` (SSE) — verified by the UI updating live.
- [ ] The global config volume (`~/.ogcode`) and the workspace are mounted at **stable paths**.
- [ ] For remotely-viewable service previews, `OGCODE_PREVIEW_DOMAIN` is set with matching wildcard DNS and TLS.
- [ ] For more than one user, you are using the [control plane](https://github.com/prasenjeet-symon/ogcode/blob/main/controlplane/docs/deploy.md) instead of the standalone server.


## See also

- [README — Remote deployment and security](https://github.com/prasenjeet-symon/ogcode/blob/main/README.md#remote-deployment-and-security) — the short version.
- [controlplane/docs/deploy.md](https://github.com/prasenjeet-symon/ogcode/blob/main/controlplane/docs/deploy.md) — the hosted, multi-user control plane.
- [OUTLINE.md](/docs/architecture/) — architecture, configuration, and every environment variable.
