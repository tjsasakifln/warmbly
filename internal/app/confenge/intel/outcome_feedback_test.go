package intel

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestParseOutcomeFeedbackPeriod(t *testing.T) {
	now := time.Date(2026, 8, 31, 18, 0, 0, 0, time.UTC)
	period, err := ParseOutcomeFeedbackPeriod("2026-08", "2026-08", now)
	if err != nil {
		t.Fatal(err)
	}
	if got := period.From.Format(time.RFC3339); got != "2026-08-01T00:00:00Z" {
		t.Fatalf("from=%s", got)
	}
	if got := period.To.Format(time.RFC3339); got != "2026-09-01T00:00:00Z" {
		t.Fatalf("exclusive to=%s", got)
	}
	defaultPeriod, err := ParseOutcomeFeedbackPeriod("", "", now)
	if err != nil {
		t.Fatal(err)
	}
	if got := defaultPeriod.From.Format(time.RFC3339); got != "2026-06-01T00:00:00Z" {
		t.Fatalf("default from=%s", got)
	}
	if got := defaultPeriod.To.Format(time.RFC3339); got != "2026-09-01T00:00:00Z" {
		t.Fatalf("default=%+v", defaultPeriod)
	}
	if _, err := ParseOutcomeFeedbackPeriod("2026-09", "2026-08", now); err == nil {
		t.Fatal("accepted reversed period")
	}
	if _, err := ParseOutcomeFeedbackPeriod("2026-08-02", "2026-08", now); err == nil {
		t.Fatal("accepted non-month cohort")
	}
}

func TestAcquisitionOutcomeFeedbackWithholdsSensitiveSubcellsBelowFive(t *testing.T) {
	now := time.Date(2026, 8, 31, 18, 0, 0, 0, time.UTC)
	period := OutcomeFeedbackPeriod{From: now.AddDate(0, 0, -30), To: now}
	chains := make([]Chain, 0, 8)
	for i := 0; i < 5; i++ {
		chain := feedbackTestChain(i, now.AddDate(0, 0, -10), "/defesa-margem", "reequilibrio")
		if i < 2 {
			chain.Qualified = true
			chain.OutcomeType = OutcomeQualifiedConversation
			chain.OpportunityID = fmt.Sprintf("opportunity-observed-%d", i)
			chain.Keys.OpportunityID = chain.OpportunityID
		}
		if i == 2 {
			chain.ProposalID = "proposal-observed"
			chain.Keys.ProposalID = chain.ProposalID
		}
		if i == 3 {
			chain.HumanConfirmed = true
			chain.OutcomeType = OutcomeWon
			chain.Commercial.Payment.ContractedCents = 800_000
			chain.Commercial.Payment.ReceivedCents = 400_000
		}
		chains = append(chains, chain)
	}
	old := feedbackTestChain(90, now.AddDate(0, 0, -40), "/defesa-margem", "reequilibrio")
	chains = append(chains, old)
	outbound := feedbackTestChain(91, now.AddDate(0, 0, -10), "/defesa-margem", "reequilibrio")
	outbound.RouteFamily = FamilyOutbound
	outbound.Keys.RouteFamily = FamilyOutbound
	outbound.Source = SourceOutbound
	outbound.Keys.Source = SourceOutbound
	chains = append(chains, outbound)
	synthetic := feedbackTestChain(92, now.AddDate(0, 0, -10), "/defesa-margem", "reequilibrio")
	synthetic.Synthetic = true
	synthetic.Label = LabelSynthetic
	chains = append(chains, synthetic)

	report := ProjectAcquisitionOutcomeFeedback(chains, period, now, false)
	if report.SchemaVersion != OutcomeFeedbackSchemaV1 || report.CausalProof || report.RealEmpty {
		t.Fatalf("report=%+v", report)
	}
	if report.RouteAttributionAvailable != RouteAttributionPartial || report.Privacy.PIIExported {
		t.Fatalf("attribution/privacy=%+v", report)
	}
	if len(report.Rows) != 1 {
		t.Fatalf("rows=%d %+v", len(report.Rows), report.Rows)
	}
	row := report.Rows[0]
	if row.PrivacyUnits == nil || *row.PrivacyUnits != 5 || row.JoinStatus != FeedbackJoinPartial {
		t.Fatalf("row=%+v", row)
	}
	assertFeedbackMetric(t, "receipts", row.Receipts, 5, 0, FeedbackStatusObserved)
	assertFeedbackMetric(t, "leads", row.Leads, 5, 0, FeedbackStatusObserved)
	assertWithheldFeedbackMetric(t, "qco", row.QualifiedOpportunities)
	assertWithheldFeedbackMetric(t, "proposals", row.Proposals)
	if row.Contracts.Observed != nil || row.Contracts.Unknown == nil || *row.Contracts.Unknown != 5 || row.Contracts.Status != FeedbackStatusUnjoinable {
		t.Fatalf("contracts=%+v", row.Contracts)
	}
	if row.Outcomes.Won != nil || row.Outcomes.Lost != nil || row.Outcomes.Unknown != nil || row.Outcomes.Status != FeedbackStatusWithheld {
		t.Fatalf("outcomes=%+v", row.Outcomes)
	}
	if len(row.KnownValue.ByCurrency) != 0 ||
		row.KnownValue.KnownRecords != nil || row.KnownValue.UnknownRecords != nil || row.KnownValue.Status != FeedbackStatusWithheld {
		t.Fatalf("value=%+v", row.KnownValue)
	}
	if !report.Privacy.SensitiveMetricsWithheld {
		t.Fatal("sensitive small metrics were withheld without a privacy signal")
	}
	if row.KnownMargin.KnownCents != nil || row.KnownMargin.Status != FeedbackStatusUnknown {
		t.Fatalf("margin=%+v", row.KnownMargin)
	}
	if !feedbackHasGap(report.JoinGaps, "buyer_job", FeedbackStatusUnjoinable) ||
		!feedbackHasGap(report.JoinGaps, "proposal", FeedbackStatusPartial) ||
		!feedbackHasGap(report.JoinGaps, "contract", FeedbackStatusUnjoinable) ||
		!feedbackHasGap(report.JoinGaps, "margin", FeedbackStatusUnknown) {
		t.Fatalf("gaps=%+v", report.JoinGaps)
	}
}

func TestAcquisitionOutcomeFeedbackReleasesSensitiveMetricsAtFiveContributors(t *testing.T) {
	now := time.Date(2026, 8, 31, 18, 0, 0, 0, time.UTC)
	period := OutcomeFeedbackPeriod{From: now.AddDate(0, 0, -30), To: now}
	var chains []Chain
	for i := 0; i < 10; i++ {
		chain := feedbackTestChain(i, now.AddDate(0, 0, -10), "/defesa-margem", "reequilibrio")
		if i < 5 {
			chain.Qualified = true
			chain.OutcomeType = OutcomeQualifiedConversation
			chain.OpportunityID = fmt.Sprintf("opportunity-%d", i)
			chain.Keys.OpportunityID = chain.OpportunityID
		} else {
			chain.ProposalID = fmt.Sprintf("proposal-%d", i)
			chain.Keys.ProposalID = chain.ProposalID
			chain.HumanConfirmed = true
			chain.OutcomeType = OutcomeWon
			chain.Commercial.Payment.ContractedCents = 100_000
			chain.Commercial.Payment.ReceivedCents = 50_000
		}
		chains = append(chains, chain)
	}
	duplicatePayment := feedbackTestChain(99, now.AddDate(0, 0, -10), "/defesa-margem", "reequilibrio")
	duplicatePayment.AccountID = "account-5"
	duplicatePayment.Keys.AccountID = "account-5"
	duplicatePayment.PaymentID = "another-payment-for-account-5"
	duplicatePayment.Keys.PaymentID = duplicatePayment.PaymentID
	duplicatePayment.Commercial.Payment.ContractedCents = 100_000
	chains = append(chains, duplicatePayment)
	report := ProjectAcquisitionOutcomeFeedback(chains, period, now, false)
	if len(report.Rows) != 1 {
		t.Fatalf("rows=%+v", report.Rows)
	}
	row := report.Rows[0]
	assertFeedbackMetric(t, "qco", row.QualifiedOpportunities, 5, 5, FeedbackStatusPartial)
	assertFeedbackMetric(t, "proposals", row.Proposals, 5, 5, FeedbackStatusPartial)
	if row.Outcomes.Won == nil || *row.Outcomes.Won != 5 || row.Outcomes.Status != FeedbackStatusPartial {
		t.Fatalf("outcomes=%+v", row.Outcomes)
	}
	value := knownFeedbackCurrency(t, row.KnownValue, "BRL")
	if value.ContractedCents == nil || *value.ContractedCents != 500_000 ||
		value.ReceivedCents == nil || *value.ReceivedCents != 250_000 || row.KnownValue.Status != FeedbackStatusPartial {
		t.Fatalf("value=%+v", row.KnownValue)
	}
}

func TestAcquisitionOutcomeFeedbackSuppressesContractedAndReceivedContributorsIndependently(t *testing.T) {
	now := time.Date(2026, 8, 31, 18, 0, 0, 0, time.UTC)
	period := OutcomeFeedbackPeriod{From: now.AddDate(0, 0, -30), To: now}
	for _, asymmetric := range []string{"received", "contracted"} {
		t.Run(asymmetric, func(t *testing.T) {
			var chains []Chain
			for i := 0; i < 5; i++ {
				chain := feedbackTestChain(i, now.AddDate(0, 0, -10), "/defesa-margem", "reequilibrio")
				if asymmetric == "received" || i == 0 {
					chain.Commercial.Payment.ContractedCents = 100_000
				}
				if asymmetric == "contracted" || i == 0 {
					chain.Commercial.Payment.ReceivedCents = 50_000
				}
				chains = append(chains, chain)
			}
			report := ProjectAcquisitionOutcomeFeedback(chains, period, now, false)
			if len(report.Rows) != 1 || report.Rows[0].KnownValue.Status != FeedbackStatusPartial {
				t.Fatalf("asymmetric value status=%+v", report.Rows)
			}
			knownValue := report.Rows[0].KnownValue
			value := knownFeedbackCurrency(t, knownValue, "BRL")
			if asymmetric == "received" {
				if value.ContractedCents == nil || *value.ContractedCents != 500_000 ||
					value.ContractedStatus != FeedbackStatusObserved || value.ReceivedCents != nil ||
					value.ReceivedStatus != FeedbackStatusWithheld {
					t.Fatalf("safe contracted stage was not independent: %+v", knownValue)
				}
			} else if value.ReceivedCents == nil || *value.ReceivedCents != 250_000 ||
				value.ReceivedStatus != FeedbackStatusObserved || value.ContractedCents != nil ||
				value.ContractedStatus != FeedbackStatusWithheld {
				t.Fatalf("safe received stage was not independent: %+v", knownValue)
			}
		})
	}
}

func TestAcquisitionOutcomeFeedbackValueThresholdCountsContributorsNotPayments(t *testing.T) {
	now := time.Date(2026, 8, 31, 18, 0, 0, 0, time.UTC)
	period := OutcomeFeedbackPeriod{From: now.AddDate(0, 0, -30), To: now}
	var chains []Chain
	for i := 0; i < 5; i++ {
		chains = append(chains, feedbackTestChain(i, now.AddDate(0, 0, -10), "/defesa-margem", "reequilibrio"))
	}
	for i := 0; i < 5; i++ {
		payment := feedbackTestChain(i+20, now.AddDate(0, 0, -10), "/defesa-margem", "reequilibrio")
		payment.AccountID = "account-0"
		payment.Keys.AccountID = "account-0"
		payment.PaymentID = fmt.Sprintf("payment-%d", i)
		payment.Keys.PaymentID = payment.PaymentID
		payment.Commercial.Payment.ContractedCents = 100_000
		chains = append(chains, payment)
	}
	report := ProjectAcquisitionOutcomeFeedback(chains, period, now, false)
	if len(report.Rows) != 1 || report.Rows[0].KnownValue.Status != FeedbackStatusWithheld ||
		len(report.Rows[0].KnownValue.ByCurrency) != 0 {
		t.Fatalf("one contributor unsuppressed value through multiple payments: %+v", report.Rows)
	}
}

func TestAcquisitionOutcomeFeedbackSeparatesKnownValueCurrencyAndPrivacy(t *testing.T) {
	now := time.Date(2026, 8, 31, 18, 0, 0, 0, time.UTC)
	period := OutcomeFeedbackPeriod{From: now.AddDate(0, 0, -30), To: now}
	var chains []Chain
	for i := 0; i < 10; i++ {
		chain := feedbackTestChain(i, now.AddDate(0, 0, -10), "/defesa-margem", "reequilibrio")
		chain.Commercial.Payment.ContractedCents = 100_000
		chain.Commercial.Payment.ReceivedCents = 50_000
		if i < 5 {
			chain.Commercial.Offer.Currency = "BRL"
		} else {
			chain.Commercial.Offer.Currency = "USD"
		}
		chains = append(chains, chain)
	}
	report := ProjectAcquisitionOutcomeFeedback(chains, period, now, false)
	if len(report.Rows) != 1 || report.Rows[0].KnownValue.Status != FeedbackStatusObserved ||
		len(report.Rows[0].KnownValue.ByCurrency) != 2 {
		t.Fatalf("mixed currency value=%+v", report.Rows)
	}
	for _, currency := range []string{"BRL", "USD"} {
		value := knownFeedbackCurrency(t, report.Rows[0].KnownValue, currency)
		if value.ContractedCents == nil || *value.ContractedCents != 500_000 ||
			value.ReceivedCents == nil || *value.ReceivedCents != 250_000 {
			t.Fatalf("%s value=%+v", currency, value)
		}
	}

	chains = nil
	for i := 0; i < 5; i++ {
		chain := feedbackTestChain(i, now.AddDate(0, 0, -10), "/defesa-margem", "reequilibrio")
		chain.Commercial.Payment.ContractedCents = 100_000
		if i < 4 {
			chain.Commercial.Offer.Currency = "BRL"
		} else {
			chain.Commercial.Offer.Currency = "USD"
		}
		chains = append(chains, chain)
	}
	report = ProjectAcquisitionOutcomeFeedback(chains, period, now, false)
	if report.Rows[0].KnownValue.Status != FeedbackStatusWithheld ||
		len(report.Rows[0].KnownValue.ByCurrency) != 0 || !report.Rows[0].KnownValue.WithheldSmallCell {
		t.Fatalf("currencies pooled to satisfy k=5: %+v", report.Rows[0].KnownValue)
	}
}

func TestAcquisitionOutcomeFeedbackValueOverflowFailsClosed(t *testing.T) {
	now := time.Date(2026, 8, 31, 18, 0, 0, 0, time.UTC)
	period := OutcomeFeedbackPeriod{From: now.AddDate(0, 0, -30), To: now}
	const maxInt64 = int64(^uint64(0) >> 1)
	var chains []Chain
	var proposals []NativeProposalFact
	for i := 0; i < 5; i++ {
		chain := feedbackTestChain(i, now.AddDate(0, 0, -5), "/same-route", "same-intent")
		chain.CorrelationID = fmt.Sprintf("corr-native-%d", i)
		chain.Keys.CorrelationID = chain.CorrelationID
		chain.OpportunityID = fmt.Sprintf("opportunity-native-%d", i)
		chain.Keys.OpportunityID = chain.OpportunityID
		chain.Commercial.Offer.Currency = "BRL"
		chain.PaymentID = fmt.Sprintf("payment-overflow-%d", i)
		chain.Keys.PaymentID = chain.PaymentID
		chain.Commercial.Payment.ContractedCents = 1
		chain.Commercial.Payment.ReceivedCents = 1
		if i == 0 {
			chain.Commercial.Payment.ContractedCents = maxInt64
			chain.Commercial.Payment.ReceivedCents = maxInt64
		}
		chains = append(chains, chain)
		proposal := feedbackNativeProposal(i, 1, now.AddDate(0, 0, -2))
		proposal.AmountMinor = 1
		if i == 0 {
			proposal.AmountMinor = maxInt64
		}
		proposals = append(proposals, proposal)
	}
	report := ProjectAcquisitionOutcomeFeedbackWithNativeProposals(chains, proposals, period, now, false)
	if len(report.Rows) != 1 {
		t.Fatalf("rows=%+v", report.Rows)
	}
	row := report.Rows[0]
	if row.ProposedValue.Status != FeedbackStatusUnknown || len(row.ProposedValue.ByCurrency) != 0 ||
		row.ProposedValue.KnownRecords != nil || row.KnownValue.Status != FeedbackStatusUnknown ||
		len(row.KnownValue.ByCurrency) != 0 || row.KnownValue.KnownRecords != nil {
		t.Fatalf("overflow was published: proposed=%+v known=%+v", row.ProposedValue, row.KnownValue)
	}
}

func TestAcquisitionOutcomeFeedbackHandlesReconciledUnknownIDsAndDoesNotPromoteValidatedLeadToQCO(t *testing.T) {
	now := time.Date(2026, 8, 31, 18, 0, 0, 0, time.UTC)
	period := OutcomeFeedbackPeriod{From: now.AddDate(0, 0, -30), To: now}
	store := NewMemoryStore()
	for i := 0; i < 5; i++ {
		facts := ObservedFacts{
			Keys: JoinKeys{
				OrganizationID: "org-reconciled", Source: ProducerCONFENGEWeb,
				LeadID: fmt.Sprintf("lead-reconciled-%d", i), ReceiptID: fmt.Sprintf("receipt-reconciled-%d", i),
				AccountID: fmt.Sprintf("account-reconciled-%d", i), RouteFamily: FamilyInbound,
				OpportunityID: fmt.Sprintf("opportunity-validated-%d", i),
				LandingPath:   "/defesa-margem", AssetID: "defesa-margem", CTAID: "diagnostico",
				IntentClass: "reequilibrio", OrganicSource: SourceOrganicSearch,
			},
			LeadCreatedAt: now.AddDate(0, 0, -5), Qualified: true, Label: LabelReal,
		}
		result := Reconcile(store, facts)
		if result.Chain.Identity == "" {
			t.Fatalf("reconcile %d=%+v", i, result)
		}
		// A bare Qualified flag is also used by lead_validated and is not a QCO.
		// Add one explicit QCO after an action so it is not an orphan.
		if i == 0 {
			qco := facts
			qco.Keys.ActionID = "action-reconciled-0"
			qco.OutcomeType = OutcomeQualifiedConversation
			qco.OutcomeOccurredAt = now.AddDate(0, 0, -4)
			result = Reconcile(store, qco)
			if result.Chain.Identity == "" {
				t.Fatalf("qco reconcile=%+v", result)
			}
		}
	}
	chains, err := store.ListChains("org-reconciled")
	if err != nil {
		t.Fatal(err)
	}
	if chains[0].ProposalID != Unknown || chains[0].PersonID != Unknown || chains[0].PaymentID != Unknown {
		t.Fatalf("test must exercise production UNKNOWN sentinels: %+v", chains[0])
	}
	report := ProjectAcquisitionOutcomeFeedback(chains, period, now, false)
	if len(report.Rows) != 1 || report.Rows[0].Cohort != FeedbackCohortDirect {
		t.Fatalf("rows=%+v", report.Rows)
	}
	row := report.Rows[0]
	if got := countFeedbackUnits(chains, feedbackHasQCO, feedbackQCOUnit); got != 1 {
		t.Fatalf("qualified lead flags promoted to QCOs: got=%d want=1", got)
	}
	assertWithheldFeedbackMetric(t, "explicit qco only", row.QualifiedOpportunities)
	if row.Proposals.Observed != nil || row.Proposals.Unknown == nil || *row.Proposals.Unknown != 5 || row.Proposals.Status != FeedbackStatusUnknown {
		t.Fatalf("UNKNOWN proposal IDs became proposals: %+v", row.Proposals)
	}
}

func TestAcquisitionOutcomeFeedbackRollsUpSmallCells(t *testing.T) {
	now := time.Date(2026, 8, 31, 18, 0, 0, 0, time.UTC)
	period := OutcomeFeedbackPeriod{From: now.AddDate(0, 0, -30), To: now}
	var chains []Chain
	for i := 0; i < 3; i++ {
		chains = append(chains, feedbackTestChain(i, now.AddDate(0, 0, -3), "/route-a", "intent-a"))
		second := feedbackTestChain(i+10, now.AddDate(0, 0, -3), "/route-b", "intent-b")
		second.AssetFamily = AssetFamilyMarketAnswer
		second.Keys.AssetFamily = AssetFamilyMarketAnswer
		chains = append(chains, second)
	}
	report := ProjectAcquisitionOutcomeFeedback(chains, period, now, false)
	if !report.Privacy.SuppressionApplied || report.Privacy.RolledUpSourceCells != 2 || report.Privacy.WithheldRollup {
		t.Fatalf("privacy=%+v", report.Privacy)
	}
	if len(report.Rows) != 1 {
		t.Fatalf("rows=%d", len(report.Rows))
	}
	row := report.Rows[0]
	if row.Cohort != FeedbackCohortRollup || !row.Suppressed || row.AcquisitionRoute != Unknown || row.IntentClass != Unknown {
		t.Fatalf("rollup=%+v", row)
	}
	assertFeedbackMetric(t, "rolled receipts", row.Receipts, 6, 0, FeedbackStatusObserved)
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "route-a") || strings.Contains(string(raw), "intent-b") {
		t.Fatalf("small-cell dimensions leaked: %s", raw)
	}
}

func TestAcquisitionOutcomeFeedbackRejectsSlugShapedPIIInLargeCell(t *testing.T) {
	now := time.Date(2026, 8, 31, 18, 0, 0, 0, time.UTC)
	period := OutcomeFeedbackPeriod{From: now.AddDate(0, 0, -30), To: now}
	var chains []Chain
	for i := 0; i < 5; i++ {
		chain := feedbackTestChain(i, now.AddDate(0, 0, -2), "/ana-da-silva", "ana-silva")
		chain.AssetID = "ana-silva"
		chain.Keys.AssetID = "ana-silva"
		chain.CTAID = "fale-com-ana"
		chain.Keys.CTAID = "fale-com-ana"
		chains = append(chains, chain)
	}
	report := ProjectAcquisitionOutcomeFeedback(chains, period, now, false)
	if report.Privacy.WithheldRollup || len(report.Rows) != 1 {
		t.Fatalf("report=%+v", report)
	}
	row := report.Rows[0]
	if row.Cohort != FeedbackCohortDirect || row.PrivacyUnits == nil || *row.PrivacyUnits != 5 ||
		row.AcquisitionSource != ProducerCONFENGEWeb || row.AcquisitionRoute != Unknown || row.AssetID != Unknown ||
		row.CTAID != Unknown || row.IntentClass != Unknown {
		t.Fatalf("unsafe dimensions survived sanitization=%+v", row)
	}
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if ReportHasPII(raw) {
		t.Fatalf("PII detector rejected report: %s", raw)
	}
	for _, forbidden := range []string{
		"ana-silva", "ana-da-silva", "fale-com-ana", "receipt-0", "lead-0",
	} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("PII/identity %q leaked: %s", forbidden, raw)
		}
	}
}

func TestAcquisitionOutcomeFeedbackDoesNotTreatReceiptIDsAsDistinctPeople(t *testing.T) {
	now := time.Date(2026, 8, 31, 18, 0, 0, 0, time.UTC)
	period := OutcomeFeedbackPeriod{From: now.AddDate(0, 0, -30), To: now}
	var chains []Chain
	for i := 0; i < 6; i++ {
		chain := feedbackTestChain(i, now.AddDate(0, 0, -2), "/same-route", "same-intent")
		chain.AccountID = ""
		chain.Keys.AccountID = ""
		chains = append(chains, chain)
	}
	report := ProjectAcquisitionOutcomeFeedback(chains, period, now, false)
	if !report.Privacy.WithheldRollup || len(report.Rows) != 1 || report.Rows[0].Cohort != FeedbackCohortWithheld {
		t.Fatalf("receipt-only identities escaped suppression: %+v", report)
	}
}

func TestAcquisitionOutcomeFeedbackDoesNotCountUnjoinableAccountBesideFourKnownAccounts(t *testing.T) {
	now := time.Date(2026, 8, 31, 18, 0, 0, 0, time.UTC)
	period := OutcomeFeedbackPeriod{From: now.AddDate(0, 0, -30), To: now}
	var chains []Chain
	for i := 0; i < 5; i++ {
		chain := feedbackTestChain(i, now.AddDate(0, 0, -2), "/same-route", "same-intent")
		if i == 4 {
			chain.AccountID = ""
			chain.Keys.AccountID = ""
			chain.OpportunityID = "opportunity-unjoinable"
			chain.Keys.OpportunityID = chain.OpportunityID
			chain.OutcomeType = OutcomeQualifiedConversation
		}
		chains = append(chains, chain)
	}
	report := ProjectAcquisitionOutcomeFeedback(chains, period, now, false)
	if !report.Privacy.WithheldRollup || len(report.Rows) != 1 || report.Rows[0].PrivacyUnits != nil {
		t.Fatalf("unjoinable account satisfied k=5: %+v", report)
	}
}

func TestAcquisitionOutcomeFeedbackUsesAccountOnlyForPrivacyUnits(t *testing.T) {
	now := time.Date(2026, 8, 31, 18, 0, 0, 0, time.UTC)
	period := OutcomeFeedbackPeriod{From: now.AddDate(0, 0, -30), To: now}
	accounts := []string{"account-a", "account-a", "account-b", "account-b", "account-c"}
	var chains []Chain
	for i, accountID := range accounts {
		chain := feedbackTestChain(i, now.AddDate(0, 0, -2), "/same-route", "same-intent")
		chain.AccountID = accountID
		chain.Keys.AccountID = accountID
		if i%2 == 0 {
			chain.PersonID = fmt.Sprintf("person-%d", i)
			chain.Keys.PersonID = chain.PersonID
		}
		chains = append(chains, chain)
	}
	report := ProjectAcquisitionOutcomeFeedback(chains, period, now, false)
	if !report.Privacy.WithheldRollup || len(report.Rows) != 1 || report.Rows[0].PrivacyUnits != nil {
		t.Fatalf("mixed person/account IDs inflated privacy count: %+v", report)
	}
}

func TestAcquisitionOutcomeFeedbackFailsClosedOnSyntheticRecordKindMismatch(t *testing.T) {
	now := time.Date(2026, 8, 31, 18, 0, 0, 0, time.UTC)
	period := OutcomeFeedbackPeriod{From: now.AddDate(0, 0, -30), To: now}
	var chains []Chain
	for i := 0; i < 5; i++ {
		chain := feedbackTestChain(i, now.AddDate(0, 0, -2), "/same-route", "same-intent")
		if i == 0 {
			chain.Keys.RecordKind = RecordKindSynthetic
		}
		chains = append(chains, chain)
	}
	report := ProjectAcquisitionOutcomeFeedback(chains, period, now, false)
	if !report.Privacy.WithheldRollup || len(report.Rows) != 1 || report.Rows[0].RecordKind != RecordKindReal {
		t.Fatalf("synthetic record-kind mismatch entered real threshold: %+v", report)
	}
}

func TestAcquisitionOutcomeFeedbackNeverUsesSyntheticSubjectsToUnsuppressReal(t *testing.T) {
	now := time.Date(2026, 8, 31, 18, 0, 0, 0, time.UTC)
	period := OutcomeFeedbackPeriod{From: now.AddDate(0, 0, -30), To: now}
	var chains []Chain
	for i := 0; i < 5; i++ {
		chain := feedbackTestChain(i, now.AddDate(0, 0, -2), "/same-route", "same-intent")
		if i > 0 {
			chain.Synthetic = true
			chain.Label = LabelSynthetic
		}
		chains = append(chains, chain)
	}
	report := ProjectAcquisitionOutcomeFeedback(chains, period, now, true)
	if len(report.Rows) != 2 || !report.Privacy.WithheldRollup {
		t.Fatalf("real and synthetic subjects mixed: %+v", report)
	}
	for _, row := range report.Rows {
		if row.Cohort != FeedbackCohortWithheld || row.PrivacyUnits != nil {
			t.Fatalf("mixed threshold escaped: %+v", row)
		}
	}
}

func TestAcquisitionOutcomeFeedbackJoinsNativeProposalsAndDeduplicatesVersions(t *testing.T) {
	now := time.Date(2026, 8, 31, 18, 0, 0, 0, time.UTC)
	period := OutcomeFeedbackPeriod{From: now.AddDate(0, 0, -30), To: now}
	var chains []Chain
	var proposals []NativeProposalFact
	for i := 0; i < 5; i++ {
		chain := feedbackTestChain(i, now.AddDate(0, 0, -5), "/same-route", "same-intent")
		chain.CorrelationID = fmt.Sprintf("corr-native-%d", i)
		chain.Keys.CorrelationID = chain.CorrelationID
		chain.OpportunityID = fmt.Sprintf("opportunity-native-%d", i)
		chain.Keys.OpportunityID = chain.OpportunityID
		chain.OutcomeType = OutcomeQualifiedConversation
		if i == 0 {
			chain.ProposalID = "proposal-native-0"
			chain.Keys.ProposalID = chain.ProposalID
		}
		chains = append(chains, chain)
		first := feedbackNativeProposal(i, 1, now.AddDate(0, 0, -3))
		first.AmountMinor = 100_000
		second := feedbackNativeProposal(i, 2, now.AddDate(0, 0, -2))
		second.DecisionState = "ACCEPTED"
		second.AcceptedSnapshotHash = "sha256:" + strings.Repeat("a", 64)
		second.AmountMinor = 120_000
		proposals = append(proposals, second, first)
	}
	report := ProjectAcquisitionOutcomeFeedbackWithNativeProposals(chains, proposals, period, now, false)
	if len(report.Rows) != 1 {
		t.Fatalf("rows=%+v", report.Rows)
	}
	row := report.Rows[0]
	assertFeedbackMetric(t, "native proposals", row.Proposals, 5, 0, FeedbackStatusObserved)
	if row.ProposalStates.Status != FeedbackStatusObserved || len(row.ProposalStates.States) != 1 ||
		row.ProposalStates.States[0].State != "ACCEPTED" || row.ProposalStates.States[0].Observed != 5 ||
		row.ProposalStates.KnownRecords == nil || *row.ProposalStates.KnownRecords != 5 ||
		row.ProposalStates.UnknownRecords == nil || *row.ProposalStates.UnknownRecords != 0 ||
		row.ProposalStates.WithheldSmallCell {
		t.Fatalf("proposal states=%+v", row.ProposalStates)
	}
	if row.ProposedValue.Status != FeedbackStatusObserved || len(row.ProposedValue.ByCurrency) != 1 ||
		row.ProposedValue.ByCurrency[0].Currency != "BRL" || row.ProposedValue.ByCurrency[0].AmountMinor != 600_000 ||
		row.ProposedValue.ByCurrency[0].ProposalCount != 5 || row.ProposedValue.KnownRecords == nil ||
		*row.ProposedValue.KnownRecords != 5 || row.ProposedValue.UnknownRecords == nil ||
		*row.ProposedValue.UnknownRecords != 0 {
		t.Fatalf("proposed value=%+v", row.ProposedValue)
	}
	if row.Contracts.Status != FeedbackStatusUnjoinable || len(row.KnownValue.ByCurrency) != 0 ||
		row.KnownMargin.Status != FeedbackStatusUnknown {
		t.Fatalf("proposal was promoted into contract/value/margin: %+v", row)
	}
}

func TestAcquisitionOutcomeFeedbackNativeProposalJoinFailsClosed(t *testing.T) {
	now := time.Date(2026, 8, 31, 18, 0, 0, 0, time.UTC)
	base := feedbackTestChain(0, now.AddDate(0, 0, -5), "/same-route", "same-intent")
	base.CorrelationID = "corr-native-0"
	base.Keys.CorrelationID = base.CorrelationID
	base.OpportunityID = "opportunity-native-0"
	base.Keys.OpportunityID = base.OpportunityID
	base.OutcomeType = OutcomeQualifiedConversation
	valid := feedbackNativeProposal(0, 1, now.AddDate(0, 0, -2))
	if _, ok := matchNativeProposalChain(valid, []Chain{base}); !ok {
		t.Fatal("exact opaque proposal did not join")
	}
	checks := map[string]func(*NativeProposalFact, *[]Chain){
		"unsafe correlation":         func(f *NativeProposalFact, _ *[]Chain) { f.CorrelationID = "email=a@example.test" },
		"wrong account":              func(f *NativeProposalFact, _ *[]Chain) { f.AccountID = "account-other" },
		"wrong opportunity":          func(f *NativeProposalFact, _ *[]Chain) { f.OpportunityID = "opportunity-other" },
		"wrong source lead":          func(f *NativeProposalFact, _ *[]Chain) { f.SourceLeadID = "lead-other" },
		"receipt is not source lead": func(f *NativeProposalFact, _ *[]Chain) { f.SourceLeadID = "receipt-0" },
		"synthetic mismatch":         func(f *NativeProposalFact, _ *[]Chain) { f.Synthetic = true },
		"draft": func(f *NativeProposalFact, _ *[]Chain) {
			f.DecisionState = "DRAFT"
			f.SentAt = nil
		},
		"held chain": func(_ *NativeProposalFact, chains *[]Chain) { (*chains)[0].Held = true },
		"ambiguous correlation": func(_ *NativeProposalFact, chains *[]Chain) {
			duplicate := (*chains)[0]
			duplicate.Identity = "another-chain"
			*chains = append(*chains, duplicate)
		},
	}
	for name, mutate := range checks {
		t.Run(name, func(t *testing.T) {
			fact := valid
			chains := []Chain{base}
			mutate(&fact, &chains)
			facts := latestEmittedNativeProposals([]NativeProposalFact{fact})
			if len(facts) == 0 {
				return
			}
			if _, ok := matchNativeProposalChain(facts[0], chains); ok {
				t.Fatalf("unsafe/ambiguous fact joined: %+v", fact)
			}
		})
	}
	other := valid
	other.ProposalID = "proposal-native-other"
	if facts := latestEmittedNativeProposals([]NativeProposalFact{valid, other}); len(facts) != 0 {
		t.Fatalf("multiple proposal IDs shared one canonical correlation: %+v", facts)
	}
}

func TestAcquisitionOutcomeFeedbackSuppressesNativeProposalSubstagesBelowFive(t *testing.T) {
	now := time.Date(2026, 8, 31, 18, 0, 0, 0, time.UTC)
	period := OutcomeFeedbackPeriod{From: now.AddDate(0, 0, -30), To: now}
	var chains []Chain
	var proposals []NativeProposalFact
	for i := 0; i < 5; i++ {
		chain := feedbackTestChain(i, now.AddDate(0, 0, -5), "/same-route", "same-intent")
		chain.CorrelationID = fmt.Sprintf("corr-native-%d", i)
		chain.Keys.CorrelationID = chain.CorrelationID
		chain.OpportunityID = fmt.Sprintf("opportunity-native-%d", i)
		chain.Keys.OpportunityID = chain.OpportunityID
		chains = append(chains, chain)
		if i < 4 {
			proposals = append(proposals, feedbackNativeProposal(i, 1, now.AddDate(0, 0, -2)))
		}
	}
	report := ProjectAcquisitionOutcomeFeedbackWithNativeProposals(chains, proposals, period, now, false)
	if len(report.Rows) != 1 {
		t.Fatalf("rows=%+v", report.Rows)
	}
	row := report.Rows[0]
	if row.Proposals.Status != FeedbackStatusWithheld || row.Proposals.Observed != nil || row.Proposals.Unknown != nil ||
		row.ProposalStates.Status != FeedbackStatusWithheld || !row.ProposalStates.WithheldSmallCell ||
		len(row.ProposalStates.States) != 0 || row.ProposedValue.Status != FeedbackStatusWithheld ||
		!row.ProposedValue.WithheldSmallCell || len(row.ProposedValue.ByCurrency) != 0 ||
		!report.Privacy.SensitiveMetricsWithheld {
		t.Fatalf("native proposal small cell leaked: %+v privacy=%+v", row, report.Privacy)
	}
}

func TestAcquisitionOutcomeFeedbackExcludesHeldEconomicFacts(t *testing.T) {
	now := time.Date(2026, 8, 31, 18, 0, 0, 0, time.UTC)
	period := OutcomeFeedbackPeriod{From: now.AddDate(0, 0, -30), To: now}
	var chains []Chain
	var proposals []NativeProposalFact
	for i := 0; i < 5; i++ {
		chain := feedbackTestChain(i, now.AddDate(0, 0, -5), "/same-route", "same-intent")
		chain.CorrelationID = fmt.Sprintf("corr-native-%d", i)
		chain.Keys.CorrelationID = chain.CorrelationID
		chain.OpportunityID = fmt.Sprintf("opportunity-native-%d", i)
		chain.Keys.OpportunityID = chain.OpportunityID
		chain.OutcomeType = OutcomeWon
		chain.HumanConfirmed = true
		chain.ProposalID = fmt.Sprintf("chain-proposal-%d", i)
		chain.Keys.ProposalID = chain.ProposalID
		chain.Commercial.Payment.ContractedCents = 100_000
		chain.Commercial.Payment.ReceivedCents = 50_000
		chain.Held = true
		chains = append(chains, chain)
		proposals = append(proposals, feedbackNativeProposal(i, 1, now.AddDate(0, 0, -2)))
	}
	report := ProjectAcquisitionOutcomeFeedbackWithNativeProposals(chains, proposals, period, now, false)
	if len(report.Rows) != 1 {
		t.Fatalf("rows=%+v", report.Rows)
	}
	row := report.Rows[0]
	if row.QualifiedOpportunities.Observed != nil || row.Proposals.Observed != nil ||
		row.Outcomes.Won != nil || row.Outcomes.Lost != nil || len(row.KnownValue.ByCurrency) != 0 ||
		len(row.ProposedValue.ByCurrency) != 0 {
		t.Fatalf("held economic fact escaped: %+v", row)
	}
}

func TestAcquisitionOutcomeFeedbackRequiresConfengeWebInboundIntersection(t *testing.T) {
	now := time.Date(2026, 8, 31, 18, 0, 0, 0, time.UTC)
	period := OutcomeFeedbackPeriod{From: now.AddDate(0, 0, -30), To: now}
	var chains []Chain
	for i := 0; i < 5; i++ {
		chains = append(chains, feedbackTestChain(i, now.AddDate(0, 0, -2), "/same-route", "same-intent"))
	}
	outbound := feedbackTestChain(20, now.AddDate(0, 0, -2), "/same-route", "same-intent")
	outbound.RouteFamily = FamilyOutbound
	outbound.Keys.RouteFamily = FamilyOutbound
	chains = append(chains, outbound)
	unknownSource := feedbackTestChain(21, now.AddDate(0, 0, -2), "/same-route", "same-intent")
	unknownSource.Source = Unknown
	unknownSource.Keys.Source = Unknown
	chains = append(chains, unknownSource)
	report := ProjectAcquisitionOutcomeFeedback(chains, period, now, false)
	if len(report.Rows) != 1 || report.Rows[0].PrivacyUnits == nil || *report.Rows[0].PrivacyUnits != 5 {
		t.Fatalf("non-CONFENGE_WEB/outbound chain contaminated feedback: %+v", report)
	}
}

func TestAcquisitionOutcomeFeedbackUsesAccountContributorsForUnknown(t *testing.T) {
	now := time.Date(2026, 8, 31, 18, 0, 0, 0, time.UTC)
	period := OutcomeFeedbackPeriod{From: now.AddDate(0, 0, -30), To: now}
	var chains []Chain
	for i := 0; i < 5; i++ {
		chain := feedbackTestChain(i, now.AddDate(0, 0, -2), "/same-route", "same-intent")
		chain.AccountID = "account-0"
		chain.Keys.AccountID = "account-0"
		chains = append(chains, chain)
	}
	for i := 1; i < 5; i++ {
		chain := feedbackTestChain(20+i, now.AddDate(0, 0, -2), "/same-route", "same-intent")
		chain.AccountID = fmt.Sprintf("account-%d", i)
		chain.Keys.AccountID = chain.AccountID
		chain.ReceiptID = Unknown
		chain.Keys.ReceiptID = Unknown
		chains = append(chains, chain)
	}
	report := ProjectAcquisitionOutcomeFeedback(chains, period, now, false)
	if len(report.Rows) != 1 || report.Rows[0].Receipts.Status != FeedbackStatusWithheld ||
		report.Rows[0].Receipts.Observed != nil || report.Rows[0].Receipts.Unknown != nil {
		t.Fatalf("receipt IDs substituted for account contributors: %+v", report.Rows)
	}

	chains = nil
	for i := 0; i < 10; i++ {
		chain := feedbackTestChain(i, now.AddDate(0, 0, -2), "/same-route", "same-intent")
		if i < 5 {
			for event := 0; event < 2; event++ {
				copy := chain
				copy.Identity = fmt.Sprintf("identity-outcome-%d-%d", i, event)
				copy.LeadID = fmt.Sprintf("lead-outcome-%d-%d", i, event)
				copy.Keys.LeadID = copy.LeadID
				copy.ReceiptID = fmt.Sprintf("receipt-outcome-%d-%d", i, event)
				copy.Keys.ReceiptID = copy.ReceiptID
				copy.OutcomeID = fmt.Sprintf("outcome-%d-%d", i, event)
				copy.Keys.OutcomeID = copy.OutcomeID
				copy.OutcomeType = OutcomeWon
				copy.HumanConfirmed = true
				chains = append(chains, copy)
			}
			continue
		}
		chains = append(chains, chain)
	}
	report = ProjectAcquisitionOutcomeFeedback(chains, period, now, false)
	row := report.Rows[0]
	if row.Outcomes.Won == nil || *row.Outcomes.Won != 10 || row.Outcomes.Unknown == nil ||
		*row.Outcomes.Unknown != 5 || row.Outcomes.Status != FeedbackStatusPartial {
		t.Fatalf("outcome IDs substituted for account contributors: %+v", row.Outcomes)
	}
}

func TestAcquisitionOutcomeFeedbackSuppressesWonLostIndependently(t *testing.T) {
	now := time.Date(2026, 8, 31, 18, 0, 0, 0, time.UTC)
	period := OutcomeFeedbackPeriod{From: now.AddDate(0, 0, -30), To: now}
	var chains []Chain
	for i := 0; i < 10; i++ {
		chain := feedbackTestChain(i, now.AddDate(0, 0, -2), "/same-route", "same-intent")
		switch {
		case i < 5:
			chain.OutcomeType = OutcomeWon
			chain.HumanConfirmed = true
		case i == 5:
			chain.OutcomeType = OutcomeLost
			chain.HumanConfirmed = true
		}
		chains = append(chains, chain)
	}
	report := ProjectAcquisitionOutcomeFeedback(chains, period, now, false)
	states := report.Rows[0].Outcomes
	if states.Won == nil || *states.Won != 5 || states.WonStatus != FeedbackStatusPartial ||
		states.Lost != nil || states.LostStatus != FeedbackStatusWithheld || states.Unknown != nil ||
		states.UnknownStatus != FeedbackStatusWithheld || states.Status != FeedbackStatusPartial {
		t.Fatalf("won/lost suppression was not independent: %+v", states)
	}
}

func TestAcquisitionOutcomeFeedbackRealEmptyWithSyntheticOnly(t *testing.T) {
	now := time.Date(2026, 8, 31, 18, 0, 0, 0, time.UTC)
	period := OutcomeFeedbackPeriod{From: now.AddDate(0, 0, -30), To: now}
	var chains []Chain
	for i := 0; i < 5; i++ {
		chain := feedbackTestChain(i, now.AddDate(0, 0, -2), "/same-route", "same-intent")
		chain.Synthetic = true
		chain.Label = LabelSynthetic
		chains = append(chains, chain)
	}
	report := ProjectAcquisitionOutcomeFeedback(chains, period, now, true)
	if !report.RealEmpty || len(report.Rows) != 1 || report.Rows[0].RecordKind != RecordKindSynthetic {
		t.Fatalf("synthetic-only report claimed real evidence: %+v", report)
	}
}

func feedbackNativeProposal(i, version int, sentAt time.Time) NativeProposalFact {
	return NativeProposalFact{
		ProposalID: fmt.Sprintf("proposal-native-%d", i), ProposalVersion: version,
		AccountID: fmt.Sprintf("account-%d", i), OpportunityID: fmt.Sprintf("opportunity-native-%d", i),
		SourceLeadID: fmt.Sprintf("lead-%d", i), CorrelationID: fmt.Sprintf("corr-native-%d", i),
		DecisionState: "SENT", AmountMinor: 100_000, Currency: "BRL", SentAt: &sentAt,
	}
}

func feedbackTestChain(i int, at time.Time, route, intent string) Chain {
	receipt := fmt.Sprintf("receipt-%d", i)
	lead := fmt.Sprintf("lead-%d", i)
	return Chain{
		SchemaVersion: SchemaV1,
		Identity:      "identity-" + receipt,
		MetricKey:     "metric-" + receipt,
		Source:        ProducerCONFENGEWeb,
		LeadID:        lead,
		ReceiptID:     receipt,
		AccountID:     fmt.Sprintf("account-%d", i),
		RouteFamily:   FamilyInbound,
		AssetFamily:   AssetFamilyContractAnalysis,
		AssetID:       "defesa-margem",
		CTAID:         "diagnostico",
		IntentClass:   intent,
		LeadCreatedAt: at,
		CreatedAt:     at,
		Label:         LabelReal,
		Keys: JoinKeys{
			Source: ProducerCONFENGEWeb, LeadID: lead, ReceiptID: receipt, AccountID: fmt.Sprintf("account-%d", i),
			RouteFamily: FamilyInbound, LandingPath: route,
			AssetFamily: AssetFamilyContractAnalysis, AssetID: "defesa-margem", CTAID: "diagnostico",
			IntentClass: intent, OrganicSource: SourceOrganicSearch, RecordKind: RecordKindReal,
		},
	}
}

func assertFeedbackMetric(t *testing.T, name string, metric OutcomeFeedbackMetric, observed, unknown int, status string) {
	t.Helper()
	if metric.Observed == nil || *metric.Observed != observed || metric.Unknown == nil || *metric.Unknown != unknown || metric.Status != status {
		t.Fatalf("%s=%+v", name, metric)
	}
}

func assertWithheldFeedbackMetric(t *testing.T, name string, metric OutcomeFeedbackMetric) {
	t.Helper()
	if metric.Observed != nil || metric.Unknown != nil || metric.Status != FeedbackStatusWithheld {
		t.Fatalf("%s=%+v", name, metric)
	}
}

func knownFeedbackCurrency(t *testing.T, value OutcomeFeedbackValue, currency string) OutcomeFeedbackKnownCurrencyValue {
	t.Helper()
	for _, candidate := range value.ByCurrency {
		if candidate.Currency == currency {
			return candidate
		}
	}
	t.Fatalf("known value missing currency %s: %+v", currency, value)
	return OutcomeFeedbackKnownCurrencyValue{}
}

func feedbackHasGap(gaps []OutcomeFeedbackGap, field, status string) bool {
	for _, gap := range gaps {
		if gap.Field == field && gap.Status == status {
			return true
		}
	}
	return false
}
