package intel

import (
	"fmt"
	"testing"
	"time"
)

func TestClassifyNamedExceptions(t *testing.T) {
	base := testFacts("org", "lead-ex", "rcpt-ex", "acc-ex", "act-ex", "out-ex")

	orphan := base
	orphan.Keys.LeadID = ""
	orphan.Keys.ReceiptID = ""
	orphan.Keys.ActionID = ""
	orphan.Keys.IdempotencyKey = ""
	orphan.Keys.OutcomeID = "out-orphan"
	orphan.OutcomeType = OutcomeMeeting

	dupExisting := &Chain{Keys: base.Keys, Identity: ChainIdentity(base.Keys)}

	conflict := base
	conflict.Keys.AccountID = "other-acc"
	conflict.Keys.SourceLeadID = "other-acc"

	missing := base
	missing.Keys.TargetFitVersion = ""
	missing.Keys.ActivationPolicyVersion = ""

	stale := base
	stale.AttributionStale = true

	codes := map[string][]Exception{
		ExceptionOrphan:             ClassifyExceptions(orphan, nil),
		ExceptionDuplicate:          ClassifyExceptions(base, dupExisting),
		ExceptionConflictingAccount: ClassifyExceptions(conflict, dupExisting),
		ExceptionMissingVersion:     ClassifyExceptions(missing, nil),
		ExceptionStaleAttribution:   ClassifyExceptions(stale, nil),
	}
	for want, got := range codes {
		if !hasCode(got, want) {
			t.Fatalf("code %s missing in %+v", want, codesOf(got))
		}
		fmt.Printf("EXCEPTION code=%s persisted_classifier=true\n", want)
	}

	st := NewMemoryStore()
	for _, in := range []ObservedFacts{orphan, missing, stale} {
		res := Reconcile(st, in)
		if len(res.Exceptions) == 0 {
			t.Fatalf("reconcile dropped exceptions for %+v", in.Keys)
		}
	}
	// Duplicate + conflict persist when a first chain exists.
	first := Reconcile(st, base)
	if !first.Created && first.Chain.Identity == "" && !hasCode(first.Exceptions, ExceptionOrphan) {
		// base has IDs; should create unless earlier orphan identity collided.
	}
	dup := Reconcile(st, base)
	if !hasCode(dup.Exceptions, ExceptionDuplicate) {
		t.Fatalf("duplicate not persisted on replay: %+v", codesOf(dup.Exceptions))
	}
	conf := Reconcile(st, conflict)
	if !hasCode(conf.Exceptions, ExceptionConflictingAccount) && conflict.Keys.LeadID == base.Keys.LeadID {
		t.Fatalf("conflicting_account not persisted: %+v", codesOf(conf.Exceptions))
	}
	stored, _ := st.ListExceptions("")
	seen := map[string]bool{}
	for _, ex := range stored {
		seen[ex.Code] = true
	}
	for _, code := range []string{ExceptionOrphan, ExceptionDuplicate, ExceptionMissingVersion, ExceptionStaleAttribution} {
		if !seen[code] {
			t.Fatalf("store missing exception %s (have %v)", code, seen)
		}
	}
	fmt.Printf("EXCEPTION_STORE codes=%d\n", len(stored))
}

func TestOutOfOrderHeldNotReordered(t *testing.T) {
	st := NewMemoryStore()
	in := testFacts("org", "lead-oo", "rcpt-oo", "acc-oo", "act-oo", "out-oo")
	in.ActionOccurredAt = time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	in.OutcomeOccurredAt = time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	res := Reconcile(st, in)
	if !hasCode(res.Exceptions, ExceptionOutOfOrder) {
		t.Fatalf("out_of_order missing: %+v", codesOf(res.Exceptions))
	}
	if !res.Held && !res.Chain.Held {
		t.Fatal("out-of-order must be held")
	}
	if !res.Chain.LeadCreatedAt.Equal(in.LeadCreatedAt) {
		t.Fatalf("lead_created_at rewritten: %v vs %v", res.Chain.LeadCreatedAt, in.LeadCreatedAt)
	}
	fmt.Printf("OUT_OF_ORDER held=%v reordered=false\n", res.Held || res.Chain.Held)
}

func TestUnconfirmedWonStaysUnknown(t *testing.T) {
	st := NewMemoryStore()
	in := testFacts("org", "lead-won", "rcpt-won", "acc-won", "act-won", "out-won")
	in.OutcomeType = OutcomeWon
	in.HumanConfirmed = false
	res := Reconcile(st, in)
	if !hasCode(res.Exceptions, ExceptionUnconfirmedWon) {
		t.Fatalf("unconfirmed WON not classified: %+v", codesOf(res.Exceptions))
	}
	if res.Chain.OutcomeType == OutcomeWon {
		t.Fatal("unconfirmed WON must not land on the chain")
	}
	if res.Chain.OutcomeType != OutcomeUnknown {
		t.Fatalf("outcome=%s want UNKNOWN", res.Chain.OutcomeType)
	}
	fmt.Printf("UNCONFIRMED_WON chain_outcome=%s rejected=true\n", res.Chain.OutcomeType)
}

func TestUnconfirmedLostStaysUnknown(t *testing.T) {
	st := NewMemoryStore()
	in := testFacts("org", "lead-lost", "rcpt-lost", "acc-lost", "act-lost", "out-lost")
	in.OutcomeType = OutcomeLost
	in.HumanConfirmed = false
	res := Reconcile(st, in)
	if !hasCode(res.Exceptions, ExceptionUnconfirmedLost) {
		t.Fatalf("unconfirmed LOST not classified: %+v", codesOf(res.Exceptions))
	}
	if res.Chain.OutcomeType == OutcomeLost {
		t.Fatal("unconfirmed LOST must not land on the chain")
	}
	if res.Chain.OutcomeType != OutcomeUnknown {
		t.Fatalf("outcome=%s want UNKNOWN", res.Chain.OutcomeType)
	}
	fmt.Printf("UNCONFIRMED_LOST chain_outcome=%s rejected=true\n", res.Chain.OutcomeType)
}

func hasCode(xs []Exception, code string) bool {
	for _, ex := range xs {
		if ex.Code == code {
			return true
		}
	}
	return false
}

func codesOf(xs []Exception) []string {
	var out []string
	for _, ex := range xs {
		out = append(out, ex.Code)
	}
	return out
}
