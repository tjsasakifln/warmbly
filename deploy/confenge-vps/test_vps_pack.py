#!/usr/bin/env python3
"""Structural tests for the CONFENGE VPS deployment pack.

Drives real files under deploy/confenge-vps/ (shipped artifacts), not reimplemented policy.
"""

from __future__ import annotations

import os
import re
import subprocess
import tempfile
import unittest
from pathlib import Path

PACK = Path(__file__).resolve().parent
ROOT = PACK.parent.parent

# Fake `docker` for the kill-switch steps of up.sh: a named volume is a directory
# under $STUB_VOLUMES and `docker run --rm -v NAME:/data[:ro] alpine <cmd...>`
# executes <cmd> on the host with /data rewritten to that directory. Everything
# else is logged and succeeds, so the real script text drives the test.
# Docker itself can fail: $DOCKER_FAIL_PROBE=<n> makes the n-th presence probe
# (any `run --rm` whose command carries `echo present`) exit 125 with no stdout,
# the way a daemon, image or mount error looks from the calling script.
KILL_SWITCH_DOCKER_STUB = r"""#!/usr/bin/env bash
echo "$*" >> "$DOCKER_LOG"
case "$1 $2" in
  "volume create") mkdir -p "$STUB_VOLUMES/$3"; exit 0 ;;
  "run --rm")
    if [[ "$*" == *"echo present"* ]]; then
      n="$(grep -c 'echo present' "$DOCKER_LOG")"
      if [[ -n "${DOCKER_FAIL_PROBE:-}" && "$n" == "$DOCKER_FAIL_PROBE" ]]; then
        echo "docker: Cannot connect to the Docker daemon (stub)" >&2
        exit 125
      fi
    fi
    shift 2 ;;
  *) exit 0 ;;
esac
vol=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    -v) vol="${2%%:*}"; shift 2 ;;
    alpine) shift; break ;;
    *) shift ;;
  esac
done
dir="$STUB_VOLUMES/$vol"
args=()
for a in "$@"; do args+=("${a//\/data/$dir}"); done
exec "${args[@]}"
"""

DEPLOY_PREFLIGHT_SWITCH = "paused\nreason=deploy_preflight\n"
OPERATOR_SWITCH = "paused\nreason=operator_keep_paused_for_261\n"


def up_step(text: str, number: int) -> str:
    """The real text of one numbered `# ── N.` block of up.sh."""
    start = text.index(f"# ── {number}.")
    end = text.index(f"# ── {number + 1}.")
    return text[start:end]


def run_up_kill_switch_steps(
    tmp: Path,
    volume_switch: str | None,
    host_mirror: str | None,
    docker_fail_probe: int | None = None,
) -> tuple[subprocess.CompletedProcess[str], Path, Path]:
    """Execute up.sh step 4 (engage the deploy pause) and step 8 (clear it) as
    shipped, against a fake ops volume seeded with `volume_switch` and a host
    mirror seeded with `host_mirror`. Steps 5-7 (compose up, health, release
    verification) are the part a deploy cannot fake and are irrelevant to who
    owns the switch, so they are skipped. `docker_fail_probe=n` makes the n-th
    presence probe fail at the Docker level (exit 125, no output)."""
    bindir = tmp / "bin"
    bindir.mkdir(exist_ok=True)
    (bindir / "docker").write_text(KILL_SWITCH_DOCKER_STUB)
    # The real payload chowns the switch to uid 1000; on the host that is a no-op.
    (bindir / "chown").write_text("#!/usr/bin/env bash\nexit 0\n")
    for name in ("docker", "chown"):
        (bindir / name).chmod(0o755)
    volumes = tmp / "volumes"
    volume_dir = volumes / "warmbly-confenge_confenge_ops"
    volume_dir.mkdir(parents=True)
    switch = volume_dir / "kill-switch"
    if volume_switch is not None:
        switch.write_text(volume_switch)
    mirror = tmp / "host" / "confenge-kill-switch"
    mirror.parent.mkdir()
    if host_mirror is not None:
        mirror.write_text(host_mirror)

    up = (PACK / "up.sh").read_text(encoding="utf-8")
    probe = (
        "set -euo pipefail\n"
        f'ROOT="{tmp}"\n'
        "COMPOSE_PROJECT_NAME=warmbly-confenge\n"
        + up_step(up, 4)
        + up_step(up, 8)
    )
    env = dict(os.environ)
    env["PATH"] = f"{bindir}:{env['PATH']}"
    env["DOCKER_LOG"] = str(tmp / "docker.log")
    env["STUB_VOLUMES"] = str(volumes)
    env["CONFENGE_KILL_SWITCH_HOST_PATH"] = str(mirror)
    # Step 4's text carries the first-boot seed block; never let it run here.
    env["CONFENGE_VPS_SEED"] = "false"
    if docker_fail_probe is not None:
        env["DOCKER_FAIL_PROBE"] = str(docker_fail_probe)
    (tmp / "docker.log").write_text("")
    proc = subprocess.run(
        ["bash"], input=probe, capture_output=True, text=True, env=env,
        timeout=60, check=False,
    )
    return proc, switch, mirror


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

    def test_up_leaves_a_preexisting_operator_pause_untouched(self) -> None:
        """Workstream H invariant: a deploy must never destroy an operator pause.

        Counter-case this pins: step 4 unconditionally overwrote the ops-volume
        switch with reason=deploy_preflight, so step 8 always saw its own reason
        and cleared it. The `held` branch was unreachable and an operator pause
        written by pause.sh (reason=operator_keep_paused_for_261) was silently
        destroyed by every deploy."""
        with tempfile.TemporaryDirectory() as d:
            tmp = Path(d)
            mirror_body = OPERATOR_SWITCH + "at=2026-09-15T00:00:00Z\n"
            proc, switch, mirror = run_up_kill_switch_steps(
                tmp, volume_switch=OPERATOR_SWITCH, host_mirror=mirror_body
            )
            self.assertEqual(proc.returncode, 0, proc.stdout + proc.stderr)
            self.assertIn(
                "DISPATCH_PAUSE=preexisting reason=operator_keep_paused_for_261",
                proc.stdout,
            )
            self.assertIn("DISPATCH_PAUSE=held", proc.stdout)
            self.assertNotIn("DISPATCH_PAUSE=cleared", proc.stdout)
            self.assertEqual(switch.read_text(), OPERATOR_SWITCH)
            self.assertEqual(mirror.read_text(), mirror_body)
            log = (tmp / "docker.log").read_text()
            self.assertNotIn("deploy_preflight", log)
            self.assertNotIn("rm -f", log)

    def test_up_leaves_a_switch_without_a_reason_untouched(self) -> None:
        """The backend pauses on file presence alone (parseKillSwitchContent
        sets Present for any readable body), so a switch with no reason line,
        even a zero-byte one, is still a pause the deploy did not write."""
        for seed in ("paused\n", ""):
            with self.subTest(seed=seed), tempfile.TemporaryDirectory() as d:
                tmp = Path(d)
                proc, switch, _ = run_up_kill_switch_steps(
                    tmp, volume_switch=seed, host_mirror=None
                )
                self.assertEqual(proc.returncode, 0, proc.stdout + proc.stderr)
                self.assertIn("DISPATCH_PAUSE=preexisting reason=<none>", proc.stdout)
                self.assertIn("DISPATCH_PAUSE=held", proc.stdout)
                self.assertNotIn("DISPATCH_PAUSE=cleared", proc.stdout)
                self.assertTrue(switch.exists())
                self.assertEqual(switch.read_text(), seed)

    def test_up_engages_and_clears_its_own_deploy_pause(self) -> None:
        """No pre-existing switch (or a stale deploy_preflight one left by an
        aborted deploy): step 4 writes deploy_preflight, step 8 clears it."""
        for seed in (None, DEPLOY_PREFLIGHT_SWITCH):
            with self.subTest(seed=seed), tempfile.TemporaryDirectory() as d:
                tmp = Path(d)
                proc, switch, mirror = run_up_kill_switch_steps(
                    tmp, volume_switch=seed, host_mirror=None
                )
                self.assertEqual(proc.returncode, 0, proc.stdout + proc.stderr)
                self.assertNotIn("DISPATCH_PAUSE=preexisting", proc.stdout)
                self.assertIn("DISPATCH_PAUSE=cleared (deploy_preflight)", proc.stdout)
                self.assertFalse(switch.exists())
                self.assertFalse(mirror.exists())
                log = (tmp / "docker.log").read_text()
                self.assertIn("reason=deploy_preflight", log)
                self.assertIn("chmod 600", log)

    def test_up_never_removes_a_host_mirror(self) -> None:
        """The host mirror is inspection metadata owned by pause.sh/resume.sh.
        up.sh never writes one, so it has no mirror of its own to clean up:
        whatever reason a mirror carries, a deploy leaves it exactly as found."""
        for body in (
            OPERATOR_SWITCH + "at=2026-09-15T00:00:00Z\n",
            DEPLOY_PREFLIGHT_SWITCH + "at=2026-09-15T00:00:00Z\n",
        ):
            with self.subTest(mirror=body), tempfile.TemporaryDirectory() as d:
                tmp = Path(d)
                proc, switch, mirror = run_up_kill_switch_steps(
                    tmp, volume_switch=None, host_mirror=body
                )
                self.assertEqual(proc.returncode, 0, proc.stdout + proc.stderr)
                self.assertIn("DISPATCH_PAUSE=cleared", proc.stdout)
                self.assertFalse(switch.exists())
                self.assertEqual(mirror.read_bytes(), body.encode(), "mirror must survive")
        up = (PACK / "up.sh").read_text(encoding="utf-8")
        self.assertNotIn("HOST_KS", up)
        for line in up.splitlines():
            if "rm -f" in line:
                self.assertIn("docker run", line, f"host-side rm in up.sh: {line!r}")

    def test_up_refuses_to_write_the_deploy_pause_when_the_probe_is_indeterminate(self) -> None:
        """A Docker-level failure of the step-4 presence probe (exit 125: daemon,
        image or mount error) is not `absent`. Writing deploy_preflight blind
        would overwrite an operator pause that step 8 then clears, so the deploy
        refuses before writing anything."""
        for seed in (OPERATOR_SWITCH, "", None, DEPLOY_PREFLIGHT_SWITCH):
            with self.subTest(seed=seed), tempfile.TemporaryDirectory() as d:
                tmp = Path(d)
                proc, switch, _ = run_up_kill_switch_steps(
                    tmp, volume_switch=seed, host_mirror=None, docker_fail_probe=1
                )
                self.assertNotEqual(proc.returncode, 0, proc.stdout + proc.stderr)
                self.assertIn("REFUSE: could not determine whether a kill switch is present", proc.stderr)
                self.assertNotIn("DISPATCH_PAUSE=", proc.stdout)
                if seed is None:
                    self.assertFalse(switch.exists(), "nothing may be written blind")
                else:
                    self.assertEqual(switch.read_text(), seed)
                log = (tmp / "docker.log").read_text()
                self.assertNotIn("reason=deploy_preflight", log)
                self.assertNotIn("rm -f", log)

    def test_up_leaves_the_switch_alone_when_the_step8_probe_is_indeterminate(self) -> None:
        """Healthy step 4 (deploy_preflight written), then the step-8 presence
        probe fails at the Docker level. The old `if docker run ... test -f`
        collapsed that into `absent` and the deploy exited 0 with the pause
        still on disk, which is the incident resume.sh documents. Now the deploy
        still completes (exit 0) but says DISPATCH_PAUSE=indeterminate, warns on
        stderr and does not touch the switch."""
        with tempfile.TemporaryDirectory() as d:
            tmp = Path(d)
            proc, switch, _ = run_up_kill_switch_steps(
                tmp, volume_switch=None, host_mirror=None, docker_fail_probe=2
            )
            self.assertEqual(proc.returncode, 0, proc.stdout + proc.stderr)
            self.assertIn("DISPATCH_PAUSE=indeterminate", proc.stdout)
            self.assertNotIn("DISPATCH_PAUSE=absent", proc.stdout)
            self.assertNotIn("DISPATCH_PAUSE=cleared", proc.stdout)
            self.assertIn("WARNING:", proc.stderr)
            self.assertIn("NOT cleared", proc.stderr)
            self.assertEqual(switch.read_text(), DEPLOY_PREFLIGHT_SWITCH)
            log = (tmp / "docker.log").read_text()
            self.assertNotIn("rm -f", log)

    def test_up_refuses_when_the_post_clear_probe_is_indeterminate(self) -> None:
        """After `rm -f`, only a confirmed absence counts as cleared. A probe
        that cannot answer must not be reported as `cleared`."""
        with tempfile.TemporaryDirectory() as d:
            tmp = Path(d)
            proc, _, _ = run_up_kill_switch_steps(
                tmp, volume_switch=None, host_mirror=None, docker_fail_probe=3
            )
            self.assertNotEqual(proc.returncode, 0, proc.stdout + proc.stderr)
            self.assertIn("REFUSE: deploy pause could not be confirmed cleared", proc.stderr)
            self.assertNotIn("DISPATCH_PAUSE=cleared", proc.stdout)

    def test_up_reads_the_switch_before_writing_it(self) -> None:
        """Static guard for the same invariant: the three-state read, the
        indeterminate refusal and the reason check precede the only
        deploy_preflight write; step 8 keeps the `held` branch reachable for a
        non-deploy reason and never touches the host mirror."""
        up = (PACK / "up.sh").read_text(encoding="utf-8")
        probe = "sh -c 'test -f /data/kill-switch && echo present || echo absent'"
        step4 = up_step(up, 4)
        read = step4.index(probe)
        refuse = step4.index('[[ "$KS_BEFORE_STATE" == "indeterminate" ]]')
        guard = step4.index(
            '"$KS_BEFORE_STATE" == "present" && "$KS_BEFORE_REASON" != "deploy_preflight"'
        )
        write = step4.index('printf "paused\\nreason=deploy_preflight\\n" > /data/kill-switch')
        self.assertLess(read, refuse)
        self.assertLess(refuse, guard)
        self.assertLess(guard, write)
        self.assertIn("DISPATCH_PAUSE=preexisting", step4)
        self.assertNotIn("alpine test -f", step4, "two-state probe must not come back")
        step8 = up_step(up, 8)
        self.assertEqual(step8.count(probe), 2, "presence probe and post-clear probe")
        self.assertIn('[[ "$KS_STATE" == "indeterminate" ]]', step8)
        self.assertIn("DISPATCH_PAUSE=indeterminate", step8)
        self.assertIn("DISPATCH_PAUSE=held", step8)
        self.assertNotIn("alpine test -f", step8, "two-state probe must not come back")
        self.assertNotIn("HOST_KS", step8)

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
            "feed_last_attempt_error",
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

        self.assertIn('printenv CONFENGE_REPOSITORY_SHA', status)
        self.assertIn('if [[ "$SHA_MATCH" != "PASS" ]]; then STATUS_EXIT=1; fi', status)
        self.assertIn('exit "$STATUS_EXIT"', status)
        self.assertIn("redact_status_error", status)
        redactor = status.split("redact_status_error() {", maxsplit=1)[1].split(
            "\n}", maxsplit=1
        )[0]
        probe = "redact_status_error() {" + redactor + "\n}\n" + r"""
printf '%s' 'fetch https://feed.example/x token=abc lead@example.com' | redact_status_error
"""
        redacted = subprocess.run(
            ["bash"], input=probe, capture_output=True, text=True, check=False
        )
        self.assertEqual(redacted.returncode, 0, redacted.stderr)
        self.assertNotIn("feed.example", redacted.stdout)
        self.assertNotIn("abc", redacted.stdout)
        self.assertNotIn("lead@example.com", redacted.stdout)

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

    def test_public_handraiser_readback_route_is_allowlisted_and_narrow(self) -> None:
        """warmbly#268: the correlated HMAC readback must be reachable on the public
        edge, and reachable ONLY as an authenticated single-item GET.

        Counter-case this pins: before the allowlist existed, the backend served
        GET /api/v1/webhooks/confenge/inbound/handraisers/:logicalId on loopback
        while the public edge fell through to `location /` and returned 404, which
        kept web-cfg's adaptive intake fail-closed (WITHHELD).
        """
        https = (PACK / "nginx/site-https.conf").read_text(encoding="utf-8")

        prefix = "/api/v1/webhooks/confenge/inbound/handraisers/"
        header = "location ^~ %s {" % prefix
        # Prefix match, not exact: the logical id is the trailing path segment.
        self.assertIn(header, https)

        start = https.index(header)
        block = https[start : https.index("\n    }", start)]

        # Read-only: no POST/PUT/PATCH/DELETE may reach the producer through here.
        self.assertIn("limit_except GET HEAD {", block)
        self.assertIn("deny all;", block)
        # No query string: the logical id and the signature are the whole credential,
        # and PII must never land in the query string or the access log.
        self.assertIn('if ($args != "") { return 400; }', block)
        # Same abuse protection and same hardened proxy snippet as the inbound POST.
        self.assertIn("limit_req zone=confenge_inbound", block)
        self.assertIn("limit_req_status 429;", block)
        self.assertIn(
            "include /etc/nginx/snippets/confenge-inbound-proxy.conf;", block
        )
        self.assertIn("proxy_pass http://warmbly_loopback;", block)

        # No collection-level route is allowlisted here. Measured against the deployed
        # edge: `/handraisers` (no trailing slash) gets a 301 from nginx itself
        # (access log shows `upstream=-`) to the slash form, which the app then 404s
        # because routes.go registers no collection handler. So listing is impossible
        # because the route does not exist, not because this prefix fails to match.
        self.assertNotIn("location ^~ /api/v1/webhooks/confenge/inbound/handraisers {", https)
        self.assertNotIn("location = /api/v1/webhooks/confenge/inbound/handraisers ", https)
        # The signature must never be logged or reflected by the edge. `$args` appears
        # only inside the reject guard above, never proxied or logged, so it is excluded
        # from this assertion deliberately.
        self.assertNotRegex(block, r"\$http_x_warmbly_signature|\$query_string")
        self.assertNotIn("proxy_set_header", block)

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
