from __future__ import annotations

import importlib.util
import json
import os
import stat
import sys
import tempfile
import threading
import unittest
import urllib.error
import urllib.request
from dataclasses import replace
from pathlib import Path
from unittest.mock import patch

MODULE = Path(__file__).with_name("adapter.py")
LEGACY_FIXTURE = Path(__file__).with_name("testdata") / "legacy-stateless-v0.sql"
LEGACY_PERSIST_FIRST_FIXTURE = (
    Path(__file__).with_name("testdata") / "legacy-persist-first-v0.sql"
)
SPEC = importlib.util.spec_from_file_location("confenge_asaas_adapter", MODULE)
assert SPEC and SPEC.loader
adapter = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = adapter
SPEC.loader.exec_module(adapter)


def event(
    event_id: str, kind: str = "PAYMENT_CREATED", external: str = "corr-extra-2026w34"
):
    return adapter.minimized_event(
        {
            "id": event_id,
            "event": kind,
            "dateCreated": "2026-08-22T12:00:00Z",
            "payment": {
                "id": "pay_sandbox_extra_1",
                "customer": "cus_sandbox_extra_1",
                "externalReference": external,
                "status": "PENDING",
                "value": 10000,
                "billingType": "PIX",
                "description": "must not persist",
            },
            "customer": {"name": "must not persist", "email": "pii@example.test"},
        }
    )


class AdapterTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.root = Path(self.tmp.name)
        self.queue = adapter.Queue(self.root / "state" / "events.sqlite3")
        self.config = adapter.Config(
            db_path=self.queue.path,
            listen_host="127.0.0.1",
            listen_port=0,
            asaas_token="a" * 32,
            warmbly_url="http://127.0.0.1/never",
            warmbly_bearer_token="",
            warmbly_webhook_secret="w" * 32,
            warmbly_previous_secret="",
            max_attempts=2,
            base_backoff_seconds=1,
            max_backoff_seconds=2,
        )

    def tearDown(self):
        self.queue.close()
        self.tmp.cleanup()

    def test_minimizes_pii_and_persists_before_dedupe_ack(self):
        payload = event("evt_1")
        raw = json.dumps(payload)
        self.assertNotIn("description", raw)
        self.assertNotIn("pii@example.test", raw)
        first, _ = self.queue.persist(payload)
        second, _ = self.queue.persist(payload)
        self.assertTrue(first)
        self.assertFalse(second)
        self.assertEqual(self.queue.stats(1)["queue"]["pending"], 1)

        without_provider_time = {"id": "evt_without_time", "event": "PAYMENT_CREATED"}
        first_payload = adapter.minimized_event(without_provider_time)
        second_payload = adapter.minimized_event(without_provider_time)
        self.assertEqual(first_payload, second_payload)
        self.assertNotIn("dateCreated", first_payload)

    def test_minimization_drops_nested_or_oversized_selected_values(self):
        payload = adapter.minimized_event(
            {
                "id": "evt_malformed_selected",
                "event": "PAYMENT_CREATED",
                "payment": {
                    "id": {"email": "hidden@example.test"},
                    "customer": ["hidden@example.test"],
                    "status": "x" * (adapter.MAX_PROVIDER_TEXT_CHARS + 1),
                    "value": "8000.00",
                    "netValue": float("nan"),
                    "billingType": "PIX",
                },
            }
        )
        encoded = json.dumps(payload)
        self.assertNotIn("hidden", encoded)
        self.assertEqual(payload["payment"], {"billingType": "PIX"})

        with self.assertRaises(ValueError):
            adapter.minimized_event(
                {
                    "id": "x" * (adapter.MAX_PROVIDER_TEXT_CHARS + 1),
                    "event": "PAYMENT_CREATED",
                }
            )

    def test_semantic_hold_is_blocked_never_processed(self):
        self.queue.persist(event("evt_hold"))
        processor = adapter.Processor(self.queue, self.config)
        with patch.object(
            processor,
            "_send",
            return_value=(202, {"data": {"processed": False, "held": True}}),
        ):
            self.assertTrue(processor.process_one())
        stats = self.queue.stats(1)
        self.assertEqual(stats["queue"]["blocked"], 1)
        self.assertEqual(stats["queue"]["processed"], 0)
        self.assertEqual(stats["open_occurrences"], 1)

    def test_dry_run_is_durable_block_not_false_processing(self):
        self.queue.persist(event("evt_dry_run"))
        processor = adapter.Processor(self.queue, replace(self.config, dry_run=True))
        self.assertTrue(processor.process_one())
        row = self.queue.db.execute(
            "SELECT state,last_code,processed_at FROM events WHERE provider_event_id='evt_dry_run'"
        ).fetchone()
        self.assertEqual(
            (row["state"], row["last_code"]), ("blocked", "dry_run_not_forwarded")
        )
        self.assertIsNone(row["processed_at"])
        self.assertEqual(self.queue.stats(1)["open_occurrences"], 1)

    def test_invalid_warmbly_json_becomes_semantic_block(self):
        class InvalidJSONResponse:
            status = 202

            def __enter__(self):
                return self

            def __exit__(self, *_args):
                return False

            def read(self, _limit):
                return b"not-json"

        self.queue.persist(event("evt_invalid_json"))
        processor = adapter.Processor(self.queue, self.config)
        with patch.object(
            adapter.urllib.request, "urlopen", return_value=InvalidJSONResponse()
        ):
            self.assertTrue(processor.process_one())
        row = self.queue.db.execute(
            "SELECT state,last_code FROM events WHERE provider_event_id='evt_invalid_json'"
        ).fetchone()
        self.assertEqual(
            (row["state"], row["last_code"]), ("blocked", "warmbly_not_processed")
        )

    def test_http_authentication_and_persist_first_ack_contract(self):
        app = adapter.App(self.config)
        server = adapter.ThreadingHTTPServer(("127.0.0.1", 0), adapter.handler_for(app))
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        url = f"http://127.0.0.1:{server.server_address[1]}/api/v1/webhooks/asaas"
        health_url = url + "/health"
        body = json.dumps(
            {
                "id": "evt_http",
                "event": "PAYMENT_CREATED",
                "payment": {
                    "id": "pay_http",
                    "externalReference": "corr-http",
                    "value": 1,
                },
            }
        ).encode()
        try:
            with urllib.request.urlopen(health_url, timeout=2) as response:
                self.assertEqual(json.loads(response.read())["worker_status"], "OK")
            head = urllib.request.Request(health_url, method="HEAD")
            with urllib.request.urlopen(head, timeout=2) as response:
                self.assertEqual(response.status, 200)
                self.assertEqual(response.read(), b"")
            app.last_worker_error = "OperationalError"
            with self.assertRaises(urllib.error.HTTPError) as degraded:
                urllib.request.urlopen(health_url, timeout=2)
            self.assertEqual(degraded.exception.code, 503)
            app.last_worker_error = ""

            bad = urllib.request.Request(
                url,
                body,
                {"asaas-access-token": "wrong", "Content-Type": "application/json"},
            )
            with self.assertRaises(urllib.error.HTTPError) as caught:
                urllib.request.urlopen(bad, timeout=2)
            self.assertEqual(caught.exception.code, 401)
            good = urllib.request.Request(
                url,
                body,
                {"asaas-access-token": "a" * 32, "Content-Type": "application/json"},
            )
            with urllib.request.urlopen(good, timeout=2) as response:
                self.assertEqual(response.status, 200)
                self.assertTrue(json.loads(response.read())["accepted"])
            row = app.queue.db.execute(
                "SELECT state FROM events WHERE provider_event_id='evt_http'"
            ).fetchone()
            self.assertIsNotNone(row)
        finally:
            server.shutdown()
            server.server_close()
            app.close()

    def test_out_of_order_block_is_replayed_after_prerequisite(self):
        self.queue.persist(event("evt_received", "PAYMENT_RECEIVED"))
        processor = adapter.Processor(self.queue, self.config)
        with patch.object(
            processor,
            "_send",
            return_value=(202, {"data": {"processed": False, "held": True}}),
        ):
            processor.process_one()
        earlier = event("evt_created", "PAYMENT_CREATED")
        earlier["dateCreated"] = "2026-08-22T11:00:00Z"
        self.queue.persist(earlier)
        with patch.object(
            processor,
            "_send",
            return_value=(202, {"data": {"processed": True, "held": False}}),
        ):
            processor.process_one()
        row = self.queue.db.execute(
            "SELECT state,last_code FROM events WHERE provider_event_id='evt_received'"
        ).fetchone()
        self.assertEqual((row["state"], row["last_code"]), ("retry", "ordered_replay"))

    def test_retry_partial_failure_then_dead_is_actionable(self):
        self.queue.persist(event("evt_retry"))
        processor = adapter.Processor(self.queue, self.config)
        with patch.object(processor, "_send", return_value=(503, {})):
            processor.process_one()
        self.queue.db.execute("UPDATE events SET next_attempt_at=0")
        self.queue.db.commit()
        with patch.object(processor, "_send", return_value=(503, {})):
            processor.process_one()
        stats = self.queue.stats(1)
        self.assertEqual(stats["queue"]["dead"], 1)
        self.assertEqual(stats["open_occurrences"], 1)

    def test_restart_recovers_processing_lease(self):
        self.queue.persist(event("evt_restart"))
        self.queue.claim(120)
        self.queue.close()
        self.queue = adapter.Queue(self.root / "state" / "events.sqlite3")
        row = self.queue.db.execute(
            "SELECT state,last_code FROM events WHERE provider_event_id='evt_restart'"
        ).fetchone()
        self.assertEqual(
            (row["state"], row["last_code"]), ("retry", "restart_recovered")
        )

    def test_online_backup_restore_and_permissions(self):
        self.queue.persist(event("evt_backup"))
        before = tuple(self.queue.db.execute("SELECT * FROM events ORDER BY rowid"))
        backup = self.root / "backup" / "events.sqlite3"
        proof = self.queue.backup(backup)
        self.assertEqual(proof["queue_state_counts"]["pending"], 1)
        self.assertEqual(proof["sha256"], adapter.file_sha256(backup))
        self.assertEqual(
            tuple(self.queue.db.execute("SELECT * FROM events ORDER BY rowid")), before
        )
        self.assertEqual(
            self.queue.db.execute("SELECT COUNT(*) FROM backups").fetchone()[0], 0
        )
        restored = self.root / "restore" / "events.sqlite3"
        restored.parent.mkdir()
        Path(f"{restored}-wal").write_bytes(b"stale-wal")
        Path(f"{restored}-shm").write_bytes(b"stale-shm")
        adapter.restore_database(backup, restored)
        self.assertFalse(Path(f"{restored}-wal").exists())
        self.assertFalse(Path(f"{restored}-shm").exists())
        restored_queue = adapter.Queue(restored)
        try:
            self.assertEqual(restored_queue.stats(1)["queue"]["pending"], 1)
            adapter.assert_permissions(restored)
            self.assertEqual(stat.S_IMODE(restored.stat().st_mode), 0o600)
            self.assertEqual(stat.S_IMODE(restored.parent.stat().st_mode), 0o700)
        finally:
            restored_queue.close()

    def test_online_backup_does_not_recover_an_active_worker_lease(self):
        self.queue.persist(event("evt_active_backup"))
        self.queue.claim(120)
        observer = adapter.Queue(self.queue.path, recover_processing=False)
        try:
            observer.backup(self.root / "backup" / "active.sqlite3")
        finally:
            observer.close()
        row = self.queue.db.execute(
            "SELECT state,last_code FROM events WHERE provider_event_id='evt_active_backup'"
        ).fetchone()
        self.assertEqual(row["state"], "processing")
        self.assertIsNone(row["last_code"])
        self.assertEqual(
            self.queue.db.execute("SELECT COUNT(*) FROM backups").fetchone()[0], 0
        )

    def test_external_backup_receipt_keeps_health_fresh_without_a_queue_row(self):
        before = tuple(self.queue.db.execute("SELECT * FROM events ORDER BY rowid"))
        self.queue.backup_receipt_path.write_text(
            json.dumps(
                {
                    "format_version": adapter.BACKUP_RECEIPT_VERSION,
                    "created_at": adapter.utc_now(),
                    "archive_sha256": "a" * 64,
                    "queue_schema": adapter.CURRENT_QUEUE_SCHEMA,
                }
            ),
            encoding="utf-8",
        )

        self.assertEqual(self.queue.stats(60)["backup_status"], "FRESH")
        self.assertEqual(
            tuple(self.queue.db.execute("SELECT * FROM events ORDER BY rowid")), before
        )
        self.assertEqual(
            self.queue.db.execute("SELECT COUNT(*) FROM backups").fetchone()[0], 0
        )

    def test_legacy_fixture_reproduces_initialize_failure_but_backup_is_read_only(self):
        self.queue.close()
        fixture_sql = LEGACY_FIXTURE.read_text(encoding="utf-8")

        reproduced = self.root / "reproduced" / "events.sqlite3"
        reproduced.parent.mkdir()
        connection = adapter.sqlite3.connect(reproduced)
        connection.executescript(fixture_sql)
        connection.close()
        with self.assertRaisesRegex(
            adapter.sqlite3.OperationalError, "no such column: state"
        ):
            adapter.Queue(reproduced)

        legacy = self.root / "legacy" / "events.sqlite3"
        legacy.parent.mkdir()
        connection = adapter.sqlite3.connect(legacy)
        connection.executescript(fixture_sql)
        connection.close()
        before_bytes = legacy.read_bytes()
        before_stat = legacy.stat()
        connection = adapter.sqlite3.connect(legacy)
        before_rows = connection.execute("SELECT * FROM events").fetchall()
        before_tables = connection.execute(
            "SELECT name,sql FROM sqlite_schema WHERE type='table' ORDER BY name"
        ).fetchall()
        connection.close()

        preflight = adapter.inspect_queue_database(legacy)
        backup = self.root / "legacy-backup" / "events.sqlite3"
        proof = adapter.backup_database(legacy, backup)

        self.assertEqual(preflight["schema"], adapter.LEGACY_QUEUE_SCHEMA)
        self.assertEqual(proof["schema"], adapter.LEGACY_QUEUE_SCHEMA)
        self.assertEqual(proof["table_counts"], {"events": 1})
        self.assertEqual(legacy.read_bytes(), before_bytes)
        self.assertEqual(legacy.stat().st_mtime_ns, before_stat.st_mtime_ns)
        connection = adapter.sqlite3.connect(legacy)
        self.assertEqual(
            connection.execute("SELECT * FROM events").fetchall(), before_rows
        )
        self.assertEqual(
            connection.execute(
                "SELECT name,sql FROM sqlite_schema WHERE type='table' ORDER BY name"
            ).fetchall(),
            before_tables,
        )
        connection.close()

        restored = self.root / "legacy-restore" / "events.sqlite3"
        restore_proof = adapter.restore_database(backup, restored)
        self.assertEqual(restore_proof["schema"], adapter.LEGACY_QUEUE_SCHEMA)
        self.assertEqual(adapter.file_sha256(restored), adapter.file_sha256(backup))
        self.queue = adapter.Queue(self.root / "replacement" / "events.sqlite3")

    def test_live_legacy_persist_first_fixture_backup_is_read_only(self):
        legacy = self.root / "legacy-persist-first" / "events.sqlite3"
        legacy.parent.mkdir()
        connection = adapter.sqlite3.connect(legacy)
        connection.executescript(
            LEGACY_PERSIST_FIRST_FIXTURE.read_text(encoding="utf-8")
        )
        connection.close()
        before_bytes = legacy.read_bytes()
        before_stat = legacy.stat()

        preflight = adapter.inspect_queue_database(legacy)
        backup = self.root / "legacy-persist-first-backup" / "events.sqlite3"
        proof = adapter.backup_database(legacy, backup)

        self.assertEqual(preflight["schema"], adapter.LEGACY_PERSIST_FIRST_QUEUE_SCHEMA)
        self.assertEqual(proof["schema"], adapter.LEGACY_PERSIST_FIRST_QUEUE_SCHEMA)
        self.assertEqual(proof["table_counts"], {"metadata": 0, "events": 1})
        self.assertEqual(legacy.read_bytes(), before_bytes)
        self.assertEqual(legacy.stat().st_mtime_ns, before_stat.st_mtime_ns)

    def test_backup_preflight_fails_closed_for_future_schema_without_writing(self):
        self.queue.close()
        connection = adapter.sqlite3.connect(self.queue.path)
        connection.execute("UPDATE metadata SET value='2' WHERE key='schema_version'")
        connection.commit()
        connection.close()
        before = self.queue.path.read_bytes()

        with self.assertRaisesRegex(
            ValueError, "unsupported adapter queue schema version 2"
        ):
            adapter.inspect_queue_database(self.queue.path)

        self.assertEqual(self.queue.path.read_bytes(), before)
        self.queue = adapter.Queue(self.root / "replacement" / "events.sqlite3")

    def test_backup_preflight_does_not_create_a_missing_source(self):
        missing = self.root / "missing" / "events.sqlite3"
        with self.assertRaisesRegex(ValueError, "does not exist"):
            adapter.inspect_queue_database(missing)
        self.assertFalse(missing.exists())

    def test_queue_refuses_to_overwrite_a_different_schema_version(self):
        self.queue.close()
        connection = adapter.sqlite3.connect(self.queue.path)
        connection.execute("UPDATE metadata SET value='2' WHERE key='schema_version'")
        connection.commit()
        connection.close()

        with self.assertRaisesRegex(ValueError, "incompatible adapter queue schema 2"):
            adapter.Queue(self.queue.path)

        connection = adapter.sqlite3.connect(self.queue.path)
        stored = connection.execute(
            "SELECT value FROM metadata WHERE key='schema_version'"
        ).fetchone()[0]
        connection.close()
        self.assertEqual(stored, "2")
        self.queue = adapter.Queue(self.root / "replacement" / "events.sqlite3")

    def test_permissions_include_wal_and_shm(self):
        adapter.assert_permissions(self.queue.path)
        wal = Path(f"{self.queue.path}-wal")
        wal.touch(exist_ok=True)
        os.chmod(wal, 0o640)
        with self.assertRaises(ValueError):
            adapter.assert_permissions(self.queue.path)

    def test_config_fails_closed_before_binding_or_forwarding(self):
        valid = replace(
            self.config,
            listen_port=8791,
            warmbly_bearer_token="b" * 32,
            warmbly_url="http://127.0.0.1/v1/confenge/intel/commercial/provider-events",
        )
        valid.validate("serve")
        for invalid in (
            replace(valid, listen_host="0.0.0.0"),
            replace(valid, warmbly_webhook_secret=""),
            replace(valid, asaas_token="short"),
            replace(
                valid,
                warmbly_url="http://169.254.169.254/v1/confenge/intel/commercial/provider-events",
            ),
            replace(valid, max_attempts=0),
        ):
            with self.assertRaises(ValueError):
                invalid.validate("serve")


if __name__ == "__main__":
    unittest.main()
