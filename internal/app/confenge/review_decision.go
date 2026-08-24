package confenge

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
)

const (
	ReviewDecisionSaveAdjustment = "SAVE_ADJUSTMENT"
	ReviewDecisionApprove        = "APPROVE"
	ReviewDecisionReject         = "REJECT"
)

type ReviewDecisionInput struct {
	Action                       string  `json:"action"`
	ExpectedContentHash          string  `json:"expected_content_hash"`
	Subject                      *string `json:"subject,omitempty"`
	BodyText                     *string `json:"body_text,omitempty"`
	Reason                       string  `json:"reason,omitempty"`
	GenericRecipientAcknowledged bool    `json:"generic_recipient_acknowledged,omitempty"`
	IdempotencyKey               string  `json:"-"`
}

type ReviewDecisionResult struct {
	Touchpoint   *models.OutreachTouchpoint `json:"touchpoint"`
	ScheduledFor *time.Time                 `json:"scheduled_for,omitempty"`
}

type ReviewBatchItem struct {
	TouchpointID                 uuid.UUID `json:"touchpoint_id"`
	ExpectedContentHash          string    `json:"expected_content_hash"`
	GenericRecipientAcknowledged bool      `json:"generic_recipient_acknowledged,omitempty"`
}

type ReviewBatchItemResult struct {
	TouchpointID uuid.UUID             `json:"touchpoint_id"`
	OK           bool                  `json:"ok"`
	Result       *ReviewDecisionResult `json:"result,omitempty"`
	Error        string                `json:"error,omitempty"`
}

func (s *service) DecideReviewTouchpoint(ctx context.Context, orgID, actorID, id uuid.UUID, in ReviewDecisionInput) (*ReviewDecisionResult, *errx.Error) {
	if actorID == uuid.Nil {
		return nil, errx.ErrUnauthorized
	}
	current, err := s.repo.GetTouchpoint(ctx, orgID, id)
	if err != nil || current == nil {
		return nil, errx.New(errx.NotFound, "touchpoint not found")
	}
	expected := strings.TrimSpace(in.ExpectedContentHash)
	if expected == "" {
		return nil, errx.New(errx.BadRequest, "expected_content_hash is required")
	}
	if current.State == models.TouchpointQueued && current.ApprovedContentHash == current.ContentHash && expected == current.ApprovedContentHash {
		at := current.DueAt.UTC()
		return &ReviewDecisionResult{Touchpoint: current, ScheduledFor: &at}, nil
	}
	if current.ContentHash != expected {
		return nil, errx.New(errx.Conflict, "content hash changed; reload the draft before deciding")
	}

	action := strings.ToUpper(strings.TrimSpace(in.Action))
	switch action {
	case ReviewDecisionSaveAdjustment:
		if in.Subject == nil && in.BodyText == nil {
			return nil, errx.New(errx.BadRequest, "subject or body_text is required for SAVE_ADJUSTMENT")
		}
		tp, xerr := s.EditTouchpoint(ctx, orgID, actorID, id, in.Subject, in.BodyText, nil, nil)
		if xerr != nil {
			return nil, xerr
		}
		return &ReviewDecisionResult{Touchpoint: tp}, nil
	case ReviewDecisionReject:
		if strings.TrimSpace(in.Reason) == "" {
			return nil, errx.New(errx.BadRequest, "reason is required for REJECT")
		}
		tp, xerr := s.RejectOrSkipTouchpointReason(ctx, orgID, actorID, id, "reject", in.Reason)
		if xerr != nil {
			return nil, xerr
		}
		return &ReviewDecisionResult{Touchpoint: tp}, nil
	case ReviewDecisionApprove:
		if in.Subject != nil || in.BodyText != nil {
			var xerr *errx.Error
			current, xerr = s.EditTouchpoint(ctx, orgID, actorID, id, in.Subject, in.BodyText, nil, nil)
			if xerr != nil {
				return nil, xerr
			}
		}
		tp, xerr := s.ApproveTouchpoint(ctx, orgID, actorID, id, ApprovalOptions{
			GenericRecipientAcknowledged: in.GenericRecipientAcknowledged,
		})
		if xerr != nil {
			return nil, xerr
		}
		result := &ReviewDecisionResult{Touchpoint: tp}
		if tp.State == models.TouchpointQueued {
			at := tp.DueAt.UTC()
			result.ScheduledFor = &at
		}
		return result, nil
	default:
		return nil, errx.New(errx.BadRequest, "action must be SAVE_ADJUSTMENT, APPROVE or REJECT")
	}
}

func (s *service) ApproveReviewBatch(ctx context.Context, orgID, actorID uuid.UUID, items []ReviewBatchItem) ([]ReviewBatchItemResult, *errx.Error) {
	if len(items) == 0 || len(items) > 500 {
		return nil, errx.New(errx.BadRequest, "items must contain 1 to 500 exact draft hashes")
	}
	seen := make(map[uuid.UUID]bool, len(items))
	out := make([]ReviewBatchItemResult, 0, len(items))
	for _, item := range items {
		row := ReviewBatchItemResult{TouchpointID: item.TouchpointID}
		if item.TouchpointID == uuid.Nil || seen[item.TouchpointID] {
			row.Error = "touchpoint_id must be unique and non-empty"
			out = append(out, row)
			continue
		}
		seen[item.TouchpointID] = true
		result, xerr := s.DecideReviewTouchpoint(ctx, orgID, actorID, item.TouchpointID, ReviewDecisionInput{
			Action: ReviewDecisionApprove, ExpectedContentHash: item.ExpectedContentHash,
			GenericRecipientAcknowledged: item.GenericRecipientAcknowledged,
		})
		if xerr != nil {
			row.Error = xerr.Message
		} else {
			row.OK, row.Result = true, result
		}
		out = append(out, row)
	}
	return out, nil
}
