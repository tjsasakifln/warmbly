package confenge

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/app/confenge/intel"
	"github.com/warmbly/warmbly/internal/models"
)

func TestHardBounceSuppressesExactMailboxWithoutInvalidatingCompany(t *testing.T) {
	ctx := context.Background()
	repo := newMemRepoWithSettings()
	svc := NewService(Config{Enabled: true, DefaultDailyLimit: 10}, repo, nil).(*service)
	orgID, accountID := uuid.New(), uuid.New()
	_, _ = repo.UpsertAccount(ctx, &models.OutreachAccount{
		ID: accountID, OrganizationID: orgID, CNPJ14: "12345678000147", QueueState: models.OutreachQueueSent,
	})
	exactID, otherID := uuid.New(), uuid.New()
	_, _ = repo.UpsertCandidate(ctx, &models.OutreachContactCandidate{ID: exactID, OrganizationID: orgID, AccountID: accountID, Email: "bounced@example.com"})
	_, _ = repo.UpsertCandidate(ctx, &models.OutreachContactCandidate{ID: otherID, OrganizationID: orgID, AccountID: accountID, Email: "healthy@example.com"})
	for i, recipient := range []string{"bounced@example.com", "healthy@example.com"} {
		if err := repo.InsertTouchpoint(ctx, &models.OutreachTouchpoint{
			ID: uuid.New(), OrganizationID: orgID, AccountID: accountID, Ordinal: i + 1,
			Channel: models.OutreachChannelEmail, State: models.TouchpointPlanned,
			Recipient: recipient, DueAt: time.Now().UTC().Add(time.Hour),
		}); err != nil {
			t.Fatal(err)
		}
	}

	if err := svc.NoteBounceObservation(ctx, orgID, "bounced@example.com", BounceObservation{
		Class: "HARD", EventID: "provider-bounce-47", OriginalMessageID: "message-47",
		Reason: "550 5.1.1 mailbox unavailable", EnhancedStatus: "5.1.1",
	}); err != nil {
		t.Fatal(err)
	}
	exact, _ := repo.GetCandidate(ctx, orgID, exactID)
	other, _ := repo.GetCandidate(ctx, orgID, otherID)
	if exact == nil || !exact.Bounced || exact.VerificationStatus != models.OutreachVerifyBounced {
		t.Fatalf("exact mailbox was not bounced: %+v", exact)
	}
	if other == nil || other.Bounced || other.Blocked || other.DoNotContact {
		t.Fatalf("healthy company route was invalidated: %+v", other)
	}
	account, _ := repo.GetAccount(ctx, orgID, accountID)
	if account.Blocked || account.DoNotContact || account.QueueState != models.OutreachQueueSent {
		t.Fatalf("company identity was invalidated: %+v", account)
	}
	touchpoints, _ := repo.ListTouchpoints(ctx, orgID, accountID, "", 10, 0)
	for _, touchpoint := range touchpoints {
		if touchpoint.Recipient == "bounced@example.com" && touchpoint.State != models.TouchpointBounced {
			t.Fatalf("bounced route state=%s", touchpoint.State)
		}
		if touchpoint.Recipient == "healthy@example.com" && touchpoint.State != models.TouchpointPlanned {
			t.Fatalf("healthy route state=%s", touchpoint.State)
		}
	}
	if suppression, _ := repo.GetOutreachRecipientSuppression(ctx, orgID, "bounced@example.com"); suppression == nil || suppression.Source != models.DeliverabilityEventBounce {
		t.Fatalf("canonical exact suppression missing: %+v", suppression)
	}
}

func TestProviderWebhookSoftBounceAndDeliveryShareAuthoritativeLedger(t *testing.T) {
	ctx := context.Background()
	repo := newMemRepoWithSettings()
	svc := NewService(Config{Enabled: true, DefaultDailyLimit: 10}, repo, nil).(*service)
	orgID, accountID := uuid.New(), uuid.New()
	_, _ = repo.UpsertAccount(ctx, &models.OutreachAccount{
		ID: accountID, OrganizationID: orgID, SourceRunID: "run-47", SourceSystem: "provider-webhook",
	})
	_, _ = repo.UpsertCandidate(ctx, &models.OutreachContactCandidate{
		ID: uuid.New(), OrganizationID: orgID, AccountID: accountID, Email: "route@example.com",
	})
	soft := &models.IngestDeliverabilityEventRequest{
		EventType: models.DeliverabilityEventSoftBounce, RecipientEmail: "route@example.com",
		IdempotencyKey: "provider-soft-47", Metadata: map[string]interface{}{"enhanced_status": "4.2.0"},
	}
	when := time.Date(2026, 8, 26, 15, 0, 0, 0, time.UTC)
	mailboxID := uuid.New()
	if err := svc.OnDeliverabilityEvent(ctx, orgID, soft, "ses", soft.IdempotencyKey, mailboxID, when); err != nil {
		t.Fatal(err)
	}
	if err := svc.OnDeliverabilityEvent(ctx, orgID, soft, "ses", soft.IdempotencyKey, mailboxID, when); err != nil {
		t.Fatal(err)
	}
	if suppression, _ := repo.GetOutreachRecipientSuppression(ctx, orgID, soft.RecipientEmail); suppression != nil {
		t.Fatalf("soft bounce created permanent suppression: %+v", suppression)
	}
	delivered := &models.IngestDeliverabilityEventRequest{
		EventType: models.DeliverabilityEventDelivered, RecipientEmail: soft.RecipientEmail,
		IdempotencyKey: "provider-delivered-47", Metadata: map[string]interface{}{"enhanced_status": "2.0.0"},
	}
	if err := svc.OnDeliverabilityEvent(ctx, orgID, delivered, "ses", delivered.IdempotencyKey, mailboxID, when.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	chains, err := svc.intelStore().ListChains(orgID.String())
	if err != nil || len(chains) != 1 {
		t.Fatalf("authoritative chains=%d err=%v", len(chains), err)
	}
	counts := map[string]int{}
	for _, receipt := range chains[0].Commercial.Timeline {
		counts[receipt.Type]++
	}
	if counts[intel.EventSoftBounce] != 1 || counts[intel.EventDelivered] != 1 {
		t.Fatalf("provider replay duplicated or lost facts: %+v", counts)
	}
}
