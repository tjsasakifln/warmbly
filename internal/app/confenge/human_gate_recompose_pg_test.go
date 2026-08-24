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

// recomposeLegacyComposer is the stamp an earlier build left on frozen copy;
// moving a version off it is the whole reason recompose exists.
const recomposeLegacyComposer = "confenge.composer.v3"

// recomposeFixture is one organization holding one immutable, legacy-stamped
// human-gate version over a real Postgres, plus the in-memory canonical source
// it was frozen from. Every test gets a fresh organization id, so tests never
// observe each other's rows even though they share the schema.
type recomposeFixture struct {
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
}

func newRecomposeFixture(t *testing.T) *recomposeFixture {
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

	f := &recomposeFixture{
		t: t, ctx: ctx, pool: pool,
		repo:   newMemRepo(),
		org:    uuid.New(),
		actor:  uuid.New(),
		runID:  "run-recompose-" + uuid.NewString(),
		source: "extra-cli",
	}
	f.svc = &service{
		humanGateDB: pool,
		repo:        f.repo,
		cohortStore: NewPostgresCohortStore(pool),
		cfg:         Config{Enabled: true, RepositorySHA: "sha-recompose", FeedMaxAge: 24 * time.Hour},
	}
	f.seedCanonicalSource()
	f.freezeLegacyVersionOne()
	return f
}

// seedCanonicalSource publishes three accounts with one controlled-eligible
// generic-company mailbox each. Nothing here is a real mailbox: every domain is
// .invalid, which can never resolve.
func (f *recomposeFixture) seedCanonicalSource() {
	f.t.Helper()
	unknown := true
	for _, spec := range []struct{ ref, cnpj, fact, mailbox string }{
		// Record-shaped facts, because the composer digests a public record and
		// refuses a generic phrase that names no work.
		{"acc-recompose-a", "11111111000191", "objeto: recuperação estrutural da ponte sobre o Rio Sapucaí; órgão: DNIT; UF MG", "contato@empresa-ra.invalid"},
		{"acc-recompose-b", "22222222000192", "objeto: licenciamento ambiental para mineração de calcário; órgão: SEMAD; UF MG", "contato@empresa-rb.invalid"},
		{"acc-recompose-c", "33333333000193", "objeto: manutenção preventiva das estações elevatórias de esgoto; órgão: SANEAGO; UF GO", "contato@empresa-rc.invalid"},
	} {
		acc := cohortAccount(spec.ref, spec.cnpj, spec.fact)
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
	}
}

// freezeLegacyVersionOne runs the real freeze path, then stamps the manifest
// with the legacy composer and its own legacy copy, exactly as a version frozen
// by an earlier build would look on disk today.
func (f *recomposeFixture) freezeLegacyVersionOne() {
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
		RepositorySHA: "sha-recompose", FeedSchemaVersion: models.OutreachSchemaV1,
		FeedIdentity: f.runID, PolicyVersion: BoundedCohortPolicyV1,
		EvidenceVersion: DefaultEvidenceVersion, Source: f.source,
		AuthoritativeSourceFreshness: freshness, RequireAuthoritativeFreshness: true,
	})
	if err != nil {
		f.t.Fatalf("prepare: %v", err)
	}
	if len(snap.Members) != 3 {
		f.t.Fatalf("members = %d, want 3", len(snap.Members))
	}

	snap.ComposerVersion = recomposeLegacyComposer
	for i := range snap.Members {
		m := &snap.Members[i]
		m.ComposerVersion = recomposeLegacyComposer
		m.Subject = "Assunto antigo sobre " + m.Company
		m.BodyText = "Bom dia. Texto antigo escrito pelo composer anterior para " + m.Company + ". Posso enviar o recorte?"
		m.ObservedFact = m.SourceFact
		m.ContentHash = hashControlledContent(m.Mailbox, m.RouteClass, m.Subject, m.BodyText)
	}
	snap.CohortHash = HashFrozenCohort(snap)

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

func (f *recomposeFixture) input(key string) HumanGateRecomposeInput {
	return HumanGateRecomposeInput{
		Reason:             "mover a copia legada para o composer atual",
		Confirmation:       fmt.Sprintf("v%d", f.version.Version),
		ExpectedFrozenHash: f.version.FrozenHash,
		IdempotencyKey:     key,
		CorrelationID:      "corr-" + key,
	}
}

func (f *recomposeFixture) versionCount() int {
	f.t.Helper()
	var n int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM confenge_cohort_versions WHERE organization_id=$1 AND cohort_id=$2`, f.org, f.cohortID).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}

func (f *recomposeFixture) maxVersion() int {
	f.t.Helper()
	var n int
	if err := f.pool.QueryRow(f.ctx, `SELECT COALESCE(max(version),0) FROM confenge_cohort_versions WHERE organization_id=$1 AND cohort_id=$2`, f.org, f.cohortID).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}

// versionRow returns the durable columns lineage is judged by, reading the
// manifest as text so a byte comparison is a byte comparison.
func (f *recomposeFixture) versionRow(versionID uuid.UUID) (manifest, frozenHash, derivation, composerVersion string) {
	f.t.Helper()
	if err := f.pool.QueryRow(f.ctx,
		`SELECT frozen_manifest::text,frozen_hash,derivation,COALESCE(frozen_manifest->>'composer_version','') FROM confenge_cohort_versions WHERE id=$1 AND organization_id=$2`,
		versionID, f.org).Scan(&manifest, &frozenHash, &derivation, &composerVersion); err != nil {
		f.t.Fatal(err)
	}
	return manifest, frozenHash, derivation, composerVersion
}

func (f *recomposeFixture) parentMember(candidateID uuid.UUID) FrozenCohortMember {
	f.t.Helper()
	for _, m := range f.version.Manifest.Members {
		if m.CandidateID == candidateID {
			return m
		}
	}
	f.t.Fatalf("candidate %s is not in the parent version", candidateID)
	return FrozenCohortMember{}
}

// authorizeParent stores a real bounded authority bound to version 1's frozen
// manifest plus the GO receipt that points at it.
func (f *recomposeFixture) authorizeParent(expiresAt time.Time) uuid.UUID {
	f.t.Helper()
	snapshot := f.version.Manifest
	snapshot.AuthorizationID = uuid.New()
	// Production's legacy grants were minted while their composer was still the
	// current one; the composer constant moved on afterwards and left them
	// behind. Mint at that moment, then let the parent stay legacy.
	snapshot.ComposerVersion = ComposerVersion
	snapshot.PolicyVersion = BoundedCohortPolicyV1
	snapshot.Members = append([]FrozenCohortMember(nil), snapshot.Members...)
	for i := range snapshot.Members {
		snapshot.Members[i].ComposerVersion = ComposerVersion
	}
	auth, err := GrantFromFrozenSnapshot(&snapshot, f.org, f.actor, time.Now().UTC())
	if err != nil {
		f.t.Fatalf("grant: %v", err)
	}
	auth.ExpiresAt = expiresAt
	auth.TTL = expiresAt.Sub(auth.AuthorizedAt)
	if err = f.svc.cohortStore.PutGrant(f.ctx, auth); err != nil {
		f.t.Fatalf("put grant: %v", err)
	}
	if err = f.svc.cohortStore.RecordGOReview(f.ctx, auth.ID, f.actor, HumanGateReadyVerdict, "fixture", time.Now().UTC()); err != nil {
		f.t.Fatalf("record go review: %v", err)
	}
	id := uuid.New()
	if _, err := f.pool.Exec(f.ctx, `INSERT INTO confenge_cohort_go_decisions(id,organization_id,cohort_version_id,decision,reason,readiness_hash,authorization_id,actor_id,correlation_id,receipt,idempotency_key,request_hash)
		VALUES($1,$2,$3,'GO','fixture','readiness',$4,$5,'corr',$6,$7,'h')`,
		id, f.org, f.versionID, auth.ID, f.actor, humanGateReceipt("decision", id), "go-"+uuid.NewString()); err != nil {
		f.t.Fatal(err)
	}
	return auth.ID
}

// approveEveryParentCandidate attaches a VALID validation and an APPROVE review
// to every member of version 1, so the recomposed version has something real to
// fail to inherit.
func (f *recomposeFixture) approveEveryParentCandidate() {
	f.t.Helper()
	now := time.Now().UTC()
	for _, m := range f.version.Manifest.Members {
		validationID := uuid.New()
		if _, err := f.pool.Exec(f.ctx, `INSERT INTO confenge_cohort_validations(id,organization_id,cohort_version_id,candidate_id,status,reason,provider,method,evidence_hash,checked_at,expires_at,actor_id,correlation_id,receipt,idempotency_key,request_hash)
			VALUES($1,$2,$3,$4,'VALID','fixture','warmbly-emailverify','syntax-mx-smtp',$5,$6,$7,$8,'corr',$9,$10,'h')`,
			validationID, f.org, f.versionID, m.CandidateID, m.EvidenceHash, now, now.Add(time.Hour), f.actor,
			humanGateReceipt("validation", validationID), "val-"+uuid.NewString()); err != nil {
			f.t.Fatal(err)
		}
		reviewID := uuid.New()
		if _, err := f.pool.Exec(f.ctx, `INSERT INTO confenge_cohort_candidate_reviews(id,organization_id,cohort_version_id,candidate_id,decision,reason,recipient_hash,content_hash,policy_version,evidence_hash,validation_id,validation_expires_at,actor_id,correlation_id,receipt,idempotency_key,request_hash)
			VALUES($1,$2,$3,$4,'APPROVE','fixture',$5,$6,$7,$8,$9,$10,$11,'corr',$12,$13,'h')`,
			reviewID, f.org, f.versionID, m.CandidateID, HashRecipientSet([]string{m.Mailbox}), m.ContentHash,
			BoundedCohortPolicyV1, m.EvidenceHash, validationID, now.Add(time.Hour), f.actor,
			humanGateReceipt("review", reviewID), "rev-"+uuid.NewString()); err != nil {
			f.t.Fatal(err)
		}
	}
}

// ---------------------------------------------------------------------------

// Two operators recomposing the same version at the same moment must not both
// create version 2.
func TestHumanGateRecomposeConcurrentForksOfOneVersionCreateOnlyOneNextVersion(t *testing.T) {
	f := newRecomposeFixture(t)
	const tabs = 2
	results := make(chan *errx.Error, tabs)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < tabs; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			// Different idempotency keys on purpose: the intent lock cannot be
			// what serializes these, only the parent row lock can.
			_, x := f.svc.RecomposeHumanGateCohort(f.ctx, f.org, f.actor, f.versionID, f.input("recompose-race-"+uuid.NewString()))
			results <- x
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	accepted, refused := 0, []string{}
	for x := range results {
		if x == nil {
			accepted++
			continue
		}
		refused = append(refused, x.Identifier)
	}
	if accepted != 1 || len(refused) != tabs-1 {
		t.Fatalf("accepted=%d refused=%v, want exactly one accepted", accepted, refused)
	}
	if refused[0] != "version_superseded" && refused[0] != "cohort_store_failed" {
		t.Fatalf("refused with %q, want version_superseded or a serialization refusal", refused[0])
	}
	if n := f.versionCount(); n != 2 {
		t.Fatalf("version rows = %d, want 2: concurrent recompositions forked the cohort", n)
	}
	if v := f.maxVersion(); v != 2 {
		t.Fatalf("max(version) = %d, want 2", v)
	}
}

// The browser retry after an ambiguous timeout must resolve to the version that
// was already created, and a reused key with a new payload is a new intent.
func TestHumanGateRecomposeReplayReturnsTheSameVersionAndCreatesNothing(t *testing.T) {
	f := newRecomposeFixture(t)
	key := "recompose-replay-" + uuid.NewString()

	first, x := f.svc.RecomposeHumanGateCohort(f.ctx, f.org, f.actor, f.versionID, f.input(key))
	if x != nil {
		t.Fatalf("first recompose: %v", x)
	}
	if first.Cohort.Version != 2 {
		t.Fatalf("first recompose produced version %d, want 2", first.Cohort.Version)
	}
	second, x := f.svc.RecomposeHumanGateCohort(f.ctx, f.org, f.actor, f.versionID, f.input(key))
	if x != nil {
		t.Fatalf("replay: %v", x)
	}
	if second.Cohort.ID != first.Cohort.ID || second.Cohort.Version != first.Cohort.Version {
		t.Fatalf("replay produced another version: %s v%d vs %s v%d", second.Cohort.ID, second.Cohort.Version, first.Cohort.ID, first.Cohort.Version)
	}
	if second.Cohort.FrozenHash != first.Cohort.FrozenHash {
		t.Fatalf("replay returned frozen hash %q, want %q", second.Cohort.FrozenHash, first.Cohort.FrozenHash)
	}
	if n := f.versionCount(); n != 2 {
		t.Fatalf("version rows = %d, want 2 after a replay", n)
	}

	conflicting := f.input(key)
	conflicting.Reason = "outro motivo completamente diferente"
	if _, x = f.svc.RecomposeHumanGateCohort(f.ctx, f.org, f.actor, f.versionID, conflicting); x == nil || x.Identifier != "idempotency_payload_conflict" || x.Code != errx.Conflict {
		t.Fatalf("got %#v, want 409 idempotency_payload_conflict", x)
	}
	if n := f.versionCount(); n != 2 {
		t.Fatalf("version rows = %d, want 2 after a rejected payload conflict", n)
	}
}

// Nothing that was approved about the old copy is evidence about the new copy,
// and the authority the parent carried dies in the same transaction.
func TestHumanGateRecomposedVersionInheritsNoAuthority(t *testing.T) {
	f := newRecomposeFixture(t)
	f.approveEveryParentCandidate()
	authID := f.authorizeParent(time.Now().UTC().Add(6 * time.Hour))

	parent, x := f.svc.GetHumanGateCohort(f.ctx, f.org, f.versionID, time.Now().UTC())
	if x != nil {
		t.Fatal(x)
	}
	if parent.Decision == nil || parent.Decision.Decision != "GO" {
		t.Fatalf("fixture did not attach a GO to version 1: %+v", parent.Decision)
	}
	for _, c := range parent.Candidates {
		if c.Review == nil || c.Review.Decision != "APPROVE" {
			t.Fatalf("fixture did not attach an APPROVE to candidate %s", c.CandidateID)
		}
	}

	got, x := f.svc.RecomposeHumanGateCohort(f.ctx, f.org, f.actor, f.versionID, f.input("recompose-authority-"+uuid.NewString()))
	if x != nil {
		t.Fatalf("recompose: %v", x)
	}
	if got.Cohort.Derivation != DerivationRecompose {
		t.Fatalf("derivation = %q, want RECOMPOSE", got.Cohort.Derivation)
	}
	if got.Cohort.ParentVersion == nil || *got.Cohort.ParentVersion != f.version.Version {
		t.Fatalf("parent_version = %v, want %d", got.Cohort.ParentVersion, f.version.Version)
	}
	if got.Cohort.Manifest.AuthorizationID != uuid.Nil {
		t.Fatalf("recomposed manifest carries authorization id %s; N+1 must be born with no authority", got.Cohort.Manifest.AuthorizationID)
	}
	if got.Cohort.Decision != nil {
		t.Fatalf("recomposed version inherited a decision: %+v", got.Cohort.Decision)
	}
	for _, c := range got.Cohort.Candidates {
		if c.Review != nil && c.Review.Effective {
			t.Fatalf("candidate %s inherited an effective review: %+v", c.CandidateID, c.Review)
		}
		if !containsString(c.BlockedBy, "approval_missing_or_invalid") {
			t.Fatalf("candidate %s must be blocked as unapproved: %v", c.CandidateID, c.BlockedBy)
		}
	}
	if got.RevokedAuthorizationID == nil || *got.RevokedAuthorizationID != authID {
		t.Fatalf("revoked = %v, want %s", got.RevokedAuthorizationID, authID)
	}
	var revokedAt *time.Time
	var revokeReason string
	if err := f.pool.QueryRow(f.ctx, `SELECT revoked_at,COALESCE(revoke_reason,'') FROM confenge_bounded_cohort_authorizations WHERE id=$1 AND organization_id=$2`, authID, f.org).Scan(&revokedAt, &revokeReason); err != nil {
		t.Fatal(err)
	}
	if revokedAt == nil {
		t.Fatal("the authority bound to the parent is still live after a recompose")
	}
	if !strings.Contains(revokeReason, "human_gate_recompose:") {
		t.Fatalf("revoke_reason = %q, want it to attribute the recompose", revokeReason)
	}
	var live int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM confenge_bounded_cohort_authorizations WHERE organization_id=$1 AND revoked_at IS NULL AND expires_at > now()`, f.org).Scan(&live); err != nil {
		t.Fatal(err)
	}
	if live != 0 {
		t.Fatalf("live authorities = %d, want 0: recompose must never leave two valid authorities", live)
	}
}

// History is evidence: the parent row may not be rewritten by the operation
// that forks it.
func TestHumanGateRecomposeLeavesTheParentByteIdentical(t *testing.T) {
	f := newRecomposeFixture(t)
	beforeManifest, beforeHash, beforeDerivation, beforeComposer := f.versionRow(f.versionID)
	if beforeComposer != recomposeLegacyComposer {
		t.Fatalf("fixture composer stamp = %q, want %q", beforeComposer, recomposeLegacyComposer)
	}

	got, x := f.svc.RecomposeHumanGateCohort(f.ctx, f.org, f.actor, f.versionID, f.input("recompose-lineage-"+uuid.NewString()))
	if x != nil {
		t.Fatalf("recompose: %v", x)
	}

	afterManifest, afterHash, afterDerivation, afterComposer := f.versionRow(f.versionID)
	for _, tc := range []struct{ field, got, want string }{
		{"frozen_manifest", afterManifest, beforeManifest},
		{"frozen_hash", afterHash, beforeHash},
		{"derivation", afterDerivation, beforeDerivation},
		{"composer_version", afterComposer, beforeComposer},
	} {
		if tc.got != tc.want {
			t.Fatalf("parent %s was rewritten:\n got %s\nwant %s", tc.field, tc.got, tc.want)
		}
	}

	// The child is a new row that names the parent, not a mutation of it.
	_, childHash, childDerivation, childComposer := f.versionRow(got.Cohort.ID)
	if childDerivation != DerivationRecompose {
		t.Fatalf("child derivation = %q, want RECOMPOSE", childDerivation)
	}
	if childComposer != ComposerVersion {
		t.Fatalf("child composer = %q, want %q", childComposer, ComposerVersion)
	}
	if childHash == beforeHash {
		t.Fatal("the recomposed version kept the parent frozen hash; recomposed copy must be new bytes")
	}
	if got.Report.ComposerBefore != recomposeLegacyComposer || got.Report.ComposerAfter != ComposerVersion {
		t.Fatalf("report composer %s -> %s", got.Report.ComposerBefore, got.Report.ComposerAfter)
	}
	if got.Report.KeptMembers < 1 || got.Report.ParentMembers != len(f.version.Manifest.Members) {
		t.Fatalf("report parent=%d kept=%d, want a reconciling report over %d parent members",
			got.Report.ParentMembers, got.Report.KeptMembers, len(f.version.Manifest.Members))
	}
	// Every surviving member is new copy under the current composer, addressed
	// to the same mailbox on the same evidence.
	for _, m := range got.Cohort.Manifest.Members {
		was := f.parentMember(m.CandidateID)
		if m.ComposerVersion != ComposerVersion {
			t.Fatalf("member %s kept composer stamp %q", m.CandidateID, m.ComposerVersion)
		}
		if m.Subject == was.Subject && m.BodyText == was.BodyText {
			t.Fatalf("member %s kept the legacy copy verbatim", m.CandidateID)
		}
		if m.Mailbox != was.Mailbox || m.EvidenceHash != was.EvidenceHash || m.RouteClass != was.RouteClass {
			t.Fatalf("member %s changed a fact recompose may not change", m.CandidateID)
		}
	}
}

// The parent stays fully readable for audit and is loudly non-actionable.
func TestHumanGateRecomposedParentStaysReadableButNonActionable(t *testing.T) {
	f := newRecomposeFixture(t)
	got, x := f.svc.RecomposeHumanGateCohort(f.ctx, f.org, f.actor, f.versionID, f.input("recompose-legacy-"+uuid.NewString()))
	if x != nil {
		t.Fatalf("recompose: %v", x)
	}

	parent, x := f.svc.GetHumanGateCohort(f.ctx, f.org, f.versionID, time.Now().UTC())
	if x != nil {
		t.Fatalf("re-read parent: %v", x)
	}
	if len(parent.Candidates) != len(f.version.Manifest.Members) {
		t.Fatalf("parent candidates = %d, want %d", len(parent.Candidates), len(f.version.Manifest.Members))
	}
	for _, c := range parent.Candidates {
		if strings.TrimSpace(c.Subject) == "" || strings.TrimSpace(c.BodyText) == "" {
			t.Fatalf("candidate %s lost its text; history must stay readable", c.CandidateID)
		}
		if c.EditorialState != EditorialStateSuperseded || c.Actionable {
			t.Fatalf("candidate %s editorial state = %q actionable = %v", c.CandidateID, c.EditorialState, c.Actionable)
		}
	}
	if parent.EditorialState != EditorialStateSuperseded {
		t.Fatalf("parent editorial state = %q, want %s", parent.EditorialState, EditorialStateSuperseded)
	}
	if parent.Actionable {
		t.Fatal("a superseded parent must not be actionable")
	}
	if !containsString(parent.EditorialReasonCodes, ReasonComposerSuperseded) {
		t.Fatalf("parent reason codes = %v, want %s", parent.EditorialReasonCodes, ReasonComposerSuperseded)
	}
	if parent.EditorialNotice != EditorialLegacyNotice {
		t.Fatalf("parent notice = %q", parent.EditorialNotice)
	}
	if parent.IsCurrentVersion {
		t.Fatal("the parent must not present itself as the current version")
	}
	if parent.CurrentVersion != got.Cohort.Version {
		t.Fatalf("parent current_version = %d, want %d", parent.CurrentVersion, got.Cohort.Version)
	}
	if parent.CurrentVersionID == nil || *parent.CurrentVersionID != got.Cohort.ID {
		t.Fatalf("parent current_version_id = %v, want %s", parent.CurrentVersionID, got.Cohort.ID)
	}

	// The version the founder is handed instead is the actionable one.
	if got.Cohort.EditorialState != EditorialStateCurrent || !got.Cohort.Actionable || !got.Cohort.IsCurrentVersion {
		t.Fatalf("recomposed version is not current: state=%q actionable=%v is_current=%v",
			got.Cohort.EditorialState, got.Cohort.Actionable, got.Cohort.IsCurrentVersion)
	}
}

// Closing out history must stay possible, so only APPROVE is refused on it.
func TestHumanGateRecomposedParentRefusesApproveButAcceptsHoldAndReject(t *testing.T) {
	f := newRecomposeFixture(t)
	f.approveEveryParentCandidate()
	if _, x := f.svc.RecomposeHumanGateCohort(f.ctx, f.org, f.actor, f.versionID, f.input("recompose-approve-"+uuid.NewString())); x != nil {
		t.Fatalf("recompose: %v", x)
	}
	candidate := f.version.Manifest.Members[0].CandidateID

	approve := HumanGateReviewInput{
		Decision: "APPROVE", Reason: "quero aprovar a copia antiga", Acknowledged: true,
		IdempotencyKey: "review-approve-" + uuid.NewString(), CorrelationID: "corr",
	}
	_, x := f.svc.ReviewHumanGateCandidate(f.ctx, f.org, f.actor, f.versionID, candidate, approve)
	if x == nil || x.Identifier != ReasonComposerSuperseded || x.Code != errx.Conflict {
		t.Fatalf("got %#v, want 409 %s", x, ReasonComposerSuperseded)
	}

	for _, decision := range []string{"HOLD", "REJECT"} {
		in := HumanGateReviewInput{
			Decision: decision, Reason: "encerrando a versao historica",
			IdempotencyKey: "review-" + strings.ToLower(decision) + "-" + uuid.NewString(), CorrelationID: "corr",
		}
		v, x := f.svc.ReviewHumanGateCandidate(f.ctx, f.org, f.actor, f.versionID, candidate, in)
		if x != nil {
			t.Fatalf("%s on a superseded version must stay possible: %v", decision, x)
		}
		var stored string
		if err := f.pool.QueryRow(f.ctx, `SELECT decision FROM confenge_cohort_candidate_reviews WHERE organization_id=$1 AND idempotency_key=$2`, f.org, in.IdempotencyKey).Scan(&stored); err != nil {
			t.Fatal(err)
		}
		if stored != decision {
			t.Fatalf("stored decision = %q, want %q", stored, decision)
		}
		if v.Actionable {
			t.Fatal("recording a closing decision must not make history actionable again")
		}
	}
}
