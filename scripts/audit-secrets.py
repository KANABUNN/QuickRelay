"""Focused secret checks. Prints locations/rule names, never matching values.

Default: tracked and unignored working files. --history: also every reachable blob.
This is a pattern scan, not proof that arbitrary secrets do not exist.
"""
import argparse
from pathlib import Path
import re
import subprocess

ROOT = Path(__file__).resolve().parents[1]
parser = argparse.ArgumentParser()
parser.add_argument('--history', action='store_true')
args = parser.parse_args()
patterns = {
    'openpgp-private-key-material': re.compile(rb'-----BEGIN PGP PRIVATE KEY BLOCK-----[\r\n]'),
    'private-key-material': re.compile(rb'-----BEGIN (?:EC |RSA |OPENSSH |ENCRYPTED )?PRIVATE KEY-----\s+[A-Za-z0-9+/=]{40,}'),
    'github-token': re.compile(rb'\bgh[pousr]_[A-Za-z0-9]{30,}\b'),
    'github-fine-grained-token': re.compile(rb'\bgithub_pat_[A-Za-z0-9_]{40,}\b'),
    'aws-access-id': re.compile(rb'\bAKIA[A-Z0-9]{16}\b'),
}
secret_suffixes = {'.p8', '.p12', '.pfx', '.mobileprovision', '.provisionprofile'}
findings = set()


def git(*values):
    return subprocess.check_output(['git', *values], cwd=ROOT)


def inspect(data, label, path):
    if Path(path).suffix.lower() in secret_suffixes or Path(path).name == '.env':
        findings.add((label, 'private-file-tracked'))
    for name, pattern in patterns.items():
        if pattern.search(data):
            findings.add((label, name))
    if Path(path).suffix == '.env' or Path(path).name.startswith('.env'):
        for line in data.splitlines():
            match = re.match(rb'\s*(DMDATA_API_KEY|DMDATA_TOKEN|APNS_DEVICE_TOKEN|PAIRING_SECRET)\s*=\s*(.*?)\s*$', line)
            if match:
                value = match.group(2).strip(b'\'"')
                if value and b'REPLACE' not in value and b'example' not in value.lower() and not value.startswith(b'#'):
                    findings.add((label, 'credential-value-in-env'))


working = 0
for name in set(git('ls-files', '-co', '--exclude-standard', '-z').decode().rstrip('\0').split('\0')):
    path = ROOT / name
    if path.is_file():
        inspect(path.read_bytes(), name, name)
        working += 1

blobs = 0
if args.history:
    objects = git('rev-list', '--objects', '--all').decode().splitlines()
    with subprocess.Popen(['git', 'cat-file', '--batch'], cwd=ROOT, stdin=subprocess.PIPE, stdout=subprocess.PIPE) as proc:
        for row in objects:
            oid, _, path = row.partition(' ')
            proc.stdin.write((oid + '\n').encode()); proc.stdin.flush()
            header = proc.stdout.readline().decode().split()
            size = int(header[2])
            data = proc.stdout.read(size)
            if len(data) != size or proc.stdout.read(1) != b'\n':
                raise RuntimeError('Incomplete git object read')
            if header[1] == 'blob':
                blobs += 1
                inspect(data, f'{oid[:12]}:{path}', path)
        proc.stdin.close()
        if proc.wait() != 0:
            raise RuntimeError('Git object scan failed')

for location, rule in sorted(findings):
    print(f'REVIEW: {location} [{rule}]')
print(f'Checked {working} working files and {blobs} historical blobs; findings={len(findings)}')
raise SystemExit(1 if findings else 0)
