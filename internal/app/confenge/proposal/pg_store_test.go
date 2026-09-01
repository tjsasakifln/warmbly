package proposal

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestDecodeOutcomeFeedbackFactFailsClosedPerRow(t *testing.T) {
	orgID := uuid.MustParse("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee")
	proposalID := uuid.MustParse("11111111-2222-4333-8444-555555555555")
	sentAt := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	p := Proposal{
		SchemaVersion: ProposalSchemaVersion, OrganizationID: orgID,
		ProposalID: proposalID, ProposalVersion: 2, AccountID: "account-1",
		OpportunityID: "opportunity-1", CorrelationID: "correlation-1",
		DecisionState: StateSent, SentAt: &sentAt, Amount: 100_000, Currency: "BRL",
	}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	fact, ok := decodeOutcomeFeedbackFact(
		orgID, proposalID, 2, "account-1", "opportunity-1", "correlation-1", StateSent, false, raw,
	)
	if !ok || fact.ProposalID != proposalID || fact.AmountMinor != 100_000 {
		t.Fatalf("valid indexed proposal was rejected: ok=%v fact=%+v", ok, fact)
	}
	if _, ok := decodeOutcomeFeedbackFact(
		orgID, proposalID, 2, "account-1", "opportunity-1", "correlation-1", StateSent, false, []byte(`{"sent_at":`),
	); ok {
		t.Fatal("malformed historical payload was accepted")
	}
	if _, ok := decodeOutcomeFeedbackFact(
		orgID, proposalID, 2, "another-account", "opportunity-1", "correlation-1", StateSent, false, raw,
	); ok {
		t.Fatal("payload/index disagreement was accepted")
	}
}
