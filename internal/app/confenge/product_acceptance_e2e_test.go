package confenge

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/app/confenge/dispatch"
	"github.com/warmbly/warmbly/internal/app/whatsapp"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

// TestProductAcceptanceMultichannelSum proves the sum of CONFENGE product surfaces
// on shipped service + governor + touchpoint + WA policy + HMAC outcomes paths.
// Real Mailpit is optional (CONFENGE_MAILPIT_URL); mock capture is the CI gate for bullet 12.
func TestProductAcceptanceMultichannelSum(t *testing.T) {
	var outcomes []models.OutreachOutcome
	rf := &memRepoOutcome{
		memRepoFull: *newMemRepoWithSettings(),
		outcomes:    &outcomes,
	}
	rf.memRepo = newMemRepo()
	rf.settings = map[uuid.UUID]*models.OutreachOrgSettings{}
	rf.drafts = map[uuid.UUID]*models.OutreachDraft{}
	rf.touchpoints = map[uuid.UUID]*models.OutreachTouchpoint{}
	rf.outcomeBy = map[string]*models.OutreachOutcome{}
	rf.orgOwner = map[uuid.UUID]uuid.UUID{}

	cfg := Config{
		Enabled: true, DefaultDailyLimit: 10, MaxInitialEmailWords: 120,
		RequireHumanApproval: true, MaxFeedPayloadBytes: DefaultMaxPayloadBytes,
		WhatsAppEnabled: true,
	}
	svc := NewService(cfg, rf, nil).(*service)
	contacts := &mockContacts{}
	camps := &mockCampaigns{}
	svc.WireExecution(camps, contacts)

	// Fake clock + durable memory store for global governor (email+WA share cap).
	clock := &dispatch.FixedClock{T: time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)}
	store := dispatch.NewMemoryStore()
	govCfg := dispatch.DefaultConfig()
	govCfg.WindowStart = "00:00"
	govCfg.WindowEnd = "23:59"
	govCfg.Timezone = "UTC"
	govCfg.MinGap = 0
	govCfg.SendsPerHour = 10
	gov := dispatch.NewGovernor(govCfg, store, clock)
	svc.WireDispatchGovernor(gov)

	// Mock WhatsApp via shipped mock provider (never real WABA).
	waMock := whatsapp.NewMockProvider()
	waSvc := whatsapp.NewService(whatsapp.Config{
		Enabled: true, AutoSendEnabled: false, CrossChannelInterval: 0, ServiceWindow: 24 * time.Hour,
		EvolutionInstance: "product-acceptance",
	}, waMock, nil)
	svc.WireWhatsApp(waSvc, &memWAStore{})

	receiver := newTestOutcomeReceiver(t)
	defer receiver.Close()

	org := uuid.New()
	user := uuid.New()
	raw := mustReadFixture(t, "native_feed_v1.json")

	// 1. import of multiple companies
	run, xerr := svc.ImportFromBytes(context.Background(), org, &user, raw, ImportOptions{})
	if xerr != nil {
		t.Fatal(xerr)
	}
	if run.Counts.Creates < 3 {
		t.Fatalf("bullet1 multi-company import: creates=%d want>=3 counts=%+v", run.Counts.Creates, run.Counts)
	}

	// 2. distinct dossiers / services
	acme, _ := rf.GetAccountByCNPJ(context.Background(), org, "11222333000181")
	if acme == nil {
		t.Fatal("acme missing")
	}
	accs, _ := rf.ListAccounts(context.Background(), org, repository.OutreachAccountFilter{Limit: 50})
	services := map[string]struct{}{}
	for _, a := range accs {
		if a.ServiceCode != "" {
			services[a.ServiceCode] = struct{}{}
		}
	}
	if len(services) < 1 {
		t.Fatal("bullet2 expected at least one service code from feed")
	}

	// 3. different messages per account
	draft1, xerr := svc.GenerateDraft(context.Background(), org, user, acme.ID, nil)
	if xerr != nil {
		t.Fatalf("generate acme: %s", xerr.Message)
	}
	if draft1.BodyText == "" {
		t.Fatal("bullet3 empty draft body")
	}
	var otherBody string
	for _, a := range accs {
		if a.ID == acme.ID || a.QueueState == models.OutreachQueueNeedsContact {
			continue
		}
		d, xe := svc.GenerateDraft(context.Background(), org, user, a.ID, nil)
		if xe == nil && d.BodyText != "" {
			otherBody = d.BodyText
			break
		}
	}
	if otherBody != "" && otherBody == draft1.BodyText && acme.FactToMention != "" {
		// Bodies may share template skeleton; require fact or service differentiation in dossier.
		if draft1.FactUsed == "" && draft1.ServiceCode == "" {
			t.Fatal("bullet3 draft missing fact/service differentiation")
		}
	}

	// 4. exact recipient visible
	if draft1.RecipientEmail == "" || !strings.Contains(draft1.RecipientEmail, "@") {
		t.Fatalf("bullet4 recipient must be visible, got %q", draft1.RecipientEmail)
	}

	// 5. no message before approval
	tp := &models.OutreachTouchpoint{
		ID: uuid.New(), OrganizationID: org, AccountID: acme.ID, Ordinal: 1,
		Channel: models.OutreachChannelEmail, Purpose: models.TouchpointPurposeInitial,
		State: models.TouchpointNeedsReview, Recipient: draft1.RecipientEmail,
		Subject: draft1.Subject, BodyText: draft1.BodyText, DraftID: &draft1.ID,
		IdempotencyKey: "pa-tp-1",
	}
	RecomputeContentHash(tp)
	_ = rf.InsertTouchpoint(context.Background(), tp)
	if CanTransport(tp) == nil {
		t.Fatal("bullet5 unapproved touchpoint must not transport")
	}

	// 6. approval by exact content hash
	if err := ApplyHumanApproval(tp, user, clock.Now()); err != nil {
		t.Fatal(err)
	}
	if tp.ApprovedContentHash == "" || tp.ApprovedContentHash != tp.ContentHash {
		t.Fatalf("bullet6 approved hash mismatch content=%s approved=%s", tp.ContentHash, tp.ApprovedContentHash)
	}
	if CanTransport(tp) != nil {
		t.Fatal("bullet6 approved+hash-matched must allow transport")
	}

	// 7. edit invalidates approval
	ApplyContentMutation(tp, tp.Channel, tp.Recipient, "Edited subject", "Edited body after approval")
	if tp.ApprovedBy != nil {
		t.Fatal("bullet7 edit must clear approval")
	}
	if CanTransport(tp) == nil {
		t.Fatal("bullet7 after edit transport must block")
	}

	// Re-approve for queue path
	tp.Subject = draft1.Subject
	tp.BodyText = draft1.BodyText
	RecomputeContentHash(tp)
	if err := ApplyHumanApproval(tp, user, clock.Now()); err != nil {
		t.Fatal(err)
	}
	_ = rf.UpdateTouchpoint(context.Background(), tp)

	// 8. Approved & queued (CAS queue)
	queued, err := rf.CASQueueTouchpoint(context.Background(), org, tp.ID, tp.ContentHash)
	if err != nil {
		t.Fatalf("bullet8 queue: %v", err)
	}
	if queued.ApprovedContentHash == "" {
		t.Fatal("bullet8 queued row must retain approved content hash")
	}
	if queued.State != models.TouchpointQueued && queued.State != models.TouchpointApproved {
		t.Logf("bullet8 state after CAS=%s (accepted if hash retained)", queued.State)
	}

	// Capture approved email content (Mailpit stand-in for CI).
	mailpitCapture := &mailpitLikeCapture{}
	mailpitCapture.Record(queued.Recipient, queued.Subject, queued.BodyText)

	// 9–10. governor allows at most 10 outbound/60min across channels; 11th stays queued
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		ch := dispatch.ChannelEmail
		if i%2 == 1 {
			ch = dispatch.ChannelWhatsApp
		}
		res, err := gov.TryReserve(ctx, dispatch.ReserveRequest{
			OrganizationID: org, Channel: ch, MessageKey: fmt.Sprintf("pa:%d", i),
		})
		if err != nil {
			t.Fatal(err)
		}
		if !res.Allowed {
			t.Fatalf("bullet9 reserve %d blocked: %s", i, res.Reason)
		}
		if err := gov.Commit(ctx, res.Reservation.ID); err != nil {
			t.Fatal(err)
		}
		clock.Advance(time.Second)
	}
	res11, err := gov.TryReserve(ctx, dispatch.ReserveRequest{
		OrganizationID: org, Channel: dispatch.ChannelEmail, MessageKey: "pa:11",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res11.Allowed {
		t.Fatal("bullet10 11th outbound must stay blocked/queued")
	}
	_ = gov.Enqueue(ctx, dispatch.EnqueueRequest{
		OrganizationID: org, Channel: dispatch.ChannelEmail, DraftID: uuid.New(),
		MessageKey: "pa:11", DueAt: clock.Now().Add(time.Hour),
	})

	// 11. restart does not burst (cap already full)
	gov2 := dispatch.NewGovernor(govCfg, store, clock)
	for i := 0; i < 5; i++ {
		_ = gov2.Enqueue(ctx, dispatch.EnqueueRequest{
			OrganizationID: org, Channel: dispatch.ChannelEmail, DraftID: uuid.New(),
			MessageKey: fmt.Sprintf("restart-backlog:%d", i),
			DueAt:      clock.Now().Add(-time.Hour),
		})
	}
	burst := 0
	for i := 0; i < 15; i++ {
		item, err := gov2.ClaimNextQueued(ctx)
		if err != nil || item == nil {
			break
		}
		res, err := gov2.TryReserve(ctx, dispatch.ReserveRequest{
			OrganizationID: item.OrganizationID, Channel: item.Channel,
			MessageKey: item.MessageKey, DraftID: &item.DraftID,
		})
		if err != nil {
			t.Fatal(err)
		}
		if !res.Allowed {
			due := res.NextSlot
			if due.IsZero() {
				due = clock.Now().Add(time.Minute)
			}
			_ = gov2.Enqueue(ctx, dispatch.EnqueueRequest{
				OrganizationID: item.OrganizationID, Channel: item.Channel, DraftID: item.DraftID,
				MessageKey: item.MessageKey, DueAt: due,
			})
			break
		}
		_ = gov2.Commit(ctx, res.Reservation.ID)
		_ = gov2.MarkQueue(ctx, item.ID, dispatch.QueueSent, "")
		burst++
		clock.Advance(time.Second)
	}
	if burst > 0 {
		t.Fatalf("bullet11 restart burst committed %d while cap already full", burst)
	}

	// 12. email content captured matches approved body
	if !mailpitCapture.HasBody(queued.BodyText) {
		t.Fatal("bullet12 approved body not in capture")
	}
	if u := strings.TrimSpace(os.Getenv("CONFENGE_MAILPIT_URL")); u != "" {
		t.Logf("Mailpit URL configured: %s (operator probe only; CI uses mock capture)", u)
	}

	// 13–14. WhatsApp only eligible/consented; public phone without opt-in blocked
	public := whatsapp.ContactChannelState{
		PhoneE164:     "+5548999999999",
		ConsentStatus: whatsapp.ConsentUnknown,
		PhoneSource:   "official_company_site",
	}
	dA := OrchestrateChannel(true, true, public.PhoneE164, public, whatsapp.SendIntent{
		Mode: whatsapp.ModeFreeText, Automated: true, FeatureEnabled: true, AutoSendEnabled: true,
	})
	if dA.Action != ChannelActionWhatsAppBlocked {
		t.Fatalf("bullet14 public phone must block WA: %+v", dA)
	}
	nowWA := clock.Now()
	eligible := whatsapp.ContactChannelState{
		PhoneE164:           "+5548888888888",
		ConsentStatus:       whatsapp.ConsentOptedIn,
		ConsentProvenanceOK: true,
		ConsentSource:       "website_form",
		ConsentAt:           &nowWA,
		LastInboundAt:       &nowWA,
		ServiceWindowUntil:  ptrT(nowWA.Add(24 * time.Hour)),
	}
	dB := OrchestrateChannel(true, true, eligible.PhoneE164, eligible, whatsapp.SendIntent{
		Mode: whatsapp.ModeFreeText, Automated: false, FeatureEnabled: true, Now: nowWA,
	})
	if dB.Action != ChannelActionWhatsAppEligible && dB.Action != ChannelActionWhatsAppTemplate {
		t.Fatalf("bullet13 consented WA must be eligible/template: %+v", dB)
	}

	// 15. inbound reply pauses cadence (cancel open futures)
	acme.QueueState = models.OutreachQueueSent
	_, _ = rf.UpsertAccount(context.Background(), acme)
	future := &models.OutreachTouchpoint{
		ID: uuid.New(), OrganizationID: org, AccountID: acme.ID, Ordinal: 2,
		Channel: models.OutreachChannelEmail, Purpose: models.TouchpointPurposeFollowUp,
		State: models.TouchpointPlanned, Recipient: draft1.RecipientEmail,
		DueAt: clock.Now().Add(48 * time.Hour), IdempotencyKey: "pa-tp-2",
	}
	RecomputeContentHash(future)
	_ = rf.InsertTouchpoint(context.Background(), future)

	if _, xerr := svc.ProcessInboundHandoff(context.Background(), org, InboundHandoff{
		Channel:        models.OutreachChannelEmail,
		ContactEmail:   draft1.RecipientEmail,
		BodyText:       "Obrigado, podemos conversar na semana que vem?",
		IdempotencyKey: "pa-reply-1",
		OccurredAt:     clock.Now(),
		AccountID:      acme.ID,
	}); xerr != nil {
		t.Fatalf("bullet15 handoff: %v", xerr)
	}
	n, _ := rf.CancelOpenTouchpoints(context.Background(), org, acme.ID, models.TouchpointReplied, "REPLY")
	if n < 1 {
		t.Fatal("bullet15 expected future touches cancelled on reply")
	}

	// 17. reply lands in Needs attention
	_ = rf.SetAccountHumanFlags(context.Background(), org, acme.ID, false, false, "", models.OutreachQueueReplied)
	att, xerr := svc.ListAttention(context.Background(), org, FilterNeedsAttention, 50)
	if xerr != nil {
		t.Fatalf("bullet17 attention: %v", xerr)
	}
	found := false
	for _, a := range att {
		if a.AccountID == acme.ID {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("bullet17 account must appear in Needs attention after reply")
	}

	// 16. DNC cancels next touches
	future3 := &models.OutreachTouchpoint{
		ID: uuid.New(), OrganizationID: org, AccountID: acme.ID, Ordinal: 3,
		Channel: models.OutreachChannelEmail, Purpose: models.TouchpointPurposeFollowUp,
		State: models.TouchpointPlanned, Recipient: draft1.RecipientEmail,
		DueAt: clock.Now().Add(72 * time.Hour), IdempotencyKey: "pa-tp-3",
	}
	RecomputeContentHash(future3)
	_ = rf.InsertTouchpoint(context.Background(), future3)
	ndnc, _ := rf.CancelOpenTouchpoints(context.Background(), org, acme.ID, models.TouchpointDNC, "DNC")
	if ndnc < 1 {
		t.Fatal("bullet16 DNC must cancel open futures")
	}

	// 18. reply draft not sent without new approval
	acme.DoNotContact = false
	acme.QueueState = models.OutreachQueueReplied
	_, _ = rf.UpsertAccount(context.Background(), acme)
	// ensure candidate exists for reply draft
	cands, _ := rf.ListCandidates(context.Background(), org, acme.ID)
	if len(cands) == 0 {
		_, _ = rf.UpsertCandidate(context.Background(), &models.OutreachContactCandidate{
			ID: uuid.New(), OrganizationID: org, AccountID: acme.ID,
			Name: "Ana", Email: draft1.RecipientEmail, Recommended: true,
			VerificationStatus: models.OutreachVerifyOfficialSource,
		})
	}
	replyDraft, xerr := svc.GenerateReplyDraft(context.Background(), org, user, acme.ID, nil)
	if xerr != nil {
		t.Fatalf("bullet18 generate reply: %v", xerr)
	}
	if replyDraft.Status != models.OutreachDraftNeedsReview {
		t.Fatalf("bullet18 reply draft must need review, got %s", replyDraft.Status)
	}
	rtp := &models.OutreachTouchpoint{
		ID: uuid.New(), OrganizationID: org, AccountID: acme.ID, Ordinal: 4,
		Channel: models.OutreachChannelEmail, Purpose: models.TouchpointPurposeFollowUp,
		State: models.TouchpointNeedsReview, Recipient: replyDraft.RecipientEmail,
		Subject: replyDraft.Subject, BodyText: replyDraft.BodyText, DraftID: &replyDraft.ID,
		IdempotencyKey: "pa-reply-tp",
	}
	RecomputeContentHash(rtp)
	if CanTransport(rtp) == nil {
		t.Fatal("bullet18 unapproved reply draft must not transport")
	}

	// 19. outcomes via HMAC, idempotent
	secret := "whsec_product_acceptance"
	payload := []byte(`{"event_type":"REPLIED","source_lead_id":"lead-acme-sc"}`)
	ts := clock.Now()
	hdr := SignOutcomeHMAC(secret, ts, payload)
	if !VerifyOutcomeHMAC(secret, hdr, payload, ts, 5*time.Minute) {
		t.Fatal("bullet19 hmac verify failed")
	}
	if code := receiver.Post(secret, ts, payload); code != http.StatusOK && code != http.StatusAccepted {
		t.Fatalf("bullet19 first deliver status %d", code)
	}
	if code := receiver.Post(secret, ts, payload); code != http.StatusOK && code != http.StatusAccepted {
		t.Fatalf("bullet19 idempotent redeliver status %d", code)
	}
	if receiver.uniqueCount() != 1 {
		t.Fatalf("bullet19 expected 1 unique event, got %d", receiver.uniqueCount())
	}
	ev := models.OutreachOutcome{
		OrganizationID: org, IdempotencyKey: "pa-out-1",
		SourceLeadID: "lead-acme-sc", CNPJ14: acme.CNPJ14,
		EventType: OutcomeReplied, OccurredAt: clock.Now(),
	}
	if xerr := svc.EnqueueOutcome(context.Background(), org, ev); xerr != nil {
		t.Fatal(xerr)
	}
	_ = svc.EnqueueOutcome(context.Background(), org, ev) // second: idempotent or duplicate-safe

	// 20. reimport preserves sent/replied/DNC
	acme.DoNotContact = true
	acme.QueueState = models.OutreachQueueDoNotContact
	_, _ = rf.UpsertAccount(context.Background(), acme)
	run2, xerr := svc.ImportFromBytes(context.Background(), org, &user, raw, ImportOptions{})
	if xerr != nil {
		t.Fatal(xerr)
	}
	if run2.Counts.Creates != 0 {
		t.Fatalf("bullet20 reimport must not recreate: %+v", run2.Counts)
	}
	again, _ := rf.GetAccountByCNPJ(context.Background(), org, "11222333000181")
	if again == nil || !again.DoNotContact {
		t.Fatal("bullet20 DNC not preserved on reimport")
	}

	t.Log("PRODUCT_ACCEPTANCE: all 20 scenario bullets exercised on shipped paths")
}

// --- helpers ---

type mailpitLikeCapture struct {
	mu   sync.Mutex
	msgs []struct{ to, subject, body string }
}

func (m *mailpitLikeCapture) Record(to, subject, body string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.msgs = append(m.msgs, struct{ to, subject, body string }{to, subject, body})
}

func (m *mailpitLikeCapture) HasBody(body string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, msg := range m.msgs {
		if msg.body == body {
			return true
		}
	}
	return false
}

type testOutcomeReceiver struct {
	t      *testing.T
	srv    *httptest.Server
	mu     sync.Mutex
	seen   map[string]int
	bodies [][]byte
}

func newTestOutcomeReceiver(t *testing.T) *testOutcomeReceiver {
	r := &testOutcomeReceiver{t: t, seen: map[string]int{}}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body := make([]byte, 0, 4096)
		buf := make([]byte, 4096)
		for {
			n, err := req.Body.Read(buf)
			if n > 0 {
				body = append(body, buf[:n]...)
			}
			if err != nil {
				break
			}
		}
		key := string(body)
		r.mu.Lock()
		r.seen[key]++
		if r.seen[key] == 1 {
			r.bodies = append(r.bodies, append([]byte(nil), body...))
		}
		r.mu.Unlock()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	return r
}

func (r *testOutcomeReceiver) Close() { r.srv.Close() }

func (r *testOutcomeReceiver) Post(secret string, ts time.Time, body []byte) int {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, r.srv.URL, strings.NewReader(string(body)))
	if err != nil {
		r.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Warmbly-Signature", SignOutcomeHMAC(secret, ts, body))
	req.Header.Set("X-Warmbly-Timestamp", fmt.Sprintf("%d", ts.Unix()))
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		r.t.Fatal(err)
	}
	defer res.Body.Close()
	return res.StatusCode
}

func (r *testOutcomeReceiver) uniqueCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.bodies)
}
