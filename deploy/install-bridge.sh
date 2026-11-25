#!/bin/sh
# Installs the EEW bridge beside an installed relay. Never starts it, never edits
# the relay's settings and never overwrites an existing bridge configuration.
set -eu
[ "$(id -u)" -eq 0 ] || { printf '%s\n' 'Run with sudo.' >&2; exit 1; }
repo=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
binaries=${1:-"$repo/server/bin"}
[ -x "$binaries/eew-bridge" ] || { printf 'Missing executable: %s\n' "$binaries/eew-bridge" >&2; exit 1; }
[ -f /etc/quick-relay/quick-relay.env ] || { printf '%s\n' 'Install the relay first (deploy/install.sh).' >&2; exit 1; }
if ! grep -qx 'RuntimeDirectory=quick-relay-handoff' /etc/systemd/system/quick-relay.service; then
  printf '%s\n' 'The installed relay unit has no hand-off directory; update the relay to this commit with deploy/update.sh first.' >&2
  exit 1
fi
if systemctl is-active --quiet quick-relay-bridge.service; then
  printf '%s\n' 'The bridge is running; use deploy/update.sh.' >&2; exit 1
fi
getent group quick-relay-handoff >/dev/null || groupadd --system quick-relay-handoff
if ! getent passwd quick-relay-bridge >/dev/null; then
  useradd --system --user-group --home-dir /var/lib/quick-relay-bridge --shell /usr/sbin/nologin quick-relay-bridge
fi
# The service joins quick-relay-handoff through SupplementaryGroups; the account
# itself stays outside it and never in the quick-relay group.
install -d -o root -g quick-relay-bridge -m 0750 /etc/quick-relay-bridge
install -d -o quick-relay-bridge -g quick-relay-bridge -m 0700 /var/lib/quick-relay-bridge
install -o root -g root -m 0755 "$binaries/eew-bridge" /opt/quick-relay/eew-bridge
if [ ! -e /etc/quick-relay-bridge/bridge.env ]; then
  install -o root -g quick-relay-bridge -m 0640 "$repo/deploy/eew-bridge.env.example" /etc/quick-relay-bridge/bridge.env
fi
runuser -u quick-relay-bridge -- /opt/quick-relay/eew-bridge -env-file /etc/quick-relay-bridge/bridge.env check
install -o root -g root -m 0644 "$repo/deploy/systemd/quick-relay-bridge.service" /etc/systemd/system/
systemd-analyze verify /etc/systemd/system/quick-relay-bridge.service
systemctl daemon-reload
printf '%s\n' 'Installed, not started. Enable the hand-off, issue tokens and open the route as described in docs/eew-bridge-deployment.md.'
