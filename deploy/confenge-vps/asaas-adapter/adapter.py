#!/usr/bin/env python3
"""Persist-first Asaas webhook edge for the CONFENGE commercial consumer.

The adapter owns transport recovery only. Warmbly owns commercial actions and
outcomes; Asaas remains the financial authority. Stored payloads are minimized
to provider identifiers and financial state before the HTTP 200 ACK.
"""
from __future__ import annotations

import argparse
import contextlib
import hashlib
import hmac
import json
import os
import shutil
import sqlite3
import stat
import tempfile
import threading
import time
import urllib.error
import urllib.request
from dataclasses import dataclass
from datetime import datetime, timezone
from http import HTTPStatus
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from typing import Any

SCHEMA_VERSION = 1
QUEUE_STATES = ("pending", "processing", "retry", "blocked", "dead", "processed")
RETRYABLE_HTTP = {408, 425, 429, 500, 502, 503, 504}
MAX_BODY_BYTES = 256 * 1024


def utc_now() -> str:
    return datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")


def _text(value: Any) -> str:
    return value.strip() if isinstance(value, str) else ""


def _object(value: Any) -> dict[str, Any]:
    return value if isinstance(value, dict) else {}


def minimized_event(raw: dict[str, Any]) -> dict[str, Any]:
    """Keep only fields required for reconciliation, never customer PII."""
    payment = _object(raw.get("payment"))
    subscription = _object(raw.get("subscription"))
    checkout = _object(raw.get("checkout"))
    event_id = _text(raw.get("id"))
    event_type = _text(raw.get("event"))
    if not event_id or not event_type:
        raise ValueError("Asaas id and event are required")
    external_ref = (
        _text(raw.get("externalReference"))
        or _text(payment.get("externalReference"))
        or _text(subscription.get("externalReference"))
        or _text(checkout.get("externalReference"))
    )
    result: dict[str, Any] = {
        "id": event_id,
        "event": event_type,
        "dateCreated": _text(raw.get("dateCreated")) or utc_now(),
    }
    if external_ref:
        result["externalReference"] = external_ref
    if payment:
        result["payment"] = {
            key: payment[key]
            for key in (
                "id",
                "customer",
                "subscription",
                "checkoutSession",
                "externalReference",
                "status",
                "value",
                "netValue",
                "billingType",
                "paymentDate",
                "confirmedDate",
                "clientPaymentDate",
            )
            if key in payment and payment[key] is not None
        }
    if subscription:
        result["subscription"] = {
            key: subscription[key]
            for key in ("id", "customer", "externalReference", "status")
            if key in subscription and subscription[key] is not None
        }
    if checkout:
        result["checkout"] = {
            key: checkout[key]
            for key in ("id", "externalReference", "status")
            if key in checkout and checkout[key] is not None
        }
    return result


def correlation_of(payload: dict[str, Any]) -> str:
    payment = _object(payload.get("payment"))
    return (
        _text(payload.get("externalReference"))
        or _text(payment.get("externalReference"))
        or _text(payment.get("id"))
        or _text(payload.get("id"))
    )


@dataclass(frozen=True)
class Config:
    db_path: Path
    listen_host: str
    listen_port: int
    asaas_token: str
    warmbly_url: str
    warmbly_bearer_token: str
    warmbly_webhook_secret: str
    warmbly_previous_secret: str
    max_attempts: int = 8
    base_backoff_seconds: int = 5
    max_backoff_seconds: int = 900
    processing_lease_seconds: int = 120
    processed_retention_days: int = 45
    backup_max_age_seconds: int = 26 * 60 * 60
    dry_run: bool = False

    @classmethod
    def from_env(cls) -> "Config":
        state = Path(os.getenv("ASAAS_ADAPTER_STATE_DIR", "/var/lib/confenge-asaas-adapter"))
        return cls(
            db_path=Path(os.getenv("ASAAS_ADAPTER_DB", str(state / "events.sqlite3"))),
            listen_host=os.getenv("ASAAS_ADAPTER_HOST", "127.0.0.1"),
            listen_port=int(os.getenv("ASAAS_ADAPTER_PORT", "8791")),
            asaas_token=os.getenv("ASAAS_WEBHOOK_TOKEN", ""),
            warmbly_url=os.getenv(
                "ASAAS_ADAPTER_WARMBLY_URL",
                "http://127.0.0.1:8080/v1/confenge/intel/commercial/provider-events",
            ),
            warmbly_bearer_token=os.getenv("ASAAS_ADAPTER_WARMBLY_BEARER_TOKEN", ""),
            warmbly_webhook_secret=os.getenv("ASAAS_ADAPTER_WARMBLY_WEBHOOK_SECRET", ""),
            warmbly_previous_secret=os.getenv("ASAAS_ADAPTER_WARMBLY_WEBHOOK_SECRET_PREVIOUS", ""),
            max_attempts=int(os.getenv("ASAAS_ADAPTER_MAX_ATTEMPTS", "8")),
            base_backoff_seconds=int(os.getenv("ASAAS_ADAPTER_BASE_BACKOFF_SECONDS", "5")),
            max_backoff_seconds=int(os.getenv("ASAAS_ADAPTER_MAX_BACKOFF_SECONDS", "900")),
            processing_lease_seconds=int(os.getenv("ASAAS_ADAPTER_PROCESSING_LEASE_SECONDS", "120")),
            processed_retention_days=int(os.getenv("ASAAS_ADAPTER_PROCESSED_RETENTION_DAYS", "45")),
            backup_max_age_seconds=int(os.getenv("ASAAS_ADAPTER_BACKUP_MAX_AGE_SECONDS", "93600")),
            dry_run=os.getenv("ASAAS_ADAPTER_DRY_RUN", "false").lower() == "true",
        )


class Queue:
    def __init__(self, path: Path):
        self.path = path
        path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
        os.chmod(path.parent, 0o700)
        self.db = sqlite3.connect(path, timeout=30, check_same_thread=False)
        self.db.row_factory = sqlite3.Row
        self.lock = threading.RLock()
        self._initialize()
        os.chmod(path, 0o600)

    def _initialize(self) -> None:
        with self.lock, self.db:
            self.db.execute("PRAGMA journal_mode=WAL")
            self.db.execute("PRAGMA synchronous=FULL")
            self.db.executescript(
                """
                CREATE TABLE IF NOT EXISTS metadata (
                    key TEXT PRIMARY KEY,
                    value TEXT NOT NULL
                );
                CREATE TABLE IF NOT EXISTS events (
                    provider_event_id TEXT PRIMARY KEY,
                    correlation_id TEXT NOT NULL,
                    event_type TEXT NOT NULL,
                    occurred_at TEXT NOT NULL,
                    received_at TEXT NOT NULL,
                    payload TEXT NOT NULL,
                    payload_sha256 TEXT NOT NULL,
                    state TEXT NOT NULL CHECK(state IN
                      ('pending','processing','retry','blocked','dead','processed')),
                    attempts INTEGER NOT NULL DEFAULT 0,
                    next_attempt_at REAL NOT NULL DEFAULT 0,
                    lease_until REAL,
                    last_http_status INTEGER,
                    last_code TEXT,
                    updated_at TEXT NOT NULL,
                    processed_at TEXT
                );
                CREATE INDEX IF NOT EXISTS events_ready_idx
                    ON events(state, next_attempt_at, occurred_at, received_at);
                CREATE INDEX IF NOT EXISTS events_correlation_idx
                    ON events(correlation_id, state, occurred_at);
                CREATE TABLE IF NOT EXISTS occurrences (
                    id INTEGER PRIMARY KEY AUTOINCREMENT,
                    provider_event_id TEXT NOT NULL,
                    correlation_id TEXT NOT NULL,
                    code TEXT NOT NULL,
                    owner TEXT NOT NULL,
                    next_action TEXT NOT NULL,
                    state TEXT NOT NULL DEFAULT 'open',
                    opened_at TEXT NOT NULL,
                    detail TEXT NOT NULL,
                    UNIQUE(provider_event_id, code, state)
                );
                CREATE TABLE IF NOT EXISTS backups (
                    path TEXT PRIMARY KEY,
                    created_at TEXT NOT NULL,
                    sha256 TEXT NOT NULL
                );
                """
            )
            self.db.execute(
                "INSERT OR REPLACE INTO metadata(key,value) VALUES('schema_version',?)",
                (str(SCHEMA_VERSION),),
            )
            self.db.execute(
                "UPDATE events SET state='retry', lease_until=NULL, next_attempt_at=0, "
                "last_code='restart_recovered' WHERE state='processing'"
            )

    def close(self) -> None:
        with self.lock:
            self.db.close()

    def persist(self, payload: dict[str, Any]) -> tuple[bool, str]:
        event_id = _text(payload.get("id"))
        encoded = json.dumps(payload, sort_keys=True, separators=(",", ":"))
        digest = hashlib.sha256(encoded.encode()).hexdigest()
        now = utc_now()
        with self.lock, self.db:
            cur = self.db.execute(
                """INSERT OR IGNORE INTO events(
                     provider_event_id,correlation_id,event_type,occurred_at,received_at,
                     payload,payload_sha256,state,next_attempt_at,updated_at)
                   VALUES(?,?,?,?,?,?,?,'pending',0,?)""",
                (
                    event_id,
                    correlation_of(payload),
                    _text(payload.get("event")),
                    _text(payload.get("dateCreated")) or now,
                    now,
                    encoded,
                    digest,
                    now,
                ),
            )
            existing = self.db.execute(
                "SELECT payload_sha256 FROM events WHERE provider_event_id=?", (event_id,)
            ).fetchone()
            if existing and existing["payload_sha256"] != digest:
                self.occurrence(
                    event_id,
                    correlation_of(payload),
                    "duplicate_payload_conflict",
                    "finance-ops",
                    "compare the provider webhook log; keep the first durable payload",
                    "same provider event id arrived with different minimized content",
                )
            return cur.rowcount == 1, event_id

    def claim(self, lease_seconds: int) -> sqlite3.Row | None:
        now_epoch = time.time()
        with self.lock, self.db:
            self.db.execute(
                "UPDATE events SET state='retry', lease_until=NULL, next_attempt_at=0, "
                "last_code='lease_recovered' WHERE state='processing' AND lease_until<?",
                (now_epoch,),
            )
            row = self.db.execute(
                """SELECT * FROM events
                   WHERE state IN ('pending','retry') AND next_attempt_at<=?
                   ORDER BY occurred_at, received_at LIMIT 1""",
                (now_epoch,),
            ).fetchone()
            if row is None:
                return None
            self.db.execute(
                "UPDATE events SET state='processing', attempts=attempts+1, lease_until=?, "
                "updated_at=? WHERE provider_event_id=?",
                (now_epoch + lease_seconds, utc_now(), row["provider_event_id"]),
            )
            return self.db.execute(
                "SELECT * FROM events WHERE provider_event_id=?", (row["provider_event_id"],)
            ).fetchone()

    def transition(
        self,
        event_id: str,
        state: str,
        *,
        code: str,
        http_status: int | None,
        next_attempt_at: float = 0,
    ) -> None:
        if state not in QUEUE_STATES:
            raise ValueError(f"invalid queue state {state}")
        processed_at = utc_now() if state == "processed" else None
        with self.lock, self.db:
            self.db.execute(
                """UPDATE events SET state=?, next_attempt_at=?, lease_until=NULL,
                   last_http_status=?, last_code=?, updated_at=?, processed_at=COALESCE(?,processed_at)
                   WHERE provider_event_id=?""",
                (state, next_attempt_at, http_status, code, utc_now(), processed_at, event_id),
            )

    def requeue_blocked(self, correlation_id: str, except_id: str) -> None:
        with self.lock, self.db:
            self.db.execute(
                """UPDATE events SET state='retry', next_attempt_at=0, last_code='ordered_replay',
                   updated_at=? WHERE correlation_id=? AND provider_event_id<>? AND state='blocked'""",
                (utc_now(), correlation_id, except_id),
            )

    def occurrence(
        self,
        event_id: str,
        correlation_id: str,
        code: str,
        owner: str,
        next_action: str,
        detail: str,
    ) -> None:
        with self.lock, self.db:
            self.db.execute(
                """INSERT OR IGNORE INTO occurrences(
                   provider_event_id,correlation_id,code,owner,next_action,opened_at,detail)
                   VALUES(?,?,?,?,?,?,?)""",
                (event_id, correlation_id, code, owner, next_action, utc_now(), detail[:500]),
            )

    def stats(self, backup_max_age_seconds: int) -> dict[str, Any]:
        with self.lock:
            counts = {state: 0 for state in QUEUE_STATES}
            for row in self.db.execute("SELECT state,COUNT(*) n FROM events GROUP BY state"):
                counts[row["state"]] = row["n"]
            oldest = self.db.execute(
                "SELECT received_at FROM events WHERE state<>'processed' ORDER BY received_at LIMIT 1"
            ).fetchone()
            open_occ = self.db.execute(
                "SELECT COUNT(*) n FROM occurrences WHERE state='open'"
            ).fetchone()["n"]
            last_backup = self.db.execute(
                "SELECT created_at FROM backups ORDER BY created_at DESC LIMIT 1"
            ).fetchone()
        backup_age: int | None = None
        if last_backup:
            stamp = datetime.fromisoformat(last_backup["created_at"].replace("Z", "+00:00"))
            backup_age = max(0, int((datetime.now(timezone.utc) - stamp).total_seconds()))
        oldest_age: int | None = None
        if oldest:
            stamp = datetime.fromisoformat(oldest["received_at"].replace("Z", "+00:00"))
            oldest_age = max(0, int((datetime.now(timezone.utc) - stamp).total_seconds()))
        return {
            "schema_version": "confenge.asaas-adapter-health.v1",
            "queue": counts,
            "oldest_backlog_age_seconds": oldest_age,
            "open_occurrences": open_occ,
            "backup_age_seconds": backup_age,
            "backup_status": (
                "UNKNOWN" if backup_age is None else "STALE" if backup_age > backup_max_age_seconds else "FRESH"
            ),
        }

    def prune(self, retention_days: int) -> int:
        cutoff = time.time() - retention_days * 86400
        with self.lock, self.db:
            cur = self.db.execute(
                "DELETE FROM events WHERE state='processed' AND unixepoch(processed_at)<?", (cutoff,)
            )
            return cur.rowcount

    def backup(self, destination: Path) -> dict[str, Any]:
        destination.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
        with self.lock:
            target = sqlite3.connect(destination)
            try:
                self.db.backup(target)
            finally:
                target.close()
        os.chmod(destination, 0o600)
        digest = hashlib.sha256(destination.read_bytes()).hexdigest()
        with self.lock, self.db:
            self.db.execute(
                "INSERT OR REPLACE INTO backups(path,created_at,sha256) VALUES(?,?,?)",
                (str(destination), utc_now(), digest),
            )
        return {"path": str(destination), "sha256": digest, "counts": self.stats(0)["queue"]}


class Processor:
    def __init__(self, queue: Queue, config: Config):
        self.queue = queue
        self.config = config

    def _signature(self, body: bytes) -> str:
        stamp = int(time.time())
        digest = hmac.new(
            self.config.warmbly_webhook_secret.encode(),
            f"{stamp}.".encode() + body,
            hashlib.sha256,
        ).hexdigest()
        return f"t={stamp},v1={digest}"

    def _send(self, body: bytes) -> tuple[int, dict[str, Any]]:
        headers = {
            "Content-Type": "application/json",
            "X-Confenge-Signature": self._signature(body),
            "X-Confenge-Webhook-Secret": self.config.warmbly_webhook_secret,
        }
        if self.config.warmbly_previous_secret:
            headers["X-Confenge-Webhook-Secret-Previous"] = self.config.warmbly_previous_secret
        if self.config.warmbly_bearer_token:
            headers["Authorization"] = f"Bearer {self.config.warmbly_bearer_token}"
        request = urllib.request.Request(self.config.warmbly_url, body, headers, method="POST")
        try:
            with urllib.request.urlopen(request, timeout=10) as response:
                raw = response.read(MAX_BODY_BYTES)
                return response.status, json.loads(raw or b"{}")
        except urllib.error.HTTPError as error:
            raw = error.read(MAX_BODY_BYTES)
            with contextlib.suppress(json.JSONDecodeError):
                return error.code, json.loads(raw or b"{}")
            return error.code, {}

    def process_one(self) -> bool:
        row = self.queue.claim(self.config.processing_lease_seconds)
        if row is None:
            return False
        event_id = row["provider_event_id"]
        correlation_id = row["correlation_id"]
        attempts = int(row["attempts"])
        if self.config.dry_run:
            self.queue.transition(event_id, "processed", code="dry_run", http_status=200)
            self.queue.requeue_blocked(correlation_id, event_id)
            return True
        try:
            status, response = self._send(row["payload"].encode())
        except (OSError, TimeoutError, urllib.error.URLError) as error:
            self._retry_or_dead(row, None, "transport_error", type(error).__name__)
            return True
        data = _object(response.get("data")) or response
        processed = data.get("processed") is True
        held = data.get("held") is True or _object(data.get("join")).get("held") is True
        rejected = data.get("rejected") is True or _object(data.get("join")).get("rejected") is True
        if 200 <= status < 300 and processed and not held and not rejected:
            self.queue.transition(event_id, "processed", code="warmbly_processed", http_status=status)
            self.queue.requeue_blocked(correlation_id, event_id)
        elif 200 <= status < 300:
            code = "warmbly_semantic_hold" if held else "warmbly_not_processed"
            self.queue.transition(event_id, "blocked", code=code, http_status=status)
            self.queue.occurrence(
                event_id,
                correlation_id,
                code,
                "commercial-ops",
                "inspect the Warmbly receipt and reconcile or replay after the prerequisite event",
                "Warmbly accepted transport but did not confirm semantic processing",
            )
        elif status in RETRYABLE_HTTP:
            self._retry_or_dead(row, status, f"http_{status}", "transient Warmbly response")
        else:
            code = f"http_{status}"
            self.queue.transition(event_id, "dead", code=code, http_status=status)
            self.queue.occurrence(
                event_id,
                correlation_id,
                code,
                "platform-ops",
                "repair authentication or contract before manually replaying",
                "non-retryable Warmbly response",
            )
        return True

    def _retry_or_dead(
        self, row: sqlite3.Row, status: int | None, code: str, detail: str
    ) -> None:
        attempts = int(row["attempts"])
        if attempts >= self.config.max_attempts:
            self.queue.transition(row["provider_event_id"], "dead", code=code, http_status=status)
            self.queue.occurrence(
                row["provider_event_id"],
                row["correlation_id"],
                "retry_exhausted",
                "platform-ops",
                "repair the dependency and replay this durable event",
                detail,
            )
            return
        delay = min(
            self.config.max_backoff_seconds,
            self.config.base_backoff_seconds * (2 ** max(0, attempts - 1)),
        )
        self.queue.transition(
            row["provider_event_id"],
            "retry",
            code=code,
            http_status=status,
            next_attempt_at=time.time() + delay,
        )


class App:
    def __init__(self, config: Config):
        self.config = config
        self.queue = Queue(config.db_path)
        self.processor = Processor(self.queue, config)
        self.wake = threading.Event()
        self.stop = threading.Event()

    def worker(self) -> None:
        while not self.stop.is_set():
            worked = self.processor.process_one()
            self.queue.prune(self.config.processed_retention_days)
            if not worked:
                self.wake.wait(1)
                self.wake.clear()

    def close(self) -> None:
        self.stop.set()
        self.wake.set()
        self.queue.close()


def handler_for(app: App) -> type[BaseHTTPRequestHandler]:
    class Handler(BaseHTTPRequestHandler):
        server_version = "confenge-asaas-adapter/1"

        def log_message(self, fmt: str, *args: Any) -> None:
            print(json.dumps({"at": utc_now(), "message": fmt % args}))

        def _json(self, status: int, payload: dict[str, Any]) -> None:
            body = json.dumps(payload, separators=(",", ":")).encode()
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def do_GET(self) -> None:  # noqa: N802
            if self.path != "/api/v1/webhooks/asaas/health":
                self._json(HTTPStatus.NOT_FOUND, {"error": "not_found"})
                return
            self._json(HTTPStatus.OK, app.queue.stats(app.config.backup_max_age_seconds))

        def do_POST(self) -> None:  # noqa: N802
            if self.path != "/api/v1/webhooks/asaas":
                self._json(HTTPStatus.NOT_FOUND, {"error": "not_found"})
                return
            supplied = self.headers.get("asaas-access-token", "")
            if not app.config.asaas_token or not hmac.compare_digest(supplied, app.config.asaas_token):
                self._json(HTTPStatus.UNAUTHORIZED, {"error": "invalid_authentication"})
                return
            try:
                length = int(self.headers.get("Content-Length", "0"))
            except ValueError:
                length = 0
            if length <= 0 or length > MAX_BODY_BYTES:
                self._json(HTTPStatus.REQUEST_ENTITY_TOO_LARGE, {"error": "invalid_size"})
                return
            try:
                raw = json.loads(self.rfile.read(length))
                if not isinstance(raw, dict):
                    raise ValueError("object required")
                payload = minimized_event(raw)
            except (json.JSONDecodeError, ValueError):
                self._json(HTTPStatus.BAD_REQUEST, {"error": "invalid_event"})
                return
            try:
                created, event_id = app.queue.persist(payload)
            except sqlite3.Error:
                self._json(HTTPStatus.SERVICE_UNAVAILABLE, {"error": "queue_unavailable"})
                return
            app.wake.set()
            self._json(
                HTTPStatus.OK,
                {"accepted": True, "replay": not created, "provider_event_id": event_id},
            )

    return Handler


def restore_database(source: Path, destination: Path) -> None:
    if not source.is_file():
        raise ValueError("backup does not exist")
    probe = sqlite3.connect(f"file:{source}?mode=ro", uri=True)
    try:
        version = probe.execute(
            "SELECT value FROM metadata WHERE key='schema_version'"
        ).fetchone()
        invalid = probe.execute(
            "SELECT COUNT(*) FROM events WHERE state NOT IN (?,?,?,?,?,?)", QUEUE_STATES
        ).fetchone()[0]
        if version is None or int(version[0]) != SCHEMA_VERSION or invalid:
            raise ValueError("incompatible adapter backup")
        probe.execute("PRAGMA integrity_check").fetchone()
    finally:
        probe.close()
    destination.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    os.chmod(destination.parent, 0o700)
    fd, staged_name = tempfile.mkstemp(prefix="events.restore.", dir=destination.parent)
    os.close(fd)
    staged = Path(staged_name)
    try:
        shutil.copyfile(source, staged)
        os.chmod(staged, 0o600)
        os.replace(staged, destination)
    finally:
        with contextlib.suppress(FileNotFoundError):
            staged.unlink()


def assert_permissions(path: Path) -> None:
    directory_mode = stat.S_IMODE(path.parent.stat().st_mode)
    file_mode = stat.S_IMODE(path.stat().st_mode)
    if directory_mode & 0o077 or file_mode & 0o077:
        raise ValueError(
            f"unsafe adapter permissions: directory={oct(directory_mode)} db={oct(file_mode)}"
        )


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("command", choices=("serve", "drain", "health", "backup", "restore", "permissions"))
    parser.add_argument("path", nargs="?")
    args = parser.parse_args()
    config = Config.from_env()
    if args.command == "restore":
        if not args.path:
            parser.error("restore requires a backup path")
        restore_database(Path(args.path), config.db_path)
        return 0
    queue = Queue(config.db_path)
    try:
        if args.command == "health":
            print(json.dumps(queue.stats(config.backup_max_age_seconds), sort_keys=True))
        elif args.command == "backup":
            if not args.path:
                parser.error("backup requires a destination path")
            print(json.dumps(queue.backup(Path(args.path)), sort_keys=True))
        elif args.command == "permissions":
            assert_permissions(config.db_path)
            print("ASAAS_ADAPTER_PERMISSIONS=PASS")
        elif args.command == "drain":
            processor = Processor(queue, config)
            while processor.process_one():
                pass
        else:
            queue.close()
            app = App(config)
            worker = threading.Thread(target=app.worker, daemon=True)
            worker.start()
            server = ThreadingHTTPServer(
                (config.listen_host, config.listen_port), handler_for(app)
            )
            try:
                server.serve_forever(poll_interval=0.5)
            finally:
                server.server_close()
                app.close()
    finally:
        with contextlib.suppress(sqlite3.ProgrammingError):
            queue.close()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
