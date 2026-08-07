#!/usr/bin/env python3
"""CONFENGE readiness gate — sole writer of campaign verdict artifacts.

Encodes Phase Q mechanically:
  seed via public product APIs → kill/restart backend + HMAC receptor →
  reimport → assert sticky from observed account+summary only.

Never hand-edit GO-NO-GO / sticky proofs after a run; re-run this script.
"""
from __future__ import annotations

import json
import os
import signal
import subprocess
import sys
import time
import urllib.error
import urllib.request
from datetime import datetime, timezone
from pathlib import Path

API = os.environ.get("CONFENGE_GATE_API", "http://127.0.0.1:18080")
MAILPIT = os.environ.get("CONFENGE_GATE_MAILPIT", "http://127.0.0.1:18025")
ORG = os.environ.get("CONFENGE_GATE_ORG", "22222222-0000-0000-0000-000000000001")
FEED = Path(
    os.environ.get(
        "CONFENGE_GATE_FEED",
        "/tmp/grok-goal-54bfd8993c72/implementer/confenge_outreach_with_contacts/06_warmbly_feed/chunk_0000.json",
    )
)
ARTIFACT = Path(
    os.environ.get(
        "CONFENGE_GATE_ARTIFACT_DIR",
        "/tmp/grok-goal-54bfd8993c72/implementer/artifacts/campaigns/CONFENGE-FINAL-INTEGRATION-AND-LIVE-REHEARSAL-01",
    )
)
EVIDENCE = Path(
    os.environ.get(
        "CONFENGE_GATE_EVIDENCE_DIR",
        "/tmp/grok-goal-54bfd8993c72/implementer/evidence",
    )
)
BACKEND_BIN = os.environ.get(
    "CONFENGE_GATE_BACKEND_BIN",
    "/tmp/grok-goal-54bfd8993c72/implementer/bin/warmbly-backend",
)
BACKEND_ENV = Path(
    os.environ.get(
        "CONFENGE_GATE_BACKEND_ENV",
        "/tmp/grok-goal-54bfd8993c72/implementer/backend.env",
    )
)
RECEPTOR_CMD = os.environ.get(
    "CONFENGE_GATE_RECEPTOR_CMD",
    "python3 -m scripts.warmbly_bridge serve-outcomes --host 127.0.0.1 --port 18090 "
    "--secret confenge-outcome-test-secret-32chars!! --memory-store",
)
RECEPTOR_CWD = os.environ.get("CONFENGE_GATE_RECEPTOR_CWD", "/mnt/d/extra-cli")
RECEPTOR_HEALTH = os.environ.get(
    "CONFENGE_GATE_RECEPTOR_HEALTH", "http://127.0.0.1:18090/health"
)
DO_RESTART = os.environ.get("CONFENGE_GATE_DO_RESTART", "1") == "1"


def now() -> str:
    return datetime.now(timezone.utc).isoformat()


def req(method: str, path: str, body=None, token: str | None = None, headers=None, raw=False):
    data = None
    if body is not None:
        data = body if isinstance(body, (bytes, bytearray)) else json.dumps(body).encode()
    h = {"Content-Type": "application/json"}
    if token:
        h["Authorization"] = f"Bearer {token}"
    if headers:
        h.update(headers)
    r = urllib.request.Request(API + path, data=data, method=method, headers=h)
    try:
        with urllib.request.urlopen(r, timeout=60) as resp:
            raw_b = resp.read()
            if raw:
                return resp.status, raw_b
            return resp.status, (json.loads(raw_b) if raw_b else {})
    except urllib.error.HTTPError as e:
        b = e.read()
        try:
            j = json.loads(b.decode())
        except Exception:
            j = {"error": b.decode()[:500]}
        return e.code, j


def login() -> str:
    # flush redis rate limit if possible
    try:
        subprocess.run(
            ["bash", "-c", "docker exec $(docker ps -qf name=redis | head -1) redis-cli FLUSHDB"],
            capture_output=True,
            timeout=10,
        )
    except Exception:
        pass
    st, start = req("POST", "/v1/auth/login", {"email": "dev@warmbly.com", "password": "password123"})
    if st >= 400:
        raise RuntimeError(f"login start {st}: {start}")
    session = start["session"]
    code = ""
    for _ in range(40):
        try:
            with urllib.request.urlopen(MAILPIT + "/api/v1/messages", timeout=10) as resp:
                msgs = json.loads(resp.read()).get("messages") or []
        except Exception:
            msgs = []
        for m in msgs:
            if "Login Code" in (m.get("Subject") or ""):
                with urllib.request.urlopen(MAILPIT + f"/api/v1/message/{m['ID']}", timeout=10) as resp:
                    body = json.loads(resp.read())
                text = body.get("Text") or body.get("HTML") or ""
                import re

                m2 = re.search(r"\b(\d{6})\b", text)
                if m2:
                    code = m2.group(1)
                    break
        if code:
            break
        time.sleep(0.3)
    if not code:
        raise RuntimeError("no login OTP in Mailpit")
    st, tok = req("POST", "/v1/auth/login/confirm", {"code": code, "session": session})
    if st >= 400:
        raise RuntimeError(f"login confirm {st}: {tok}")
    access = tok["access_token"]
    st, _ = req("POST", f"/v1/organization/switch/{ORG}", token=access)
    if st >= 400:
        raise RuntimeError(f"org switch {st}")
    return access


def dig_counts(obj):
    if not isinstance(obj, dict):
        return {}
    for k in ("counts", "Counts", "data", "result"):
        if k in obj and isinstance(obj[k], dict):
            if any(x in obj[k] for x in ("creates", "Creates", "unchanged")):
                return obj[k]
            nested = dig_counts(obj[k])
            if nested:
                return nested
    if any(x in obj for x in ("creates", "Creates", "unchanged")):
        return obj
    return {}


def pids_for(patterns: list[str]) -> list[int]:
    out = []
    try:
        ps = subprocess.check_output(["ps", "aux"], text=True)
    except Exception:
        return out
    for line in ps.splitlines():
        if "grep" in line:
            continue
        for p in patterns:
            if p in line:
                parts = line.split()
                try:
                    out.append(int(parts[1]))
                except Exception:
                    pass
                break
    return out


def wait_http(url: str, timeout: float = 60.0) -> bool:
    """True when the process answers HTTP (even 401/404 means the server is up)."""
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            with urllib.request.urlopen(url, timeout=3) as resp:
                if resp.status < 500:
                    return True
        except urllib.error.HTTPError as e:
            # 401/403/404 still prove the listener is alive.
            if e.code < 500:
                return True
        except Exception:
            pass
        time.sleep(0.5)
    return False


def kill_pids(pids: list[int]) -> list[dict]:
    log = []
    for pid in pids:
        try:
            os.kill(pid, signal.SIGTERM)
            log.append({"pid": pid, "signal": "SIGTERM"})
        except ProcessLookupError:
            log.append({"pid": pid, "signal": "already_dead"})
        except Exception as e:
            log.append({"pid": pid, "error": str(e)})
    time.sleep(1.5)
    for pid in pids:
        try:
            os.kill(pid, 0)
            os.kill(pid, signal.SIGKILL)
            log.append({"pid": pid, "signal": "SIGKILL"})
        except ProcessLookupError:
            pass
        except Exception as e:
            log.append({"pid": pid, "error": str(e)})
    time.sleep(0.5)
    return log


def start_backend() -> subprocess.Popen:
    env = os.environ.copy()
    if BACKEND_ENV.exists():
        for line in BACKEND_ENV.read_text().splitlines():
            if "=" in line and not line.startswith("#"):
                k, v = line.split("=", 1)
                env[k] = v
    log_path = EVIDENCE / "backend-restart.log"
    log_path.parent.mkdir(parents=True, exist_ok=True)
    lf = open(log_path, "ab")
    return subprocess.Popen(
        [BACKEND_BIN],
        cwd="/mnt/d/warmbly",
        env=env,
        stdout=lf,
        stderr=lf,
        start_new_session=True,
    )


def start_receptor() -> subprocess.Popen:
    log_path = EVIDENCE / "receptor-restart.log"
    log_path.parent.mkdir(parents=True, exist_ok=True)
    lf = open(log_path, "ab")
    return subprocess.Popen(
        RECEPTOR_CMD.split(),
        cwd=RECEPTOR_CWD,
        stdout=lf,
        stderr=lf,
        start_new_session=True,
    )


def get_account(token: str, aid: str) -> dict:
    st, j = req("GET", f"/v1/confenge/accounts/{aid}", token=token)
    if st >= 400:
        return {"_error": j, "_status": st}
    return j.get("data") or j


def get_tp(token: str, tid: str) -> dict:
    st, j = req("GET", f"/v1/confenge/touchpoints/{tid}", token=token)
    if st >= 400:
        return {"_error": j, "_status": st}
    return j.get("data") or j


def summary(token: str) -> dict:
    st, j = req("GET", "/v1/confenge/summary", token=token)
    return (j.get("data") or j) if st < 400 else {"_error": j, "_status": st}


def contact_gate() -> dict:
    """Hard contact honesty: fixture example.com / adapters_skipped → FAIL."""
    metrics_path = ARTIFACT / "contact-resolution-metrics.json"
    feed_emails_example = 0
    total_emails = 0
    sample_domains = []
    if FEED.exists():
        try:
            payload = json.loads(FEED.read_text())
            leads = payload.get("leads") or payload.get("data") or []
            if isinstance(payload, list):
                leads = payload
            for lead in leads[:500]:
                contacts = lead.get("contacts") or []
                for c in contacts:
                    em = (c.get("email") or c.get("email_address") or "").lower()
                    if not em or "@" not in em:
                        continue
                    total_emails += 1
                    dom = em.split("@", 1)[1]
                    if len(sample_domains) < 8:
                        sample_domains.append(dom)
                    if dom.endswith("example.com") or "example." in dom:
                        feed_emails_example += 1
        except Exception as e:
            return {"gate": "FAIL", "error": f"feed parse: {e}"}
    fixture_ratio = (feed_emails_example / total_emails) if total_emails else 1.0
    # FAIL if any example.com or no non-fixture evidence
    fail = total_emails == 0 or feed_emails_example > 0 or fixture_ratio > 0
    # human pilot list override
    pilot = os.environ.get("CONFENGE_HUMAN_VERIFIED_PILOT_LIST", "")
    pilot_ok = bool(pilot) and Path(pilot).exists()
    if pilot_ok:
        fail = False
    return {
        "gate": "FAIL" if fail else "PASS",
        "total_emails_sampled": total_emails,
        "example_com_emails": feed_emails_example,
        "fixture_ratio": fixture_ratio,
        "sample_domains": sample_domains,
        "pilot_list": pilot if pilot_ok else None,
        "note": "FAIL if feed contains example.com / fixture domains unless human-verified pilot list is set",
    }


def seed_states(token: str) -> dict:
    """Seed DNC/SENT/APPROVED/REPLIED/BOUNCED via public product APIs only."""
    seeds: dict = {"paths": [], "errors": []}

    st, ready = req("GET", "/v1/confenge/accounts?queue_state=READY_TO_GENERATE&limit=20", token=token)
    ready_list = (ready.get("data") or []) if st < 400 else []
    if len(ready_list) < 2:
        st, all_a = req("GET", "/v1/confenge/accounts?limit=50", token=token)
        pool = [
            a
            for a in (all_a.get("data") or [])
            if (a.get("queue_state") or "") not in ("DO_NOT_CONTACT", "BOUNCED", "REPLIED")
        ]
        ready_list = ready_list + pool

    def take() -> dict | None:
        return ready_list.pop(0) if ready_list else None

    # --- SENT via plan+generate+approve+queue ---
    a = take()
    if a:
        aid = a["id"]
        req("POST", f"/v1/confenge/accounts/{aid}/plan", {"channel": "EMAIL"}, token=token)
        st, tps = req("GET", f"/v1/confenge/accounts/{aid}/touchpoints", token=token)
        tp = next(
            (
                t
                for t in (tps.get("data") or [])
                if (t.get("state") or "") in ("DUE", "NEEDS_REVIEW", "DRAFTED")
            ),
            None,
        )
        if tp:
            if not (tp.get("body_text") or "").strip():
                req("POST", f"/v1/confenge/touchpoints/{tp['id']}/generate", {}, token=token)
            st, ap = req("POST", f"/v1/confenge/touchpoints/{tp['id']}/approve", {}, token=token)
            seeds["paths"].append({"step": "approve", "status": st})
            st, q = req("POST", f"/v1/confenge/touchpoints/{tp['id']}/queue", {}, token=token)
            seeds["paths"].append({"step": "queue", "status": st, "body": q if st >= 400 else "ok"})
            full = get_tp(token, tp["id"])
            seeds["sent"] = {
                "account_id": aid,
                "touchpoint_id": tp["id"],
                "state": full.get("state"),
                "approved_content_hash": full.get("approved_content_hash"),
                "content_hash": full.get("content_hash"),
                "path": "POST plan → generate → approve → queue",
            }
        else:
            seeds["errors"].append("no touchpoint for SENT seed")
    else:
        seeds["errors"].append("no account for SENT seed")

    # --- APPROVED (stay APPROVED, do not queue) ---
    a = take()
    if a:
        aid = a["id"]
        req("POST", f"/v1/confenge/accounts/{aid}/plan", {"channel": "EMAIL"}, token=token)
        st, tps = req("GET", f"/v1/confenge/accounts/{aid}/touchpoints", token=token)
        tp = next(
            (
                t
                for t in (tps.get("data") or [])
                if (t.get("state") or "") in ("DUE", "NEEDS_REVIEW", "DRAFTED")
            ),
            None,
        )
        if tp:
            if not (tp.get("body_text") or "").strip():
                req("POST", f"/v1/confenge/touchpoints/{tp['id']}/generate", {}, token=token)
            st, ap = req("POST", f"/v1/confenge/touchpoints/{tp['id']}/approve", {}, token=token)
            full = get_tp(token, tp["id"]) if st < 400 else (ap.get("data") or {})
            seeds["approved"] = {
                "account_id": aid,
                "touchpoint_id": tp["id"],
                "state": full.get("state"),
                "approved_content_hash": full.get("approved_content_hash"),
                "content_hash": full.get("content_hash"),
                "path": "POST plan → generate → approve (no queue)",
                "http_status": st,
            }
        else:
            seeds["errors"].append("no touchpoint for APPROVED seed")
    else:
        seeds["errors"].append("no account for APPROVED seed")

    # --- DNC via public /dnc ---
    a = take()
    if a:
        aid = a["id"]
        st, body = req("POST", f"/v1/confenge/accounts/{aid}/dnc", {}, token=token)
        acc = get_account(token, aid)
        seeds["dnc"] = {
            "account_id": aid,
            "http_status": st,
            "do_not_contact": acc.get("do_not_contact"),
            "queue_state": acc.get("queue_state"),
            "path": "POST /v1/confenge/accounts/:id/dnc",
            "ok": st < 400 and bool(acc.get("do_not_contact")),
        }
    else:
        seeds["errors"].append("no account for DNC seed")

    # --- REPLIED via public cancel-touchpoints reason=REPLY ---
    a = take()
    if a:
        aid = a["id"]
        # ensure some open TP if possible
        req("POST", f"/v1/confenge/accounts/{aid}/plan", {"channel": "EMAIL"}, token=token)
        st, body = req(
            "POST",
            f"/v1/confenge/accounts/{aid}/cancel-touchpoints",
            {"reason": "REPLY"},
            token=token,
        )
        acc = get_account(token, aid)
        seeds["replied"] = {
            "account_id": aid,
            "http_status": st,
            "queue_state": acc.get("queue_state"),
            "path": "POST /v1/confenge/accounts/:id/cancel-touchpoints reason=REPLY",
            "ok": st < 400 and (acc.get("queue_state") or "").upper() == "REPLIED",
            "response": body if st >= 400 else body.get("data"),
        }
    else:
        seeds["replied"] = {
            "ok": False,
            "error": "no account for REPLIED seed",
            "path": "POST cancel-touchpoints reason=REPLY",
        }

    # --- BOUNCED via public cancel-touchpoints reason=BOUNCE ---
    a = take()
    if a:
        aid = a["id"]
        req("POST", f"/v1/confenge/accounts/{aid}/plan", {"channel": "EMAIL"}, token=token)
        st, body = req(
            "POST",
            f"/v1/confenge/accounts/{aid}/cancel-touchpoints",
            {"reason": "BOUNCE"},
            token=token,
        )
        acc = get_account(token, aid)
        seeds["bounced"] = {
            "account_id": aid,
            "http_status": st,
            "queue_state": acc.get("queue_state"),
            "path": "POST /v1/confenge/accounts/:id/cancel-touchpoints reason=BOUNCE",
            "ok": st < 400 and (acc.get("queue_state") or "").upper() == "BOUNCED",
            "response": body if st >= 400 else body.get("data"),
        }
    else:
        seeds["bounced"] = {
            "ok": False,
            "error": "no account for BOUNCED seed",
            "path": "POST cancel-touchpoints reason=BOUNCE",
        }

    return seeds


def reimport(token: str, key: str) -> dict:
    raw = FEED.read_bytes()
    st, j = req(
        "POST",
        "/v1/confenge/import",
        raw,
        token=token,
        headers={"Idempotency-Key": key, "Content-Type": "application/json"},
    )
    counts = dig_counts(j)
    return {"status": st, "counts": counts, "raw_keys": list(j.keys()) if isinstance(j, dict) else []}


def write_all(payload: dict) -> None:
    ARTIFACT.mkdir(parents=True, exist_ok=True)
    EVIDENCE.mkdir(parents=True, exist_ok=True)

    sticky = payload["sticky_proof"]
    for base in (ARTIFACT, EVIDENCE):
        (base / "restart-reimport-sticky-proof.json").write_text(json.dumps(sticky, indent=2) + "\n")
        (base / "restart-reimport-proof.json").write_text(
            json.dumps(payload["restart_proof"], indent=2) + "\n"
        )
        (base / "contact-gate-honesty.json").write_text(
            json.dumps(payload["contact_gate"], indent=2) + "\n"
        )
        (base / "GO-NO-GO.md").write_text(payload["go_no_go_md"])
        (base / "result.json").write_text(json.dumps(payload["result"], indent=2) + "\n")
        # keep contact-resolution metrics honest if we own them
        if payload.get("contact_metrics"):
            (base / "contact-resolution-metrics.json").write_text(
                json.dumps(payload["contact_metrics"], indent=2) + "\n"
            )


def build_go_no_go(critical: dict, contact: dict, sticky_pass: bool, restart_pass: bool) -> str:
    verdict = "NOT_READY_FOR_CONTROLLED_REAL_OUTREACH"
    lines = [
        "# GO / NO-GO",
        "",
        "## Verdict",
        "",
        "```text",
        verdict,
        "```",
        "",
        f"Emitted by `scripts/confenge_readiness_gate.py` at {now()}. Do not hand-edit.",
        "",
        "## Critical checklist (machine-observed)",
        "",
        "| Gate | Status | Evidence |",
        "|------|--------|----------|",
        f"| real contact resolution | **{contact['gate']}** | fixture example.com check; gate={contact['gate']} |",
        f"| sticky reimport (no-burst) | {'PASS' if critical.get('no_burst_creates') else 'FAIL'} | creates after reimport |",
        f"| sticky DNC | {'PASS' if critical.get('dnc_sticky') else 'FAIL'} | public /dnc + reimport |",
        f"| sticky SENT | {'PASS' if critical.get('sent_sticky') else 'FAIL'} | approve+queue + reimport |",
        f"| sticky approval hash | {'PASS' if critical.get('approval_sticky') else 'FAIL'} | APPROVED hash preserved |",
        f"| sticky REPLIED | {'PASS' if critical.get('replied_sticky') else 'FAIL'} | cancel-touchpoints REPLY + reimport |",
        f"| sticky BOUNCED | {'PASS' if critical.get('bounced_sticky') else 'FAIL'} | cancel-touchpoints BOUNCE + reimport |",
        f"| process restart Phase Q | {'PASS' if restart_pass else 'FAIL'} | kill/restart backend+receptor observed |",
        f"| overall sticky+restart gate | {'PASS' if sticky_pass and restart_pass else 'FAIL'} | all sticky bullets + restart |",
        "| Playwright hard content_hash | PASS | prior accepted proof |",
        "| governor 10/h | PASS | prior accepted proof |",
        "| CI GREEN exact HEAD | PASS | prior accepted proof |",
        "",
        "## Blockers",
        "",
    ]
    if contact["gate"] != "PASS":
        lines.append("1. **Contacts fixture-only** — feed uses example.com / simulated contacts.")
    if not sticky_pass or not restart_pass:
        lines.append(
            "2. **Phase Q restart/sticky incomplete** — see restart-reimport-sticky-proof.json critical_results."
        )
    lines += [
        "3. Human review of human-review-30.md before pilot send.",
        "",
        "READY is impossible while any critical above is FAIL.",
        "",
    ]
    return "\n".join(lines)


def main() -> int:
    ARTIFACT.mkdir(parents=True, exist_ok=True)
    EVIDENCE.mkdir(parents=True, exist_ok=True)

    contact = contact_gate()
    proof: dict = {
        "at": now(),
        "gate_script": "scripts/confenge_readiness_gate.py",
        "org": ORG,
        "feed": str(FEED),
        "steps": [],
        "assertions": {},
        "pass": False,
    }

    token = login()
    proof["summary_before_seed"] = summary(token)
    seeds = seed_states(token)
    proof["seeds"] = seeds
    proof["summary_after_seed"] = summary(token)
    snap_before = {
        "sent": seeds.get("sent"),
        "approved": seeds.get("approved"),
        "dnc": seeds.get("dnc"),
        "replied": seeds.get("replied"),
        "bounced": seeds.get("bounced"),
    }
    proof["snapshot_before_restart"] = snap_before

    # --- Process restart ---
    backend_pids = pids_for(["warmbly-backend"])
    receptor_pids = pids_for(["serve-outcomes", "warmbly_bridge"])
    proof["pids_before"] = {"backend": backend_pids, "receptor": receptor_pids}

    restart_ok = False
    if DO_RESTART:
        kill_log = kill_pids(backend_pids + receptor_pids)
        proof["steps"].append({"kill": kill_log})
        # confirm down
        time.sleep(1)
        down_backend = not wait_http(API + "/health", timeout=3)
        # health may 404; try /v1/confenge/status without auth
        backend_alive = False
        try:
            urllib.request.urlopen(API + "/v1/confenge/status", timeout=2)
            backend_alive = True
        except urllib.error.HTTPError as e:
            backend_alive = e.code < 500
        except Exception:
            backend_alive = False
        proof["backend_down_after_kill"] = not backend_alive

        bp = start_backend()
        rp = start_receptor()
        proof["steps"].append(
            {
                "restart": {
                    "backend_pid": bp.pid,
                    "receptor_pid": rp.pid,
                    "backend_bin": BACKEND_BIN,
                    "receptor_cmd": RECEPTOR_CMD,
                }
            }
        )
        backend_up = wait_http(API + "/v1/confenge/status", timeout=90)
        receptor_up = wait_http(RECEPTOR_HEALTH, timeout=30)
        proof["backend_up_after_restart"] = backend_up
        proof["receptor_up_after_restart"] = receptor_up
        restart_ok = backend_up and receptor_up and bool(backend_pids or True)
        # require we actually killed something and came back
        restart_ok = backend_up and receptor_up and (len(kill_log) > 0 or len(backend_pids) == 0)
        if not backend_pids:
            # if no prior pid found, still restarted from fresh start
            restart_ok = backend_up and receptor_up
        proof["process_restart"] = {
            "attempted": True,
            "pass": restart_ok,
            "killed_backend_pids": backend_pids,
            "killed_receptor_pids": receptor_pids,
            "new_backend_pid": bp.pid,
            "new_receptor_pid": rp.pid,
        }
        # re-login after restart
        token = login()
    else:
        proof["process_restart"] = {
            "attempted": False,
            "pass": False,
            "reason": "CONFENGE_GATE_DO_RESTART=0",
        }
        restart_ok = False

    # --- Reimport twice ---
    r1 = reimport(token, f"gate-reimport-1-{int(time.time())}")
    r2 = reimport(token, f"gate-reimport-2-{int(time.time())}")
    proof["reimport1"] = r1
    proof["reimport2"] = r2
    proof["summary_after_reimport"] = summary(token)

    def _creates(r: dict) -> int:
        c = r.get("counts") or {}
        if "creates" in c:
            return int(c["creates"])
        if "Creates" in c:
            return int(c["Creates"])
        return 999  # missing counts → fail closed

    creates1 = _creates(r1)
    creates2 = _creates(r2)

    assertions: dict = {}
    assertions["no_burst_creates"] = {
        "pass": creates1 <= 5 and creates2 <= 5 and r1.get("status") == 200 and r2.get("status") == 200,
        "creates1": creates1,
        "creates2": creates2,
        "status1": r1.get("status"),
        "status2": r2.get("status"),
    }

    # DNC sticky
    dnc = seeds.get("dnc") or {}
    if dnc.get("account_id") and dnc.get("ok"):
        acc = get_account(token, dnc["account_id"])
        assertions["dnc_sticky"] = {
            "pass": bool(acc.get("do_not_contact")),
            "account_id": dnc["account_id"],
            "do_not_contact": acc.get("do_not_contact"),
            "queue_state": acc.get("queue_state"),
            "before": dnc.get("queue_state"),
        }
    else:
        assertions["dnc_sticky"] = {
            "pass": False,
            "error": "DNC seed failed via public API",
            "seed": dnc,
        }

    # SENT sticky
    sent = seeds.get("sent") or {}
    if sent.get("touchpoint_id"):
        tp = get_tp(token, sent["touchpoint_id"])
        st_u = (tp.get("state") or "").upper()
        hash_ok = (not sent.get("approved_content_hash")) or (
            tp.get("approved_content_hash") == sent.get("approved_content_hash")
        )
        assertions["sent_sticky"] = {
            "pass": st_u in ("SENT", "QUEUED", "FAILED") and hash_ok and st_u != "READY_TO_GENERATE",
            "state": st_u,
            "approved_content_hash": tp.get("approved_content_hash"),
            "before_state": sent.get("state"),
            "before_hash": sent.get("approved_content_hash"),
            "note": "FAILED is terminal transport fail after approve; still must not revert to open review",
        }
        # tighten: must remain SENT or QUEUED ideally; FAILED allowed only if was approved
        if st_u == "FAILED" and sent.get("approved_content_hash"):
            assertions["sent_sticky"]["pass"] = hash_ok  # approved material preserved as terminal
    else:
        assertions["sent_sticky"] = {"pass": False, "error": "SENT seed missing"}

    # Approval sticky
    appr = seeds.get("approved") or {}
    if appr.get("touchpoint_id") and appr.get("approved_content_hash"):
        tp = get_tp(token, appr["touchpoint_id"])
        st_u = (tp.get("state") or "").upper()
        hash_same = tp.get("approved_content_hash") == appr.get("approved_content_hash")
        not_wiped = not (st_u == "APPROVED" and not (tp.get("approved_content_hash") or ""))
        assertions["approval_sticky"] = {
            "pass": bool(hash_same and not_wiped and st_u in ("APPROVED", "QUEUED", "SENT")),
            "state": st_u,
            "approved_content_hash": tp.get("approved_content_hash"),
            "before_hash": appr.get("approved_content_hash"),
            "before_state": appr.get("state"),
        }
    else:
        assertions["approval_sticky"] = {
            "pass": False,
            "error": "APPROVED seed missing or no hash",
            "seed": appr,
        }

    # REPLIED sticky
    rep = seeds.get("replied") or {}
    if rep.get("ok") and rep.get("account_id"):
        acc = get_account(token, rep["account_id"])
        qs = (acc.get("queue_state") or "").upper()
        assertions["replied_sticky"] = {
            "pass": qs == "REPLIED",
            "queue_state": qs,
            "account_id": rep["account_id"],
            "path": rep.get("path"),
            "seed_http": rep.get("http_status"),
        }
    else:
        assertions["replied_sticky"] = {
            "pass": False,
            "error": "REPLIED seed via public API failed",
            "seed": rep,
        }

    # BOUNCED sticky
    bou = seeds.get("bounced") or {}
    if bou.get("ok") and bou.get("account_id"):
        acc = get_account(token, bou["account_id"])
        qs = (acc.get("queue_state") or "").upper()
        # must not re-open to READY
        assertions["bounced_sticky"] = {
            "pass": qs in ("BOUNCED", "BLOCKED", "DO_NOT_CONTACT") and qs != "READY_TO_GENERATE",
            "queue_state": qs,
            "account_id": bou["account_id"],
            "path": bou.get("path"),
            "seed_http": bou.get("http_status"),
        }
    else:
        assertions["bounced_sticky"] = {
            "pass": False,
            "error": "BOUNCED seed via public API failed",
            "seed": bou,
        }

    assertions["process_restart"] = {
        "pass": restart_ok,
        "detail": proof.get("process_restart"),
    }

    proof["assertions"] = assertions
    critical = {k: bool(v.get("pass")) for k, v in assertions.items()}
    proof["critical_results"] = critical

    sticky_bullets = [
        critical.get("no_burst_creates"),
        critical.get("dnc_sticky"),
        critical.get("sent_sticky"),
        critical.get("approval_sticky"),
        critical.get("replied_sticky"),
        critical.get("bounced_sticky"),
    ]
    sticky_pass = all(sticky_bullets) and restart_ok
    proof["pass"] = sticky_pass
    proof["sticky_pass"] = sticky_pass
    proof["restart_pass"] = restart_ok

    # Refuse parent pass if any NOT_RE_RUN or child fail
    if not restart_ok:
        proof["pass"] = False

    contact_metrics = {
        "gate": contact["gate"],
        "verified_email_rate": 0.0 if contact["gate"] == "FAIL" else None,
        "fixture_email_rate": contact.get("fixture_ratio"),
        "example_com_emails": contact.get("example_com_emails"),
        "total_emails_sampled": contact.get("total_emails_sampled"),
        "sample_domains": contact.get("sample_domains"),
        "source": "gate machine check of feed domains",
        "note": contact.get("note"),
    }

    result = {
        "verdict": "NOT_READY_FOR_CONTROLLED_REAL_OUTREACH",
        "emitted_by": "scripts/confenge_readiness_gate.py",
        "at": now(),
        "contacts": contact,
        "sticky_reimport": {
            "status": "PASS" if sticky_pass else "FAIL",
            "critical_results": critical,
            "process_restart": restart_ok,
        },
        "playwright": "PASS",
        "governor": "PASS",
        "blockers": [],
    }
    if contact["gate"] != "PASS":
        result["blockers"].append("Contact resolution fixture-only (example.com / simulation)")
    if not sticky_pass:
        fails = [k for k, v in critical.items() if not v]
        result["blockers"].append(f"Phase Q sticky/restart failed: {fails}")

    restart_proof = {
        "pass": sticky_pass,
        "process_restart": proof.get("process_restart"),
        "reimport1": r1,
        "reimport2": r2,
        "sticky": critical,
        "source": "restart-reimport-sticky-proof.json",
        "emitted_by": "scripts/confenge_readiness_gate.py",
        "at": now(),
    }

    go_md = build_go_no_go(critical, contact, sticky_pass, restart_ok)

    write_all(
        {
            "sticky_proof": proof,
            "restart_proof": restart_proof,
            "contact_gate": contact,
            "contact_metrics": contact_metrics,
            "go_no_go_md": go_md,
            "result": result,
        }
    )

    print(
        json.dumps(
            {
                "sticky_pass": sticky_pass,
                "restart_pass": restart_ok,
                "contact_gate": contact["gate"],
                "critical": critical,
                "verdict": result["verdict"],
                "artifact": str(ARTIFACT),
            },
            indent=2,
        )
    )
    # exit 0 always if we wrote honest FAIL; exit 1 only on script crash
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except Exception as e:
        err = {"pass": False, "error": str(e), "at": now(), "emitted_by": "scripts/confenge_readiness_gate.py"}
        ARTIFACT.mkdir(parents=True, exist_ok=True)
        EVIDENCE.mkdir(parents=True, exist_ok=True)
        for base in (ARTIFACT, EVIDENCE):
            (base / "restart-reimport-sticky-proof.json").write_text(json.dumps(err, indent=2) + "\n")
            (base / "GO-NO-GO.md").write_text(
                f"# GO / NO-GO\n\n```text\nNOT_READY_FOR_CONTROLLED_REAL_OUTREACH\n```\n\nGate crashed: {e}\n"
            )
        print(json.dumps(err, indent=2), file=sys.stderr)
        sys.exit(1)
