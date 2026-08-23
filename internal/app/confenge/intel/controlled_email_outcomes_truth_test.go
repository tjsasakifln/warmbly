package intel

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestControlledEmailReportPublishesStableEmptyArray(t *testing.T) {
	report := BuildObservabilityReport(NewMemoryStore(), "11111111-2222-4333-8444-555555555555", "2026-08", false)
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"controlled_email":[]`) {
		t.Fatalf("empty real report must keep the controlled-email contract: %s", raw)
	}
}

func TestControlledEmailReportPreservesUnknownAndDeduplicatesReplay(t *testing.T) {
	attempt := CommercialEvent{
		EventID: "event-1", IdempotencyKey: "attempt:one", Type: EventEmailAttempted,
		EmailRouteClass: "GENERIC_COMPANY", CohortID: "cohort-1", PolicyVersion: "p1",
	}
	rows := SliceControlledEmailOutcomes([]CommercialEvent{attempt, attempt})
	if len(rows) != 1 || rows[0].Attempted == nil || *rows[0].Attempted != 1 {
		t.Fatalf("replay counted twice: %+v", rows)
	}
	if rows[0].ProviderAccepted != nil || rows[0].Delivered != nil || rows[0].HardBounce != nil {
		t.Fatalf("absence was fabricated as zero: %+v", rows[0])
	}
	text := FormatControlledEmailReport(ControlledEmailExecutiveReport{Rows: rows})
	if !strings.Contains(text, "\t1\tUNKNOWN\tUNKNOWN\tUNKNOWN\t") {
		t.Fatalf("founder report lost UNKNOWN: %s", text)
	}
}

func TestControlledEmailSurvivesDurableChainReadModel(t *testing.T) {
	store := NewMemoryStore()
	at := time.Date(2026, 8, 22, 15, 0, 0, 0, time.UTC)
	event := CommercialEvent{
		EventID: "accepted-1", Version: EventSchemaV1, Type: EventProviderAccepted,
		OccurredAt: at, IngestedAt: at, OrganizationID: "11111111-2222-4333-8444-555555555555",
		IdempotencyKey: "accepted:tp-1", CorrelationID: "tp-1:acc-1",
		AccountPublicID: "acc-1", EntityPublicID: "tp-1", EmailRouteClass: "GENERIC_COMPANY",
		CohortID: "cohort-1", PolicyVersion: "p1", ProviderName: "smtp", Synthetic: false,
	}
	result := IngestEvent(store, event)
	if result.Held {
		t.Fatalf("event held: %+v", result.Exceptions)
	}
	report := BuildObservabilityReport(store, event.OrganizationID, "2026-08", false)
	if len(report.ControlledEmail) != 1 {
		t.Fatalf("controlled email disappeared on read path: %+v", report.ControlledEmail)
	}
	row := report.ControlledEmail[0]
	if row.ProviderAccepted == nil || *row.ProviderAccepted != 1 || row.CohortID != "cohort-1" || row.Provider != "smtp_imap" {
		t.Fatalf("durable context lost: %+v", row)
	}
	if row.Delivered != nil {
		t.Fatalf("SMTP accepted fabricated delivery: %+v", row)
	}
}

func TestSMTPAndSMTPIMAPProviderLabelsReconcileIntoOneCohortSlice(t *testing.T) {
	base := CommercialEvent{EmailRouteClass: "GENERIC_COMPANY", CohortID: "cohort-1", PolicyVersion: "p1"}
	attempt := base
	attempt.EventID, attempt.Type, attempt.ProviderName = "attempt-1", EventEmailAttempted, "smtp"
	bounce := base
	bounce.EventID, bounce.Type, bounce.ProviderName = "bounce-1", EventSoftBounce, "smtp_imap"
	rows := SliceControlledEmailOutcomes([]CommercialEvent{attempt, bounce})
	if len(rows) != 1 || rows[0].Provider != "smtp_imap" || rows[0].SoftBounce == nil || *rows[0].SoftBounce != 1 {
		t.Fatalf("provider aliases fragmented cohort reporting: %+v", rows)
	}
}

func TestControlledEmailReplyClassificationUsesShippedVocabulary(t *testing.T) {
	rows := SliceControlledEmailOutcomes([]CommercialEvent{{
		EventID: "reply-1", Type: EventReply, ReplyClass: "POSITIVE_INTEREST",
		EmailRouteClass: "DIRECT_PERSON", CohortID: "cohort-1",
	}})
	if len(rows) != 1 || rows[0].Reply == nil || *rows[0].Reply != 1 ||
		rows[0].PositiveReply == nil || *rows[0].PositiveReply != 1 {
		t.Fatalf("positive reply not projected: %+v", rows)
	}
}

func TestReplyOptOutCountsAsReplyAndSuppressionWithoutInventingPositive(t *testing.T) {
	rows := SliceControlledEmailOutcomes([]CommercialEvent{{
		EventID: "reply-opt-out-1", Type: EventOptOut, ReplyClass: "OPT_OUT",
		EmailRouteClass: "GENERIC_COMPANY", CohortID: "cohort-1",
	}})
	if len(rows) != 1 || rows[0].Reply == nil || *rows[0].Reply != 1 ||
		rows[0].OptOut == nil || *rows[0].OptOut != 1 {
		t.Fatalf("reply opt-out projection incomplete: %+v", rows)
	}
	if rows[0].PositiveReply == nil || *rows[0].PositiveReply != 0 {
		t.Fatalf("classified opt-out must prove positive_reply=0: %+v", rows)
	}
}
