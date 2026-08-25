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
import ipaddress
import json
import math
import os
import shutil
import sqlite3
import stat
import tempfile
import threading
import time
import urllib.error
import urllib.parse
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
MAX_PROVIDER_TEXT_CHARS = 500
WARMBLY_PROVIDER_PATH = "/v1/confenge/intel/commercial/provider-events"


def utc_now() -> str:
    return datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")


def _text(value: Any) -> str:
    if not isinstance(value, str):
        return ""
    value = value.strip()
    return value if len(value) <= MAX_PROVIDER_TEXT_CHARS else ""


def _object(value: Any) -> dict[str, Any]:
    return value if isinstance(value, dict) else {}


def _number(value: Any) -> int | float | None:
    if isinstance(value, bool) or not isinstance(value, (int, float)):
        return None
    if isinstance(value, float) and not math.isfinite(value):
        return None
    return value


def _minimized_fields(
    source: dict[str, Any],
    text_fields: tuple[str, ...],
    number_fields: tuple[str, ...] = (),
) -> dict[str, Any]:
    result: dict[str, Any] = {}
    for key in text_fields:
        if value := _text(source.get(key)):
            result[key] = value
    for key in number_fields:
        if (value := _number(source.get(key))) is not None:
            result[key] = value
    return result


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
    }
    if date_created := _text(raw.get("dateCreated")):
        result["dateCreated"] = date_created
    if external_ref:
        result["externalReference"] = external_ref
    if payment:
        minimized_payment = _minimized_fields(
            payment,
            (
                "id",
                "customer",
                "subscription",
                "checkoutSession",
                "externalReference",
                "status",
                "billingType",
                "paymentDate",
                "confirmedDate",
                "clientPaymentDate",
            ),
            ("value", "netValue"),
        )
        if minimized_payment:
            result["payment"] = minimized_payment
    if subscription:
        minimized_subscription = _minimized_fields(
            subscription, ("id", "customer", "externalReference", "status")
        )
        if minimized_subscription:
            result["subscription"] = minimized_subscription
    if checkout:
        minimized_checkout = _minimized_fields(
            checkout, ("id", "externalReference", "status")
        )
        if minimized_checkout:
            result["checkout"] = minimized_checkout
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
    def from_env(cls) -> Config:
        state = Path(
            os.getenv("ASAAS_ADAPTER_STATE_DIR", "/var/lib/confenge-asaas-adapter")
        )
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
            warmbly_webhook_secret=os.getenv(
                "ASAAS_ADAPTER_WARMBLY_WEBHOOK_SECRET", ""
            ),
            warmbly_previous_secret=os.getenv(
                "ASAAS_ADAPTER_WARMBLY_WEBHOOK_SECRET_PREVIOUS", ""
            ),
            max_attempts=int(os.getenv("ASAAS_ADAPTER_MAX_ATTEMPTS", "8")),
            base_backoff_seconds=int(
                os.getenv("ASAAS_ADAPTER_BASE_BACKOFF_SECONDS", "5")
            ),
            max_backoff_seconds=int(
                os.getenv("ASAAS_ADAPTER_MAX_BACKOFF_SECONDS", "900")
            ),
            processing_lease_seconds=int(
                os.getenv("ASAAS_ADAPTER_PROCESSING_LEASE_SECONDS", "120")
            ),
            processed_retention_days=int(
                os.getenv("ASAAS_ADAPTER_PROCESSED_RETENTION_DAYS", "45")
            ),
            backup_max_age_seconds=int(
                os.getenv("ASAAS_ADAPTER_BACKUP_MAX_AGE_SECONDS", "93600")
            ),
            dry_run=os.getenv("ASAAS_ADAPTER_DRY_RUN", "false").lower() == "true",
        )

    def validate(self, command: str) -> None:
        if not 1 <= self.listen_port <= 65535:
            raise ValueError("ASAAS_ADAPTER_PORT must be between 1 and 65535")
        if not _is_loopback_host(self.listen_host):
            raise ValueError("ASAAS_ADAPTER_HOST must be loopback")
        if self.max_attempts < 1:
            raise ValueError("ASAAS_ADAPTER_MAX_ATTEMPTS must be positive")
        if (
            self.base_backoff_seconds < 1
            or self.max_backoff_seconds < self.base_backoff_seconds
        ):
            raise ValueError("adapter backoff bounds are invalid")
        if self.processing_lease_seconds < 1 or self.processed_retention_days < 1:
            raise ValueError("adapter lease and retention must be positive")
        if self.backup_max_age_seconds < 1:
            raise ValueError("ASAAS_ADAPTER_BACKUP_MAX_AGE_SECONDS must be positive")
        if command in ("serve", "validate") and (
            not 32 <= len(self.asaas_token) <= 255
            or any(character.isspace() for character in self.asaas_token)
        ):
            raise ValueError(
                "ASAAS_WEBHOOK_TOKEN must contain 32 to 255 non-whitespace characters"
            )
        if command not in ("serve", "drain", "validate") or self.dry_run:
            return
        if not self.warmbly_bearer_token or not self.warmbly_webhook_secret:
            raise ValueError("Warmbly bearer token and webhook secret are required")
        _validate_warmbly_url(self.warmbly_url)


def _is_loopback_host(host: str) -> bool:
    host = host.strip().lower()
    if host == "localhost":
        return True
    with contextlib.suppress(ValueError):
        return ipaddress.ip_address(host).is_loopback
    return False


def _validate_warmbly_url(value: str) -> None:
    parsed = urllib.parse.urlsplit(value)
    if parsed.scheme not in ("http", "https") or not parsed.hostname:
        raise ValueError("ASAAS_ADAPTER_WARMBLY_URL must be HTTP or HTTPS")
    if parsed.username or parsed.password or parsed.query or parsed.fragment:
        raise ValueError(
            "ASAAS_ADAPTER_WARMBLY_URL cannot contain credentials, query, or fragment"
        )
    if parsed.path != WARMBLY_PROVIDER_PATH:
        raise ValueError(
            f"ASAAS_ADAPTER_WARMBLY_URL path must be {WARMBLY_PROVIDER_PATH}"
        )
    if parsed.scheme == "http" and not _is_loopback_host(parsed.hostname):
        raise ValueError("plain HTTP Warmbly URL must be loopback")


def secure_database_permissions(path: Path) -> None:
    os.chmod(path.parent, 0o700)
    for candidate in (path, Path(f"{path}-wal"), Path(f"{path}-shm")):
        if candidate.exists():
            os.chmod(candidate, 0o600)


def file_sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as source:
        while chunk := source.read(1024 * 1024):
            digest.update(chunk)
    return digest.hexdigest()


class Queue:
    def __init__(self, path: Path, *, recover_processing: bool = True):
        self.path = path
        path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
        secure_database_permissions(path)
        self.db = sqlite3.connect(path, timeout=30, check_same_thread=False)
        self.db.row_factory = sqlite3.Row
        self.lock = threading.RLock()
        try:
            self._initialize(recover_processing)
        except Exception:
            self.db.close()
            raise
        secure_database_permissions(path)

    def _initialize(self, recover_processing: bool) -> None:
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
            version = self.db.execute(
                "SELECT value FROM metadata WHERE key='schema_version'"
            ).fetchone()
            if version is None:
                self.db.execute(
                    "INSERT INTO metadata(key,value) VALUES('schema_version',?)",
                    (str(SCHEMA_VERSION),),
                )
            else:
                try:
                    stored_version = int(version["value"])
                except (TypeError, ValueError) as error:
                    raise ValueError("invalid adapter queue schema version") from error
                if stored_version != SCHEMA_VERSION:
                    raise ValueError(
                        f"incompatible adapter queue schema {stored_version}; "
                        f"expected {SCHEMA_VERSION}"
                    )
            if recover_processing:
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
                "SELECT payload_sha256 FROM events WHERE provider_event_id=?",
                (event_id,),
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
                "SELECT * FROM events WHERE provider_event_id=?",
                (row["provider_event_id"],),
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
                (
                    state,
                    next_attempt_at,
                    http_status,
                    code,
                    utc_now(),
                    processed_at,
                    event_id,
                ),
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
                (
                    event_id,
                    correlation_id,
                    code,
                    owner,
                    next_action,
                    utc_now(),
                    detail[:500],
                ),
            )

    def stats(self, backup_max_age_seconds: int) -> dict[str, Any]:
        with self.lock:
            counts = {state: 0 for state in QUEUE_STATES}
            for row in self.db.execute(
                "SELECT state,COUNT(*) n FROM events GROUP BY state"
            ):
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
            stamp = datetime.fromisoformat(
                last_backup["created_at"].replace("Z", "+00:00")
            )
            backup_age = max(
                0, int((datetime.now(timezone.utc) - stamp).total_seconds())
            )
        oldest_age: int | None = None
        if oldest:
            stamp = datetime.fromisoformat(oldest["received_at"].replace("Z", "+00:00"))
            oldest_age = max(
                0, int((datetime.now(timezone.utc) - stamp).total_seconds())
            )
        return {
            "schema_version": "confenge.asaas-adapter-health.v1",
            "queue": counts,
            "oldest_backlog_age_seconds": oldest_age,
            "open_occurrences": open_occ,
            "backup_age_seconds": backup_age,
            "backup_status": (
                "UNKNOWN"
                if backup_age is None
                else "STALE"
                if backup_age > backup_max_age_seconds
                else "FRESH"
            ),
        }

    def prune(self, retention_days: int) -> int:
        cutoff = time.time() - retention_days * 86400
        with self.lock, self.db:
            cur = self.db.execute(
                "DELETE FROM events WHERE state='processed' AND unixepoch(processed_at)<?",
                (cutoff,),
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
        digest = file_sha256(destination)
        with self.lock, self.db:
            self.db.execute(
                "INSERT OR REPLACE INTO backups(path,created_at,sha256) VALUES(?,?,?)",
                (str(destination), utc_now(), digest),
            )
        return {
            "path": str(destination),
            "sha256": digest,
            "counts": self.stats(0)["queue"],
        }


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
            headers["X-Confenge-Webhook-Secret-Previous"] = (
                self.config.warmbly_previous_secret
            )
        if self.config.warmbly_bearer_token:
            headers["Authorization"] = f"Bearer {self.config.warmbly_bearer_token}"
        request = urllib.request.Request(
            self.config.warmbly_url, body, headers, method="POST"
        )
        try:
            with urllib.request.urlopen(request, timeout=10) as response:
                raw = response.read(MAX_BODY_BYTES + 1)
                if len(raw) > MAX_BODY_BYTES:
                    return response.status, {}
                with contextlib.suppress(json.JSONDecodeError):
                    return response.status, _object(json.loads(raw or b"{}"))
                return response.status, {}
        except urllib.error.HTTPError as error:
            raw = error.read(MAX_BODY_BYTES)
            with contextlib.suppress(json.JSONDecodeError):
                return error.code, _object(json.loads(raw or b"{}"))
            return error.code, {}

    def process_one(self) -> bool:
        row = self.queue.claim(self.config.processing_lease_seconds)
        if row is None:
            return False
        event_id = row["provider_event_id"]
        correlation_id = row["correlation_id"]
        if self.config.dry_run:
            self.queue.transition(
                event_id, "blocked", code="dry_run_not_forwarded", http_status=None
            )
            self.queue.occurrence(
                event_id,
                correlation_id,
                "dry_run_not_forwarded",
                "commercial-ops",
                "disable dry-run and replay only after validating the Warmbly gate",
                "dry-run retained the durable event without forwarding it",
            )
            return True
        try:
            status, response = self._send(row["payload"].encode())
        except (OSError, TimeoutError, urllib.error.URLError) as error:
            self._retry_or_dead(row, None, "transport_error", type(error).__name__)
            return True
        data = _object(response.get("data")) or response
        processed = data.get("processed") is True
        held = data.get("held") is True or _object(data.get("join")).get("held") is True
        rejected = (
            data.get("rejected") is True
            or _object(data.get("join")).get("rejected") is True
        )
        if 200 <= status < 300 and processed and not held and not rejected:
            self.queue.transition(
                event_id, "processed", code="warmbly_processed", http_status=status
            )
            self.queue.requeue_blocked(correlation_id, event_id)
        elif 200 <= status < 300:
            code = (
                "warmbly_rejected"
                if rejected
                else "warmbly_semantic_hold"
                if held
                else "warmbly_not_processed"
            )
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
            self._retry_or_dead(
                row, status, f"http_{status}", "transient Warmbly response"
            )
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
            self.queue.transition(
                row["provider_event_id"], "dead", code=code, http_status=status
            )
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
        self.last_worker_error = ""

    def worker(self) -> None:
        while not self.stop.is_set():
            try:
                worked = self.processor.process_one()
                self.queue.prune(self.config.processed_retention_days)
                self.last_worker_error = ""
            except Exception as error:  # noqa: BLE001
                self.last_worker_error = type(error).__name__
                print(
                    json.dumps(
                        {
                            "at": utc_now(),
                            "code": "worker_iteration_failed",
                            "error_type": self.last_worker_error,
                        }
                    )
                )
                self.wake.wait(1)
                self.wake.clear()
                continue
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

        def _json(
            self, status: int, payload: dict[str, Any], *, include_body: bool = True
        ) -> None:
            body = json.dumps(payload, separators=(",", ":")).encode()
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            if include_body:
                self.wfile.write(body)

        def _health(self, *, include_body: bool) -> None:
            health = app.queue.stats(app.config.backup_max_age_seconds)
            health["worker_status"] = "DEGRADED" if app.last_worker_error else "OK"
            if app.last_worker_error:
                health["worker_error_type"] = app.last_worker_error
            status = (
                HTTPStatus.SERVICE_UNAVAILABLE
                if app.last_worker_error
                else HTTPStatus.OK
            )
            self._json(status, health, include_body=include_body)

        def do_GET(self) -> None:
            if self.path != "/api/v1/webhooks/asaas/health":
                self._json(HTTPStatus.NOT_FOUND, {"error": "not_found"})
                return
            self._health(include_body=True)

        def do_HEAD(self) -> None:
            if self.path != "/api/v1/webhooks/asaas/health":
                self._json(
                    HTTPStatus.NOT_FOUND, {"error": "not_found"}, include_body=False
                )
                return
            self._health(include_body=False)

        def do_POST(self) -> None:
            if self.path != "/api/v1/webhooks/asaas":
                self._json(HTTPStatus.NOT_FOUND, {"error": "not_found"})
                return
            supplied = self.headers.get("asaas-access-token", "")
            if not app.config.asaas_token or not hmac.compare_digest(
                supplied, app.config.asaas_token
            ):
                self._json(HTTPStatus.UNAUTHORIZED, {"error": "invalid_authentication"})
                return
            try:
                length = int(self.headers.get("Content-Length", "0"))
            except ValueError:
                length = 0
            if length <= 0 or length > MAX_BODY_BYTES:
                self._json(
                    HTTPStatus.REQUEST_ENTITY_TOO_LARGE, {"error": "invalid_size"}
                )
                return
            try:
                raw = json.loads(self.rfile.read(length))
                if not isinstance(raw, dict):
                    raise TypeError("object required")
                payload = minimized_event(raw)
            except (json.JSONDecodeError, TypeError, ValueError):
                self._json(HTTPStatus.BAD_REQUEST, {"error": "invalid_event"})
                return
            try:
                created, event_id = app.queue.persist(payload)
            except sqlite3.Error:
                self._json(
                    HTTPStatus.SERVICE_UNAVAILABLE, {"error": "queue_unavailable"}
                )
                return
            app.wake.set()
            self._json(
                HTTPStatus.OK,
                {
                    "accepted": True,
                    "replay": not created,
                    "provider_event_id": event_id,
                },
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
        integrity = probe.execute("PRAGMA integrity_check").fetchall()
        if integrity != [("ok",)]:
            raise ValueError("adapter backup failed integrity_check")
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
        for sidecar in (Path(f"{destination}-wal"), Path(f"{destination}-shm")):
            sidecar.unlink(missing_ok=True)
        os.replace(staged, destination)
    finally:
        with contextlib.suppress(FileNotFoundError):
            staged.unlink()


def assert_permissions(path: Path) -> None:
    directory_mode = stat.S_IMODE(path.parent.stat().st_mode)
    unsafe = []
    for candidate in (path, Path(f"{path}-wal"), Path(f"{path}-shm")):
        if not candidate.exists():
            continue
        mode = stat.S_IMODE(candidate.stat().st_mode)
        if mode != 0o600:
            unsafe.append(f"{candidate.name}={oct(mode)}")
    if directory_mode != 0o700 or unsafe:
        detail = ",".join(unsafe) or "files=secure"
        raise ValueError(
            f"unsafe adapter permissions: directory={oct(directory_mode)} {detail}"
        )


def main() -> int:
    os.umask(0o077)
    parser = argparse.ArgumentParser()
    parser.add_argument(
        "command",
        choices=(
            "serve",
            "drain",
            "health",
            "backup",
            "restore",
            "permissions",
            "validate",
        ),
    )
    parser.add_argument("path", nargs="?")
    args = parser.parse_args()
    try:
        config = Config.from_env()
        config.validate(args.command)
    except ValueError as error:
        parser.error(str(error))
    if args.command == "restore":
        if not args.path:
            parser.error("restore requires a backup path")
        restore_database(Path(args.path), config.db_path)
        return 0
    if args.command == "permissions":
        assert_permissions(config.db_path)
        print("ASAAS_ADAPTER_PERMISSIONS=PASS")
        return 0
    if args.command == "validate":
        print("ASAAS_ADAPTER_CONFIG=PASS")
        return 0
    queue = Queue(config.db_path, recover_processing=args.command in ("serve", "drain"))
    try:
        if args.command == "health":
            print(
                json.dumps(queue.stats(config.backup_max_age_seconds), sort_keys=True)
            )
        elif args.command == "backup":
            if not args.path:
                parser.error("backup requires a destination path")
            print(json.dumps(queue.backup(Path(args.path)), sort_keys=True))
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
                app.stop.set()
                app.wake.set()
                worker.join()
                app.close()
    finally:
        with contextlib.suppress(sqlite3.ProgrammingError):
            queue.close()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
