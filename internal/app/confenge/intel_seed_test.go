package confenge

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/app/confenge/dispatch"
	"github.com/warmbly/warmbly/internal/app/confenge/liveintel"
	"github.com/warmbly/warmbly/internal/models"
)

// intelSeedFixture builds a contact that already passes the canonical
// cold-outreach admission gates, so the tests below are about INTEL_SEED's own
// behaviour rather than about re-proving admission.
type intelSeedFixture struct {
	svc  *service
	org  uuid.UUID
	acc  *models.OutreachAccount
	cand *models.OutreachContactCandidate
	repo *memRepoFull
}

func newIntelSeedFixture(t *testing.T) *intelSeedFixture {
	t.Helper()
	allowConfengeSendingForTest(t)
	org, accID, candID := uuid.New(), uuid.New(), uuid.New()
	repo := newMemRepoWithSettings()
	observed := time.Now().UTC().Add(-24 * time.Hour)
	// A company that genuinely passes EvaluateTargetFit, so the tests exercise
	// the real predicate rather than a relaxed stand-in.
	acc := &models.OutreachAccount{
		ID: accID, OrganizationID: org, CNPJ14: "12345678000199",
		RazaoSocial:    "EMPRESA EXEMPLO ENGENHARIA LTDA",
		TargetFitClass: TargetFitConfirmed, TargetFitVersion: "v1",
		TargetFitSourceWatermark: "2026-09-01", TargetFitObservedAt: &observed,
		TargetFitFresh: true, EmailSendReady: true,
	}
	if _, err := repo.UpsertAccount(context.Background(), acc); err != nil {
		t.Fatal(err)
	}
	cand := &models.OutreachContactCandidate{
		ID: candID, OrganizationID: org, AccountID: accID,
		Name: "Maria Souza", Email: "maria@empresaexemplo.com.br",
		EmailSendReady: true,
	}
	if _, err := repo.UpsertCandidate(context.Background(), cand); err != nil {
		t.Fatal(err)
	}
	svc := &service{cfg: Config{Enabled: true}, repo: repo, liveIntel: liveintel.NoopResolver{}}
	return &intelSeedFixture{svc: svc, org: org, acc: acc, cand: cand, repo: repo}
}

func seedIntel(orgID, accountID uuid.UUID) *liveintel.LiveIntelligenceV1 {
	return &liveintel.LiveIntelligenceV1{
		Schema: liveintel.SchemaLiveIntelligenceV1, OrganizationID: orgID, AccountID: accountID,
		SubjectKey: "contrato-2026-0001", Kind: liveintel.KindOpportunity,
		Headline:  "aditivo publicado no contrato 2026-0001",
		Summary:   "O aditivo alterou o cronograma de execucao.",
		PublicURL: "https://exemplo.gov.br/contratos/2026-0001",
		// A real timestamp and attestation are both required by the contract.
		ObservedAt: time.Now().UTC().Add(-time.Hour), Attestation: "sig-abc",
	}
}

func (f *intelSeedFixture) withIntel(intel *liveintel.LiveIntelligenceV1) *intelSeedFixture {
	f.svc.liveIntel = liveintel.LookupFunc(func(context.Context, uuid.UUID, uuid.UUID) (*liveintel.LiveIntelligenceV1, error) {
		return intel, nil
	})
	return f
}

// TEST B. An eligible contact yields a deliverable INTEL_SEED message.
func TestIntelSeedAdmitsAnEligibleContactAndComposesAMessage(t *testing.T) {
	t.Setenv(EnvIntelSeedEnabled, "true")
	f := newIntelSeedFixture(t)
	f.withIntel(seedIntel(f.org, f.acc.ID))

	decision := f.svc.GateIntelSeed(context.Background(), f.org, f.cand.ID)
	if decision.Kind != IntelSeedProceed {
		t.Fatalf("eligible contact was not admitted: kind=%d reason=%q err=%v",
			decision.Kind, decision.Reason, decision.Err)
	}
	msg := decision.Message
	if msg == nil {
		t.Fatal("admitted INTEL_SEED carries no message")
	}
	if strings.TrimSpace(msg.Subject) == "" || strings.TrimSpace(msg.BodyText) == "" {
		t.Fatalf("INTEL_SEED message is empty: %+v", msg)
	}
	if msg.To != "maria@empresaexemplo.com.br" {
		t.Fatalf("unexpected recipient %q", msg.To)
	}
	// The CTA is to monitor a specific public asset, not a generic pitch.
	if !strings.Contains(msg.BodyText, "https://exemplo.gov.br/contratos/2026-0001") {
		t.Fatal("the INTEL_SEED message does not point at the specific public asset")
	}
	if !strings.Contains(strings.ToLower(msg.BodyText), "acompanhar") {
		t.Fatal("the INTEL_SEED CTA is not a monitoring offer")
	}
	if msg.SubjectKey != "contrato-2026-0001" {
		t.Fatalf("the watched subject key was not carried through: %q", msg.SubjectKey)
	}
}

// TEST B (isolation half). Running INTEL_SEED must leave first-touch gate,
// queue and reservation state exactly as it was.
func TestIntelSeedLeavesFirstTouchGateAndQueueUntouched(t *testing.T) {
	t.Setenv(EnvIntelSeedEnabled, "true")
	f := newIntelSeedFixture(t)
	f.withIntel(seedIntel(f.org, f.acc.ID))

	clock := &dispatch.FixedClock{T: time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)}
	cfg := dispatch.DefaultConfig()
	cfg.WindowStart, cfg.WindowEnd, cfg.Timezone, cfg.MinGap = "00:00", "23:59", "UTC", 0
	cfg.BusinessDaysOnly = false
	store := dispatch.NewMemoryStore()
	f.svc.governor = dispatch.NewGovernor(cfg, store, clock)

	// The first-touch gate's answer before INTEL_SEED runs.
	campaignID, contactID, sequenceID := uuid.New(), uuid.New(), uuid.New()
	before := f.svc.GateCampaignEmail(context.Background(), f.org, DefaultCampaignName,
		f.cand.Email, campaignID, contactID, sequenceID)

	// Fifty INTEL_SEED admissions. If this lane consumed governor capacity,
	// the shared hourly cap would be gone by now.
	for i := 0; i < 50; i++ {
		if d := f.svc.GateIntelSeed(context.Background(), f.org, f.cand.ID); d.Kind != IntelSeedProceed {
			t.Fatalf("INTEL_SEED pass %d was refused: kind=%d reason=%q", i, d.Kind, d.Reason)
		}
	}

	after := f.svc.GateCampaignEmail(context.Background(), f.org, DefaultCampaignName,
		f.cand.Email, campaignID, contactID, sequenceID)
	if before.Kind != after.Kind {
		t.Fatalf("INTEL_SEED changed the first-touch gate answer: %d -> %d (%q -> %q)",
			before.Kind, after.Kind, before.Reason, after.Reason)
	}
}

// The message key namespaces must be mutually unmistakable across all lanes.
func TestIntelSeedMessageKeyNamespaceIsDistinctFromEveryOtherLane(t *testing.T) {
	candidateID := uuid.New()
	seed := MessageKeyIntelSeed(candidateID, "contrato-2026-0001")
	firstTouch := MessageKeyCampaignEmail(uuid.New(), candidateID, uuid.New())
	watch := MessageKeyIntelWatch(uuid.New(), "evt-1", "hash-1")

	if !strings.HasPrefix(seed, "email:intel_seed:") {
		t.Fatalf("seed key %q is not namespaced", seed)
	}
	for _, other := range []string{firstTouch, watch} {
		if seed == other {
			t.Fatalf("INTEL_SEED key collided with %q", other)
		}
		if strings.HasPrefix(other, "email:intel_seed:") {
			t.Fatalf("another lane minted an INTEL_SEED-looking key: %q", other)
		}
	}
	// Distinct subjects for the same contact are distinct sends.
	if seed == MessageKeyIntelSeed(candidateID, "contrato-2026-0002") {
		t.Fatal("different watched subjects produced the same INTEL_SEED key")
	}
}

// Missing intelligence means INTEL_SEED has nothing to offer. It is not an
// error, and it must never be confused with an admission refusal.
func TestIntelSeedWithoutIntelligenceHasNothingToSend(t *testing.T) {
	t.Setenv(EnvIntelSeedEnabled, "true")
	f := newIntelSeedFixture(t) // NoopResolver: no intelligence at all.
	decision := f.svc.GateIntelSeed(context.Background(), f.org, f.cand.ID)
	if decision.Kind != IntelSeedNoAmmunition || decision.Reason != IntelSeedReasonNoIntel {
		t.Fatalf("absent intelligence produced kind=%d reason=%q", decision.Kind, decision.Reason)
	}
	if decision.Message != nil {
		t.Fatal("INTEL_SEED composed a message with no intelligence to point at")
	}
}

// A resolver that errors, times out or returns junk is still just "nothing to
// send" -- never a block, never an error the caller must handle.
func TestIntelSeedSurvivesEveryBrokenResolverShape(t *testing.T) {
	t.Setenv(EnvIntelSeedEnabled, "true")
	for name, resolver := range map[string]liveintel.Resolver{
		"error": liveintel.LookupFunc(func(context.Context, uuid.UUID, uuid.UUID) (*liveintel.LiveIntelligenceV1, error) {
			return nil, context.DeadlineExceeded
		}),
		"malformed": liveintel.LookupFunc(func(context.Context, uuid.UUID, uuid.UUID) (*liveintel.LiveIntelligenceV1, error) {
			return &liveintel.LiveIntelligenceV1{Schema: "WRONG/9.9"}, nil
		}),
		"no_public_url": liveintel.LookupFunc(func(orgCtx context.Context, orgID, accID uuid.UUID) (*liveintel.LiveIntelligenceV1, error) {
			intel := seedIntel(orgID, accID)
			intel.PublicURL = ""
			return intel, nil
		}),
		"panicking": panickingResolver{},
	} {
		f := newIntelSeedFixture(t)
		f.svc.liveIntel = resolver
		decision := f.svc.GateIntelSeed(context.Background(), f.org, f.cand.ID)
		if decision.Kind != IntelSeedNoAmmunition {
			t.Fatalf("%s resolver produced kind=%d reason=%q", name, decision.Kind, decision.Reason)
		}
	}
}

type panickingResolver struct{}

func (panickingResolver) Resolve(context.Context, uuid.UUID, uuid.UUID) (*liveintel.LiveIntelligenceV1, bool) {
	panic("resolver exploded")
}

// The canonical hard blocks stop INTEL_SEED exactly as they stop first touch.
func TestIntelSeedRespectsTheCanonicalHardBlocks(t *testing.T) {
	t.Setenv(EnvIntelSeedEnabled, "true")
	for name, mutate := range map[string]func(*intelSeedFixture){
		"candidate_dnc": func(f *intelSeedFixture) {
			f.cand.DoNotContact = true
			_, _ = f.repo.UpsertCandidate(context.Background(), f.cand)
		},
		"candidate_bounced": func(f *intelSeedFixture) {
			f.cand.Bounced = true
			_, _ = f.repo.UpsertCandidate(context.Background(), f.cand)
		},
		"account_dnc": func(f *intelSeedFixture) {
			f.acc.DoNotContact = true
			_, _ = f.repo.UpsertAccount(context.Background(), f.acc)
		},
		"account_blocked": func(f *intelSeedFixture) {
			f.acc.Blocked = true
			_, _ = f.repo.UpsertAccount(context.Background(), f.acc)
		},
	} {
		f := newIntelSeedFixture(t)
		f.withIntel(seedIntel(f.org, f.acc.ID))
		mutate(f)
		decision := f.svc.GateIntelSeed(context.Background(), f.org, f.cand.ID)
		if decision.Kind != IntelSeedBlocked {
			t.Fatalf("%s did not block INTEL_SEED: kind=%d reason=%q", name, decision.Kind, decision.Reason)
		}
		if !decision.PermanentSuppress() {
			t.Fatalf("%s block is not suppression-worthy", name)
		}
		if decision.Message != nil {
			t.Fatalf("%s still composed a message", name)
		}
	}
}

// Only a hard block justifies suppression, mirroring the first-touch contract.
func TestIntelSeedOnlyBlockedSuppresses(t *testing.T) {
	for kind, want := range map[IntelSeedKind]bool{
		IntelSeedProceed:      false,
		IntelSeedBlocked:      true,
		IntelSeedNotQualified: false,
		IntelSeedNoAmmunition: false,
		IntelSeedDormant:      false,
		IntelSeedTransient:    false,
		IntelSeedPaused:       false,
	} {
		if got := (IntelSeedDecision{Kind: kind}).PermanentSuppress(); got != want {
			t.Fatalf("kind=%d PermanentSuppress=%v want %v", kind, got, want)
		}
	}
}

// The operator pause and kill switch that stop first touch stop INTEL_SEED too:
// it is cold outreach, not subscription mail.
func TestIntelSeedObeysTheSameKillSwitchAsFirstTouch(t *testing.T) {
	t.Setenv(EnvIntelSeedEnabled, "true")
	f := newIntelSeedFixture(t)
	f.withIntel(seedIntel(f.org, f.acc.ID))
	if err := EngageKillSwitch(); err != nil {
		t.Fatal(err)
	}
	decision := f.svc.GateIntelSeed(context.Background(), f.org, f.cand.ID)
	if decision.Kind != IntelSeedPaused || decision.Reason != IntelSeedReasonSendingOff {
		t.Fatalf("the kill switch did not stop INTEL_SEED: kind=%d reason=%q", decision.Kind, decision.Reason)
	}
}

// INTEL_SEED is cold outreach, so it must respect the business window that
// first touch respects. A lane that composes deliverable cold mail at 3am is
// not the same lane, whatever it takes no reservation.
func TestIntelSeedObeysTheBusinessWindowLikeFirstTouch(t *testing.T) {
	t.Setenv(EnvIntelSeedEnabled, "true")
	f := newIntelSeedFixture(t)
	f.withIntel(seedIntel(f.org, f.acc.ID))

	// 03:00 UTC against a 09:00-18:00 window: outside business hours.
	clock := &dispatch.FixedClock{T: time.Date(2026, 6, 15, 3, 0, 0, 0, time.UTC)}
	cfg := dispatch.DefaultConfig()
	cfg.WindowStart, cfg.WindowEnd, cfg.Timezone = "09:00", "18:00", "UTC"
	cfg.BusinessDaysOnly = true
	f.svc.governor = dispatch.NewGovernor(cfg, dispatch.NewMemoryStore(), clock)

	outside := f.svc.GateIntelSeed(context.Background(), f.org, f.cand.ID)
	if outside.Kind != IntelSeedPaused {
		t.Fatalf("INTEL_SEED composed outside the business window: kind=%d reason=%q",
			outside.Kind, outside.Reason)
	}
	if outside.Message != nil {
		t.Fatal("INTEL_SEED produced a deliverable message outside the business window")
	}

	// The same fixture inside the window is admitted, so the test above is
	// about the window and not about some unrelated refusal.
	clock.T = time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	inside := f.svc.GateIntelSeed(context.Background(), f.org, f.cand.ID)
	if inside.Kind != IntelSeedProceed {
		t.Fatalf("INTEL_SEED refused inside the business window: kind=%d reason=%q err=%v",
			inside.Kind, inside.Reason, inside.Err)
	}
}

// The lane is off unless explicitly opted in, even for a fully eligible contact
// with valid intelligence.
func TestIntelSeedIsDormantWithoutAnExplicitOptIn(t *testing.T) {
	t.Setenv(EnvIntelSeedEnabled, "")
	f := newIntelSeedFixture(t)
	f.withIntel(seedIntel(f.org, f.acc.ID))
	decision := f.svc.GateIntelSeed(context.Background(), f.org, f.cand.ID)
	if decision.Kind != IntelSeedDormant {
		t.Fatalf("the lane produced kind=%d without an opt-in", decision.Kind)
	}
	if decision.Message != nil {
		t.Fatal("a dormant lane still produced a deliverable message")
	}
}

// Composition refuses intelligence that the contract itself rejects, so no
// INTEL_SEED copy can cite a source the system cannot stand behind.
func TestComposeIntelSeedRefusesUnattestedIntelligence(t *testing.T) {
	f := newIntelSeedFixture(t)
	for name, mutate := range map[string]func(*liveintel.LiveIntelligenceV1){
		"no_attestation": func(i *liveintel.LiveIntelligenceV1) { i.Attestation = "" },
		"no_public_url":  func(i *liveintel.LiveIntelligenceV1) { i.PublicURL = "" },
		"http_url":       func(i *liveintel.LiveIntelligenceV1) { i.PublicURL = "http://exemplo.gov.br/x" },
		"unknown_kind":   func(i *liveintel.LiveIntelligenceV1) { i.Kind = "GOSSIP" },
		"no_observed_at": func(i *liveintel.LiveIntelligenceV1) { i.ObservedAt = time.Time{} },
		"no_headline":    func(i *liveintel.LiveIntelligenceV1) { i.Headline = "" },
	} {
		intel := seedIntel(f.org, f.acc.ID)
		mutate(intel)
		if msg := ComposeIntelSeed(f.acc, f.cand, intel); msg != nil {
			t.Fatalf("%s intelligence still produced copy: %q", name, msg.BodyText)
		}
	}
}

// Freshness is a separate question from validity: stale intelligence is not
// wrong, it is just not news worth offering to watch.
func TestIntelSeedFreshnessIsSeparateFromValidity(t *testing.T) {
	f := newIntelSeedFixture(t)
	now := time.Now().UTC()
	fresh := seedIntel(f.org, f.acc.ID)
	if !IntelSeedObservedAtFresh(fresh, now, 24*time.Hour) {
		t.Fatal("an hour-old observation is not fresh within 24h")
	}
	stale := seedIntel(f.org, f.acc.ID)
	stale.ObservedAt = now.Add(-72 * time.Hour)
	if IntelSeedObservedAtFresh(stale, now, 24*time.Hour) {
		t.Fatal("a three-day-old observation counted as fresh within 24h")
	}
	if IntelSeedObservedAtFresh(nil, now, 24*time.Hour) {
		t.Fatal("absent intelligence counted as fresh")
	}
}
