#!/usr/bin/env python3
"""Tests for the CONFENGE VPS deploy/disk failure mode.

ec-prod filled its root filesystem during a deploy: the build context carried
4.3 GB of data/backups, the VPS rebuilt images locally on every deploy, builder
cache grew to ~174 GB unbounded, Postgres could not extend files and the backend
restart-looped on migrations.

These drive the real shipped scripts with stubbed `docker`/`df` so the policy is
tested, not a reimplementation of it.
"""

from __future__ import annotations

import os
import re
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

PACK = Path(__file__).resolve().parent
ROOT = PACK.parent.parent

DOCKER_STUB = r"""#!/usr/bin/env bash
echo "$*" >> "$DOCKER_LOG"
case "$1 $2" in
  "info --format") echo "${STUB_DOCKER_ROOT:-/}" ;;
  "system df")     echo "Build Cache 90 0 17.83GB 17.83GB" ;;
  "buildx du")     echo "Total:		${STUB_BUILDER_TOTAL:-17.83GB}" ;;
  "image ls")      printf '%s\n' "${STUB_IMAGES:-}" ;;
  "image inspect") exit "${STUB_INSPECT_RC:-0}" ;;
esac
exit 0
"""

DF_STUB = r"""#!/usr/bin/env bash
# df -P -k <path>
echo "Filesystem 1024-blocks Used Available Capacity Mounted on"
total="${STUB_TOTAL_KB:-527000000}"
avail="${STUB_AVAIL_KB:-152000000}"
echo "/dev/vda4 $total $((total - avail)) $avail 71% /"
"""



def strip_comments(text: str) -> str:
    """Policy assertions target executable lines, not the prose documenting them."""
    return "\n".join(
        line for line in text.splitlines() if not line.lstrip().startswith("#")
    )


def stub_env(tmp: Path, **overrides: str) -> dict[str, str]:
    bindir = tmp / "bin"
    bindir.mkdir(exist_ok=True)
    (bindir / "docker").write_text(DOCKER_STUB)
    (bindir / "df").write_text(DF_STUB)
    for name in ("docker", "df"):
        (bindir / name).chmod(0o755)
    env = dict(os.environ)
    env["PATH"] = f"{bindir}:{env['PATH']}"
    env["DOCKER_LOG"] = str(tmp / "docker.log")
    env["CONFENGE_RELEASE_STATE_DIR"] = str(tmp / "state")
    (tmp / "docker.log").write_text("")
    env.update(overrides)
    return env


def run_guard(env: dict[str, str], *args: str) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        ["bash", str(PACK / "disk-guard.sh"), *args],
        capture_output=True,
        text=True,
        env=env,
        timeout=60,
        check=False,
    )


def docker_log(env: dict[str, str]) -> str:
    return Path(env["DOCKER_LOG"]).read_text()


class TestDiskPreflight(unittest.TestCase):
    def test_insufficient_disk_fails_before_service_mutation(self) -> None:
        """The whole point: refuse while the healthy release is still running."""
        with tempfile.TemporaryDirectory() as d:
            tmp = Path(d)
            # 5 GB free against a 20 GB deploy budget + 20 GB Postgres reserve.
            env = stub_env(tmp, STUB_AVAIL_KB=str(5 * 1024 * 1024))
            proc = run_guard(env, "preflight", "a" * 40)

            self.assertEqual(proc.returncode, 4, proc.stdout + proc.stderr)
            self.assertIn("REFUSE: insufficient disk headroom", proc.stderr)
            self.assertIn("No service was recreated", proc.stderr)

            log = docker_log(env)
            for mutation in ("up ", "stop ", "restart ", "rm -f", "compose"):
                self.assertNotIn(
                    mutation, log, f"preflight touched production via: {mutation}"
                )

    def test_sufficient_disk_passes_without_cleanup(self) -> None:
        with tempfile.TemporaryDirectory() as d:
            tmp = Path(d)
            env = stub_env(tmp, STUB_AVAIL_KB=str(145 * 1024 * 1024))
            proc = run_guard(env, "preflight", "a" * 40)
            self.assertEqual(proc.returncode, 0, proc.stdout + proc.stderr)
            self.assertIn("DISK_PREFLIGHT=PASS", proc.stdout)
            self.assertNotIn("prune", docker_log(env))

    def test_reserved_postgres_headroom_is_not_part_of_the_deploy_budget(self) -> None:
        """25 GB free clears a 20 GB deploy on its own, but not once Postgres'
        20 GB reserve is excluded. Postgres must never be the thing that runs
        out of space because a deploy took the last gigabytes."""
        with tempfile.TemporaryDirectory() as d:
            tmp = Path(d)
            env = stub_env(tmp, STUB_AVAIL_KB=str(25 * 1024 * 1024))
            proc = run_guard(env, "preflight", "a" * 40)
            self.assertEqual(proc.returncode, 4)
            self.assertIn("Postgres reserve", proc.stderr)


class TestCleanupSafety(unittest.TestCase):
    FORBIDDEN = [
        "volume rm",
        "volume prune",
        "system prune",
        "image prune -a",
        "image prune --all",
        "builder prune -af",
        "builder prune -a ",
        "-v --rmi",
        "down --volumes",
    ]

    def test_cleanup_never_targets_persistent_volumes(self) -> None:
        source = strip_comments((PACK / "disk-guard.sh").read_text())
        for bad in self.FORBIDDEN:
            self.assertNotIn(
                bad,
                source,
                f"disk-guard.sh must never issue `{bad}`: postgres data, the "
                "CONFENGE ops and key volumes and blobs live in named volumes",
            )

    def test_cleanup_run_issues_no_volume_command(self) -> None:
        with tempfile.TemporaryDirectory() as d:
            tmp = Path(d)
            env = stub_env(tmp, STUB_AVAIL_KB=str(2 * 1024 * 1024))
            run_guard(env, "preflight", "a" * 40)
            log = docker_log(env)
            self.assertIn("builder prune", log)
            for line in log.splitlines():
                self.assertNotIn("volume", line, f"cleanup touched volumes: {line}")

    def test_cleanup_policy_is_bounded_not_blanket(self) -> None:
        """`docker builder prune -af` is the manual emergency, not the policy."""
        with tempfile.TemporaryDirectory() as d:
            tmp = Path(d)
            env = stub_env(tmp)
            run_guard(env, "retain", "a" * 40)
            log = docker_log(env)
            prunes = [ln for ln in log.splitlines() if ln.startswith("builder prune")]
            self.assertTrue(prunes, "retention must bound the builder cache")
            for line in prunes:
                self.assertNotRegex(line, r"(^|\s)-af(\s|$)")

            # The size cap must not be gated by the age filter: entries younger
            # than the age would then be exempt and the cache would sit above
            # the cap forever while the sweep still reported success.
            capped = [ln for ln in prunes if "--keep-storage" in ln]
            self.assertTrue(capped, "retention must cap the builder cache size")
            for line in capped:
                self.assertNotIn("--filter", line)
            self.assertTrue(
                any("--filter" in ln and "until=" in ln for ln in prunes),
                "retention must also drop stale cache by age",
            )

    def test_dangling_prune_is_never_the_all_variant(self) -> None:
        with tempfile.TemporaryDirectory() as d:
            tmp = Path(d)
            env = stub_env(tmp)
            run_guard(env, "retain", "a" * 40)
            for line in docker_log(env).splitlines():
                if line.startswith("image prune"):
                    self.assertNotIn("-a", line)
                    self.assertNotIn("--all", line)


class TestReleaseRetention(unittest.TestCase):
    PREFIX = "ghcr.io/tjsasakifln/warmbly"

    def _images(self, *shas: str) -> str:
        out = []
        for sha in shas:
            out += [
                f"{self.PREFIX}/backend {sha}-minprofile",
                f"{self.PREFIX}/consumer {sha}-minprofile",
                f"{self.PREFIX}/worker {sha}-minprofile",
            ]
        return "\n".join(out)

    def test_rollback_image_remains_available(self) -> None:
        current, previous, ancient = "a" * 40, "b" * 40, "c" * 40
        with tempfile.TemporaryDirectory() as d:
            tmp = Path(d)
            state = tmp / "state"
            state.mkdir()
            (state / "release-history").write_text(
                f"{ancient} 2026-08-01T00:00:00Z\n{previous} 2026-08-27T00:00:00Z\n"
            )
            env = stub_env(tmp, STUB_IMAGES=self._images(current, previous, ancient))
            proc = run_guard(env, "retain", current)
            self.assertEqual(proc.returncode, 0, proc.stdout + proc.stderr)

            removed = [
                ln for ln in docker_log(env).splitlines() if ln.startswith("image rm")
            ]
            joined = "\n".join(removed)
            self.assertNotIn(current, joined, "removed the running release")
            self.assertNotIn(previous, joined, "removed the rollback release")
            self.assertIn(ancient, joined, "kept an image beyond the retention window")

    def test_release_removal_is_never_forced(self) -> None:
        """Without -f the daemon refuses an image any container references, so a
        wrong keep set still cannot yank the running release."""
        source = (PACK / "disk-guard.sh").read_text()
        self.assertRegex(source, r"docker image rm \"\$repo:\$tag\"")
        self.assertNotRegex(source, r"docker image rm\s+(-f|--force)")

    def test_history_is_bounded(self) -> None:
        with tempfile.TemporaryDirectory() as d:
            tmp = Path(d)
            env = stub_env(tmp)
            for i in range(30):
                run_guard(env, "retain", f"{i:040x}")
            history = (tmp / "state" / "release-history").read_text().splitlines()
            self.assertLessEqual(len(history), 20)

    def test_non_warmbly_images_are_never_considered(self) -> None:
        """Extra Consultoria and the control center share this daemon."""
        with tempfile.TemporaryDirectory() as d:
            tmp = Path(d)
            env = stub_env(
                tmp,
                STUB_IMAGES=(
                    "confenge-control-center-web 5fdda4f1b8fbe18243375614fb9b330d95cc2364\n"
                    "extra-consultoria-api deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
                ),
            )
            run_guard(env, "retain", "a" * 40)
            for line in docker_log(env).splitlines():
                if line.startswith("image rm"):
                    self.fail(f"cleanup reached a co-tenant image: {line}")


class TestImmutableRelease(unittest.TestCase):
    def _lib(self, script: str, *args: str) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            ["bash", "-c", f'source "$1"; shift; {script}', "t", str(PACK / "lib.sh"), *args],
            capture_output=True,
            text=True,
            check=False,
        )

    def test_correct_immutable_release_is_selected(self) -> None:
        sha = "a" * 40
        proc = self._lib(f'WARMBLY_RELEASE_SHA={sha}; for s in $(release_services); do release_image_ref "$s"; done')
        self.assertEqual(proc.returncode, 0, proc.stderr)
        refs = proc.stdout.split()
        self.assertIn(f"ghcr.io/tjsasakifln/warmbly/backend:{sha}-minprofile", refs)
        self.assertIn(f"ghcr.io/tjsasakifln/warmbly/consumer:{sha}-minprofile", refs)
        self.assertIn(f"ghcr.io/tjsasakifln/warmbly/worker:{sha}-minprofile", refs)
        self.assertIn(f"ghcr.io/tjsasakifln/warmbly/web:{sha}", refs)
        for ref in refs:
            self.assertNotIn(":dev", ref, "a mutable tag is not a release authority")
            self.assertNotIn(":latest", ref)

    def test_release_overlay_pins_every_application_service(self) -> None:
        text = (PACK / "docker-compose.release.yml").read_text()
        code = strip_comments(text)
        for svc in ("backend", "consumer", "worker", "tracking", "realtime", "web", "admin", "seed"):
            self.assertRegex(
                text,
                rf"\n  {svc}:\n    build: !reset null\n    image: ",
                f"{svc} must pin an image and reset its build section",
            )
        self.assertNotIn(":dev", code)
        self.assertNotIn(":latest", code)
        # No default: a deploy without an explicit release SHA must fail loudly.
        self.assertIn("WARMBLY_RELEASE_SHA:?", code)
        self.assertNotIn("WARMBLY_RELEASE_SHA:-", code)

    def test_go_services_keep_the_minprofile_contract(self) -> None:
        """Defaults to the variant with Stripe/AWS/GCP/Kafka not compile-linked."""
        text = (PACK / "docker-compose.release.yml").read_text()
        for svc in ("backend", "consumer", "worker", "seed"):
            self.assertRegex(
                text,
                rf"\n  {svc}:\n.*\n    image: .*\$\{{CONFENGE_GO_IMAGE_SUFFIX--minprofile\}}\n",
            )
        sha = "a" * 40
        proc = self._lib(
            f'WARMBLY_RELEASE_SHA={sha}; release_image_ref backend; '
            f'CONFENGE_GO_IMAGE_SUFFIX= release_image_ref backend'
        )
        self.assertEqual(proc.returncode, 0, proc.stderr)
        default_ref, rollback_ref = proc.stdout.split()
        self.assertTrue(default_ref.endswith("-minprofile"))
        # Rolling back to a release published before the variant existed.
        self.assertTrue(rollback_ref.endswith(sha))

    def test_ci_publishes_every_image_the_vps_pins(self) -> None:
        wf = (ROOT / ".github/workflows/build-push.yml").read_text()
        for svc in ("backend", "consumer", "worker", "tracking", "realtime", "web", "admin"):
            self.assertIn(svc, wf, f"CI must publish {svc} or the VPS has to build it")
        self.assertIn('profile: ["", "minprofile"]', wf)
        self.assertIn("org.opencontainers.image.revision=${{ github.sha }}", wf)


class TestDeployPath(unittest.TestCase):
    def test_no_build_on_normal_production_deploy(self) -> None:
        up = (PACK / "up.sh").read_text()
        self.assertIn("compose_cmd up -d --no-build --remove-orphans", up)
        self.assertNotIn("up -d --build", up)

    def test_preflight_precedes_every_mutation(self) -> None:
        up = (PACK / "up.sh").read_text()
        preflight = up.index("disk_guard preflight")
        for mutation in (
            "docker volume create",
            "compose_cmd up -d",
            "compose_cmd --profile seed run",
        ):
            self.assertLess(
                preflight,
                up.index(mutation),
                f"`{mutation}` runs before the disk preflight",
            )

    def test_release_is_acquired_and_verified_before_recreate(self) -> None:
        up = (PACK / "up.sh").read_text()
        self.assertLess(up.index("compose_cmd pull"), up.index("compose_cmd up -d --no-build"))
        self.assertLess(
            up.index("carries revision"), up.index("compose_cmd up -d --no-build")
        )

    def test_running_sha_is_validated_after_deploy(self) -> None:
        up = (PACK / "up.sh").read_text()
        self.assertIn("deploy/verify-release.sh", up)
        self.assertLess(up.index("compose_cmd up -d --no-build"), up.index("verify-release.sh"))
        self.assertIn("pg_isready", up)

    def test_deploy_clears_only_its_own_pause(self) -> None:
        """An operator emergency pause must survive a deploy; the deploy's own
        preflight pause must not require a human to remember to clear it."""
        up = (PACK / "up.sh").read_text()
        self.assertIn("reason=deploy_preflight", up)
        self.assertIn("DISPATCH_PAUSE=cleared", up)
        self.assertIn("DISPATCH_PAUSE=held", up)
        self.assertLess(up.index("verify-release.sh"), up.index("DISPATCH_PAUSE=cleared"))

    def test_retention_runs_without_an_operator_remembering(self) -> None:
        up = (PACK / "up.sh").read_text()
        self.assertIn("disk_guard retain", up)
        timer = (PACK / "systemd/confenge-docker-gc.timer").read_text()
        self.assertIn("OnCalendar=daily", timer)
        service = (PACK / "systemd/confenge-docker-gc.service").read_text()
        self.assertIn("disk-guard.sh retain", service)

    def test_legacy_local_build_driver_is_replaced(self) -> None:
        driver = (PACK / "release-deploy.sh").read_text()
        self.assertNotIn("compose.sh build", driver)
        self.assertIn("up.sh", driver)

    def test_driver_exports_the_release_before_any_compose_call(self) -> None:
        """compose_cmd rebinds the decision-audit identity from this variable,
        so a post-deploy compose call without it refuses instead of reporting."""
        driver = strip_comments((PACK / "release-deploy.sh").read_text())
        export = driver.index('export WARMBLY_RELEASE_SHA="$NEW_SHA"')
        self.assertLess(export, driver.index("up.sh"))
        self.assertLess(export, driver.index("compose_cmd"))


class TestBuildContext(unittest.TestCase):
    MAX_MB = 150

    def test_build_context_stays_small(self) -> None:
        proc = subprocess.run(
            [
                sys.executable,
                str(ROOT / "scripts/build_context_size.py"),
                str(ROOT),
                "--max-mb",
                str(self.MAX_MB),
            ],
            capture_output=True,
            text=True,
            timeout=300,
            check=False,
        )
        self.assertEqual(proc.returncode, 0, proc.stdout + proc.stderr)

    def test_runtime_data_trees_are_excluded(self) -> None:
        """data/backups reached 4.3 GB inside a 4.7 GB context on ec-prod."""
        proc = subprocess.run(
            [
                sys.executable,
                "-c",
                "import sys; sys.path.insert(0, %r); "
                "from build_context_size import load_rules, excluded; "
                "from pathlib import Path; "
                "rules = load_rules(Path(%r)); "
                "print('\\n'.join('%%s=%%s' %% (p, excluded(p, rules)) for p in sys.argv[1:]))"
                % (str(ROOT / "scripts"), str(ROOT)),
                "data/backups/confenge-vps/warmbly-confenge-20260827T005102Z.tar.gz",
                "data/GeoLite2-City.mmdb",
                "ops/feed-tls/ca-bundle.crt",
                "ops-evidence/x.json",
                "node_modules/left-pad/index.js",
                "web/node_modules/react/index.js",
                ".worktrees/x/internal/a.go",
                "backups/dump.sql.gz",
                "release.tar.gz",
                "internal/api/routes.go",
                "cmd/backend/main.go",
                "scripts/install-worker.sh",
                "go.mod",
            ],
            capture_output=True,
            text=True,
            check=False,
        )
        self.assertEqual(proc.returncode, 0, proc.stderr)
        verdicts = dict(
            line.split("=") for line in proc.stdout.strip().splitlines() if "=" in line
        )
        for path in (
            "data/backups/confenge-vps/warmbly-confenge-20260827T005102Z.tar.gz",
            "data/GeoLite2-City.mmdb",
            "ops/feed-tls/ca-bundle.crt",
            "ops-evidence/x.json",
            "node_modules/left-pad/index.js",
            "web/node_modules/react/index.js",
            ".worktrees/x/internal/a.go",
            "backups/dump.sql.gz",
            "release.tar.gz",
        ):
            self.assertEqual(verdicts[path], "True", f"{path} must not enter the context")
        # The Go images still need their source and the worker installer.
        for path in ("internal/api/routes.go", "cmd/backend/main.go", "scripts/install-worker.sh", "go.mod"):
            self.assertEqual(verdicts[path], "False", f"{path} must stay in the context")

    def test_no_dockerfile_consumes_the_excluded_trees(self) -> None:
        """The exclusions are only safe because nothing copies these at build."""
        dockerfiles = list((ROOT / "deploy/docker").glob("*.Dockerfile"))
        dockerfiles += [ROOT / "tracking/Dockerfile", ROOT / "web/Dockerfile", ROOT / "admin/Dockerfile"]
        pattern = re.compile(r"^\s*(COPY|ADD)\s+(?!--from)(.*)$", re.MULTILINE)
        for df in dockerfiles:
            if not df.is_file():
                continue
            for _, args in pattern.findall(df.read_text()):
                for token in args.split():
                    if token.startswith("--"):
                        continue
                    self.assertFalse(
                        token.startswith(("data/", "ops/", "backups/", "ops-evidence/")),
                        f"{df.name} copies an excluded runtime tree: {token}",
                    )


class TestBackupRetention(unittest.TestCase):
    def test_backup_retention_is_bounded_with_a_floor(self) -> None:
        text = (PACK / "backup.sh").read_text()
        self.assertIn("CONFENGE_BACKUP_KEEP", text)
        self.assertIn("-ge 3", text)
        # Retention must only run after the archive is complete.
        self.assertLess(text.index('mv "$ARCHIVE_TMP" "$ARCHIVE"'), text.index("BACKUP_KEEP="))

    def test_co_tenant_data_is_reported_not_deleted(self) -> None:
        report = (PACK / "host-disk-report.sh").read_text()
        self.assertIn("Deletes nothing", report)
        for bad in ("rm -rf", "rm -f", "prune", "docker volume"):
            self.assertNotIn(bad, report)
        self.assertIn("extra-consultoria", report)


if __name__ == "__main__":
    unittest.main()
