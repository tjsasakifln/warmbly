package confenge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
)

// Governance#65 inbound-only admission.
//
// A valid-contact REQUEST_* must never disappear because no outreach account
// exists yet. This write creates or reuses one inbound-only commercial
// representation, keyed by origin+intent+contact, and files it on the unique
// commercial-action queue. It never grants outbound eligibility.

const (
	WebIntentReasonAdmissionFailed = "inbound_only_admission_failed"
	WebIntentReasonUnknown         = "UNKNOWN"

	inboundOnlyOriginPrefix  = "origin:"
	inboundOnlyIntentPrefix  = "intent_kind:"
	inboundOnlyReceiptPrefix = "receipt:"
	inboundOnlyContextPrefix = "context:"
	inboundOnlyFlagEvidence  = "inbound_only:true"
	inboundOnlyNotOutbound   = "outbound_eligible:false"
	inboundOnlyLeadIDPrefix  = "inbound_only:"
)

// InboundAdmission is one Governance#65 representation after admit-or-reuse.
type InboundAdmission struct {
	Account          *models.OutreachAccount
	Candidate        *models.OutreachContactCandidate
	Origin           string
	Lane             string
	IntentKind       string
	Context          string
	Receipt          string
	InboundOnly      bool
	OutboundEligible bool
	Reused           bool
}

// InboundAdmissionKey is the idempotency identity of one logical intent.
func InboundAdmissionKey(origin, intentKind, email string) string {
	return inboundOnlyLeadIDPrefix + strings.Join([]string{
		NormalizeEngineLane(origin),
		strings.ToUpper(strings.TrimSpace(intentKind)),
		normalizeWebIntentEmail(email),
	}, ":")
}

// InboundReceiptID is a stable, PII-free receipt for one logical intent.
func InboundReceiptID(origin, intentKind, email, subjectKey string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		"inbound_receipt/1.0",
		NormalizeEngineLane(origin),
		strings.ToUpper(strings.TrimSpace(intentKind)),
		normalizeWebIntentEmail(email),
		strings.TrimSpace(subjectKey),
	}, "\n")))
	return hex.EncodeToString(sum[:])
}

// AdmitInboundOnly creates or reuses one inbound-only commercial representation
// for a valid contact that is not already on the book. An existing outbound
// account is reused as-is and is not rewritten to inbound-only.
func (s *service) AdmitInboundOnly(ctx context.Context, orgID uuid.UUID, origin, intentKind, email, name, subjectKey, contextText string, now time.Time) (*InboundAdmission, *errx.Error) {
	if s == nil || s.repo == nil {
		return nil, errx.NewWithIdentifier(errx.Internal, WebIntentReasonAdmissionFailed,
			"inbound-only admission store is not wired")
	}
	if orgID == uuid.Nil {
		return nil, errx.NewWithIdentifier(errx.BadRequest, WebIntentReasonUnknown,
			"inbound-only admission requires an organization")
	}
	email = normalizeWebIntentEmail(email)
	if email == "" {
		return nil, errx.NewWithIdentifier(errx.BadRequest, WebIntentReasonEmailMissing,
			"contact_email is required")
	}
	origin = NormalizeEngineLane(origin)
	if origin == EngineLaneUnattributed {
		origin = EngineLaneConfengeWeb
	}
	intentKind = strings.ToUpper(strings.TrimSpace(intentKind))
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	receipt := InboundReceiptID(origin, intentKind, email, subjectKey)
	out := &InboundAdmission{
		Origin:     origin,
		Lane:       origin,
		IntentKind: intentKind,
		Context:    SanitizeText(contextText, 500),
		Receipt:    receipt,
	}

	cand, acc, err := s.repo.FindCandidateByEmail(ctx, orgID, email)
	if err != nil {
		return nil, errx.NewWithIdentifier(errx.Internal, WebIntentReasonAdmissionFailed,
			"inbound-only contact lookup: "+err.Error())
	}
	if acc != nil {
		out.Account = acc
		out.Candidate = cand
		out.Reused = true
		out.InboundOnly = models.AccountIsInboundOnly(acc)
		out.OutboundEligible = accountOutboundEligible(acc)
		if cand == nil {
			created, xerr := s.upsertInboundCandidate(ctx, orgID, acc.ID, email, name, now)
			if xerr != nil {
				return nil, xerr
			}
			out.Candidate = created
		}
		return out, nil
	}

	leadID := InboundAdmissionKey(origin, intentKind, email)
	existing, getErr := s.repo.GetAccountBySourceLeadID(ctx, orgID, leadID)
	if getErr != nil {
		return nil, errx.NewWithIdentifier(errx.Internal, WebIntentReasonAdmissionFailed,
			"inbound-only account lookup: "+getErr.Error())
	}
	if existing != nil {
		out.Account = existing
		out.Reused = true
		out.InboundOnly = true
		out.OutboundEligible = false
		cands, listErr := s.repo.ListCandidates(ctx, orgID, existing.ID)
		if listErr != nil {
			return nil, errx.NewWithIdentifier(errx.Internal, WebIntentReasonAdmissionFailed,
				"inbound-only candidate lookup: "+listErr.Error())
		}
		for i := range cands {
			if normalizeWebIntentEmail(cands[i].Email) == email {
				cp := cands[i]
				out.Candidate = &cp
				break
			}
		}
		if out.Candidate == nil {
			created, xerr := s.upsertInboundCandidate(ctx, orgID, existing.ID, email, name, now)
			if xerr != nil {
				return nil, xerr
			}
			out.Candidate = created
		}
		return out, nil
	}

	placeholderCNPJ := inboundOnlyPlaceholderCNPJ(leadID)
	acc = &models.OutreachAccount{
		ID:                uuid.New(),
		OrganizationID:    orgID,
		SourceLeadID:      leadID,
		SourceSystem:      models.SourceSystemInboundOnly,
		InboundOnly:       true,
		CNPJ14:            placeholderCNPJ,
		CNPJRoot:          placeholderCNPJ[:8],
		RazaoSocial:       firstNonEmpty(SanitizeText(name, 120), email),
		NomeFantasia:      SanitizeText(subjectKey, 120),
		QueueState:        models.OutreachQueueNeedsReview,
		TargetFitEligible: false,
		EmailSendReady:    false,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if _, upsertErr := s.repo.UpsertAccount(ctx, acc); upsertErr != nil {
		// A racing replay of the same key lands on the unique identity.
		if stored, _ := s.repo.GetAccountBySourceLeadID(ctx, orgID, leadID); stored != nil {
			acc = stored
			acc.InboundOnly = true
		} else {
			return nil, errx.NewWithIdentifier(errx.Internal, WebIntentReasonAdmissionFailed,
				"inbound-only account write: "+upsertErr.Error())
		}
	}
	// Re-read by the idempotency key so a racing replay still converges.
	if stored, _ := s.repo.GetAccountBySourceLeadID(ctx, orgID, leadID); stored != nil {
		acc = stored
		acc.InboundOnly = true
	}
	created, xerr := s.upsertInboundCandidate(ctx, orgID, acc.ID, email, name, now)
	if xerr != nil {
		return nil, xerr
	}
	out.Account = acc
	out.Candidate = created
	out.InboundOnly = true
	out.OutboundEligible = false
	return out, nil
}

// inboundOnlyPlaceholderCNPJ is a 14-digit identity that satisfies the
// outreach_accounts cnpj14 CHECK. It is not a tax id and is derived only from
// the admission key so replay converges.
func inboundOnlyPlaceholderCNPJ(leadID string) string {
	sum := sha256.Sum256([]byte("inbound_only_cnpj/1.0\n" + strings.TrimSpace(leadID)))
	out := make([]byte, 14)
	for i := 0; i < 14; i++ {
		out[i] = '0' + sum[i]%10
	}
	return string(out)
}

// InboundAdmissionMetrics is the PII-free aggregate view of one REQUEST_*.
type InboundAdmissionMetrics struct {
	IntentKind       string `json:"intent_kind"`
	Origin           string `json:"origin"`
	Lane             string `json:"lane"`
	InboundOnly      bool   `json:"inbound_only"`
	OutboundEligible bool   `json:"outbound_eligible"`
	Receipt          string `json:"receipt"`
	Reason           string `json:"reason,omitempty"`
	Matched          bool   `json:"matched"`
	Queued           bool   `json:"queued"`
	Reused           bool   `json:"reused,omitempty"`
}

// WebIntentMetricsView is the aggregated readback of one intake. It never
// copies email, name, phone or free-text context.
func WebIntentMetricsView(res *WebIntentResult) InboundAdmissionMetrics {
	if res == nil {
		return InboundAdmissionMetrics{Reason: WebIntentReasonUnknown}
	}
	return InboundAdmissionMetrics{
		IntentKind:       res.IntentKind,
		Origin:           res.Origin,
		Lane:             res.EngineLane,
		InboundOnly:      res.InboundOnly,
		OutboundEligible: res.OutboundEligible,
		Receipt:          res.Receipt,
		Reason:           res.Reason,
		Matched:          res.Matched,
		Queued:           res.ActionID != nil,
	}
}

func accountOutboundEligible(acc *models.OutreachAccount) bool {
	if acc == nil || models.AccountIsInboundOnly(acc) {
		return false
	}
	return acc.TargetFitEligible || acc.EmailSendReady
}

func (s *service) upsertInboundCandidate(ctx context.Context, orgID, accountID uuid.UUID, email, name string, now time.Time) (*models.OutreachContactCandidate, *errx.Error) {
	cand := &models.OutreachContactCandidate{
		ID:                 uuid.New(),
		OrganizationID:     orgID,
		AccountID:          accountID,
		Name:               SanitizeText(name, 120),
		Email:              email,
		SourceContactID:    inboundOnlyLeadIDPrefix + email,
		VerificationStatus: models.OutreachVerifyCandidateUnverified,
		EmailSendReady:     false,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	if _, err := s.repo.UpsertCandidate(ctx, cand); err != nil {
		return nil, errx.NewWithIdentifier(errx.Internal, WebIntentReasonAdmissionFailed,
			"inbound-only candidate write: "+err.Error())
	}
	return cand, nil
}
