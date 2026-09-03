package confenge

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/app/confenge/intel"
	"github.com/warmbly/warmbly/internal/models"
)

// CONFENGE_WEB intake proofs.
//
// A: company_ref intake yields a company:* subscription.
// B: opportunity_id intake yields an opportunity:* subscription.
// I: a CNPJ or an li_ share token as a correlation key is refused.

func webIntentConsentAt() *time.Time {
	at := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	return &at
}

func validWebIntent(kind string) intel.WebIntentEnvelope {
	return intel.WebIntentEnvelope{
		Schema: intel.WebIntentSchemaV1, IntentKind: kind, Lane: EngineLaneConfengeWeb,
		CompanyRef: "acme-holdings", ContactEmail: "ana@example.com",
		ConsentSource: "web_form:confenge.com/monitor", ConsentAt: webIntentConsentAt(),
		ConsentProvenanceOK: true,
	}
}

// fakeWatchSubs records what CONFENGE_WEB intake wrote.
type fakeWatchSubs struct {
	saved []models.IntelWatchSubscription
}

func (f *fakeWatchSubs) CreateOrReactivateSubscription(_ context.Context, sub *models.IntelWatchSubscription) (*models.IntelWatchSubscription, error) {
	out := *sub
	out.ID = uuid.New()
	f.saved = append(f.saved, out)
	return &out, nil
}

func TestWebIntentSubjectKeyCompanyShape(t *testing.T) {
	key, xerr := WebIntentSubjectKey(intel.WebIntentMonitorCompany, "acme-holdings", "")
	if xerr != nil {
		t.Fatalf("valid company ref rejected: %v", xerr)
	}
	if key != "company:acme-holdings" {
		t.Fatalf("subject key = %q, want company:acme-holdings", key)
	}
}

func TestWebIntentSubjectKeyOpportunityShape(t *testing.T) {
	key, xerr := WebIntentSubjectKey(intel.WebIntentMonitorOpportunity, "", "pregao-2026-0042")
	if xerr != nil {
		t.Fatalf("valid opportunity id rejected: %v", xerr)
	}
	if key != "opportunity:pregao-2026-0042" {
		t.Fatalf("subject key = %q, want opportunity:pregao-2026-0042", key)
	}
}

// Proof I. A tax identity and a share token are both refused as correlation
// keys, on either field, for every intent kind.
func TestWebIntentRejectsCNPJAndShareTokenAsCorrelationKey(t *testing.T) {
	cases := []struct {
		name       string
		mutate     func(*intel.WebIntentEnvelope)
		wantReason string
	}{
		{"cnpj in company_ref", func(e *intel.WebIntentEnvelope) { e.CompanyRef = "11.222.333/0001-81" }, WebIntentReasonKeyIsCNPJ},
		{"bare cnpj digits in company_ref", func(e *intel.WebIntentEnvelope) { e.CompanyRef = "11222333000181" }, WebIntentReasonKeyIsCNPJ},
		{"share token in company_ref", func(e *intel.WebIntentEnvelope) { e.CompanyRef = "li_abc123" }, WebIntentReasonKeyIsShare},
		{"cnpj riding along on opportunity_id", func(e *intel.WebIntentEnvelope) { e.OpportunityID = "11222333000181" }, WebIntentReasonKeyIsCNPJ},
		{"share token riding along on opportunity_id", func(e *intel.WebIntentEnvelope) { e.OpportunityID = "li_zzz" }, WebIntentReasonKeyIsShare},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := validWebIntent(intel.WebIntentMonitorCompany)
			tc.mutate(&env)
			_, xerr := ValidateWebIntent(env)
			if xerr == nil {
				t.Fatalf("envelope accepted, want rejection %s", tc.wantReason)
			}
			if xerr.Identifier != tc.wantReason {
				t.Fatalf("rejection = %q, want %q", xerr.Identifier, tc.wantReason)
			}
		})
	}
}

func TestWebIntentRejectsMissingCorrelationKeyPerIntent(t *testing.T) {
	cases := []struct {
		kind       string
		wantReason string
	}{
		{intel.WebIntentMonitorCompany, WebIntentReasonCompanyMissing},
		{intel.WebIntentRequestDeepDive, WebIntentReasonCompanyMissing},
		{intel.WebIntentRequestHumanReview, WebIntentReasonCompanyMissing},
		{intel.WebIntentMonitorOpportunity, WebIntentReasonOpportunity},
	}
	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			env := validWebIntent(tc.kind)
			env.CompanyRef = ""
			env.OpportunityID = ""
			_, xerr := ValidateWebIntent(env)
			if xerr == nil || xerr.Identifier != tc.wantReason {
				t.Fatalf("rejection = %v, want %s", xerr, tc.wantReason)
			}
		})
	}
}

func TestWebIntentRejectsUnknownKindAndForeignLane(t *testing.T) {
	env := validWebIntent("MONITOR_EVERYTHING")
	if _, xerr := ValidateWebIntent(env); xerr == nil || xerr.Identifier != WebIntentReasonKindUnknown {
		t.Fatalf("unknown intent_kind accepted: %v", xerr)
	}
	env = validWebIntent(intel.WebIntentMonitorCompany)
	env.Lane = EngineLaneFirstTouch
	if _, xerr := ValidateWebIntent(env); xerr == nil || xerr.Identifier != WebIntentReasonLane {
		t.Fatalf("caller-chosen lane accepted: %v", xerr)
	}
	env = validWebIntent(intel.WebIntentMonitorCompany)
	env.ContactEmail = ""
	if _, xerr := ValidateWebIntent(env); xerr == nil || xerr.Identifier != WebIntentReasonEmailMissing {
		t.Fatalf("missing contact_email accepted: %v", xerr)
	}
}

// Consent provenance gates only the intents that put an address on a standing
// mail list. A REQUEST_* is answered by a human and needs none.
func TestWebIntentConsentRequiredOnlyForMonitorIntents(t *testing.T) {
	env := validWebIntent(intel.WebIntentMonitorCompany)
	env.ConsentProvenanceOK = false
	if _, xerr := ValidateWebIntent(env); xerr == nil || xerr.Identifier != WebIntentReasonConsent {
		t.Fatalf("monitor without consent accepted: %v", xerr)
	}
	env = validWebIntent(intel.WebIntentRequestDeepDive)
	env.ConsentSource, env.ConsentAt, env.ConsentProvenanceOK = "", nil, false
	if _, xerr := ValidateWebIntent(env); xerr != nil {
		t.Fatalf("deep dive without consent rejected: %v", xerr)
	}
}

// Proof A. A valid MONITOR_COMPANY intake creates a company:* subscription.
func TestWebIntentMonitorCompanyCreatesCompanySubscription(t *testing.T) {
	svc, orgID := newWebIntentService(t)
	subs := &fakeWatchSubs{}
	svc.WireIntelWatchSubscriptions(subs)

	res, xerr := svc.IngestWebIntent(context.Background(), orgID, validWebIntent(intel.WebIntentMonitorCompany), time.Now().UTC())
	if xerr != nil {
		t.Fatalf("intake rejected: %v", xerr)
	}
	if res.SubjectKey != "company:acme-holdings" {
		t.Fatalf("subject key = %q", res.SubjectKey)
	}
	if res.SubscriptionID == nil || res.ActionID != nil {
		t.Fatalf("monitor intake must create a subscription and no action: %+v", res)
	}
	if len(subs.saved) != 1 {
		t.Fatalf("saved %d subscriptions, want 1", len(subs.saved))
	}
	saved := subs.saved[0]
	if saved.SubjectKey != "company:acme-holdings" || saved.OrganizationID != orgID {
		t.Fatalf("subscription = %+v", saved)
	}
	if saved.IntentKind != models.IntelWatchIntentFitBecameRelevant {
		t.Fatalf("subscription intent kind = %q, want the closed-set default", saved.IntentKind)
	}
	if !saved.ConsentProvenanceOK || saved.ConsentAt == nil {
		t.Fatalf("consent provenance was not carried onto the subscription: %+v", saved)
	}
}

// Proof B. A valid MONITOR_OPPORTUNITY intake creates an opportunity:*
// subscription.
func TestWebIntentMonitorOpportunityCreatesOpportunitySubscription(t *testing.T) {
	svc, orgID := newWebIntentService(t)
	subs := &fakeWatchSubs{}
	svc.WireIntelWatchSubscriptions(subs)

	env := validWebIntent(intel.WebIntentMonitorOpportunity)
	env.CompanyRef = ""
	env.OpportunityID = "pregao-2026-0042"
	res, xerr := svc.IngestWebIntent(context.Background(), orgID, env, time.Now().UTC())
	if xerr != nil {
		t.Fatalf("intake rejected: %v", xerr)
	}
	if res.SubjectKey != "opportunity:pregao-2026-0042" {
		t.Fatalf("subject key = %q", res.SubjectKey)
	}
	if len(subs.saved) != 1 || subs.saved[0].SubjectKey != "opportunity:pregao-2026-0042" {
		t.Fatalf("subscriptions = %+v", subs.saved)
	}
}

// A rejected envelope creates nothing at all, not even the half of it that was
// well formed.
func TestWebIntentRejectionIsWholeEnvelope(t *testing.T) {
	svc, orgID := newWebIntentService(t)
	subs := &fakeWatchSubs{}
	svc.WireIntelWatchSubscriptions(subs)

	env := validWebIntent(intel.WebIntentMonitorCompany)
	env.CompanyRef = "11222333000181"
	if _, xerr := svc.IngestWebIntent(context.Background(), orgID, env, time.Now().UTC()); xerr == nil {
		t.Fatal("CNPJ correlation key accepted")
	}
	if len(subs.saved) != 0 {
		t.Fatalf("a rejected envelope wrote %d subscriptions", len(subs.saved))
	}
}

func TestWebIntentEnvelopeRecognizerDoesNotClaimOtherBodies(t *testing.T) {
	if intel.IsWebIntentEnvelope([]byte(`{"schema":"confenge.commercial_event.v1"}`)) {
		t.Fatal("web intent recognizer claimed a commercial event body")
	}
	if !intel.IsWebIntentEnvelope([]byte(`{"schema":"` + intel.WebIntentSchemaV1 + `"}`)) {
		t.Fatal("web intent recognizer did not claim its own body")
	}
}
