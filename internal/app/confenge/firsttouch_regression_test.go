package confenge

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/app/confenge/dispatch"
	"github.com/warmbly/warmbly/internal/config"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
	"github.com/warmbly/warmbly/internal/scheduler"
)

// LANE 0 tripwire for the CONFENGE first-touch engine. Production revenue
// depends on this path, so any change that shrinks admission, lowers queue
// throughput, moves a volume cap, softens cadence, or lets the gate fail open
// has to break a test here before it reaches a deploy.

// firstTouchAdmissionSnapshot is the admitted first-touch shape of the 5-class
// canary as it behaves today. Route class is the identity; the two booleans are
// the admission verdicts. Only tighten this table with a deliberate decision.
var firstTouchAdmissionSnapshot = map[string]struct {
	email            string
	routeAllowed     bool
	controlledCohort bool
}{
	RouteClassDirectPerson:          {"ana.souza@empresaexemplo.com.br", true, true},
	RouteClassRoleOrDepartment:      {"comercial@empresaexemplo.com.br", true, false},
	RouteClassGenericCompany:        {"contato@empresaexemplo.com.br", true, false},
	RouteClassPublicCompanyFreemail: {"empresa@gmail.com", true, false},
	RouteClassProbabilisticOrRisky:  {"joao.silva@empresaexemplo.com.br", false, false},
}

// Admission must not shrink: the same five route classes are still produced by
// the canary feed, the same four are still routable, and the same one is still
// the preferred initial. Deliberately reads no feed policy/provenance field, so
// a fixture version bump cannot flip the meaning of this assertion.
func TestFirstTouchRegressionAdmissionDoesNotShrink(t *testing.T) {
	t.Setenv(EnvKillSwitchPath, filepath.Join(t.TempDir(), "absent"))
	ctx := context.Background()
	raw, err := os.ReadFile(filepath.Join("testdata", "controlled_email_five_class_canary.json"))
	if err != nil {
		t.Fatal(err)
	}
	repo := newMemRepo()
	orgID, userID := uuid.New(), uuid.New()
	svc := NewService(Config{
		Enabled: true, RequireHumanApproval: true, DefaultDailyLimit: 50,
		RepositorySHA: "sha-lane0", FeedSchemaVersion: models.OutreachSchemaV1,
		EvidenceVersion: DefaultEvidenceVersion, MaxFeedPayloadBytes: DefaultMaxPayloadBytes,
	}, repo, nil).(*service)
	run, xerr := svc.ImportFromBytes(ctx, orgID, &userID, raw, ImportOptions{IdempotencyKey: "lane0-admission"})
	if xerr != nil {
		t.Fatal(xerr)
	}
	if run == nil || run.Status != models.OutreachImportCompleted {
		t.Fatalf("canary import did not complete: %+v", run)
	}
	account, err := repo.GetAccountByCNPJ(ctx, orgID, "12345678000190")
	if err != nil || account == nil {
		t.Fatalf("canary account missing: %v", err)
	}
	candidates, err := repo.ListCandidates(ctx, orgID, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != len(firstTouchAdmissionSnapshot) {
		t.Fatalf("canary produced %d candidates, snapshot expects %d", len(candidates), len(firstTouchAdmissionSnapshot))
	}
	seen := map[string]bool{}
	preferred := 0
	for i := range candidates {
		cand := &candidates[i]
		class := CandidateRouteClass(cand)
		want, known := firstTouchAdmissionSnapshot[class]
		if !known {
			t.Fatalf("unsnapshotted route class %q for %q", class, cand.Email)
		}
		if seen[class] {
			t.Fatalf("route class %q appeared twice", class)
		}
		seen[class] = true
		if cand.Email != want.email {
			t.Fatalf("class %s recipient identity moved: got %q want %q", class, cand.Email, want.email)
		}
		if got := ControlledRouteAllowed(cand, nil); got != want.routeAllowed {
			t.Fatalf("class %s ControlledRouteAllowed=%v want %v", class, got, want.routeAllowed)
		}
		if got := CandidateControlledEligible(cand); got != want.controlledCohort {
			t.Fatalf("class %s CandidateControlledEligible=%v want %v", class, got, want.controlledCohort)
		}
		if CandidatePreferredInitial(cand) {
			preferred++
		}
	}
	if len(seen) != len(firstTouchAdmissionSnapshot) {
		t.Fatalf("admitted route classes shrank to %d: %v", len(seen), seen)
	}
	if preferred != 1 {
		t.Fatalf("preferred initial count=%d want exactly 1", preferred)
	}
}

// newLane0Governor builds a governor whose only binding constraint is the
// hourly cap, so a throughput assertion measures the cap and nothing else.
func newLane0Governor(store dispatch.Store, sendsPerHour int, now time.Time) *dispatch.Governor {
	cfg := dispatch.DefaultConfig()
	cfg.SendsPerHour = sendsPerHour
	cfg.MinGap = 0
	cfg.RateMode = "fixed"
	cfg.WindowStart, cfg.WindowEnd, cfg.Timezone = "00:00", "23:59", "UTC"
	cfg.BusinessDaysOnly = false
	return dispatch.NewGovernor(cfg, store, &dispatch.FixedClock{T: now})
}

// A fixed-size queue still drains to exactly the configured hourly cap: no more
// (a throughput regression is also a safety regression) and no fewer.
func TestFirstTouchRegressionQueueDrainsToTheSameReservedCount(t *testing.T) {
	ctx := context.Background()
	const capPerHour = 5
	const queued = 12
	store := dispatch.NewMemoryStore()
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	gov := newLane0Governor(store, capPerHour, now)
	orgID := uuid.New()

	for i := 0; i < queued; i++ {
		draftID := uuid.New()
		if err := store.Enqueue(ctx, &dispatch.QueueItem{
			ID: uuid.New(), OrganizationID: orgID, Channel: dispatch.ChannelEmail,
			DraftID: draftID, MessageKey: dispatch.MessageKeyEmail(draftID),
			RecipientRef: fmt.Sprintf("lane0-%d@example.com", i),
			DueAt:        now.Add(-time.Minute), Status: dispatch.QueueQueued, CreatedAt: now,
		}); err != nil {
			t.Fatalf("enqueue %d: %v", i, err)
		}
	}

	reserved, claimed := 0, 0
	for i := 0; i < queued; i++ {
		item, err := gov.ClaimNextQueued(ctx)
		if err != nil {
			t.Fatalf("claim %d: %v", i, err)
		}
		if item == nil {
			break
		}
		claimed++
		res, err := gov.TryReserve(ctx, dispatch.ReserveRequest{
			OrganizationID: item.OrganizationID, Channel: item.Channel,
			MessageKey: item.MessageKey, DraftID: &item.DraftID,
		})
		if err != nil {
			t.Fatalf("reserve %d: %v", i, err)
		}
		if res.Allowed {
			reserved++
			if err := gov.Commit(ctx, res.Reservation.ID); err != nil {
				t.Fatalf("commit %d: %v", i, err)
			}
		}
	}
	if claimed != queued {
		t.Fatalf("claimed %d of %d queued rows", claimed, queued)
	}
	if reserved != capPerHour {
		t.Fatalf("queue drained to %d reservations, cap is %d", reserved, capPerHour)
	}
}

// One queued row is handed to exactly one claimer. This is the invariant the
// fast-lane/legacy mutual exclusion documented in fast_lane_wiring.go rests on:
// two loops over one store cannot both take the same message key.
func TestFirstTouchRegressionSingleClaimPerQueueRow(t *testing.T) {
	ctx := context.Background()
	store := dispatch.NewMemoryStore()
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	fastLane := newLane0Governor(store, 100, now)
	legacy := newLane0Governor(store, 100, now)

	draftID := uuid.New()
	key := dispatch.MessageKeyEmail(draftID)
	if err := store.Enqueue(ctx, &dispatch.QueueItem{
		ID: uuid.New(), OrganizationID: uuid.New(), Channel: dispatch.ChannelEmail,
		DraftID: draftID, MessageKey: key, RecipientRef: "solo@example.com",
		DueAt: now.Add(-time.Minute), Status: dispatch.QueueQueued, CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	first, err := fastLane.ClaimNextQueued(ctx)
	if err != nil || first == nil {
		t.Fatalf("fast lane claim: item=%+v err=%v", first, err)
	}
	if first.MessageKey != key {
		t.Fatalf("claimed the wrong row: %q", first.MessageKey)
	}
	second, err := legacy.ClaimNextQueued(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if second != nil {
		t.Fatalf("a claimed row was handed to a second transport: %+v", second)
	}
}

// The product-level volume defaults are pinned here, not only in docs. These
// are the mailbox-first cold sending guardrails from CLAUDE.md.
func TestFirstTouchRegressionVolumeDefaultsUnchanged(t *testing.T) {
	cases := []struct {
		name string
		got  int
		want int
	}{
		{"cold campaign cap per mailbox per day", config.CampaignLimitDefault, 50},
		{"minimum gap per mailbox in seconds", config.MinWaitTimeDefault, 600},
		{"warmup start per mailbox per day", config.WarmupBaseDefault, 10},
		{"warmup ceiling per mailbox per day", config.WarmupMaxDefault, 40},
		{"warmup ramp per day", config.WarmupIncreaseDefault, 1},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Fatalf("%s = %d, want %d", tc.name, tc.got, tc.want)
		}
	}
}

// mailboxUpdateRejected reports whether the mailbox update validator refuses a
// payload with the expected error. Same trick as campaignLimitRejected below: a
// value the validator accepts reaches the (unwired) database and panics, which
// is itself proof of acceptance.
func mailboxUpdateRejected(t *testing.T, want *errx.Error, udata *models.UpdateEmail) (rejected bool) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			rejected = false
		}
	}()
	repo := repository.NewEmailRepostory(nil, nil)
	_, xerr := repo.Update(context.Background(), uuid.NewString(), uuid.NewString(), udata)
	return xerr != nil && xerr.Code == want.Code && xerr.Message == want.Message
}

func intPtr(v int) *int { return &v }

// The test above pins what the defaults ARE. This one pins that the enforcement
// actually reads them.
//
// The mailbox-first invariant is that a mailbox's own live cold cap is the
// binding constraint and that nothing else can raise it, so the assertions here
// run the real campaign-scheduler ceiling and the real mailbox update validator
// rather than comparing constants to constants. Concretely they fail if a
// future cap check reads a different field (warmup ceiling, warmup base, min
// gap), hardcodes a literal instead of the mailbox's own value, drops the
// min() against the cap, or lets an unrelated counter (recent warmup volume)
// push a mailbox above what it is allowed to send.
func TestFirstTouchRegressionVolumeDefaultsAreEnforcedNotOnlyDeclared(t *testing.T) {
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	warmedSince := now.Add(-90 * 24 * time.Hour)
	rampStartedLongAgo := now.Add(-365 * 24 * time.Hour)
	mailbox := models.Email{
		CampaignLimit:     config.CampaignLimitDefault,
		MinWaitTime:       config.MinWaitTimeDefault,
		WarmupBase:        config.WarmupBaseDefault,
		WarmupMax:         config.WarmupMaxDefault,
		WarmupIncrease:    config.WarmupIncreaseDefault,
		ColdRampStartedAt: &rampStartedLongAgo,
	}

	// The ceiling is the mailbox's OWN campaign_limit, not a literal and not
	// another field that happens to hold a plausible number today.
	for _, limit := range []int{1, 7, 23, config.CampaignLimitDefault, 100} {
		probe := mailbox
		probe.CampaignLimit = limit
		if got := scheduler.ColdReadinessCeiling(probe, now, 0, 0); got != limit {
			t.Fatalf("a fully ramped mailbox with campaign_limit=%d got a ceiling of %d", limit, got)
		}
	}

	// A brand new cold mailbox starts at the documented 10/day and ramps; it
	// never exceeds its own cap on any day of the ramp.
	for _, days := range []int{0, 1, 3, 7, 30, 365} {
		started := now.Add(-time.Duration(days) * 24 * time.Hour)
		probe := mailbox
		probe.ColdRampStartedAt = &started
		got := scheduler.ColdReadinessCeiling(probe, now, 0, 0)
		if got > config.CampaignLimitDefault {
			t.Fatalf("day %d of the cold ramp allows %d/day, above the %d cap", days, got, config.CampaignLimitDefault)
		}
		if days == 0 && got != 10 {
			t.Fatalf("a fresh cold mailbox starts at %d/day, want 10", got)
		}
	}

	// An unrelated counter must not become permission. A mailbox with a huge
	// recent warmup volume is still bounded by its cold cap.
	warming := mailbox
	warming.Warmup = &warmedSince
	if got := scheduler.ColdReadinessCeiling(warming, now, 0, 100000); got != config.CampaignLimitDefault {
		t.Fatalf("recent warmup volume pushed the cold ceiling to %d, cap is %d", got, config.CampaignLimitDefault)
	}

	// Spam-placement evidence may only lower the ceiling, never raise it.
	withPlacements := scheduler.ColdReadinessCeiling(mailbox, now, 3, 0)
	if withPlacements >= config.CampaignLimitDefault {
		t.Fatalf("observed spam placement left the ceiling at %d", withPlacements)
	}

	// The remaining defaults are enforced by the mailbox update validator: each
	// default must be an accepted value, and each documented bound must still
	// refuse what lies outside it.
	accepted := []struct {
		name  string
		udata *models.UpdateEmail
	}{
		{"min gap default", &models.UpdateEmail{MinWaitTime: intPtr(config.MinWaitTimeDefault)}},
		{"warmup start default", &models.UpdateEmail{WarmupBase: intPtr(config.WarmupBaseDefault)}},
		{"warmup ceiling default", &models.UpdateEmail{WarmupMax: intPtr(config.WarmupMaxDefault)}},
		{"warmup ramp default", &models.UpdateEmail{WarmupIncrease: intPtr(config.WarmupIncreaseDefault)}},
		{"warmup start under its ceiling", &models.UpdateEmail{
			WarmupBase: intPtr(config.WarmupBaseDefault), WarmupMax: intPtr(config.WarmupMaxDefault),
		}},
	}
	for _, tc := range accepted {
		if mailboxUpdateRejected(t, errx.ErrEmailMinWaitTime, tc.udata) ||
			mailboxUpdateRejected(t, errx.ErrEmailWarmupBase, tc.udata) ||
			mailboxUpdateRejected(t, errx.ErrEmailWarmupMax, tc.udata) ||
			mailboxUpdateRejected(t, errx.ErrEmailWarmupIncrease, tc.udata) {
			t.Fatalf("%s is no longer an accepted value", tc.name)
		}
	}
	refused := []struct {
		name  string
		want  *errx.Error
		udata *models.UpdateEmail
	}{
		{"negative min gap", errx.ErrEmailMinWaitTime, &models.UpdateEmail{MinWaitTime: intPtr(-1)}},
		{"min gap beyond a day", errx.ErrEmailMinWaitTime, &models.UpdateEmail{MinWaitTime: intPtr(86401)}},
		{"warmup start above 100", errx.ErrEmailWarmupBase, &models.UpdateEmail{WarmupBase: intPtr(101)}},
		{"warmup ceiling above 100", errx.ErrEmailWarmupMax, &models.UpdateEmail{WarmupMax: intPtr(101)}},
		{"warmup ramp above 100", errx.ErrEmailWarmupIncrease, &models.UpdateEmail{WarmupIncrease: intPtr(101)}},
		{"warmup start above its ceiling", errx.ErrEmailWarmupBase, &models.UpdateEmail{
			WarmupBase: intPtr(config.WarmupMaxDefault + 1), WarmupMax: intPtr(config.WarmupMaxDefault),
		}},
	}
	for _, tc := range refused {
		if !mailboxUpdateRejected(t, tc.want, tc.udata) {
			t.Fatalf("%s is no longer refused", tc.name)
		}
	}
}

// campaignLimitRejected reports whether the mailbox update validator refuses a
// campaign_limit. The ceiling is an inline literal in the repository, so the
// only honest pin is the validator's own behaviour. A value it accepts reaches
// the (unwired) database and panics, which is itself proof of acceptance.
// The payload sets only CampaignLimit, so no earlier field check can
// short-circuit before the ceiling is evaluated.
func campaignLimitRejected(t *testing.T, limit int) (rejected bool) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			rejected = false
		}
	}()
	repo := repository.NewEmailRepostory(nil, nil)
	_, xerr := repo.Update(context.Background(), uuid.NewString(), uuid.NewString(),
		&models.UpdateEmail{CampaignLimit: &limit})
	return xerr != nil && xerr.Code == errx.ErrEmailCampaignLimit.Code &&
		xerr.Message == errx.ErrEmailCampaignLimit.Message
}

// campaign_limit stays capped at 100. Raising the ceiling is the single easiest
// way to turn a safe mailbox into a spam complaint, so it fails here first.
func TestFirstTouchRegressionCampaignLimitCeilingUnchanged(t *testing.T) {
	for _, limit := range []int{101, 150, 1000, -1} {
		if !campaignLimitRejected(t, limit) {
			t.Fatalf("campaign_limit %d is no longer rejected", limit)
		}
	}
	for _, limit := range []int{0, 50, 100} {
		if campaignLimitRejected(t, limit) {
			t.Fatalf("campaign_limit %d is now rejected; the ceiling moved down", limit)
		}
	}
}

// The dispatch pacing defaults are a separate axis from the per-mailbox product
// caps above; a change to either has to be seen and decided, not absorbed.
func TestFirstTouchRegressionDispatchPacingDefaultsUnchanged(t *testing.T) {
	cfg := dispatch.DefaultConfig()
	if !cfg.BusinessDaysOnly {
		t.Fatal("dispatch default must stay business-days-only")
	}
	if cfg.RateMode != "adaptive" {
		t.Fatalf("dispatch default rate mode = %q want adaptive", cfg.RateMode)
	}
	if cfg.RateMaxPerHour != 20 {
		t.Fatalf("dispatch adaptive ceiling = %d want 20", cfg.RateMaxPerHour)
	}
	if cfg.SendsPerHour != cfg.RateStartPerHour {
		t.Fatalf("adaptive ramp must start at the default rate: %d vs %d", cfg.SendsPerHour, cfg.RateStartPerHour)
	}
	for rate, want := range map[int]time.Duration{
		20: 180 * time.Second,
		15: 240 * time.Second,
		10: 360 * time.Second,
	} {
		if got := dispatch.MinGapForRate(rate); got != want {
			t.Fatalf("MinGapForRate(%d) = %s want %s", rate, got, want)
		}
	}
}

// One accepted initial per account. A second first touch for the same account
// is cancelled before the provider is ever reached.
func TestFirstTouchRegressionCadenceOneInitialPerAccount(t *testing.T) {
	h := newFastLaneHarness(t, FirstTouchAccepted, nil)
	draftID, key := h.enqueue(t, "cadencia@exemplo.com.br")
	accountID := h.repo.touchpoints[draftID].AccountID
	h.repo.accepted[accountID] = true

	progressed, err := h.svc.ProcessFastLaneOnce(context.Background())
	if err != nil || !progressed {
		t.Fatalf("the row was not processed: progressed=%v err=%v", progressed, err)
	}
	if h.transport.attempts != 0 {
		t.Fatalf("a second first touch reached the provider %d times", h.transport.attempts)
	}
	if got := h.queueStatus(t, key); got != dispatch.QueueCancelled {
		t.Fatalf("queue row status=%q want %q", got, dispatch.QueueCancelled)
	}
	if _, sent, _ := h.store.GetSendByKey(context.Background(), key); sent {
		t.Fatal("a refused second first touch was recorded as a send")
	}
}

// A suppressed recipient is blocked in the fast lane before any handoff.
func TestFirstTouchRegressionFastLaneBlocksSuppressedRecipient(t *testing.T) {
	h := newFastLaneHarness(t, FirstTouchAccepted, nil)
	_, key := h.enqueue(t, "suprimido@exemplo.com.br")
	h.repo.suppressed["suprimido@exemplo.com.br"] = &models.SuppressedRecipient{
		OrganizationID: h.orgID, Email: "suprimido@exemplo.com.br",
		Source: models.DeliverabilityEventUnsubscribe, Reason: "one-click unsubscribe",
	}

	progressed, err := h.svc.ProcessFastLaneOnce(context.Background())
	if err != nil || !progressed {
		t.Fatalf("the row was not processed: progressed=%v err=%v", progressed, err)
	}
	if h.transport.attempts != 0 {
		t.Fatalf("a suppressed recipient reached the provider %d times", h.transport.attempts)
	}
	if got := h.queueStatus(t, key); got != dispatch.QueueCancelled {
		t.Fatalf("queue row status=%q want %q", got, dispatch.QueueCancelled)
	}
}

// A blocked or DNC account is blocked in the fast lane too.
func TestFirstTouchRegressionFastLaneBlocksAccountDNC(t *testing.T) {
	h := newFastLaneHarness(t, FirstTouchAccepted, nil)
	draftID, key := h.enqueue(t, "bloqueado@exemplo.com.br")
	account := h.repo.accounts[h.repo.touchpoints[draftID].AccountID]
	account.DoNotContact = true

	progressed, err := h.svc.ProcessFastLaneOnce(context.Background())
	if err != nil || !progressed {
		t.Fatalf("the row was not processed: progressed=%v err=%v", progressed, err)
	}
	if h.transport.attempts != 0 {
		t.Fatalf("a DNC account reached the provider %d times", h.transport.attempts)
	}
	if got := h.queueStatus(t, key); got != dispatch.QueueCancelled {
		t.Fatalf("queue row status=%q want %q", got, dispatch.QueueCancelled)
	}
}

// The legacy gate blocks a DNC candidate before consuming a dispatch slot.
func TestFirstTouchRegressionGateHardBlocksDNCCandidate(t *testing.T) {
	allowConfengeSendingForTest(t)
	ctx := context.Background()
	repo := newMemRepoWithSettings()
	orgID, accountID, candidateID := uuid.New(), uuid.New(), uuid.New()
	_, _ = repo.UpsertAccount(ctx, &models.OutreachAccount{
		ID: accountID, OrganizationID: orgID, CNPJ14: "12345678000133",
	})
	_, _ = repo.UpsertCandidate(ctx, &models.OutreachContactCandidate{
		ID: candidateID, OrganizationID: orgID, AccountID: accountID,
		Email: "lane0-dnc@example.com", DoNotContact: true,
	})
	store := dispatch.NewMemoryStore()
	svc := &service{
		cfg: Config{Enabled: true}, repo: repo,
		governor: newLane0Governor(store, 100, time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)),
	}
	result := svc.GateCampaignEmail(ctx, orgID, DefaultCampaignName, "lane0-dnc@example.com",
		uuid.New(), uuid.New(), uuid.New())
	if result.Kind != GateHardBlock || result.Reason != ReasonDNCOrBounce {
		t.Fatalf("DNC candidate must hard block: %+v", result)
	}
	if !result.PermanentSuppress() {
		t.Fatal("a hard block must permanently suppress")
	}
}

// Gate honesty: a CONFENGE campaign without a healthy governor is transient,
// never a bypass; a non-CONFENGE campaign keeps the legacy bypass. Both entry
// points behave identically.
func TestFirstTouchRegressionGateNeverFailsOpen(t *testing.T) {
	allowConfengeSendingForTest(t)
	ctx := context.Background()
	orgID := uuid.New()
	binding := CampaignTransportBinding{EmailAccountID: uuid.New(), TaskID: uuid.New()}

	for _, cfg := range []Config{{Enabled: true}, {Enabled: false}} {
		svc := &service{cfg: cfg, governor: nil}
		for _, name := range []string{DefaultCampaignName, "CONFENGE cold", "CONFENGE"} {
			plain := svc.GateCampaignEmail(ctx, orgID, name, "lead@example.com",
				uuid.New(), uuid.New(), uuid.New())
			if plain.Kind != GateTransient {
				t.Fatalf("enabled=%v %q GateCampaignEmail kind=%d want GateTransient", cfg.Enabled, name, plain.Kind)
			}
			if plain.PermanentSuppress() {
				t.Fatalf("enabled=%v %q transient must never permanently suppress", cfg.Enabled, name)
			}
			bound := svc.GateCampaignEmailForTransport(ctx, orgID, name, "lead@example.com",
				uuid.New(), uuid.New(), uuid.New(), binding)
			if bound.Kind != GateTransient {
				t.Fatalf("enabled=%v %q GateCampaignEmailForTransport kind=%d want GateTransient", cfg.Enabled, name, bound.Kind)
			}
		}
		for _, name := range []string{"Regular campaign", "confenge lowercase", ""} {
			plain := svc.GateCampaignEmail(ctx, orgID, name, "a@b.com", uuid.Nil, uuid.Nil, uuid.New())
			if plain.Kind != GateBypass || plain.Reason != ReasonNotConfenge {
				t.Fatalf("enabled=%v %q must keep the legacy bypass: %+v", cfg.Enabled, name, plain)
			}
			bound := svc.GateCampaignEmailForTransport(ctx, orgID, name, "a@b.com",
				uuid.Nil, uuid.Nil, uuid.New(), binding)
			if bound.Kind != GateBypass {
				t.Fatalf("enabled=%v %q bound bypass kind=%d", cfg.Enabled, name, bound.Kind)
			}
		}
	}
}

// Only GateHardBlock may permanently suppress, across the whole closed set.
func TestFirstTouchRegressionOnlyHardBlockSuppresses(t *testing.T) {
	for kind, want := range map[GateKind]bool{
		GateProceed:         false,
		GateDeferred:        false,
		GateAlready:         false,
		GateHardBlock:       true,
		GateCommercialBlock: false,
		GateTransient:       false,
		GateBypass:          false,
	} {
		if got := (CampaignGateResult{Kind: kind}).PermanentSuppress(); got != want {
			t.Fatalf("kind=%d PermanentSuppress=%v want %v", kind, got, want)
		}
	}
}

// Lane-0 volume/admission tests above stay frozen. The cases below pin first-touch
// transport fail-closed behaviour that #204/#43 depend on.

func TestFirstTouchRegressionUnknownProviderEventNeverNoResponse(t *testing.T) {
	h := newFastLaneHarness(t, FirstTouchAmbiguous, fmt.Errorf("DELIVERY_UNKNOWN: stale provider event"))
	_, key := h.enqueue(t, "lane0-unknown@exemplo.com.br")
	if _, err := h.svc.ProcessFastLaneOnce(context.Background()); err != nil {
		t.Fatalf("process: %v", err)
	}
	item := h.queueItem(t, key)
	if item.Status != dispatch.QueueAttempted {
		t.Fatalf("unknown provider event status=%q", item.Status)
	}
	assertNeverNoResponse(t, item.LastError, item.CancelReason)
	if h.transport.attempts != 1 {
		t.Fatalf("unknown event send count=%d", h.transport.attempts)
	}
	if progressed, err := h.svc.ProcessFastLaneOnce(context.Background()); err != nil || progressed {
		t.Fatalf("unknown event became work again: progressed=%v err=%v", progressed, err)
	}
	if h.transport.attempts != 1 {
		t.Fatalf("unknown event was resent: %d", h.transport.attempts)
	}
}

func TestFirstTouchRegressionPolicyMismatchBlocksReadiness(t *testing.T) {
	now := time.Date(2026, 9, 2, 15, 0, 0, 0, time.UTC)
	snap := readyFirstWindowSnapshot(now)
	snap.PolicyID = "CFG-FIRST-TOUCH-ROUTING-not-a-shipped-policy"
	snap.PolicyVersion = snap.PolicyID
	rep := EvaluateFirstWindowReadiness(snap)
	if !strings.HasPrefix(rep.Verdict, FirstWindowBlockedPrefix) {
		t.Fatalf("unknown policy armed the window: %s", rep.Verdict)
	}
	if rep.Verdict == FirstWindowGOForControlledPilot || strings.Contains(rep.Verdict, "GO_FOR_CONTROLLED_EMAIL_PILOT") {
		t.Fatal("policy mismatch emitted GO")
	}
	h := newFastLaneHarness(t, FirstTouchAccepted, nil)
	draftID, key := h.enqueue(t, "politica@exemplo.com.br")
	h.repo.touchpoints[draftID].Subject = "Assunto que não foi o aprovado"
	if _, err := h.svc.ProcessFastLaneOnce(context.Background()); err != nil {
		t.Fatalf("process: %v", err)
	}
	if h.transport.attempts != 0 || h.queueStatus(t, key) != dispatch.QueueCancelled {
		t.Fatalf("payload/policy hash mismatch reached provider: attempts=%d status=%s", h.transport.attempts, h.queueStatus(t, key))
	}
}
