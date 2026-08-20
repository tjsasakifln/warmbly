package intel

import (
	"crypto/sha1"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ExceptionFilter selects durable queue rows. Empty fields match any.
type ExceptionFilter struct {
	Type     string `json:"type,omitempty"`
	Lane     string `json:"lane,omitempty"`
	Source   string `json:"source,omitempty"`
	Severity string `json:"severity,omitempty"`
	Status   string `json:"status,omitempty"`
	AgeMin   int64  `json:"age_min_seconds,omitempty"`
	AgeMax   int64  `json:"age_max_seconds,omitempty"`
}

// ResolveRequest is one operator action. Only the four named actions are legal.
type ResolveRequest struct {
	Action         string `json:"action"`
	Actor          string `json:"actor"`
	Reason         string `json:"reason"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
	LinkIdentity   string `json:"link_identity,omitempty"`
	LinkLeadID     string `json:"link_lead_id,omitempty"`
	LinkActionID   string `json:"link_action_id,omitempty"`
	LinkAccountID  string `json:"link_account_id,omitempty"`

	// Presence of any of these is an attempt to invent outcome or identity.
	OutcomeType    string `json:"outcome_type,omitempty"`
	Revenue        string `json:"revenue,omitempty"`
	Identity       string `json:"identity,omitempty"`
	HumanConfirmed *bool  `json:"human_confirmed,omitempty"`
}

// ExceptionSnapshot is the audited before/after surface.
type ExceptionSnapshot struct {
	ID         string `json:"id"`
	Status     string `json:"status"`
	Code       string `json:"code"`
	Held       bool   `json:"held"`
	NextAction string `json:"next_action"`
	Identity   string `json:"identity,omitempty"`
	OutcomeID  string `json:"outcome_id,omitempty"`
}

// ResolveResult is the shipped resolve outcome. Replay returns the first result.
type ResolveResult struct {
	Exception Exception         `json:"exception"`
	Replay    bool              `json:"replay"`
	Refused   bool              `json:"refused"`
	Reason    string            `json:"reason,omitempty"`
	Before    ExceptionSnapshot `json:"before"`
	After     ExceptionSnapshot `json:"after"`
	Actor     string            `json:"actor,omitempty"`
	Action    string            `json:"action,omitempty"`
}

// OperatorQueueNow is the pinned clock for labeled fixture presentation.
var OperatorQueueNow = time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)

// OperatorQueueOrgID is the labeled fixture organization.
const OperatorQueueOrgID = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"

func allowedResolveActions() []string {
	return []string{ResolveLink, ResolveDefer, ResolveReject, ResolveExternalEvidence}
}

func isAllowedResolve(action string) bool {
	switch strings.TrimSpace(action) {
	case ResolveLink, ResolveDefer, ResolveReject, ResolveExternalEvidence:
		return true
	default:
		return false
	}
}

func severityFor(code string) string {
	switch strings.TrimSpace(code) {
	case ExceptionOrphan, ExceptionConflictingAccount, ExceptionOutOfOrder,
		ExceptionUnconfirmedWon, ExceptionUnconfirmedLost, ExceptionUnavailable,
		ExceptionSyntheticTreatedAsReal, ExceptionGSCQueryOnLead, ExceptionQueryHashOnLead,
		ExceptionContradictorySource, ExceptionMissingConsent, ExceptionRevenueWithoutFinancial:
		return SeverityHigh
	case ExceptionMissingVersion, ExceptionStaleAttribution,
		ExceptionLeadWithoutAssetID, ExceptionUnknownAssetVersion,
		ExceptionPipelineWithoutEvidence:
		return SeverityMedium
	case ExceptionDuplicate:
		return SeverityLow
	default:
		return SeverityMedium
	}
}

func statusAfter(action string) string {
	switch action {
	case ResolveLink:
		return StatusLinked
	case ResolveDefer:
		return StatusDeferred
	case ResolveReject:
		return StatusRejected
	case ResolveExternalEvidence:
		return StatusExternalEvidence
	default:
		return StatusOpen
	}
}

func nextActionFor(status, classified string) string {
	switch status {
	case StatusLinked:
		return "linked to an existing identity; do not invent a new one"
	case StatusDeferred:
		return "deferred; remain open until a legal operator action"
	case StatusRejected:
		return "rejected; exception stays closed without an invented outcome"
	case StatusExternalEvidence:
		return "await external evidence; remain open"
	default:
		if strings.TrimSpace(classified) != "" {
			return classified
		}
		return "remain open until a legal operator action"
	}
}

func isOpenStatus(status string) bool {
	switch strings.TrimSpace(status) {
	case "", StatusOpen, StatusDeferred, StatusExternalEvidence:
		return true
	default:
		return false
	}
}

func allowedActionsFor(ex Exception) []string {
	status := strings.TrimSpace(ex.Status)
	if status == "" {
		status = StatusOpen
	}
	if !isOpenStatus(status) {
		if ex.Resolution != nil && ex.Resolution.Action != "" {
			return []string{ex.Resolution.Action}
		}
		return nil
	}
	out := []string{ResolveDefer, ResolveReject, ResolveExternalEvidence}
	if ex.Code == ExceptionOrphan {
		out = append([]string{ResolveLink}, out...)
	}
	return out
}

func actionAllowedOn(ex Exception, action string) bool {
	for _, a := range allowedActionsFor(ex) {
		if a == action {
			return true
		}
	}
	return false
}

func snapshotOf(ex Exception) ExceptionSnapshot {
	return ExceptionSnapshot{
		ID:         ex.ID,
		Status:     presentStatus(ex.Status),
		Code:       ex.Code,
		Held:       ex.Held,
		NextAction: ex.NextAction,
		Identity:   ex.Identity,
		OutcomeID:  ex.OutcomeID,
	}
}

func presentStatus(status string) string {
	if strings.TrimSpace(status) == "" {
		return StatusOpen
	}
	return status
}

func buildEvidence(ex Exception, in ObservedFacts) []EvidenceItem {
	items := []EvidenceItem{
		{Kind: "exception", Key: "code", Value: ex.Code},
		{Kind: "exception", Key: "reason", Value: ex.Reason},
		{Kind: "exception", Key: "held", Value: fmt.Sprintf("%v", ex.Held)},
	}
	add := func(kind, key, val string) {
		if strings.TrimSpace(val) == "" {
			return
		}
		items = append(items, EvidenceItem{Kind: kind, Key: key, Value: val})
	}
	add("join_id", "identity", ex.Identity)
	add("join_id", "lead_id", firstNonEmpty(ex.LeadID, in.Keys.LeadID))
	add("join_id", "receipt_id", firstNonEmpty(ex.ReceiptID, in.Keys.ReceiptID))
	add("join_id", "action_id", firstNonEmpty(ex.ActionID, in.Keys.ActionID))
	add("join_id", "outcome_id", firstNonEmpty(ex.OutcomeID, in.Keys.OutcomeID))
	add("join_id", "account_id", firstNonEmpty(ex.AccountID, in.Keys.AccountID))
	add("join_id", "metric_key", ex.MetricKey)
	add("event", "source", in.Keys.Source)
	add("event", "route_family", in.Keys.RouteFamily)
	if in.Synthetic || ex.Synthetic {
		add("event", "label", LabelSynthetic)
	}
	return items
}

func enrichException(ex *Exception, in ObservedFacts) {
	if ex == nil {
		return
	}
	if ex.Lane == "" {
		ex.Lane = normalizeFamily(in.Keys.RouteFamily)
	}
	if ex.Source == "" {
		ex.Source = idOrUnknown(in.Keys.Source)
	}
	if ex.ReceiptID == "" {
		ex.ReceiptID = strings.TrimSpace(in.Keys.ReceiptID)
	}
	ex.Severity = severityFor(ex.Code)
	if ex.Status == "" {
		ex.Status = StatusOpen
	}
	if strings.TrimSpace(ex.Owner) == "" {
		ex.Owner = ExceptionOwner(ex.Code)
	}
	if strings.TrimSpace(ex.RetryState) == "" {
		ex.RetryState = "pending"
	}
	if ex.OpenedAt.IsZero() {
		ex.OpenedAt = ex.At
	}
	if strings.TrimSpace(ex.NextAction) == "" {
		ex.NextAction = nextActionFor(ex.Status, "")
	}
	if len(ex.Evidence) == 0 {
		ex.Evidence = buildEvidence(*ex, in)
	}
	if len(ex.History) == 0 {
		at := ex.At
		if at.IsZero() {
			at = time.Now().UTC()
		}
		ex.History = []QueueEvent{{
			At:     at,
			Kind:   "classified",
			Detail: ex.Reason,
		}}
	}
	ex.AllowedActions = allowedActionsFor(*ex)
}

// PresentException fills age and defaults so old payload rows stay readable.
func PresentException(ex Exception, now time.Time) Exception {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if ex.Status == "" {
		ex.Status = StatusOpen
	}
	if ex.Severity == "" {
		ex.Severity = severityFor(ex.Code)
	}
	if ex.Lane == "" {
		ex.Lane = Unknown
	}
	if ex.Source == "" {
		ex.Source = Unknown
	}
	if strings.TrimSpace(ex.Owner) == "" {
		ex.Owner = ExceptionOwner(ex.Code)
	}
	if strings.TrimSpace(ex.NextAction) == "" {
		ex.NextAction = nextActionFor(ex.Status, "")
	}
	if !ex.At.IsZero() {
		age := int64(now.Sub(ex.At).Seconds())
		if age < 0 {
			age = 0
		}
		ex.AgeSeconds = age
	}
	ex.AllowedActions = allowedActionsFor(ex)
	return ex
}

// FilterExceptions applies type/lane/age/source/severity/status. Presentation first.
func FilterExceptions(xs []Exception, filter ExceptionFilter, now time.Time) []Exception {
	out := make([]Exception, 0, len(xs))
	wantType := strings.TrimSpace(filter.Type)
	wantLane := strings.TrimSpace(filter.Lane)
	wantSource := strings.TrimSpace(filter.Source)
	wantSev := strings.TrimSpace(filter.Severity)
	wantStatus := strings.TrimSpace(filter.Status)
	for _, ex := range xs {
		item := PresentException(ex, now)
		if wantType != "" && item.Code != wantType {
			continue
		}
		if wantLane != "" && item.Lane != wantLane {
			continue
		}
		if wantSource != "" && item.Source != wantSource {
			continue
		}
		if wantSev != "" && item.Severity != wantSev {
			continue
		}
		if wantStatus != "" && item.Status != wantStatus {
			continue
		}
		if filter.AgeMin > 0 && item.AgeSeconds < filter.AgeMin {
			continue
		}
		if filter.AgeMax > 0 && item.AgeSeconds > filter.AgeMax {
			continue
		}
		out = append(out, item)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].At.Equal(out[j].At) {
			return out[i].At.Before(out[j].At)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// ListQueue is the shipped list/filter path used by HTTP and CLI.
func ListQueue(store Store, orgID string, filter ExceptionFilter, now time.Time) ([]Exception, error) {
	if store == nil {
		return nil, fmt.Errorf("commercial intelligence store unavailable")
	}
	xs, err := store.ListExceptions(orgID)
	if err != nil {
		return nil, err
	}
	return FilterExceptions(xs, filter, now), nil
}

// GetQueueItem is the shipped detail path.
func GetQueueItem(store Store, orgID, id string, now time.Time) (*Exception, error) {
	if store == nil {
		return nil, fmt.Errorf("commercial intelligence store unavailable")
	}
	ex, err := store.GetException(orgID, id)
	if err != nil {
		return nil, err
	}
	if ex == nil {
		return nil, nil
	}
	presented := PresentException(*ex, now)
	return &presented, nil
}

func inventAttempt(req ResolveRequest) string {
	if strings.TrimSpace(req.OutcomeType) != "" {
		return "cannot invent outcome_type; UNKNOWN stays UNKNOWN"
	}
	if strings.TrimSpace(req.Revenue) != "" {
		return "cannot invent revenue"
	}
	if strings.TrimSpace(req.Identity) != "" {
		return "cannot invent identity; use link_identity of an existing chain"
	}
	if req.HumanConfirmed != nil {
		return "cannot invent human confirmation through the exception queue"
	}
	return ""
}

func sameResolveCommand(prev *ExceptionResolution, req ResolveRequest) bool {
	if prev == nil {
		return false
	}
	if prev.Action != strings.TrimSpace(req.Action) {
		return false
	}
	if k := strings.TrimSpace(req.IdempotencyKey); k != "" {
		return strings.TrimSpace(prev.IdempotencyKey) == k
	}
	return strings.TrimSpace(prev.Actor) == strings.TrimSpace(req.Actor) &&
		strings.TrimSpace(prev.Reason) == strings.TrimSpace(req.Reason)
}

func findLinkTarget(store Store, orgID string, req ResolveRequest) (Chain, error) {
	orgID = strings.TrimSpace(orgID)
	if id := strings.TrimSpace(req.LinkIdentity); id != "" {
		c, err := store.GetChain(orgID, id)
		if err != nil {
			return Chain{}, err
		}
		if c == nil {
			return Chain{}, fmt.Errorf("link target identity does not exist; will not invent one")
		}
		return *c, nil
	}
	lead := strings.TrimSpace(req.LinkLeadID)
	action := strings.TrimSpace(req.LinkActionID)
	account := strings.TrimSpace(req.LinkAccountID)
	if lead == "" && action == "" && account == "" {
		return Chain{}, fmt.Errorf("link requires an existing link_identity, link_lead_id, link_action_id, or link_account_id")
	}
	chains, err := store.ListChains(orgID)
	if err != nil {
		return Chain{}, err
	}
	var match *Chain
	for i := range chains {
		c := &chains[i]
		if lead != "" && (c.LeadID == lead || c.Keys.LeadID == lead) {
			match = c
			break
		}
		if action != "" && (c.ActionID == action || c.Keys.ActionID == action) {
			match = c
			break
		}
		if account != "" && (c.AccountID == account || c.Keys.AccountID == account) {
			match = c
			break
		}
	}
	if match == nil {
		return Chain{}, fmt.Errorf("no existing chain matches the supplied IDs; will not invent identity")
	}
	return *match, nil
}

// Resolve is the shipped resolve path. Same command replays the first result.
func Resolve(store Store, orgID, id string, req ResolveRequest, now time.Time) (ResolveResult, error) {
	if store == nil {
		return ResolveResult{Refused: true, Reason: "commercial intelligence store unavailable"}, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	req.Action = strings.TrimSpace(req.Action)
	req.Actor = strings.TrimSpace(req.Actor)
	req.Reason = strings.TrimSpace(req.Reason)
	orgID = strings.TrimSpace(orgID)
	id = strings.TrimSpace(id)

	ex, err := store.GetException(orgID, id)
	if err != nil {
		return ResolveResult{}, err
	}
	if ex == nil {
		return ResolveResult{Refused: true, Reason: "exception not found"}, nil
	}
	current := PresentException(*ex, now)
	before := snapshotOf(current)

	if msg := inventAttempt(req); msg != "" {
		current.History = append(current.History, QueueEvent{
			At: now, Kind: "refused", Actor: req.Actor, Action: req.Action, Reason: msg,
		})
		_ = store.UpdateException(current)
		return ResolveResult{
			Exception: PresentException(current, now),
			Refused:   true,
			Reason:    msg,
			Before:    before,
			After:     snapshotOf(current),
			Actor:     req.Actor,
			Action:    req.Action,
		}, nil
	}
	if !isAllowedResolve(req.Action) {
		msg := "action is not one of link, defer, reject, mark_external_evidence_required"
		return ResolveResult{
			Exception: current,
			Refused:   true,
			Reason:    msg,
			Before:    before,
			After:     before,
			Actor:     req.Actor,
			Action:    req.Action,
		}, nil
	}
	if req.Actor == "" || req.Reason == "" {
		return ResolveResult{
			Exception: current,
			Refused:   true,
			Reason:    "actor and reason are required",
			Before:    before,
			After:     before,
			Actor:     req.Actor,
			Action:    req.Action,
		}, nil
	}

	if current.Resolution != nil && (sameResolveCommand(current.Resolution, req) || current.Resolution.Action == req.Action && !isOpenStatus(current.Status)) {
		return ResolveResult{
			Exception: current,
			Replay:    true,
			Before: ExceptionSnapshot{
				ID: current.ID, Status: current.Resolution.BeforeStatus, Code: current.Code,
				Held: current.Held, NextAction: current.NextAction, Identity: current.Identity,
				OutcomeID: current.OutcomeID,
			},
			After:  snapshotOf(current),
			Actor:  current.Resolution.Actor,
			Action: current.Resolution.Action,
			Reason: current.Resolution.Reason,
		}, nil
	}

	if !actionAllowedOn(current, req.Action) {
		msg := "resolution violates lifecycle; exception stays open"
		if !isOpenStatus(current.Status) {
			msg = "exception is already resolved with a different action; replay the first command"
		}
		if req.Action == ResolveLink {
			switch current.Code {
			case ExceptionConflictingAccount:
				msg = "link would overwrite a conflicting extra-cli account; refused"
			case ExceptionOutOfOrder:
				msg = "link would invent order on an out-of-order exception; refused"
			case ExceptionUnconfirmedWon, ExceptionUnconfirmedLost:
				msg = "link cannot confirm WON/LOST; UNKNOWN stays UNKNOWN"
			}
		}
		current.History = append(current.History, QueueEvent{
			At: now, Kind: "refused", Actor: req.Actor, Action: req.Action, Reason: msg,
		})
		_ = store.UpdateException(current)
		return ResolveResult{
			Exception: PresentException(current, now),
			Refused:   true,
			Reason:    msg,
			Before:    before,
			After:     snapshotOf(current),
			Actor:     req.Actor,
			Action:    req.Action,
		}, nil
	}

	var linked Chain
	if req.Action == ResolveLink {
		linked, err = findLinkTarget(store, orgID, req)
		if err != nil {
			msg := err.Error()
			current.History = append(current.History, QueueEvent{
				At: now, Kind: "refused", Actor: req.Actor, Action: req.Action, Reason: msg,
			})
			_ = store.UpdateException(current)
			return ResolveResult{
				Exception: PresentException(current, now),
				Refused:   true,
				Reason:    msg,
				Before:    before,
				After:     snapshotOf(current),
				Actor:     req.Actor,
				Action:    req.Action,
			}, nil
		}
	}

	afterStatus := statusAfter(req.Action)
	res := ExceptionResolution{
		Action:         req.Action,
		Actor:          req.Actor,
		Reason:         req.Reason,
		At:             now,
		IdempotencyKey: strings.TrimSpace(req.IdempotencyKey),
		BeforeStatus:   before.Status,
		AfterStatus:    afterStatus,
	}
	if req.Action == ResolveLink {
		res.LinkIdentity = linked.Identity
		res.LinkLeadID = firstNonEmpty(req.LinkLeadID, linked.LeadID)
		res.LinkActionID = firstNonEmpty(req.LinkActionID, linked.ActionID)
		res.LinkAccountID = firstNonEmpty(req.LinkAccountID, linked.AccountID)
		current.LinkedIdentity = linked.Identity
	}
	current.Status = afterStatus
	current.NextAction = nextActionFor(afterStatus, "")
	current.Resolution = &res
	current.History = append(current.History, QueueEvent{
		At:     now,
		Kind:   "resolved",
		Actor:  req.Actor,
		Action: req.Action,
		Reason: req.Reason,
		Detail: "before=" + before.Status + " after=" + afterStatus,
	})
	current.AllowedActions = allowedActionsFor(current)
	if err := store.UpdateException(current); err != nil {
		return ResolveResult{}, err
	}
	presented := PresentException(current, now)
	return ResolveResult{
		Exception: presented,
		Before:    before,
		After:     snapshotOf(presented),
		Actor:     req.Actor,
		Action:    req.Action,
		Reason:    req.Reason,
	}, nil
}

// StableExceptionID is a deterministic UUID so classify/fixture replay is stable.
func StableExceptionID(parts ...string) string {
	sum := sha1.Sum([]byte(strings.Join(parts, "\x00")))
	id, err := uuid.FromBytes(sum[:16])
	if err != nil {
		return uuid.NewSHA1(uuid.NameSpaceOID, sum[:]).String()
	}
	id[6] = (id[6] & 0x0f) | 0x50
	id[8] = (id[8] & 0x3f) | 0x80
	return id.String()
}

func assignExceptionID(ex Exception) Exception {
	if strings.TrimSpace(ex.ID) == "" {
		ex.ID = StableExceptionID(ex.OrganizationID, ex.Code, ex.Identity, ex.MetricKey, ex.Reason, ex.LeadID, ex.ActionID, ex.OutcomeID)
	}
	return ex
}
