package confenge

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

// memRepo is an in-memory OutreachRepository for unit tests of ImportFromBytes.
type memRepo struct {
	mu       sync.Mutex
	runs     map[uuid.UUID]*models.OutreachImportRun
	byIdem   map[string]*models.OutreachImportRun
	accounts map[string]*models.OutreachAccount // org|cnpj
	byID     map[uuid.UUID]*models.OutreachAccount
	cands    map[uuid.UUID][]models.OutreachContactCandidate
	evidence map[uuid.UUID][]models.OutreachEvidence
	drafts   map[uuid.UUID]*models.OutreachDraft
	outcomes []models.OutreachOutcome
}

func newMemRepo() *memRepo {
	return &memRepo{
		runs:     map[uuid.UUID]*models.OutreachImportRun{},
		byIdem:   map[string]*models.OutreachImportRun{},
		accounts: map[string]*models.OutreachAccount{},
		byID:     map[uuid.UUID]*models.OutreachAccount{},
		cands:    map[uuid.UUID][]models.OutreachContactCandidate{},
		evidence: map[uuid.UUID][]models.OutreachEvidence{},
		drafts:   map[uuid.UUID]*models.OutreachDraft{},
	}
}

func accKey(org uuid.UUID, cnpj string) string { return org.String() + "|" + cnpj }

func (m *memRepo) CreateImportRun(ctx context.Context, run *models.OutreachImportRun) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if run.ID == uuid.Nil {
		run.ID = uuid.New()
	}
	cp := *run
	m.runs[run.ID] = &cp
	if run.IdempotencyKey != "" {
		m.byIdem[run.OrganizationID.String()+"|"+run.IdempotencyKey] = &cp
	}
	return nil
}

func (m *memRepo) UpdateImportRun(ctx context.Context, run *models.OutreachImportRun) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *run
	m.runs[run.ID] = &cp
	if run.IdempotencyKey != "" {
		m.byIdem[run.OrganizationID.String()+"|"+run.IdempotencyKey] = &cp
	}
	return nil
}

func (m *memRepo) GetImportRun(ctx context.Context, orgID, id uuid.UUID) (*models.OutreachImportRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.runs[id]
	if r == nil || r.OrganizationID != orgID {
		return nil, nil
	}
	cp := *r
	return &cp, nil
}

func (m *memRepo) GetImportRunByIdempotency(ctx context.Context, orgID uuid.UUID, key string) (*models.OutreachImportRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.byIdem[orgID.String()+"|"+key]
	if r == nil {
		return nil, nil
	}
	cp := *r
	return &cp, nil
}

func (m *memRepo) ListImportRuns(ctx context.Context, orgID uuid.UUID, limit int) ([]models.OutreachImportRun, error) {
	return nil, nil
}

func (m *memRepo) GetAccountByCNPJ(ctx context.Context, orgID uuid.UUID, cnpj14 string) (*models.OutreachAccount, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a := m.accounts[accKey(orgID, cnpj14)]
	if a == nil {
		return nil, nil
	}
	cp := *a
	return &cp, nil
}

func (m *memRepo) GetAccount(ctx context.Context, orgID, id uuid.UUID) (*models.OutreachAccount, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a := m.byID[id]
	if a == nil || a.OrganizationID != orgID {
		return nil, nil
	}
	cp := *a
	return &cp, nil
}

func (m *memRepo) UpsertAccount(ctx context.Context, acc *models.OutreachAccount) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := accKey(acc.OrganizationID, acc.CNPJ14)
	existing := m.accounts[k]
	created := existing == nil
	if existing != nil {
		acc.ID = existing.ID
		// preserve DNC
		if existing.DoNotContact {
			acc.DoNotContact = true
		}
	} else if acc.ID == uuid.Nil {
		acc.ID = uuid.New()
	}
	cp := *acc
	m.accounts[k] = &cp
	m.byID[cp.ID] = &cp
	return created, nil
}

func (m *memRepo) ListAccounts(ctx context.Context, orgID uuid.UUID, filter repository.OutreachAccountFilter) ([]models.OutreachAccount, error) {
	return nil, nil
}

func (m *memRepo) CountByQueueState(ctx context.Context, orgID uuid.UUID) (*models.OutreachQueueSummary, error) {
	return &models.OutreachQueueSummary{}, nil
}

func (m *memRepo) SetAccountHumanFlags(ctx context.Context, orgID, id uuid.UUID, blocked, dnc bool, reason, queueState string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a := m.byID[id]
	if a == nil || a.OrganizationID != orgID {
		return context.Canceled
	}
	a.Blocked = blocked
	a.DoNotContact = dnc
	a.BlockReason = reason
	a.QueueState = queueState
	a.HumanOverride = true
	return nil
}

func (m *memRepo) ListCandidates(ctx context.Context, orgID, accountID uuid.UUID) ([]models.OutreachContactCandidate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]models.OutreachContactCandidate{}, m.cands[accountID]...), nil
}

func (m *memRepo) UpsertCandidate(ctx context.Context, c *models.OutreachContactCandidate) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c.ID == uuid.Nil {
		c.ID = uuid.New()
	}
	list := m.cands[c.AccountID]
	for i, existing := range list {
		if existing.ID == c.ID || (existing.SourceContactID != "" && existing.SourceContactID == c.SourceContactID) {
			if existing.DoNotContact {
				c.DoNotContact = true
			}
			c.ID = existing.ID
			list[i] = *c
			m.cands[c.AccountID] = list
			return false, nil
		}
	}
	m.cands[c.AccountID] = append(list, *c)
	return true, nil
}

func (m *memRepo) GetCandidate(ctx context.Context, orgID, id uuid.UUID) (*models.OutreachContactCandidate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, list := range m.cands {
		for i := range list {
			if list[i].ID == id && list[i].OrganizationID == orgID {
				cp := list[i]
				return &cp, nil
			}
		}
	}
	return nil, nil
}

func (m *memRepo) ListEvidence(ctx context.Context, orgID, accountID uuid.UUID) ([]models.OutreachEvidence, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]models.OutreachEvidence{}, m.evidence[accountID]...), nil
}

func (m *memRepo) UpsertEvidence(ctx context.Context, e *models.OutreachEvidence) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e.ID == uuid.Nil {
		e.ID = uuid.New()
	}
	list := m.evidence[e.AccountID]
	for i, existing := range list {
		if existing.SourceEvidenceID == e.SourceEvidenceID {
			e.ID = existing.ID
			list[i] = *e
			m.evidence[e.AccountID] = list
			return false, nil
		}
	}
	m.evidence[e.AccountID] = append(list, *e)
	return true, nil
}

func (m *memRepo) UpsertDraft(ctx context.Context, d *models.OutreachDraft) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if d.ID == uuid.Nil {
		d.ID = uuid.New()
	}
	cp := *d
	m.drafts[d.ID] = &cp
	return nil
}
func (m *memRepo) GetDraft(ctx context.Context, orgID, id uuid.UUID) (*models.OutreachDraft, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.drafts[id]
	if d == nil || d.OrganizationID != orgID {
		return nil, nil
	}
	cp := *d
	return &cp, nil
}
func (m *memRepo) GetActiveDraftForAccount(ctx context.Context, orgID, accountID uuid.UUID) (*models.OutreachDraft, error) {
	return nil, nil
}
func (m *memRepo) ListDrafts(ctx context.Context, orgID uuid.UUID, status string, limit, offset int) ([]models.OutreachDraft, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []models.OutreachDraft
	for _, d := range m.drafts {
		if d.OrganizationID == orgID && (status == "" || d.Status == status) {
			out = append(out, *d)
		}
	}
	return out, nil
}
func (m *memRepo) UpdateDraftStatus(ctx context.Context, d *models.OutreachDraft) error {
	return m.UpsertDraft(ctx, d)
}
func (m *memRepo) GetOrgSettings(ctx context.Context, orgID uuid.UUID) (*models.OutreachOrgSettings, error) {
	return nil, nil
}
func (m *memRepo) UpsertOrgSettings(ctx context.Context, s *models.OutreachOrgSettings) error {
	return nil
}
func (m *memRepo) EnqueueOutcome(ctx context.Context, ev *models.OutreachOutcome) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.outcomes = append(m.outcomes, *ev)
	return nil
}
func (m *memRepo) ListPendingOutcomes(ctx context.Context, limit int) ([]models.OutreachOutcome, error) {
	return nil, nil
}
func (m *memRepo) MarkOutcomeDelivered(ctx context.Context, orgID, id uuid.UUID) error {
	return nil
}
func (m *memRepo) MarkOutcomeAttempt(ctx context.Context, orgID, id uuid.UUID, attempts int, next time.Time, lastErr string, dead bool) error {
	return nil
}
func (m *memRepo) GetOutcomeByIdempotency(ctx context.Context, orgID uuid.UUID, key string) (*models.OutreachOutcome, error) {
	return nil, nil
}
func (m *memRepo) FindCandidateByEmail(ctx context.Context, orgID uuid.UUID, email string) (*models.OutreachContactCandidate, *models.OutreachAccount, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, list := range m.cands {
		for i := range list {
			c := list[i]
			if c.OrganizationID == orgID && c.Email == email {
				acc := m.byID[c.AccountID]
				return &c, acc, nil
			}
		}
	}
	return nil, nil, nil
}
func (m *memRepo) FindCandidateByPhone(ctx context.Context, orgID uuid.UUID, phone string) (*models.OutreachContactCandidate, *models.OutreachAccount, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, list := range m.cands {
		for i := range list {
			c := list[i]
			if c.OrganizationID != orgID {
				continue
			}
			if c.PhoneE164 == phone || c.Phone == phone {
				return &c, m.byID[c.AccountID], nil
			}
		}
	}
	return nil, nil, nil
}

func testSvc(repo repository.OutreachRepository) Service {
	cfg := Config{
		Enabled:              true,
		RequireHumanApproval: true,
		DefaultDailyLimit:    10,
		MaxInitialEmailWords: 120,
		MaxFeedPayloadBytes:  DefaultMaxPayloadBytes,
	}
	return NewService(cfg, repo, nil)
}

func TestImportNativeFeedCreatesAccounts(t *testing.T) {
	repo := newMemRepo()
	svc := testSvc(repo)
	org := uuid.New()
	user := uuid.New()
	raw := mustReadFixture(t, "native_feed_v1.json")

	run, xerr := svc.ImportFromBytes(context.Background(), org, &user, raw, ImportOptions{})
	if xerr != nil {
		t.Fatalf("import: %v", xerr)
	}
	if run.Status != models.OutreachImportCompleted && run.Status != models.OutreachImportPartial {
		t.Fatalf("status %s counts=%+v errs=%+v", run.Status, run.Counts, run.Errors)
	}
	if run.Counts.Creates < 4 {
		t.Fatalf("want >=4 creates, got %+v", run.Counts)
	}
	// No-email company staged as NEEDS_CONTACT
	acc, err := repo.GetAccountByCNPJ(context.Background(), org, "55444333000122")
	if err != nil || acc == nil {
		t.Fatal("missing no-contact company")
	}
	if acc.QueueState != models.OutreachQueueNeedsContact {
		t.Fatalf("queue_state=%s want NEEDS_CONTACT", acc.QueueState)
	}
	// Official contact company ready
	acme, _ := repo.GetAccountByCNPJ(context.Background(), org, "11222333000181")
	if acme == nil || acme.QueueState != models.OutreachQueueReadyToGenerate {
		t.Fatalf("acme state=%v", acme)
	}
	// Unverified-only contact is not enrollable => NEEDS_CONTACT
	cn, _ := repo.GetAccountByCNPJ(context.Background(), org, "66777888000199")
	if cn == nil || cn.QueueState != models.OutreachQueueNeedsContact {
		t.Fatalf("unverified-only should be NEEDS_CONTACT, got %v", cn)
	}
}

func TestImportDryRunDoesNotPersistAccounts(t *testing.T) {
	repo := newMemRepo()
	svc := testSvc(repo)
	org := uuid.New()
	raw := mustReadFixture(t, "native_feed_v1.json")
	run, xerr := svc.ImportFromBytes(context.Background(), org, nil, raw, ImportOptions{DryRun: true})
	if xerr != nil {
		t.Fatal(xerr)
	}
	if !run.DryRun {
		t.Fatal("expected dry_run")
	}
	if run.Counts.Creates < 1 {
		t.Fatalf("dry-run should count creates: %+v", run.Counts)
	}
	// accounts map empty of real companies
	acc, _ := repo.GetAccountByCNPJ(context.Background(), org, "11222333000181")
	if acc != nil {
		t.Fatal("dry-run must not persist accounts")
	}
}

func TestImportIdempotencySameKeySamePayload(t *testing.T) {
	repo := newMemRepo()
	svc := testSvc(repo)
	org := uuid.New()
	raw := mustReadFixture(t, "native_feed_v1.json")
	opts := ImportOptions{IdempotencyKey: "idem-1"}
	r1, xerr := svc.ImportFromBytes(context.Background(), org, nil, raw, opts)
	if xerr != nil {
		t.Fatal(xerr)
	}
	r2, xerr := svc.ImportFromBytes(context.Background(), org, nil, raw, opts)
	if xerr != nil {
		t.Fatal(xerr)
	}
	if r1.ID != r2.ID {
		t.Fatalf("idempotent reimport should return same run %s vs %s", r1.ID, r2.ID)
	}
}

func TestImportIdempotencyConflictOnDifferentPayload(t *testing.T) {
	repo := newMemRepo()
	svc := testSvc(repo)
	org := uuid.New()
	raw := mustReadFixture(t, "native_feed_v1.json")
	opts := ImportOptions{IdempotencyKey: "idem-2"}
	if _, xerr := svc.ImportFromBytes(context.Background(), org, nil, raw, opts); xerr != nil {
		t.Fatal(xerr)
	}
	other := []byte(`{"schema_version":"confenge.outreach.v1","source":{"system":"extra-cli"},"leads":[]}`)
	_, xerr := svc.ImportFromBytes(context.Background(), org, nil, other, opts)
	if xerr == nil {
		t.Fatal("expected conflict")
	}
}

func TestReimportPreservesDNC(t *testing.T) {
	repo := newMemRepo()
	svc := testSvc(repo)
	org := uuid.New()
	raw := mustReadFixture(t, "native_feed_v1.json")
	if _, xerr := svc.ImportFromBytes(context.Background(), org, nil, raw, ImportOptions{}); xerr != nil {
		t.Fatal(xerr)
	}
	acc, _ := repo.GetAccountByCNPJ(context.Background(), org, "11222333000181")
	if acc == nil {
		t.Fatal("missing")
	}
	acc.DoNotContact = true
	acc.QueueState = models.OutreachQueueDoNotContact
	_, _ = repo.UpsertAccount(context.Background(), acc)

	if _, xerr := svc.ImportFromBytes(context.Background(), org, nil, raw, ImportOptions{}); xerr != nil {
		t.Fatal(xerr)
	}
	again, _ := repo.GetAccountByCNPJ(context.Background(), org, "11222333000181")
	if again == nil || !again.DoNotContact {
		t.Fatal("DNC must survive reimport")
	}
}

func TestReimportIdenticalIsUnchanged(t *testing.T) {
	repo := newMemRepo()
	svc := testSvc(repo)
	org := uuid.New()
	raw := mustReadFixture(t, "native_feed_v1.json")
	if _, xerr := svc.ImportFromBytes(context.Background(), org, nil, raw, ImportOptions{}); xerr != nil {
		t.Fatal(xerr)
	}
	run2, xerr := svc.ImportFromBytes(context.Background(), org, nil, raw, ImportOptions{})
	if xerr != nil {
		t.Fatal(xerr)
	}
	if run2.Counts.Creates != 0 {
		t.Fatalf("second import should not create: %+v", run2.Counts)
	}
	if run2.Counts.Unchanged < 1 {
		t.Fatalf("expected unchanged counts: %+v", run2.Counts)
	}
}

func TestReimportScoreChangeUpdates(t *testing.T) {
	repo := newMemRepo()
	svc := testSvc(repo)
	org := uuid.New()
	raw := mustReadFixture(t, "native_feed_v1.json")
	if _, xerr := svc.ImportFromBytes(context.Background(), org, nil, raw, ImportOptions{}); xerr != nil {
		t.Fatal(xerr)
	}
	// Mutate score in payload
	feed, _ := ParseFeed(raw)
	feed.Leads[0].Priority.Score = 99
	mut, _ := jsonMarshal(feed)
	run, xerr := svc.ImportFromBytes(context.Background(), org, nil, mut, ImportOptions{})
	if xerr != nil {
		t.Fatal(xerr)
	}
	if run.Counts.Updates < 1 {
		t.Fatalf("score change should update: %+v", run.Counts)
	}
}

func TestMultiTenantIsolation(t *testing.T) {
	repo := newMemRepo()
	svc := testSvc(repo)
	orgA, orgB := uuid.New(), uuid.New()
	raw := mustReadFixture(t, "native_feed_v1.json")
	if _, xerr := svc.ImportFromBytes(context.Background(), orgA, nil, raw, ImportOptions{}); xerr != nil {
		t.Fatal(xerr)
	}
	accB, _ := repo.GetAccountByCNPJ(context.Background(), orgB, "11222333000181")
	if accB != nil {
		t.Fatal("org B must not see org A accounts")
	}
	accA, _ := repo.GetAccountByCNPJ(context.Background(), orgA, "11222333000181")
	if accA == nil {
		t.Fatal("org A should have account")
	}
}

func TestDisabledServiceRejects(t *testing.T) {
	repo := newMemRepo()
	cfg := Config{Enabled: false}
	svc := NewService(cfg, repo, nil)
	_, xerr := svc.ImportFromBytes(context.Background(), uuid.New(), nil, []byte(`{}`), ImportOptions{})
	if xerr == nil {
		t.Fatal("disabled must reject")
	}
}

func TestLegacyImportWorks(t *testing.T) {
	repo := newMemRepo()
	svc := testSvc(repo)
	org := uuid.New()
	raw := mustReadFixture(t, "legacy_leads_array.json")
	run, xerr := svc.ImportFromBytes(context.Background(), org, nil, raw, ImportOptions{})
	if xerr != nil {
		t.Fatal(xerr)
	}
	if run.Counts.Creates < 2 {
		t.Fatalf("legacy creates: %+v errs=%+v", run.Counts, run.Errors)
	}
	noContact, _ := repo.GetAccountByCNPJ(context.Background(), org, "55444333000122")
	if noContact == nil || noContact.QueueState != models.OutreachQueueNeedsContact {
		t.Fatalf("legacy no-contact company: %+v", noContact)
	}
}

func TestInvalidFeedRejected(t *testing.T) {
	repo := newMemRepo()
	svc := testSvc(repo)
	_, xerr := svc.ImportFromBytes(context.Background(), uuid.New(), nil, []byte(`{not json`), ImportOptions{})
	if xerr == nil {
		t.Fatal("expected invalid feed error")
	}
}

func TestEvidenceAddedOnReimport(t *testing.T) {
	repo := newMemRepo()
	svc := testSvc(repo)
	org := uuid.New()
	raw := mustReadFixture(t, "native_feed_v1.json")
	if _, xerr := svc.ImportFromBytes(context.Background(), org, nil, raw, ImportOptions{}); xerr != nil {
		t.Fatal(xerr)
	}
	feed, _ := ParseFeed(raw)
	feed.Leads[0].Evidence = append(feed.Leads[0].Evidence, FeedEvidence{
		ID:             "ev-acme-2",
		Title:          "Nova evidencia",
		EpistemicClass: models.OutreachEpistemicConfirmedFact,
	})
	// Also change a machine field so content hash moves (evidence is in hash)
	mut, _ := jsonMarshal(feed)
	run, xerr := svc.ImportFromBytes(context.Background(), org, nil, mut, ImportOptions{})
	if xerr != nil {
		t.Fatal(xerr)
	}
	if run.Counts.EvidenceAdded < 1 {
		t.Fatalf("expected new evidence: %+v", run.Counts)
	}
}

// local helper avoiding import cycle noise
func jsonMarshal(v any) ([]byte, error) {
	return marshalJSON(v)
}
