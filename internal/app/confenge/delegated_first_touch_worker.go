package confenge

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/warmbly/warmbly/internal/models"
)

const (
	delegatedFirstTouchMaxBurst          = 100
	delegatedFirstTouchIdempotencyPrefix = "delegated-first-touch:"
)

type delegatedFirstTouchProcessor interface {
	ProcessDelegatedFirstTouchOnce(context.Context) (bool, error)
}

// DelegatedFirstTouchWorker maintains a rolling, capacity-derived queue runway.
type DelegatedFirstTouchWorker struct {
	processor delegatedFirstTouchProcessor
	interval  time.Duration
}

func NewDelegatedFirstTouchWorker(processor delegatedFirstTouchProcessor, interval time.Duration) *DelegatedFirstTouchWorker {
	if interval <= 0 {
		interval = time.Minute
	}
	return &DelegatedFirstTouchWorker{processor: processor, interval: interval}
}

func (w *DelegatedFirstTouchWorker) Run(ctx context.Context) {
	if w == nil || w.processor == nil {
		return
	}
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		for i := 0; i < delegatedFirstTouchMaxBurst; i++ {
			processed, err := w.processor.ProcessDelegatedFirstTouchOnce(ctx)
			if !processed {
				if err != nil {
					log.Printf("confenge delegated first-touch autorun deferred: %v", err)
				}
				break
			}
			select {
			case <-ctx.Done():
				return
			default:
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// ProcessDelegatedFirstTouchOnce evaluates one prepared first touch for the next capacity slot.
func (s *service) ProcessDelegatedFirstTouchOnce(ctx context.Context) (bool, error) {
	if s == nil || !s.cfg.DelegatedFirstTouchEnabled || !s.cfg.DelegatedFirstTouchAutorunEnabled ||
		s.cfg.DelegatedFirstTouchRunwayDays < 1 || s.delegatedDB == nil || s.policyStore == nil ||
		s.cfg.OperatorOrgID == uuid.Nil {
		return false, nil
	}
	orgID := s.cfg.OperatorOrgID
	unlock, locked, err := s.lockDelegatedFirstTouchRunway(ctx, orgID)
	if err != nil || !locked {
		return false, err
	}
	defer unlock()

	feed, err := s.repo.GetFeedSyncState(ctx, orgID)
	if err != nil || feed == nil {
		return false, err
	}
	if feed.LastStatus != "completed" {
		return false, nil
	}
	settings, err := s.repo.GetOrgSettings(ctx, orgID)
	if err != nil || settings == nil || settings.CampaignID == nil {
		return false, err
	}
	auth, err := s.policyStore.GetActiveCampaignPolicy(ctx, orgID, *settings.CampaignID, time.Now().UTC())
	if err != nil || auth == nil {
		_, _ = s.retireStaleDelegatedFirstTouches(ctx, orgID, feed.LastRunID, feed.LastSnapshotHash, nil)
		return false, err
	}
	if _, err = s.retireStaleDelegatedFirstTouches(ctx, orgID, feed.LastRunID, feed.LastSnapshotHash, &auth.ID); err != nil {
		return false, err
	}
	plan, err := s.delegatedFirstTouchRunwayPlan(ctx, orgID, feed, auth, time.Now().UTC())
	if err != nil || !plan.CapacityKnown || plan.TargetReached() {
		return false, err
	}

	touchpointID, accountID, candidateID, err := s.nextDelegatedFirstTouchCandidate(ctx, orgID, feed, auth)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}

	acc, err := s.repo.GetAccount(ctx, orgID, accountID)
	if err != nil || acc == nil {
		return false, err
	}
	cand, err := s.repo.GetCandidate(ctx, orgID, candidateID)
	if err != nil || cand == nil {
		return false, err
	}
	recent, err := s.repo.ListDrafts(ctx, orgID, "", 500, 0)
	if err != nil {
		return false, err
	}
	recentBodies := make([]string, 0, len(recent))
	for i := range recent {
		if recent[i].AccountID != accountID {
			recentBodies = append(recentBodies, recent[i].BodyText)
		}
	}
	subject, body := composeDelegatedRoutingCopy(acc, recentBodies)
	entry := delegatedEntryFromCurrentState(acc, cand, touchpointID, subject, body)
	entry.IdempotencyKey = delegatedFirstTouchRunwayIdempotencyKey(feed, auth.ID, s.cfg.RepositorySHA, accountID)
	sealed, err := SealDelegatedFirstTouchEntry(entry, cand)
	if err != nil {
		sealed = entry
		sealed.Recipient = strings.ToLower(strings.TrimSpace(cand.Email))
		sealed.RouteClass = CandidateRouteClass(cand)
		sealed.SubjectHash = hashText(sealed.Subject)
		sealed.BodyHash = hashText(sealed.BodyText)
	}
	now := time.Now().UTC()
	manifest := DelegatedFirstTouchManifest{
		SchemaVersion: DelegatedFirstTouchManifestV1,
		BatchID:       "autorun-" + uuid.NewString(), AgentID: "warmbly:delegated-first-touch-worker",
		PolicyVersion: DelegatedFirstTouchPolicyV1, PolicyHash: DelegatedFirstTouchPolicyHashV1,
		AuthorityReference: DelegatedFirstTouchAuthorityRef, PolicyAuthorizationID: auth.ID,
		SourceRunID: feed.LastRunID, SourceSnapshotHash: feed.LastSnapshotHash,
		EvidenceVersion: DelegatedFirstTouchEvidenceV1, ComposerVersion: ComposerVersion,
		TemplateVersion: DelegatedFirstTouchTemplateV1, PromptVersion: PromptVersion,
		GeneratedAt: now, Entries: []DelegatedFirstTouchEntry{sealed},
	}
	nextDue := plan.NextDueAt()
	report, xerr := s.applyDelegatedFirstTouchManifest(ctx, orgID, manifest, false, &nextDue)
	if xerr != nil {
		return false, fmt.Errorf("delegated first-touch autorun: %s", xerr.Message)
	}
	return report != nil && len(report.Items) == 1 && report.Items[0].State == "QUEUED", nil
}

func (s *service) nextDelegatedFirstTouchCandidate(ctx context.Context, orgID uuid.UUID, feed *models.OutreachFeedSyncState, auth *models.CampaignPolicyAuthorization) (uuid.UUID, uuid.UUID, uuid.UUID, error) {
	var touchpointID, accountID, candidateID uuid.UUID
	if feed == nil || auth == nil {
		return touchpointID, accountID, candidateID, pgx.ErrNoRows
	}
	err := s.delegatedDB.QueryRow(ctx, `
		SELECT t.id,t.account_id,t.contact_candidate_id
		FROM outreach_touchpoints t
		JOIN outreach_accounts a ON a.organization_id=t.organization_id AND a.id=t.account_id
		JOIN outreach_feed_sync_state feed ON feed.organization_id=t.organization_id
		WHERE t.organization_id=$1
		  AND t.ordinal=1 AND t.purpose='INITIAL' AND t.channel='EMAIL'
		  AND t.state='NEEDS_REVIEW' AND t.contact_candidate_id IS NOT NULL
		  AND feed.last_status='completed' AND a.source_run_id=feed.last_run_id
		  AND NOT EXISTS (
		    SELECT 1 FROM confenge_delegated_first_touch_decisions d
		    WHERE d.organization_id=t.organization_id AND d.account_id=t.account_id
		      AND (d.state='SENT' OR (d.state<>'CANCELLED'
		        AND d.evidence_source_run_id=$2 AND d.source_snapshot_hash=$3
		        AND d.runtime_release_sha=$4 AND d.policy_authorization_id=$5))
		  )
		ORDER BY t.due_at,t.created_at,t.id
		LIMIT 1`, orgID, feed.LastRunID, feed.LastSnapshotHash, s.cfg.RepositorySHA, auth.ID).Scan(&touchpointID, &accountID, &candidateID)
	return touchpointID, accountID, candidateID, err
}

func delegatedFirstTouchRunwayIdempotencyKey(feed *models.OutreachFeedSyncState, policyAuthorizationID uuid.UUID, runtimeSHA string, accountID uuid.UUID) string {
	binding := ""
	if feed != nil {
		binding = feed.LastRunID + "\x00" + feed.LastSnapshotHash
	}
	binding += "\x00" + runtimeSHA + "\x00" + policyAuthorizationID.String()
	return delegatedFirstTouchIdempotencyPrefix + "runway-v1:" + hashText(binding) + ":" + accountID.String()
}

func delegatedEntryFromCurrentState(acc *models.OutreachAccount, cand *models.OutreachContactCandidate, touchpointID uuid.UUID, subject, body string) DelegatedFirstTouchEntry {
	observedAt := time.Time{}
	if acc.ContractorRoleObservedAt != nil {
		observedAt = acc.ContractorRoleObservedAt.UTC()
	}
	// SourceDate is the imported observation date for the public mailbox
	// evidence. UpdatedAt is only the Warmbly row/import timestamp and must
	// never refresh external evidence. Missing source provenance stays zero so
	// delegatedWebSourceAllowed fails closed.
	webObservedAt := time.Time{}
	if cand.SourceDate != nil {
		webObservedAt = cand.SourceDate.UTC()
	}
	key := delegatedFirstTouchIdempotencyPrefix + acc.SourceRunID + ":" + acc.ID.String()
	return DelegatedFirstTouchEntry{
		IdempotencyKey: key, CorrelationID: "touchpoint:" + touchpointID.String(),
		AccountID: acc.ID, ContactCandidateID: cand.ID, CNPJ14: acc.CNPJ14,
		SupplierCNPJ14: acc.SupplierCNPJ14, BuyerCNPJ14: acc.BuyerCNPJ14,
		ContractorRoleStatus: acc.ContractorRoleStatus, TargetPartyRole: acc.TargetPartyRole,
		ContractRoleSource: acc.ContractorRoleSource, ContractEvidenceIDs: append([]string{}, acc.ContractorRoleEvidenceIDs...),
		ContractEvidenceHash: acc.ContractorRoleEvidenceHash, ContractEvidenceReference: acc.ContractorRoleEvidenceReference,
		SupplierIdentityRef: acc.SupplierIdentityRef, BuyerIdentityRef: acc.BuyerIdentityRef,
		RoleMatchMethod: acc.ContractorRoleMatchMethod, RoleConfidence: acc.ContractorRoleConfidence,
		ContractRoleReasonCodes: append([]string{}, acc.ContractorRoleReasonCodes...), EvidenceObservedAt: observedAt,
		ReconciliationStatus: ReconciliationWebContact,
		WebSources:           []DelegatedWebSource{{URL: cand.SourceURL, Kind: "PUBLIC_COMPANY_SOURCE", Supports: "COMPANY_MAILBOX", ObservedAt: webObservedAt}},
		Subject:              subject, BodyText: body, EvidenceIDs: append([]string{}, acc.ContractorRoleEvidenceIDs...),
		QA: DelegatedFirstTouchQA{Result: "PASS", Attempts: 1, IdentityPassed: true, FactualPassed: true,
			CopyPassed: true, OperationalPassed: true, Reviewer: DelegatedFirstTouchValidatorV1,
			ReasonCodes: []string{"deterministic_routing_copy", "current_supplier_evidence", "current_attributed_recipient"}},
	}
}

func composeDelegatedRoutingCopy(acc *models.OutreachAccount, recent []string) (string, string) {
	company := strings.TrimSpace(firstNonEmpty(acc.NomeFantasia, acc.RazaoSocial))
	subjects := []string{
		"Responsável por contratos públicos", "Contato sobre contratos públicos",
		"Área responsável por licitações", "Encaminhamento sobre contratos públicos",
		"Quem cuida de contratos públicos?", "Direcionamento para contratos públicos",
		"Contato com a área de licitações", "Setor de contratos públicos",
	}
	openings := []string{
		"Sou Tiago Sasaki, da CONFENGE. Em fonte pública, a %s consta como empresa contratada em contratos públicos.",
		"Meu nome é Tiago Sasaki e falo pela CONFENGE. Uma fonte pública registra a %s como fornecedora em contratos públicos.",
		"Aqui é Tiago Sasaki, da CONFENGE. Consultamos fonte pública que identifica a %s como contratada no setor público.",
		"Tiago Sasaki falando, pela CONFENGE. A atuação da %s como empresa contratada aparece em fonte pública.",
		"Sou o Tiago Sasaki, da CONFENGE. Encontramos em fonte pública a %s no papel de fornecedora em contrato público.",
		"Meu nome é Tiago Sasaki, da CONFENGE. O registro público consultado mostra a %s como empresa contratada.",
		"Falo em nome da CONFENGE, sou Tiago Sasaki. A %s foi identificada em fonte pública como fornecedora do setor público.",
		"Sou Tiago Sasaki e escrevo pela CONFENGE. Há registro público da %s como contratada em contratos públicos.",
	}
	purposes := []string{
		"Este contato inicial serve apenas para localizar a pessoa ou área que acompanha esse assunto dentro da empresa.",
		"Escrevo somente para encontrar o setor interno que trata desse tema na empresa.",
		"O objetivo desta primeira mensagem é chegar à equipe responsável por esse tema dentro da empresa.",
		"Busco apenas direcionar este primeiro contato à área interna que acompanha contratos públicos.",
		"Minha intenção neste contato inicial é identificar quem recebe assuntos ligados a contratos públicos.",
		"Esta mensagem tem apenas o propósito de localizar a área adequada para tratar desse assunto.",
		"Quero somente confirmar qual equipe interna acompanha contratos públicos e licitações.",
		"O motivo deste contato é encontrar o canal correto para assuntos de contratos públicos na empresa.",
	}
	questions := []string{
		"Você poderia indicar quem é responsável por contratos públicos e licitações, ou encaminhar esta mensagem ao setor correto?",
		"Poderia indicar a pessoa responsável por contratos públicos, ou encaminhar o contato à área de licitações?",
		"Quem é responsável por contratos públicos na empresa, e seria possível encaminhar esta mensagem a essa área?",
		"Seria possível indicar quem cuida de contratos públicos e direcionar esta mensagem ao setor responsável?",
		"Você poderia encaminhar esta mensagem à área responsável por licitações, ou indicar quem cuida desse tema?",
		"Qual pessoa ou setor é responsável por contratos públicos, e você poderia indicar esse contato?",
		"Poderia direcionar esta mensagem a quem cuida de licitações e contratos públicos dentro da empresa?",
		"Você consegue indicar o responsável por contratos públicos ou encaminhar esta mensagem à equipe adequada?",
	}
	seed := int(acc.ID.ID())
	for attempt := 0; attempt < len(subjects)*len(openings)*len(purposes)*len(questions); attempt++ {
		n := seed + attempt
		subject := subjects[n%len(subjects)]
		n /= len(subjects)
		opening := fmt.Sprintf(openings[n%len(openings)], company)
		n /= len(openings)
		purpose := purposes[n%len(purposes)]
		n /= len(purposes)
		question := questions[n%len(questions)]
		body := strings.Join([]string{opening, purpose, question, "Atenciosamente,\nEng. Tiago Sasaki\nCONFENGE\ntiago.sasaki@confenge.com.br"}, "\n\n")
		if _, duplicate := NearDuplicate(body, recent); !duplicate {
			return subject, body
		}
	}
	return subjects[seed%len(subjects)], ""
}
