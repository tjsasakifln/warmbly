package intel

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// ParseCommercialEvent decodes one versioned envelope. Unknown extra
// fields are ignored. Narrative/PII blobs are not copied.
func ParseCommercialEvent(raw []byte) (CommercialEvent, error) {
	var ev CommercialEvent
	if len(raw) == 0 {
		return ev, fmt.Errorf("empty commercial event")
	}
	if err := json.Unmarshal(raw, &ev); err != nil {
		return ev, err
	}
	var env producerEnvelope
	if err := json.Unmarshal(raw, &env); err == nil {
		ev = overlayProducerEnvelope(ev, env)
	}
	ev = NormalizeProducerCommercialEvent(ev)
	return ev, ValidateCommercialEvent(ev)
}

// ValidateCommercialEvent checks the minimum envelope. It does not
// invent missing IDs.
func ValidateCommercialEvent(ev CommercialEvent) error {
	if strings.TrimSpace(ev.EventID) == "" {
		return fmt.Errorf("event_id required")
	}
	if strings.TrimSpace(ev.Version) == "" && strings.TrimSpace(ev.Schema) == "" {
		return fmt.Errorf("version or schema required")
	}
	if strings.TrimSpace(ev.Type) == "" {
		return fmt.Errorf("type required")
	}
	if ev.OccurredAt.IsZero() {
		return fmt.Errorf("occurred_at required")
	}
	if err := validateCommercialIdentityFields(ev); err != nil {
		return err
	}
	if value := strings.TrimSpace(ev.CommercialDecision); value != "" && normalizeDecision(value) == Unknown {
		return fmt.Errorf("commercial_decision must be GO, NO-GO, or WAIT")
	}
	return nil
}

func validateCommercialIdentityFields(ev CommercialEvent) error {
	fields := []struct {
		name  string
		value string
	}{
		{name: "correlation_id", value: ev.CorrelationID},
		{name: "external_reference", value: ev.ExternalReference},
		{name: "account_public_id", value: ev.AccountPublicID},
		{name: "opportunity_id", value: ev.OpportunityID},
		{name: "offer_id", value: ev.OfferID},
		{name: "offer.offer_id", value: ev.Offer.OfferID},
		{name: "proposal_id", value: ev.ProposalID},
		{name: "charge_id", value: ev.ChargeID},
		{name: "provider.charge_id", value: ev.Provider.ChargeID},
		{name: "payment_id", value: ev.PaymentID},
		{name: "provider.payment_id", value: ev.Provider.PaymentID},
		{name: "provider_event_id", value: ev.ProviderEventID},
		{name: "provider.provider_event_id", value: ev.Provider.ProviderEventID},
		{name: "provider_external_reference", value: ev.Provider.ExternalRef},
		{name: "deliverable_id", value: ev.DeliverableID},
		{name: "responsible", value: ev.Responsible},
	}
	for _, field := range fields {
		if err := validateOpaqueIdentifier(field.name, field.value); err != nil {
			return err
		}
	}
	return nil
}

func validateOpaqueIdentifier(name, value string) error {
	value = strings.TrimSpace(value)
	if value == "" || value == Unknown {
		return nil
	}
	if utf8.RuneCountInString(value) > 160 || strings.Contains(value, "@") ||
		strings.IndexFunc(value, unicode.IsSpace) >= 0 || MetricKeyContainsPII(value) {
		return fmt.Errorf("%s must be an opaque identifier of at most 160 characters", name)
	}
	return nil
}

func knownEventType(t string) bool {
	c, _, unk := NormalizeEventType(t)
	if unk {
		return c == EventUnknownProvider
	}
	switch strings.ToLower(strings.TrimSpace(c)) {
	case EventLeadReceived, EventLeadValidated, EventLeadRejected,
		EventHandoffAccepted, EventHandoffException,
		EventActionApproved, EventActionExecuted,
		EventReply, EventOutcomeObserved, EventMeeting, EventProposal,
		EventPipelineCreated, EventPipelineUpdated,
		EventWon, EventLost, EventUnknownState, EventRevenueEvidenced,
		EventLearningCandidate, EventXRayCompleted, EventPageView,
		EventCitation, EventCorrection, EventSearchObservation,
		EventOperatorAlertCreated, EventOperatorAlertEmitted, EventOperatorAlertFailed,
		EventOperatorAlertAcknowledged, EventFirstHumanActionRecorded, EventInboundResolvedNoAction,
		EventEmailAttempted, EventProviderAccepted, EventDelivered,
		EventHardBounce, EventSoftBounce, EventOptOut, EventSpamComplaint, EventNoReply:
		return true
	default:
		return isCommercialEvent(c)
	}
}

// EventToFacts maps one envelope onto ObservedFacts. PIIPointer is
// discarded. Pipeline and revenue flags are set only from their events.
func EventToFacts(ev CommercialEvent) ObservedFacts {
	strippedQuery := LooksLikeIndividualSearchQuery(ev.Query) || LooksLikeIndividualSearchQuery(ev.GSCQuery)
	strippedHash := strings.TrimSpace(ev.QueryHash) != "" || LooksLikeQueryHash(ev.Query)
	producerKind := strings.ToLower(strings.TrimSpace(ev.RecordKind))
	producerSaidReal := producerKind == RecordKindReal || producerKind == "live" || producerKind == strings.ToLower(LabelReal)
	invalidVersion := strings.TrimSpace(ev.AssetVersion) != "" && !validAssetVersion(ev.AssetVersion)
	ev = SanitizeCommercialEvent(ev)
	typ, _, _ := NormalizeEventType(ev.Type)
	if typ == "" {
		typ = strings.ToLower(strings.TrimSpace(ev.Type))
	}
	ingested := ev.IngestedAt
	if ingested.IsZero() {
		ingested = time.Now().UTC()
	}
	occurred := ev.OccurredAt
	in := ObservedFacts{
		Keys: JoinKeys{
			OrganizationID:    strings.TrimSpace(ev.OrganizationID),
			Source:            strings.TrimSpace(ev.Source),
			Query:             strings.TrimSpace(ev.Query),
			AssetID:           strings.TrimSpace(ev.AssetID),
			CTAID:             strings.TrimSpace(ev.CTAID),
			CorrelationID:     strings.TrimSpace(ev.CorrelationID),
			PreferCorrelation: preferCommercialCorrelation(ev, typ),
			LeadID:            strings.TrimSpace(ev.LeadID),
			ReceiptID:         strings.TrimSpace(ev.ReceiptID),
			AccountID:         firstNonEmpty(ev.AccountPublicID, ev.EntityPublicID),
			OpportunityID:     strings.TrimSpace(ev.OpportunityID),
			ProposalID:        strings.TrimSpace(ev.ProposalID),
			ChargeID:          firstNonEmpty(ev.ChargeID, ev.Provider.ChargeID, ev.Provider.PaymentID),
			PaymentID:         strings.TrimSpace(ev.PaymentID),
			SourceLeadID:      strings.TrimSpace(ev.EntityPublicID),
			ActionID:          strings.TrimSpace(ev.ActionID),
			OutcomeID:         strings.TrimSpace(ev.OutcomeID),
			IdempotencyKey:    strings.TrimSpace(ev.IdempotencyKey),
			RouteFamily:       strings.TrimSpace(ev.RouteFamily),
			Trigger:           strings.TrimSpace(ev.Trigger),
			OfferID:           strings.TrimSpace(ev.OfferID),
			Route:             strings.TrimSpace(ev.Route),
			EventID:           strings.TrimSpace(ev.EventID),
			EventIDs:          eventIDList(ev),
			AssetFamily:       normalizeAssetFamily(ev.AssetFamily),
			MarketAnswerID:    strings.TrimSpace(ev.MarketAnswerID),
			AnalysisID:        strings.TrimSpace(ev.AnalysisID),
			Referrer:          strings.TrimSpace(ev.Referrer),
			IntentClass:       strings.TrimSpace(ev.IntentClass),
			ActorRef:          strings.TrimSpace(ev.ActorRef),
			EvidenceRef:       strings.TrimSpace(ev.EvidenceRef),
			Consent:           strings.TrimSpace(ev.Consent),
			ProducerSHA:       strings.TrimSpace(ev.ProducerSHA),
			Schema:            firstNonEmpty(ev.Schema, ev.Version),
			RevenueDocumentID: strings.TrimSpace(ev.RevenueDocumentID),
			CustomerProofLane: ev.CustomerProofLane,
			OfferVersion:      strings.TrimSpace(ev.Offer.OfferVersion),
			TermsVersion:      strings.TrimSpace(ev.Offer.TermsVersion),
			ExternalReference: firstNonEmpty(ev.ExternalReference, ev.Provider.ExternalRef),
			ProviderEventID:   firstNonEmpty(ev.ProviderEventID, ev.Provider.ProviderEventID),
			CompanyRef:        strings.TrimSpace(ev.CompanyRef),
			CNPJHash:          strings.TrimSpace(ev.CNPJHash),
			HoldID:            strings.TrimSpace(ev.Capacity.HoldID),
			QueryClass:        strings.TrimSpace(ev.QueryClass),
			ReferrerClass:     strings.TrimSpace(ev.ReferrerClass),
			OrganicSource:     ev.OrganicSource,
			Medium:            strings.TrimSpace(ev.Medium),
			Campaign:          strings.TrimSpace(ev.Campaign),
			LandingPath:       ev.LandingPath,
			AssetVersion:      strings.TrimSpace(ev.AssetVersion),
			CTAVersion:        strings.TrimSpace(ev.CTAVersion),
			RecordKind:        ev.RecordKind,
			ConsentVersion:    strings.TrimSpace(ev.ConsentVersion),
			PageVersion:       strings.TrimSpace(ev.PageVersion),
			ContentVersion:    strings.TrimSpace(ev.ContentVersion),
			FirstTouchAt:      ev.FirstTouchAt,
			LastTouchAt:       ev.LastTouchAt,
		},
		LeadCreatedAt:        leadStamp(typ, occurred),
		IngestedAt:           ingested,
		ActionOccurredAt:     actionStamp(typ, occurred),
		OutcomeOccurredAt:    outcomeStamp(typ, occurred),
		EventType:            typ,
		Qualified:            ev.Qualified || typ == EventLeadValidated,
		HumanConfirmed:       ev.HumanConfirmed,
		Correction:           ev.Correction || typ == EventCorrection,
		HandRaise:            ev.HandRaise,
		Suppression:          ev.Suppression,
		Timezone:             firstNonEmpty(ev.Timezone, "UTC"),
		PublishedAt:          ev.PublishedAt,
		DetectedAt:           ev.DetectedAt,
		FollowUpAt:           ev.FollowUpAt,
		Synthetic:            ev.Synthetic,
		Label:                labelFor(ev.Synthetic),
		RecordKind:           ev.RecordKind,
		ConsentValid:         strings.TrimSpace(ev.Consent) != "",
		StrippedGSCQuery:     strippedQuery,
		StrippedQueryHash:    strippedHash,
		SyntheticLabeledReal: ev.Synthetic && producerSaidReal,
		InvalidAssetVersion:  invalidVersion,
	}
	if ev.Synthetic {
		in.Label = LabelSynthetic
	}

	switch typ {
	case EventLeadReceived:
		in.NotALead = false
	case EventLeadValidated:
		in.Qualified = true
	case EventLeadRejected:
		in.OutcomeType = OutcomeUnknown
	case EventHandoffException:
		in.OutcomeType = OutcomeUnknown
	case EventActionApproved, EventActionExecuted, EventFirstHumanActionRecorded:
		t := occurred
		in.FirstActionAt = &t
	case EventReply:
		in.Conversation = true
		in.OutcomeType = OutcomeReplied
		t := occurred
		in.ConversationAt = &t
	case EventOutcomeObserved:
		in.OutcomeType = normalizeOutcomeState(ev.OutcomeState)
		if in.OutcomeType == OutcomeReplied || in.OutcomeType == OutcomeQualifiedConversation {
			in.Conversation = true
			t := occurred
			in.ConversationAt = &t
		}
	case EventMeeting:
		in.OutcomeType = OutcomeMeeting
		in.Conversation = true
		t := occurred
		in.ConversationAt = &t
	case EventProposal:
		in.OutcomeType = OutcomeProposal
		t := occurred
		in.ProposalAt = &t
	case EventPipelineCreated, EventPipelineUpdated:
		in.PipelineOpen = true
	case EventWon:
		in.OutcomeType = OutcomeWon
		t := occurred
		in.CloseAt = &t
	case EventLost:
		in.OutcomeType = OutcomeLost
		t := occurred
		in.CloseAt = &t
	case EventUnknownState:
		in.OutcomeType = OutcomeUnknown
	case EventRevenueEvidenced:
		if strings.TrimSpace(ev.RevenueDocumentID) != "" && ev.RevenueCents > 0 {
			in.RevenueEvidenced = true
			in.RevenueCents = ev.RevenueCents
		}
	case EventXRayCompleted, EventPageView, EventCitation, EventSearchObservation:
		in.NotALead = true
		in.PipelineOpen = false
		in.Qualified = false
	case EventCorrection:
		in.Correction = true
		in.OutcomeType = normalizeOutcomeState(ev.OutcomeState)
		if isWonType(in.OutcomeType) || isLostType(in.OutcomeType) {
			t := occurred
			in.CloseAt = &t
		}
	}

	if typ == EventLearningCandidate {
		in.OutcomeType = normalizeOutcomeState(ev.OutcomeState)
	}
	return in
}

func eventIDList(ev CommercialEvent) []string {
	var out []string
	if id := strings.TrimSpace(ev.EventID); id != "" {
		out = append(out, id)
	}
	if id := strings.TrimSpace(ev.IdempotencyKey); id != "" {
		out = append(out, "idem:"+id)
	}
	if id := strings.TrimSpace(ev.ProviderEventID); id != "" {
		out = append(out, "provider:"+id)
	}
	return out
}

func leadStamp(typ string, occurred time.Time) time.Time {
	switch typ {
	case EventLeadReceived, EventLeadValidated, EventLeadRejected:
		return occurred
	default:
		return time.Time{}
	}
}

func actionStamp(typ string, occurred time.Time) time.Time {
	switch typ {
	case EventActionApproved, EventActionExecuted, EventFirstHumanActionRecorded:
		return occurred
	default:
		return time.Time{}
	}
}

func outcomeStamp(typ string, occurred time.Time) time.Time {
	switch typ {
	case EventReply, EventOutcomeObserved, EventMeeting, EventProposal,
		EventWon, EventLost, EventUnknownState, EventCorrection:
		return occurred
	default:
		return time.Time{}
	}
}

func labelFor(synthetic bool) string {
	if synthetic {
		return LabelSynthetic
	}
	return LabelReal
}

func normalizeOutcomeState(v string) string {
	u := strings.ToUpper(strings.TrimSpace(v))
	switch u {
	case OutcomeWon, EventWon:
		return OutcomeWon
	case OutcomeLost, EventLost:
		return OutcomeLost
	case OutcomeMeeting, EventMeeting:
		return OutcomeMeeting
	case OutcomeProposal, EventProposal:
		return OutcomeProposal
	case OutcomeQualifiedConversation, "QCO":
		return OutcomeQualifiedConversation
	case OutcomeReplied, EventReply:
		return OutcomeReplied
	case OutcomeContacted:
		return OutcomeContacted
	case OutcomeClient:
		return OutcomeClient
	case OutcomeNoResponse:
		return OutcomeNoResponse
	case OutcomeDoNotContact, "DNC":
		return OutcomeDoNotContact
	case "", OutcomeUnknown, EventUnknownState:
		return OutcomeUnknown
	default:
		return OutcomeUnknown
	}
}

func normalizeAssetFamily(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case AssetFamilyMarketAnswer, "market-answer", "marketanswer":
		return AssetFamilyMarketAnswer
	case AssetFamilyContractAnalysis, "contract-analysis", "contractanalysis":
		return AssetFamilyContractAnalysis
	case AssetFamilyB2GXRay, "b2g-xray", "b2gxray", "xray":
		return AssetFamilyB2GXRay
	case "":
		return ""
	default:
		return strings.ToLower(strings.TrimSpace(v))
	}
}

func knownAssetFamily(v string) bool {
	switch normalizeAssetFamily(v) {
	case AssetFamilyMarketAnswer, AssetFamilyContractAnalysis, AssetFamilyB2GXRay, "":
		return true
	default:
		return false
	}
}

// IngestEvent is the shipped entry for versioned events. Fixtures and
// future real events share this path. Same event_id or idempotency key
// is a replay. Search observations persist on the observation table,
// never as a commercial chain.
func IngestEvent(store Store, ev CommercialEvent) JoinResult {
	ev = NormalizeProducerCommercialEvent(ev)
	if isSearchObservationType(ev.Type, ev.Version, ev.Schema) {
		return ingestSearchObservationEvent(store, ev)
	}
	if err := RejectUnsupportedEnvelope(ev); err != nil {
		now := time.Now().UTC()
		ex := Exception{
			Code:       ExceptionOrphan,
			Reason:     err.Error(),
			NextAction: "fix the event and retry; do not invent a chain",
			At:         now,
			Synthetic:  ev.Synthetic,
			Held:       true,
			Owner:      OwnerInboundOps,
			Severity:   SeverityHigh,
			Status:     StatusOpen,
		}
		if store != nil {
			_ = store.PutException(ex)
		}
		return JoinResult{Exceptions: []Exception{ex}, Held: true}
	}
	if err := ValidateCommercialEvent(ev); err != nil {
		now := time.Now().UTC()
		ex := Exception{
			Code:       ExceptionOrphan,
			Reason:     "invalid envelope: " + err.Error(),
			NextAction: "fix the event and retry; do not invent a chain",
			At:         now,
			Synthetic:  ev.Synthetic,
		}
		if store != nil {
			_ = store.PutException(ex)
		}
		return JoinResult{Exceptions: []Exception{ex}, Held: true}
	}

	seen, err := findSeenEvent(store, ev)
	if err != nil {
		ex := storeUnavailableException(ev, "", "find existing event", err)
		if store != nil {
			_ = store.PutException(ex)
		}
		return JoinResult{Exceptions: []Exception{ex}, Held: true}
	}
	if existing := seen; existing != nil {
		if !ev.AllowReceiptRetry || commercialReceiptApplied(*existing, ev) {
			return JoinResult{Chain: *existing, Replay: true, Held: existing.Held}
		}
	}

	if isCommercialEvent(ev.Type) || ev.Offer.OfferID != "" || ev.ProviderEventID != "" || ev.CallbackOnly {
		return ingestCommercial(store, ev)
	}

	if ev.Type == EventLearningCandidate {
		facts := EventToFacts(ev)
		cand := EmitLearning(store, LearningInput{
			From:           LearningFromOutcome,
			Reason:         firstNonEmpty(ev.EvidenceRef, ev.OutcomeState, "learning_candidate"),
			OutcomeType:    facts.OutcomeType,
			HumanConfirmed: ev.HumanConfirmed,
			Keys:           facts.Keys,
			Synthetic:      ev.Synthetic,
		})
		res := Reconcile(store, facts)
		_ = cand
		return res
	}

	facts := EventToFacts(ev)
	res := Reconcile(store, facts)
	if ev.Type == EventCorrection && !res.Held {
		EmitLearning(store, LearningInput{
			From:            LearningFromCorrection,
			Reason:          firstNonEmpty(ev.EvidenceRef, "late_correction"),
			CorrectionCodes: []string{"correction"},
			OutcomeType:     facts.OutcomeType,
			HumanConfirmed:  ev.HumanConfirmed,
			Keys:            facts.Keys,
			Synthetic:       ev.Synthetic,
		})
	}
	return res
}

func findSeenEvent(store Store, ev CommercialEvent) (*Chain, error) {
	if store == nil {
		return nil, nil
	}
	eventID := strings.TrimSpace(ev.EventID)
	idem := strings.TrimSpace(ev.IdempotencyKey)
	prov := strings.TrimSpace(ev.ProviderEventID)
	if eventID == "" && idem == "" && prov == "" {
		return nil, nil
	}
	if rs, ok := store.(interface {
		GetEventReceipt(string, string) (*EventReceipt, error)
	}); ok && prov != "" {
		rec, err := rs.GetEventReceipt(ev.OrganizationID, prov)
		if err != nil {
			return nil, err
		}
		if rec != nil && rec.Identity != "" {
			ch, err := store.GetChain(ev.OrganizationID, rec.Identity)
			if err != nil {
				return nil, err
			}
			if ch != nil {
				return ch, nil
			}
		}
	}
	chains, err := store.ListChains(strings.TrimSpace(ev.OrganizationID))
	if err != nil {
		return nil, err
	}
	for i := range chains {
		for _, id := range chains[i].Keys.EventIDs {
			if prov != "" {
				if id == "provider:"+prov {
					return &chains[i], nil
				}
				continue
			}
			if eventID != "" && id == eventID {
				return &chains[i], nil
			}
			if idem != "" && id == "idem:"+idem {
				return &chains[i], nil
			}
		}
		if prov == "" && eventID != "" && chains[i].Keys.EventID == eventID {
			return &chains[i], nil
		}
		if prov != "" && chains[i].Keys.ProviderEventID == prov {
			return &chains[i], nil
		}
	}
	return nil, nil
}

func ingestCommercial(store Store, ev CommercialEvent) JoinResult {
	now := time.Now().UTC()
	if ev.CallbackOnly && ev.Type == "" {
		ev.Type = EventUnknownProvider
	}
	if ev.CallbackOnly && !hasFinancialType(ev.Type) {
		ex := Exception{
			OrganizationID: ev.OrganizationID,
			Code:           ExceptionImpossibleCommercial,
			CodeVersion:    ExceptionCodeVersion,
			Reason:         "callback/success URL is not a financial event",
			NextAction:     "wait for a confirmed/received payment event; do not infer revenue",
			LeadID:         ev.LeadID,
			Held:           true,
			Synthetic:      ev.Synthetic,
			Owner:          firstNonEmpty(ev.ActorRef, "commercial-intel"),
			OpenedAt:       now,
			At:             now,
			RetryState:     "pending",
		}
		if store != nil {
			_ = store.PutException(ex)
		}
		facts := EventToFacts(ev)
		facts.Commercial.Payment.CanonicalStatus = PaymentStatusNone
		res := Reconcile(store, facts)
		res.Exceptions = append(res.Exceptions, ex)
		res.Held = true
		return res
	}

	identity := ChainIdentity(JoinKeys{
		LeadID: ev.LeadID, ReceiptID: ev.ReceiptID, ActionID: ev.ActionID,
		IdempotencyKey: ev.IdempotencyKey, OrganizationID: ev.OrganizationID,
		ExternalReference: firstNonEmpty(ev.ExternalReference, ev.Provider.ExternalRef),
		CorrelationID:     ev.CorrelationID,
		PreferCorrelation: preferCommercialCorrelation(ev, ev.Type),
		OpportunityID:     ev.OpportunityID,
		ProposalID:        ev.ProposalID,
		ChargeID:          firstNonEmpty(ev.ChargeID, ev.Provider.ChargeID, ev.Provider.PaymentID),
		PaymentID:         ev.PaymentID,
		EventID:           ev.EventID,
	})
	var existing *Chain
	if store != nil && identity != "" {
		var err error
		existing, err = store.GetChain(ev.OrganizationID, identity)
		if err != nil {
			ex := storeUnavailableException(ev, identity, "get canonical chain", err)
			_ = store.PutException(ex)
			return JoinResult{Exceptions: []Exception{ex}, Held: true}
		}
	}

	if store != nil {
		if err := holdConflictingExternal(store, ev, identity, existing); err != nil {
			code := ExceptionUnavailable
			nextAction := "retry with the same IDs"
			if errors.Is(err, errCanonicalIdentityConflict) {
				code = ExceptionConflictingExternal
				nextAction = "hold; keep the first binding"
			}
			ex := Exception{
				OrganizationID: ev.OrganizationID, Code: code,
				CodeVersion: ExceptionCodeVersion, Reason: err.Error(),
				NextAction: nextAction, LeadID: ev.LeadID,
				Held: true, Synthetic: ev.Synthetic, Owner: "commercial-intel",
				OpenedAt: now, At: now, RetryState: "pending",
			}
			_ = store.PutException(ex)
			if existing != nil {
				return JoinResult{Chain: *existing, Exceptions: []Exception{ex}, Held: true}
			}
			return JoinResult{Exceptions: []Exception{ex}, Held: true}
		}
	}
	tr := ApplyCommercialTransition(existing, ev)
	receiptKey := firstNonEmpty(ev.ProviderEventID, ev.EventID)
	if store != nil && receiptKey != "" {
		if rs, ok := store.(interface {
			PutEventReceipt(EventReceipt) (EventReceipt, bool, error)
		}); ok {
			rec, created, err := rs.PutEventReceipt(EventReceipt{
				OrganizationID:  ev.OrganizationID,
				ProviderEventID: receiptKey,
				ExternalRef:     firstNonEmpty(ev.ExternalReference, ev.Provider.ExternalRef),
				EventID:         ev.EventID,
				Identity:        identity,
				Type:            ev.Type,
				RawType:         ev.RawEventType,
				RawStatus:       ev.RawProviderStatus,
				Synthetic:       ev.Synthetic,
				At:              now,
			})
			if err != nil {
				ex := Exception{
					Code: ExceptionUnavailable, CodeVersion: ExceptionCodeVersion,
					Reason:     "receipt store unavailable: " + err.Error(),
					NextAction: "retry; do not drop the event", Held: true, At: now,
					Owner: "commercial-intel", RetryState: "pending",
				}
				_ = store.PutException(ex)
				return JoinResult{Exceptions: []Exception{ex}, Held: true}
			}
			if !created {
				if rec.Processed {
					ch, err := store.GetChain(ev.OrganizationID, rec.Identity)
					if err != nil {
						ex := storeUnavailableException(ev, rec.Identity, "get processed receipt chain", err)
						_ = store.PutException(ex)
						return JoinResult{Exceptions: []Exception{ex}, Held: true}
					}
					if ch != nil {
						return JoinResult{Chain: *ch, Replay: true, Held: ch.Held}
					}
					ex := storeUnavailableException(ev, rec.Identity, "get processed receipt chain", errors.New("chain is missing"))
					_ = store.PutException(ex)
					return JoinResult{Exceptions: []Exception{ex}, Held: true}
				}
			}
		}
	}

	if tr.Rejected {
		persistExceptions(store, tr.Exceptions)
		if existing != nil {
			return JoinResult{Chain: *existing, Exceptions: tr.Exceptions, Held: true}
		}
		// Persist a held chain so timeline/canonical state is visible.
		res := Reconcile(store, tr.Facts)
		saved, err := storeGet(store, ev.OrganizationID, identity)
		if err != nil {
			ex := storeUnavailableException(ev, identity, "get held chain", err)
			_ = store.PutException(ex)
			res.Exceptions = append(res.Exceptions, tr.Exceptions...)
			res.Exceptions = append(res.Exceptions, ex)
			res.Held = true
			return res
		}
		if saved != nil {
			previous := *saved
			saved.Commercial = tr.Facts.Commercial
			saved.Held = true
			if err := store.UpdateChain(*saved); err != nil {
				ex := storeUnavailableException(ev, identity, "update chain", err)
				_ = store.PutException(ex)
				res.Chain = previous
				res.Exceptions = append(res.Exceptions, tr.Exceptions...)
				res.Exceptions = append(res.Exceptions, ex)
				res.Held = true
				return res
			}
			res.Chain = *saved
		}
		res.Exceptions = append(res.Exceptions, tr.Exceptions...)
		res.Held = true
		return res
	}

	res := Reconcile(store, tr.Facts)
	if store == nil {
		res.Exceptions = append(res.Exceptions, tr.Exceptions...)
		res.Held = res.Held || tr.Held
		return res
	}
	if JoinUnavailable(res) {
		res.Exceptions = append(res.Exceptions, tr.Exceptions...)
		res.Held = true
		return res
	}
	saved, err := store.GetChain(ev.OrganizationID, identity)
	if err != nil {
		ex := storeUnavailableException(ev, identity, "get reconciled chain", err)
		persistExceptions(store, tr.Exceptions)
		_ = store.PutException(ex)
		res.Exceptions = append(res.Exceptions, tr.Exceptions...)
		res.Exceptions = append(res.Exceptions, ex)
		res.Held = true
		return res
	}
	if saved == nil && res.Chain.Identity != "" {
		cp := res.Chain
		saved = &cp
	}
	if saved != nil {
		previous := *saved
		saved.Commercial = tr.Facts.Commercial
		if tr.Held {
			saved.Held = true
		}
		if err := store.UpdateChain(*saved); err != nil {
			ex := storeUnavailableException(ev, identity, "update chain", err)
			persistExceptions(store, tr.Exceptions)
			_ = store.PutException(ex)
			res.Chain = previous
			res.Exceptions = append(res.Exceptions, tr.Exceptions...)
			res.Exceptions = append(res.Exceptions, ex)
			res.Held = true
			return res
		}
		res.Chain = *saved
	}
	persistExceptions(store, tr.Exceptions)
	res.Exceptions = append(res.Exceptions, tr.Exceptions...)
	res.Held = res.Held || tr.Held
	if rs, ok := store.(interface {
		MarkReceiptProcessed(string, string) error
	}); ok && receiptKey != "" && commercialReceiptApplied(res.Chain, ev) {
		if err := rs.MarkReceiptProcessed(ev.OrganizationID, receiptKey); err != nil {
			ex := storeUnavailableException(ev, identity, "mark receipt processed", err)
			_ = store.PutException(ex)
			res.Exceptions = append(res.Exceptions, ex)
			res.Held = true
		}
	}
	return res
}

func storeUnavailableException(ev CommercialEvent, identity, operation string, err error) Exception {
	now := time.Now().UTC()
	return Exception{
		OrganizationID: ev.OrganizationID,
		Code:           ExceptionUnavailable,
		CodeVersion:    ExceptionCodeVersion,
		Reason:         operation + " failed: " + err.Error(),
		NextAction:     "retry with the same IDs",
		Identity:       identity,
		Held:           true,
		Synthetic:      ev.Synthetic,
		Owner:          "commercial-intel",
		OpenedAt:       now,
		At:             now,
		RetryState:     "pending",
	}
}

func commercialReceiptApplied(chain Chain, ev CommercialEvent) bool {
	eventID := strings.TrimSpace(ev.EventID)
	providerEventID := firstNonEmpty(ev.ProviderEventID, ev.Provider.ProviderEventID)
	for _, receipt := range chain.Commercial.Timeline {
		if providerEventID != "" && receipt.ProviderEventID == providerEventID {
			return true
		}
		if providerEventID == "" && eventID != "" && receipt.EventID == eventID {
			return true
		}
	}
	return false
}

func preferCommercialCorrelation(ev CommercialEvent, typ string) bool {
	return isCommercialEvent(typ) || ev.ProviderEventID != "" || ev.Provider.ProviderEventID != "" ||
		ev.OpportunityID != "" || ev.ProposalID != "" || ev.ChargeID != "" || ev.PaymentID != ""
}

// JoinUnavailable reports a fail-closed store error the HMAC edge should
// retry (HTTP 5xx). Replays are not 5xx.
func JoinUnavailable(res JoinResult) bool {
	for _, ex := range res.Exceptions {
		if ex.Code == ExceptionUnavailable {
			return true
		}
	}
	return false
}

func storeGet(store Store, orgID, identity string) (*Chain, error) {
	if store == nil {
		return nil, nil
	}
	return store.GetChain(orgID, identity)
}

var errCanonicalIdentityConflict = errors.New("canonical identity conflict")

func holdConflictingExternal(store Store, ev CommercialEvent, identity string, existing *Chain) error {
	ext := firstNonEmpty(ev.ExternalReference, ev.Provider.ExternalRef)
	lead := strings.TrimSpace(ev.LeadID)
	chains, err := store.ListChains(ev.OrganizationID)
	if err != nil {
		return err
	}
	incoming := CanonicalCommercialIdentity{
		CorrelationID: firstCanonicalID(ev.CorrelationID, ev.ExternalReference, ev.Provider.ExternalRef),
		AccountID:     firstCanonicalID(ev.AccountPublicID),
		OpportunityID: firstCanonicalID(ev.OpportunityID),
		OfferID:       firstCanonicalID(ev.OfferID, ev.Offer.OfferID),
		ProposalID:    firstCanonicalID(ev.ProposalID),
		ChargeID:      firstCanonicalID(ev.ChargeID, ev.Provider.ChargeID, ev.Provider.PaymentID),
		PaymentID:     firstCanonicalID(ev.PaymentID),
	}
	for _, c := range chains {
		if existing != nil && c.Identity == existing.Identity {
			continue
		}
		if ext != "" {
			haveExternal := firstNonEmpty(c.Commercial.Provider.ExternalRef, c.Keys.ExternalReference)
			if haveExternal == ext {
				other := strings.TrimSpace(c.LeadID)
				if other != "" && other != Unknown && lead != "" && other != lead {
					return fmt.Errorf("%w: externalReference %s already bound to %s", errCanonicalIdentityConflict, ext, c.Identity)
				}
				wantOffer := strings.TrimSpace(c.Commercial.Offer.OfferID)
				gotOffer := firstNonEmpty(ev.Offer.OfferID, ev.OfferID)
				if wantOffer != "" && gotOffer != "" && wantOffer != gotOffer {
					return fmt.Errorf("%w: externalReference %s already bound to offer %s", errCanonicalIdentityConflict, ext, wantOffer)
				}
			}
		}
		have := CanonicalIdentityOf(c)
		if existing == nil && incoming.CorrelationID != Unknown && have.CorrelationID == incoming.CorrelationID && c.Identity != identity {
			return fmt.Errorf("%w: correlation %s already has legacy chain %s; reconcile it before replay", errCanonicalIdentityConflict, incoming.CorrelationID, c.Identity)
		}
		if incoming.CorrelationID == Unknown || have.CorrelationID == Unknown ||
			incoming.CorrelationID == have.CorrelationID {
			continue
		}
		for _, pair := range []struct {
			kind     string
			incoming string
		}{
			{kind: "opportunity", incoming: incoming.OpportunityID},
			{kind: "proposal", incoming: incoming.ProposalID},
			{kind: "charge", incoming: incoming.ChargeID},
			{kind: "payment", incoming: incoming.PaymentID},
		} {
			if pair.incoming != Unknown && chainHasCanonicalID(c, pair.kind, pair.incoming) {
				return fmt.Errorf("%w: canonical %s id is already bound to %s", errCanonicalIdentityConflict, pair.kind, c.Identity)
			}
		}
	}
	return nil
}

func chainHasCanonicalID(chain Chain, kind, id string) bool {
	identity := CanonicalIdentityOf(chain)
	var canonical string
	switch kind {
	case "opportunity":
		canonical = identity.OpportunityID
	case "proposal":
		canonical = identity.ProposalID
	case "charge":
		canonical = identity.ChargeID
	case "payment":
		canonical = identity.PaymentID
	}
	if canonical == id {
		return true
	}
	for _, receipt := range chain.Commercial.Timeline {
		if kind == "charge" && canonicalID(receipt.ChargeID) == id {
			return true
		}
		if kind == "payment" && canonicalID(receipt.PaymentID) == id {
			return true
		}
	}
	return false
}

func hasFinancialType(t string) bool {
	c, _, _ := NormalizeEventType(t)
	switch c {
	case EventPaymentCreated, EventPaymentPending, EventPaymentConfirmed,
		EventPaymentReceived, EventPaymentOverdue, EventPaymentRefunded, EventPaymentFailed:
		return true
	default:
		return false
	}
}

func isNonLeadEvent(typ string) bool {
	switch strings.ToLower(strings.TrimSpace(typ)) {
	case EventXRayCompleted, EventPageView, EventCitation, EventSearchObservation,
		EventOperatorAlertCreated, EventOperatorAlertEmitted, EventOperatorAlertFailed,
		EventOperatorAlertAcknowledged, EventFirstHumanActionRecorded, EventInboundResolvedNoAction:
		return true
	default:
		return false
	}
}
