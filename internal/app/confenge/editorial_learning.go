package confenge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

const EditorialGuidelineVersion = "confenge.editorial-guidelines.v1"

// recordEditorialSignal aggregates a defect without persisting recipient PII
// or the full message. The issue outbox is written in the same transaction so
// a temporarily unavailable GitHub never loses the engineering work item.
func (s *service) recordEditorialSignal(ctx context.Context, orgID uuid.UUID, draftID, touchpointID *uuid.UUID, kind, reason, channel string) {
	if s.humanGateDB == nil {
		return
	}
	kind = strings.ToUpper(strings.TrimSpace(kind))
	reason = strings.ToLower(strings.TrimSpace(reason))
	channel = strings.ToUpper(strings.TrimSpace(channel))
	if kind == "" || reason == "" {
		return
	}
	sum := sha256.Sum256([]byte(strings.Join([]string{kind, reason, channel, ComposerVersion, PromptVersion, EditorialGuidelineVersion}, "|")))
	signature := hex.EncodeToString(sum[:16])
	title := fmt.Sprintf("editorial: %s em %s", reason, firstNonEmpty(channel, "EMAIL"))
	body := fmt.Sprintf("Defeito editorial recorrente detectado.\n\n- assinatura: `%s`\n- tipo: `%s`\n- razão: `%s`\n- canal: `%s`\n- composer: `%s`\n- prompt: `%s`\n- diretrizes: `%s`\n\nNenhum destinatário ou corpo identificável foi incluído.", signature, kind, reason, channel, ComposerVersion, PromptVersion, EditorialGuidelineVersion)

	tx, err := s.humanGateDB.Begin(ctx)
	if err != nil {
		return
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `
		INSERT INTO confenge_editorial_signals (
			organization_id, draft_id, touchpoint_id, signal_kind, reason_code,
			defect_signature, composer_version, prompt_version, guideline_version
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (organization_id, defect_signature) DO UPDATE SET
			occurrences=confenge_editorial_signals.occurrences+1,
			last_seen_at=now(), updated_at=now(),
			draft_id=COALESCE(EXCLUDED.draft_id, confenge_editorial_signals.draft_id),
			touchpoint_id=COALESCE(EXCLUDED.touchpoint_id, confenge_editorial_signals.touchpoint_id)`,
		orgID, draftID, touchpointID, kind, reason, signature,
		ComposerVersion, PromptVersion, EditorialGuidelineVersion)
	if err != nil {
		return
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO confenge_editorial_issue_outbox (
			organization_id, defect_signature, title, body_redacted
		) VALUES ($1,$2,$3,$4)
		ON CONFLICT (organization_id, defect_signature) DO UPDATE SET
			title=EXCLUDED.title, body_redacted=EXCLUDED.body_redacted,
			status=CASE WHEN confenge_editorial_issue_outbox.status='PUBLISHED' THEN 'PUBLISHED' ELSE 'PENDING' END,
			updated_at=now()`, orgID, signature, title, body)
	if err != nil {
		return
	}
	_ = tx.Commit(ctx)
}
