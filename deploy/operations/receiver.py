#!/usr/bin/env python3
"""Forced SSH command. Only receive/read named encrypted archives under this folder."""
import hashlib
import json
import os
from pathlib import Path
import re
import shlex
import sys
import tempfile
import time

ROOT = Path(__file__).resolve().parent / "archives"
NAME = re.compile(r"quick-relay-\d{8}T\d{6}Z-[a-f0-9]{8}\.tar\.gz\.gpg")
MAX_SIZE = 1024 ** 3


def main():
    os.umask(0o077)
    args = shlex.split(os.environ.get("SSH_ORIGINAL_COMMAND", ""))
    if len(args) not in (2, 4) or not NAME.fullmatch(args[1]):
        raise ValueError("Unsupported command")
    ROOT.mkdir(mode=0o700, exist_ok=True)
    target = ROOT / args[1]
    if target.is_symlink():
        raise ValueError("Unexpected link")
    if args[0] == "get" and len(args) == 2:
        with target.open("rb") as stream:
            while True:
                block = stream.read(1024 * 1024)
                if not block:
                    break
                sys.stdout.buffer.write(block)
        return
    if args[0] != "put" or len(args) != 4:
        raise ValueError("Unsupported command")
    expected, size = args[2], int(args[3])
    if not re.fullmatch(r"[a-f0-9]{64}", expected) or not 1 <= size <= MAX_SIZE:
        raise ValueError("Invalid transfer")
    if target.exists():
        raise ValueError("Archive already exists")
    fd, tmp = tempfile.mkstemp(prefix=".upload-", dir=ROOT)
    try:
        digest = hashlib.sha256()
        remaining = size
        with os.fdopen(fd, "wb") as stream:
            while remaining:
                block = sys.stdin.buffer.read(min(1024 * 1024, remaining))
                if not block:
                    raise ValueError("Truncated transfer")
                stream.write(block)
                digest.update(block)
                remaining -= len(block)
            if sys.stdin.buffer.read(1) or digest.hexdigest() != expected:
                raise ValueError("Hash or size mismatch")
            stream.flush()
            os.fsync(stream.fileno())
        os.chmod(tmp, 0o600)
        # Atomic, no overwrite even when two transfers race.
        os.link(tmp, target)
        print(json.dumps({"sha256": expected, "bytes": size}))
        cutoff = time.time() - 30 * 86400
        for old in ROOT.iterdir():
            if (NAME.fullmatch(old.name) and not old.is_symlink() and old.is_file()
                    and old != target and old.stat().st_mtime < cutoff):
                old.unlink()
    finally:
        try:
            Path(tmp).unlink()
        except FileNotFoundError:
            pass


if __name__ == "__main__":
    try:
        main()
    except Exception:
        print("backup receiver rejected request", file=sys.stderr)
        sys.exit(1)
