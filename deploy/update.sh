#!/bin/sh
# Validates before restart and keeps a consistent DB snapshot plus previous binaries.
set -eu
[ "$(id -u)" -eq 0 ] || { printf '%s\n' 'Run with sudo.' >&2; exit 1; }
repo=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
binaries=${1:-"$repo/server/bin"}
settings=/etc/quick-relay/quick-relay.env
[ -f "$settings" ] || { printf '%s\n' 'Run install.sh first.' >&2; exit 1; }
for name in quakerelay apns-send; do
  [ -x "$binaries/$name" ] || { printf 'Missing executable: %s\n' "$binaries/$name" >&2; exit 1; }
  install -o root -g root -m 0755 "$binaries/$name" "/opt/quick-relay/$name.next"
done
runuser -u quick-relay -- /opt/quick-relay/quakerelay.next -env-file "$settings" check
snapshot="/var/lib/quick-relay/backup-$(date -u +%Y%m%dT%H%M%SZ)-$$.db"
runuser -u quick-relay -- /opt/quick-relay/quakerelay -env-file "$settings" backup "$snapshot"
for name in quakerelay apns-send; do
  cp -p "/opt/quick-relay/$name" "/opt/quick-relay/$name.previous"
  mv "/opt/quick-relay/$name.next" "/opt/quick-relay/$name"
done
install -o root -g root -m 0644 "$repo"/deploy/systemd/quick-relay* /etc/systemd/system/
systemctl daemon-reload
systemctl restart quick-relay.service
attempt=0
while [ "$attempt" -lt 30 ]; do
  if curl --fail --silent --show-error --max-time 2 "${QUICK_RELAY_HEALTH_URL:-http://127.0.0.1:8080/health}" >/dev/null 2>&1; then
    printf 'Updated; /health passed. Check /readyz in live mode. Snapshot: %s\n' "$snapshot"
    exit 0
  fi
  attempt=$((attempt + 1))
  sleep 1
done
printf 'Health check failed. Inspect journalctl -u quick-relay. Restore the matching previous binary and snapshot if required: %s\n' "$snapshot" >&2
exit 1
