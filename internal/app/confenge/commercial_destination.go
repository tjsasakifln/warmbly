package confenge

import (
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// CommercialDestinationState is the publication gate for a public destination.
type CommercialDestinationState string

const (
	CommercialDestinationActive                    CommercialDestinationState = "ACTIVE"
	CommercialDestinationWithheldPendingWebRelease CommercialDestinationState = "WITHHELD_PENDING_WEB_RELEASE"
)

// CommercialActivationState keeps preparation separate from campaign activation.
type CommercialActivationState string

const (
	CommercialActivationActive       CommercialActivationState = "ACTIVE"
	CommercialActivationNotActivated CommercialActivationState = "NOT_ACTIVATED"
)

// CommercialCampaignKind is deliberately finite. It is never derived from a lead.
type CommercialCampaignKind string

const (
	CommercialCampaignFirstTouchEmail   CommercialCampaignKind = "OUTBOUND_FIRST_TOUCH_EMAIL"
	CommercialCampaignHumanConversation CommercialCampaignKind = "HUMAN_CONVERSATION_FOLLOWUP"
)

// CommercialVertical names a segment without carrying account identity or PII.
type CommercialVertical string

const (
	CommercialVerticalB2G                CommercialVertical = "B2G_ENGINEERING_CONTRACTORS"
	CommercialVerticalPrivateEngineering CommercialVertical = "PRIVATE_ENGINEERING_COMPANIES"
	CommercialVerticalProjectOffices     CommercialVertical = "PROJECT_OFFICES_AND_PLATFORMS"
	CommercialVerticalCondominiums       CommercialVertical = "CONDOMINIUMS_AND_ADMINISTRATORS"
	CommercialVerticalExpertiseLaw       CommercialVertical = "EXPERTISE_AND_LAW_FIRMS"
	CommercialVerticalValuations         CommercialVertical = "VALUATIONS"
	CommercialVerticalOccupationalSafety CommercialVertical = "OCCUPATIONAL_HEALTH_AND_SAFETY"
	CommercialVerticalPublicEntities     CommercialVertical = "PUBLIC_ENTITIES"
)

var (
	ErrCommercialDestinationNotFound  = errors.New("commercial destination not found")
	ErrCommercialDestinationWithheld  = errors.New("commercial destination withheld")
	ErrCommercialCampaignNotAllowed   = errors.New("commercial campaign kind not allowed")
	ErrCommercialDestinationUnsafe    = errors.New("commercial destination URL is unsafe")
	ErrCommercialDestinationAmbiguous = errors.New("commercial destination is ambiguous for this vertical")
)

// CommercialDestination is the finite, public-safe destination contract.
// CanonicalURL and Anchor never carry account data. UTM values are generated
// from the landing id and campaign kind, never accepted from a caller.
type CommercialDestination struct {
	ID                   string
	Vertical             CommercialVertical
	CanonicalURL         string
	Anchor               string
	AllowedCampaignKinds []CommercialCampaignKind
	State                CommercialDestinationState
	Activation           CommercialActivationState
	PublicProofExpected  string
	TerminalAction       string
	UTMAllowlist         []string
}

// CommercialMessageRoute explains why a claim goes to one public destination.
type CommercialMessageRoute struct {
	ClaimKey         string
	VisitorSituation string
	Destination      CommercialDestination
	URL              string
}

const (
	landingB2GAddenda         = "public-addenda"
	landingB2GMeasurements    = "public-measurements-glosas"
	landingB2GRebalancing     = "public-rebalancing"
	landingB2GBudget          = "public-budget-bdi"
	landingB2GTender          = "public-tender-proposal"
	landingB2GDelay           = "public-delay-extension"
	landingB2GMonitoring      = "public-contract-monitoring"
	landingB2GOverview        = "public-contract-problems"
	landingPrivateEngineering = "private-engineering"
	landingProjectOffices     = "project-offices"
	landingCondominiums       = "condominiums-admin"
	landingExpertiseLaw       = "expertise-law"
	landingValuations         = "valuations"
	landingOccupationalSafety = "occupational-safety"
	landingPublicEntities     = "public-entities"
)

var commercialUTMAllowlist = []string{"utm_source", "utm_medium", "utm_campaign", "utm_content"}

var commercialDestinationRegistry = []CommercialDestination{
	{
		ID: landingB2GAddenda, Vertical: CommercialVerticalB2G,
		CanonicalURL: "https://confenge.com.br/aditivos-obras-publicas/", Anchor: "metodo",
		AllowedCampaignKinds: activeCommercialCampaignKinds(), State: CommercialDestinationActive, Activation: CommercialActivationActive,
		PublicProofExpected: "Método, documentos e limites para tratar aditivos em obras públicas.",
		TerminalAction:      "Registrar a situação na captura da própria superfície ou iniciar conversa humana.", UTMAllowlist: commercialUTMKeys(),
	},
	{
		ID: landingB2GMeasurements, Vertical: CommercialVerticalB2G,
		CanonicalURL: "https://confenge.com.br/medicoes-glosas-obras-publicas/", Anchor: "metodo",
		AllowedCampaignKinds: activeCommercialCampaignKinds(), State: CommercialDestinationActive, Activation: CommercialActivationActive,
		PublicProofExpected: "Critério de medição, prova contemporânea, glosa e caminho para recebimento.",
		TerminalAction:      "Registrar a demanda ou falar com a CONFENGE a partir da própria superfície.", UTMAllowlist: commercialUTMKeys(),
	},
	{
		ID: landingB2GRebalancing, Vertical: CommercialVerticalB2G,
		CanonicalURL: "https://confenge.com.br/reequilibrio-obras-publicas/", Anchor: "metodo",
		AllowedCampaignKinds: activeCommercialCampaignKinds(), State: CommercialDestinationActive, Activation: CommercialActivationActive,
		PublicProofExpected: "Diferença entre reajuste e reequilíbrio, método de nexo e documentos do pleito.",
		TerminalAction:      "Registrar o contexto contratual ou iniciar conversa humana na própria superfície.", UTMAllowlist: commercialUTMKeys(),
	},
	{
		ID: landingB2GBudget, Vertical: CommercialVerticalB2G,
		CanonicalURL: "https://confenge.com.br/auditoria-orcamento-licitacao/", Anchor: "metodo",
		AllowedCampaignKinds: activeCommercialCampaignKinds(), State: CommercialDestinationActive, Activation: CommercialActivationActive,
		PublicProofExpected: "Método de auditoria de orçamento, BDI, SINAPI e SICRO antes da proposta.",
		TerminalAction:      "Registrar o edital ou avançar para conversa humana pela própria superfície.", UTMAllowlist: commercialUTMKeys(),
	},
	{
		ID: landingB2GTender, Vertical: CommercialVerticalB2G,
		CanonicalURL: "https://confenge.com.br/bid-room-licitacoes-obras/", Anchor: "quando-nao-contratar",
		AllowedCampaignKinds: activeCommercialCampaignKinds(), State: CommercialDestinationActive, Activation: CommercialActivationActive,
		PublicProofExpected: "Critérios para decidir se o edital merece equipe, capital e uma operação de proposta.",
		TerminalAction:      "Enviar o contexto do edital na captura da superfície.", UTMAllowlist: commercialUTMKeys(),
	},
	{
		ID: landingB2GDelay, Vertical: CommercialVerticalB2G,
		CanonicalURL: "https://confenge.com.br/atrasos-prorrogacao-obras-publicas/", Anchor: "metodo",
		AllowedCampaignKinds: activeCommercialCampaignKinds(), State: CommercialDestinationActive, Activation: CommercialActivationActive,
		PublicProofExpected: "Cronologia, avisos, prova e decisão para atrasos, prorrogações e encerramento.",
		TerminalAction:      "Registrar a situação contratual na captura da superfície.", UTMAllowlist: commercialUTMKeys(),
	},
	{
		ID: landingB2GMonitoring, Vertical: CommercialVerticalB2G,
		CanonicalURL: "https://confenge.com.br/acompanhamento-contratos-obras/", Anchor: "metodo",
		AllowedCampaignKinds: activeCommercialCampaignKinds(), State: CommercialDestinationActive, Activation: CommercialActivationActive,
		PublicProofExpected: "Rotina, responsáveis e método de acompanhamento de carteira contratual.",
		TerminalAction:      "Registrar a necessidade de acompanhamento na captura da superfície.", UTMAllowlist: commercialUTMKeys(),
	},
	{
		ID: landingB2GOverview, Vertical: CommercialVerticalB2G,
		CanonicalURL:         "https://confenge.com.br/problemas-que-resolvemos/",
		AllowedCampaignKinds: activeCommercialCampaignKinds(), State: CommercialDestinationActive, Activation: CommercialActivationActive,
		PublicProofExpected: "Mapa de problemas contratuais que a CONFENGE trata, sem fingir uma especialidade não indicada pelo dado.",
		TerminalAction:      "Escolher uma situação específica e avançar pela superfície correspondente.", UTMAllowlist: commercialUTMKeys(),
	},
	withheldCommercialDestination(landingPrivateEngineering, CommercialVerticalPrivateEngineering, "https://confenge.com.br/engenharia-empresas-privadas/", "Prova pública de entregas para empresas privadas, escopo e limites."),
	withheldCommercialDestination(landingProjectOffices, CommercialVerticalProjectOffices, "https://confenge.com.br/escritorios-plataformas-projetos/", "Prova pública de coordenação, revisão e compatibilização de projetos."),
	withheldCommercialDestination(landingCondominiums, CommercialVerticalCondominiums, "https://confenge.com.br/engenharia-condominios/", "Prova pública de escopo técnico para condomínios e administradoras."),
	withheldCommercialDestination(landingExpertiseLaw, CommercialVerticalExpertiseLaw, "https://confenge.com.br/pericia-assistencia-tecnica/", "Prova pública da fronteira entre engenharia, perícia e atuação jurídica."),
	withheldCommercialDestination(landingValuations, CommercialVerticalValuations, "https://confenge.com.br/avaliacoes-engenharia/", "Prova pública de método, finalidade e limites das avaliações."),
	withheldCommercialDestination(landingOccupationalSafety, CommercialVerticalOccupationalSafety, "https://confenge.com.br/seguranca-saude-trabalho/", "Prova pública de escopo, habilitação aplicável e limites em SST."),
	withheldCommercialDestination(landingPublicEntities, CommercialVerticalPublicEntities, "https://confenge.com.br/engenharia-entes-publicos/", "Prova pública de escopo e forma de contratação adequada a entes públicos."),
}

func activeCommercialCampaignKinds() []CommercialCampaignKind {
	return []CommercialCampaignKind{CommercialCampaignFirstTouchEmail, CommercialCampaignHumanConversation}
}

func commercialUTMKeys() []string {
	return append([]string(nil), commercialUTMAllowlist...)
}

func withheldCommercialDestination(id string, vertical CommercialVertical, canonical, proof string) CommercialDestination {
	return CommercialDestination{
		ID: id, Vertical: vertical, CanonicalURL: canonical,
		AllowedCampaignKinds: activeCommercialCampaignKinds(),
		State:                CommercialDestinationWithheldPendingWebRelease, Activation: CommercialActivationNotActivated,
		PublicProofExpected: proof,
		TerminalAction:      "WITHHELD: nenhuma ação pública ou campanha pode usar esta URL antes da liberação web e da política adequada.",
		UTMAllowlist:        commercialUTMKeys(),
	}
}

// CommercialDestinations returns a defensive copy of the finite registry.
func CommercialDestinations() []CommercialDestination {
	out := make([]CommercialDestination, len(commercialDestinationRegistry))
	for i := range commercialDestinationRegistry {
		out[i] = commercialDestinationRegistry[i]
		out[i].AllowedCampaignKinds = append([]CommercialCampaignKind(nil), commercialDestinationRegistry[i].AllowedCampaignKinds...)
		out[i].UTMAllowlist = append([]string(nil), commercialDestinationRegistry[i].UTMAllowlist...)
	}
	return out
}

// CommercialDestinationForVertical returns metadata even when it is withheld.
// URL emission remains the responsibility of CommercialDestinationURL.
func CommercialDestinationForVertical(vertical CommercialVertical) (CommercialDestination, error) {
	return selectCommercialDestinationForVertical(commercialDestinationRegistry, vertical)
}

// selectCommercialDestinationForVertical is a pure function over the passed
// slice so the selection can be proven invariant under registry reordering.
// A vertical carrying more than one destination is ambiguous: the commercial
// decision belongs to the situation (service/moment), never to array order.
func selectCommercialDestinationForVertical(destinations []CommercialDestination, vertical CommercialVertical) (CommercialDestination, error) {
	var found []CommercialDestination
	for _, d := range destinations {
		if d.Vertical == vertical {
			found = append(found, d)
		}
	}
	switch len(found) {
	case 0:
		return CommercialDestination{}, ErrCommercialDestinationNotFound
	case 1:
		return found[0], nil
	default:
		return CommercialDestination{}, fmt.Errorf("%w: vertical %s has %d destinations; select by situation via ResolveCommercialMessageRoute or by id via CommercialDestinationURL", ErrCommercialDestinationAmbiguous, vertical, len(found))
	}
}

func commercialDestinationByID(id string) (CommercialDestination, error) {
	for _, d := range commercialDestinationRegistry {
		if d.ID == id {
			return d, nil
		}
	}
	return CommercialDestination{}, ErrCommercialDestinationNotFound
}

// ResolveCommercialMessageRoute maps only existing Warmbly service/moment
// taxonomy. It never reads a personalized claim or a lead identifier.
func ResolveCommercialMessageRoute(serviceCode, momentCode string, kind CommercialCampaignKind) (CommercialMessageRoute, error) {
	claimKey, situation, landingID := commercialRouteRule(serviceCode, momentCode)
	destination, err := commercialDestinationByID(landingID)
	if err != nil {
		return CommercialMessageRoute{}, err
	}
	resolvedURL, err := CommercialDestinationURL(destination.ID, kind)
	if err != nil {
		return CommercialMessageRoute{}, err
	}
	return CommercialMessageRoute{ClaimKey: claimKey, VisitorSituation: situation, Destination: destination, URL: resolvedURL}, nil
}

func commercialRouteRule(serviceCode, momentCode string) (claimKey, situation, landingID string) {
	code := canonicalCommercialServiceCode(serviceCode, momentCode)
	switch code {
	case "ADITIVOS", "EXTRACONTRATUAIS":
		return "ADITIVO_OU_EXTRA", "A execução mudou e a empresa precisa formalizar escopo, efeito e prova.", landingB2GAddenda
	case "MEDICOES":
		return "MEDICAO_OU_GLOSA", "Há medição, glosa, memória ou recebimento sob discussão.", landingB2GMeasurements
	case "REAJUSTE", "REEQUILIBRIO":
		return "REAJUSTE_OU_REEQUILIBRIO", "A empresa precisa distinguir o mecanismo e organizar nexo, cálculo e documentos.", landingB2GRebalancing
	case "PLANILHAS":
		return "ORCAMENTO_OU_BDI", "O edital ou a planilha pode comprometer preço e margem antes da proposta.", landingB2GBudget
	case "APOIO_LICITACAO":
		return "EDITAL_OU_PROPOSTA", "A empresa precisa decidir se disputa o edital e como organizar a proposta.", landingB2GTender
	case "INTELIGENCIA_PNCP":
		// Market intelligence is not proposal preparation. The B2G situation is
		// confirmed, but no specific contractual pain is, so this goes to the
		// overview instead of claiming a tender the company may not be running.
		return "MERCADO_PUBLICO_OU_RECORTE_PNCP", "A empresa acompanha oportunidades e recortes do mercado público, sem edital ou disputa confirmada.", landingB2GOverview
	case "ENCERRAMENTO_CONTRATUAL":
		return "ATRASO_PRORROGACAO_OU_ENCERRAMENTO", "Prazo, atraso, prorrogação ou encerramento exigem cronologia e decisão.", landingB2GDelay
	case "MONITORAMENTO_CONTRATUAL", "BACKOFFICE":
		return "CARTEIRA_OU_ROTINA_CONTRATUAL", "A empresa precisa priorizar eventos e responsáveis na carteira contratual.", landingB2GMonitoring
	default:
		return "CONTRATO_PUBLICO_SEM_DOR_CONFIRMADA", "O dado confirma atuação B2G, mas não autoriza inventar uma dor específica.", landingB2GOverview
	}
}

func canonicalCommercialServiceCode(serviceCode, momentCode string) string {
	pb, err := LoadPlaybook()
	if err == nil {
		if service := pb.ResolveServicePlaybook(serviceCode); service != nil {
			if trigger := pb.ResolveTrigger(momentCode); trigger != nil && strings.TrimSpace(trigger.RefinesService) != "" {
				if refined := pb.ResolveServicePlaybook(trigger.RefinesService); refined != nil {
					return strings.ToUpper(strings.TrimSpace(refined.Code))
				}
			}
			return strings.ToUpper(strings.TrimSpace(service.Code))
		}
		if service := pb.ResolveServicePlaybook(momentCode); service != nil {
			return strings.ToUpper(strings.TrimSpace(service.Code))
		}
		if trigger := pb.ResolveTrigger(momentCode); trigger != nil {
			return strings.ToUpper(strings.TrimSpace(trigger.RefinesService))
		}
	}
	return strings.ToUpper(strings.TrimSpace(firstNonEmpty(serviceCode, momentCode)))
}

// CommercialDestinationURL emits only an active, campaign-compatible URL.
// There is no parameter for name, email, phone, CNPJ, process, contract or copy.
func CommercialDestinationURL(landingID string, kind CommercialCampaignKind) (string, error) {
	destination, err := commercialDestinationByID(landingID)
	if err != nil {
		return "", err
	}
	if destination.State != CommercialDestinationActive || destination.Activation != CommercialActivationActive {
		return "", ErrCommercialDestinationWithheld
	}
	if !commercialCampaignAllowed(destination, kind) {
		return "", ErrCommercialCampaignNotAllowed
	}
	parsed, err := url.Parse(destination.CanonicalURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host != "confenge.com.br" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", ErrCommercialDestinationUnsafe
	}
	medium, campaign, ok := commercialAttribution(kind)
	if !ok {
		return "", ErrCommercialCampaignNotAllowed
	}
	query := url.Values{
		"utm_source":   {"warmbly"},
		"utm_medium":   {medium},
		"utm_campaign": {campaign},
		"utm_content":  {destination.ID},
	}
	parsed.RawQuery = query.Encode()
	parsed.Fragment = destination.Anchor
	result := parsed.String()
	if err := ValidateCommercialDestinationURL(result, destination); err != nil {
		return "", err
	}
	return result, nil
}

func commercialCampaignAllowed(destination CommercialDestination, kind CommercialCampaignKind) bool {
	for _, allowed := range destination.AllowedCampaignKinds {
		if allowed == kind {
			return true
		}
	}
	return false
}

func commercialAttribution(kind CommercialCampaignKind) (medium, campaign string, ok bool) {
	switch kind {
	case CommercialCampaignFirstTouchEmail:
		return "email", "first_touch_contracts", true
	case CommercialCampaignHumanConversation:
		return "operator", "conversation_followup", true
	default:
		return "", "", false
	}
}

// ValidateCommercialDestinationURL rejects arbitrary query keys and values.
// The accepted UTM tuple is the exact finite tuple generated for this landing.
func ValidateCommercialDestinationURL(raw string, destination CommercialDestination) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host != "confenge.com.br" || parsed.User != nil {
		return ErrCommercialDestinationUnsafe
	}
	canonical, err := url.Parse(destination.CanonicalURL)
	if err != nil || parsed.EscapedPath() != canonical.EscapedPath() || parsed.Fragment != destination.Anchor {
		return ErrCommercialDestinationUnsafe
	}
	allowedKeys := append([]string(nil), destination.UTMAllowlist...)
	sort.Strings(allowedKeys)
	gotKeys := make([]string, 0, len(parsed.Query()))
	for key, values := range parsed.Query() {
		gotKeys = append(gotKeys, key)
		if len(values) != 1 || strings.TrimSpace(values[0]) == "" {
			return ErrCommercialDestinationUnsafe
		}
	}
	sort.Strings(gotKeys)
	if strings.Join(gotKeys, "\x00") != strings.Join(allowedKeys, "\x00") {
		return ErrCommercialDestinationUnsafe
	}
	q := parsed.Query()
	if q.Get("utm_source") != "warmbly" || q.Get("utm_content") != destination.ID {
		return ErrCommercialDestinationUnsafe
	}
	for _, kind := range destination.AllowedCampaignKinds {
		medium, campaign, ok := commercialAttribution(kind)
		if ok && q.Get("utm_medium") == medium && q.Get("utm_campaign") == campaign {
			return nil
		}
	}
	return fmt.Errorf("%w: attribution tuple", ErrCommercialDestinationUnsafe)
}
