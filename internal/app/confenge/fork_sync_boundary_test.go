package confenge

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const forkDriftAuditRel = "docs/confenge/fork-drift-audit.json"

var allowedDriftDecisions = map[string]struct{}{
	"KEEP_CONFENGE": {},
	"UPSTREAMABLE":  {},
	"ISOLATE":       {},
	"DROP":          {},
	"RISK":          {},
}

type forkDriftAudit struct {
	Campaign            string            `json:"campaign"`
	Refs                map[string]string `json:"refs"`
	BuildTagsConfenge   []string          `json:"build_tags_confenge"`
	Rows                []forkDriftRow    `json:"rows"`
	UpstreamOnlyCommits []struct {
		SHA string `json:"sha"`
	} `json:"upstream_only_commits"`
}

type forkDriftRow struct {
	ID                string   `json:"id"`
	Kind              string   `json:"kind"`
	Name              string   `json:"name"`
	Paths             []string `json:"paths"`
	Owner             string   `json:"owner"`
	Decision          string   `json:"decision"`
	Risk              string   `json:"risk"`
	OnOperationalPath bool     `json:"on_operational_path"`
	Notes             string   `json:"notes"`
}

func loadForkDriftAudit(t *testing.T) (string, forkDriftAudit) {
	t.Helper()
	root := findRepoRoot(t)
	path := filepath.Join(root, forkDriftAuditRel)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("fork drift audit missing at %s: %v", path, err)
	}
	var a forkDriftAudit
	if err := json.Unmarshal(raw, &a); err != nil {
		t.Fatalf("fork drift audit JSON: %v", err)
	}
	return root, a
}

// TestForkSyncBoundaryAppConfengeRouteAndPage fails if the dashboard route or
// page is dropped during an upstream sync. It reads the shipped router, nav,
// and page, not a copy of their contents.
func TestForkSyncBoundaryAppConfengeRouteAndPage(t *testing.T) {
	root := findRepoRoot(t)
	page := filepath.Join(root, "web", "src", "app", "app", "confenge", "page.tsx")
	if _, err := os.Stat(page); err != nil {
		t.Fatalf("/app/confenge page missing: %v", err)
	}
	mainTSX := readRepoFile(t, root, "web/src/main.tsx")
	if !strings.Contains(mainTSX, `path: "confenge"`) {
		t.Fatal(`web/src/main.tsx must keep path: "confenge"`)
	}
	if !strings.Contains(mainTSX, "ConfengePage") {
		t.Fatal("web/src/main.tsx must mount ConfengePage")
	}
	if !strings.Contains(mainTSX, `path: "app/confenge"`) {
		t.Fatal(`web/src/main.tsx must keep the operator path "app/confenge"`)
	}
	nav := readRepoFile(t, root, "web/src/components/layout/AppNav.tsx")
	if !strings.Contains(nav, `url: "/app/confenge"`) {
		t.Fatal("AppNav must keep url /app/confenge")
	}
	routes := readRepoFile(t, root, "internal/api/routes.go")
	if !strings.Contains(routes, `protected.Group("/confenge")`) {
		t.Fatal("routes.go must keep the /confenge group")
	}
	if !strings.Contains(routes, `/api/v1/webhooks/confenge/inbound`) {
		t.Fatal("routes.go must keep the inbound webhook")
	}
	if !strings.Contains(routes, `h.ApproveConfengeTouchpoint`) {
		t.Fatal("routes.go must keep human approve")
	}
}

// TestForkSyncBoundaryHumanApprovalKillSwitchNoAutoSend drives the shipped
// config and kill-switch functions. A sync that defaults AUTO_SEND on, drops
// human approval, or fail-opens the kill switch must fail here.
func TestForkSyncBoundaryHumanApprovalKillSwitchNoAutoSend(t *testing.T) {
	t.Setenv(EnvEnabled, "")
	t.Setenv(EnvAutoSend, "")
	t.Setenv(EnvRequireHuman, "")
	t.Setenv(EnvGreenAutorun, "")
	t.Setenv(EnvSendingPaused, "")
	t.Setenv(EnvOperatorMode, "")
	dir := t.TempDir()
	t.Setenv(EnvKillSwitchPath, filepath.Join(dir, "kill-switch"))

	cfg := LoadConfig()
	if cfg.AutoSendEnabled {
		t.Fatal("CONFENGE_AUTO_SEND_ENABLED must default false")
	}
	if cfg.GreenAutorunEnabled {
		t.Fatal("CONFENGE_GREEN_AUTORUN_ENABLED must default false")
	}
	if !cfg.RequireHumanApproval {
		t.Fatal("CONFENGE_REQUIRE_HUMAN_APPROVAL must default true")
	}
	if FileKillSwitchActive() {
		t.Fatal("kill switch must be inactive when the file is absent")
	}
	if !cfg.SendingAllowed() {
		t.Fatal("SendingAllowed must be true when env pause and file are off")
	}

	enabled := cfg
	enabled.Enabled = true
	enabled.AutoSendEnabled = true
	if err := enabled.ValidateStartup("dev"); err == nil {
		t.Fatal("ValidateStartup must reject AUTO_SEND=true")
	}
	if !strings.Contains(errString(enabled.ValidateStartup("dev")), EnvAutoSend) {
		t.Fatal("AUTO_SEND rejection must name CONFENGE_AUTO_SEND_ENABLED")
	}

	if err := EngageKillSwitch(); err != nil {
		t.Fatalf("EngageKillSwitch: %v", err)
	}
	if !FileKillSwitchActive() {
		t.Fatal("kill switch file must be observed after engage")
	}
	if LoadConfig().SendingAllowed() {
		t.Fatal("SendingAllowed must be false while the kill-switch file exists")
	}
	if err := ReleaseKillSwitch(); err != nil {
		t.Fatalf("ReleaseKillSwitch: %v", err)
	}
	if FileKillSwitchActive() || !LoadConfig().SendingAllowed() {
		t.Fatal("SendingAllowed must recover after ReleaseKillSwitch")
	}

	root := findRepoRoot(t)
	worker := readRepoFile(t, root, "internal/app/worker/event_send_email.go")
	if !strings.Contains(worker, "confenge.LoadConfig") || !strings.Contains(worker, "SendingAllowed") {
		t.Fatal("worker HandleSendEmail must re-check CONFENGE LoadConfig and SendingAllowed")
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// TestForkSyncBoundaryNoUnsuitedUpstreamAutoSync fails if a GitHub workflow
// cherry-picks or syncs from warmbly/warmbly without the CONFENGE suite.
func TestForkSyncBoundaryNoUnsuitedUpstreamAutoSync(t *testing.T) {
	root := findRepoRoot(t)
	wfDir := filepath.Join(root, ".github", "workflows")
	entries, err := os.ReadDir(wfDir)
	if err != nil {
		t.Fatalf("workflows dir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal(".github/workflows must exist")
	}
	for _, e := range entries {
		if e.IsDir() || !isWorkflowFile(e.Name()) {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(wfDir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		src := string(raw)
		if !workflowLooksLikeUpstreamSync(src) {
			continue
		}
		if !workflowInvokesConfengeSuite(src) {
			t.Errorf("%s syncs from upstream without go test on CONFENGE packages and confenge-product-acceptance", e.Name())
		}
	}
}

func isWorkflowFile(name string) bool {
	return strings.HasSuffix(name, ".yml") || strings.HasSuffix(name, ".yaml")
}

func workflowLooksLikeUpstreamSync(src string) bool {
	lower := strings.ToLower(src)
	hasSource := strings.Contains(lower, "warmbly/warmbly") ||
		strings.Contains(lower, "upstream/main") ||
		strings.Contains(lower, "remote add upstream")
	hasVerb := strings.Contains(lower, "cherry-pick") ||
		strings.Contains(lower, "git pull") ||
		strings.Contains(lower, "git rebase") ||
		strings.Contains(lower, "repo-sync") ||
		strings.Contains(lower, "sync upstream") ||
		strings.Contains(lower, "sync from upstream")
	return hasSource && hasVerb
}

func workflowInvokesConfengeSuite(src string) bool {
	hasGo := strings.Contains(src, "./internal/app/confenge") ||
		(strings.Contains(src, "go test") && strings.Contains(src, "confenge"))
	hasJob := strings.Contains(src, "confenge-product-acceptance") ||
		strings.Contains(src, "pnpm test:e2e:confenge")
	return hasGo && hasJob
}

// TestForkSyncBoundaryAuditManifestComplete treats the JSON as the classification
// source of truth and checks every material row has owner/decision/risk.
func TestForkSyncBoundaryAuditManifestComplete(t *testing.T) {
	root, a := loadForkDriftAudit(t)
	if a.Campaign != "WARMBLY-013" {
		t.Fatalf("campaign=%q", a.Campaign)
	}
	for _, key := range []string{"origin_main", "upstream_main", "merge_base"} {
		sha := a.Refs[key]
		if !isGitSHA(sha) {
			t.Errorf("refs.%s is not a 40-char SHA: %q", key, sha)
		}
	}
	if a.Refs["base_moved"] != "false" && a.Refs["origin_main"] != a.Refs["plan_time_base"] {
		// origin/main moved after plan time; residual-only must be written in notes, not silently ignored.
		t.Logf("origin/main moved from plan-time base; audit must stay residual-only")
	}
	if len(a.BuildTagsConfenge) != 0 {
		t.Fatalf("CONFENGE build tags must stay empty, got %v", a.BuildTagsConfenge)
	}
	if err := assertNoConfengeBuildTags(root); err != nil {
		t.Fatal(err)
	}

	seenDecision := map[string]int{}
	seenID := map[string]bool{}
	requiredIDs := []string{
		"cluster-action-plane-core",
		"dir-web-confenge",
		"mig-fork-083-101",
		"mig-upstream-083-084-collision",
		"api-confenge-action",
		"core-ci",
		"upstream-16",
		"policy-no-full-upstream",
	}
	for _, row := range a.Rows {
		if row.ID == "" || row.Kind == "" || row.Name == "" || row.Owner == "" || row.Risk == "" {
			t.Errorf("incomplete row: %+v", row)
			continue
		}
		if _, ok := allowedDriftDecisions[row.Decision]; !ok {
			t.Errorf("row %s has invalid decision %q", row.ID, row.Decision)
		}
		if seenID[row.ID] {
			t.Errorf("duplicate row id %s", row.ID)
		}
		seenID[row.ID] = true
		seenDecision[row.Decision]++
		for _, p := range row.Paths {
			if p == "" {
				t.Errorf("row %s has empty path", row.ID)
				continue
			}
			// Paths may be files or directories; skip when the row is a forecast
			// of upstream-only files not present on this tree.
			if row.Kind == "upstream_commit" || row.ID == "mig-upstream-083-084-collision" {
				continue
			}
			abs := filepath.Join(root, p)
			if _, err := os.Stat(abs); err != nil {
				t.Errorf("row %s path missing on origin/main tree: %s", row.ID, p)
			}
		}
	}
	for _, id := range requiredIDs {
		if !seenID[id] {
			t.Errorf("required classification row missing: %s", id)
		}
	}
	for dec := range allowedDriftDecisions {
		if seenDecision[dec] == 0 {
			t.Errorf("no row uses decision %s", dec)
		}
	}
	if len(a.UpstreamOnlyCommits) != 16 {
		t.Fatalf("expected 16 upstream-only commits, got %d", len(a.UpstreamOnlyCommits))
	}
	for i, c := range a.UpstreamOnlyCommits {
		if !isGitSHA(c.SHA) {
			t.Errorf("upstream_only_commits[%d] sha %q", i, c.SHA)
			continue
		}
		if !gitObjectExists(t, root, c.SHA) {
			t.Errorf("upstream commit %s not in this clone; refetch upstream/main", c.SHA)
		}
	}
	if origin := a.Refs["origin_main"]; isGitSHA(origin) && !gitIsAncestor(t, root, origin, "HEAD") {
		t.Errorf("recorded origin_main %s is not an ancestor of HEAD; revalidate the audit", origin)
	}

	maint := readRepoFile(t, root, "docs/confenge/upstream-maintenance.md")
	for _, needle := range []string{
		"Never automatic cherry-pick",
		"confenge-product-acceptance",
		"000083",
		"000084",
		"000101",
		"Prefer merge",
	} {
		if !strings.Contains(maint, needle) {
			t.Errorf("upstream-maintenance.md missing %q", needle)
		}
	}
}

func isGitSHA(s string) bool {
	if len(s) != 40 {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func assertNoConfengeBuildTags(root string) error {
	roots := []string{
		filepath.Join(root, "internal", "app", "confenge"),
		filepath.Join(root, "cmd", "confenge"),
		filepath.Join(root, "internal", "api", "handler"),
	}
	for _, start := range roots {
		err := filepath.WalkDir(start, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
				return err
			}
			if !strings.Contains(strings.ToLower(filepath.Base(path)), "confenge") && !strings.Contains(path, filepath.Join("app", "confenge")) {
				return nil
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			first := strings.SplitN(string(raw), "\n", 4)
			for _, line := range first {
				if strings.HasPrefix(line, "//go:build") && strings.Contains(line, "confenge") {
					return fmt.Errorf("CONFENGE must not hide behind a build tag: %s", path)
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func gitObjectExists(t *testing.T, root, sha string) bool {
	t.Helper()
	cmd := exec.Command("git", "cat-file", "-t", sha)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("git cat-file %s: %s", sha, out)
		return false
	}
	return strings.TrimSpace(string(out)) == "commit"
}

func gitIsAncestor(t *testing.T, root, anc, desc string) bool {
	t.Helper()
	cmd := exec.Command("git", "merge-base", "--is-ancestor", anc, desc)
	cmd.Dir = root
	return cmd.Run() == nil
}

func readRepoFile(t *testing.T, root, rel string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(raw)
}
