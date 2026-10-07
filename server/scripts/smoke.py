"""Exercise the built relay process, real HTTP API and SQLite, without external services.

Run from server/: python scripts/smoke.py --binary .local/quakerelay.exe
All credentials and data are temporary. No APNs / DMDATA connection is made.
"""
import argparse
import contextlib
import datetime as dt
import json
import os
import pathlib
import re
import secrets
import socket
import sqlite3
import subprocess
import tempfile
import time
import urllib.error
import urllib.request

parser = argparse.ArgumentParser()
parser.add_argument("--binary", required=True)
args = parser.parse_args()
binary = str(pathlib.Path(args.binary).resolve())
pathlib.Path(".local").mkdir(exist_ok=True)
with tempfile.TemporaryDirectory(prefix="smoke-", dir=".local") as temp:
    root = pathlib.Path(temp).resolve()
    assert root.is_relative_to(pathlib.Path(".local").resolve())
    with socket.socket() as probe:
        probe.bind(("127.0.0.1", 0))
        port = probe.getsockname()[1]
    env = dict(os.environ, RELAY_MODE="offline", LISTEN_ADDR=f"127.0.0.1:{port}",
               DATABASE_PATH=str(root / "relay.db"), PAIRING_SECRET=secrets.token_hex(32))
    base = f"http://127.0.0.1:{port}"
    flags = subprocess.CREATE_NO_WINDOW if os.name == "nt" else 0

    def cli(*values):
        return subprocess.run([binary, *values], env=env, text=True, encoding="utf-8",
                              capture_output=True, check=True, creationflags=flags).stdout

    def http(method, path, data=None, token=None):
        headers = {"Content-Type": "application/json"}
        if token:
            headers["Authorization"] = "Bearer " + token
        body = None if data is None else json.dumps(data).encode()
        req = urllib.request.Request(base + path, data=body, method=method, headers=headers)
        try:
            result = urllib.request.urlopen(req, timeout=5)
        except urllib.error.HTTPError as error:
            result = error
        with result:
            raw = result.read()
            decoded = json.loads(raw) if "application/json" in result.headers.get("Content-Type", "") else raw.decode()
            return result.status, decoded

    def start():
        log = open(root / "server.log", "ab")
        proc = subprocess.Popen([binary, "serve"], env=env, stdout=log, stderr=log, creationflags=flags)
        log.close()
        for _ in range(100):
            if proc.poll() is not None:
                raise RuntimeError("Server exited: " + (root / "server.log").read_text())
            try:
                if http("GET", "/healthz")[0] == 200:
                    return proc
            except OSError:
                pass
            time.sleep(0.05)
        proc.terminate()
        proc.wait(timeout=10)
        raise RuntimeError("Startup timeout")

    cli("check")
    proc = start()
    try:
        assert http("GET", "/health")[0] == 200
        assert http("GET", "/readyz")[0] == 503
        assert http("GET", "/api/v1/sync")[0] == 401
        code = re.search(r"\b[0-9]{8}\b", cli("pair")).group()
        status, paired = http("POST", "/api/v1/pair/complete",
                              {"pairing_code": code, "installation_id": "smoke-phone"})
        assert status == 200
        token = paired["device_access_token"]
        status, device = http("POST", "/api/v1/devices/register",
                              {"installation_id": "smoke-phone", "device_token": "ab" * 32,
                               "environment": "development", "device_name": "Smoke test"}, token)
        assert status == 200 and "device_token" not in device["device"]
        assert http("PUT", "/api/v1/devices/me",
                    {"installation_id": "someone-else", "device_token": "cd" * 32,
                     "environment": "production"}, token)[0] == 400

        at = dt.datetime.now(dt.timezone.utc) - dt.timedelta(seconds=10)
        frames = []
        for index, (serial, cancelled) in enumerate([(1, False), (3, False), (2, False), (3, True)]):
            body = {"_schema": {"type": "eew-information", "version": "1.0.0"},
                    "eventId": "20261005000000", "serialNo": str(serial), "status": "通常",
                    "infoType": "取消" if cancelled else "発表",
                    "reportDateTime": (at + dt.timedelta(seconds=index)).isoformat(),
                    "title": "合成テスト電文", "body": {"isCanceled": cancelled, "isLastInfo": cancelled}}
            frames.append({"type": "data", "id": f"synthetic-smoke-{index}",
                           "classification": "eew.forecast", "head": {"type": "VXSE45", "test": False},
                           "format": "json", "encoding": "utf-8", "compression": None,
                           "body": json.dumps(body, ensure_ascii=False)})
        source = root / "frames.jsonl"
        source.write_text("\n".join(json.dumps(f, ensure_ascii=False) for f in frames), encoding="utf-8")
        cli("replay", str(source))
        status, page = http("GET", "/api/v1/sync?after_sequence=0&limit=2", token=token)
        assert status == 200 and len(page["items"]) == 2 and page["has_more"]
        status, second = http("GET", f'/api/v1/sync?after_sequence={page["next_after_sequence"]}&limit=2', token=token)
        assert status == 200 and len(second["items"]) == 2 and not second["has_more"]
        assert second["items"][-1]["event"]["is_cancelled"]
        assert second["items"][-1]["event"]["source_serial"] == 3
        assert http("GET", "/api/v1/events/current", token=token)[0] == 200
        status, detail = http("GET", "/api/v1/events/20261005000000", token=token)
        assert status == 200 and len(detail["reports"]) == 4
        status, state = http("GET", "/api/v1/status", token=token)
        assert status == 200 and not state["deliveries"], "Historical replay must never enqueue APNs"
        assert http("GET", "/metrics", token=token)[0] == 200

        cli("backup", str(root / "backup.db"))
        with contextlib.closing(sqlite3.connect(root / "backup.db")) as db:
            assert db.execute("PRAGMA integrity_check").fetchone()[0] == "ok"
            assert db.execute("SELECT count(*) FROM reports").fetchone()[0] == 4
        proc.terminate()
        proc.wait(timeout=10)
        if os.name != "nt":
            assert proc.returncode == 0, "SIGTERM should shut down cleanly"
        proc = start()
        status, persisted = http("GET", "/api/v1/events/20261005000000", token=token)
        assert status == 200 and len(persisted["reports"]) == 4
        assert http("DELETE", "/api/v1/devices/me", token=token)[0] == 200
        assert http("GET", "/api/v1/sync", token=token)[0] == 401
        print("PASS: real process, pairing, token registration, authorization, historical replay,")
        print("      paged history, readiness, metrics, SQLite snapshot/restore read, restart, revocation")
        print("External DMDATA, Apple APNs and iPhone delivery were not exercised.")
    finally:
        if proc.poll() is None:
            proc.terminate()
            proc.wait(timeout=10)
