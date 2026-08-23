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
from pathlib import Path
from unittest.mock import patch

MODULE = Path(__file__).with_name("adapter.py")
SPEC = importlib.util.spec_from_file_location("confenge_asaas_adapter", MODULE)
assert SPEC and SPEC.loader
adapter = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = adapter
SPEC.loader.exec_module(adapter)


def event(event_id: str, kind: str = "PAYMENT_CREATED", external: str = "corr-extra-2026w34"):
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

    def test_semantic_hold_is_blocked_never_processed(self):
        self.queue.persist(event("evt_hold"))
        processor = adapter.Processor(self.queue, self.config)
        with patch.object(processor, "_send", return_value=(202, {"data": {"processed": False, "held": True}})):
            self.assertTrue(processor.process_one())
        stats = self.queue.stats(1)
        self.assertEqual(stats["queue"]["blocked"], 1)
        self.assertEqual(stats["queue"]["processed"], 0)
        self.assertEqual(stats["open_occurrences"], 1)

    def test_http_authentication_and_persist_first_ack_contract(self):
        app = adapter.App(self.config)
        server = adapter.ThreadingHTTPServer(("127.0.0.1", 0), adapter.handler_for(app))
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        url = f"http://127.0.0.1:{server.server_address[1]}/api/v1/webhooks/asaas"
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
            bad = urllib.request.Request(
                url, body, {"asaas-access-token": "wrong", "Content-Type": "application/json"}
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
        with patch.object(processor, "_send", return_value=(202, {"data": {"processed": False, "held": True}})):
            processor.process_one()
        earlier = event("evt_created", "PAYMENT_CREATED")
        earlier["dateCreated"] = "2026-08-22T11:00:00Z"
        self.queue.persist(earlier)
        with patch.object(processor, "_send", return_value=(202, {"data": {"processed": True, "held": False}})):
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
        self.assertEqual((row["state"], row["last_code"]), ("retry", "restart_recovered"))

    def test_online_backup_restore_and_permissions(self):
        self.queue.persist(event("evt_backup"))
        backup = self.root / "backup" / "events.sqlite3"
        proof = self.queue.backup(backup)
        self.assertEqual(proof["counts"]["pending"], 1)
        restored = self.root / "restore" / "events.sqlite3"
        adapter.restore_database(backup, restored)
        restored_queue = adapter.Queue(restored)
        try:
            self.assertEqual(restored_queue.stats(1)["queue"]["pending"], 1)
            adapter.assert_permissions(restored)
            self.assertEqual(stat.S_IMODE(restored.stat().st_mode), 0o600)
            self.assertEqual(stat.S_IMODE(restored.parent.stat().st_mode), 0o700)
        finally:
            restored_queue.close()


if __name__ == "__main__":
    unittest.main()
