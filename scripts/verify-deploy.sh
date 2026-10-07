#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
sh -n deploy/install.sh
sh -n deploy/update.sh
python3 - <<'PY'
from pathlib import Path
import subprocess
import tempfile
root = Path.cwd()
binary = root/'server/.local/quakerelay'
if not binary.is_file():
    raise SystemExit('Build the Linux relay using scripts/verify.sh first.')
# Validate exact unit directives, substituting only the executable location.
with tempfile.TemporaryDirectory(prefix='quick-relay-units-') as directory:
    units = []
    for source in (root/'deploy/systemd').iterdir():
        target = Path(directory)/source.name
        target.write_text(source.read_text().replace('/opt/quick-relay/quakerelay',str(binary)))
        units.append(str(target))
    subprocess.run(['systemd-analyze','verify','--man=no',*units],check=True)
print('PASS: shell syntax and systemd unit validation (local executable path substituted)')
PY
if command -v caddy >/dev/null 2>&1; then
  caddy validate --config deploy/caddy/Caddyfile --adapter caddyfile
else
  printf '%s\n' 'SKIP: Caddy binary not installed; validate on the deployment host.'
fi
