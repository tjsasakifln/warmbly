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
	if row.KnownValue.ContractedCents != nil || row.KnownValue.ReceivedCents != nil ||
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
	if row.KnownValue.ContractedCents == nil || *row.KnownValue.ContractedCents != 500_000 ||
		row.KnownValue.ReceivedCents == nil || *row.KnownValue.ReceivedCents != 250_000 ||
		row.KnownValue.Status != FeedbackStatusPartial {
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
			if len(report.Rows) != 1 || report.Rows[0].KnownValue.Status != FeedbackStatusWithheld ||
				report.Rows[0].KnownValue.ContractedCents != nil || report.Rows[0].KnownValue.ReceivedCents != nil {
				t.Fatalf("asymmetric contributors leaked value: %+v", report.Rows)
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
		report.Rows[0].KnownValue.ContractedCents != nil {
		t.Fatalf("one contributor unsuppressed value through multiple payments: %+v", report.Rows)
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

func feedbackHasGap(gaps []OutcomeFeedbackGap, field, status string) bool {
	for _, gap := range gaps {
		if gap.Field == field && gap.Status == status {
			return true
		}
	}
	return false
}
