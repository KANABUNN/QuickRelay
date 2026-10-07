#!/usr/bin/env python3
"""Upload an encrypted backup over restricted SSH, read it back and compare SHA-256."""
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile


def digest(path):
    h = hashlib.sha256()
    with open(path, "rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            h.update(block)
    return h.hexdigest()


def main():
    os.umask(0o077)
    if len(sys.argv) != 3:
        raise ValueError("archive and checksum required")
    archive = Path(sys.argv[1])
    expected = sys.argv[2]
    if not re.fullmatch(r"quick-relay-\d{8}T\d{6}Z-[a-f0-9]{8}\.tar\.gz\.gpg", archive.name):
        raise ValueError("Unexpected archive name")
    if not re.fullmatch(r"[a-f0-9]{64}", expected) or digest(archive) != expected:
        raise ValueError("Source checksum mismatch")
    config = json.loads(Path("/etc/quick-relay-ops/offsite.json").read_text())
    ssh = ["ssh", "-F", "/dev/null", "-i", config["identity_file"],
           "-o", "BatchMode=yes", "-o", "IdentitiesOnly=yes", "-o", "StrictHostKeyChecking=yes",
           "-o", "UserKnownHostsFile=" + config["known_hosts"], "-o", "ConnectTimeout=15",
           "-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=3",
           "-p", str(config["port"]), config["user"] + "@" + config["host"]]
    size = archive.stat().st_size
    with archive.open("rb") as stream:
        result = subprocess.run(ssh + ["put " + archive.name + " " + expected + " " + str(size)],
                                stdin=stream, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                timeout=600, check=True)
    acknowledgement = json.loads(result.stdout)
    if acknowledgement != {"sha256": expected, "bytes": size}:
        raise ValueError("Remote acknowledgement mismatch")
    with tempfile.TemporaryFile(dir=archive.parent) as returned:
        subprocess.run(ssh + ["get " + archive.name], stdin=subprocess.DEVNULL,
                       stdout=returned, stderr=subprocess.PIPE, timeout=600, check=True)
        returned.seek(0)
        h = hashlib.sha256()
        for block in iter(lambda: returned.read(1024 * 1024), b""):
            h.update(block)
        if h.hexdigest() != expected:
            raise ValueError("Downloaded copy checksum mismatch")
    print("offsite roundtrip verified")


if __name__ == "__main__":
    try:
        main()
    except Exception:
        print("offsite backup failed", file=sys.stderr)
        sys.exit(1)
