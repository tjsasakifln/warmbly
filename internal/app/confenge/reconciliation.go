package confenge

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

type TargetFitReconciliationReport struct {
	DryRun               bool           `json:"dry_run"`
	AccountsScanned      int            `json:"accounts_scanned"`
	BeforeOperational    int            `json:"before_operational"`
	AfterOperational     int            `json:"after_operational"`
	AccountsChanged      int            `json:"accounts_changed"`
	SuppressedByReason   map[string]int `json:"suppressed_by_reason"`
	CancelledTouchpoints int            `json:"cancelled_touchpoints"`
	BlockedDrafts        int            `json:"blocked_drafts"`
	DetachedEnrollments  int            `json:"detached_enrollments"`
	CancelledDispatch    int            `json:"cancelled_dispatch_items"`
	CurrentSourceRunID   string         `json:"current_source_run_id,omitempty"`
	SupersededRetired    int            `json:"superseded_rows_retired"`
	SupersededRoots      int            `json:"superseded_roots"`
}

// SupersededByCurrentRunReason is recorded on rows retired because a newer
// import already published a canonical member for the same company root.
const SupersededByCurrentRunReason = "superseded_by_current_source_run"

// supersededByCurrentRun returns the accounts that a newer import has replaced.
//
// Membership is one canonical member per cnpj_root8. Older imports leave their
// rows behind, and while the transport gate already refuses them with
// account_source_run_drift, they stay live and can still be counted by any
// projection that does not repeat that check. Retirement here is evidence, not
// heuristic: a row is superseded only when the current run publishes a row for
// the very same root. A root the current run never mentions is left alone —
// absence of evidence is not evidence of supersession — and nothing is deleted.
func supersededByCurrentRun(accounts []models.OutreachAccount, currentRunID string) map[uuid.UUID]bool {
	out := map[uuid.UUID]bool{}
	if strings.TrimSpace(currentRunID) == "" {
		return out
	}
	currentRoots := map[string]bool{}
	for i := range accounts {
		root := strings.TrimSpace(accounts[i].CNPJRoot)
		if root != "" && accounts[i].SourceRunID == currentRunID {
			currentRoots[root] = true
		}
	}
	for i := range accounts {
		acc := &accounts[i]
		root := strings.TrimSpace(acc.CNPJRoot)
		if root == "" || acc.SourceRunID == currentRunID || acc.SourceRunID == "" {
			continue
		}
		if currentRoots[root] {
			out[acc.ID] = true
		}
	}
	return out
}

func (s *service) ReconcileTargetFit(ctx context.Context, orgID uuid.UUID, dryRun bool) (*TargetFitReconciliationReport, *errx.Error) {
	if xerr := s.requireEnabled(); xerr != nil {
		return nil, xerr
	}
	report := &TargetFitReconciliationReport{DryRun: dryRun, SuppressedByReason: map[string]int{}}
	var accounts []models.OutreachAccount
	for offset := 0; ; offset += 1000 {
		batch, err := s.repo.ListAccounts(ctx, orgID, repository.OutreachAccountFilter{Limit: 1000, Offset: offset, StableOrder: true})
		if err != nil {
			return nil, errx.New(errx.Internal, "target-fit reconciliation scan: "+err.Error())
		}
		accounts = append(accounts, batch...)
		if len(batch) < 1000 {
			break
		}
	}
	// The current feed run is the supersession evidence. Without it, no row is
	// retired: an unreadable feed state must never look like "everything is old".
	if feedState, feedErr := s.repo.GetFeedSyncState(ctx, orgID); feedErr == nil && feedState != nil {
		report.CurrentSourceRunID = feedState.LastRunID
	}
	superseded := supersededByCurrentRun(accounts, report.CurrentSourceRunID)
	supersededRoots := map[string]bool{}

	for i := range accounts {
		acc := &accounts[i]
		report.AccountsScanned++
		candidates, err := s.repo.ListCandidates(ctx, orgID, acc.ID)
		if err != nil {
			return nil, errx.New(errx.Internal, "target-fit reconciliation contacts: "+err.Error())
		}
		contactReady := hasSendReadyEmailIgnoringTargetFit(acc, candidates)
		if acc.TargetFitEligible && contactReady {
			report.BeforeOperational++
		}
		decision := EvaluateTargetFit(acc)
		if superseded[acc.ID] {
			decision = TargetFitAuthorization{Eligible: false, Reason: SupersededByCurrentRunReason}
			report.SupersededRetired++
			if root := strings.TrimSpace(acc.CNPJRoot); root != "" {
				supersededRoots[root] = true
			}
		}
		if decision.Eligible {
			if contactReady {
				report.AfterOperational++
			}
		} else {
			report.SuppressedByReason[decision.Reason]++
		}
		changed := acc.TargetFitEligible != decision.Eligible || acc.TargetFitSuppressionReason != decision.Reason
		if !decision.Eligible && !isHistoricalTerminalQueue(acc.QueueState) && acc.QueueState != models.OutreachQueueTargetFitSuppressed {
			changed = true
		}
		if decision.Eligible && acc.QueueState == models.OutreachQueueTargetFitSuppressed {
			changed = true
		}
		if changed {
			report.AccountsChanged++
		}
		if dryRun {
			continue
		}
		acc.TargetFitEligible = decision.Eligible
		acc.TargetFitSuppressionReason = decision.Reason
		now := time.Now().UTC()
		acc.TargetFitReconciledAt = &now
		if !decision.Eligible && !isHistoricalTerminalQueue(acc.QueueState) {
			acc.QueueState = models.OutreachQueueTargetFitSuppressed
		} else if decision.Eligible && acc.QueueState == models.OutreachQueueTargetFitSuppressed {
			acc.QueueState = models.OutreachQueueNeedsContact
			for j := range candidates {
				if RequireEmailOutbound(acc, &candidates[j]) == nil {
					acc.QueueState = models.OutreachQueueReadyToGenerate
					break
				}
			}
		}
		if _, err := s.repo.UpsertAccount(ctx, acc); err != nil {
			return nil, errx.New(errx.Internal, "target-fit reconciliation account: "+err.Error())
		}
		if !decision.Eligible {
			counts, err := s.repo.InvalidateAccountOutboundForTargetFit(ctx, orgID, acc.ID, decision.Reason)
			if err != nil {
				return nil, errx.New(errx.Internal, "target-fit outbound invalidation: "+err.Error())
			}
			report.CancelledTouchpoints += counts.Touchpoints
			report.BlockedDrafts += counts.Drafts
			report.DetachedEnrollments += counts.Enrollments
			report.CancelledDispatch += counts.DispatchItems
		}
	}
	report.SupersededRoots = len(supersededRoots)
	return report, nil
}
