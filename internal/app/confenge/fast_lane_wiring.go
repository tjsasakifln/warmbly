package confenge

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"

	"github.com/warmbly/warmbly/internal/app/confenge/dispatch"
	"github.com/warmbly/warmbly/internal/models"
)

// WireFastLane installs the synchronous first-touch transport.
func (s *service) WireFastLane(pool *pgxpool.Pool, transport FirstTouchTransport) {
	s.fastLaneDB = pool
	s.firstTouchTransport = transport
}

// FastLaneEnabled reports whether the fast lane owns first-touch transport.
// When it does, the legacy enrol-into-campaign dispatcher must not also run:
// two transports over one queue is how a message gets sent twice.
func FastLaneEnabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("CONFENGE_FAST_LANE_ENABLED")))
	return v == "1" || v == "true" || v == "on"
}

// resolveConfengeMailbox picks the sending mailbox for a first touch.
//
// Queue rows carry no mailbox (email_account_id is null for every row the
// producer writes), so transport resolves it: the configured CONFENGE address
// when set, otherwise the organisation's single active SMTP mailbox. It never
// guesses between several candidates.
func (s *service) resolveConfengeMailbox(ctx context.Context, orgID uuid.UUID) (uuid.UUID, error) {
	if s.fastLaneDB == nil {
		return uuid.Nil, fmt.Errorf("fast lane database not wired")
	}
	return resolveConfengeMailboxIn(ctx, s.fastLaneDB, orgID)
}

// resolveConfengeMailboxIn is resolveConfengeMailbox's body against an explicit
// pool, so a side lane can ask the same question without reaching into the fast
// lane's own wiring. The rules are unchanged: configured address when set,
// otherwise the single active SMTP mailbox, and never a guess between several.
func resolveConfengeMailboxIn(ctx context.Context, pool *pgxpool.Pool, orgID uuid.UUID) (uuid.UUID, error) {
	if pool == nil {
		return uuid.Nil, fmt.Errorf("confenge mailbox database not wired")
	}
	want := strings.ToLower(strings.TrimSpace(os.Getenv("CONFENGE_MAILBOX_EMAIL")))
	if want != "" {
		var id uuid.UUID
		err := pool.QueryRow(ctx, `
			SELECT ea.id
			FROM email_accounts ea
			JOIN email_accounts_smtp_imap s ON s.email_account_id = ea.id
			WHERE ea.organization_id = $1 AND ea.status = 'active'
			  AND lower(ea.email) = $2 AND COALESCE(s.smtp_host,'') <> ''
			LIMIT 1`, orgID, want).Scan(&id)
		if err == nil {
			return id, nil
		}
		if err != pgx.ErrNoRows {
			return uuid.Nil, err
		}
		return uuid.Nil, fmt.Errorf("configured CONFENGE mailbox %q is not active with SMTP configured", want)
	}
	rows, err := pool.Query(ctx, `
		SELECT ea.id
		FROM email_accounts ea
		JOIN email_accounts_smtp_imap s ON s.email_account_id = ea.id
		WHERE ea.organization_id = $1 AND ea.status = 'active'
		  AND COALESCE(s.smtp_host,'') <> ''
		ORDER BY ea.created_at ASC
		LIMIT 2`, orgID)
	if err != nil {
		return uuid.Nil, err
	}
	defer rows.Close()
	var found []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return uuid.Nil, err
		}
		found = append(found, id)
	}
	if err := rows.Err(); err != nil {
		return uuid.Nil, err
	}
	switch len(found) {
	case 0:
		return uuid.Nil, fmt.Errorf("no active CONFENGE SMTP mailbox")
	case 1:
		return found[0], nil
	default:
		// Refuse rather than pick. Choosing a sender changes which reputation
		// carries the message; that is an operator decision.
		return uuid.Nil, fmt.Errorf("several active mailboxes; set CONFENGE_MAILBOX_EMAIL")
	}
}

// reconcileFastLaneCompat brings the legacy projections into line AFTER the
// send is already authoritative. Every step is best effort by design: a
// reporting mirror that fails must never resurrect a message the provider has
// already accepted, and must never block the next one.
func (s *service) reconcileFastLaneCompat(ctx context.Context, item *dispatch.QueueItem, tp *models.OutreachTouchpoint, acc FirstTouchAcceptance) {
	sentAt := acc.AcceptedAt
	projected := *tp
	projected.State = models.TouchpointSent
	projected.SentAt = &sentAt
	if acc.ProviderMessageID != "" {
		projected.ProviderMessageID = acc.ProviderMessageID
	}
	projectionUpdated := false
	if s.fastLaneDB != nil {
		tag, err := s.fastLaneDB.Exec(ctx, `
			UPDATE outreach_touchpoints
			SET state='SENT',sent_at=$3,
			    provider_message_id=CASE WHEN $4<>'' THEN $4 ELSE provider_message_id END,
			    stop_reason='',updated_at=now()
			WHERE organization_id=$1 AND id=$2 AND state='QUEUED'`,
			item.OrganizationID, tp.ID, sentAt.UTC(), acc.ProviderMessageID)
		if err != nil {
			log.Warn().Err(err).Str("message_key", item.MessageKey).
				Msg("confenge fast lane: touchpoint projection lagging an accepted send")
		} else {
			projectionUpdated = tag.RowsAffected() == 1
		}
	} else if err := s.repo.UpdateTouchpoint(ctx, &projected); err != nil {
		log.Warn().Err(err).Str("message_key", item.MessageKey).
			Msg("confenge fast lane: touchpoint projection lagging an accepted send")
	} else {
		projectionUpdated = true
	}
	if err := s.markDelegatedFirstTouchSent(ctx, item.OrganizationID, tp.ID, sentAt); err != nil {
		log.Warn().Err(err).Str("message_key", item.MessageKey).
			Msg("confenge fast lane: delegated decision lagging an accepted send")
	}
	if !projectionUpdated {
		return
	}
	if err := s.releaseNextTouch(ctx, item.OrganizationID, &projected); err != nil {
		log.Warn().Err(err).Str("message_key", item.MessageKey).
			Msg("confenge fast lane: next touch not released")
	}
}

// fastLaneSuppress records a definitive provider rejection so the same address
// cannot be attempted again by any path.
func (s *service) fastLaneSuppress(ctx context.Context, item *dispatch.QueueItem, tp *models.OutreachTouchpoint, cause error) {
	if suppressions, ok := s.repo.(interface {
		UpsertOutreachRecipientSuppression(context.Context, *models.SuppressedRecipient) error
	}); ok {
		if err := suppressions.UpsertOutreachRecipientSuppression(ctx, &models.SuppressedRecipient{
			OrganizationID: item.OrganizationID,
			Email:          strings.ToLower(strings.TrimSpace(tp.Recipient)),
			Reason:         firstNonEmpty(errText(cause), "permanent provider rejection"),
			Source:         models.DeliverabilityEventBounce,
			Metadata: map[string]interface{}{
				"message_key": item.MessageKey,
				"source":      "confenge_fast_lane_smtp",
			},
		}); err != nil {
			log.Error().Err(err).Str("message_key", item.MessageKey).
				Msg("confenge fast lane: durable recipient suppression failed")
		}
	} else {
		log.Error().Str("message_key", item.MessageKey).
			Msg("confenge fast lane: repository cannot persist recipient suppression")
	}
	if s.governor != nil && tp.Recipient != "" {
		if _, err := s.governor.CancelByRecipient(ctx, item.OrganizationID, tp.Recipient,
			"permanent_provider_rejection"); err != nil {
			log.Warn().Err(err).Msg("confenge fast lane: recipient cancel failed")
		}
	}
	if s.governor != nil {
		draft := item.DraftID
		_ = s.governor.RecordFailure(ctx, item.OrganizationID, dispatch.ChannelEmail, item.MessageKey, &draft, errText(cause))
	}
	if tp.ContactCandidateID != nil {
		if candidate, err := s.repo.GetCandidate(ctx, item.OrganizationID, *tp.ContactCandidateID); err == nil && candidate != nil {
			candidate.Bounced = true
			candidate.VerificationStatus = models.OutreachVerifyBounced
			if _, err = s.repo.UpsertCandidate(ctx, candidate); err != nil {
				log.Warn().Err(err).Msg("confenge fast lane: candidate bounce projection failed")
			}
		}
	}
	tp.State = models.TouchpointBounced
	tp.StopReason = "permanent_provider_rejection"
	if err := s.repo.UpdateTouchpoint(ctx, tp); err != nil {
		log.Warn().Err(err).Msg("confenge fast lane: bounce projection failed")
	}
}
