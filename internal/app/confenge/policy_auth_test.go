package confenge

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func fullGreenInput() GreenAutorunInput {
	return GreenAutorunInput{
		Channel:                   "EMAIL",
		EmailSendReady:            true,
		TargetFitSendTier:         "A_AUTOMATIC",
		OwnershipAllowed:          true,
		MailboxPurposeAllowed:     true,
		VerificationAllowed:       true,
		DNC:                       false,
		Bounce:                    false,
		Replied:                   false,
		Blocked:                   false,
		ContactFresh:              true,
		ContextFresh:              true,
		ServiceCode:               "REAJUSTE_14133",
		SingleService:             true,
		FactualHookAnchored:       true,
		NoUnknownEvidenceIDs:      true,
		NoHypothesisAsFact:        true,
		NoClaimsToAvoidViolated:   true,
		ValidationOK:              true,
		RiskClass:                 "GREEN",
		MessageContextHashCurrent: true,
		NoEditAfterAuthorization:  true,
		CopyWithinLimits:          true,
		GovernorHealthy:           true,
		InSendWindow:              true,
		ProviderHealthy:           true,
	}
}

func TestGreenAutorunFailClosedDefault(t *testing.T) {
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	auth := &CampaignPolicyAuthorization{
		CampaignID: uuid.New(), Channel: "EMAIL", AllowedRiskClass: "GREEN",
		EffectiveAt: now.Add(-time.Hour), AuthorizedBy: uuid.New(),
	}
	d := EvaluateGreenAutorun(false, auth, fullGreenInput(), now)
	if d.Allow {
		t.Fatal("disabled flag must fail closed")
	}
}

func TestGreenAutorunRequiresPolicy(t *testing.T) {
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	d := EvaluateGreenAutorun(true, nil, fullGreenInput(), now)
	if d.Allow {
		t.Fatal("nil auth must fail")
	}
}

func TestGreenAutorunAllPredicatesPass(t *testing.T) {
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	auth := &CampaignPolicyAuthorization{
		CampaignID: uuid.New(), Channel: "EMAIL", AllowedRiskClass: "GREEN",
		EffectiveAt: now.Add(-time.Hour), AuthorizedBy: uuid.New(),
		AllowPolicyTemplateGREEN: true,
	}
	d := EvaluateGreenAutorun(true, auth, fullGreenInput(), now)
	if !d.Allow {
		t.Fatalf("want allow, reasons=%v", d.Reasons)
	}
	if d.AuthorizationMode != AuthorizationModeCampaignPolicy {
		t.Fatalf("mode=%s", d.AuthorizationMode)
	}
}

func TestGreenAutorunBlocksYELLOWAndGenericTemplate(t *testing.T) {
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	auth := &CampaignPolicyAuthorization{
		CampaignID: uuid.New(), Channel: "EMAIL", AllowedRiskClass: "GREEN",
		EffectiveAt: now.Add(-time.Hour), AuthorizedBy: uuid.New(),
	}
	in := fullGreenInput()
	in.RiskClass = "YELLOW"
	d := EvaluateGreenAutorun(true, auth, in, now)
	if d.Allow {
		t.Fatal("YELLOW must not autorun")
	}
	in = fullGreenInput()
	in.GenericUnauditedTemplate = true
	d = EvaluateGreenAutorun(true, auth, in, now)
	if d.Allow {
		t.Fatal("generic template must not autorun")
	}
}

func TestGreenAutorunBlocksMailboxAndTargetFit(t *testing.T) {
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	auth := &CampaignPolicyAuthorization{
		CampaignID: uuid.New(), Channel: "EMAIL", AllowedRiskClass: "GREEN",
		EffectiveAt: now.Add(-time.Hour), AuthorizedBy: uuid.New(),
	}
	in := fullGreenInput()
	in.MailboxPurposeAllowed = false
	if EvaluateGreenAutorun(true, auth, in, now).Allow {
		t.Fatal("blocked mailbox purpose")
	}
	in = fullGreenInput()
	in.TargetFitSendTier = "RESEARCH_ONLY"
	if EvaluateGreenAutorun(true, auth, in, now).Allow {
		t.Fatal("RESEARCH_ONLY must not autorun")
	}
	in = fullGreenInput()
	in.EmailSendReady = false
	if EvaluateGreenAutorun(true, auth, in, now).Allow {
		t.Fatal("EMAIL_SEND_READY false")
	}
}
