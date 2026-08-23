#!/usr/bin/env python3
"""Structural tests for the CONFENGE VPS deployment pack.

Drives real files under deploy/confenge-vps/ (shipped artifacts), not reimplemented policy.
"""
from __future__ import annotations

import re
import subprocess
import sys
import unittest
from pathlib import Path

PACK = Path(__file__).resolve().parent
ROOT = PACK.parent.parent


class TestConfengeVpsPack(unittest.TestCase):
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
        ]
        for name in required:
            path = PACK / name
            self.assertTrue(path.is_file(), f"missing {name}")

    def test_env_example_safety_flags(self) -> None:
        text = (PACK / "env.example").read_text(encoding="utf-8")
        self.assertIn("CONFENGE_GREEN_AUTORUN_ENABLED=false", text)
        self.assertIn("CONFENGE_AUTO_SEND_ENABLED=false", text)
        self.assertIn("CONFENGE_REQUIRE_HUMAN_APPROVAL=true", text)
        self.assertIn("CONFENGE_WHATSAPP_ENABLED=false", text)
        self.assertIn("CONFENGE_RATE_MAX_PER_HOUR=20", text)
        self.assertIn("HOSTINGER_PLAN_CLASS=BUSINESS_EMAIL_STARTER", text)
        self.assertIn("CONFENGE_DEFAULT_CAMPAIGN_DAILY_LIMIT=200", text)
        self.assertIn("TRUSTED_PROXIES=127.0.0.1", text)
        # Must not raise operational max above 20 in this pack
        for m in re.finditer(r"CONFENGE_RATE_MAX_PER_HOUR=(\d+)", text):
            self.assertLessEqual(int(m.group(1)), 20)

    def test_provider_vs_operational_documented(self) -> None:
        plane = (ROOT / "docs/confenge/vps-execution-plane.md").read_text(encoding="utf-8")
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
        )
        if proc.returncode != 0:
            self.fail(f"validate.sh exit {proc.returncode}\n{proc.stdout}\n{proc.stderr}")
        self.assertIn("VALIDATE=PASS", proc.stdout)

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
        self.assertNotIn('includeSubDomains', https.split("add_header", 1)[-1])
        self.assertIn("return 301 https://api.confenge.com.br", http)
        self.assertIn("location ^~ /.well-known/acme-challenge/", http)
        self.assertNotRegex(http, r"proxy_pass")
        self.assertNotRegex(params, r"\$request_body|\$http_x_warmbly_signature|\$args|\$query_string")
        for blob in (https, http, proxy):
            self.assertNotRegex(blob, r"\$request_body|\$http_x_warmbly_signature|\$query_string")
            self.assertNotIn("location /confenge", blob)
            self.assertNotIn("location /admin", blob)
            self.assertNotRegex(blob, r"listen\s+8080")
            self.assertNotRegex(blob, r"listen\s+15432")
        self.assertIn("ufw allow 80/tcp", install)
        self.assertIn("ufw allow 443/tcp", install)
        self.assertNotIn("ufw allow 8080", install)
        self.assertNotIn("ufw allow 15432", install)
        self.assertNotIn("CONFENGE_AUTO_SEND_ENABLED=true", install)
        self.assertIn("/opt/warmbly-confenge/deploy/confenge-vps/inbound-edge-install.sh", wait_dns)
        self.assertIn("confenge_inbound_hmac_fail_total", monitor)
        self.assertIn("confenge_inbound_replay_total", monitor)
        self.assertIn("public_health_not_ready", monitor)
        self.assertNotIn("CONFENGE_INBOUND_WEBHOOK_SECRET", monitor)

        unit = (PACK / "systemd/confenge-asaas-adapter.service").read_text(encoding="utf-8")
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


if __name__ == "__main__":
    unittest.main()
