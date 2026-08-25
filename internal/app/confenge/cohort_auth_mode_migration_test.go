package confenge

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Every authorization mode the cohort operator can persist must be permitted by
// the newest outreach_touchpoints_auth_mode_check. Migration 000092 pinned the
// column to three values; the bounded cohort added a fourth in Go only, so every
// frozen-cohort touchpoint write failed with SQLSTATE 23514.
func TestBoundedCohortAuthModeIsAllowedByLatestCheckConstraint(t *testing.T) {
	dir := filepath.Join("..", "..", "infrastructure", "db", "migrations")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}
	var ups []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".up.sql") {
			ups = append(ups, e.Name())
		}
	}
	sort.Strings(ups)

	// The last migration that redefines the constraint wins.
	var latest string
	for _, name := range ups {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		body := string(b)
		if strings.Contains(body, "outreach_touchpoints_auth_mode_check") && strings.Contains(body, "ADD CONSTRAINT") {
			latest = body
		}
	}
	if latest == "" {
		t.Fatal("no migration defines outreach_touchpoints_auth_mode_check")
	}
	for _, mode := range []string{
		AuthorizationModeHumanTouchpoint,
		AuthorizationModeHumanGate,
		AuthorizationModeCampaignPolicy,
		AuthorizationModeBoundedCohort,
	} {
		if !strings.Contains(latest, "'"+mode+"'") {
			t.Fatalf("authorization mode %q is written by Go but rejected by the check constraint", mode)
		}
	}
}
