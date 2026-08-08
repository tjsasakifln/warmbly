package confenge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestConfengeUIAcceptanceAffordancesPresent is the CI-safe static gate for the
// Playwright product-acceptance UI path: required routes and data-testids must
// ship in web/ even when browsers/stack cannot run in this environment.
func TestConfengeUIAcceptanceAffordancesPresent(t *testing.T) {
	root := findRepoRoot(t)
	page := filepath.Join(root, "web", "src", "app", "app", "confenge", "page.tsx")
	raw, err := os.ReadFile(page)
	if err != nil {
		t.Fatalf("CONFENGE UI page missing at %s: %v", page, err)
	}
	src := string(raw)
	needles := []string{
		`data-testid="confenge-review-queue"`,
		`data-testid="confenge-approve-queue"`,
		`data-testid="confenge-dispatch-quota"`,
		`data-testid="confenge-needs-attention"`,
		`data-testid="confenge-evidence"`,
		`data-testid="confenge-body-input"`,
		`data-testid="confenge-recipient"`,
		`data-testid="confenge-recipient-input"`,
		`confenge-stat-`, // template for Sent/Review/etc.
		"Approve & Queue",
		"Needs attention",
		"Exact send preview",
	}
	for _, n := range needles {
		if !strings.Contains(src, n) {
			t.Errorf("UI affordance missing from confenge page: %q", n)
		}
	}

	spec := filepath.Join(root, "web", "e2e", "confenge-product-acceptance.spec.ts")
	if _, err := os.Stat(spec); err != nil {
		t.Fatalf("Playwright CONFENGE acceptance spec missing: %v", err)
	}
	cfg := filepath.Join(root, "web", "playwright.config.ts")
	if _, err := os.Stat(cfg); err != nil {
		t.Fatalf("Playwright config missing: %v", err)
	}
	specRaw, err := os.ReadFile(spec)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{
		"confenge-review-queue",
		"confenge-approve-queue",
		"confenge-dispatch-quota",
		"confenge-needs-attention",
		"confenge-body-input",
		"confenge-stat-sent",
		"/app/confenge",
		"healthcheckOrThrow",
		"approved_content_hash",
		"NEEDS_REVIEW",
		// Live browser is opt-in; static presence never implies playwright_live PASS.
		"CONFENGE_E2E",
	} {
		if !strings.Contains(string(specRaw), n) {
			t.Errorf("Playwright spec missing step for %q", n)
		}
	}
	// Ephemeral goal-scratch paths must not be defaults.
	if strings.Contains(string(specRaw), "/tmp/grok-goal-") {
		t.Error("Playwright spec must not hardcode /tmp/grok-goal-* paths")
	}
}

func findRepoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := wd
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("could not locate repo root (go.mod)")
	return ""
}
