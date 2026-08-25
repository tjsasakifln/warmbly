package intel

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	OpRegisterSnapshot = "register_snapshot"
	OpAttachProvider   = "attach_provider_ids"
	OpRecordFinancial  = "record_financial"
	OpStartOnboarding  = "start_onboarding"
	OpHumanCorrect     = "human_correction"
	OpReplay           = "replay"
)

// ApplyOperator is the shipped manual-first entry. It writes the same
// receipts a webhook would write. It never calls a provider.
func ApplyOperator(store Store, orgID string, req OperatorRequest) OperatorResult {
	if err := rejectOperatorPII(req); err != nil {
		now := time.Now().UTC()
		ex := Exception{
			OrganizationID: orgID, Code: ExceptionPIIRejected, CodeVersion: ExceptionCodeVersion,
			Reason: err.Error(), NextAction: "resubmit with IDs/hashes only", Held: true,
			Owner: firstNonEmpty(req.ActorRef, "operator"), OpenedAt: now, At: now,
		}
		if store != nil {
			_ = store.PutException(ex)
		}
		return OperatorResult{Rejected: true, Reason: err.Error(), Join: JoinResult{Exceptions: []Exception{ex}, Held: true}}
	}

	ev := operatorEvent(orgID, req)
	if ev.EventID == "" {
		ev.EventID = "op-" + uuid.NewString()
	}
	if ev.OccurredAt.IsZero() {
		ev.OccurredAt = time.Now().UTC()
	}
	ev.ActorRef = firstNonEmpty(req.ActorRef, "operator")
	ev.Synthetic = req.Synthetic

	if req.Action == OpStartOnboarding {
		ev.Type = EventOnboardingStarted
	}
	if req.Action == OpRecordFinancial {
		if ev.Type == "" {
			ev.Type = EventPaymentReceived
		}
		ev.Payment.ReviewStatus = firstNonEmpty(req.Payment.ReviewStatus, ReviewRequired)
		ev.Payment.FinanceReviewReq = true
		ev.Payment.EvidenceRef = firstNonEmpty(req.EvidenceRef, req.Payment.EvidenceRef)
	}
	if req.Action == OpAttachProvider {
		if ev.Type == "" {
			ev.Type = EventCheckoutCreated
		}
	}
	if req.Action == OpRegisterSnapshot {
		if ev.Type == "" {
			ev.Type = EventTermsAccepted
		}
		if ev.Capacity.State == CapacityStateOK || ev.Capacity.State == CapacityStateHold || ev.Type == EventCapacityApproved {
			if ev.Type == EventTermsAccepted {
				// register snapshot also records capacity if supplied
			}
		}
	}
	if req.Action == OpHumanCorrect {
		ev.Type = EventCorrection
		ev.Correction = true
		ev.HumanConfirmed = req.HumanConfirmed
	}

	if req.Action == OpRegisterSnapshot && req.Capacity.State == CapacityStateOK {
		if cs, ok := store.(interface {
			HoldCapacity(string, string, int, time.Time) (CapacityHold, error)
		}); ok && req.Capacity.HoldID == "" {
			if h, err := cs.HoldCapacity(orgID, req.LeadID, max(1, req.Capacity.Units), ev.OccurredAt); err == nil {
				ev.Capacity.HoldID = h.HoldID
				ev.Capacity.State = CapacityStateHold
				t := h.CreatedAt
				x := h.ExpiresAt
				ev.Capacity.HoldCreatedAt = &t
				ev.Capacity.HoldExpiresAt = &x
				ev.Capacity.PolicyVersion = CapacityPolicyV1
				ev.Capacity.Limit = CapacityLimitV1
			}
		}
	}

	if req.CNPJ != "" {
		ev.CNPJHash = HashCNPJ(req.CNPJ)
		if store != nil && duplicateCNPJ(store, orgID, ev.CNPJHash, req.LeadID) {
			now := time.Now().UTC()
			ex := Exception{
				OrganizationID: orgID, Code: ExceptionDuplicateCNPJ, CodeVersion: ExceptionCodeVersion,
				Reason: "duplicate company/CNPJ hash on another chain", NextAction: "keep the first account; do not overwrite",
				LeadID: req.LeadID, Held: true, Owner: ev.ActorRef, OpenedAt: now, At: now,
				EvidenceRefs: []string{req.EvidenceRef},
			}
			_ = store.PutException(ex)
		}
	}

	res := IngestEvent(store, ev)
	if req.Action == OpStartOnboarding && !res.Held && store != nil {
		if cs, ok := store.(interface {
			FinalizeCapacity(string, string, time.Time) error
		}); ok && ev.Capacity.HoldID != "" && ev.Offer.BillingMode == BillingRecurring {
			_ = cs.FinalizeCapacity(orgID, ev.Capacity.HoldID, ev.OccurredAt)
		}
	}

	out := OperatorResult{Join: res}
	if res.Chain.Identity != "" {
		can := ProjectCanonical(res.Chain, res.Exceptions)
		out.Canonical = &can
	}
	out.Rejected = res.Held && (req.Action == OpStartOnboarding || req.Action == OpRecordFinancial && res.ReasonHeld())
	if res.Held && req.Action == OpStartOnboarding {
		out.Rejected = true
		out.Reason = "onboarding_gate"
	}
	return out
}

func (r JoinResult) ReasonHeld() bool { return r.Held }

func operatorEvent(orgID string, req OperatorRequest) CommercialEvent {
	typ := strings.TrimSpace(req.Action)
	switch req.Action {
	case OpRegisterSnapshot:
		typ = EventTermsAccepted
	case OpAttachProvider:
		typ = EventCheckoutCreated
	case OpRecordFinancial:
		typ = firstNonEmpty(req.Payment.CanonicalStatus, EventPaymentReceived)
		if typ == PaymentStatusReceived {
			typ = EventPaymentReceived
		}
		if typ == PaymentStatusConfirmed {
			typ = EventPaymentConfirmed
		}
		if typ == PaymentStatusPending {
			typ = EventPaymentPending
		}
		if typ == PaymentStatusCreated {
			typ = EventPaymentCreated
		}
		if typ == PaymentStatusOverdue {
			typ = EventPaymentOverdue
		}
		if typ == PaymentStatusRefunded {
			typ = EventPaymentRefunded
		}
	case OpStartOnboarding:
		typ = EventOnboardingStarted
	case OpHumanCorrect:
		typ = EventCorrection
	case OpReplay:
		typ = firstNonEmpty(req.Payment.CanonicalStatus, EventTermsAccepted)
	}
	off := req.Offer
	if off.OfferID != "" {
		if frozen, ok := FrozenOffer(off.OfferID); ok {
			if off.AmountCents == 0 {
				off = frozen
			}
		}
	}
	cap := req.Capacity
	if cap.PolicyVersion == "" {
		cap.PolicyVersion = CapacityPolicyV1
	}
	if cap.Limit == 0 {
		cap.Limit = CapacityLimitV1
	}
	return CommercialEvent{
		EventID:            firstNonEmpty(req.EventID, req.IdempotencyKey),
		Version:            "1",
		Schema:             EventSchemaV1,
		Type:               typ,
		OccurredAt:         req.OccurredAt,
		IngestedAt:         time.Now().UTC(),
		Timezone:           "America/Sao_Paulo",
		OrganizationID:     orgID,
		LeadID:             req.LeadID,
		ReceiptID:          req.ReceiptID,
		CorrelationID:      req.CorrelationID,
		AccountPublicID:    req.AccountPublicID,
		OpportunityID:      req.OpportunityID,
		ProposalID:         req.ProposalID,
		ChargeID:           req.ChargeID,
		PaymentID:          req.PaymentID,
		IdempotencyKey:     firstNonEmpty(req.IdempotencyKey, req.EventID),
		OfferID:            off.OfferID,
		Source:             firstNonEmpty(req.Source, "operator"),
		AssetID:            req.AssetID,
		CTAID:              req.CTAID,
		Query:              req.Query,
		Referrer:           req.Referrer,
		RouteFamily:        firstNonEmpty(req.RouteFamily, FamilyInbound),
		ActorRef:           req.ActorRef,
		EvidenceRef:        req.EvidenceRef,
		DeliverableID:      req.DeliverableID,
		CommercialDecision: req.CommercialDecision,
		Responsible:        req.Responsible,
		Deadline:           req.Deadline,
		NextAction:         req.NextAction,
		HumanConfirmed:     req.HumanConfirmed,
		Correction:         req.Correction || req.Action == OpHumanCorrect,
		Synthetic:          req.Synthetic,
		Offer:              off,
		Capacity:           cap,
		Provider:           req.Provider,
		Payment:            req.Payment,
		Gates:              req.Gates,
		ExternalReference:  req.Provider.ExternalRef,
		ProviderEventID:    req.Provider.ProviderEventID,
		CompanyRef:         req.CompanyRef,
		CNPJHash:           HashCNPJ(req.CNPJ),
		ProducerSHA:        "manual-first",
	}
}

// ProjectCanonical is the operator timeline view over the existing chain.
func ProjectCanonical(c Chain, xs []Exception) CanonicalState {
	return CanonicalState{
		Identity:    c.Identity,
		LeadID:      c.LeadID,
		Held:        c.Held,
		Outcome:     c.OutcomeType,
		Commercial:  c.Commercial,
		Timeline:    c.Commercial.Timeline,
		Exceptions:  xs,
		CausalProof: false,
		Synthetic:   c.Synthetic,
	}
}

func GetCanonical(store Store, orgID, leadID string) (*CanonicalState, error) {
	if store == nil {
		return nil, fmt.Errorf("store unavailable")
	}
	identity := ChainIdentity(JoinKeys{LeadID: leadID})
	c, err := store.GetChain(orgID, identity)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return nil, nil
	}
	var xs []Exception
	if listed, lerr := store.ListExceptions(orgID); lerr == nil {
		for _, ex := range listed {
			if ex.Identity == c.Identity || ex.LeadID == leadID {
				xs = append(xs, ex)
			}
		}
	}
	can := ProjectCanonical(*c, xs)
	return &can, nil
}

func rejectOperatorPII(req OperatorRequest) error {
	for _, v := range []string{
		req.LeadID, req.ReceiptID, req.CorrelationID, req.Source, req.Query, req.Referrer,
		req.AssetID, req.CompanyRef, req.AccountPublicID, req.OpportunityID, req.ProposalID,
		req.ChargeID, req.PaymentID, req.DeliverableID, req.Responsible,
	} {
		if MetricKeyContainsPII(v) || strings.Contains(v, "@") {
			return fmt.Errorf("raw PII rejected in operator fields")
		}
	}
	if u, err := url.Parse(req.Query); err == nil && u.RawQuery != "" {
		q := strings.ToLower(u.RawQuery)
		for _, tok := range []string{"email=", "phone=", "cnpj=", "nome=", "name="} {
			if strings.Contains(q, tok) {
				return fmt.Errorf("PII in query string rejected")
			}
		}
	}
	return nil
}

func duplicateCNPJ(store Store, orgID, hash, leadID string) bool {
	if hash == "" {
		return false
	}
	chains, err := store.ListChains(orgID)
	if err != nil {
		return false
	}
	for _, c := range chains {
		if c.Commercial.CNPJHash == hash && c.LeadID != leadID && c.LeadID != Unknown {
			return true
		}
	}
	return false
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// ReopenException reopens a resolved commercial exception. It does not
// invent WON/LOST/revenue.
func ReopenException(store Store, orgID, id, actor, reason string, now time.Time) (ResolveResult, error) {
	if store == nil {
		return ResolveResult{Refused: true, Reason: "store unavailable"}, fmt.Errorf("store unavailable")
	}
	ex, err := store.GetException(orgID, id)
	if err != nil {
		return ResolveResult{Refused: true, Reason: err.Error()}, err
	}
	if ex == nil {
		return ResolveResult{Refused: true, Reason: "exception not found"}, nil
	}
	before := ExceptionSnapshot{ID: ex.ID, Status: ex.Status, Code: ex.Code, Held: ex.Held, NextAction: ex.NextAction, Identity: ex.Identity}
	if strings.TrimSpace(ex.Status) == StatusOpen || ex.ResolvedAt == nil && ex.Status == "" {
		// already open: replay
		return ResolveResult{Exception: *ex, Replay: true, Before: before, After: before, Actor: actor, Action: "reopen"}, nil
	}
	ex.Status = StatusOpen
	ex.Held = true
	ex.ResolvedAt = nil
	ex.Resolution = nil
	ex.RetryState = "replayed"
	ex.History = append(ex.History, QueueEvent{At: now, Kind: "reopen", Actor: actor, Action: "reopen", Reason: reason})
	if err := store.UpdateException(*ex); err != nil {
		return ResolveResult{Refused: true, Reason: err.Error()}, err
	}
	after := ExceptionSnapshot{ID: ex.ID, Status: ex.Status, Code: ex.Code, Held: ex.Held, NextAction: ex.NextAction, Identity: ex.Identity}
	return ResolveResult{Exception: *ex, Before: before, After: after, Actor: actor, Action: "reopen"}, nil
}
