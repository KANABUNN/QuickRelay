#!/bin/sh
# Validates before restart and keeps a consistent DB snapshot plus previous binaries.
set -eu
[ "$(id -u)" -eq 0 ] || { printf '%s\n' 'Run with sudo.' >&2; exit 1; }
repo=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
binaries=${1:-"$repo/server/bin"}
settings=/etc/quick-relay/quick-relay.env
[ -f "$settings" ] || { printf '%s\n' 'Run install.sh first.' >&2; exit 1; }
names="quakerelay apns-send"
# The EEW bridge is updated together once install-bridge.sh has installed it.
bridge=false
if [ -e /opt/quick-relay/eew-bridge ]; then
  bridge=true
  names="$names eew-bridge"
fi
for name in $names; do
  [ -x "$binaries/$name" ] || { printf 'Missing executable: %s\n' "$binaries/$name" >&2; exit 1; }
  install -o root -g root -m 0755 "$binaries/$name" "/opt/quick-relay/$name.next"
done
runuser -u quick-relay -- /opt/quick-relay/quakerelay.next -env-file "$settings" check
stamp="$(date -u +%Y%m%dT%H%M%SZ)-$$"
snapshot="/var/lib/quick-relay/backup-$stamp.db"
runuser -u quick-relay -- /opt/quick-relay/quakerelay -env-file "$settings" backup "$snapshot"
if [ "$bridge" = true ]; then
  set --
  if [ -f /etc/quick-relay-bridge/bridge.env ]; then
    set -- -env-file /etc/quick-relay-bridge/bridge.env
  fi
  runuser -u quick-relay-bridge -- /opt/quick-relay/eew-bridge.next "$@" check
  runuser -u quick-relay-bridge -- /opt/quick-relay/eew-bridge "$@" backup "/var/lib/quick-relay-bridge/backup-$stamp.db"
fi
for name in $names; do
  cp -p "/opt/quick-relay/$name" "/opt/quick-relay/$name.previous"
  mv "/opt/quick-relay/$name.next" "/opt/quick-relay/$name"
done
# quick-relay.service joins this group for the EEW hand-off socket.
getent group quick-relay-handoff >/dev/null || groupadd --system quick-relay-handoff
install -o root -g root -m 0644 "$repo"/deploy/systemd/quick-relay* /etc/systemd/system/
systemctl daemon-reload
systemctl restart quick-relay.service

# Prints the HTTP status of $1 within 30 seconds once it equals $2; otherwise the last one.
wait_for() {
  attempt=0
  code=000
  while [ "$attempt" -lt 30 ]; do
    code=$(curl --silent --output /dev/null --write-out '%{http_code}' --max-time 2 "$1" 2>/dev/null) || code=000
    [ "$code" = "$2" ] && break
    attempt=$((attempt + 1))
    sleep 1
  done
  printf '%s' "$code"
}

if [ "$(wait_for "${QUICK_RELAY_HEALTH_URL:-http://127.0.0.1:8080/health}" 200)" != 200 ]; then
  printf 'Health check failed. Inspect journalctl -u quick-relay. Restore the matching previous binary and snapshot if required: %s\n' "$snapshot" >&2
  exit 1
fi
printf 'Updated; /health passed. Check /readyz in live mode. Snapshot: %s\n' "$snapshot"
if [ "$bridge" = true ] && systemctl is-active --quiet quick-relay-bridge.service; then
  systemctl restart quick-relay-bridge.service
  # 503 means running but degraded, for example while DMDATA is disconnected.
  case "$(wait_for "${QUICK_RELAY_BRIDGE_HEALTH_URL:-http://127.0.0.1:9082/healthz}" 200)" in
    200) printf '%s\n' 'EEW bridge restarted; /healthz is healthy.' ;;
    503) printf '%s\n' 'EEW bridge restarted but reports degraded; inspect journalctl -u quick-relay-bridge.' >&2 ;;
    *)
      printf 'EEW bridge is not answering. Inspect journalctl -u quick-relay-bridge; previous binary: /opt/quick-relay/eew-bridge.previous, snapshot: /var/lib/quick-relay-bridge/backup-%s.db\n' "$stamp" >&2
      exit 1
      ;;
  esac
fi
