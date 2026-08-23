package confenge

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/models"
)

func TestTransportFailedSendReleasesSlotAndRetryDoesNotDuplicate(t *testing.T) {
	t.Setenv(EnvKillSwitchPath, t.TempDir()+"/absent")
	now := time.Now().UTC()
	store := NewMemoryCohortStore()
	auth := sampleGOReviewGrant(t, now)
	auth.MaxDailyVolume = 1
	if err := store.PutGrant(context.Background(), auth); err != nil {
		t.Fatal(err)
	}
	tp := dispatchTestTouch(t, auth, now)
	in := dispatchTransportInput(auth, auth.FrozenManifest.Members[0], tp, now)
	_, err := TransportOneCohortMessage(context.Background(), store, auth, tp, in, func(*models.OutreachTouchpoint) (string, error) {
		return "", errors.New("smtp_connect_failed")
	})
	if err == nil {
		t.Fatal("failed provider send must error")
	}
	key := cohortMessageKey(auth.ID, tp.ID)
	st, _ := store.HeldSlot(context.Background(), auth.ID, key)
	if st == CohortSlotSent || st == CohortSlotReserved {
		t.Fatalf("failed send must release slot, state=%s", st)
	}
	okTP := dispatchTestTouch(t, auth, now)
	okTP.ID = tp.ID
	got, err := TransportOneCohortMessage(context.Background(), store, auth, okTP, in, func(*models.OutreachTouchpoint) (string, error) {
		return "prov-1", nil
	})
	if err != nil || got != "prov-1" {
		t.Fatalf("retry after release: id=%s err=%v", got, err)
	}
	_, err = TransportOneCohortMessage(context.Background(), store, auth, okTP, in, func(*models.OutreachTouchpoint) (string, error) {
		t.Fatal("retry must not send a second time")
		return "prov-2", nil
	})
	if !errors.Is(err, errCohortAlreadySent) {
		t.Fatalf("second send must be duplicate, got %v", err)
	}
}

func TestDispatchRequiresLiveGOAndBlocksRiskyAndOverCap(t *testing.T) {
	t.Setenv(EnvKillSwitchPath, t.TempDir()+"/absent")
	now := time.Now().UTC()
	store := NewMemoryCohortStore()
	auth := sampleGOReviewGrant(t, now)
	auth.MaxDailyVolume = 10
	m0 := auth.FrozenManifest.Members[0]
	m0.Subject = "s"
	m0.BodyText = "Olá, equipe,\n\nSou da CONFENGE."
	if m0.TouchpointID == uuid.Nil {
		m0.TouchpointID = uuid.New()
	}
	auth.FrozenManifest.Members[0] = m0
	if err := store.PutGrant(context.Background(), auth); err != nil {
		t.Fatal(err)
	}
	live := matchingControlledEmailLive(auth)
	cmp := CompareControlledEmailRelease(expectedReleaseFromGrant(auth), live)
	if cmp.Verdict.Verdict != ReleaseGOForControlledEmailPilot {
		t.Fatalf("setup GO: %s %v", cmp.Verdict.Verdict, cmp.Verdict.Reasons)
	}

	empty := CompareControlledEmailRelease(expectedReleaseFromGrant(auth), ReleaseManifest{})
	if _, err := DispatchBoundedCohort(context.Background(), store, nil, &empty, auth, now, 50, func(FrozenCohortMember, *models.OutreachTouchpoint) (string, error) {
		t.Fatal("must not send without GO")
		return "", nil
	}); err == nil {
		t.Fatal("no grant live GO must block dispatch")
	}

	sends := 0
	res, err := DispatchBoundedCohort(context.Background(), store, nil, &cmp, auth, now, 50, func(m FrozenCohortMember, tp *models.OutreachTouchpoint) (string, error) {
		sends++
		return "prov-" + m.Mailbox, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Attempted != 1 || res.ProviderAccepted != 1 || !res.RealEmailSent {
		t.Fatalf("%+v", res)
	}
	if res.AutoSendEnabled || res.GreenAutorunEnabled {
		t.Fatal("auto-send must stay false")
	}

	risky := *auth
	risky.ID = uuid.New()
	risky.FrozenManifest = &FrozenCohortSnapshot{
		SnapshotHash: auth.FrozenManifest.SnapshotHash,
		Members: []FrozenCohortMember{{
			AccountID: uuid.New(), AccountRef: "risky", Mailbox: "x@y.com",
			RouteClass: RouteClassProbabilisticOrRisky, TouchpointID: uuid.New(),
			Subject: "s", BodyText: "Olá, equipe,\n\nSou da CONFENGE.",
		}},
	}
	riskyCmp := CompareControlledEmailRelease(expectedReleaseFromGrant(&risky), matchingControlledEmailLive(&risky))
	out, err := DispatchBoundedCohort(context.Background(), store, nil, &riskyCmp, &risky, now, 50, func(FrozenCohortMember, *models.OutreachTouchpoint) (string, error) {
		t.Fatal("RISKY must not send")
		return "no", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Attempted != 0 || out.Blocked < 1 {
		t.Fatalf("RISKY: %+v", out)
	}
}

func TestDispatchCapBlocksOverTen(t *testing.T) {
	t.Setenv(EnvKillSwitchPath, t.TempDir()+"/absent")
	now := time.Now().UTC()
	store := NewMemoryCohortStore()
	auth := sampleGOReviewGrant(t, now)
	auth.MaxDailyVolume = 10
	members := make([]FrozenCohortMember, 0, 11)
	for i := 0; i < 11; i++ {
		members = append(members, FrozenCohortMember{
			AccountID: uuid.New(), AccountRef: "acc-" + itoa(i), CandidateID: uuid.New(),
			Mailbox: "contato" + itoa(i) + "@empresa.com.br", RouteClass: RouteClassGenericCompany,
			TouchpointID: uuid.New(), Subject: "CONFENGE", BodyText: "Olá, equipe,\n\nSou da CONFENGE.",
		})
	}
	auth.FrozenManifest.Members = members
	auth.CohortHash = HashFrozenMembership(members)
	if err := store.PutGrant(context.Background(), auth); err != nil {
		t.Fatal(err)
	}
	live := matchingControlledEmailLive(auth)
	cmp := CompareControlledEmailRelease(expectedReleaseFromGrant(auth), live)
	sends := 0
	res, err := DispatchBoundedCohort(context.Background(), store, nil, &cmp, auth, now, 999, func(FrozenCohortMember, *models.OutreachTouchpoint) (string, error) {
		sends++
		return "prov", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Attempted > DefaultCohortDispatchCap || sends > DefaultCohortDispatchCap {
		t.Fatalf("cap breached attempted=%d sends=%d", res.Attempted, sends)
	}
}

func TestFixtureCannotProduceLiveGO(t *testing.T) {
	t.Setenv("SMTP_HOST", "")
	t.Setenv("CONFENGE_SMTP_HOST", "")
	t.Setenv("IMAP_HOST", "")
	t.Setenv("CONFENGE_IMAP_HOST", "")
	t.Setenv("CONFENGE_MAILBOX_EMAIL", "")
	t.Setenv(EnvRepositorySHA, "")
	t.Setenv(EnvKillSwitchPath, t.TempDir()+"/absent-kill")
	raw, err := os.ReadFile("testdata/controlled_email_five_class_canary.json")
	if err != nil {
		t.Fatal(err)
	}
	feed, err := ParseFeed(raw)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := PrepareControlledCohortFromFeed(feed, CohortPrepareOptions{
		Now: time.Now().UTC(), Limit: 50, MaxDailyVolume: 10, TTL: 24 * time.Hour,
		RepositorySHA: "fixture-sha",
	})
	if err != nil {
		t.Skip("fixture is not a live cohort: " + err.Error())
	}
	auth, err := GrantFromFrozenSnapshot(snap, uuid.New(), uuid.New(), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	live := CollectLiveReleaseManifest(context.Background(), LiveReleaseInput{
		Now: time.Now().UTC(), Config: &Config{}, Store: NewMemoryCohortStore(), Auth: auth,
	})
	v := EvaluateControlledEmailRelease(expectedReleaseFromGrant(auth), live)
	if v.Verdict == ReleaseGOForControlledEmailPilot {
		t.Fatal("fixture collector evidence must not produce live GO")
	}
}

func TestIMAPUnknownAndDialPass(t *testing.T) {
	t.Setenv("CONFENGE_IMAP_HOST", "")
	t.Setenv("IMAP_HOST", "")
	st, _ := ProbeIMAPReadiness(time.Second)
	if st.IsPass() || st.IsFail() {
		t.Fatalf("unset IMAP must be UNKNOWN, got %s", st.Label())
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	t.Setenv("CONFENGE_IMAP_HOST", "127.0.0.1")
	t.Setenv("CONFENGE_IMAP_PORT", port)
	st, reason := ProbeIMAPReadiness(time.Second)
	if st != EvidencePass {
		t.Fatalf("dial proof: %s %s", st.Label(), reason)
	}
}

func TestNoReplyStaysUnknownInDispatchReport(t *testing.T) {
	events := []struct{ Type string }{{Type: "no_reply"}}
	_ = events
	auth := sampleGOReviewGrant(t, time.Now().UTC())
	res := &CohortDispatchResult{AuthorizationID: auth.ID, CohortID: auth.CohortID, Attempted: 1}
	text := FormatCohortDispatch(res)
	if strings.Contains(strings.ToLower(text), "delivered=true") {
		t.Fatal("must not infer delivered")
	}
	if !strings.Contains(text, "N_PROVIDER_ACCEPTED=0") {
		t.Fatalf("unobserved provider accepted must stay 0\n%s", text)
	}
}

func dispatchTestTouch(t *testing.T, auth *BoundedCohortAuthorization, now time.Time) *models.OutreachTouchpoint {
	t.Helper()
	m := auth.FrozenManifest.Members[0]
	tp := &models.OutreachTouchpoint{
		ID: m.TouchpointID, OrganizationID: auth.OrganizationID, AccountID: m.AccountID,
		State: models.TouchpointDrafted, Recipient: m.Mailbox,
		Subject: "s", BodyText: "Olá, equipe,\n\nSou da CONFENGE.",
		Channel: "EMAIL", Purpose: models.TouchpointPurposeInitial,
	}
	if tp.ID == uuid.Nil {
		tp.ID = uuid.New()
		m.TouchpointID = tp.ID
		auth.FrozenManifest.Members[0] = m
	}
	if err := ApplyBoundedCohortAuthorization(tp, auth, now); err != nil {
		t.Fatal(err)
	}
	return tp
}
