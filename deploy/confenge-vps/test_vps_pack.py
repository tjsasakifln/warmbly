#!/usr/bin/env python3
"""Structural tests for the CONFENGE VPS deployment pack.

Drives real files under deploy/confenge-vps/ (shipped artifacts), not reimplemented policy.
"""

from __future__ import annotations

import re
import subprocess
import unittest
from pathlib import Path

PACK = Path(__file__).resolve().parent
ROOT = PACK.parent.parent


class TestConfengeVpsPack(unittest.TestCase):
    def test_resume_clears_the_ops_volume_kill_switch(self) -> None:
        """up.sh and pause.sh engage the switch on the ops volume, which is what
        the backend reads. resume.sh once cleared only the host mirror, so it
        reported DISPATCH=ACTIVE while every deploy preflight pause stayed on."""
        resume = (PACK / "resume.sh").read_text(encoding="utf-8")
        self.assertIn("confenge-ops/kill-switch", resume)
        self.assertIn("_confenge_ops", resume)
        self.assertRegex(
            resume,
            r"if docker run .*\$OPS_VOLUME:/data:ro.* test -f /data/kill-switch",
        )
        self.assertIn("REFUSE: transport kill switch still engaged after resume", resume)

    def test_required_scripts_exist_and_executable_intent(self) -> None:
        required = [
            "validate.sh",
            "up.sh",
            "down.sh",
            "status.sh",
            "pause.sh",
            "resume.sh",
            "backup.sh",
            "restore.sh",
            "connect-hostinger.sh",
            "prove-hostinger-net.sh",
            "prove-restart.sh",
            "self-smoke.sh",
            "post-smtp-unlock.sh",
            "gen-secrets.sh",
            "install.sh",
            "lib.sh",
            "env.example",
            "docker-compose.override.yml",
            "inbound-edge-install.sh",
            "inbound-edge-monitor.sh",
            "asaas-adapter-install.sh",
            "asaas-adapter.env.example",
            "disk-guard.sh",
            "docker-gc-install.sh",
            "host-disk-report.sh",
            "release-deploy.sh",
            "docker-compose.release.yml",
            "systemd/confenge-docker-gc.service",
            "systemd/confenge-docker-gc.timer",
        ]
        for name in required:
            path = PACK / name
            self.assertTrue(path.is_file(), f"missing {name}")

    def test_env_example_safety_flags(self) -> None:
        text = (PACK / "env.example").read_text(encoding="utf-8")
        self.assertIn("CONFENGE_GREEN_AUTORUN_ENABLED=false", text)
        self.assertIn("CONFENGE_AUTO_SEND_ENABLED=false", text)
        self.assertIn("CONFENGE_REQUIRE_HUMAN_APPROVAL=true", text)
        self.assertIn("CONFENGE_DELEGATED_FIRST_TOUCH_ENABLED=false", text)
        self.assertIn("CONFENGE_DELEGATED_FIRST_TOUCH_AUTORUN_ENABLED=false", text)
        self.assertIn("CONFENGE_DELEGATED_FIRST_TOUCH_RUNWAY_DAYS=30", text)
        self.assertIn("CONFENGE_DRAFT_REVIEW_BACKLOG_TARGET=1000", text)
        self.assertIn("CONFENGE_WHATSAPP_ENABLED=false", text)
        self.assertIn("CONFENGE_RATE_MAX_PER_HOUR=20", text)
        self.assertIn("HOSTINGER_PLAN_CLASS=BUSINESS_EMAIL_STARTER", text)
        self.assertIn("CONFENGE_DEFAULT_CAMPAIGN_DAILY_LIMIT=200", text)
        self.assertIn("TRUSTED_PROXIES=127.0.0.1", text)
        # Must not raise operational max above 20 in this pack
        for m in re.finditer(r"CONFENGE_RATE_MAX_PER_HOUR=(\d+)", text):
            self.assertLessEqual(int(m.group(1)), 20)

    def test_status_helper_renders_enabled_without_false_failure(self) -> None:
        proc = subprocess.run(
            [
                "bash",
                "-c",
                'source "$1"; pass_fail "DELEGATED FIRST TOUCH" ENABLED',
                "status-test",
                str(PACK / "lib.sh"),
            ],
            capture_output=True,
            text=True,
            check=False,
        )
        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertIn("DELEGATED FIRST TOUCH ENABLED", proc.stdout)
        self.assertNotIn("FAIL", proc.stdout)

    def test_status_optional_feed_fields_cannot_abort_the_pack(self) -> None:
        status = (PACK / "status.sh").read_text(encoding="utf-8")
        for helper in ("json_optional_string", "json_optional_uint"):
            body = status.split(f"{helper}() {{", maxsplit=1)[1].split(
                "\n}", maxsplit=1
            )[0]
            self.assertIn("|| true", body)
        for field in (
            "feed_last_success_at",
            "feed_snapshot_hash",
            "feed_authority_state",
            "feed_source_expires_at",
            "target_membership_count",
            "supplier_confirmed_count",
            "feed_last_attempt_at",
            "feed_last_attempt_status",
        ):
            self.assertIn(field, status)

        syntax = subprocess.run(
            ["bash", "-n"],
            input=status,
            capture_output=True,
            text=True,
            check=False,
        )
        self.assertEqual(syntax.returncode, 0, syntax.stderr)

        helpers = status.split("json_optional_string() {", maxsplit=1)[1].split(
            "# BACKEND", maxsplit=1
        )[0]
        probe = "set -euo pipefail\njson_optional_string() {" + helpers + """
payload='{"feed_state":"fresh"}'
test "$(json_optional_string "$payload" feed_state)" = fresh
test -z "$(json_optional_string "$payload" feed_snapshot_hash)"
test -z "$(json_optional_uint "$payload" target_membership_count)"
"""
        optional = subprocess.run(
            ["bash"], input=probe, capture_output=True, text=True, check=False
        )
        self.assertEqual(optional.returncode, 0, optional.stderr)

    def test_provider_vs_operational_documented(self) -> None:
        plane = (ROOT / "docs/confenge/vps-execution-plane.md").read_text(
            encoding="utf-8"
        )
        self.assertIn("provider ceiling ≠ operational target", plane)
        self.assertIn("HOSTINGER_PLAN_CLASS", plane)
        self.assertIn("Business Email Starter", plane)
        self.assertIn("1000", plane)
        self.assertIn("10/h", plane)
        self.assertIn("20/h", plane)
        self.assertNotIn("HOSTINGER_PLAN_CLASS=CPANEL", plane)
        # Must not document cPanel hourly ceiling as this mailbox's plan
        env = (PACK / "env.example").read_text(encoding="utf-8")
        self.assertNotIn("HOSTINGER_PLAN_CLASS=CPANEL", env)
        self.assertIn("BUSINESS_EMAIL_STARTER", env)

    def test_connect_script_uses_read_s_not_argv_password(self) -> None:
        src = (PACK / "connect-hostinger.sh").read_text(encoding="utf-8")
        self.assertIn("read -r -s PASS", src)
        # password must not be passed as curl --data with shell expansion of raw argv pattern
        self.assertNotRegex(src, r"curl.*--password")
        self.assertIn("unset CONFENGE_MAILBOX_PASSWORD", src)
        # JSON body from temp file via --data-binary (never -d "$BODY"; password must not be in argv/ps)
        self.assertIn("--data-binary @", src)
        self.assertNotRegex(src, r'curl[^\n]*-d\s+"\$BODY"')

    def test_no_mta_install(self) -> None:
        for path in PACK.rglob("*"):
            if path.suffix in {".sh", ".yml", ".md", ".example"} and path.is_file():
                text = path.read_text(encoding="utf-8", errors="replace")
                self.assertNotRegex(
                    text,
                    r"apt(-get)?\s+install\s+.*(postfix|exim4|mailcow|mailu)",
                    msg=f"MTA install in {path.name}",
                )

    def test_validate_sh_passes(self) -> None:
        """Run the shipped validate entrypoint (real path)."""
        script = PACK / "validate.sh"
        proc = subprocess.run(
            ["bash", str(script)],
            cwd=str(ROOT),
            capture_output=True,
            text=True,
            timeout=120,
            check=False,
        )
        if proc.returncode != 0:
            self.fail(
                f"validate.sh exit {proc.returncode}\n{proc.stdout}\n{proc.stderr}"
            )
        self.assertIn("VALIDATE=PASS", proc.stdout)

    def test_up_deploys_the_pinned_release_images(self) -> None:
        """A new checkout must not silently reuse the previous app images. The
        release SHA is bound before the images are pulled and the pull is pinned
        to that SHA, so the guarantee the old `--build` gave is preserved
        without compiling on the VPS."""
        text = (PACK / "up.sh").read_text(encoding="utf-8")
        self.assertIn("compose_cmd up -d --no-build --remove-orphans", text)
        self.assertNotIn("up -d --build", text)
        env_load = text.index('set -a; . "$ENVF"; set +a')
        identity_bind = text.index(
            'bind_release_identity "$RELEASE_SHA_RESOLVED"'
        )
        pull = text.index("compose_cmd pull")
        compose_up = text.index("compose_cmd up -d --no-build --remove-orphans")
        self.assertLess(env_load, identity_bind)
        self.assertLess(identity_bind, pull)
        self.assertLess(pull, compose_up)

    def test_release_identity_binding_overrides_stale_audit_sha(self) -> None:
        release_sha = "a" * 40
        stale_sha = "b" * 40
        proc = subprocess.run(
            [
                "bash",
                "-c",
                'source "$1"; WARMBLY_RELEASE_SHA="$2"; '
                'CONFENGE_REPOSITORY_SHA="$3"; '
                'bind_release_identity "$WARMBLY_RELEASE_SHA"; '
                'printf "%s\\n%s\\n" "$WARMBLY_RELEASE_SHA" "$CONFENGE_REPOSITORY_SHA"',
                "release-identity-test",
                str(PACK / "lib.sh"),
                release_sha,
                stale_sha,
            ],
            capture_output=True,
            text=True,
            check=False,
        )
        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertEqual(proc.stdout.splitlines(), [release_sha, release_sha])

    def test_release_identity_binding_rejects_unproven_revision(self) -> None:
        proc = subprocess.run(
            [
                "bash",
                "-c",
                'source "$1"; bind_release_identity local',
                "release-identity-test",
                str(PACK / "lib.sh"),
            ],
            capture_output=True,
            text=True,
            check=False,
        )
        self.assertEqual(proc.returncode, 3)
        self.assertIn("REFUSE: immutable release SHA", proc.stderr)

    def test_compose_maintenance_rebinds_decision_audit_sha(self) -> None:
        text = (PACK / "lib.sh").read_text(encoding="utf-8")
        compose_body = text.split("compose_cmd() {", maxsplit=1)[1].split(
            "\n}", maxsplit=1
        )[0]
        identity_bind = compose_body.index(
            'bind_release_identity "${WARMBLY_RELEASE_SHA:-}"'
        )
        docker_compose = compose_body.index("args=(docker compose)")
        self.assertLess(identity_bind, docker_compose)

    def test_release_verifier_requires_decision_audit_sha(self) -> None:
        text = (ROOT / "deploy/verify-release.sh").read_text(encoding="utf-8")
        self.assertIn("CONFENGE_REPOSITORY_SHA", text)
        self.assertIn('if [ "$auditsha" != "$EXPECTED" ]', text)

    def test_inbound_edge_nginx_allowlist_is_the_shipped_config(self) -> None:
        """Drive the real nginx files that install.sh copies onto the VPS."""
        https = (PACK / "nginx/site-https.conf").read_text(encoding="utf-8")
        http = (PACK / "nginx/site-http.conf").read_text(encoding="utf-8")
        params = (PACK / "nginx/http-params.conf").read_text(encoding="utf-8")
        proxy = (PACK / "nginx/proxy-params.conf").read_text(encoding="utf-8")
        install = (PACK / "inbound-edge-install.sh").read_text(encoding="utf-8")
        monitor = (PACK / "inbound-edge-monitor.sh").read_text(encoding="utf-8")
        wait_dns = (PACK / "inbound-edge-wait-dns.sh").read_text(encoding="utf-8")

        self.assertIn("server_name api.confenge.com.br;", https)
        self.assertIn("location = /api/v1/webhooks/confenge/inbound/health", https)
        self.assertIn("location = /api/v1/webhooks/confenge/inbound {", https)
        self.assertIn("location = /api/v1/webhooks/asaas {", https)
        self.assertIn("location = /api/v1/webhooks/asaas/health {", https)
        self.assertIn("server 127.0.0.1:8791;", params)
        self.assertIn("proxy_pass http://warmbly_loopback;", https)
        self.assertIn("server 127.0.0.1:8080;", params)
        self.assertIn("limit_req zone=confenge_inbound", https)
        self.assertIn("limit_req_status 429", https)
        self.assertIn("client_max_body_size 1m;", https)
        self.assertIn("proxy_connect_timeout 5s;", proxy)
        self.assertIn("proxy_read_timeout 30s;", proxy)
        self.assertIn("X-Forwarded-For $remote_addr", proxy)
        self.assertIn("return 404;", https)
        self.assertIn("return 444;", https)
        self.assertIn("Strict-Transport-Security", https)
        self.assertNotIn("includeSubDomains", https.split("add_header", 1)[-1])
        self.assertIn("return 301 https://api.confenge.com.br", http)
        self.assertIn("location ^~ /.well-known/acme-challenge/", http)
        self.assertNotRegex(http, r"proxy_pass")
        self.assertNotRegex(
            params, r"\$request_body|\$http_x_warmbly_signature|\$args|\$query_string"
        )
        for blob in (https, http, proxy):
            self.assertNotRegex(
                blob, r"\$request_body|\$http_x_warmbly_signature|\$query_string"
            )
            self.assertNotIn("location /confenge", blob)
            self.assertNotIn("location /admin", blob)
            self.assertNotRegex(blob, r"listen\s+8080")
            self.assertNotRegex(blob, r"listen\s+15432")
        self.assertIn("ufw allow 80/tcp", install)
        self.assertIn("ufw allow 443/tcp", install)
        self.assertNotIn("ufw allow 8080", install)
        self.assertNotIn("ufw allow 15432", install)
        self.assertNotIn("CONFENGE_AUTO_SEND_ENABLED=true", install)
        self.assertIn(
            "/opt/warmbly-confenge/deploy/confenge-vps/inbound-edge-install.sh",
            wait_dns,
        )
        self.assertIn("confenge_inbound_hmac_fail_total", monitor)
        self.assertIn("confenge_inbound_replay_total", monitor)
        self.assertIn("public_health_not_ready", monitor)
        self.assertNotIn("CONFENGE_INBOUND_WEBHOOK_SECRET", monitor)

        unit = (PACK / "systemd/confenge-asaas-adapter.service").read_text(
            encoding="utf-8"
        )
        self.assertIn("DynamicUser=yes", unit)
        self.assertIn("StateDirectoryMode=0700", unit)
        self.assertIn("UMask=0077", unit)
        self.assertIn("NoNewPrivileges=yes", unit)

    def test_asaas_adapter_is_persist_first_and_backup_aware(self) -> None:
        source = (PACK / "asaas-adapter/adapter.py").read_text(encoding="utf-8")
        backup = (PACK / "backup.sh").read_text(encoding="utf-8")
        restore = (PACK / "restore.sh").read_text(encoding="utf-8")
        self.assertIn("asaas-access-token", source)
        self.assertIn("INSERT OR IGNORE INTO events", source)
        self.assertIn("warmbly_semantic_hold", source)
        self.assertIn("asaas-events.sqlite3", backup)
        self.assertIn("asaas-events.sqlite3", restore)

        override = (PACK / "docker-compose.override.yml").read_text(encoding="utf-8")
        self.assertIn("TRUSTED_PROXIES: ${TRUSTED_PROXIES:-127.0.0.1}", override)
        self.assertIn("127.0.0.1:8080:8080", override)
        self.assertIn("127.0.0.1:15432:5432", override)

    def test_docs_inventory_exists(self) -> None:
        inv = ROOT / "docs/confenge/vps-execution-inventory.md"
        self.assertTrue(inv.is_file())
        text = inv.read_text(encoding="utf-8")
        self.assertIn("159.195.18.88", text)
        self.assertIn("warmbly-confenge", text)
        self.assertIn("BUSINESS_EMAIL_STARTER", text)
        self.assertIn("1000", text)
        # network premise recorded (SMTP egress may FAIL on Netcup until unlock)
        self.assertTrue("smtp" in text.lower() and "imap" in text.lower())

    def test_feed_manifest_uses_atomic_current_publication(self) -> None:
        env = (PACK / "env.example").read_text(encoding="utf-8")
        override = (PACK / "docker-compose.override.yml").read_text(encoding="utf-8")
        self.assertIn(
            "CONFENGE_EXTRA_CLI_MANIFEST_URL=https://confenge-feed:8443/current/manifest.json",
            env,
        )
        self.assertIn(
            "CONFENGE_EXTRA_CLI_MANIFEST_URL:-https://host.docker.internal:8443/current/manifest.json",
            override,
        )


if __name__ == "__main__":
    unittest.main()
