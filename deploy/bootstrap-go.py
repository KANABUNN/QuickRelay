"""Install a verified official Go toolchain under the ignored .local/ directory.

Ubuntu 24.04 Python 3.12+; no root and no global Go installation is changed.
"""
import hashlib
import json
from pathlib import Path
import platform
import re
import tarfile
import tempfile
import urllib.request

root = Path(__file__).resolve().parents[1]
if platform.system() != 'Linux':
    raise SystemExit('Use this helper on the Ubuntu server.')
arch = {'x86_64': 'amd64', 'aarch64': 'arm64'}.get(platform.machine())
if not arch:
    raise SystemExit('Unsupported CPU architecture.')
minimum = tuple(map(int, re.search(r'^go (\d+\.\d+)', (root/'server/go.mod').read_text(), re.M).group(1).split('.')))
with urllib.request.urlopen('https://go.dev/dl/?mode=json', timeout=30) as response:
    releases = json.load(response)
release = next(r for r in releases if r['stable'] and tuple(map(int, re.match(r'go(\d+)\.(\d+)', r['version']).groups())) >= minimum)
asset = next(f for f in release['files'] if f['os'] == 'linux' and f['arch'] == arch and f['kind'] == 'archive')
destination = root/'.local/toolchain'
if (destination/'go').exists():
    raise SystemExit('A local toolchain already exists. Reuse .local/toolchain/go/bin or move it aside before upgrading.')
destination.mkdir(parents=True, exist_ok=True)
with tempfile.TemporaryDirectory(dir=destination) as scratch:
    archive = Path(scratch)/asset['filename']
    with urllib.request.urlopen('https://go.dev/dl/'+asset['filename'], timeout=60) as response, archive.open('wb') as target:
        while chunk := response.read(1024*1024):
            target.write(chunk)
    with archive.open('rb') as source:
        digest = hashlib.file_digest(source, 'sha256').hexdigest()
    if digest != asset['sha256']:
        raise SystemExit('Official Go SHA-256 mismatch.')
    with tarfile.open(archive, 'r:gz') as bundle:
        bundle.extractall(destination, filter='data')
print(f"Installed {release['version']} from the official archive with SHA-256 verified.")
print('Add to PATH: '+str(destination/'go/bin'))
