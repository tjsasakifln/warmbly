package confenge

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
)

// adjustFixture is one organization with one frozen human-gate version over a
// real Postgres, plus the in-memory canonical source that version was frozen
// from. Every test gets a fresh organization id, so tests never observe each
// other's rows even though they share the schema.
type adjustFixture struct {
	t         *testing.T
	ctx       context.Context
	pool      *pgxpool.Pool
	svc       *service
	repo      *memRepo
	org       uuid.UUID
	actor     uuid.UUID
	runID     string
	source    string
	cohortID  uuid.UUID
	versionID uuid.UUID
	version   *HumanGateCohort
	candidate uuid.UUID
}

func newAdjustFixture(t *testing.T) *adjustFixture {
	t.Helper()
	dsn := testPostgresDSN(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	applyBoundedCohortSchema(t, ctx, pool)
	applyHumanGateSchema(t, pool)

	f := &adjustFixture{
		t: t, ctx: ctx, pool: pool,
		repo:   newMemRepo(),
		org:    uuid.New(),
		actor:  uuid.New(),
		runID:  "run-adjust-" + uuid.NewString(),
		source: "extra-cli",
	}
	f.svc = &service{
		humanGateDB: pool,
		repo:        f.repo,
		cohortStore: NewPostgresCohortStore(pool),
		cfg:         Config{Enabled: true, RepositorySHA: "sha-adjust", FeedMaxAge: 24 * time.Hour},
	}
	f.seedCanonicalSource()
	f.freezeVersionOne()
	return f
}

// seedCanonicalSource publishes two accounts with one controlled-eligible
// generic-company mailbox each. Nothing here is a real mailbox: every domain is
// .invalid, which can never resolve.
func (f *adjustFixture) seedCanonicalSource() {
	f.t.Helper()
	unknown := true
	for i, spec := range []struct{ ref, cnpj, mailbox string }{
		{"acc-adjust-a", "11111111000191", "contato@empresa-a.invalid"},
		{"acc-adjust-b", "22222222000192", "contato@empresa-b.invalid"},
	} {
		acc := cohortAccount(spec.ref, spec.cnpj, "Contrato vigente publicado")
		acc.ID = uuid.New()
		acc.OrganizationID = f.org
		acc.SourceRunID = f.runID
		cand := models.OutreachContactCandidate{
			ID:              uuid.New(),
			AccountID:       acc.ID,
			Email:           spec.mailbox,
			MailboxPurpose:  "GENERIC_CONTACT",
			OwnershipStatus: "COMPANY_OWNED",
			DiscoveryJSON:   eligibleDisc(f.t, RouteClassGenericCompany, true, controlledDiscovery{PersonUnknown: &unknown}),
		}
		f.repo.mu.Lock()
		f.repo.byID[acc.ID] = &acc
		f.repo.cands[acc.ID] = []models.OutreachContactCandidate{cand}
		f.repo.mu.Unlock()
		if i == 0 {
			f.candidate = cand.ID
		}
	}
}

// freezeVersionOne runs the real freeze path and stores version 1 exactly the
// way CreateHumanGateCohort does.
func (f *adjustFixture) freezeVersionOne() {
	f.t.Helper()
	now := time.Now().UTC()
	accounts, err := AccountsFromOrgForRun(f.ctx, f.repo, f.org, f.source, f.runID)
	if err != nil {
		f.t.Fatalf("load canonical source: %v", err)
	}
	freshness := &FeedSourceFreshness{
		ContractVersion: AuthoritativeFreshnessContractV1,
		Status:          "FRESH",
		AsOf:            now.Add(-time.Hour).Format(time.RFC3339Nano),
		ExpiresAt:       now.Add(23 * time.Hour).Format(time.RFC3339Nano),
		RunID:           f.runID,
	}
	snap, err := PrepareControlledCohort(accounts, CohortPrepareOptions{
		Now: now, Limit: 5, MaxDailyVolume: 5, TTL: DefaultCohortTTL,
		RepositorySHA: "sha-adjust", FeedSchemaVersion: models.OutreachSchemaV1,
		FeedIdentity: f.runID, PolicyVersion: BoundedCohortPolicyV1,
		EvidenceVersion: DefaultEvidenceVersion, Source: f.source,
		AuthoritativeSourceFreshness: freshness, RequireAuthoritativeFreshness: true,
	})
	if err != nil {
		f.t.Fatalf("prepare: %v", err)
	}
	if len(snap.Members) != 2 {
		f.t.Fatalf("members = %d, want 2", len(snap.Members))
	}
	// The fixture edits whichever member the manifest sorted first; the
	// candidate id has to come from the frozen bytes, not from seeding order.
	f.candidate = snap.Members[0].CandidateID

	f.cohortID, f.versionID = uuid.New(), uuid.New()
	raw, err := json.Marshal(snap)
	if err != nil {
		f.t.Fatal(err)
	}
	_, err = f.pool.Exec(f.ctx, `INSERT INTO confenge_cohort_versions
		(id,organization_id,cohort_id,version,source_run_id,source_system,source_as_of,freshness_expires_at,policy_version,frozen_hash,frozen_manifest,created_by,correlation_id,idempotency_key,request_hash)
		VALUES($1,$2,$3,1,$4,$5,$6,$7,$8,$9,$10,$11,'corr-fixture',$12,'fixture-hash')`,
		f.versionID, f.org, f.cohortID, f.runID, f.source, now.Add(-time.Hour), now.Add(23*time.Hour),
		BoundedCohortPolicyV1, snap.CohortHash, raw, f.actor, "fixture-"+uuid.NewString())
	if err != nil {
		f.t.Fatalf("store version 1: %v", err)
	}
	v, x := f.svc.GetHumanGateCohort(f.ctx, f.org, f.versionID, time.Now().UTC())
	if x != nil {
		f.t.Fatalf("read version 1: %v", x)
	}
	if v.Freshness != "FRESH" {
		f.t.Fatalf("fixture freshness = %s", v.Freshness)
	}
	if v.Derivation != DerivationCreate || v.ParentVersion != nil {
		f.t.Fatalf("version 1 derivation = %q parent = %v, want CREATE/nil", v.Derivation, v.ParentVersion)
	}
	f.version = v
}

func (f *adjustFixture) input(key string) HumanGateAdjustInput {
	return HumanGateAdjustInput{
		Subject:            "Reajuste contratual: posso enviar o recorte?",
		BodyText:           "Bom dia. Vi o contrato vigente publicado e preparei um recorte do reajuste. Posso enviar?",
		Reason:             "assunto anterior estava truncado",
		Confirmation:       fmt.Sprintf("v%d", f.version.Version),
		ExpectedFrozenHash: f.version.FrozenHash,
		IdempotencyKey:     key,
		CorrelationID:      "corr-" + key,
	}
}

func (f *adjustFixture) versionCount() int {
	f.t.Helper()
	var n int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM confenge_cohort_versions WHERE organization_id=$1 AND cohort_id=$2`, f.org, f.cohortID).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}

func (f *adjustFixture) rawManifest(versionID uuid.UUID) []byte {
	f.t.Helper()
	var raw []byte
	if err := f.pool.QueryRow(f.ctx, `SELECT frozen_manifest FROM confenge_cohort_versions WHERE id=$1`, versionID).Scan(&raw); err != nil {
		f.t.Fatal(err)
	}
	return raw
}

func (f *adjustFixture) memberOf(v *HumanGateCohort, candidateID uuid.UUID) *FrozenCohortMember {
	f.t.Helper()
	for i := range v.Manifest.Members {
		if v.Manifest.Members[i].CandidateID == candidateID {
			return &v.Manifest.Members[i]
		}
	}
	f.t.Fatalf("candidate %s missing from version %d", candidateID, v.Version)
	return nil
}

// ---------------------------------------------------------------------------

func TestHumanGateAdjustCreatesNextVersionAndLeavesTheParentByteIdentical(t *testing.T) {
	f := newAdjustFixture(t)
	before := f.rawManifest(f.versionID)
	beforeMember := *f.memberOf(f.version, f.candidate)

	in := f.input("adjust-happy-" + uuid.NewString())
	got, x := f.svc.AdjustHumanGateCandidate(f.ctx, f.org, f.actor, f.versionID, f.candidate, in)
	if x != nil {
		t.Fatalf("adjust: %v", x)
	}
	if got.ContractVersion != HumanGateContractV1 {
		t.Fatalf("contract = %q", got.ContractVersion)
	}
	if got.Cohort.Version != 2 || got.Cohort.CohortID != f.cohortID {
		t.Fatalf("new version = %d cohort = %s", got.Cohort.Version, got.Cohort.CohortID)
	}
	if got.Cohort.Derivation != DerivationAdjust {
		t.Fatalf("derivation = %q, want ADJUST", got.Cohort.Derivation)
	}
	if got.Cohort.ParentVersion == nil || *got.Cohort.ParentVersion != 1 {
		t.Fatalf("parent_version = %v, want 1", got.Cohort.ParentVersion)
	}
	if got.Adjustment.FromVersion != 1 || got.Adjustment.ToVersion != 2 {
		t.Fatalf("adjustment %d -> %d", got.Adjustment.FromVersion, got.Adjustment.ToVersion)
	}
	if got.Adjustment.RevokedAuthorizationID != nil {
		t.Fatalf("nothing was authorized; revoked = %v", got.Adjustment.RevokedAuthorizationID)
	}
	if len(got.Adjustment.Diff) != 2 || got.Adjustment.Diff[0].Field != "subject" || got.Adjustment.Diff[1].Field != "body_text" {
		t.Fatalf("diff = %+v", got.Adjustment.Diff)
	}

	// 1. The prior version is byte identical and still readable.
	if after := f.rawManifest(f.versionID); string(after) != string(before) {
		t.Fatal("the parent frozen_manifest was rewritten; prior versions must stay byte identical")
	}
	parent, x := f.svc.GetHumanGateCohort(f.ctx, f.org, f.versionID, time.Now().UTC())
	if x != nil {
		t.Fatalf("re-read parent: %v", x)
	}
	if got, want := *f.memberOf(parent, f.candidate), beforeMember; got.Subject != want.Subject || got.BodyText != want.BodyText || got.ContentHash != want.ContentHash {
		t.Fatal("the parent's copy changed")
	}
	if parent.FrozenHash != f.version.FrozenHash {
		t.Fatal("the parent's frozen hash changed")
	}

	// 2. Exactly the addressed member changed; every other member is copied.
	next := got.Cohort
	edited := f.memberOf(next, f.candidate)
	if edited.Subject != in.Subject || edited.BodyText != in.BodyText {
		t.Fatalf("edited copy not applied: %q / %q", edited.Subject, edited.BodyText)
	}
	if edited.ContentHash != hashControlledContent(edited.Mailbox, edited.RouteClass, in.Subject, in.BodyText) {
		t.Fatal("content hash was not recomputed with the freeze path helper")
	}
	if edited.Mailbox != beforeMember.Mailbox || edited.RouteClass != beforeMember.RouteClass || edited.EvidenceHash != beforeMember.EvidenceHash {
		t.Fatal("adjust must not touch recipient, route class or evidence")
	}
	for i := range next.Manifest.Members {
		m := next.Manifest.Members[i]
		if m.CandidateID == f.candidate {
			continue
		}
		old := f.memberOf(f.version, m.CandidateID)
		if m.Subject != old.Subject || m.BodyText != old.BodyText || m.ContentHash != old.ContentHash {
			t.Fatalf("member %s was not copied unchanged", m.CandidateID)
		}
	}

	// 3. Hashes were recomputed with the freeze path's own helpers.
	if next.FrozenHash != HashFrozenCohort(&next.Manifest) {
		t.Fatal("frozen hash does not match the recomputed manifest hash")
	}
	if next.FrozenHash == parent.FrozenHash {
		t.Fatal("an edited manifest must not keep the parent frozen hash")
	}
	if next.Manifest.CohortHash != next.FrozenHash {
		t.Fatal("manifest cohort_hash and the stored frozen_hash disagree")
	}
	if next.Manifest.RecipientSetHash != parent.Manifest.RecipientSetHash {
		t.Fatal("recipients did not change; the recipient set hash must not change")
	}
	if got.Adjustment.BeforeFrozenHash != parent.FrozenHash || got.Adjustment.AfterFrozenHash != next.FrozenHash {
		t.Fatal("the adjustment receipt does not name both frozen hashes")
	}
	if got.Adjustment.BeforeContentHash != beforeMember.ContentHash || got.Adjustment.AfterContentHash != edited.ContentHash {
		t.Fatal("the adjustment receipt does not name both content hashes")
	}

	// 4. Preview was recomputed deterministically from the new members.
	wantSamples := selectPreviewSamples(next.Manifest.Members, previewSamplePerClass)
	gotSamples, _ := json.Marshal(next.Manifest.Preview.SamplesByClass)
	expected, _ := json.Marshal(wantSamples)
	if string(gotSamples) != string(expected) {
		t.Fatal("preview samples were not recomputed from the adjusted members")
	}

	// 5. The new version carries no authority of its own.
	if next.Manifest.AuthorizationID != uuid.Nil {
		t.Fatal("a freshly adjusted version must not carry an authorization id")
	}
	if f.versionCount() != 2 {
		t.Fatalf("version rows = %d, want 2", f.versionCount())
	}
}

// The same intent replayed must return the same adjustment and must not fork
// the cohort. This is the browser retry after an ambiguous timeout.
func TestHumanGateAdjustReplayReturnsTheSameAdjustment(t *testing.T) {
	f := newAdjustFixture(t)
	key := "adjust-replay-" + uuid.NewString()

	first, x := f.svc.AdjustHumanGateCandidate(f.ctx, f.org, f.actor, f.versionID, f.candidate, f.input(key))
	if x != nil {
		t.Fatalf("first adjust: %v", x)
	}
	second, x := f.svc.AdjustHumanGateCandidate(f.ctx, f.org, f.actor, f.versionID, f.candidate, f.input(key))
	if x != nil {
		t.Fatalf("replay: %v", x)
	}
	if second.Adjustment.ID != first.Adjustment.ID || second.Adjustment.Receipt != first.Adjustment.Receipt {
		t.Fatalf("replay produced another adjustment: %s vs %s", second.Adjustment.ID, first.Adjustment.ID)
	}
	if second.Cohort.ID != first.Cohort.ID || second.Cohort.Version != 2 {
		t.Fatalf("replay produced another version: %s v%d", second.Cohort.ID, second.Cohort.Version)
	}
	if second.Adjustment.AfterFrozenHash != first.Adjustment.AfterFrozenHash {
		t.Fatal("replay returned a different frozen hash")
	}
	if f.versionCount() != 2 {
		t.Fatalf("version rows = %d, want 2 after a replay", f.versionCount())
	}

	// The same key with a different payload is a different intent, not a replay.
	conflicting := f.input(key)
	conflicting.Subject = "Outro assunto"
	if _, x = f.svc.AdjustHumanGateCandidate(f.ctx, f.org, f.actor, f.versionID, f.candidate, conflicting); x == nil || x.Identifier != "idempotency_payload_conflict" {
		t.Fatalf("got %#v, want idempotency_payload_conflict", x)
	}

	// A different key with an identical payload addresses a version that is now
	// superseded: it must fail loudly rather than silently fork the cohort.
	forked := f.input("adjust-fork-" + uuid.NewString())
	if _, x = f.svc.AdjustHumanGateCandidate(f.ctx, f.org, f.actor, f.versionID, f.candidate, forked); x == nil || x.Identifier != "version_superseded" {
		t.Fatalf("got %#v, want version_superseded", x)
	}
	if f.versionCount() != 2 {
		t.Fatalf("version rows = %d, want 2 after a rejected fork", f.versionCount())
	}
}

// Two operators adjusting the same version at the same moment must not both
// create version 2.
func TestHumanGateAdjustConcurrentDoubleAdjustCreatesOneVersion(t *testing.T) {
	f := newAdjustFixture(t)
	const tabs = 4
	results := make(chan *errx.Error, tabs)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < tabs; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, x := f.svc.AdjustHumanGateCandidate(f.ctx, f.org, f.actor, f.versionID, f.candidate, f.input("adjust-race-"+uuid.NewString()))
			results <- x
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	accepted, superseded, other := 0, 0, []string{}
	for x := range results {
		switch {
		case x == nil:
			accepted++
		case x.Identifier == "version_superseded":
			superseded++
		default:
			other = append(other, x.Identifier)
		}
	}
	if accepted != 1 || superseded != tabs-1 || len(other) != 0 {
		t.Fatalf("accepted=%d superseded=%d other=%v", accepted, superseded, other)
	}
	if n := f.versionCount(); n != 2 {
		t.Fatalf("version rows = %d, want 2: concurrent adjusts forked the cohort", n)
	}
	var adjustments int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM confenge_cohort_adjustments WHERE organization_id=$1`, f.org).Scan(&adjustments); err != nil {
		t.Fatal(err)
	}
	if adjustments != 1 {
		t.Fatalf("adjustment rows = %d, want 1", adjustments)
	}
}

// After a process restart the receipt must still be visible to a brand new
// pool, and the retry must resolve to the original adjustment rather than
// creating a second version.
func TestHumanGateAdjustRetryAfterSimulatedCrashResolvesToTheSameVersion(t *testing.T) {
	f := newAdjustFixture(t)
	key := "adjust-crash-" + uuid.NewString()
	first, x := f.svc.AdjustHumanGateCandidate(f.ctx, f.org, f.actor, f.versionID, f.candidate, f.input(key))
	if x != nil {
		t.Fatalf("adjust: %v", x)
	}

	// Simulate the crash: drop every connection and rebuild the service the way
	// a restarted process would.
	f.pool.Close()
	restarted, err := pgxpool.New(f.ctx, testPostgresDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	f.pool = restarted
	f.svc = &service{humanGateDB: restarted, repo: f.repo, cohortStore: NewPostgresCohortStore(restarted), cfg: f.svc.cfg}

	replay, x := f.svc.AdjustHumanGateCandidate(f.ctx, f.org, f.actor, f.versionID, f.candidate, f.input(key))
	if x != nil {
		t.Fatalf("retry after restart: %v", x)
	}
	if replay.Adjustment.ID != first.Adjustment.ID || replay.Cohort.ID != first.Cohort.ID {
		t.Fatal("retry after restart created a second adjustment")
	}
	if n := f.versionCount(); n != 2 {
		t.Fatalf("version rows = %d, want 2 after a restart retry", n)
	}
}

// Nothing about the old copy is evidence about the new copy.
func TestHumanGateAdjustNewVersionInheritsNoValidationReviewOrDecision(t *testing.T) {
	f := newAdjustFixture(t)
	now := time.Now().UTC()
	validationID := uuid.New()
	for _, m := range f.version.Manifest.Members {
		id := uuid.New()
		if _, err := f.pool.Exec(f.ctx, `INSERT INTO confenge_cohort_validations(id,organization_id,cohort_version_id,candidate_id,status,reason,provider,method,evidence_hash,checked_at,expires_at,actor_id,correlation_id,receipt,idempotency_key,request_hash)
			VALUES($1,$2,$3,$4,'VALID','fixture','warmbly-emailverify','syntax-mx-smtp',$5,$6,$7,$8,'corr',$9,$10,'h')`,
			id, f.org, f.versionID, m.CandidateID, m.EvidenceHash, now, now.Add(time.Hour), f.actor,
			humanGateReceipt("validation", id), "val-"+uuid.NewString()); err != nil {
			t.Fatal(err)
		}
		if m.CandidateID == f.candidate {
			validationID = id
		}
		reviewID := uuid.New()
		if _, err := f.pool.Exec(f.ctx, `INSERT INTO confenge_cohort_candidate_reviews(id,organization_id,cohort_version_id,candidate_id,decision,reason,recipient_hash,content_hash,policy_version,evidence_hash,validation_id,validation_expires_at,actor_id,correlation_id,receipt,idempotency_key,request_hash)
			VALUES($1,$2,$3,$4,'APPROVE','fixture',$5,$6,$7,$8,$9,$10,$11,'corr',$12,$13,'h')`,
			reviewID, f.org, f.versionID, m.CandidateID, HashRecipientSet([]string{m.Mailbox}), m.ContentHash,
			BoundedCohortPolicyV1, m.EvidenceHash, id, now.Add(time.Hour), f.actor,
			humanGateReceipt("review", reviewID), "rev-"+uuid.NewString()); err != nil {
			t.Fatal(err)
		}
	}
	decisionID := uuid.New()
	if _, err := f.pool.Exec(f.ctx, `INSERT INTO confenge_cohort_go_decisions(id,organization_id,cohort_version_id,decision,reason,readiness_hash,actor_id,correlation_id,receipt,idempotency_key,request_hash)
		VALUES($1,$2,$3,'NO_GO','fixture','readiness',$4,'corr',$5,$6,'h')`,
		decisionID, f.org, f.versionID, f.actor, humanGateReceipt("decision", decisionID), "dec-"+uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	_ = validationID

	parent, x := f.svc.GetHumanGateCohort(f.ctx, f.org, f.versionID, time.Now().UTC())
	if x != nil {
		t.Fatal(x)
	}
	f.version = parent
	for _, c := range parent.Candidates {
		if c.Validation == nil || c.Review == nil {
			t.Fatal("fixture did not attach validation and review to version 1")
		}
	}
	if parent.Decision == nil {
		t.Fatal("fixture did not attach a decision to version 1")
	}

	got, x := f.svc.AdjustHumanGateCandidate(f.ctx, f.org, f.actor, f.versionID, f.candidate, f.input("adjust-inherit-"+uuid.NewString()))
	if x != nil {
		t.Fatalf("adjust: %v", x)
	}
	if got.Cohort.Decision != nil {
		t.Fatalf("version 2 inherited a decision: %+v", got.Cohort.Decision)
	}
	for _, c := range got.Cohort.Candidates {
		if c.Validation != nil {
			t.Fatalf("candidate %s inherited a validation", c.CandidateID)
		}
		if c.Review != nil {
			t.Fatalf("candidate %s inherited a review", c.CandidateID)
		}
		if !containsString(c.BlockedBy, "validation_missing") || !containsString(c.BlockedBy, "approval_missing_or_invalid") {
			t.Fatalf("candidate %s must be blocked as unvalidated and unapproved: %v", c.CandidateID, c.BlockedBy)
		}
	}
	if blockers := humanGateDecisionBlockers(got.Cohort); len(blockers) == 0 {
		t.Fatal("a freshly adjusted version must not be GO-ready")
	}
	// The parent keeps its own receipts.
	parent, x = f.svc.GetHumanGateCohort(f.ctx, f.org, f.versionID, time.Now().UTC())
	if x != nil {
		t.Fatal(x)
	}
	if parent.Decision == nil || parent.Candidates[0].Validation == nil || parent.Candidates[0].Review == nil {
		t.Fatal("adjust must not remove the parent's receipts")
	}
}

func TestHumanGateAdjustRefusesMismatchedHashConfirmationAndSupersededVersion(t *testing.T) {
	f := newAdjustFixture(t)

	badHash := f.input("adjust-badhash-" + uuid.NewString())
	badHash.ExpectedFrozenHash = strings.Repeat("0", 64)
	if _, x := f.svc.AdjustHumanGateCandidate(f.ctx, f.org, f.actor, f.versionID, f.candidate, badHash); x == nil || x.Identifier != "frozen_hash_mismatch" || x.Code != errx.Conflict {
		t.Fatalf("got %#v, want 409 frozen_hash_mismatch", x)
	}

	badConfirm := f.input("adjust-badconfirm-" + uuid.NewString())
	badConfirm.Confirmation = "v9"
	if _, x := f.svc.AdjustHumanGateCandidate(f.ctx, f.org, f.actor, f.versionID, f.candidate, badConfirm); x == nil || x.Identifier != "confirmation_mismatch" || x.Code != errx.Conflict {
		t.Fatalf("got %#v, want 409 confirmation_mismatch", x)
	}

	missing := f.input("adjust-missing-" + uuid.NewString())
	if _, x := f.svc.AdjustHumanGateCandidate(f.ctx, f.org, f.actor, f.versionID, uuid.New(), missing); x == nil || x.Identifier != "candidate_not_found" || x.Code != errx.NotFound {
		t.Fatalf("got %#v, want 404 candidate_not_found", x)
	}
	if n := f.versionCount(); n != 1 {
		t.Fatalf("version rows = %d, want 1: a refused adjust must write nothing", n)
	}

	// Accept one adjust, then address the now-superseded version 1 again.
	if _, x := f.svc.AdjustHumanGateCandidate(f.ctx, f.org, f.actor, f.versionID, f.candidate, f.input("adjust-ok-"+uuid.NewString())); x != nil {
		t.Fatalf("adjust: %v", x)
	}
	if _, x := f.svc.AdjustHumanGateCandidate(f.ctx, f.org, f.actor, f.versionID, f.candidate, f.input("adjust-late-"+uuid.NewString())); x == nil || x.Identifier != "version_superseded" || x.Code != errx.Conflict {
		t.Fatalf("got %#v, want 409 version_superseded", x)
	}
	if n := f.versionCount(); n != 2 {
		t.Fatalf("version rows = %d, want 2", n)
	}
}

func TestHumanGateAdjustRefusesCopyThatFailsCopyQA(t *testing.T) {
	f := newAdjustFixture(t)
	bad := f.input("adjust-qa-" + uuid.NewString())
	bad.BodyText = "Segue o registro: decision_unit=COMERCIAL; email_discovery_class=GENERIC_COMPANY."
	_, x := f.svc.AdjustHumanGateCandidate(f.ctx, f.org, f.actor, f.versionID, f.candidate, bad)
	if x == nil || x.Identifier != "copy_qa_failed" || x.Code != errx.Unprocessable {
		t.Fatalf("got %#v, want 422 copy_qa_failed", x)
	}
	if !strings.Contains(x.Message, "internal_dump") {
		t.Fatalf("copy_qa_failed must name the reason codes: %q", x.Message)
	}
	if n := f.versionCount(); n != 1 {
		t.Fatalf("version rows = %d, want 1: failed copy QA must write nothing", n)
	}
}

func TestHumanGateAdjustRefusesImmutableFieldsOverTheRealStore(t *testing.T) {
	f := newAdjustFixture(t)
	for _, field := range humanGateImmutableFields {
		in := f.input("adjust-immutable-" + uuid.NewString())
		in.RawBody = []byte(`{"subject":"a","body_text":"b","` + field + `":"typed-by-hand"}`)
		_, x := f.svc.AdjustHumanGateCandidate(f.ctx, f.org, f.actor, f.versionID, f.candidate, in)
		if x == nil || x.Identifier != "immutable_field" || x.Code != errx.Unprocessable {
			t.Fatalf("%s: got %#v, want 422 immutable_field", field, x)
		}
		if !strings.Contains(x.Message, field) {
			t.Fatalf("%s: error must name the field: %q", field, x.Message)
		}
	}
	if n := f.versionCount(); n != 1 {
		t.Fatalf("version rows = %d, want 1", n)
	}
}

// A live authority on the prior version is revoked in the same transaction that
// creates N+1. There must never be two valid authorities.
func TestHumanGateAdjustRevokesTheLiveAuthorityAtomically(t *testing.T) {
	f := newAdjustFixture(t)
	authID := f.authorizeVersionOne(t, time.Now().UTC().Add(6*time.Hour))

	got, x := f.svc.AdjustHumanGateCandidate(f.ctx, f.org, f.actor, f.versionID, f.candidate, f.input("adjust-revoke-"+uuid.NewString()))
	if x != nil {
		t.Fatalf("adjust: %v", x)
	}
	if got.Adjustment.RevokedAuthorizationID == nil || *got.Adjustment.RevokedAuthorizationID != authID {
		t.Fatalf("revoked = %v, want %s", got.Adjustment.RevokedAuthorizationID, authID)
	}
	var live int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM confenge_bounded_cohort_authorizations WHERE organization_id=$1 AND revoked_at IS NULL AND expires_at > now()`, f.org).Scan(&live); err != nil {
		t.Fatal(err)
	}
	if live != 0 {
		t.Fatalf("live authorities = %d, want 0: adjust must never leave two valid authorities", live)
	}
	grant, err := f.svc.cohortStore.GetGrant(f.ctx, authID)
	if err != nil {
		t.Fatal(err)
	}
	if grant.RevokedAt == nil || !strings.HasPrefix(grant.RevokeReason, "human_gate_adjust:") {
		t.Fatalf("prior authority not revoked with an attributable reason: %+v", grant.Summary())
	}
	if got.Cohort.Manifest.AuthorizationID != uuid.Nil {
		t.Fatal("the adjusted version must not be born with an authority")
	}
}

// If the authority cannot be proven revoked inside the same transaction, the
// adjustment is refused and nothing is written.
func TestHumanGateAdjustRefusesWhenTheLiveAuthorityCannotBeRevoked(t *testing.T) {
	f := newAdjustFixture(t)
	// A GO receipt naming an authority row that this transaction cannot lock:
	// the decision points at an id that is not in the authority table, but the
	// GO receipt itself is durable, so adjust must fail closed rather than
	// assume the authority is dead.
	ghost := uuid.New()
	f.recordGO(t, ghost)
	if _, err := f.pool.Exec(f.ctx, `ALTER TABLE confenge_bounded_cohort_authorizations RENAME TO confenge_bounded_cohort_authorizations_hidden`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = f.pool.Exec(f.ctx, `ALTER TABLE confenge_bounded_cohort_authorizations_hidden RENAME TO confenge_bounded_cohort_authorizations`)
	}()

	_, x := f.svc.AdjustHumanGateCandidate(f.ctx, f.org, f.actor, f.versionID, f.candidate, f.input("adjust-authority-"+uuid.NewString()))
	if x == nil || x.Identifier != "authority_active" || x.Code != errx.Conflict {
		t.Fatalf("got %#v, want 409 authority_active", x)
	}
	if n := f.versionCount(); n != 1 {
		t.Fatalf("version rows = %d, want 1: a refused adjust must write nothing", n)
	}
}

// A GO whose authority already expired is not a live authority.
func TestHumanGateAdjustProceedsWhenThePriorAuthorityIsAlreadyDead(t *testing.T) {
	f := newAdjustFixture(t)
	authID := f.authorizeVersionOne(t, time.Now().UTC().Add(-time.Minute))

	got, x := f.svc.AdjustHumanGateCandidate(f.ctx, f.org, f.actor, f.versionID, f.candidate, f.input("adjust-expired-"+uuid.NewString()))
	if x != nil {
		t.Fatalf("adjust: %v", x)
	}
	if got.Adjustment.RevokedAuthorizationID != nil {
		t.Fatalf("an expired authority must not be reported as revoked by this adjust: %v", got.Adjustment.RevokedAuthorizationID)
	}
	grant, err := f.svc.cohortStore.GetGrant(f.ctx, authID)
	if err != nil {
		t.Fatal(err)
	}
	if grant.RevokedAt != nil {
		t.Fatal("adjust must not rewrite the revocation history of a dead authority")
	}
}

// authorizeVersionOne stores a real bounded authority bound to version 1's
// frozen manifest and the GO receipt that points at it.
func (f *adjustFixture) authorizeVersionOne(t *testing.T, expiresAt time.Time) uuid.UUID {
	t.Helper()
	snapshot := f.version.Manifest
	snapshot.AuthorizationID = uuid.New()
	auth, err := GrantFromFrozenSnapshot(&snapshot, f.org, f.actor, time.Now().UTC())
	if err != nil {
		t.Fatalf("grant: %v", err)
	}
	auth.ExpiresAt = expiresAt
	auth.TTL = expiresAt.Sub(auth.AuthorizedAt)
	if err = f.svc.cohortStore.PutGrant(f.ctx, auth); err != nil {
		t.Fatalf("put grant: %v", err)
	}
	if err = f.svc.cohortStore.RecordGOReview(f.ctx, auth.ID, f.actor, HumanGateReadyVerdict, "fixture", time.Now().UTC()); err != nil {
		t.Fatalf("record go review: %v", err)
	}
	f.recordGO(t, auth.ID)
	return auth.ID
}

func (f *adjustFixture) recordGO(t *testing.T, authorizationID uuid.UUID) {
	t.Helper()
	id := uuid.New()
	if _, err := f.pool.Exec(f.ctx, `INSERT INTO confenge_cohort_go_decisions(id,organization_id,cohort_version_id,decision,reason,readiness_hash,authorization_id,actor_id,correlation_id,receipt,idempotency_key,request_hash)
		VALUES($1,$2,$3,'GO','fixture','readiness',$4,$5,'corr',$6,$7,'h')`,
		id, f.org, f.versionID, authorizationID, f.actor, humanGateReceipt("decision", id), "go-"+uuid.NewString()); err != nil {
		t.Fatal(err)
	}
}
