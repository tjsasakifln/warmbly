package confenge

import (
	"testing"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/models"
)

// Membership is one canonical member per cnpj_root8. Older imports leave rows
// behind: production carried 97 TARGET_CONFIRMED rows from superseded runs
// across 89 roots, every one of them also published by the current run. The
// transport gate already refused them with account_source_run_drift, but they
// stayed live and countable.

const currentRun = "run-333a9ea5d4912fe5"

func account(root, runID string) models.OutreachAccount {
	return models.OutreachAccount{ID: uuid.New(), CNPJRoot: root, SourceRunID: runID}
}

func TestOlderRowIsRetiredOnlyWhenTheCurrentRunPublishesTheSameRoot(t *testing.T) {
	current := account("12345678", currentRun)
	legacy := account("12345678", "run-c48007591e089881")
	out := supersededByCurrentRun([]models.OutreachAccount{current, legacy}, currentRun)

	if !out[legacy.ID] {
		t.Fatal("a legacy row whose root the current run republished must be retired")
	}
	if out[current.ID] {
		t.Fatal("the current canonical member must never be retired")
	}
}

func TestARootTheCurrentRunNeverMentionsIsLeftAlone(t *testing.T) {
	// Absence of evidence is not evidence of supersession.
	elsewhere := account("99999999", currentRun)
	orphan := account("12345678", "run-c48007591e089881")
	out := supersededByCurrentRun([]models.OutreachAccount{elsewhere, orphan}, currentRun)

	if out[orphan.ID] {
		t.Fatal("a root the current run never published must not be retired")
	}
}

func TestNoCurrentRunRetiresNothing(t *testing.T) {
	// An unreadable feed state must never read as "everything is old".
	rows := []models.OutreachAccount{account("12345678", "run-old"), account("12345678", "run-older")}
	if len(supersededByCurrentRun(rows, "")) != 0 {
		t.Fatal("without supersession evidence nothing may be retired")
	}
}

func TestRowsWithoutARunAreNotRetired(t *testing.T) {
	current := account("12345678", currentRun)
	unattributed := account("12345678", "")
	out := supersededByCurrentRun([]models.OutreachAccount{current, unattributed}, currentRun)
	if out[unattributed.ID] {
		t.Fatal("a row with no run attribution carries no supersession evidence")
	}
}

func TestRowsWithoutARootAreNotRetired(t *testing.T) {
	current := account("12345678", currentRun)
	rootless := account("", "run-old")
	out := supersededByCurrentRun([]models.OutreachAccount{current, rootless}, currentRun)
	if out[rootless.ID] {
		t.Fatal("supersession is per root; a rootless row cannot be matched")
	}
}

func TestEveryLegacyRowOfARepublishedRootIsRetired(t *testing.T) {
	current := account("12345678", currentRun)
	rows := []models.OutreachAccount{current}
	var legacyIDs []uuid.UUID
	for _, run := range []string{"run-a", "run-b", "run-c"} {
		row := account("12345678", run)
		legacyIDs = append(legacyIDs, row.ID)
		rows = append(rows, row)
	}
	out := supersededByCurrentRun(rows, currentRun)
	for _, id := range legacyIDs {
		if !out[id] {
			t.Fatalf("legacy row %s was left live", id)
		}
	}
	if len(out) != len(legacyIDs) {
		t.Fatalf("retired %d rows, expected exactly %d", len(out), len(legacyIDs))
	}
}

func TestRetirementReasonIsNamedNotGeneric(t *testing.T) {
	if SupersededByCurrentRunReason != "superseded_by_current_source_run" {
		t.Fatalf("reason=%q", SupersededByCurrentRunReason)
	}
}
