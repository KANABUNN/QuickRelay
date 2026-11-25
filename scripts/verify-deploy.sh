#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
sh -n deploy/install.sh
sh -n deploy/install-bridge.sh
sh -n deploy/update.sh
python3 - <<'PY'
from pathlib import Path
import subprocess
import tempfile
root = Path.cwd()
binaries = {name: root/'server/.local'/name for name in ('quakerelay', 'eew-bridge')}
for binary in binaries.values():
    if not binary.is_file():
        raise SystemExit('Build the Linux relay and bridge using scripts/verify.sh first.')
# Validate exact unit directives, substituting only the executable locations.
with tempfile.TemporaryDirectory(prefix='quick-relay-units-') as directory:
    units = []
    for source in (root/'deploy/systemd').iterdir():
        target = Path(directory)/source.name
        text = source.read_text()
        for name, binary in binaries.items():
            text = text.replace('/opt/quick-relay/' + name + ' ', str(binary) + ' ')
        target.write_text(text)
        units.append(str(target))
    subprocess.run(['systemd-analyze','verify','--man=no',*units],check=True)
print('PASS: shell syntax and systemd unit validation (local executable paths substituted)')
PY
if command -v caddy >/dev/null 2>&1; then
  work=$(mktemp -d)
  trap 'rm -rf "$work"' EXIT
  if caddy validate --config deploy/caddy/Caddyfile --adapter caddyfile 2>"$work/error"; then
    printf '%s\n' 'PASS: Caddy configuration'
  else
    # Distribution packages may predate the subdirective; production needs Caddy 2.7+.
    grep -q stream_close_delay "$work/error" || { cat "$work/error" >&2; exit 1; }
    grep -v stream_close_delay deploy/caddy/Caddyfile >"$work/Caddyfile"
    caddy validate --config "$work/Caddyfile" --adapter caddyfile
    printf 'NOTE: Caddy %s lacks stream_close_delay; validated the rest. Deploy with Caddy 2.7 or later.\n' "$(caddy version)"
  fi
else
  printf '%s\n' 'SKIP: Caddy binary not installed; validate on the deployment host.'
fi
