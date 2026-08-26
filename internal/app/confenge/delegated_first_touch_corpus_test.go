package confenge

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/models"
)

type sanitizedReplaySeed struct {
	ServiceCode string
	MomentCode  string
	Fact        string
}

func TestDelegatedFirstTouchCorpusAtScale(t *testing.T) {
	replay := loadSanitizedReplaySeeds(t)
	if len(replay) != 200 {
		t.Fatalf("sanitized replay seeds=%d want 200 from the #152 feed", len(replay))
	}
	for _, size := range []int{1000, 10000} {
		t.Run(fmt.Sprintf("corpus_%d", size), func(t *testing.T) {
			started := time.Now()
			messages := syntheticDelegatedCorpus(size, replay)
			report := AuditDelegatedFirstTouchCorpus(messages)
			if report.Messages != size {
				t.Fatalf("messages=%d want %d", report.Messages, size)
			}
			if report.SourceMix["sanitized_replay"] != len(replay) {
				t.Fatalf("sanitized replay mix=%v", report.SourceMix)
			}
			if len(report.Violations) != 0 {
				for i := range messages {
					expected := buildDelegatedRoutingCopy(messages[i].Account, messages[i].Candidate, messages[i].Evidence)
					if delegatedCorpusGuessedPerson(messages[i].Copy, expected, messages[i].Candidate) {
						t.Logf("guessed-person diagnostic id=%s route=%s used=%q expected=%q body=%q", messages[i].ID, messages[i].Copy.RouteClass, messages[i].Copy.PersonUsed, expected.PersonUsed, strings.SplitN(messages[i].Copy.Body, "\n", 2)[0])
					}
					blob := messages[i].Copy.Subject + "\n" + messages[i].Copy.Body
					if LooksLikeInternalReasoning(blob) || looksLikeMetadataDump(blob) || containsDumpLabel(blob) || qaEnumRe.MatchString(blob) || qaKeyValueRe.MatchString(blob) || qaScoreRe.MatchString(blob) {
						t.Logf("metadata diagnostic id=%s subject=%q body=%q", messages[i].ID, messages[i].Copy.Subject, messages[i].Copy.Body)
					}
				}
				t.Fatalf("corpus hard violations: %+v", report.Violations)
			}
			if report.UnsupportedClaims != 0 || report.GuessedPeople != 0 || report.RouteInappropriate != 0 {
				t.Fatalf("truth or routing regression: %+v", report)
			}
			if report.LengthWords.Minimum < 45 || report.LengthWords.Maximum > 150 {
				t.Fatalf("length policy drift: %+v", report.LengthWords)
			}
			if report.NearDuplicateDefinition == "" {
				t.Fatal("near-duplicate control became unmeasurable")
			}
			encoded, err := json.Marshal(report)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("processed=%d elapsed=%s report=%s", size, time.Since(started), encoded)
		})
	}
}

func TestDelegatedCorpusAuditDetectsHardFailures(t *testing.T) {
	messages := syntheticDelegatedCorpus(8, nil)
	messages[1].Copy = messages[0].Copy
	messages[2].Copy.Body += " route_class=GENERIC_COMPANY"
	messages[3].Copy.Body = strings.Replace(messages[3].Copy.Body, "Olá,", "Olá, Roberto,", 1)
	messages[3].Copy.PersonUsed = "Roberto"
	messages[4].Copy.Body = strings.Replace(messages[4].Copy.Body, messages[4].Copy.CTA, "Você precisa responder agora.", 1)
	messages[4].Copy.CTA = "Você precisa responder agora."
	messages[5].Copy.Subject = ""
	messages[6].Copy.Body = strings.Replace(messages[6].Copy.Body, "como contratada", "como contratante", 1)
	messages[7].Copy.Body = strings.Replace(messages[7].Copy.Body, messages[7].Copy.CTA, "Podemos marcar uma reunião?", 1)
	messages[7].Copy.CTA = "Podemos marcar uma reunião?"

	report := AuditDelegatedFirstTouchCorpus(messages)
	for _, code := range []string{
		"exact_content_reused", "unsupported_claim", "guessed_person", "internal_metadata_leak",
		"offensive_or_manipulative_language", "subject_or_body_empty", "buyer_supplier_confusion", "route_inappropriate",
	} {
		if report.Violations[code] == 0 {
			t.Errorf("hard defect %s was not detected: %+v", code, report.Violations)
		}
	}
}

func syntheticDelegatedCorpus(size int, replay []sanitizedReplaySeed) []DelegatedCorpusMessage {
	messages := make([]DelegatedCorpusMessage, 0, size)
	for i := 0; i < size; i++ {
		seed := sanitizedReplaySeed{}
		source := "synthetic_structured"
		if i < len(replay) {
			seed = replay[i]
			source = "sanitized_replay"
		}
		acc, cand, evidence := syntheticDelegatedBrief(i, seed)
		messages = append(messages, ComposeDelegatedCorpusMessage(fmt.Sprintf("message-%d", i), source, acc, cand, evidence))
	}
	return messages
}

func syntheticDelegatedBrief(i int, seed sanitizedReplaySeed) (*models.OutreachAccount, *models.OutreachContactCandidate, []models.OutreachEvidence) {
	label := corpusAlphaLabel(i)
	service := seed.ServiceCode
	if service == "" {
		switch i % 100 {
		case 0:
			service = "aditivos_extracontratuais"
		case 1, 2, 3, 4, 5, 6, 7, 8, 9:
			service = "diagnostico_contratual_b2g"
		case 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20:
			service = "reforco_temporario_backoffice"
		case 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32, 33, 34, 35, 36, 37, 38, 39, 40, 41, 42, 43, 44, 45, 46, 47, 48, 49, 50, 51, 52:
			service = "gestao_monitoramento_contratual"
		default:
			service = "auditoria_orcamento_bdi"
		}
	}
	fact := strings.TrimSpace(seed.Fact)
	if fact == "" {
		fact = "Contratação de empresa para execução de pavimentação asfáltica na Avenida " + label
	}
	factID := "fact-" + label
	roleID := "role-" + label
	accountID := uuid.NewSHA1(uuid.NameSpaceOID, []byte("corpus-account:"+label))
	acc := &models.OutreachAccount{
		ID: accountID, NomeFantasia: "Construtora Aurora " + label,
		ServiceCode: service, MomentCode: firstNonEmpty(seed.MomentCode, "PORTFOLIO_REVIEW"),
		FactToMention: fact, MomentEvidenceIDs: []string{factID},
		ContractorRoleEvidenceIDs: []string{roleID}, ContractorRoleStatus: ContractorRoleConfirmed,
		TargetPartyRole: "SUPPLIER", SupplierCNPJ14: "11222333000144", BuyerCNPJ14: "99888777000166",
	}
	route := []string{RouteClassDirectPerson, RouteClassRoleOrDepartment, RouteClassGenericCompany, RouteClassPublicCompanyFreemail}[i%4]
	personUnknown := route != RouteClassDirectPerson
	discovery := []byte(fmt.Sprintf(`{"route_class":%q,"person_unknown":%t}`, route, personUnknown))
	cand := &models.OutreachContactCandidate{
		ID: uuid.NewSHA1(uuid.NameSpaceOID, []byte("corpus-candidate:"+label)), AccountID: accountID,
		Name: "Contato", Role: "Atendimento", Email: "contato." + strings.ToLower(label) + "@empresa.example",
		VerificationStatus: models.OutreachVerifyOfficialSource, EmailSendReady: true,
		OwnershipStatus: "COMPANY_OWNED", DiscoveryJSON: discovery, MailboxPurpose: "GENERIC_CONTACT",
	}
	switch route {
	case RouteClassDirectPerson:
		cand.Name, cand.Role, cand.MailboxPurpose = "Ana Souza", "Gerente de Contratos", "PERSONAL_WORK"
	case RouteClassRoleOrDepartment:
		cand.Name, cand.Role, cand.MailboxPurpose = "Equipe", "Licitações", "LICITACOES"
	case RouteClassPublicCompanyFreemail:
		cand.Email = "empresa." + strings.ToLower(label) + "@gmail.com"
	}
	evidence := []models.OutreachEvidence{
		{SourceEvidenceID: roleID, EpistemicClass: models.OutreachEpistemicConfirmedFact, Synthesis: "A empresa aparece como contratada no setor público."},
		{SourceEvidenceID: factID, EpistemicClass: models.OutreachEpistemicConfirmedFact, Synthesis: fact},
	}
	return acc, cand, evidence
}

func corpusAlphaLabel(i int) string {
	// Fixed-width alphabetic identity keeps synthetic facts unique without
	// leaking test counters into copy, which the delegated policy forbids.
	i++
	letters := make([]byte, 5)
	for pos := len(letters) - 1; pos >= 0; pos-- {
		letters[pos] = byte('A' + i%26)
		i /= 26
	}
	return string(letters)
}

func loadSanitizedReplaySeeds(t *testing.T) []sanitizedReplaySeed {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join("..", "..", "..", "data", "confenge-feeds", "full_national", "chunk_*.json"))
	if err != nil {
		t.Fatal(err)
	}
	var seeds []sanitizedReplaySeed
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var feed Feed
		if err := json.Unmarshal(raw, &feed); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		for i := range feed.Leads {
			lead := feed.Leads[i]
			seed := sanitizedReplaySeed{ServiceCode: lead.Offer.ServiceCode, MomentCode: lead.Moment.Code}
			if len(lead.Contracts) > 0 {
				var contract struct {
					Object string `json:"object"`
				}
				_ = json.Unmarshal(lead.Contracts[0], &contract)
				seed.Fact = strings.TrimSpace(contract.Object)
			}
			if seed.Fact == "" {
				seed.Fact = strings.TrimSpace(lead.MessagingContext.FactToMention)
			}
			// Company, contact, agency, values and identifiers are deliberately
			// discarded. Only service, moment and contract-object shape replay.
			seeds = append(seeds, seed)
		}
	}
	return seeds
}
