package confenge

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

const DefaultCohortDispatchCap = 10

var errCohortAlreadySent = errors.New("cohort member already sent")

// CohortDispatchResult is the redacted operator report after a bounded send.
type CohortDispatchResult struct {
	AuthorizationID     uuid.UUID          `json:"authorization_id"`
	CohortID            string             `json:"cohort_id"`
	PolicyVersion       string             `json:"policy_version"`
	RepositorySHA       string             `json:"repository_sha"`
	FeedHash            string             `json:"feed_hash"`
	Attempted           int                `json:"attempted"`
	ProviderAccepted    int                `json:"provider_accepted"`
	Failed              int                `json:"failed"`
	SkippedDuplicate    int                `json:"skipped_duplicate"`
	Blocked             int                `json:"blocked"`
	RealEmailSent       bool               `json:"real_email_sent"`
	AutoSendEnabled     bool               `json:"auto_send_enabled"`
	GreenAutorunEnabled bool               `json:"green_autorun_enabled"`
	KillSwitchAvailable bool               `json:"kill_switch_available"`
	MaxDaily            int                `json:"max_daily"`
	Failures            []CohortMemberFail `json:"failures,omitempty"`
}

// CohortSendFunc delivers one approved touchpoint. Tests inject a fake that
// never opens SMTP. Production uses QueueTouchpoint / worker enrollment.
type CohortSendFunc func(member FrozenCohortMember, tp *models.OutreachTouchpoint) (providerID string, err error)

// TransportOneCohortMessage is the shipped reserve → send → commit/release
// path. A failed provider send releases the reserved slot. A retry of the
// same message key does not consume a second cap slot once committed.
func TransportOneCohortMessage(
	ctx context.Context,
	store BoundedCohortStore,
	auth *BoundedCohortAuthorization,
	tp *models.OutreachTouchpoint,
	in CohortTransportInput,
	send func(*models.OutreachTouchpoint) (providerID string, err error),
) (providerID string, err error) {
	if store == nil {
		return "", ErrCohortStoreUnavailable
	}
	if auth == nil || tp == nil {
		return "", fmt.Errorf("bounded cohort grant missing at transport")
	}
	if send == nil {
		return "", fmt.Errorf("send function required; refusing implicit SMTP")
	}
	if FileKillSwitchActive() {
		return "", fmt.Errorf("kill switch engaged")
	}
	key := cohortMessageKey(auth.ID, tp.ID)
	slot, err := store.ReserveSlot(ctx, auth.ID, key, in.Now)
	if err != nil {
		return "", err
	}
	if slot.State == CohortSlotSent || slot.Already && slot.State == CohortSlotSent {
		return tp.ProviderMessageID, errCohortAlreadySent
	}
	in.SlotHeld = true
	in.SentToday = slot.Occupied
	if err := CanTransportCohort(tp, auth, in); err != nil {
		_ = store.ReleaseSlot(ctx, auth.ID, key, "transport_invalid")
		return "", err
	}
	providerID, err = send(tp)
	if err != nil {
		_ = store.ReleaseSlot(ctx, auth.ID, key, "provider_send_failed")
		return "", err
	}
	if strings.TrimSpace(providerID) == "" {
		// Attempted; provider accepted is not yet observable. Slot stays reserved.
		return "", nil
	}
	if err := store.CommitSlot(ctx, auth.ID, key, in.Now); err != nil {
		return providerID, err
	}
	now := in.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if err := TransitionToSentCohort(tp, now, providerID, auth, in); err != nil {
		return providerID, err
	}
	return providerID, nil
}

// DispatchBoundedCohort sends at most N<=10 members of a founder-authorized
// grant after a live GO_FOR_CONTROLLED_EMAIL_PILOT. It never enables auto-send.
func DispatchBoundedCohort(
	ctx context.Context,
	store BoundedCohortStore,
	repo repository.OutreachRepository,
	cmp *ReleaseComparison,
	auth *BoundedCohortAuthorization,
	now time.Time,
	limit int,
	send CohortSendFunc,
) (*CohortDispatchResult, error) {
	out := &CohortDispatchResult{KillSwitchAvailable: true}
	if auth != nil {
		out.AuthorizationID = auth.ID
		out.CohortID = auth.CohortID
		out.PolicyVersion = auth.PolicyVersion
		out.RepositorySHA = auth.RepositorySHA
		out.MaxDaily = auth.MaxDailyVolume
		if auth.FrozenManifest != nil {
			out.FeedHash = firstNonEmpty(auth.FrozenManifest.SnapshotHash, auth.FrozenManifest.FeedIdentity)
		}
	}
	if err := RequireControlledEmailGO(cmp); err != nil {
		out.Blocked = 1
		return out, err
	}
	if store == nil {
		return out, ErrCohortStoreUnavailable
	}
	if auth == nil || auth.FrozenManifest == nil {
		return out, ErrCohortGrantMissing
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if auth.RevokedAt != nil {
		return out, ErrCohortGrantRevoked
	}
	if !auth.EffectiveExpiry().IsZero() && !now.Before(auth.EffectiveExpiry()) {
		return out, ErrCohortGrantExpired
	}
	if auth.AutoSendEnabled {
		return out, fmt.Errorf("auto_send_forbidden")
	}
	if auth.MaxDailyVolume < 1 || auth.MaxDailyVolume > DefaultCohortDispatchCap {
		return out, fmt.Errorf("authorization daily cap must be 1-%d", DefaultCohortDispatchCap)
	}
	if auth.GreenAutorunEnabled {
		return out, fmt.Errorf("green_autorun_forbidden")
	}
	if FileKillSwitchActive() {
		return out, fmt.Errorf("kill switch engaged")
	}
	if send == nil {
		return out, fmt.Errorf("send function required; refusing implicit SMTP")
	}
	if limit <= 0 || limit > DefaultCohortDispatchCap {
		limit = DefaultCohortDispatchCap
	}
	if auth.MaxDailyVolume > 0 && limit > auth.MaxDailyVolume {
		limit = auth.MaxDailyVolume
	}
	if limit > DefaultCohortDispatchCap {
		limit = DefaultCohortDispatchCap
	}
	seenAccount := map[string]bool{}
	for _, m := range auth.FrozenManifest.Members {
		if out.Attempted+out.SkippedDuplicate >= limit {
			break
		}
		if strings.EqualFold(m.RouteClass, RouteClassProbabilisticOrRisky) {
			out.Blocked++
			out.Failures = append(out.Failures, CohortMemberFail{AccountRef: m.AccountRef, Mailbox: m.Mailbox, Reason: "risky_outside_default_pilot"})
			continue
		}
		accKey := firstNonEmpty(m.AccountRef, m.AccountID.String())
		if seenAccount[accKey] {
			out.Blocked++
			out.Failures = append(out.Failures, CohortMemberFail{AccountRef: m.AccountRef, Mailbox: m.Mailbox, Reason: "second_mailbox_in_parallel"})
			continue
		}
		seenAccount[accKey] = true
		tp, err := loadDispatchTouchpoint(ctx, repo, auth, m, now)
		if err != nil {
			out.Failed++
			out.Failures = append(out.Failures, CohortMemberFail{AccountRef: m.AccountRef, Mailbox: m.Mailbox, Reason: err.Error()})
			continue
		}
		in := dispatchTransportInput(auth, m, tp, now)
		providerID, err := TransportOneCohortMessage(ctx, store, auth, tp, in, func(tp *models.OutreachTouchpoint) (string, error) {
			return send(m, tp)
		})
		if err != nil {
			if errors.Is(err, errCohortAlreadySent) {
				out.SkippedDuplicate++
				continue
			}
			if errors.Is(err, ErrCohortDailyCap) || strings.Contains(err.Error(), "daily cap") {
				out.Blocked++
				out.Failures = append(out.Failures, CohortMemberFail{AccountRef: m.AccountRef, Mailbox: m.Mailbox, Reason: "daily_cap_exceeded"})
				break
			}
			out.Failed++
			out.Failures = append(out.Failures, CohortMemberFail{AccountRef: m.AccountRef, Mailbox: m.Mailbox, Reason: err.Error()})
			continue
		}
		out.Attempted++
		out.RealEmailSent = true
		if strings.TrimSpace(providerID) != "" {
			out.ProviderAccepted++
		}
		if repo != nil && tp.ID != uuid.Nil {
			_ = repo.UpdateTouchpoint(ctx, tp)
		}
	}
	return out, nil
}

func dispatchTransportInput(auth *BoundedCohortAuthorization, m FrozenCohortMember, tp *models.OutreachTouchpoint, now time.Time) CohortTransportInput {
	in := CohortTransportInput{
		Now:               now,
		RepositorySHA:     auth.RepositorySHA,
		FeedSchemaVersion: auth.FeedSchemaVersion,
		CohortHash:        auth.CohortHash,
		PolicyVersion:     auth.PolicyVersion,
		ComposerVersion:   auth.ComposerVersion,
		EvidenceVersion:   auth.EvidenceVersion,
		RecipientSetHash:  auth.RecipientSetHash,
		RecipientMailbox:  m.Mailbox,
		RouteClass:        m.RouteClass,
	}
	if tp != nil && tp.Recipient != "" {
		in.RecipientMailbox = tp.Recipient
	}
	return in
}

func loadDispatchTouchpoint(ctx context.Context, repo repository.OutreachRepository, auth *BoundedCohortAuthorization, m FrozenCohortMember, now time.Time) (*models.OutreachTouchpoint, error) {
	orgID := uuid.Nil
	if auth != nil {
		orgID = auth.OrganizationID
	}
	if repo != nil && m.TouchpointID != uuid.Nil {
		tp, err := repo.GetTouchpoint(ctx, orgID, m.TouchpointID)
		if err != nil {
			return nil, err
		}
		if tp != nil {
			if strings.TrimSpace(tp.AuthorizationMode) != AuthorizationModeBoundedCohort {
				if err := ApplyBoundedCohortAuthorization(tp, auth, now); err != nil {
					return nil, err
				}
			}
			return tp, nil
		}
	}
	tp := newFrozenTouchpoint(orgID, m, now)
	if err := ApplyBoundedCohortAuthorization(tp, auth, now); err != nil {
		return nil, err
	}
	return tp, nil
}

// controlledDraftFact is the evidence the cohort message actually rests on,
// condensed the same way the composer condenses it so a serialized feed record
// never reaches the draft as prose.
func controlledDraftFact(acc *models.OutreachAccount) string {
	raw := firstNonEmpty(strings.TrimSpace(acc.FactToMention), strings.TrimSpace(acc.MomentSummary))
	raw = ApplyCopyHygiene(raw)
	if looksLikeMetadataDump(raw) || containsDumpLabel(raw) {
		condensed := condenseMetadataFact(raw)
		if condensed != "" && !looksLikeMetadataDump(condensed) && !containsDumpLabel(condensed) {
			return condensed
		}
		return strings.TrimSpace(acc.MomentSummary)
	}
	return raw
}

func FormatCohortDispatch(res *CohortDispatchResult) string {
	if res == nil {
		return "dispatch_missing=true\n"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "authorization_id=%s\n", res.AuthorizationID)
	fmt.Fprintf(&b, "cohort_id=%s\n", res.CohortID)
	fmt.Fprintf(&b, "policy_version=%s\n", res.PolicyVersion)
	fmt.Fprintf(&b, "repository_sha=%s\n", res.RepositorySHA)
	fmt.Fprintf(&b, "feed_hash=%s\n", res.FeedHash)
	fmt.Fprintf(&b, "REAL_EMAIL_SENT=%v\n", res.RealEmailSent)
	fmt.Fprintf(&b, "N_ATTEMPTED=%d\n", res.Attempted)
	fmt.Fprintf(&b, "N_PROVIDER_ACCEPTED=%d\n", res.ProviderAccepted)
	fmt.Fprintf(&b, "MAX_DAILY=%d\n", res.MaxDaily)
	fmt.Fprintf(&b, "AUTO_SEND_ENABLED=%v\n", res.AutoSendEnabled)
	fmt.Fprintf(&b, "GREEN_AUTORUN_ENABLED=%v\n", res.GreenAutorunEnabled)
	fmt.Fprintf(&b, "KILL_SWITCH_AVAILABLE=%v\n", res.KillSwitchAvailable)
	fmt.Fprintf(&b, "failed=%d skipped_duplicate=%d blocked=%d\n", res.Failed, res.SkippedDuplicate, res.Blocked)
	for _, f := range res.Failures {
		fmt.Fprintf(&b, "FAIL account=%s mailbox=%s reason=%s\n", f.AccountRef, RedactMailbox(f.Mailbox), f.Reason)
	}
	return b.String()
}

func (s *service) DispatchBoundedCohort(ctx context.Context, orgID, actor, authID uuid.UUID, now time.Time, limit int) (*CohortDispatchResult, *errx.Error) {
	if s == nil {
		return nil, errx.New(errx.ServiceUnavailable, "confenge service unavailable")
	}
	if xerr := s.requireEnabled(); xerr != nil {
		return nil, xerr
	}
	if s.cfg.AutoSendEnabled || s.cfg.GreenAutorunEnabled {
		return nil, errx.New(errx.BadRequest, "auto-send and green autorun cannot dispatch")
	}
	if !s.cfg.SendingAllowed() {
		return nil, errx.New(errx.Conflict, "sending paused")
	}
	if s.cohortStore == nil {
		return nil, errx.New(errx.ServiceUnavailable, ErrCohortStoreUnavailable.Error())
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	cmp, auth, err := PrepareControlledEmailGOReview(ctx, LiveReleaseInput{
		Now: now, Config: &s.cfg, Store: s.cohortStore, Repo: s.repo,
	}, authID)
	if err != nil {
		return nil, errx.New(errx.BadRequest, err.Error())
	}
	if auth == nil || auth.OrganizationID != orgID {
		return nil, errx.New(errx.NotFound, "bounded cohort grant missing")
	}
	send := CohortSendFunc(func(m FrozenCohortMember, tp *models.OutreachTouchpoint) (string, error) {
		if err := s.ensureCohortDraft(ctx, orgID, actor, tp, m); err != nil {
			return "", err
		}
		queued, xerr := s.QueueTouchpoint(ctx, orgID, actor, tp.ID)
		if xerr != nil {
			return "", fmt.Errorf("%s", xerr.Message)
		}
		if queued == nil {
			return "", fmt.Errorf("queue returned empty")
		}
		id := strings.TrimSpace(queued.ProviderMessageID)
		if id == "" || strings.HasPrefix(id, "email-enrolled:") {
			return "", nil
		}
		return id, nil
	})
	res, err := DispatchBoundedCohort(ctx, s.cohortStore, s.repo, cmp, auth, now, limit, send)
	if err != nil {
		return res, errx.New(errx.Conflict, err.Error())
	}
	return res, nil
}

func (s *service) ensureCohortDraft(ctx context.Context, orgID, actor uuid.UUID, tp *models.OutreachTouchpoint, m FrozenCohortMember) error {
	if tp == nil {
		return fmt.Errorf("nil touchpoint")
	}
	if tp.DraftID != nil && *tp.DraftID != uuid.Nil {
		return nil
	}
	now := time.Now().UTC()
	d := &models.OutreachDraft{
		ID:             uuid.New(),
		OrganizationID: orgID,
		AccountID:      tp.AccountID,
		Channel:        models.OutreachChannelEmail,
		RecipientEmail: tp.Recipient,
		Subject:        tp.Subject,
		BodyText:       tp.BodyText,
		Status:         models.OutreachDraftApproved,
		ApprovedBy:     tp.ApprovedBy,
		ApprovedAt:     tp.ApprovedAt,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	// ValidateDraft requires a fact or a claim. Carry the account's observed
	// fact so a cohort draft is not rejected as evidence-free at enroll.
	if acc, err := s.repo.GetAccount(ctx, orgID, tp.AccountID); err == nil && acc != nil {
		d.FactUsed = controlledDraftFact(acc)
		d.ServiceCode = acc.ServiceCode
	}
	if d.ApprovedBy == nil && actor != uuid.Nil {
		d.ApprovedBy = &actor
		d.ApprovedAt = &now
	}
	if tp.ContactCandidateID != nil {
		d.ContactCandidateID = tp.ContactCandidateID
	} else if m.CandidateID != uuid.Nil {
		id := m.CandidateID
		d.ContactCandidateID = &id
		tp.ContactCandidateID = &id
	}
	if err := s.repo.UpsertDraft(ctx, d); err != nil {
		return err
	}
	id := d.ID
	tp.DraftID = &id
	return s.repo.UpdateTouchpoint(ctx, tp)
}
