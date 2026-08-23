package confenge

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

// seedRunAccount stages one account plus one controlled-eligible generic route.
func seedRunAccount(t *testing.T, repo *memRepo, orgID uuid.UUID, ref, cnpj, runID string) uuid.UUID {
	t.Helper()
	unk := true
	acc := cohortAccount(ref, cnpj, "Contrato vigente")
	acc.OrganizationID = orgID
	acc.SourceRunID = runID
	if _, err := repo.UpsertAccount(context.Background(), &acc); err != nil {
		t.Fatalf("upsert account %s: %v", ref, err)
	}
	if acc.ID == uuid.Nil {
		t.Fatalf("account %s got no id from upsert", ref)
	}
	cand := models.OutreachContactCandidate{
		OrganizationID:  orgID,
		AccountID:       acc.ID,
		Email:           "contato@" + ref + ".com.br",
		MailboxPurpose:  "GENERIC_CONTACT",
		OwnershipStatus: "COMPANY_OWNED",
		DiscoveryJSON:   eligibleDisc(t, RouteClassGenericCompany, true, controlledDiscovery{PersonUnknown: &unk}),
	}
	if _, err := repo.UpsertCandidate(context.Background(), &cand); err != nil {
		t.Fatalf("upsert candidate %s: %v", ref, err)
	}
	return acc.ID
}

// An org far past one page must still freeze the imported run. Before the run
// scope was pushed into the query, an empty filter returned 50 rows ordered by
// priority and the client-side run filter found nothing.
func TestAccountsFromOrgForRunFindsMembersBeyondFirstPage(t *testing.T) {
	repo := newMemRepo()
	orgID := uuid.New()
	ctx := context.Background()

	const noise = 1200
	for i := 0; i < noise; i++ {
		// cnpj14 sorts low, so this run fills every page before the target run.
		seedRunAccount(t, repo, orgID, fmt.Sprintf("noise-%04d", i), fmt.Sprintf("1%013d", i), "run-old")
	}

	const wanted = 12
	targetIDs := map[uuid.UUID]bool{}
	for i := 0; i < wanted; i++ {
		id := seedRunAccount(t, repo, orgID, fmt.Sprintf("target-%02d", i), fmt.Sprintf("9%013d", i), "run-target")
		targetIDs[id] = true
	}

	// Guard the premise: the target run really is beyond the first page.
	page1, err := repo.ListAccounts(ctx, orgID, repository.OutreachAccountFilter{})
	if err != nil {
		t.Fatalf("list page 1: %v", err)
	}
	for i := range page1 {
		if page1[i].SourceRunID == "run-target" {
			t.Fatal("premise broken: target run appears on page 1, the regression cannot be observed")
		}
	}

	accounts, err := AccountsFromOrgForRun(ctx, repo, orgID, "extra-cli", "run-target")
	if err != nil {
		t.Fatalf("accounts from org for run: %v", err)
	}
	if len(accounts) != wanted {
		t.Fatalf("scoped accounts = %d, want %d", len(accounts), wanted)
	}
	for i := range accounts {
		if accounts[i].Account.SourceRunID != "run-target" {
			t.Fatalf("account %s leaked from run %q", accounts[i].Account.SourceLeadID, accounts[i].Account.SourceRunID)
		}
		if !accounts[i].Persisted {
			t.Fatalf("account %s must be marked persisted", accounts[i].Account.SourceLeadID)
		}
		if len(accounts[i].Candidates) != 1 {
			t.Fatalf("account %s candidates = %d, want 1", accounts[i].Account.SourceLeadID, len(accounts[i].Candidates))
		}
	}

	snap, err := PrepareControlledCohort(accounts, CohortPrepareOptions{
		RepositorySHA:  "sha-1",
		FeedIdentity:   "run-target",
		Limit:          50,
		MaxDailyVolume: 10,
	})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if len(snap.Members) != wanted {
		t.Fatalf("members = %d, want %d", len(snap.Members), wanted)
	}
	for i := range snap.Members {
		m := &snap.Members[i]
		if m.AccountID == uuid.Nil {
			t.Fatalf("member %q frozen with a nil account id", m.AccountRef)
		}
		if !targetIDs[m.AccountID] {
			t.Fatalf("member %q bound to account %s outside the target run", m.AccountRef, m.AccountID)
		}
		if m.CandidateID == uuid.Nil {
			t.Fatalf("member %q frozen with a nil candidate id", m.AccountRef)
		}
	}
}

// An unscoped org freeze must refuse rather than truncate to whatever fits.
func TestAccountsFromOrgRefusesUnscopedOversizedOrg(t *testing.T) {
	repo := newMemRepo()
	orgID := uuid.New()

	for i := 0; i < maxOrgFreezeAccounts+10; i++ {
		seedRunAccount(t, repo, orgID, fmt.Sprintf("bulk-%05d", i), fmt.Sprintf("1%013d", i), "run-old")
	}

	_, err := AccountsFromOrg(context.Background(), repo, orgID, "extra-cli")
	if err == nil {
		t.Fatal("oversized unscoped org must not silently truncate")
	}
	if !strings.Contains(err.Error(), "scope the freeze to one feed run") {
		t.Fatalf("error = %v, want operator guidance to scope by feed run", err)
	}
}

// A Postgres-bound account with no canonical id must fail at freeze time, not
// at authorize time as an opaque touchpoint foreign key violation.
func TestPrepareRefusesPersistedAccountWithoutCanonicalID(t *testing.T) {
	unk := true
	acc := cohortAccount("acc-unbound", "77777777000177", "Contrato vigente")
	accounts := []CohortAccountInput{{
		Account:   acc,
		Persisted: true,
		Candidates: []models.OutreachContactCandidate{{
			Email:           "contato@empresa7.com.br",
			MailboxPurpose:  "GENERIC_CONTACT",
			OwnershipStatus: "COMPANY_OWNED",
			DiscoveryJSON:   eligibleDisc(t, RouteClassGenericCompany, true, controlledDiscovery{PersonUnknown: &unk}),
		}},
		Source: "extra-cli",
	}}

	_, err := PrepareControlledCohort(accounts, CohortPrepareOptions{
		RepositorySHA: "sha-1", FeedIdentity: "run-1", Limit: 50, MaxDailyVolume: 10,
	})
	if err == nil {
		t.Fatal("freezing a persisted account without a canonical id must fail")
	}
	if !strings.Contains(err.Error(), "no canonical id") {
		t.Fatalf("error = %v, want a canonical-id refusal", err)
	}
}
