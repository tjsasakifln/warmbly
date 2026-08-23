package confenge

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/app/confenge/intel"
)

func TestControlledReplyClassIsBoundedAndUnknownSafe(t *testing.T) {
	cases := map[string]string{
		IntentPositiveInterest: "POSITIVE",
		IntentNegative:         "NEGATIVE",
		IntentReferral:         "ROUTED/FORWARDED",
		IntentDoNotContact:     "OPT_OUT",
		IntentQuestion:         "NEUTRAL",
		"invented-new-class":   "UNKNOWN",
	}
	for input, want := range cases {
		if got := controlledReplyClass(input); got != want {
			t.Fatalf("input=%s got=%s want=%s", input, got, want)
		}
	}
}

func TestLaterMailboxEventCarriesProviderFromDurableControlledReceipt(t *testing.T) {
	store := intel.NewMemoryStore()
	svc := &service{intel: store}
	org := uuid.New()
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	result := intel.IngestEvent(store, intel.CommercialEvent{
		EventID: "accepted-provider-1", Version: intel.EventSchemaV1,
		Type: intel.EventProviderAccepted, OccurredAt: now, IngestedAt: now,
		OrganizationID: org.String(), IdempotencyKey: "accepted:tp-1",
		AccountPublicID: "acc-1", EntityPublicID: "tp-1",
		EmailRouteClass: RouteClassGenericCompany, CohortID: "cohort-1",
		PolicyVersion: BoundedCohortPolicyV1, ProviderName: "smtp",
	})
	if result.Held {
		t.Fatalf("setup event held: %+v", result.Exceptions)
	}
	if got := svc.observedControlledProvider(org, "acc-1", "tp-1"); got != "smtp" {
		t.Fatalf("provider did not survive to later IMAP/DSN event: %s", got)
	}
}

func TestClassifyControlledReplyIsOfflineBoundedAndOptOutSafe(t *testing.T) {
	if got := ClassifyControlledReply("Re: proposta", "tenho interesse, vamos agendar", nil); got != "POSITIVE" {
		t.Fatalf("positive deterministic evidence lost: %s", got)
	}
	if got := ClassifyControlledReply("Re: proposta", "remova meu email da lista", nil); got != "OPT_OUT" {
		t.Fatalf("opt-out evidence lost: %s", got)
	}
	if got := ClassifyControlledReply("Re: proposta", "recebido", nil); got != "UNKNOWN" {
		t.Fatalf("ambiguous reply was invented as classified: %s", got)
	}
}
