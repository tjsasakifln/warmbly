from __future__ import annotations

import hashlib
import json
import os
import sqlite3
import stat
import subprocess
import tarfile
import tempfile
import unittest
from pathlib import Path

PACK = Path(__file__).resolve().parent
ROOT = PACK.parent.parent
ADAPTER = PACK / "asaas-adapter" / "adapter.py"
LEGACY_FIXTURE = PACK / "asaas-adapter" / "testdata" / "legacy-stateless-v0.sql"


class BackupScriptTest(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory(prefix="warmbly-backup-test-")
        self.root = Path(self.temporary.name)
        self.bin = self.root / "bin"
        self.bin.mkdir()
        self.docker_log = self.root / "docker.log"
        docker = self.bin / "docker"
        docker.write_text(
            """#!/usr/bin/env bash
set -euo pipefail
printf '%s\\n' "$*" >>"$FAKE_DOCKER_LOG"
case " $* " in
  *" pg_dump --version "*)
    echo "pg_dump (PostgreSQL) 16.10"
    ;;
  *" pg_dump "*)
    printf '%s\\n' \
      '-- fixture PostgreSQL dump' \
      'CREATE TABLE backup_restore_marker (value text PRIMARY KEY);' \
      "INSERT INTO backup_restore_marker VALUES ('fixture-186');" \
      '-- PostgreSQL database dump complete'
    ;;
  *" pg_isready "*)
    ;;
  *" -Atqc SELECT count(*) FROM pg_tables "*)
    echo "0"
    ;;
  *" -Atqc SELECT current_database() "*)
    previous=""
    for argument in "$@"; do
      if [[ "$previous" == "-d" ]]; then
        echo "$argument"
        break
      fi
      previous="$argument"
    done
    ;;
  *" psql "*)
    grep -q 'backup_restore_marker'
    ;;
  *)
    echo "unexpected fake docker invocation" >&2
    exit 1
    ;;
esac
""",
            encoding="utf-8",
        )
        docker.chmod(0o755)
        self.environment_file = self.root / "vps.env"
        self.environment_file.write_text(
            """COMPOSE_PROJECT_NAME=warmbly-confenge-test
AUTH_SECRET=fixture-secret-never-archive
INTERNAL_API_TOKEN=fixture-token-never-archive
KMS_LOCAL_MASTER_KEY=fixture-kms-never-archive
CREDENTIALS_ENCRYPTION_KEY=fixture-credentials-never-archive
CONFENGE_OUTCOME_WEBHOOK_SECRET=fixture-webhook-never-archive
PRIMARY_DB=postgres://fixture:fixture-password@postgres/db
CONFENGE_MAILBOX_EMAIL=operator-fixture@example.invalid
CONFENGE_OPERATOR_USER_ID=11111111-0000-0000-0000-000000000001
PUBLIC_HOST=127.0.0.1
PUBLIC_CALLBACK_URL=https://example.invalid/callback?token=fixture-query-never-archive
""",
            encoding="utf-8",
        )

    def tearDown(self) -> None:
        self.temporary.cleanup()

    def environment(self, database: Path, backup_dir: Path) -> dict[str, str]:
        environment = os.environ.copy()
        environment.update(
            {
                "PATH": f"{self.bin}:{environment['PATH']}",
                "FAKE_DOCKER_LOG": str(self.docker_log),
                "CONFENGE_VPS_ENV": str(self.environment_file),
                "CONFENGE_BACKUP_DIR": str(backup_dir),
                "CONFENGE_SECRETS_BACKUP_DIR": str(backup_dir / "secrets"),
                "ASAAS_ADAPTER_DB": str(database),
            }
        )
        return environment

    @staticmethod
    def create_legacy_database(path: Path) -> None:
        path.parent.mkdir(parents=True)
        database = sqlite3.connect(path)
        database.executescript(LEGACY_FIXTURE.read_text(encoding="utf-8"))
        database.close()

    def test_canonical_backup_and_isolated_restore_preserve_legacy_queue(self) -> None:
        database = self.root / "live-copy" / "events.sqlite3"
        backup_dir = self.root / "backups"
        self.create_legacy_database(database)
        before_bytes = database.read_bytes()
        before_stat = database.stat()
        connection = sqlite3.connect(database)
        before_rows = connection.execute("SELECT * FROM events").fetchall()
        connection.close()

        result = subprocess.run(
            ["bash", str(PACK / "backup.sh")],
            cwd=ROOT,
            env=self.environment(database, backup_dir),
            capture_output=True,
            text=True,
            timeout=30,
            check=False,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("Preflighting Asaas queue schema (read-only)", result.stdout)
        self.assertEqual(database.read_bytes(), before_bytes)
        self.assertEqual(database.stat().st_mtime_ns, before_stat.st_mtime_ns)
        connection = sqlite3.connect(database)
        self.assertEqual(
            connection.execute("SELECT * FROM events").fetchall(), before_rows
        )
        self.assertEqual(
            [
                row[0]
                for row in connection.execute(
                    "SELECT name FROM sqlite_schema WHERE type='table' ORDER BY name"
                )
            ],
            ["events"],
        )
        connection.close()
        receipt = Path(f"{database}.backup-receipt.json")
        self.assertTrue(receipt.is_file())
        receipt_data = json.loads(receipt.read_text(encoding="utf-8"))
        self.assertEqual(
            receipt_data["format_version"], "confenge.asaas-backup-receipt.v1"
        )
        self.assertNotIn(str(database), receipt.read_text(encoding="utf-8"))

        archives = list(backup_dir.glob("warmbly-confenge-*.tar.gz"))
        self.assertEqual(len(archives), 1)
        archive = archives[0]
        self.assertTrue(Path(f"{archive}.manifest.json").is_file())
        self.assertTrue(Path(f"{archive}.sha256").is_file())
        expected_archive_hash = Path(f"{archive}.sha256").read_text().split()[0]
        self.assertEqual(
            expected_archive_hash, hashlib.sha256(archive.read_bytes()).hexdigest()
        )

        with tarfile.open(archive, "r:gz") as bundle:
            members = {
                member.name.removeprefix("./"): bundle.extractfile(member).read()
                for member in bundle.getmembers()
                if member.isfile()
            }
        self.assertIn("MANIFEST.json", members)
        self.assertIn("SHA256SUMS", members)
        self.assertIn("asaas-events.sqlite3", members)
        manifest = json.loads(members["MANIFEST.json"])
        self.assertEqual(manifest["format_version"], "confenge.backup-manifest.v2")
        self.assertEqual(
            manifest["components"]["asaas_queue"]["schema"],
            "confenge.asaas-queue.legacy-stateless-v0",
        )
        self.assertEqual(
            manifest["components"]["asaas_queue"]["table_counts"], {"events": 1}
        )
        for checksum_line in members["SHA256SUMS"].decode().splitlines():
            digest, name = checksum_line.split("  ", 1)
            self.assertEqual(hashlib.sha256(members[name]).hexdigest(), digest)

        forbidden = (
            b"fixture-secret-never-archive",
            b"fixture-token-never-archive",
            b"fixture-password",
            b"fixture-query-never-archive",
            b"operator-fixture@example.invalid",
        )
        public_metadata = members["MANIFEST.json"] + members["env.redacted"]
        for value in forbidden:
            self.assertNotIn(value, public_metadata)
        self.assertIn(b"PRIMARY_DB=***REDACTED***", members["env.redacted"])
        self.assertIn(b"CONFENGE_MAILBOX_EMAIL=***REDACTED***", members["env.redacted"])
        self.assertIn(b"PUBLIC_CALLBACK_URL=***REDACTED***", members["env.redacted"])

        secret_bundles = list((backup_dir / "secrets").glob("keys-*.env"))
        self.assertEqual(len(secret_bundles), 1)
        self.assertEqual(stat.S_IMODE(secret_bundles[0].stat().st_mode), 0o600)
        self.assertIn("fixture-secret-never-archive", secret_bundles[0].read_text())

        restored = self.root / "isolated" / "asaas-events.sqlite3"
        restore_environment = self.environment(database, backup_dir)
        restore_environment.update(
            {
                "CONFENGE_RESTORE_DATABASE": "warmbly_restore_verify_186",
                "CONFENGE_RESTORE_ASAAS_DB": str(restored),
            }
        )
        restored_result = subprocess.run(
            ["bash", str(PACK / "restore.sh"), str(archive)],
            cwd=ROOT,
            env=restore_environment,
            capture_output=True,
            text=True,
            timeout=30,
            check=False,
        )
        self.assertEqual(restored_result.returncode, 0, restored_result.stderr)
        self.assertIn(
            "RESTORE_PROOF=PASS postgres_database=warmbly_restore_verify_186",
            restored_result.stdout,
        )
        self.assertEqual(
            hashlib.sha256(restored.read_bytes()).hexdigest(),
            hashlib.sha256(members["asaas-events.sqlite3"]).hexdigest(),
        )
        connection = sqlite3.connect(restored)
        self.assertEqual(
            connection.execute("SELECT COUNT(*) FROM events").fetchone()[0], 1
        )
        connection.close()
        self.assertEqual(database.read_bytes(), before_bytes)

    def test_unknown_schema_fails_before_postgres_dump(self) -> None:
        database = self.root / "future" / "events.sqlite3"
        database.parent.mkdir()
        connection = sqlite3.connect(database)
        connection.executescript(
            """
            CREATE TABLE metadata (key TEXT PRIMARY KEY, value TEXT NOT NULL);
            INSERT INTO metadata VALUES ('schema_version', '2');
            CREATE TABLE events (
                provider_event_id TEXT PRIMARY KEY,
                payload TEXT NOT NULL,
                state TEXT NOT NULL
            );
            """
        )
        connection.close()
        before = database.read_bytes()

        result = subprocess.run(
            ["bash", str(PACK / "backup.sh")],
            cwd=ROOT,
            env=self.environment(database, self.root / "future-backups"),
            capture_output=True,
            text=True,
            timeout=30,
            check=False,
        )

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("unsupported adapter queue schema version 2", result.stderr)
        self.assertFalse(self.docker_log.exists())
        self.assertEqual(database.read_bytes(), before)


if __name__ == "__main__":
    unittest.main()
