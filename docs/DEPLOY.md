# Deploying to the mini PC

Assumes a Linux box you can SSH into. Nothing here needs a public IP, a domain
you own DNS for on the box, or an open port on the router.

## 1. Build

The SQLite driver is pure Go, so cross-compiling from any machine works with no
C toolchain:

```bash
GOOS=linux GOARCH=amd64 go build -o gymd ./cmd/gymd
scp gymd mini:/usr/local/bin/gymd
```

On an ARM mini PC use `GOARCH=arm64`.

## 2. Layout on the box

```bash
sudo useradd --system --home /var/lib/gymlogger --shell /usr/sbin/nologin gymlogger
sudo mkdir -p /var/lib/gymlogger
sudo chown gymlogger:gymlogger /var/lib/gymlogger
```

The database lives at `/var/lib/gymlogger/gym.db`. It is the only state that
matters, and it is small — back it up with a nightly copy.

## 3. Secrets

```bash
sudo mkdir -p /etc/gymlogger
sudo tee /etc/gymlogger/env >/dev/null <<EOF
GYM_AUTH_TOKEN=$(openssl rand -hex 32)
GYM_DB=/var/lib/gymlogger/gym.db
GYM_ADDR=127.0.0.1:8080
GYM_TZ=Europe/Bucharest
# OPENCODE_API_KEY=...
# FCM_CREDENTIALS=/etc/gymlogger/fcm.json
EOF
sudo chmod 600 /etc/gymlogger/env
sudo chown gymlogger:gymlogger /etc/gymlogger/env
```

Print the token once and put it into the app's settings screen:

```bash
sudo grep GYM_AUTH_TOKEN /etc/gymlogger/env
```

## 4. systemd

`/etc/systemd/system/gymd.service`:

```ini
[Unit]
Description=Gym logger
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=gymlogger
Group=gymlogger
EnvironmentFile=/etc/gymlogger/env
ExecStart=/usr/local/bin/gymd
Restart=always
RestartSec=5

# The service needs the network, its own data directory, and nothing else.
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/lib/gymlogger
ProtectKernelTunables=true
ProtectControlGroups=true
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now gymd
sudo systemctl status gymd
journalctl -u gymd -f
```

Check it locally before going further:

```bash
curl -s localhost:8080/healthz
curl -s -H "Authorization: Bearer $TOKEN" localhost:8080/v1/next
```

## 5. Cloudflare Tunnel

The tunnel dials out from the mini PC, so the router stays closed.

```bash
# Install cloudflared from Cloudflare's apt repo, then:
cloudflared tunnel login
cloudflared tunnel create gym
cloudflared tunnel route dns gym gym.yourdomain.com
```

`/etc/cloudflared/config.yml`:

```yaml
tunnel: gym
credentials-file: /root/.cloudflared/<TUNNEL-UUID>.json

ingress:
  - hostname: gym.yourdomain.com
    service: http://127.0.0.1:8080
  - service: http_status:404
```

```bash
sudo cloudflared service install
sudo systemctl enable --now cloudflared
```

Then from anywhere:

```bash
curl -s https://gym.yourdomain.com/healthz
```

### The tunnel is not a firewall

That hostname is on the public internet. Anyone who finds it can reach the
service, which is exactly why `GYM_AUTH_TOKEN` is mandatory and every route
except `/healthz` checks it in constant time.

Two things worth adding on top, both free:

- **Cloudflare Access** in front of the hostname, so requests without a session
  never reach the mini PC at all. The app sends a bearer token, so use a Service
  Token policy rather than the browser login flow.
- **A WAF rate limit** on `/v1/log`, since that route is the one that spends
  money on model calls.

## 6. Backups

```bash
sudo tee /etc/cron.daily/gymlogger-backup >/dev/null <<'EOF'
#!/bin/sh
# .backup is safe against a live WAL database; cp is not.
sqlite3 /var/lib/gymlogger/gym.db ".backup '/var/lib/gymlogger/backup-$(date +%u).db'"
EOF
sudo chmod +x /etc/cron.daily/gymlogger-backup
```

Seven rotating copies, one per weekday. If `sqlite3` is not installed,
`apt install sqlite3`.

## 7. Setting it up for you

```bash
TOKEN=$(sudo grep -oP '(?<=GYM_AUTH_TOKEN=).*' /etc/gymlogger/env)
API=https://gym.yourdomain.com

curl -s -X POST "$API/v1/settings" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"bodyweight_kg":"78","no_rack":"true","reminder_hour":"17"}'
```

`no_rack=true` is the one that matters if you are still squatting off the dip
bar. It makes the coach say something once, plainly, rather than cheering.
