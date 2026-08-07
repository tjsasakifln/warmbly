package confenge

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/pkg/generation"
)

// DraftGenerator produces structured outreach copy from staging inputs only.
type DraftGenerator interface {
	Generate(ctx context.Context, acc *models.OutreachAccount, cand *models.OutreachContactCandidate, evidence []models.OutreachEvidence) (DraftOutput, string, string, error)
}

// AIDraftGenerator uses the existing generation.Provider abstraction.
type AIDraftGenerator struct {
	Provider generation.Provider
	Model    string
}

// Generate asks the model for JSON only from structured inputs (no research).
func (g *AIDraftGenerator) Generate(ctx context.Context, acc *models.OutreachAccount, cand *models.OutreachContactCandidate, evidence []models.OutreachEvidence) (DraftOutput, string, string, error) {
	if g == nil || g.Provider == nil {
		return DraftOutput{}, "", "", generation.ErrNotConfigured
	}
	system := draftSystemPrompt()
	user := draftUserPrompt(acc, cand, evidence)
	model := g.Model
	if model == "" {
		model = g.Provider.ModelForTier(true)
	}
	res, err := g.Provider.Complete(ctx, generation.CompletionRequest{
		System:      system,
		Prompt:      user,
		Model:       model,
		MaxTokens:   1200,
		Temperature: generation.Deterministic(),
	})
	if err != nil {
		return DraftOutput{}, g.Provider.Name(), model, err
	}
	out, err := parseDraftJSON(res.Text)
	if err != nil {
		return DraftOutput{}, g.Provider.Name(), res.Model, err
	}
	return out, g.Provider.Name(), res.Model, nil
}

// TemplateGenerator is the safe fallback when AI is off.
type TemplateGenerator struct{}

func (TemplateGenerator) Generate(ctx context.Context, acc *models.OutreachAccount, cand *models.OutreachContactCandidate, evidence []models.OutreachEvidence) (DraftOutput, string, string, error) {
	_ = ctx
	_ = evidence
	return TemplateDraft(acc, cand), "template", "deterministic", nil
}

func draftSystemPrompt() string {
	return strings.TrimSpace(`
Você redige e-mails comerciais curtos em português brasileiro para a CONFENGE (engenharia consultiva).
Você NÃO pesquisa. Use somente os dados estruturados do usuário.
Não transforme hipótese em fato. Não invente número, contrato, data, órgão, nome ou cargo ausentes dos inputs.
Não use travessões. Não use frases de IA. Não diga "espero que esta mensagem o encontre bem".
Não prometa resultado, crédito, dinheiro a receber, irregularidade ou urgência falsa.
Primeira mensagem: até ~120 palavras, até 3 parágrafos curtos, uma pergunta, um CTA, um serviço.
Follow-ups menores, mesmo fio, sem repetir o primeiro e-mail.
Responda APENAS com JSON válido no schema:
{"subject":"","body_text":"","body_html":"","followups":[{"delay_days":3,"subject_mode":"same_thread","body_text":"","body_html":""}],"fact_used":"","evidence_ids":[],"service_code":"","question":"","cta":"","risk_flags":[]}
`)
}

func draftUserPrompt(acc *models.OutreachAccount, cand *models.OutreachContactCandidate, evidence []models.OutreachEvidence) string {
	type payload struct {
		Company   any `json:"company"`
		Contact   any `json:"contact"`
		Moment    any `json:"moment"`
		Offer     any `json:"offer"`
		Messaging any `json:"messaging"`
		Evidence  any `json:"evidence"`
	}
	company := map[string]any{}
	moment := map[string]any{}
	offer := map[string]any{}
	messaging := map[string]any{}
	if acc != nil {
		company = map[string]any{
			"razao_social":  acc.RazaoSocial,
			"nome_fantasia": acc.NomeFantasia,
			"cnpj14":        acc.CNPJ14,
			"municipio":     acc.Municipio,
			"uf":            acc.UF,
		}
		moment = map[string]any{
			"code":    acc.MomentCode,
			"summary": acc.MomentSummary,
		}
		offer = map[string]any{
			"service_code": acc.ServiceCode,
			"service_name": acc.ServiceName,
			"entry_offer":  acc.EntryOffer,
		}
		messaging = map[string]any{
			"fact_to_mention": acc.FactToMention,
			"question_to_ask": acc.QuestionToAsk,
			"cta":             acc.CTA,
			"claims_to_avoid": acc.ClaimsToAvoid,
		}
	}
	contact := map[string]any{}
	if cand != nil {
		contact = map[string]any{
			"name":                cand.Name,
			"role":                cand.Role,
			"email":               cand.Email,
			"verification_status": cand.VerificationStatus,
		}
	}
	ev := make([]map[string]any, 0, len(evidence))
	for _, e := range evidence {
		ev = append(ev, map[string]any{
			"id":              e.SourceEvidenceID,
			"title":           e.Title,
			"synthesis":       e.Synthesis,
			"excerpt":         e.Excerpt,
			"epistemic_class": e.EpistemicClass,
			"url":             e.URL,
		})
	}
	b, _ := json.MarshalIndent(payload{
		Company: company, Contact: contact, Moment: moment,
		Offer: offer, Messaging: messaging, Evidence: ev,
	}, "", "  ")
	return "Gere a mensagem com base apenas nestes dados:\n" + string(b)
}

func parseDraftJSON(text string) (DraftOutput, error) {
	text = strings.TrimSpace(text)
	// Strip markdown fences if the model wraps JSON.
	if i := strings.Index(text, "{"); i >= 0 {
		if j := strings.LastIndex(text, "}"); j > i {
			text = text[i : j+1]
		}
	}
	var out DraftOutput
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		return DraftOutput{}, fmt.Errorf("invalid draft JSON: %w", err)
	}
	out.Subject = strings.TrimSpace(out.Subject)
	out.BodyText = strings.TrimSpace(out.BodyText)
	out.FactUsed = strings.TrimSpace(out.FactUsed)
	return out, nil
}
