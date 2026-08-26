package intel

import (
	"strings"
	"testing"
	"time"
)

func TestEmailOutcomeReplayRestartAndOutOfOrderRemainHonest(t *testing.T) {
	orgID := "00000000-0000-0000-0000-000000000047"
	base := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	event := func(id, typ string, at time.Time) CommercialEvent {
		return CommercialEvent{
			EventID: id, Version: EventSchemaV1, Type: typ,
			OccurredAt: at, IngestedAt: base.Add(time.Hour), OrganizationID: orgID,
			CorrelationID: "touchpoint-47:account-47", IdempotencyKey: "event:" + id,
			EmailRouteClass: "R1_DIRECT", Source: "extra-cli", SourceRunID: "run-47",
			MailboxID: "00000000-0000-0000-0000-000000000047", ProviderName: "smtp",
		}
	}

	store := NewMemoryStore()
	accepted := IngestEvent(store, event("accepted-47", EventProviderAccepted, base.Add(time.Minute)))
	if !hasCode(accepted.Exceptions, ExceptionOutOfOrder) {
		t.Fatal("accepted before attempted must enter the existing exception queue")
	}
	if accepted.Chain.Commercial.Email.DeliveryStatus != EventProviderAccepted {
		t.Fatalf("status=%s", accepted.Chain.Commercial.Email.DeliveryStatus)
	}
	IngestEvent(store, event("attempted-47", EventEmailAttempted, base))
	unknown := IngestEvent(store, event("unknown-47", EventDeliveryUnknown, base.Add(2*time.Minute)))
	if unknown.Chain.Commercial.Email.DeliveryStatus != EventProviderAccepted {
		t.Fatalf("UNKNOWN must not regress accepted, got %s", unknown.Chain.Commercial.Email.DeliveryStatus)
	}
	if unknown.Chain.Commercial.Email.DeliveryUnknownAt == nil {
		t.Fatal("UNKNOWN evidence must remain preserved")
	}
	delivered := IngestEvent(store, event("delivered-47", EventDelivered, base.Add(3*time.Minute)))
	if delivered.Chain.Commercial.Email.DeliveryStatus != EventDelivered {
		t.Fatalf("explicit delivery must resolve UNKNOWN, got %s", delivered.Chain.Commercial.Email.DeliveryStatus)
	}

	// Simulate process restart by restoring the durable chain into a fresh store.
	restarted := NewMemoryStore()
	if _, _, err := restarted.PutChain(delivered.Chain); err != nil {
		t.Fatal(err)
	}
	replay := IngestEvent(restarted, event("delivered-47", EventDelivered, base.Add(3*time.Minute)))
	if !replay.Replay {
		t.Fatal("provider/event replay after restart must be idempotent")
	}
	if got, want := len(replay.Chain.Commercial.Timeline), len(delivered.Chain.Commercial.Timeline); got != want {
		t.Fatalf("timeline duplicated after restart replay: got=%d want=%d", got, want)
	}
}

func TestEmailOutcomeSlicesIncludeOperationalDimensionsWithoutPII(t *testing.T) {
	events := []CommercialEvent{{
		EventID: "soft-47", Type: EventSoftBounce, EmailRouteClass: "R4_ROLE_ROUTE",
		Source: "extra-cli", SourceRunID: "run-47", MailboxID: "mailbox-47",
		ProviderName: "smtp", CohortID: "cohort-47", PolicyVersion: "policy-47",
	}, {
		EventID: "unknown-47", Type: EventDeliveryUnknown, EmailRouteClass: "R4_ROLE_ROUTE",
		Source: "extra-cli", SourceRunID: "run-47", MailboxID: "mailbox-47",
		ProviderName: "smtp", CohortID: "cohort-47", PolicyVersion: "policy-47",
	}}
	rows := SliceControlledEmailOutcomes(events)
	if len(rows) != 1 || rows[0].SoftBounce == nil || *rows[0].SoftBounce != 1 {
		t.Fatalf("soft bounce slice=%+v", rows)
	}
	if rows[0].HardBounce != nil {
		t.Fatal("soft bounce must never be promoted to hard bounce")
	}
	if rows[0].DeliveryUnknown == nil || *rows[0].DeliveryUnknown != 1 {
		t.Fatalf("delivery_unknown slice=%+v", rows[0])
	}
	report := FormatControlledEmailReport(BuildControlledEmailExecutiveReport(events))
	for _, want := range []string{"source_run_id", "mailbox_id", "run-47", "mailbox-47", "delivery_unknown"} {
		if !strings.Contains(report, want) {
			t.Fatalf("report missing %q: %s", want, report)
		}
	}
	if strings.Contains(report, "@") || MetricKeyContainsPII(report) {
		t.Fatal("operational outcome report must not contain recipient PII")
	}
}

func TestNoReplyDoesNotInvalidateMailbox(t *testing.T) {
	ev := CommercialEvent{Type: EventNoReply}
	if !NonReplyDoesNotInvalidateMailbox(ev) {
		t.Fatal("no reply is UNKNOWN, never bad mailbox evidence")
	}
	rows := SliceControlledEmailOutcomes([]CommercialEvent{{EventID: "no-reply-47", Type: EventNoReply}})
	if len(rows) != 1 || rows[0].Unknown == nil || *rows[0].Unknown != 1 || rows[0].HardBounce != nil {
		t.Fatalf("no-reply projection=%+v", rows)
	}
}
