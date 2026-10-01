# Deploying the control plane on the internet

This walks through running the master on a Linux server with a real domain and a
wildcard TLS certificate, then connecting a worker from another machine. Example
domain: `panel.example.com`; example DNS provider: Cloudflare (swap for yours).

The master is a **single standalone binary built from this repo** — it does not
depend on ogcode.

## Part 1 — the master

### 0. Prerequisites

- A Linux VPS with a public IP and sudo.
- A domain you control + a DNS-provider **API token** (needed for the wildcard cert).
- A base host, e.g. `panel.example.com`.

### 1. DNS: apex + wildcard → your server

```
panel.example.com.     A   YOUR_SERVER_IP
*.panel.example.com.   A   YOUR_SERVER_IP
```

Verify: `dig +short panel.example.com anything.panel.example.com` → both return the IP.

### 2. Firewall

Open TCP **443** (and the cloud security group). Port 80 is not needed — the
wildcard cert uses the DNS-01 challenge.

### 3. Build the binary and copy it over

From this repo, cross-compile a static Linux binary (use `arm64` if the VPS is ARM):

```sh
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o ogcode-control-plane ./cmd/ogcode-control-plane
scp ogcode-control-plane YOUR_USER@panel.example.com:/tmp/
```

On the server:

```sh
sudo install -o root -g root -m 755 /tmp/ogcode-control-plane /usr/local/bin/ogcode-control-plane
sudo useradd --system --no-create-home --shell /usr/sbin/nologin ogcode
sudo mkdir -p /etc/ogcode-control-plane/tls
```

### 4. Wildcard certificate (DNS-01)

```sh
sudo apt-get update && sudo apt-get install -y certbot python3-certbot-dns-cloudflare
```

Store the token in `/root/.secrets/cloudflare.ini` (`chmod 600`):

```ini
dns_cloudflare_api_token = YOUR_TOKEN
```

Issue a cert covering **both** the apex and the wildcard:

```sh
sudo certbot certonly --dns-cloudflare \
  --dns-cloudflare-credentials /root/.secrets/cloudflare.ini \
  --preferred-challenges dns-01 \
  -d panel.example.com -d '*.panel.example.com' \
  --agree-tos -m you@example.com --non-interactive
```

### 5. Make the cert readable by the service + auto-apply renewals

certbot's files are root-only, so a deploy hook copies them where `ogcode` can
read them and reloads the master (hot-reload = zero downtime). Create
`/etc/letsencrypt/renewal-hooks/deploy/ogcode-cp.sh`:

```sh
#!/bin/sh
set -e
D=/etc/letsencrypt/live/panel.example.com
install -o ogcode -g ogcode -m 644 "$D/fullchain.pem" /etc/ogcode-control-plane/tls/cert.pem
install -o ogcode -g ogcode -m 600 "$D/privkey.pem"  /etc/ogcode-control-plane/tls/key.pem
systemctl reload ogcode-control-plane 2>/dev/null || true
```

`chmod +x` it and run it once now for the initial copy.

### 6. Config

Generate strong secrets: `openssl rand -base64 32` (pairing) and
`openssl rand -base64 24` (operator). Write
`/etc/ogcode-control-plane/control-plane.json` (owned by `ogcode`, `chmod 600`):

```json
{
  "master": {
    "listen": ":443",
    "pairingSecret": "PAIRING_SECRET",
    "operatorPassword": "OPERATOR_PASSWORD",
    "cookieDomain": ".panel.example.com",
    "tls": {
      "cert": "/etc/ogcode-control-plane/tls/cert.pem",
      "key": "/etc/ogcode-control-plane/tls/key.pem"
    }
  }
}
```

The master also accepts `dbPath` (its bbolt registry + accounts; default
`~/.ogcode-control-plane/controlplane.db`), `sessionTtlSeconds`,
`tokenTtlSeconds` and `workerTimeoutSeconds`. See `control-plane.example.json`
for the full set.

### 7. systemd unit

`/etc/systemd/system/ogcode-control-plane.service`:

```ini
[Unit]
Description=ogcode control plane
After=network-online.target
Wants=network-online.target

[Service]
User=ogcode
Group=ogcode
ExecStart=/usr/local/bin/ogcode-control-plane serve --config /etc/ogcode-control-plane/control-plane.json
ExecReload=/bin/kill -HUP $MAINPID
Restart=always
RestartSec=3
AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true

[Install]
WantedBy=multi-user.target
```

`AmbientCapabilities=CAP_NET_BIND_SERVICE` lets the non-root service bind 443;
`ExecReload` maps `systemctl reload` to the `SIGHUP` the cert hot-reload watches.

### 8. Start + verify

```sh
sudo systemctl daemon-reload && sudo systemctl enable --now ogcode-control-plane
curl https://panel.example.com/healthz          # → ok
sudo journalctl -u ogcode-control-plane -f      # → listening addr=:443 tls=true, no UNAUTHENTICATED warning
```

## Part 2 — connecting a worker (another machine)

The worker is outbound-only: it needs only outbound 443 to the master (NAT/CGNAT/
corporate firewalls are fine), and no inbound ports.

### 1. Get the `ogcode` binary onto the worker

ogcode requires **cgo** (a tree-sitter grammar) and builds against the control-
plane module through a local `replace`, which lives in the same repository as
`controlplane/` — so clone the one repo and build from it:

```sh
sudo apt-get install -y golang gcc git
git clone <ogcode-remote> ogcode
cd ogcode && go build -o /usr/local/bin/ogcode .
```

### 2. Providers

The worker runs sessions locally, so model keys live on that machine — env vars
or `~/.config/ogcode/config.json`. e.g. `export ANTHROPIC_API_KEY=sk-…`.

### 3. Run the worker

The worker hosts a full ogcode server itself — one per workspace — and reaches
the panel through its outbound tunnel. There is no separate `ogcode serve` to
run, and the worker listens on no inbound port:

```sh
cd ~/code/app && OGCODE_PAIRING_SECRET='PAIRING_SECRET' ogcode worker \
  --master https://panel.example.com --workspace ~/code/app &
```

`--workspace` is repeatable; each directory you list (default: the current one)
gets its own in-process server and its own subdomain. No `--master-ca` is needed
— the master's Let's Encrypt cert is publicly trusted. The worker logs
`registered with master workerID=…`.

### 4. Open it

The base host is the operator console, which lists the connected workers and,
for each, links into its worktree UIs:

```
https://panel.example.com/                          # operator console (workers + links)
https://<workerId>-<worktree>.panel.example.com/    # a worktree UI on that worker
```

After the operator login you get that worker's real ogcode UI. (A worker also
answers on the legacy bare `https://<workerId>.panel.example.com/` form.)

### Durable (systemd on the worker)

Put the pairing secret (and any model keys) in `/etc/ogcode/worker.env`
(`chmod 600`):

```ini
OGCODE_PAIRING_SECRET=PAIRING_SECRET
ANTHROPIC_API_KEY=sk-…
```

`ogcode-worker.service` is the only unit — the worker runs its own servers:

```ini
[Unit]
Description=ogcode worker
After=network-online.target
Wants=network-online.target
[Service]
User=youruser
WorkingDirectory=/home/youruser/code/app
EnvironmentFile=/etc/ogcode/worker.env
ExecStart=/usr/local/bin/ogcode worker --master https://panel.example.com --workspace /home/youruser/code/app
Restart=always
RestartSec=3
[Install]
WantedBy=multi-user.target
```

```sh
sudo systemctl daemon-reload && sudo systemctl enable --now ogcode-worker
```

It runs as `youruser` (not a locked-down account) because the agent reads/writes
that user's code.

### Multiple workers / projects on one machine

A single worker hosts every directory you give it via `--workspace`
(repeatable) — one in-process server and one subdomain each. You only need a
second `ogcode worker` to register a **separate worker id** (e.g. a different
`--name`). Workers bind no inbound ports, so there are no ports to keep
distinct.

## Security reminders

- The operator password is now internet-facing — make it strong. Login
  rate-limiting is **not yet built**.
- Prefer a network-layer control (VPN/Tailscale, IP allowlist, or an
  identity-aware proxy) over exposing the login to the whole internet.
- The pairing secret admits workers — keep it strong and rotate it.
