import contextlib
import io
import json
import os
from pathlib import Path
import sqlite3
import tarfile
import tempfile
import time
import unittest
from unittest.mock import patch

import operations as ops


class OperationsTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.shared = tempfile.TemporaryDirectory(prefix="quick-relay-test-")
        cls.root = Path(cls.shared.name)
        cls.home = cls.root / "keyring"
        cls.home.mkdir(mode=0o700)
        base = ["gpg", "--batch", "--homedir", str(cls.home)]
        ops.run(base + ["--pinentry-mode", "loopback", "--passphrase", "",
                        "--quick-generate-key", "Disposable Operations Test", "rsa2048", "encrypt", "0"])
        listing = ops.run(base + ["--with-colons", "--list-keys"]).decode()
        cls.fingerprint = next(line.split(":")[9] for line in listing.splitlines() if line.startswith("fpr:"))
        cls.public = cls.root / "public.asc"
        cls.secret = cls.root / "secret.asc"
        cls.public.write_bytes(ops.run(base + ["--armor", "--export", cls.fingerprint]))
        cls.secret.write_bytes(ops.run(base + ["--armor", "--export-secret-keys", cls.fingerprint]))

    @classmethod
    def tearDownClass(cls):
        ops.run(["gpgconf", "--homedir", str(cls.home), "--kill", "all"])
        cls.shared.cleanup()

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(dir=self.root)
        self.addCleanup(self.temp.cleanup)
        self.work = Path(self.temp.name)
        self.db = self.work / "live.sqlite3"
        with sqlite3.connect(self.db) as db:
            db.execute("PRAGMA journal_mode=WAL")
            for table in ("devices", "reports", "deliveries"):
                db.execute("CREATE TABLE " + table + " (id integer primary key, data text)")
                db.execute("INSERT INTO " + table + " (data) VALUES ('preserved')")
        self.private = self.work / "private.env"
        self.private.write_text("PAIRING_SECRET=not-a-real-secret\n")
        self.config = {"database": str(self.db), "sources": {str(self.private): "config/relay.env"},
                       "public_key": str(self.public), "recipient": self.fingerprint,
                       "archives": str(self.work / "archives"), "state_dir": str(self.work / "state"),
                       "readiness_url": "http://127.0.0.1:8080/readyz", "disk_path": "/"}

    def make_backup(self):
        with contextlib.redirect_stdout(io.StringIO()):
            return ops.backup(self.config)

    def test_encrypted_roundtrip_preserves_snapshot_and_secrets(self):
        # Keep a WAL writer open: copying only the live database file would miss this row.
        with sqlite3.connect(self.db) as db:
            db.execute("PRAGMA wal_autocheckpoint=0")
            db.execute("INSERT INTO reports(data) VALUES ('in WAL')")
            db.commit()
            archive = self.make_backup()
            with contextlib.redirect_stdout(io.StringIO()):
                counts = ops.verify(archive, self.secret, self.work / "restored", ops.digest(archive))
        self.assertEqual(counts, {"devices": 1, "reports": 2, "deliveries": 1})
        self.assertEqual((self.work / "restored/config/relay.env").read_bytes(), self.private.read_bytes())
        self.assertNotIn(b"PAIRING_SECRET", archive.read_bytes())
        self.assertEqual(archive.stat().st_mode & 0o777, 0o600)
        self.assertFalse(any((self.work / "archives").glob(".backup-*")))

    def test_modified_encrypted_archive_is_rejected(self):
        archive = self.make_backup()
        original_hash = ops.digest(archive)
        data = bytearray(archive.read_bytes())
        data[len(data) // 2] ^= 128
        archive.write_bytes(data)
        with self.assertRaises(ops.OperationError):
            ops.verify(archive, self.secret, self.work / "bad-hash", original_hash)
        with self.assertRaises(ops.OperationError):
            ops.verify(archive, self.secret, self.work / "bad-cipher", ops.digest(archive))
        self.assertFalse((self.work / "bad-cipher").exists())

    def test_failed_backup_cannot_report_previous_success(self):
        archive = self.make_backup()
        self.config["database"] = str(self.work / "missing.db")
        with self.assertRaises(sqlite3.OperationalError):
            self.make_backup()
        self.assertTrue(archive.exists())
        self.assertFalse(ops.read_json(self.work / "state/backup.json")["ok"])

    def test_failed_offsite_transfer_is_failure_and_retains_local_archive(self):
        self.config["offsite_hook"] = "/bin/false"
        with self.assertRaises(ops.OperationError):
            self.make_backup()
        self.assertEqual(len(list((self.work / "archives").glob("*.gpg"))), 1)
        self.assertFalse(ops.read_json(self.work / "state/backup.json")["ok"])

    def test_rejects_archive_paths_links_and_overwrite(self):
        for i, name in enumerate(("../escape", "/absolute", "symlink")):
            archive = self.work / (str(i) + ".tar.gz")
            with tarfile.open(archive, "w:gz") as tar:
                info = tarfile.TarInfo(name)
                if name == "symlink":
                    info.type = tarfile.SYMTYPE
                    info.linkname = "/etc/passwd"
                tar.addfile(info)
            with self.assertRaises(ops.OperationError):
                ops.unpack_verified(archive, self.work / ("rejected" + str(i)))
        with self.assertRaises(ops.OperationError):
            ops.unpack_verified(archive, self.work)
        self.assertFalse((self.work / "escape").exists())

    def test_health_requires_fresh_backup_offsite_and_live_readiness(self):
        self.make_backup()
        now = time.time()
        good = {"ok": True, "db": True, "apns_configured": True, "source_connected": True}
        def response(*a, **k):
            return io.BytesIO(json.dumps(good).encode())
        with patch("operations.urllib.request.urlopen", side_effect=response), \
             patch("operations.shutil.disk_usage", return_value=shutil_usage()):
            self.assertFalse(ops.health(self.config, now)["ok"])
            state = ops.read_json(self.work / "state/backup.json")
            state["offsite_ok"] = True
            ops.atomic_json(self.work / "state/backup.json", state)
            self.assertTrue(ops.health(self.config, now)["ok"])
            self.assertFalse(ops.health(self.config, now + 31 * 3600)["ok"])
            ops.atomic_json(self.work / "state/alert-drill.json", {"until": now + 600})
            self.assertFalse(ops.health(self.config, now)["ok"])
            self.assertTrue(ops.health(self.config, now + 601)["ok"])
            good["source_connected"] = False
            self.assertFalse(ops.health(self.config, now + 601)["ok"])


def shutil_usage():
    from collections import namedtuple
    return namedtuple("usage", "total used free")(10 * 1024**3, 1024**3, 9 * 1024**3)


if __name__ == "__main__":
    unittest.main(verbosity=2)
