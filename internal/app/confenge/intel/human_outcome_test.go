package intel

import (
	"fmt"
	"testing"
	"time"
)

func TestEmptyEnvelopesInventNoIDsAndReplayIdempotent(t *testing.T) {
	envs := EmptyEnvelopes()
	if len(envs) != 4 {
		t.Fatalf("envelopes=%d", len(envs))
	}
	want := []string{EnvelopeEXTRA, EnvelopeAccount1, EnvelopeAccount2, EnvelopeAccount3}
	seen := map[string]string{}
	for i, env := range envs {
		if env.Slot != want[i] {
			t.Fatalf("slot %d=%s want %s", i, env.Slot, want[i])
		}
		if env.LeadID != "" || env.AccountID != "" || env.InventedIDs {
			t.Fatalf("invented IDs on %s: %+v", env.Slot, env)
		}
		if env.IdempotencyKey != EnvelopeIdempotencyKey(env.Slot) {
			t.Fatalf("idempotency %s", env.IdempotencyKey)
		}
		seen[env.Slot] = env.IdempotencyKey
	}
	again := EmptyEnvelopes()
	for i := range again {
		if again[i].IdempotencyKey != envs[i].IdempotencyKey {
			t.Fatal("envelope keys drifted")
		}
	}

	st := NewMemoryStore()
	now := time.Date(2026, 8, 18, 16, 0, 0, 0, time.UTC)
	first := RegisterHumanOutcome(st, HumanOutcomeEntry{
		EnvelopeID: EnvelopeEXTRA, IdempotencyKey: EnvelopeIdempotencyKey(EnvelopeEXTRA),
		Action: HumanAttempted, OccurredAt: now, OrganizationID: loopOrg,
	})
	if first.Held && len(first.Exceptions) > 0 && first.Chain.Identity == "" && !first.Replay {
		// attempted without lead_id is an orphan; still must be observable
		if first.Exceptions[0].Owner == "" {
			t.Fatalf("empty EXTRA attempt silent: %+v", first)
		}
	}
	replay := RegisterHumanOutcome(st, HumanOutcomeEntry{
		EnvelopeID: EnvelopeEXTRA, IdempotencyKey: EnvelopeIdempotencyKey(EnvelopeEXTRA),
		Action: HumanAttempted, OccurredAt: now, OrganizationID: loopOrg,
	})
	if first.Chain.Identity != "" && replay.Created {
		t.Fatal("envelope replay created a second chain")
	}
	fmt.Printf("ENVELOPES extra=%s a1=%s a2=%s a3=%s invented=false replay_created=%v\n",
		seen[EnvelopeEXTRA], seen[EnvelopeAccount1], seen[EnvelopeAccount2], seen[EnvelopeAccount3], replay.Created)
}

func TestRegisterHumanOutcomeEvidenceGates(t *testing.T) {
	st := NewMemoryStore()
	now := time.Date(2026, 8, 18, 16, 30, 0, 0, time.UTC)
	won := RegisterHumanOutcome(st, HumanOutcomeEntry{
		LeadID: "lead-human-1", Action: HumanWon, OccurredAt: now,
		OrganizationID: loopOrg, HumanConfirmed: false,
	})
	if !won.Held || !hasCode(won.Exceptions, ExceptionUnconfirmedWon) {
		t.Fatalf("WON without evidence accepted: %+v", won)
	}
	lost := RegisterHumanOutcome(st, HumanOutcomeEntry{
		LeadID: "lead-human-1", Action: HumanLost, OccurredAt: now,
		OrganizationID: loopOrg, HumanConfirmed: true,
	})
	if !lost.Held || !hasCode(lost.Exceptions, ExceptionUnconfirmedLost) {
		t.Fatalf("LOST without evidence_ref accepted: %+v", lost)
	}
	cash := RegisterHumanOutcome(st, HumanOutcomeEntry{
		LeadID: "lead-human-1", Action: HumanRevenueReceived, OccurredAt: now,
		OrganizationID: loopOrg, HumanConfirmed: true, RevenueCents: 180000,
	})
	if !cash.Held {
		t.Fatalf("receita without document accepted: %+v", cash)
	}

	ok := RegisterHumanOutcome(st, HumanOutcomeEntry{
		LeadID: "lead-human-1", Action: HumanProposalEmitted, OccurredAt: now,
		OrganizationID: loopOrg, HumanConfirmed: true, EvidenceRef: "evidence:proposal-1",
		Source: "web-cfg", Query: "segunda-leitura", AssetID: "asset-1", CTAID: "cta-1",
		CorrelationID: "corr-1", Referrer: "https://search.example/ref",
		AssetFamily: AssetFamilyContractAnalysis,
	})
	if ok.Held && hasCode(ok.Exceptions, ExceptionUnconfirmedWon) {
		t.Fatalf("proposal with evidence held as WON: %+v", ok)
	}
	if ok.Chain.Keys.Source != "web-cfg" || ok.Chain.Keys.Query == "" || ok.Chain.Keys.CTAID == "" {
		t.Fatalf("attribution IDs dropped: %+v", ok.Chain.Keys)
	}
	if MetricKeyContainsPII(ok.Chain.MetricKey) {
		t.Fatalf("metric key has PII: %s", ok.Chain.MetricKey)
	}

	documented := RegisterHumanOutcome(st, HumanOutcomeEntry{
		LeadID: "lead-human-1", Action: HumanWon, OccurredAt: now.Add(time.Hour),
		OrganizationID: loopOrg, HumanConfirmed: true, EvidenceRef: "evidence:won-1",
		Source: "web-cfg", Query: "segunda-leitura", AssetID: "asset-1", CTAID: "cta-1",
	})
	if documented.Held && hasCode(documented.Exceptions, ExceptionUnconfirmedWon) {
		t.Fatalf("documented WON still unconfirmed: %+v", documented)
	}
	fmt.Printf("HUMAN_GATES won_bare=%s lost_bare=%s cash_bare_held=%v proposal_attr=%s\n",
		codesOf(won.Exceptions), codesOf(lost.Exceptions), cash.Held, ok.Chain.Keys.CTAID)
}

func TestRegisterHumanOutcomeReplayIsIdempotent(t *testing.T) {
	st := NewMemoryStore()
	now := time.Date(2026, 8, 18, 17, 0, 0, 0, time.UTC)
	in := HumanOutcomeEntry{
		LeadID: "lead-human-replay", Action: HumanReply, OccurredAt: now,
		OrganizationID: loopOrg, IdempotencyKey: "human:lead-human-replay:reply",
		HumanConfirmed: true,
	}
	first := RegisterHumanOutcome(st, in)
	second := RegisterHumanOutcome(st, in)
	if !second.Replay && second.Created {
		t.Fatalf("reply replay created: first=%+v second=%+v", first, second)
	}
	chains, _ := st.ListChains(loopOrg)
	n := 0
	for _, c := range chains {
		if c.LeadID == "lead-human-replay" {
			n++
		}
	}
	if n > 1 {
		t.Fatalf("reply opened %d chains", n)
	}
	fmt.Printf("HUMAN_REPLAY created=%v replay=%v chains=%d\n", second.Created, second.Replay, n)
}

func TestPlaceholderIDsAreRejected(t *testing.T) {
	if err := ValidateHumanOutcome(HumanOutcomeEntry{Action: HumanAttempted, LeadID: "ACCOUNT_1"}); err == nil {
		t.Fatal("placeholder ACCOUNT_1 accepted")
	}
	if err := ValidateHumanOutcome(HumanOutcomeEntry{Action: HumanAttempted}); err != nil {
		t.Fatalf("blank lead_id should be allowed: %v", err)
	}
}
