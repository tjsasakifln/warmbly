package confenge

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

// CohortAuthorizeResult is the fail-closed apply report.
type CohortAuthorizeResult struct {
	AuthorizationID       uuid.UUID          `json:"authorization_id"`
	SelectedAccounts      int                `json:"selected_accounts"`
	AuthorizedTouchpoints int                `json:"authorized_touchpoints"`
	ExcludedBeforeFreeze  int                `json:"excluded_before_freeze"`
	FailedAuthorization   int                `json:"failed_authorization"`
	PostFreeze            int                `json:"post_freeze"`
	DuplicateRoutes       int                `json:"duplicate_routes"`
	Failures              []CohortMemberFail `json:"failures,omitempty"`
	Idempotent            bool               `json:"idempotent"`
	RealEmailSent         bool               `json:"real_email_sent"`
	AutoSendEnabled       bool               `json:"auto_send_enabled"`
}

type CohortMemberFail struct {
	AccountRef string `json:"account_ref"`
	Mailbox    string `json:"mailbox,omitempty"`
	Reason     string `json:"reason"`
}

type cohortApplyWork struct {
	member FrozenCohortMember
	tp     *models.OutreachTouchpoint
	create bool
}

// AuthorizeFrozenCohort persists the grant (when confirm) and applies
// BOUNDED_COHORT_AUTHORIZATION to every frozen member. Partial apply is
// rejected: if any member fails, nothing is authorized.
func AuthorizeFrozenCohort(
	ctx context.Context,
	store BoundedCohortStore,
	repo repository.OutreachRepository,
	orgID, actor uuid.UUID,
	snap *FrozenCohortSnapshot,
	confirm bool,
	now time.Time,
) (*CohortAuthorizeResult, error) {
	if snap == nil {
		return nil, fmt.Errorf("frozen cohort required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if snap.AutoSendEnabled || snap.GreenAutorunEnabled || snap.RealEmailSent {
		return nil, fmt.Errorf("frozen cohort cannot enable auto-send, green autorun, or claim a real send")
	}
	out := &CohortAuthorizeResult{
		SelectedAccounts:     len(snap.Members),
		ExcludedBeforeFreeze: len(snap.Exclusions),
		RealEmailSent:        false,
		AutoSendEnabled:      false,
	}
	dup := map[string]int{}
	for _, m := range snap.Members {
		dup[m.AccountRef]++
		if dup[m.AccountRef] > 1 {
			out.DuplicateRoutes++
		}
	}
	if out.DuplicateRoutes > 0 {
		return out, fmt.Errorf("account has more than one initial route")
	}

	auth, err := GrantFromFrozenSnapshot(snap, orgID, actor, now)
	if err != nil {
		return out, err
	}

	works, fails := planCohortApply(ctx, repo, orgID, snap, now)
	out.Failures = fails
	out.FailedAuthorization = len(fails)
	if len(fails) > 0 {
		return out, fmt.Errorf("partial cohort authorization refused: %d member(s) failed", len(fails))
	}
	if !confirm {
		out.AuthorizedTouchpoints = 0
		return out, ErrCohortNotConfirmed
	}
	if store == nil {
		return out, ErrCohortStoreUnavailable
	}

	cp := *snap
	cp.AuthorizationID = auth.ID
	auth.FrozenManifest = &cp
	got, err := CreateBoundedCohortGrant(ctx, store, auth, true)
	if err != nil {
		return out, err
	}
	out.AuthorizationID = got.ID
	snap.AuthorizationID = got.ID

	applied := 0
	for i := range works {
		w := works[i]
		if err := ApplyBoundedCohortAuthorization(w.tp, got, now); err != nil {
			_ = store.RevokeGrant(ctx, got.ID, actor, "partial_apply_rollback:"+err.Error(), now)
			out.FailedAuthorization = 1
			out.Failures = append(out.Failures, CohortMemberFail{AccountRef: w.member.AccountRef, Mailbox: w.member.Mailbox, Reason: err.Error()})
			return out, fmt.Errorf("partial cohort authorization refused: %s", err)
		}
		if repo != nil {
			if w.create {
				if err := repo.InsertTouchpoint(ctx, w.tp); err != nil {
					_ = store.RevokeGrant(ctx, got.ID, actor, "partial_apply_rollback:insert", now)
					return out, fmt.Errorf("partial cohort authorization refused: persist touchpoint: %w", err)
				}
			} else if err := repo.UpdateTouchpoint(ctx, w.tp); err != nil {
				_ = store.RevokeGrant(ctx, got.ID, actor, "partial_apply_rollback:update", now)
				return out, fmt.Errorf("partial cohort authorization refused: persist touchpoint: %w", err)
			}
		}
		applied++
	}
	out.AuthorizedTouchpoints = applied
	return out, nil
}

func planCohortApply(ctx context.Context, repo repository.OutreachRepository, orgID uuid.UUID, snap *FrozenCohortSnapshot, now time.Time) ([]cohortApplyWork, []CohortMemberFail) {
	var works []cohortApplyWork
	var fails []CohortMemberFail
	if snap == nil {
		return nil, []CohortMemberFail{{Reason: "nil snapshot"}}
	}
	seenMailbox := map[string]bool{}
	for _, m := range snap.Members {
		if seenMailbox[m.Mailbox] {
			fails = append(fails, CohortMemberFail{AccountRef: m.AccountRef, Mailbox: m.Mailbox, Reason: "duplicate_mailbox"})
			continue
		}
		seenMailbox[m.Mailbox] = true
		if strings.EqualFold(m.RouteClass, RouteClassProbabilisticOrRisky) {
			fails = append(fails, CohortMemberFail{AccountRef: m.AccountRef, Mailbox: m.Mailbox, Reason: "risky_outside_default_pilot"})
			continue
		}
		if qa := ValidateCopyForRouteClass(m.RouteClass, m.BodyText, m.Subject, nil); len(qa) > 0 {
			fails = append(fails, CohortMemberFail{AccountRef: m.AccountRef, Mailbox: m.Mailbox, Reason: "copy_qa_failure:" + strings.Join(qa, ",")})
			continue
		}
		wantHash := hashControlledContent(m.Mailbox, m.RouteClass, m.Subject, m.BodyText)
		if m.ContentHash != "" && m.ContentHash != wantHash {
			fails = append(fails, CohortMemberFail{AccountRef: m.AccountRef, Mailbox: m.Mailbox, Reason: "content_drift"})
			continue
		}
		tp, create, err := locateOrBuildTouchpoint(ctx, repo, orgID, m, now)
		if err != nil {
			fails = append(fails, CohortMemberFail{AccountRef: m.AccountRef, Mailbox: m.Mailbox, Reason: err.Error()})
			continue
		}
		if tp.Recipient != "" && canonicalPilotEmail(tp.Recipient) != canonicalPilotEmail(m.Mailbox) {
			fails = append(fails, CohortMemberFail{AccountRef: m.AccountRef, Mailbox: m.Mailbox, Reason: "mailbox_mismatch"})
			continue
		}
		if tp.ContentHash != "" && tp.BodyText != "" && tp.BodyText != m.BodyText &&
			(tp.State == models.TouchpointApproved || tp.State == models.TouchpointQueued || tp.State == models.TouchpointSent) {
			fails = append(fails, CohortMemberFail{AccountRef: m.AccountRef, Mailbox: m.Mailbox, Reason: "content_drift"})
			continue
		}
		tp.Recipient = m.Mailbox
		tp.Subject = m.Subject
		tp.BodyText = m.BodyText
		RecomputeContentHash(tp)
		works = append(works, cohortApplyWork{member: m, tp: tp, create: create})
	}
	return works, fails
}

func locateOrBuildTouchpoint(ctx context.Context, repo repository.OutreachRepository, orgID uuid.UUID, m FrozenCohortMember, now time.Time) (*models.OutreachTouchpoint, bool, error) {
	if repo == nil {
		tp := newFrozenTouchpoint(orgID, m, now)
		return tp, true, nil
	}
	if m.TouchpointID != uuid.Nil {
		tp, err := repo.GetTouchpoint(ctx, orgID, m.TouchpointID)
		if err != nil {
			return nil, false, err
		}
		if tp == nil {
			return nil, false, fmt.Errorf("touchpoint_missing")
		}
		return tp, false, nil
	}
	if m.AccountID == uuid.Nil {
		if acc, err := resolveAccountForMember(ctx, repo, orgID, m); err == nil && acc != nil {
			m.AccountID = acc.ID
		}
	}
	if m.AccountID != uuid.Nil {
		list, err := repo.ListTouchpoints(ctx, orgID, m.AccountID, "", 50, 0)
		if err != nil {
			return nil, false, err
		}
		var reusable *models.OutreachTouchpoint
		for i := range list {
			tp := &list[i]
			if tp.Ordinal != 1 && tp.Purpose != models.TouchpointPurposeInitial {
				continue
			}
			if canonicalPilotEmail(tp.Recipient) != canonicalPilotEmail(m.Mailbox) && tp.Recipient != "" {
				continue
			}
			// A route already in flight or delivered must never be re-authorized
			// into a second initial touch.
			if terminalSentTouchpointState(tp.State) {
				return nil, false, fmt.Errorf("route_already_dispatched:%s", strings.ToLower(tp.State))
			}
			// A stop that means "do not contact this recipient" still blocks,
			// whatever the stop reason text says.
			if suppressedTouchpointState(tp.State) {
				return nil, false, fmt.Errorf("route_suppressed:%s", strings.ToLower(tp.State))
			}
			if reusableTouchpointState(tp.State) && reusable == nil {
				reusable = tp
			}
		}
		if reusable != nil {
			return reusable, false, nil
		}
		// Only stale non-terminal artifacts remain (e.g. a CANCELLED row from an
		// older composer version). Supersede them with a fresh initial touch
		// rather than blocking the whole frozen cohort on one stale row.
		tp := newFrozenTouchpoint(orgID, m, now)
		return tp, true, nil
	}
	tp := newFrozenTouchpoint(orgID, m, now)
	return tp, true, nil
}

func resolveAccountForMember(ctx context.Context, repo repository.OutreachRepository, orgID uuid.UUID, m FrozenCohortMember) (*models.OutreachAccount, error) {
	if repo == nil {
		return nil, fmt.Errorf("repository required")
	}
	if cnpj := NormalizeCNPJ14(m.AccountRef); len(cnpj) == 14 {
		return repo.GetAccountByCNPJ(ctx, orgID, cnpj)
	}
	accs, err := repo.ListAccounts(ctx, orgID, repository.OutreachAccountFilter{})
	if err != nil {
		return nil, err
	}
	for i := range accs {
		if accs[i].SourceLeadID == m.AccountRef || accs[i].ID.String() == m.AccountRef {
			return &accs[i], nil
		}
	}
	return nil, fmt.Errorf("account_not_found")
}

func newFrozenTouchpoint(orgID uuid.UUID, m FrozenCohortMember, now time.Time) *models.OutreachTouchpoint {
	tp := &models.OutreachTouchpoint{
		ID:             uuid.New(),
		OrganizationID: orgID,
		AccountID:      m.AccountID,
		Ordinal:        1,
		Channel:        models.OutreachChannelEmail,
		Purpose:        models.TouchpointPurposeInitial,
		DueAt:          now,
		State:          models.TouchpointNeedsReview,
		Recipient:      m.Mailbox,
		Subject:        m.Subject,
		BodyText:       m.BodyText,
		PolicyVersion:  RecipientPolicyVersion,
		IdempotencyKey: "cohort:" + m.AccountRef + ":" + m.Mailbox,
	}
	if m.CandidateID != uuid.Nil {
		id := m.CandidateID
		tp.ContactCandidateID = &id
	}
	RecomputeContentHash(tp)
	return tp
}

func FormatCohortAuthorize(res *CohortAuthorizeResult) string {
	if res == nil {
		return "no authorize result\n"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "authorization_id=%s\n", res.AuthorizationID)
	fmt.Fprintf(&b, "selected_accounts=%d\n", res.SelectedAccounts)
	fmt.Fprintf(&b, "authorized_touchpoints=%d\n", res.AuthorizedTouchpoints)
	fmt.Fprintf(&b, "excluded_before_freeze=%d\n", res.ExcludedBeforeFreeze)
	fmt.Fprintf(&b, "failed_authorization=%d\n", res.FailedAuthorization)
	fmt.Fprintf(&b, "post_freeze=%d\n", res.PostFreeze)
	fmt.Fprintf(&b, "duplicate_routes=%d\n", res.DuplicateRoutes)
	fmt.Fprintf(&b, "real_email_sent=%v auto_send_enabled=%v\n", res.RealEmailSent, res.AutoSendEnabled)
	for _, f := range res.Failures {
		fmt.Fprintf(&b, "FAIL account=%s mailbox=%s reason=%s\n", f.AccountRef, RedactMailbox(f.Mailbox), f.Reason)
	}
	return b.String()
}

// reusableTouchpointState is a pre-send state a frozen cohort may authorize.
func reusableTouchpointState(state string) bool {
	switch state {
	case models.TouchpointDrafted, models.TouchpointNeedsReview, models.TouchpointApproved,
		models.TouchpointPlanned, models.TouchpointDue:
		return true
	default:
		return false
	}
}

// terminalSentTouchpointState means the route already reached transport.
// Re-authorizing it would risk a duplicate send.
func terminalSentTouchpointState(state string) bool {
	switch state {
	case models.TouchpointQueued, models.TouchpointSent, models.TouchpointReplied:
		return true
	default:
		return false
	}
}

// suppressedTouchpointState means the recipient itself is stopped.
func suppressedTouchpointState(state string) bool {
	switch state {
	case models.TouchpointDNC, models.TouchpointBounced, models.TouchpointRejected:
		return true
	default:
		return false
	}
}
