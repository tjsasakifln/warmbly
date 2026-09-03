package confenge

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
)

// The Founder Interrupt Budget.
//
// This is a projection over the commercial-action rows the Control Center
// already owns, not a new dashboard and not a new entity. Its whole job is to
// answer one question without the founder having to know to look: who is
// waiting on a person right now, and which engine produced them.
//
// The fourth bucket is the important one. A hand-raiser with no next action is
// invisible in every due-date-ordered view precisely because it has no due
// date, so it is the one that silently rots. It is surfaced first.

// InterruptBucket is the closed set of reasons a founder is being interrupted.
type InterruptBucket string

const (
	// BucketNoNextAction: a hand-raiser nobody has committed to yet.
	BucketNoNextAction InterruptBucket = "hand_raiser_without_next_action"
	// BucketMeetingOrProposal: an explicit ask to meet or to be quoted.
	BucketMeetingOrProposal InterruptBucket = "meeting_or_proposal_request"
	// BucketReplyAwaitingHuman: a reply from a prospect that needs an answer.
	BucketReplyAwaitingHuman InterruptBucket = "reply_awaiting_human"
	// BucketReviewRequest: something asking a human to look before it moves.
	BucketReviewRequest InterruptBucket = "review_request"
)

// InterruptBuckets is the closed set in surfacing order: most silently
// dangerous first, not most numerous first.
var InterruptBuckets = []InterruptBucket{
	BucketNoNextAction, BucketMeetingOrProposal, BucketReplyAwaitingHuman, BucketReviewRequest,
}

// InterruptItem is one thing waiting on a person.
type InterruptItem struct {
	ActionID    uuid.UUID       `json:"action_id"`
	AccountID   uuid.UUID       `json:"account_id"`
	CandidateID *uuid.UUID      `json:"candidate_id,omitempty"`
	Bucket      InterruptBucket `json:"bucket"`
	// EngineLane is which acquisition engine produced this. Empty means the
	// signal predates engine attribution; it is never guessed.
	EngineLane     string     `json:"engine_lane"`
	PersonName     string     `json:"person_name,omitempty"`
	CompanyName    string     `json:"company_name,omitempty"`
	Lane           string     `json:"lane,omitempty"`
	State          string     `json:"state"`
	NextActionType string     `json:"next_action_type,omitempty"`
	NextActionAt   *time.Time `json:"next_action_at,omitempty"`
	// Overdue is true only when a due time exists and has passed. A row with no
	// due time is never "overdue"; it is BucketNoNextAction, which is worse.
	Overdue   bool      `json:"overdue"`
	WhyNow    string    `json:"why_now,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	// WaitingSeconds is how long a human has been the blocker.
	WaitingSeconds int64 `json:"waiting_seconds"`
}

// InterruptBudget is the whole projection: counts per bucket, counts per
// engine, and the items themselves.
type InterruptBudget struct {
	GeneratedAt time.Time `json:"generated_at"`
	Total       int       `json:"total"`
	// ByBucket and ByEngine are always fully populated, including zeros, so a
	// reader can tell "no hand-raisers from INTEL_SEED" from "INTEL_SEED is not
	// a thing this projection knows about".
	ByBucket map[InterruptBucket]int `json:"by_bucket"`
	ByEngine map[string]int          `json:"by_engine"`
	// Unattributed counts hand-raisers with no engine of origin. Kept separate
	// from ByEngine so an aggregate can never quietly absorb them.
	Unattributed int             `json:"unattributed"`
	Oldest       *time.Time      `json:"oldest_waiting_since,omitempty"`
	Items        []InterruptItem `json:"items"`
}

// interruptBudgetScanLimit bounds how many open actions the projection reads.
// The Control Center is a working queue, not an archive.
const interruptBudgetScanLimit = 500

// FounderInterruptBudget projects everything currently waiting on a human.
//
// It is read-only: it reserves nothing, sends nothing and mutates no row. It
// reads the same open commercial actions the cockpit already lists.
func (s *service) FounderInterruptBudget(ctx context.Context, orgID uuid.UUID, limit int) (*InterruptBudget, *errx.Error) {
	if xerr := s.requireEnabled(); xerr != nil {
		return nil, xerr
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	store := s.actionStore()
	if store == nil {
		return nil, errx.New(errx.Internal, "commercial action store is not wired")
	}
	// openOnly: a completed or skipped action is not an interruption.
	actions, err := store.ListCommercialActions(ctx, orgID, uuid.Nil, true, interruptBudgetScanLimit)
	if err != nil {
		return nil, errx.New(errx.Internal, "failed to project the founder interrupt budget: "+err.Error())
	}
	now := s.now()
	budget := &InterruptBudget{
		GeneratedAt: now,
		ByBucket:    map[InterruptBucket]int{},
		ByEngine:    map[string]int{},
	}
	for _, bucket := range InterruptBuckets {
		budget.ByBucket[bucket] = 0
	}
	for _, engine := range EngineLanes {
		budget.ByEngine[engine] = 0
	}

	var items []InterruptItem
	for i := range actions {
		action := &actions[i]
		bucket, ok := classifyInterrupt(action)
		if !ok {
			continue
		}
		item := InterruptItem{
			ActionID: action.ID, AccountID: action.AccountID, CandidateID: action.CandidateID,
			Bucket: bucket, EngineLane: NormalizeEngineLane(action.EngineLane),
			PersonName: action.PersonName, CompanyName: action.CompanyName,
			Lane: action.Lane, State: action.State,
			NextActionType: action.NextActionType, NextActionAt: action.NextActionAt,
			WhyNow: action.WhyNow, CreatedAt: action.CreatedAt,
		}
		if action.NextActionAt != nil && action.NextActionAt.Before(now) {
			item.Overdue = true
		}
		if !action.CreatedAt.IsZero() {
			item.WaitingSeconds = int64(now.Sub(action.CreatedAt).Seconds())
			if budget.Oldest == nil || action.CreatedAt.Before(*budget.Oldest) {
				at := action.CreatedAt
				budget.Oldest = &at
			}
		}
		budget.ByBucket[bucket]++
		if item.EngineLane == EngineLaneUnattributed {
			budget.Unattributed++
		} else {
			budget.ByEngine[item.EngineLane]++
		}
		items = append(items, item)
	}
	budget.Total = len(items)

	// Surface order: bucket severity first, then how long a person has been the
	// blocker. Overdue beats not-overdue within a bucket.
	sort.SliceStable(items, func(i, j int) bool {
		bi, bj := bucketRank(items[i].Bucket), bucketRank(items[j].Bucket)
		if bi != bj {
			return bi < bj
		}
		if items[i].Overdue != items[j].Overdue {
			return items[i].Overdue
		}
		return items[i].CreatedAt.Before(items[j].CreatedAt)
	})
	if len(items) > limit {
		items = items[:limit]
	}
	budget.Items = items
	return budget, nil
}

func bucketRank(bucket InterruptBucket) int {
	for i, known := range InterruptBuckets {
		if known == bucket {
			return i
		}
	}
	return len(InterruptBuckets)
}

// classifyInterrupt decides whether one action is waiting on a human, and why.
// It reads only facts already on the row; it infers nothing about the person.
func classifyInterrupt(action *models.OutreachCommercialAction) (InterruptBucket, bool) {
	if action == nil {
		return "", false
	}
	switch action.State {
	case models.ActionStateCompleted, models.ActionStateSkipped, models.ActionStateFailed:
		return "", false
	}
	// A hand-raiser nobody committed to. Checked first: it is the one no
	// due-date view can show, so nothing else may claim it.
	if HandRaiseHasNoNextAction(action) {
		return BucketNoNextAction, true
	}
	next := strings.ToUpper(strings.TrimSpace(action.NextActionType))
	switch next {
	case models.OutcomeMeetingScheduled, models.OutcomeProposalCode:
		return BucketMeetingOrProposal, true
	}
	// A prospect actually talked to us and is waiting.
	if action.ConversationStarted || next == models.OutcomeInterested ||
		strings.EqualFold(strings.TrimSpace(action.OutcomeCode), models.OutcomeRepliedCode) {
		return BucketReplyAwaitingHuman, true
	}
	// Our own machinery asking a human to look before anything moves.
	switch strings.ToUpper(strings.TrimSpace(action.Lane)) {
	case models.LaneEmailNeedsReview, models.LaneHumanReviewEmail, models.LaneLowConfidenceManual:
		return BucketReviewRequest, true
	}
	if action.ActionType == models.ActionInferredEmailReview {
		return BucketReviewRequest, true
	}
	// An unconverged hand-raiser that fell through every branch above is still
	// waiting on a person; never drop it silently.
	if HandRaiseAwaitsHuman(action) {
		return BucketReviewRequest, true
	}
	return "", false
}
