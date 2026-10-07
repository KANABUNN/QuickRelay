#!/usr/bin/env python3
"""Encrypted SQLite snapshots and a small, non-sensitive operations health endpoint."""
import argparse
import contextlib
import datetime
import fcntl
import hashlib
import http.server
import json
import os
from pathlib import Path, PurePosixPath
import re
import shutil
import sqlite3
import subprocess
import tarfile
import tempfile
import time
import urllib.request


class OperationError(Exception):
    pass


def run(args, **kwargs):
    # Do not publish captured stderr: a failing third-party command may include secrets.
    try:
        return subprocess.run(args, check=True, stdout=subprocess.PIPE,
                              stderr=subprocess.PIPE, timeout=900, **kwargs).stdout
    except (subprocess.CalledProcessError, subprocess.TimeoutExpired) as exc:
        raise OperationError(Path(args[0]).name + " failed; see protected diagnostics") from exc


def digest(path):
    with open(path, "rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def atomic_json(path, value):
    path = Path(path)
    fd, name = tempfile.mkstemp(prefix=".state-", dir=path.parent)
    try:
        with os.fdopen(fd, "w") as stream:
            json.dump(value, stream, sort_keys=True)
            stream.write("\n")
            stream.flush()
            os.fsync(stream.fileno())
        os.chmod(name, 0o644)  # This file contains health metadata only.
        os.replace(name, path)
    finally:
        Path(name).unlink(missing_ok=True)


def read_json(path):
    try:
        return json.loads(Path(path).read_text())
    except (OSError, ValueError):
        return {}


def snapshot(source, destination):
    uri = Path(source).resolve().as_uri() + "?mode=ro"
    with contextlib.closing(sqlite3.connect(uri, uri=True, timeout=10)) as src:
        with contextlib.closing(sqlite3.connect(destination)) as dst:
            deadline = time.monotonic() + 300
            def progress(status, remaining, total):
                if time.monotonic() > deadline:
                    raise OperationError("SQLite snapshot timed out")
            src.backup(dst, pages=256, progress=progress, sleep=0.05)
            if dst.execute("PRAGMA integrity_check").fetchall() != [("ok",)]:
                raise OperationError("SQLite integrity check failed")
    os.chmod(destination, 0o600)


def regular_files(source):
    source = Path(source)
    if source.is_symlink():
        raise OperationError("Backup sources must not be symlinks")
    if source.is_file():
        yield source, ""
    elif source.is_dir():
        for item in sorted(source.rglob("*")):
            if item.is_symlink() or not (item.is_file() or item.is_dir()):
                raise OperationError("Non-regular backup source")
            if item.is_file():
                yield item, item.relative_to(source).as_posix()
    else:
        raise OperationError("Required backup source is missing")


def package(config, target, work):
    root = work / "payload"
    root.mkdir(mode=0o700)
    snapshot(config["database"], root / "database.sqlite3")
    for source, name in config["sources"].items():
        safe_name(name)
        for item, relative in regular_files(source):
            destination = root / name
            if relative:
                destination /= relative
            destination.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
            shutil.copyfile(item, destination)
            os.chmod(destination, 0o600)
    manifest = {p.relative_to(root).as_posix(): digest(p)
                for p in root.rglob("*") if p.is_file()}
    (root / "manifest.json").write_text(json.dumps(manifest, sort_keys=True) + "\n")
    with tarfile.open(target, "w:gz") as archive:
        for item in sorted(root.rglob("*")):
            if item.is_file():
                archive.add(item, arcname=item.relative_to(root).as_posix(), recursive=False)


def safe_name(name):
    path = PurePosixPath(name)
    if not name or path.is_absolute() or ".." in path.parts or "\\" in name:
        raise OperationError("Unsafe archive path")
    return path


def encrypt(config, source, destination, work):
    home = work / "public-keyring"
    home.mkdir(mode=0o700)
    base = ["gpg", "--batch", "--homedir", str(home)]
    run(base + ["--import", config["public_key"]])
    fingerprint = config["recipient"]
    if not re.fullmatch(r"[A-F0-9]{40,64}", fingerprint):
        raise OperationError("A full GPG recipient fingerprint is required")
    run(base + ["--trust-model", "always", "--recipient", fingerprint,
                "--output", str(destination), "--encrypt", str(source)])


def backup(config):
    archives = Path(config["archives"])
    archives.mkdir(parents=True, exist_ok=True, mode=0o700)
    state = Path(config["state_dir"])
    state.mkdir(parents=True, exist_ok=True, mode=0o755)
    # systemd already serializes starts; this also protects manual invocations.
    with open(archives / ".lock", "a") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        try:
            with tempfile.TemporaryDirectory(prefix=".backup-", dir=archives) as tmp:
                work = Path(tmp)
                plain = work / "payload.tar.gz"
                package(config, plain, work)
                encrypted = work / "payload.gpg"
                encrypt(config, plain, encrypted, work)
                name = "quick-relay-" + datetime.datetime.now(datetime.timezone.utc).strftime("%Y%m%dT%H%M%SZ") + "-" + os.urandom(4).hex() + ".tar.gz.gpg"
                destination = archives / name
                os.chmod(encrypted, 0o600)
                os.replace(encrypted, destination)
                result = {"ok": True, "completed_at": time.time(), "archive": name,
                          "sha256": digest(destination), "offsite_ok": False}
                # The hook must upload, download to a separate file and compare bytes;
                # it is an audited local executable, never a shell command from JSON.
                hook = config.get("offsite_hook")
                if hook:
                    run([hook, str(destination), result["sha256"]])
                    result["offsite_ok"] = True
                atomic_json(state / "backup.json", result)
                # Retention only after a fully successful configured backup. Never touch
                # pre-existing W7 backups or archives outside this exact name pattern.
                cutoff = time.time() - max(2, config.get("retention_days", 14)) * 86400
                for old in archives.glob("quick-relay-*.tar.gz.gpg"):
                    if (re.fullmatch(r"quick-relay-\d{8}T\d{6}Z-[a-f0-9]{8}\.tar\.gz\.gpg", old.name)
                            and old.is_file() and not old.is_symlink()
                            and old.stat().st_mtime < cutoff and old != destination):
                        old.unlink()
                print(json.dumps(result, sort_keys=True))
                return destination
        except Exception:
            previous = read_json(state / "backup.json")
            previous["ok"] = False
            previous["failed_at"] = time.time()
            atomic_json(state / "backup.json", previous)
            raise


def unpack_verified(plain, destination):
    destination = Path(destination)
    if destination.exists():
        raise OperationError("Restore target must not exist")
    destination.mkdir(mode=0o700, parents=True)
    with tarfile.open(plain, "r:gz") as archive:
        members = archive.getmembers()
        seen = set()
        for member in members:
            safe_name(member.name)
            if not member.isfile() or member.name in seen:
                raise OperationError("Archive contains links, special files or duplicate entries")
            seen.add(member.name)
        for member in members:
            target = destination / member.name
            target.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
            with archive.extractfile(member) as src, open(target, "xb") as dst:
                shutil.copyfileobj(src, dst)
            target.chmod(0o600)
    manifest = json.loads((destination / "manifest.json").read_text())
    actual = {p.relative_to(destination).as_posix(): digest(p)
              for p in destination.rglob("*") if p.is_file() and p.relative_to(destination).as_posix() != "manifest.json"}
    if manifest != actual:
        raise OperationError("Archive manifest verification failed")
    with contextlib.closing(sqlite3.connect((destination / "database.sqlite3").as_uri() + "?mode=ro", uri=True)) as db:
        if db.execute("PRAGMA integrity_check").fetchall() != [("ok",)]:
            raise OperationError("Restored database integrity check failed")
        counts = {table: db.execute('SELECT count(*) FROM "' + table + '"').fetchone()[0]
                  for table in ("devices", "reports", "deliveries")}
    return counts


def verify(archive, key, destination, expected_hash):
    if not re.fullmatch(r"[a-f0-9]{64}", expected_hash) or digest(archive) != expected_hash:
        raise OperationError("Encrypted archive hash mismatch")
    destination = Path(destination).absolute()
    if destination.exists():
        raise OperationError("Restore target must not exist")
    with tempfile.TemporaryDirectory(prefix=".restore-", dir=destination.parent) as tmp:
        work = Path(tmp)
        home = work / "keyring"
        home.mkdir(mode=0o700)
        base = ["gpg", "--batch", "--homedir", str(home)]
        try:
            run(base + ["--import", str(key)])
            plain = work / "payload.tar.gz"
            run(base + ["--output", str(plain), "--decrypt", str(archive)])
            result = unpack_verified(plain, destination)
            print(json.dumps({"integrity": "ok", "counts": result}))
            return result
        finally:
            subprocess.run(["gpgconf", "--homedir", str(home), "--kill", "all"],
                           stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=10)


def health(config, now=None):
    now = time.time() if now is None else now
    state = read_json(Path(config["state_dir"]) / "backup.json")
    age = now - state.get("completed_at", 0)
    backup_ok = state.get("ok") is True and 0 <= age <= config.get("max_backup_age_hours", 30) * 3600
    disk = shutil.disk_usage(config["disk_path"])
    disk_ok = disk.free >= config.get("min_free_bytes", 1073741824) and disk.free / disk.total >= 0.10
    try:
        request = urllib.request.Request(config["readiness_url"], headers={"Accept": "application/json"})
        with urllib.request.urlopen(request, timeout=5) as response:
            ready = json.load(response)
        relay_ok = all(ready.get(k) is True for k in ("ok", "db", "apns_configured", "source_connected"))
    except Exception:
        relay_ok = False
    offsite_ok = backup_ok and state.get("offsite_ok") is True
    drill = read_json(Path(config["state_dir"]) / "alert-drill.json")
    drill_active = 0 < drill.get("until", 0) - now <= 900
    checks = {"relay": relay_ok, "disk": disk_ok, "backup": backup_ok,
              "offsite": offsite_ok, "alert_drill": not drill_active}
    required = [checks[k] for k in ("relay", "disk", "backup", "alert_drill")]
    if config.get("require_offsite", True):
        required.append(offsite_ok)
    return {"ok": all(required), "checks": checks}


def serve(config):
    class Handler(http.server.BaseHTTPRequestHandler):
        def do_GET(self):
            if self.path != "/ops/readyz":
                self.send_error(404)
                return
            try:
                result = health(config)
            except Exception:
                result = {"ok": False}
            data = (json.dumps(result, sort_keys=True) + "\n").encode()
            self.send_response(200 if result["ok"] else 503)
            self.send_header("Content-Type", "application/json")
            self.send_header("Cache-Control", "no-store")
            self.send_header("Content-Length", str(len(data)))
            self.end_headers()
            if self.command != "HEAD":
                self.wfile.write(data)
        def do_HEAD(self):
            self.do_GET()
        def log_message(self, *_):
            pass
    http.server.ThreadingHTTPServer(("127.0.0.1", config.get("port", 9081)), Handler).serve_forever()


def main():
    os.umask(0o077)
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--config", default="/etc/quick-relay-ops/config.json")
    commands = parser.add_subparsers(dest="command", required=True)
    for name in ("backup", "health", "serve"):
        commands.add_parser(name)
    restore = commands.add_parser("verify")
    restore.add_argument("archive")
    restore.add_argument("--key", required=True)
    restore.add_argument("--destination", required=True)
    restore.add_argument("--sha256", required=True)
    args = parser.parse_args()
    try:
        if args.command == "verify":
            verify(args.archive, args.key, args.destination, args.sha256)
        else:
            config = json.loads(Path(args.config).read_text())
            if args.command == "backup":
                backup(config)
            elif args.command == "serve":
                serve(config)
            else:
                result = health(config)
                print(json.dumps(result, sort_keys=True))
                return 0 if result["ok"] else 1
        return 0
    except Exception as exc:
        # Exception types are safe. Do not print environment values or private file data.
        print("operations failed: " + type(exc).__name__, file=__import__("sys").stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
