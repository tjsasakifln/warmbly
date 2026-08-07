package confenge

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/app/replyclassify"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
)

// Default CRM pipeline name (idempotent bootstrap).
const DefaultPipelineName = "CONFENGE Comercial"

// CRMAPI is the slice of CRM service used for pipeline/task/deal bootstrap.
type CRMAPI interface {
	ListPipelines(ctx context.Context, orgID uuid.UUID) ([]models.Pipeline, *errx.Error)
	CreatePipeline(ctx context.Context, orgID uuid.UUID, data *models.CreatePipeline) (*models.Pipeline, *errx.Error)
	CreateCRMTask(ctx context.Context, orgID, userID uuid.UUID, data *models.CreateCRMTask) (*models.CRMTask, *errx.Error)
	CreateDeal(ctx context.Context, orgID uuid.UUID, data *models.CreateDeal) (*models.Deal, *errx.Error)
}

// SuppressAPI writes platform-wide suppression (blocks campaign send).
type SuppressAPI interface {
	SuppressRecipient(ctx context.Context, orgID uuid.UUID, email, reason string) error
}

// WireCRM attaches the CRM control-plane service.
func (s *service) WireCRM(crm CRMAPI) {
	s.crm = crm
}

// WireSuppress attaches platform suppression (advanced outreach list).
func (s *service) WireSuppress(sup SuppressAPI) {
	s.suppress = sup
}

// SuppressFromAdvanced adapts advanced.Service.SuppressRecipient to SuppressAPI
// without importing advanced into every confenge call site (wiring only).
type SuppressFromAdvanced struct {
	// Fn is typically advancedService.SuppressRecipient wrapped to return error.
	Fn func(ctx context.Context, orgID uuid.UUID, email, reason string) error
}

func (a SuppressFromAdvanced) SuppressRecipient(ctx context.Context, orgID uuid.UUID, email, reason string) error {
	if a.Fn == nil {
		return nil
	}
	return a.Fn(ctx, orgID, email, reason)
}

// AdvancedSuppressor is the slice of advanced.Service used for DNC suppression.
// Kept as a narrow interface so confenge does not import advanced at compile time
// from every consumer of this package; mains pass advancedService.
type AdvancedSuppressor interface {
	SuppressRecipient(ctx context.Context, organizationID uuid.UUID, email, reason string) *errx.Error
}

// NewSuppressAdapter wraps an AdvancedSuppressor for WireSuppress.
// Returns nil when adv is nil so callers can write:
//
//	s.WireSuppress(confenge.NewSuppressAdapter(advancedService))
//
// without a nil-check; WireSuppress(nil) clears the adapter (safe no-op on DNC).
func NewSuppressAdapter(adv AdvancedSuppressor) SuppressAPI {
	if adv == nil {
		return nil
	}
	return SuppressFromAdvanced{
		Fn: func(ctx context.Context, orgID uuid.UUID, email, reason string) error {
			if xerr := adv.SuppressRecipient(ctx, orgID, email, reason); xerr != nil {
				return fmt.Errorf("%s", xerr.Message)
			}
			return nil
		},
	}
}

// confengeStages is the fixed stage list. Re-bootstrap never renames human edits
// when a pipeline with the same name already exists.
func confengeStages() []models.CreatePipelineStage {
	// Colors match dashboard slate/sky/amber/emerald conventions.
	return []models.CreatePipelineStage{
		{Name: "Novo", Color: "#94a3b8"},
		{Name: "Preparação", Color: "#64748b"},
		{Name: "Contato aprovado", Color: "#0ea5e9"},
		{Name: "Contatado", Color: "#0284c7"},
		{Name: "Respondeu", Color: "#8b5cf6"},
		{Name: "Reunião", Color: "#f59e0b"},
		{Name: "Diagnóstico", Color: "#d97706"},
		{Name: "Proposta", Color: "#ea580c"},
		{Name: "Negociação", Color: "#dc2626"},
		{Name: "Ganho", Color: "#16a34a"},
		{Name: "Perdido", Color: "#6b7280"},
		{Name: "Não contatar", Color: "#1f2937"},
	}
}

// BootstrapPipeline finds or creates the CONFENGE Comercial pipeline.
// Idempotent: existing pipeline by name is returned unchanged (preserves human edits).
func (s *service) BootstrapPipeline(ctx context.Context, orgID uuid.UUID) (*models.Pipeline, *errx.Error) {
	if xerr := s.requireEnabled(); xerr != nil {
		return nil, xerr
	}
	if s.crm == nil {
		return nil, errx.New(errx.ServiceUnavailable, "CRM service not wired")
	}
	list, xerr := s.crm.ListPipelines(ctx, orgID)
	if xerr != nil {
		return nil, xerr
	}
	for i := range list {
		if strings.EqualFold(list[i].Name, DefaultPipelineName) {
			return &list[i], nil
		}
	}
	created, xerr := s.crm.CreatePipeline(ctx, orgID, &models.CreatePipeline{
		Name:   DefaultPipelineName,
		Stages: confengeStages(),
	})
	if xerr != nil {
		return nil, xerr
	}
	// Persist pointer on org settings when available.
	if settings, err := s.repo.GetOrgSettings(ctx, orgID); err == nil {
		if settings == nil {
			settings = &models.OutreachOrgSettings{OrganizationID: orgID, CampaignName: DefaultCampaignName}
		}
		// reuse campaign_name field area; store pipeline id in raw via upsert only if we add column
		// For now pipeline is found by name — no extra column required.
		_ = settings
	}
	return created, nil
}

// MapReplyClass converts replyclassify / confenge commercial classes into
// outcome event type + optional CRM side effects (task/deal). Never auto-WON.
type ReplyCRMAction struct {
	OutcomeType string
	CreateTask  bool
	TaskTitle   string
	TaskType    string
	OpenDeal    bool // only on explicit positive interest path when contact known
	SuppressDNC bool
	QueueState  string
}

// ClassifyReplyForCRM maps classifier/commercial labels to CRM actions.
// replyClass is replyclassify class or confenge commercial class.
func ClassifyReplyForCRM(replyClass string) ReplyCRMAction {
	switch strings.ToLower(strings.TrimSpace(replyClass)) {
	case replyclassify.ClassPositive, "positive_interest":
		return ReplyCRMAction{
			OutcomeType: OutcomeReplied,
			CreateTask:  true,
			TaskTitle:   "CONFENGE: interesse positivo - acompanhar",
			TaskType:    "call",
			OpenDeal:    true,
			QueueState:  models.OutreachQueueReplied,
		}
	case replyclassify.ClassNeutral:
		return ReplyCRMAction{OutcomeType: OutcomeReplied, CreateTask: false, QueueState: models.OutreachQueueReplied}
	case "referral":
		return ReplyCRMAction{
			OutcomeType: OutcomeReplied,
			CreateTask:  true,
			TaskTitle:   "CONFENGE: encaminhamento - cadastrar contato indicado",
			TaskType:    "email",
			QueueState:  models.OutreachQueueReplied,
		}
	case "wrong_contact":
		return ReplyCRMAction{
			OutcomeType: OutcomeReplied,
			CreateTask:  true,
			TaskTitle:   "CONFENGE: contato errado - achar interlocutor",
			TaskType:    "email",
			QueueState:  models.OutreachQueueNeedsContact,
		}
	case "not_now":
		return ReplyCRMAction{
			OutcomeType: OutcomeReplied,
			CreateTask:  true,
			TaskTitle:   "CONFENGE: nao agora - follow-up futuro",
			TaskType:    "call",
			QueueState:  models.OutreachQueueReplied,
		}
	case replyclassify.ClassNegative, "no_interest":
		return ReplyCRMAction{OutcomeType: OutcomeReplied, QueueState: models.OutreachQueueSkipped}
	case replyclassify.ClassUnsubscribe, "do_not_contact", "dnc":
		return ReplyCRMAction{
			OutcomeType: OutcomeDoNotContact,
			SuppressDNC: true,
			QueueState:  models.OutreachQueueDoNotContact,
		}
	case replyclassify.ClassOutOfOffice, "ooo":
		return ReplyCRMAction{OutcomeType: OutcomeReplied, QueueState: ""} // no queue change
	case replyclassify.ClassAutoReply, "automated_reply", "bounce":
		// auto_reply covers OOO-adjacent headers; hard bounces use NoteBounce
		return ReplyCRMAction{OutcomeType: "", QueueState: ""}
	case "meeting":
		return ReplyCRMAction{
			OutcomeType: OutcomeMeeting,
			CreateTask:  true,
			TaskTitle:   "CONFENGE: reuniao marcada - preparar",
			TaskType:    "meeting",
			QueueState:  models.OutreachQueueMeeting,
		}
	case "proposal":
		return ReplyCRMAction{
			OutcomeType: OutcomeProposal,
			CreateTask:  true,
			TaskTitle:   "CONFENGE: proposta - acompanhar",
			TaskType:    "email",
			QueueState:  models.OutreachQueueProposal,
		}
	default:
		return ReplyCRMAction{OutcomeType: OutcomeReplied, QueueState: models.OutreachQueueReplied}
	}
}

// HandleClassifiedReply applies CRM + outbox side effects for a classified reply
// on a confenge-staged contact. Never marks WON automatically.
func (s *service) HandleClassifiedReply(ctx context.Context, orgID, actorID uuid.UUID, contactEmail, replyClass string, warmblyContactID *uuid.UUID) *errx.Error {
	if xerr := s.requireEnabled(); xerr != nil {
		return nil // feature off — silent
	}
	email := strings.TrimSpace(strings.ToLower(contactEmail))
	if email == "" {
		return nil
	}
	action := ClassifyReplyForCRM(replyClass)
	cand, acc, err := s.repo.FindCandidateByEmail(ctx, orgID, email)
	if err != nil {
		return errx.New(errx.Internal, "lookup candidate: "+err.Error())
	}
	if cand == nil && warmblyContactID == nil {
		return nil
	}
	cnpj, lead := "", ""
	if acc != nil {
		cnpj, lead = acc.CNPJ14, acc.SourceLeadID
		if action.QueueState != "" {
			_ = s.repo.SetAccountHumanFlags(ctx, orgID, acc.ID, acc.Blocked || action.SuppressDNC, acc.DoNotContact || action.SuppressDNC, "reply:"+replyClass, action.QueueState)
		}
	}
	if action.SuppressDNC {
		_ = s.NoteDNC(ctx, orgID, email, "reply classification: "+replyClass)
	}
	if action.OutcomeType != "" {
		_ = s.EnqueueOutcome(ctx, orgID, models.OutreachOutcome{
			IdempotencyKey: fmt.Sprintf("%s:%s:%s:%d", strings.ToLower(action.OutcomeType), orgID, email, time.Now().UTC().Truncate(time.Minute).Unix()),
			SourceLeadID:   lead,
			CNPJ14:         cnpj,
			ContactEmail:   email,
			EventType:      action.OutcomeType,
			OccurredAt:     time.Now().UTC(),
			Payload: mustJSON(map[string]any{
				"reply_class": replyClass,
			}),
		})
	}
	if s.crm == nil {
		return nil
	}
	contactID := warmblyContactID
	if contactID == nil && cand != nil {
		contactID = cand.WarmblyContactID
	}
	if contactID == nil {
		return nil
	}
	if action.CreateTask && actorID != uuid.Nil {
		_, _ = s.crm.CreateCRMTask(ctx, orgID, actorID, &models.CreateCRMTask{
			ContactID: contactID,
			Title:     action.TaskTitle,
			Type:      action.TaskType,
			Priority:  "medium",
		})
	}
	// Positive interest may open a deal in "Respondeu" stage — never Ganho.
	if action.OpenDeal {
		pipe, xerr := s.BootstrapPipeline(ctx, orgID)
		if xerr == nil && pipe != nil {
			stageID := stageIDByName(pipe, "Respondeu")
			if stageID != uuid.Nil {
				name := "CONFENGE"
				if acc != nil {
					name = firstNonEmpty(acc.NomeFantasia, acc.RazaoSocial, "CONFENGE")
				}
				_, _ = s.crm.CreateDeal(ctx, orgID, &models.CreateDeal{
					PipelineID: pipe.ID,
					StageID:    stageID,
					ContactID:  contactID,
					Name:       name,
					Currency:   "BRL",
				})
			}
		}
	}
	return nil
}

func stageIDByName(p *models.Pipeline, name string) uuid.UUID {
	if p == nil {
		return uuid.Nil
	}
	for _, st := range p.Stages {
		if strings.EqualFold(st.Name, name) {
			return st.ID
		}
	}
	return uuid.Nil
}
