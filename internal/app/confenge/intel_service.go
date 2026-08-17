package confenge

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/warmbly/warmbly/internal/app/confenge/intel"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
)

// WireIntel attaches the commercial-intelligence store. Nil db uses memory.
func (s *service) WireIntel(db *pgxpool.Pool) {
	if db != nil {
		s.intel = intel.NewPGStore(db, "")
		return
	}
	s.intel = intel.NewMemoryStore()
}

func (s *service) intelStore() intel.Store {
	if s.intel == nil {
		s.intel = intel.NewMemoryStore()
	}
	return s.intel
}

func (s *service) emitCorrectionLearning(orgID, accountID uuid.UUID, hc HumanCorrection) {
	intel.EmitLearning(s.intelStore(), intel.LearningInput{
		From:            intel.LearningFromCorrection,
		Reason:          firstNonEmpty(hc.Reason, strings.Join(hc.ReasonCodes, ",")),
		CorrectionCodes: append([]string{}, hc.ReasonCodes...),
		HumanConfirmed:  true,
		Keys: intel.JoinKeys{
			OrganizationID: orgID.String(),
			AccountID:      accountID.String(),
			ActionID:       hc.DraftID,
		},
	})
}

// ReconcileCommercialIntel joins one observed ID record. Replay is a no-op.
func (s *service) ReconcileCommercialIntel(_ context.Context, orgID uuid.UUID, in intel.ObservedFacts) (intel.JoinResult, *errx.Error) {
	if xerr := s.requireEnabled(); xerr != nil {
		return intel.JoinResult{}, xerr
	}
	if in.Keys.OrganizationID == "" {
		in.Keys.OrganizationID = orgID.String()
	}
	return intel.Reconcile(s.intelStore(), in), nil
}

// CommercialExecutiveView reconciles known inbound/action IDs then rolls up
// the month. includeSynthetic=false keeps real metrics empty/UNKNOWN.
func (s *service) CommercialExecutiveView(ctx context.Context, orgID uuid.UUID, month string, includeSynthetic bool) (*intel.ExecutiveView, *errx.Error) {
	if xerr := s.requireEnabled(); xerr != nil {
		return nil, xerr
	}
	s.observeExisting(ctx, orgID)
	chains, err := s.intelStore().ListChains(orgID.String())
	if err != nil {
		return nil, errx.New(errx.Internal, "commercial intel list: "+err.Error())
	}
	view := intel.Rollup(chains, month, includeSynthetic)
	return &view, nil
}

// RecordIntelLearning emits a LEARNING CANDIDATE. No upstream write.
func (s *service) RecordIntelLearning(_ context.Context, orgID uuid.UUID, in intel.LearningInput) (intel.LearningCandidate, *errx.Error) {
	if xerr := s.requireEnabled(); xerr != nil {
		return intel.LearningCandidate{}, xerr
	}
	if in.Keys.OrganizationID == "" {
		in.Keys.OrganizationID = orgID.String()
	}
	return intel.EmitLearning(s.intelStore(), in), nil
}

// IngestCommercialEvent consumes one versioned envelope. Same event_id
// is a replay. Fixtures and real events share this path.
func (s *service) IngestCommercialEvent(_ context.Context, orgID uuid.UUID, ev intel.CommercialEvent) (intel.JoinResult, *errx.Error) {
	if xerr := s.requireEnabled(); xerr != nil {
		return intel.JoinResult{}, xerr
	}
	if ev.OrganizationID == "" {
		ev.OrganizationID = orgID.String()
	}
	return intel.IngestEvent(s.intelStore(), ev), nil
}

// CommercialIntelReport is the executive observability payload.
func (s *service) CommercialIntelReport(_ context.Context, orgID uuid.UUID, month string, includeSynthetic bool) (*intel.ObservabilityReport, *errx.Error) {
	if xerr := s.requireEnabled(); xerr != nil {
		return nil, xerr
	}
	rep := intel.BuildObservabilityReport(s.intelStore(), orgID.String(), month, includeSynthetic)
	return &rep, nil
}

// ListIntelExceptions returns the durable exception queue with operator filters.
func (s *service) ListIntelExceptions(_ context.Context, orgID uuid.UUID, filter intel.ExceptionFilter) ([]intel.Exception, *errx.Error) {
	if xerr := s.requireEnabled(); xerr != nil {
		return nil, xerr
	}
	xs, err := intel.ListQueue(s.intelStore(), orgID.String(), filter, time.Now().UTC())
	if err != nil {
		return nil, errx.New(errx.Internal, "commercial intel exceptions: "+err.Error())
	}
	return xs, nil
}

// GetIntelException returns one presented queue item.
func (s *service) GetIntelException(_ context.Context, orgID uuid.UUID, id string) (*intel.Exception, *errx.Error) {
	if xerr := s.requireEnabled(); xerr != nil {
		return nil, xerr
	}
	ex, err := intel.GetQueueItem(s.intelStore(), orgID.String(), id, time.Now().UTC())
	if err != nil {
		return nil, errx.New(errx.Internal, "commercial intel exception: "+err.Error())
	}
	if ex == nil {
		return nil, errx.New(errx.NotFound, "intel exception not found")
	}
	return ex, nil
}

// ResolveIntelException applies one legal operator action. Replay is a no-op.
func (s *service) ResolveIntelException(_ context.Context, orgID uuid.UUID, id string, req intel.ResolveRequest) (intel.ResolveResult, *errx.Error) {
	if xerr := s.requireEnabled(); xerr != nil {
		return intel.ResolveResult{}, xerr
	}
	res, err := intel.Resolve(s.intelStore(), orgID.String(), id, req, time.Now().UTC())
	if err != nil {
		return intel.ResolveResult{}, errx.New(errx.Internal, "commercial intel resolve: "+err.Error())
	}
	if res.Refused && res.Reason == "exception not found" {
		return res, errx.New(errx.NotFound, res.Reason)
	}
	if res.Refused {
		return res, errx.New(errx.Unprocessable, res.Reason)
	}
	return res, nil
}

func (s *service) ApplyCommercialOperator(_ context.Context, orgID uuid.UUID, req intel.OperatorRequest) (intel.OperatorResult, *errx.Error) {
	if xerr := s.requireEnabled(); xerr != nil {
		return intel.OperatorResult{}, xerr
	}
	return intel.ApplyOperator(s.intelStore(), orgID.String(), req), nil
}

func (s *service) GetCommercialCanonical(_ context.Context, orgID uuid.UUID, leadID string) (*intel.CanonicalState, *errx.Error) {
	if xerr := s.requireEnabled(); xerr != nil {
		return nil, xerr
	}
	can, err := intel.GetCanonical(s.intelStore(), orgID.String(), leadID)
	if err != nil {
		return nil, errx.New(errx.Internal, "commercial canonical: "+err.Error())
	}
	if can == nil {
		return nil, errx.New(errx.NotFound, "commercial canonical not found")
	}
	return can, nil
}

func (s *service) IngestProviderWebhook(_ context.Context, orgID uuid.UUID, secret, previous, header string, body []byte) (intel.WebhookAck, *errx.Error) {
	if xerr := s.requireEnabled(); xerr != nil {
		return intel.WebhookAck{}, xerr
	}
	ack, err := intel.IngestProviderWebhook(s.intelStore(), intel.NewFakeAdapter(), orgID.String(), secret, previous, header, body, time.Now().UTC())
	if err != nil && ack.ReceiptID == "" && !ack.Acked {
		if strings.Contains(err.Error(), "invalid") || strings.Contains(err.Error(), "secret") {
			return ack, errx.New(errx.Unauthorized, err.Error())
		}
		return ack, errx.New(errx.Unprocessable, err.Error())
	}
	return ack, nil
}

func (s *service) ReopenIntelException(_ context.Context, orgID uuid.UUID, id, actor, reason string) (intel.ResolveResult, *errx.Error) {
	if xerr := s.requireEnabled(); xerr != nil {
		return intel.ResolveResult{}, xerr
	}
	res, err := intel.ReopenException(s.intelStore(), orgID.String(), id, actor, reason, time.Now().UTC())
	if err != nil {
		return intel.ResolveResult{}, errx.New(errx.Internal, "commercial intel reopen: "+err.Error())
	}
	if res.Refused && res.Reason == "exception not found" {
		return res, errx.New(errx.NotFound, res.Reason)
	}
	if res.Refused {
		return res, errx.New(errx.Unprocessable, res.Reason)
	}
	return res, nil
}

func (s *service) observeExisting(ctx context.Context, orgID uuid.UUID) {
	st := s.intelStore()
	seenActions := map[string]bool{}
	if inb := s.inboundStore(); inb != nil {
		leads, err := inb.ListInboundLeads(ctx, orgID, false, 500)
		if err == nil {
			for i := range leads {
				var acc *models.OutreachAccount
				var action *models.OutreachCommercialAction
				var outcome *models.OutreachOutcome
				if leads[i].AccountID != nil {
					acc, _ = s.repo.GetAccount(ctx, orgID, *leads[i].AccountID)
				}
				if leads[i].ActionID != nil {
					if ast := s.actionStore(); ast != nil {
						action, _ = ast.GetCommercialAction(ctx, orgID, *leads[i].ActionID)
					}
				}
				outcome = outcomeByJoinIDs(ctx, s.repo, orgID, leads[i], action)
				facts := intel.ObserveFromInbound(leads[i], acc, action, outcome)
				intel.Reconcile(st, facts)
				if action != nil {
					seenActions[action.ID.String()] = true
				}
			}
		}
	}
	if ast := s.actionStore(); ast != nil {
		actions, err := ast.ListCommercialActions(ctx, orgID, uuid.Nil, false, 500)
		if err == nil {
			for i := range actions {
				if seenActions[actions[i].ID.String()] {
					continue
				}
				var acc *models.OutreachAccount
				if actions[i].AccountID != uuid.Nil {
					acc, _ = s.repo.GetAccount(ctx, orgID, actions[i].AccountID)
				}
				family := intel.Unknown
				if strings.HasPrefix(actions[i].IdempotencyKey, "inbound:") {
					family = intel.FamilyInbound
				}
				intel.Reconcile(st, intel.ObserveFromAction(actions[i], acc, family))
			}
		}
	}
}

// intelOutboxByID looks up outbox rows by durable IDs only. Email/CNPJ
// must not be used as a join key.
type intelOutboxByID interface {
	GetOutcomeBySourceLeadID(ctx context.Context, orgID uuid.UUID, sourceLeadID string) (*models.OutreachOutcome, error)
}

func outcomeByJoinIDs(ctx context.Context, repo any, orgID uuid.UUID, lead models.OutreachInboundLead, action *models.OutreachCommercialAction) *models.OutreachOutcome {
	st, ok := repo.(intelOutboxByID)
	if !ok {
		return nil
	}
	for _, id := range []string{strings.TrimSpace(lead.LeadID), strings.TrimSpace(lead.ReceiptID)} {
		if id == "" {
			continue
		}
		ev, err := st.GetOutcomeBySourceLeadID(ctx, orgID, id)
		if err != nil || ev == nil {
			continue
		}
		src := strings.TrimSpace(ev.SourceLeadID)
		if src != "" && (src == strings.TrimSpace(lead.LeadID) || src == strings.TrimSpace(lead.ReceiptID)) {
			return ev
		}
	}
	if action != nil {
		if id := strings.TrimSpace(action.SourceLeadID); id != "" && (id == strings.TrimSpace(lead.LeadID) || id == strings.TrimSpace(lead.ReceiptID)) {
			ev, err := st.GetOutcomeBySourceLeadID(ctx, orgID, id)
			if err == nil && ev != nil && strings.TrimSpace(ev.SourceLeadID) == id {
				return ev
			}
		}
	}
	return nil
}
