#!/usr/bin/env python3
"""Run a restored binary/database offline. Use PrivateNetwork=yes for this drill."""
import json
import os
from pathlib import Path
import secrets
import sqlite3
import subprocess
import sys
import time
import urllib.error
import urllib.request

root = Path(sys.argv[1]).resolve()
binary = root / "bin/quakerelay"
binary.chmod(0o700)
env = {"PATH": "/usr/bin:/bin", "RELAY_MODE": "offline", "LISTEN_ADDR": "127.0.0.1:18081",
       "DATABASE_PATH": str(root / "database.sqlite3"), "PAIRING_SECRET": secrets.token_hex(32)}
process = subprocess.Popen([str(binary), "serve"], env=env, cwd=root,
                           stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
try:
    deadline = time.monotonic() + 15
    while time.monotonic() < deadline:
        if process.poll() is not None:
            raise RuntimeError("restored relay exited early")
        try:
            with urllib.request.urlopen("http://127.0.0.1:18081/healthz", timeout=1) as r:
                assert r.status == 200
            break
        except (OSError, urllib.error.URLError):
            time.sleep(0.2)
    else:
        raise RuntimeError("restored health check timed out")
    try:
        urllib.request.urlopen("http://127.0.0.1:18081/readyz", timeout=2)
        raise RuntimeError("offline drill unexpectedly reported live readiness")
    except urllib.error.HTTPError as response:
        assert response.code == 503
        state = json.load(response)
        assert state["db"] is True and state["source_connected"] is False and state["apns_configured"] is False
    print("Restored application startup: PASS (offline; no DMDATA/APNs credentials supplied)")
finally:
    process.terminate()
    try:
        process.wait(timeout=10)
    except subprocess.TimeoutExpired:
        process.kill()
        process.wait()
with sqlite3.connect(str(root / "database.sqlite3")) as db:
    assert db.execute("PRAGMA integrity_check").fetchall() == [("ok",)]
    assert db.execute("PRAGMA foreign_key_check").fetchall() == []
print("Restored database integrity and foreign keys: PASS")
