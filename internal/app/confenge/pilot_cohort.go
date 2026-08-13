package confenge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
)

const PilotCohortTarget = 30

const (
	PilotPrepared = "PREPARED"
	PilotBlocked  = "BLOCKED"
)

type PilotOperation struct {
	IdempotencyKey string
	RequestID      string
}

type PilotAccountResult struct {
	AccountID     uuid.UUID  `json:"account_id"`
	CNPJ14        string     `json:"cnpj14,omitempty"`
	Company       string     `json:"company,omitempty"`
	Status        string     `json:"status"`
	ReasonCode    string     `json:"reason_code,omitempty"`
	Reason        string     `json:"human_readable_reason,omitempty"`
	Remediation   string     `json:"remediation,omitempty"`
	PreviousState string     `json:"previous_state,omitempty"`
	IntendedState string     `json:"intended_state"`
	ContactState  string     `json:"contact_state,omitempty"`
	Recipient     string     `json:"recipient,omitempty"`
	RecipientName string     `json:"recipient_name,omitempty"`
	RecipientRole string     `json:"recipient_role,omitempty"`
	ContactID     *uuid.UUID `json:"contact_candidate_id,omitempty"`
	TouchpointID  *uuid.UUID `json:"touchpoint_id,omitempty"`
	DraftID       *uuid.UUID `json:"draft_id,omitempty"`
	DraftState    string     `json:"draft_state,omitempty"`
	Warnings      []string   `json:"warnings,omitempty"`
	SnapshotHash  string     `json:"upstream_snapshot_hash,omitempty"`
	ContextHash   string     `json:"message_context_hash,omitempty"`
	PreparedAt    *time.Time `json:"prepared_at,omitempty"`
	Idempotent    bool       `json:"idempotent"`
}

type PilotCohortResult struct {
	CohortID       string               `json:"cohort_id"`
	Target         int                  `json:"target"`
	Selected       int                  `json:"selected"`
	Prepared       int                  `json:"prepared"`
	Blocked        int                  `json:"blocked"`
	ContactNeeded  int                  `json:"contact_needed"`
	CohortPrepared int                  `json:"cohort_prepared"`
	Remaining      int                  `json:"remaining"`
	SnapshotHash   string               `json:"upstream_snapshot_hash,omitempty"`
	FeedTimestamp  *time.Time           `json:"feed_timestamp,omitempty"`
	Results        []PilotAccountResult `json:"results"`
}

type pilotFeedEvidence struct {
	State        string
	SnapshotHash string
	Timestamp    *time.Time
}

func (s *service) PreparePilotCohort(ctx context.Context, orgID, userID uuid.UUID, accountIDs []uuid.UUID, operation PilotOperation) (*PilotCohortResult, *errx.Error) {
	if xerr := s.requireEnabled(); xerr != nil {
		return nil, xerr
	}
	if !s.cfg.RequireHumanApproval || s.cfg.AutoSendEnabled {
		return nil, errx.New(errx.Conflict, "pilot safety configuration requires human approval and auto_send=false")
	}
	unique := uniqueAccountIDs(accountIDs)
	if len(unique) == 0 || len(unique) > PilotCohortTarget {
		return nil, errx.New(errx.BadRequest, "account_ids must contain between 1 and 30 unique accounts")
	}
	feed := s.pilotFeedEvidence(ctx, orgID, time.Now().UTC())
	cohortID := uuid.NewSHA1(orgID, []byte("confenge-pilot-v1")).String()
	result := &PilotCohortResult{
		CohortID: cohortID, Target: PilotCohortTarget, Selected: len(unique),
		SnapshotHash: feed.SnapshotHash, FeedTimestamp: feed.Timestamp,
		Results: make([]PilotAccountResult, 0, len(unique)),
	}
	existingMembers := s.pilotPreparedAccounts(ctx, orgID)

	for _, accountID := range unique {
		accountResult := s.preparePilotAccount(ctx, orgID, userID, accountID, operation, feed, cohortID, existingMembers)
		result.Results = append(result.Results, accountResult)
		if accountResult.Status == PilotPrepared {
			result.Prepared++
			existingMembers[accountID] = true
		} else {
			result.Blocked++
			if strings.HasPrefix(accountResult.ReasonCode, "recipient_") || accountResult.ReasonCode == "provenance_tainted" || accountResult.ReasonCode == "generic_mailbox_not_allowed" {
				result.ContactNeeded++
			}
		}
	}
	result.CohortPrepared = len(existingMembers)
	if result.CohortPrepared > PilotCohortTarget {
		result.CohortPrepared = PilotCohortTarget
	}
	result.Remaining = PilotCohortTarget - result.CohortPrepared
	return result, nil
}

func (s *service) preparePilotAccount(
	ctx context.Context,
	orgID, userID, accountID uuid.UUID,
	operation PilotOperation,
	feed pilotFeedEvidence,
	cohortID string,
	existingMembers map[uuid.UUID]bool,
) PilotAccountResult {
	now := time.Now().UTC()
	result := PilotAccountResult{
		AccountID: accountID, Status: PilotBlocked, IntendedState: models.TouchpointNeedsReview,
		SnapshotHash: feed.SnapshotHash,
	}
	acc, err := s.repo.GetAccount(ctx, orgID, accountID)
	if err != nil || acc == nil {
		return s.finishPilotResult(ctx, orgID, userID, cohortID, operation, result, pilotBlock{Code: "account_not_found", Reason: "A conta selecionada não existe mais.", Remediation: "Atualize a lista e selecione novamente."})
	}
	result.CNPJ14 = acc.CNPJ14
	result.Company = firstNonEmpty(acc.NomeFantasia, acc.RazaoSocial)
	result.PreviousState = acc.QueueState
	result.ContextHash = acc.MessageContextHash

	switch feed.State {
	case "missing":
		return s.finishPilotResult(ctx, orgID, userID, cohortID, operation, result, pilotBlock{Code: "feed_missing", Reason: "Nenhum snapshot autoritativo sincronizado está disponível.", Remediation: "Sincronize o feed CONFENGE antes de preparar a coorte."})
	case "stale":
		return s.finishPilotResult(ctx, orgID, userID, cohortID, operation, result, pilotBlock{Code: "feed_stale", Reason: "O snapshot autoritativo está obsoleto.", Remediation: "Sincronize um snapshot atual e tente novamente."})
	}
	if acc.DoNotContact {
		return s.finishPilotResult(ctx, orgID, userID, cohortID, operation, result, pilotBlock{Code: "account_do_not_contact", Reason: "A conta está marcada como não contatar.", Remediation: "Mantenha a supressão e escolha outra conta."})
	}
	if acc.Blocked {
		return s.finishPilotResult(ctx, orgID, userID, cohortID, operation, result, pilotBlock{Code: "account_blocked", Reason: "A conta está bloqueada para outreach.", Remediation: "Revise o bloqueio antes de qualquer nova preparação."})
	}
	if decision := EvaluateTargetFit(acc); !decision.Eligible {
		code := "account_ineligible"
		if !acc.TargetFitFresh || decision.Reason == TargetFitReasonStale {
			code = "target_fit_stale"
		}
		return s.finishPilotResult(ctx, orgID, userID, cohortID, operation, result, pilotBlock{Code: code, Reason: "A conta não atende mais aos gates de target fit.", Remediation: "Revise os reason codes e aguarde nova evidência elegível."})
	}
	if acc.ActivationExpiresAt != nil && !acc.ActivationExpiresAt.After(now) {
		return s.finishPilotResult(ctx, orgID, userID, cohortID, operation, result, pilotBlock{Code: "commercial_context_expired", Reason: "O contexto comercial desta conta expirou.", Remediation: "Atualize a evidência comercial antes de gerar a mensagem."})
	}
	candidates, err := s.repo.ListCandidates(ctx, orgID, accountID)
	if err != nil {
		return s.finishPilotResult(ctx, orgID, userID, cohortID, operation, result, pilotBlock{Code: "recipient_lookup_failed", Reason: "Não foi possível validar os destinatários desta conta.", Remediation: "Tente novamente usando o mesmo Idempotency-Key."})
	}
	recipient, recipientBlock := resolvePilotRecipient(candidates, now)
	if recipientBlock != nil {
		result.ContactState = models.OutreachQueueNeedsContact
		return s.finishPilotResult(ctx, orgID, userID, cohortID, operation, result, *recipientBlock)
	}
	result.ContactState = "READY"
	result.Recipient = recipient.Candidate.Email
	result.RecipientName = recipient.Candidate.Name
	result.RecipientRole = recipient.Candidate.Role
	result.ContactID = &recipient.Candidate.ID
	result.Warnings = recipient.Warnings

	evidence, err := s.repo.ListEvidence(ctx, orgID, accountID)
	if err != nil {
		return s.finishPilotResult(ctx, orgID, userID, cohortID, operation, result, pilotBlock{Code: "copy_evidence_unavailable", Reason: "Não foi possível carregar as evidências da conta.", Remediation: "Tente novamente usando o mesmo Idempotency-Key."})
	}
	if block := pilotCopyContextBlock(acc, recipient.Candidate, evidence); block != nil {
		return s.finishPilotResult(ctx, orgID, userID, cohortID, operation, result, *block)
	}

	if existing, ok := s.existingPilotTouchpoint(ctx, orgID, accountID); ok && existing.DraftID != nil {
		if existing.GeneratedContextHash == acc.MessageContextHash && existing.ContactCandidateID != nil && *existing.ContactCandidateID == recipient.Candidate.ID {
			result.Status = PilotPrepared
			result.TouchpointID = &existing.ID
			result.DraftID = existing.DraftID
			result.DraftState = existing.State
			result.PreparedAt = &existing.UpdatedAt
			result.Idempotent = true
			return s.finishPilotResult(ctx, orgID, userID, cohortID, operation, result, pilotBlock{})
		}
	}
	if len(existingMembers) >= PilotCohortTarget && !existingMembers[accountID] {
		return s.finishPilotResult(ctx, orgID, userID, cohortID, operation, result, pilotBlock{Code: "cohort_capacity_reached", Reason: "A coorte piloto já possui 30 contas.", Remediation: "Revise a coorte existente em vez de adicionar outra conta."})
	}

	touchpoints, xerr := s.PlanAccountCadence(ctx, orgID, userID, accountID, &recipient.Candidate.ID, models.OutreachChannelEmail)
	if xerr != nil {
		return s.finishPilotResult(ctx, orgID, userID, cohortID, operation, result, pilotBlockFromError(xerr.Message))
	}
	first := firstPilotTouchpoint(touchpoints)
	if first == nil {
		return s.finishPilotResult(ctx, orgID, userID, cohortID, operation, result, pilotBlock{Code: "cohort_membership_failed", Reason: "A primeira etapa da conta não foi persistida.", Remediation: "Tente novamente usando o mesmo Idempotency-Key."})
	}
	if first.ContactCandidateID == nil || *first.ContactCandidateID != recipient.Candidate.ID {
		if xerr := s.rebindPilotCadence(ctx, touchpoints, recipient.Candidate); xerr != nil {
			return s.finishPilotResult(ctx, orgID, userID, cohortID, operation, result, *xerr)
		}
		first = firstPilotTouchpoint(touchpoints)
	}
	generated, xerr := s.GenerateTouchpointDraft(ctx, orgID, userID, first.ID)
	if xerr != nil {
		return s.finishPilotResult(ctx, orgID, userID, cohortID, operation, result, pilotBlockFromError(xerr.Message))
	}
	if generated.State != models.TouchpointNeedsReview || generated.DraftID == nil {
		return s.finishPilotResult(ctx, orgID, userID, cohortID, operation, result, pilotBlock{Code: "invalid_review_state", Reason: "A mensagem não terminou na fila de revisão.", Remediation: "Não prossiga com envio; revise a inconsistência operacional."})
	}
	result.Status = PilotPrepared
	result.TouchpointID = &generated.ID
	result.DraftID = generated.DraftID
	result.DraftState = generated.State
	result.PreparedAt = &generated.UpdatedAt
	return s.finishPilotResult(ctx, orgID, userID, cohortID, operation, result, pilotBlock{})
}

func (s *service) finishPilotResult(ctx context.Context, orgID, userID uuid.UUID, cohortID string, operation PilotOperation, result PilotAccountResult, block pilotBlock) PilotAccountResult {
	if result.Status != PilotPrepared {
		result.ReasonCode = block.Code
		result.Reason = block.Reason
		result.Remediation = block.Remediation
	}
	gate := result.ReasonCode
	if gate == "" {
		gate = "all_gates_passed"
	}
	slog.InfoContext(ctx, "confenge pilot cohort account result",
		"request_id", operation.RequestID, "idempotency_key", operation.IdempotencyKey,
		"user_id", userID, "organization_id", orgID, "cohort_id", cohortID,
		"account_id", result.AccountID, "cnpj14", result.CNPJ14,
		"previous_state", result.PreviousState, "intended_state", result.IntendedState,
		"status", result.Status, "gate", gate, "reason_code", result.ReasonCode,
		"upstream_snapshot_hash", result.SnapshotHash,
	)
	if s.audit != nil && result.AccountID != uuid.Nil {
		s.audit.LogAction(ctx, orgID, userID, models.AuditActionCreate, models.AuditEntityOutreachAccount, &result.AccountID, "", "",
			map[string]string{"cohort_id": cohortID, "status": result.Status, "reason_code": result.ReasonCode},
			map[string]string{"request_id": operation.RequestID, "snapshot_hash": result.SnapshotHash},
		)
	}
	return result
}

func (s *service) pilotFeedEvidence(ctx context.Context, orgID uuid.UUID, now time.Time) pilotFeedEvidence {
	maxAge := s.cfg.FeedMaxAge
	if maxAge <= 0 {
		maxAge = 24 * time.Hour
	}
	if state, err := s.repo.GetFeedSyncState(ctx, orgID); err == nil && state != nil && state.LastSuccessAt != nil {
		value := pilotFeedEvidence{State: "fresh", SnapshotHash: state.LastSnapshotHash, Timestamp: state.LastSuccessAt}
		if now.Sub(state.LastSuccessAt.UTC()) > maxAge {
			value.State = "stale"
		}
		return value
	}
	if runs, err := s.repo.ListImportRuns(ctx, orgID, 1); err == nil && len(runs) > 0 {
		timestamp := runs[0].FinishedAt
		if timestamp == nil {
			timestamp = &runs[0].StartedAt
		}
		value := pilotFeedEvidence{State: "fresh", SnapshotHash: runs[0].SnapshotHash, Timestamp: timestamp}
		if timestamp == nil || now.Sub(timestamp.UTC()) > maxAge {
			value.State = "stale"
		}
		return value
	}
	return pilotFeedEvidence{State: "missing"}
}

func (s *service) pilotPreparedAccounts(ctx context.Context, orgID uuid.UUID) map[uuid.UUID]bool {
	result := map[uuid.UUID]bool{}
	touchpoints, err := s.repo.ListReviewTouchpoints(ctx, orgID, 200, 0)
	if err != nil {
		return result
	}
	for i := range touchpoints {
		touchpoint := &touchpoints[i]
		if touchpoint.Ordinal == 1 && touchpoint.DraftID != nil && (touchpoint.State == models.TouchpointNeedsReview || touchpoint.State == models.TouchpointApproved) {
			result[touchpoint.AccountID] = true
		}
	}
	return result
}

func (s *service) existingPilotTouchpoint(ctx context.Context, orgID, accountID uuid.UUID) (*models.OutreachTouchpoint, bool) {
	touchpoints, err := s.repo.ListTouchpoints(ctx, orgID, accountID, "", 50, 0)
	if err != nil {
		return nil, false
	}
	first := firstPilotTouchpoint(touchpoints)
	return first, first != nil
}

func (s *service) rebindPilotCadence(ctx context.Context, touchpoints []models.OutreachTouchpoint, candidate *models.OutreachContactCandidate) *pilotBlock {
	for i := range touchpoints {
		touchpoint := &touchpoints[i]
		if models.TouchpointTerminalStates[touchpoint.State] || touchpoint.State == models.TouchpointSent {
			return &pilotBlock{Code: "cohort_state_conflict", Reason: "A conta já possui uma etapa terminal com outro destinatário.", Remediation: "Revise a linha do tempo antes de alterar o destinatário."}
		}
		touchpoint.ContactCandidateID = &candidate.ID
		ApplyContentMutation(touchpoint, models.OutreachChannelEmail, candidate.Email, touchpoint.Subject, touchpoint.BodyText)
		if err := s.repo.UpdateTouchpoint(ctx, touchpoint); err != nil {
			return &pilotBlock{Code: "cohort_membership_failed", Reason: "Não foi possível atualizar o destinatário da coorte.", Remediation: "Tente novamente usando o mesmo Idempotency-Key."}
		}
	}
	return nil
}

func pilotCopyContextBlock(acc *models.OutreachAccount, candidate *models.OutreachContactCandidate, evidence []models.OutreachEvidence) *pilotBlock {
	playbook, _ := LoadPlaybook()
	strategy := PlanOutreachStrategy(playbook, acc, candidate, evidence, 1)
	incomplete := strings.TrimSpace(strategy.MicroOfferCode) == "" || strings.TrimSpace(strategy.WhyThisAccount) == "" ||
		strings.TrimSpace(strategy.WhyNow) == "" || strings.TrimSpace(strategy.ObservedFact) == "" ||
		containsStr(strategy.RiskFlags, "incomplete_strategy") || containsStr(strategy.RiskFlags, "incomplete_copy_context") ||
		containsStr(strategy.RiskFlags, "unknown_service_code") || containsStr(strategy.RiskFlags, "missing_service_code")
	if incomplete {
		return &pilotBlock{Code: "incomplete_copy_context", Reason: "Não há contexto comercial suficiente para uma mensagem factual.", Remediation: "Atualize serviço, oferta e evidência específica da conta antes de gerar."}
	}
	return nil
}

func pilotBlockFromError(message string) pilotBlock {
	lower := strings.ToLower(strings.TrimSpace(message))
	switch {
	case strings.Contains(lower, "email_send_ready"):
		return pilotBlock{Code: "recipient_not_send_ready", Reason: "O destinatário não está validado para email comercial.", Remediation: "Revalide o contato e sincronize o feed."}
	case strings.Contains(lower, "contact candidate") || strings.Contains(lower, "contact is not enrollable"):
		return pilotBlock{Code: "recipient_invalid", Reason: "O destinatário não atende aos gates de contato.", Remediation: "Resolva outro contato corporativo validado."}
	case strings.Contains(lower, "target fit") || strings.Contains(lower, "commercial outreach blocked"):
		return pilotBlock{Code: "account_ineligible", Reason: "A conta não atende aos gates de target fit.", Remediation: "Revise a elegibilidade antes de tentar novamente."}
	case strings.Contains(lower, "incomplete") || strings.Contains(lower, "service"):
		return pilotBlock{Code: "incomplete_copy_context", Reason: "O contexto comercial não sustenta uma mensagem factual.", Remediation: "Atualize a evidência e o serviço selecionado."}
	default:
		return pilotBlock{Code: "preparation_failed", Reason: "A preparação falhou antes de criar uma mensagem revisável.", Remediation: "Use o request_id nos logs e tente novamente com o mesmo Idempotency-Key."}
	}
}

func firstPilotTouchpoint(touchpoints []models.OutreachTouchpoint) *models.OutreachTouchpoint {
	for i := range touchpoints {
		if touchpoints[i].Ordinal == 1 {
			return &touchpoints[i]
		}
	}
	return nil
}

func uniqueAccountIDs(values []uuid.UUID) []uuid.UUID {
	seen := make(map[uuid.UUID]bool, len(values))
	result := make([]uuid.UUID, 0, len(values))
	for _, value := range values {
		if value == uuid.Nil || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

func pilotOperationHash(accountIDs []uuid.UUID) string {
	values := make([]string, 0, len(accountIDs))
	for _, accountID := range accountIDs {
		values = append(values, accountID.String())
	}
	sort.Strings(values)
	sum := sha256.Sum256([]byte(strings.Join(values, ",")))
	return hex.EncodeToString(sum[:])
}
