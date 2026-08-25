package confenge

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

const humanGateSelectionPageSize = 100

func normalizeHumanGateSelection(in *HumanGateCreateInput) *errx.Error {
	mode := strings.ToUpper(strings.TrimSpace(in.SelectionMode))
	if mode == "" {
		mode = HumanGateSelectionLegacy
	}
	in.SelectionMode = mode
	in.SourceRunID = strings.TrimSpace(in.SourceRunID)

	seen := make(map[uuid.UUID]struct{}, len(in.RecoverVersionIDs))
	ids := make([]uuid.UUID, 0, len(in.RecoverVersionIDs))
	for _, id := range in.RecoverVersionIDs {
		if id == uuid.Nil {
			return humanGateError(errx.BadRequest, "invalid_recover_version_id", "recover_version_ids must contain canonical non-empty UUIDs")
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })
	in.RecoverVersionIDs = ids

	switch mode {
	case HumanGateSelectionLegacy, HumanGateSelectionNextUnclaimed:
		if len(ids) != 0 {
			return humanGateError(errx.BadRequest, "recover_versions_not_allowed", "recover_version_ids is only valid with RECOVER_PRIOR")
		}
	case HumanGateSelectionRecoverPrior:
		if len(ids) == 0 {
			return humanGateError(errx.BadRequest, "recover_versions_required", "RECOVER_PRIOR requires at least one recover_version_id")
		}
		if len(ids) > 20 {
			return humanGateError(errx.BadRequest, "recover_versions_limit", "RECOVER_PRIOR accepts at most 20 version ids")
		}
	default:
		return humanGateError(errx.BadRequest, "invalid_selection_mode", "selection_mode must be LEGACY, NEXT_UNCLAIMED or RECOVER_PRIOR")
	}
	return nil
}

func humanGateSelectionRequestHash(in HumanGateCreateInput) string {
	if in.SelectionMode == HumanGateSelectionLegacy && len(in.RecoverVersionIDs) == 0 {
		// Preserve the pre-pagination intent hash so an ambiguous request made
		// before rollout can still replay its original receipt after rollout.
		return humanGateRequestHash(struct {
			Limit int
			Run   string
		}{in.Limit, in.SourceRunID})
	}
	ids := make([]string, 0, len(in.RecoverVersionIDs))
	for _, id := range in.RecoverVersionIDs {
		ids = append(ids, id.String())
	}
	return humanGateRequestHash(struct {
		Limit   int
		Run     string
		Mode    string
		Recover []string
	}{in.Limit, in.SourceRunID, in.SelectionMode, ids})
}

func humanGateSelectionLockID(orgID uuid.UUID, sourceRunID string) int64 {
	sum := sha256.Sum256([]byte("confenge-cohort-selection\x00" + orgID.String() + "\x00" + sourceRunID))
	return int64(binary.BigEndian.Uint64(sum[:8]))
}

func canonicalSupplierRoot(acc *models.OutreachAccount) string {
	if acc == nil {
		return ""
	}
	root := digitsOnly(acc.CNPJRoot)
	if len(root) == 8 {
		return root
	}
	cnpj := digitsOnly(acc.CNPJ14)
	if len(cnpj) == 14 {
		return cnpj[:8]
	}
	return ""
}

func humanGateRecipientHash(mailbox string) string {
	sum := sha256.Sum256([]byte(canonicalPilotEmail(mailbox)))
	return hex.EncodeToString(sum[:])
}

func humanGateAccountOperational(acc *models.OutreachAccount, sourceRunID string, now time.Time) string {
	if acc == nil {
		return "account_missing"
	}
	if acc.SourceRunID != sourceRunID {
		return "source_run_mismatch"
	}
	if canonicalSupplierRoot(acc) == "" {
		return "supplier_cnpj_root_invalid"
	}
	if acc.Blocked || acc.DoNotContact {
		return "account_blocked_or_dnc"
	}
	switch acc.QueueState {
	case "DO_NOT_CONTACT", "BLOCKED", "BOUNCED", "REPLIED", "MEETING", "PROPOSAL", "WON", "LOST", "SENT", "ENROLLED":
		return "account_already_terminal"
	}
	if acc.ActivationState != ActivationActionableNow {
		return "activation_not_actionable"
	}
	if acc.NextBestActionAt != nil && acc.NextBestActionAt.After(now) {
		return "activation_not_due"
	}
	if acc.ActivationExpiresAt != nil && !acc.ActivationExpiresAt.After(now) {
		return "activation_expired"
	}
	if !acc.TargetFitEligible || acc.TargetFitClass != TargetFitConfirmed || !acc.TargetFitFresh || acc.TargetFitVersion == "" || acc.TargetFitSourceWatermark == "" || acc.TargetFitObservedAt == nil {
		return "target_fit_not_operational"
	}
	if !acc.EmailSendReady {
		return "account_email_not_ready"
	}
	return ""
}

func (s *service) prepareHumanGateSelection(
	ctx context.Context,
	tx pgx.Tx,
	orgID uuid.UUID,
	run *models.OutreachImportRun,
	in HumanGateCreateInput,
	opts CohortPrepareOptions,
) (*FrozenCohortSnapshot, HumanGateSelection, map[uuid.UUID]models.OutreachAccount, *errx.Error) {
	report := HumanGateSelection{
		Mode:               in.SelectionMode,
		SourceRunID:        run.SourceRunID,
		ExclusionsByReason: map[string]int{},
	}
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", humanGateSelectionLockID(orgID, run.SourceRunID)); err != nil {
		return nil, report, nil, humanGateError(errx.ServiceUnavailable, "selection_lock_unavailable", "cohort selection lock is unavailable")
	}

	claimedRoots, claimedRecipients, err := loadHumanGateClaims(ctx, tx, orgID, run.SourceRunID)
	if err != nil {
		return nil, report, nil, humanGateError(errx.ServiceUnavailable, "selection_claims_unavailable", "cohort selection claims could not be read")
	}

	var inputs []CohortAccountInput
	accounts := map[uuid.UUID]models.OutreachAccount{}
	localRoots := map[string]bool{}
	add := func(acc models.OutreachAccount, skipPriorCohorts bool) *errx.Error {
		if reason := humanGateAccountOperational(&acc, run.SourceRunID, opts.Now); reason != "" {
			report.ExclusionsByReason[reason]++
			return nil
		}
		root := canonicalSupplierRoot(&acc)
		if claimedRoots[root] || localRoots[root] {
			report.ExclusionsByReason["supplier_already_claimed"]++
			return nil
		}
		worked, readErr := humanGateAccountPreviouslyWorked(ctx, tx, orgID, acc.ID, skipPriorCohorts)
		if readErr != nil {
			return humanGateError(errx.ServiceUnavailable, "selection_history_unavailable", "prior outreach history could not be read")
		}
		if worked {
			report.ExclusionsByReason["account_previously_worked"]++
			return nil
		}
		cands, readErr := s.repo.ListCandidates(ctx, orgID, acc.ID)
		if readErr != nil {
			return humanGateError(errx.ServiceUnavailable, "source_candidates_unavailable", "canonical contact candidates could not be read")
		}
		filtered := cands[:0]
		for i := range cands {
			hash := humanGateRecipientHash(cands[i].Email)
			if canonicalPilotEmail(cands[i].Email) == "" || claimedRecipients[hash] {
				continue
			}
			filtered = append(filtered, cands[i])
		}
		if len(filtered) == 0 {
			report.ExclusionsByReason["recipient_already_claimed_or_missing"]++
			return nil
		}
		localRoots[root] = true
		accounts[acc.ID] = acc
		inputs = append(inputs, CohortAccountInput{Account: acc, Candidates: filtered, Source: run.SourceSystem, Persisted: true})
		return nil
	}

	if in.SelectionMode == HumanGateSelectionRecoverPrior {
		ids, x := humanGateRecoverAccountIDs(ctx, tx, orgID, in.RecoverVersionIDs)
		if x != nil {
			return nil, report, nil, x
		}
		report.RecoveredRequested = len(ids)
		for _, id := range ids {
			acc, readErr := s.repo.GetAccount(ctx, orgID, id)
			if readErr != nil || acc == nil {
				report.ExclusionsByReason["account_missing"]++
				continue
			}
			if x := add(*acc, true); x != nil {
				return nil, report, nil, x
			}
		}
	} else {
		for offset := 0; ; offset += humanGateSelectionPageSize {
			page, readErr := s.repo.ListAccounts(ctx, orgID, repository.OutreachAccountFilter{
				Limit:                humanGateSelectionPageSize,
				Offset:               offset,
				DynamicPriority:      true,
				ActivationState:      ActivationActionableNow,
				ActivationDueNow:     true,
				ActivationNotExpired: true,
				ExcludeTerminal:      true,
				RequireOperational:   true,
				SourceRunID:          run.SourceRunID,
			})
			if readErr != nil {
				return nil, report, nil, humanGateError(errx.ServiceUnavailable, "source_accounts_unavailable", "canonical source accounts could not be paged")
			}
			for i := range page {
				if x := add(page[i], false); x != nil {
					return nil, report, nil, x
				}
			}
			if len(inputs) > 0 {
				candidate, prepErr := PrepareControlledCohort(inputs, opts)
				if prepErr != nil {
					return nil, report, nil, humanGateError(errx.Unprocessable, "cohort_prepare_failed", prepErr.Error())
				}
				if len(candidate.Members) >= in.Limit {
					break
				}
			}
			if len(page) < humanGateSelectionPageSize {
				break
			}
		}
	}

	manifest, err := PrepareControlledCohort(inputs, opts)
	if err != nil {
		return nil, report, nil, humanGateError(errx.Unprocessable, "cohort_prepare_failed", err.Error())
	}
	if manifest == nil || len(manifest.Members) == 0 {
		return nil, report, nil, humanGateError(errx.Unprocessable, "selection_exhausted", "no unclaimed supplier with a controlled eligible route remains")
	}
	for reason, count := range manifest.Preview.ByExclusionReason {
		report.ExclusionsByReason[reason] += count
	}
	report.ClaimedCount = len(manifest.Members)
	if in.SelectionMode == HumanGateSelectionRecoverPrior {
		report.RecoveredEligible = len(manifest.Members)
	}
	return manifest, report, accounts, nil
}

func loadHumanGateClaims(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, sourceRunID string) (map[string]bool, map[string]bool, error) {
	rows, err := tx.Query(ctx, `SELECT cnpj_root,recipient_hash FROM confenge_cohort_selection_claims WHERE organization_id=$1 AND source_run_id=$2`, orgID, sourceRunID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	roots, recipients := map[string]bool{}, map[string]bool{}
	for rows.Next() {
		var root, recipient string
		if err = rows.Scan(&root, &recipient); err != nil {
			return nil, nil, err
		}
		roots[root], recipients[recipient] = true, true
	}
	return roots, recipients, rows.Err()
}

func humanGateAccountPreviouslyWorked(ctx context.Context, tx pgx.Tx, orgID, accountID uuid.UUID, skipPriorCohorts bool) (bool, error) {
	var worked bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM outreach_touchpoints WHERE organization_id=$1 AND account_id=$2)`, orgID, accountID).Scan(&worked); err != nil || worked {
		return worked, err
	}
	if skipPriorCohorts {
		return false, nil
	}
	err := tx.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM confenge_cohort_versions cv
		CROSS JOIN LATERAL jsonb_array_elements(COALESCE(cv.frozen_manifest->'members','[]'::jsonb)) member
		WHERE cv.organization_id=$1 AND member->>'account_id'=$2
	)`, orgID, accountID.String()).Scan(&worked)
	return worked, err
}

func humanGateRecoverAccountIDs(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, versionIDs []uuid.UUID) ([]uuid.UUID, *errx.Error) {
	var found int
	if err := tx.QueryRow(ctx, `SELECT count(*)::int FROM confenge_cohort_versions WHERE organization_id=$1 AND id=ANY($2)`, orgID, versionIDs).Scan(&found); err != nil {
		return nil, humanGateError(errx.ServiceUnavailable, "selection_history_unavailable", "prior cohorts could not be read")
	}
	if found != len(versionIDs) {
		return nil, humanGateError(errx.NotFound, "recover_version_not_found", "one or more recover_version_ids were not found in this organization")
	}
	rows, err := tx.Query(ctx, `SELECT DISTINCT (member->>'account_id')::uuid AS account_id
		FROM confenge_cohort_versions cv
		CROSS JOIN LATERAL jsonb_array_elements(COALESCE(cv.frozen_manifest->'members','[]'::jsonb)) member
		WHERE cv.organization_id=$1 AND cv.id=ANY($2)
		ORDER BY account_id`, orgID, versionIDs)
	if err != nil {
		return nil, humanGateError(errx.ServiceUnavailable, "selection_history_unavailable", "prior cohort members could not be read")
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err = rows.Scan(&id); err != nil {
			return nil, humanGateError(errx.ServiceUnavailable, "selection_history_unavailable", "prior cohort members could not be read")
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		return nil, humanGateError(errx.ServiceUnavailable, "selection_history_unavailable", "prior cohort members could not be read")
	}
	return ids, nil
}

func storeHumanGateSelectionClaims(ctx context.Context, tx pgx.Tx, orgID, versionID uuid.UUID, sourceRunID, mode string, manifest *FrozenCohortSnapshot, accounts map[uuid.UUID]models.OutreachAccount) *errx.Error {
	for i := range manifest.Members {
		member := &manifest.Members[i]
		acc, ok := accounts[member.AccountID]
		if !ok {
			return humanGateError(errx.Internal, "selection_account_unbound", "selected cohort member is not bound to its canonical supplier account")
		}
		root := canonicalSupplierRoot(&acc)
		if root == "" {
			return humanGateError(errx.Unprocessable, "supplier_cnpj_root_invalid", "selected cohort member has no canonical supplier CNPJ root")
		}
		if _, err := tx.Exec(ctx, `INSERT INTO confenge_cohort_selection_claims
			(id,organization_id,source_run_id,cohort_version_id,account_id,cnpj_root,recipient_hash,selection_mode)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, uuid.New(), orgID, sourceRunID, versionID, member.AccountID, root, humanGateRecipientHash(member.Mailbox), mode); err != nil {
			return humanGateError(errx.Conflict, "selection_claim_conflict", "another cohort already claimed this supplier or recipient; retry with a new idempotency key")
		}
	}
	return nil
}

func finalizeHumanGateSelectionReport(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, sourceRunID string, report *HumanGateSelection) *errx.Error {
	if err := tx.QueryRow(ctx, `SELECT count(*)::int FROM confenge_cohort_selection_claims WHERE organization_id=$1 AND source_run_id=$2`, orgID, sourceRunID).Scan(&report.UniqueClaimedTotal); err != nil {
		return humanGateError(errx.ServiceUnavailable, "selection_progress_unavailable", "cohort selection progress could not be read")
	}
	if err := tx.QueryRow(ctx, `SELECT count(DISTINCT COALESCE(NULLIF(a.cnpj_root,''),left(a.cnpj14,8)))::int
		FROM outreach_accounts a
		WHERE a.organization_id=$1 AND a.source_run_id=$2
		  AND a.activation_state='ACTIONABLE_NOW'
		  AND (a.next_best_action_at IS NULL OR a.next_best_action_at<=now())
		  AND (a.activation_expires_at IS NULL OR a.activation_expires_at>now())
		  AND a.target_fit_eligible=true AND a.target_fit_class='TARGET_CONFIRMED'
		  AND a.target_fit_fresh=true AND a.target_fit_version<>''
		  AND a.target_fit_source_watermark<>'' AND a.target_fit_observed_at IS NOT NULL
		  AND a.email_send_ready=true AND a.do_not_contact=false AND a.blocked=false
		  AND a.queue_state NOT IN ('DO_NOT_CONTACT','BLOCKED','BOUNCED','REPLIED','MEETING','PROPOSAL','WON','LOST','SENT','ENROLLED')
		  AND COALESCE(NULLIF(a.cnpj_root,''),left(a.cnpj14,8)) ~ '^[0-9]{8}$'
		  AND EXISTS (SELECT 1 FROM outreach_contact_candidates c WHERE c.organization_id=a.organization_id AND c.account_id=a.id AND c.email_send_ready=true AND c.email<>'' AND c.blocked=false AND c.do_not_contact=false AND c.bounced=false AND c.mailbox_purpose_send_blocked=false AND c.verification_status NOT IN ('CANDIDATE_UNVERIFIED','NOT_FOUND','INVALID','BOUNCED','DO_NOT_CONTACT'))
		  AND NOT EXISTS (SELECT 1 FROM confenge_cohort_selection_claims claim WHERE claim.organization_id=a.organization_id AND claim.source_run_id=a.source_run_id AND (claim.account_id=a.id OR claim.cnpj_root=COALESCE(NULLIF(a.cnpj_root,''),left(a.cnpj14,8))))
		  AND NOT EXISTS (SELECT 1 FROM outreach_touchpoints tp WHERE tp.organization_id=a.organization_id AND tp.account_id=a.id)
		  AND NOT EXISTS (SELECT 1 FROM confenge_cohort_versions cv CROSS JOIN LATERAL jsonb_array_elements(COALESCE(cv.frozen_manifest->'members','[]'::jsonb)) member WHERE cv.organization_id=a.organization_id AND member->>'account_id'=a.id::text)`, orgID, sourceRunID).Scan(&report.EligibleRemaining); err != nil {
		return humanGateError(errx.ServiceUnavailable, "selection_progress_unavailable", "remaining cohort selection capacity could not be read")
	}
	report.Exhausted = report.EligibleRemaining == 0
	return nil
}
