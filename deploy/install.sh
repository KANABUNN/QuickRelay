#!/bin/sh
# Initial installation only; preserves existing settings and never starts live traffic.
set -eu
[ "$(id -u)" -eq 0 ] || { printf '%s\n' 'Run with sudo.' >&2; exit 1; }
repo=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
binaries=${1:-"$repo/server/bin"}
for name in quakerelay apns-send; do
  [ -x "$binaries/$name" ] || { printf 'Missing executable: %s\n' "$binaries/$name" >&2; exit 1; }
done
if systemctl is-active --quiet quick-relay.service; then
  printf '%s\n' 'Service is running; use deploy/update.sh.' >&2; exit 1
fi
if { [ -e /etc/quakerelay/quakerelay.env ] || [ -e /var/lib/quakerelay/relay.db ]; } && [ ! -e /etc/quick-relay/quick-relay.env ]; then
  printf '%s\n' 'Legacy deployment found. Follow the migration section in docs/deployment.md first.' >&2; exit 1
fi
if ! getent passwd quick-relay >/dev/null; then
  useradd --system --user-group --home-dir /var/lib/quick-relay --shell /usr/sbin/nologin quick-relay
fi
install -d -o root -g root -m 0755 /opt/quick-relay
install -d -o root -g quick-relay -m 0750 /etc/quick-relay /etc/quick-relay/keys
install -d -o quick-relay -g quick-relay -m 0700 /var/lib/quick-relay
install -o root -g root -m 0755 "$binaries/quakerelay" "$binaries/apns-send" /opt/quick-relay/
if [ ! -e /etc/quick-relay/quick-relay.env ]; then
  umask 077
  python3 - "$repo/server/.env.example" /etc/quick-relay/quick-relay.env <<'PY'
from pathlib import Path
import secrets
import sys
source = Path(sys.argv[1]).read_text()
marker = 'REPLACE_WITH_RANDOM_64_HEX_CHARACTERS'
if source.count(marker) != 1:
    raise SystemExit('Invalid configuration template')
with open(sys.argv[2], 'x') as target:
    target.write(source.replace(marker, secrets.token_hex(32)))
PY
fi
chown root:quick-relay /etc/quick-relay/quick-relay.env
chmod 0640 /etc/quick-relay/quick-relay.env
install -o root -g root -m 0644 "$repo"/deploy/systemd/quick-relay* /etc/systemd/system/
install -d -o root -g root -m 0755 /etc/caddy
if [ ! -e /etc/caddy/quick-relay.Caddyfile ]; then
  install -o root -g root -m 0644 "$repo/deploy/caddy/Caddyfile" /etc/caddy/quick-relay.Caddyfile
fi
systemd-analyze verify /etc/systemd/system/quick-relay.service /etc/systemd/system/quick-relay-health.service /etc/systemd/system/quick-relay-health.timer
systemctl daemon-reload
printf '%s\n' 'Installed. Validate settings, start quick-relay, then import /etc/caddy/quick-relay.Caddyfile. See docs/deployment.md.'
