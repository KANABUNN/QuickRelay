# Quick Relay operations

This adds an optional operations endpoint and encrypted daily backups to the deployed relay. The Go relay and iOS app are unchanged. Live DMDATA/APNs acceptance and real iPhone delivery remain separate from operations acceptance.

## Health and external alerts

- `/readyz` verifies the database, configured APNs provider and recent live DMDATA frames. It supports GET and HEAD.
- `/ops/readyz` is served on loopback port 9081 and explicitly forwarded by Caddy. GET and HEAD return 200 only when all required checks pass; otherwise 503. It exposes booleans only, with `Cache-Control: no-store`.
- Operations checks: live readiness, at least 1 GiB and 10% free disk, successful backup no older than 30 hours, verified offsite copy, and an inactive alert drill.
- A failed backup immediately marks the backup state unhealthy, even if an earlier archive was successful. If the timer stops running entirely, freshness expiration detects it.
- Configure two external HTTPS monitors at five-minute intervals with email alerts, one for each endpoint. HTTP HEAD is supported for services whose free plan defaults to HEAD. HTTPS validation must remain enabled. Certificate-expiration advance warnings depend on the monitoring service's plan.
- The existing `quick-relay-health.timer` also checks readiness every minute and records failures in the journal. It is not an external notification channel.

The operations endpoint does **not** prove that APNs accepted a specific notification or that an iPhone displayed it. Review delivery metrics/logs and repeat device acceptance after distribution changes.

Caddy route inside the relay site, before the fallback handler:

```caddyfile
handle /ops/readyz {
    reverse_proxy 127.0.0.1:9081
}
```

## Backups

`operations.py backup` uses SQLite's online backup API, including committed WAL data, and checks database integrity. It bundles the snapshot, explicitly configured source files, and a SHA-256 manifest. GnuPG encrypts the archive to a full recipient fingerprint; only the public key is needed for scheduled backups. Plaintext staging is owner-only and removed on success or failure.

The example includes the database, application environment/APNs key directory, Caddy configuration, main relay unit, and the matching relay/APNs command binaries. Install the operations scripts and units from the same reviewed repository revision during a rebuild; these scripts and their dedicated transfer key are not part of the example application backup.

- Schedule: daily at 03:15 Asia/Tokyo, plus a randomized delay up to ten minutes; persistent timer catches a missed run after boot.
- Local archives: `/var/backups/quick-relay`, root-only, fourteen days.
- Offsite receiver: a private directory outside all web roots, thirty days.
- Pruning affects only this tool's exact archive-name pattern and runs after a successful new backup; unrelated and legacy backups are preserved.
- Recovery point target: at most approximately one daily interval of data loss if the VPS is lost. A full replacement-VPS recovery time is not established by the isolated startup drill.

`offsite.py` uses a separate SSH identity, strict host-key verification and a fixed receiver. It uploads the encrypted archive, then downloads it and checks the checksum. The main backup reports success only after this roundtrip succeeds. The default example requires offsite success; leaving the hook unset must not be reported as completed disaster recovery.

On the receiving host, `receiver.py` accepts only `put <archive> <sha256> <size>` and `get <archive>` for restricted names. It rejects shell commands, path traversal, truncated uploads, checksum mismatches and overwrites. Use an SSH authorized-key entry with `restrict`, a VPS source-address restriction, and this script as the forced command. Preserve and audit existing authorized keys before adding the dedicated entry. Do not copy the owner's general Xserver management key to the VPS.

The dedicated offsite JSON has `identity_file`, `known_hosts`, `port`, `user`, and `host` properties and is installed root-only at `/etc/quick-relay-ops/offsite.json`. Public operations configuration is `/etc/quick-relay-ops/config.json`; it must contain no secret values.

## Recovery acceptance and full recovery

Keep the recovery secret key in owner-only storage **outside the VPS**, and keep an additional offline copy separately from the archive storage. Losing this key makes the archives unrecoverable. Never commit recovery keys, transfer keys, application configuration or decrypted snapshots.

1. Retrieve an encrypted archive from the offsite host, and retain its recorded checksum.
2. On an isolated recovery host or private working directory, verify and decrypt to a **new**, nonexistent destination:

```sh
python3 operations.py verify ARCHIVE.tar.gz.gpg \
  --key /private/recovery-secret.asc \
  --destination /private/restore-new \
  --sha256 RECORDED_SHA256
```

3. `verify` rejects links, path escapes and duplicate entries; verifies every file against the manifest; then checks SQLite integrity and reports counts only.
4. Run `restore-smoke.py /private/restore-new` inside a systemd transient unit with `PrivateNetwork=yes`. It runs the archived binary against the restored database in `offline` mode with a new temporary pairing secret, checks local health, verifies that live readiness is false, and checks database integrity/foreign keys. No DMDATA or APNs credentials are supplied to the process.
5. Delete only the dedicated disposable plaintext drill directory after verification. Remove temporary secret-key copies/keyrings from the VPS after the off-host recovery key has been confirmed.

For a **real outage**, first preserve the failed instance/database and consult [deployment recovery](deployment.md). Rebuild the service account, compatible runtime, firewall and HTTPS routing; restore the archived binary, config/keys and database with the documented owners/modes. Restore while the relay is stopped, preserve/remove old WAL and SHM files consistently, check configuration, then enable live mode only after review. If restoring an older sequence, iOS clients must reset/re-pair so their cursors do not skip reports. Recreate operations units and the restricted offsite identity as needed. The isolated drill is not a full OS/disaster-recovery test.

## Operational checks

```sh
systemctl status quick-relay.service quick-relay-ops.service
systemctl list-timers quick-relay-health.timer quick-relay-backup.timer
journalctl -u quick-relay-backup.service --since today
cat /var/lib/quick-relay-ops/backup.json
```

For an approved notification drill, an administrator can atomically write `{"until": UNIX_TIME_PLUS_600}` to `/var/lib/quick-relay-ops/alert-drill.json`, readable by the operations service. The operations endpoint returns 503 for at most fifteen minutes, then recovers automatically. The live relay is left running. Verify the external service records both transitions and that the operator actually receives the messages. Do not claim message delivery from configuration alone.

## Validation

Linux/Python 3.11+ and GnuPG are required on the VPS; the constrained receiver supports Xserver's Python 3.6. Unit/integration tests use disposable keys and a temporary database:

```sh
python3 -m unittest discover -s deploy/operations -p 'test_*.py' -v
```

Coverage includes a committed WAL snapshot, encrypted roundtrip and private permissions, tamper rejection, failed backup/offsite state, archive traversal/link/overwrite rejection, and stale/offsite/live readiness/drill handling. Run `scripts/verify-deploy.sh` for systemd/Caddy validation. Live offsite SSH restrictions, HTTPS monitoring and email receipts require separate environment evidence.
