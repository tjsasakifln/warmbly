package confenge

import (
	"errors"
	"os"
	"strings"

	"github.com/warmbly/warmbly/internal/models"
)

// The composer decides the message before it writes it. A MessageBrief answers
// who is being written to, why this company, which public fact may be spoken,
// what that fact safely means, why CONFENGE is writing and the single action
// wanted. Prose is rendered from the brief and from nothing else, so no field
// reaches the recipient just because the database happened to hold it.

// EnvSenderName overrides the human sender resolved from the signature block.
const EnvSenderName = "CONFENGE_SENDER_NAME"

// SenderIdentity is the real person signing the mail. It is resolved, never
// invented: an unresolvable sender fails composition.
type SenderIdentity struct {
	FirstName string
	FullName  string
}

// roleWords can never stand in for a human sender name.
var senderRoleWords = map[string]bool{
	"equipe": true, "time": true, "comercial": true, "contato": true,
	"suporte": true, "atendimento": true, "vendas": true, "confenge": true,
	"consultor": true, "eng": true, "engenheiro": true, "empresa": true,
}

var errSenderUnresolved = errors.New("sender identity unresolved")

// ResolveSenderIdentity reads the configured sender. It prefers the explicit
// env override and otherwise parses the signature block, which is the identity
// of record for first-touch mail.
func ResolveSenderIdentity() (SenderIdentity, error) {
	if v := strings.TrimSpace(os.Getenv(EnvSenderName)); v != "" {
		return senderFromFullName(v)
	}
	for _, line := range strings.Split(SignaturePlainBlock, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasSuffix(line, ",") {
			continue
		}
		if id, err := senderFromFullName(line); err == nil {
			return id, nil
		}
	}
	return SenderIdentity{}, errSenderUnresolved
}

// senderFromFullName strips an honorific and keeps a human-shaped given name.
func senderFromFullName(full string) (SenderIdentity, error) {
	full = strings.TrimSpace(full)
	// A signature line carrying a role or channel is not a person.
	if strings.ContainsAny(full, "|@") || strings.ContainsAny(full, "0123456789") {
		return SenderIdentity{}, errSenderUnresolved
	}
	fields := strings.Fields(full)
	cleaned := make([]string, 0, len(fields))
	for _, f := range fields {
		key := foldASCII(strings.ToLower(strings.Trim(f, ".")))
		if key == "eng" || key == "sr" || key == "sra" || key == "dr" || key == "dra" {
			continue
		}
		cleaned = append(cleaned, f)
	}
	if len(cleaned) == 0 {
		return SenderIdentity{}, errSenderUnresolved
	}
	first := cleaned[0]
	key := foldASCII(strings.ToLower(first))
	if len([]rune(first)) < 3 || senderRoleWords[key] {
		return SenderIdentity{}, errSenderUnresolved
	}
	for _, r := range first {
		if !strings.ContainsRune("abcdefghijklmnopqrstuvwxyzáàãâéêíóôõúç", []rune(strings.ToLower(string(r)))[0]) {
			return SenderIdentity{}, errSenderUnresolved
		}
	}
	return SenderIdentity{FirstName: titleWordPT(first), FullName: strings.Join(cleaned, " ")}, nil
}

// AskKind is the single action the message asks for. The ask follows the route:
// a company-wide mailbox genuinely needs routing, a department mailbox may
// already be the right team, and a personal mailbox is never asked to forward
// the mail to somebody else.
const (
	AskRouting         = "ASK_ROUTING"
	AskOwnerDirect     = "ASK_OWNER_DIRECT"
	AskDepartmentOwns  = "ASK_DEPARTMENT_OWNS"
	AskPersonalMailbox = "ASK_PERSONAL_MAILBOX"
)

// BriefRecipient carries only what is proven about the reader.
type BriefRecipient struct {
	PersonFirstName string
	PersonProven    bool
}

// MessageBrief is the semantic message. Rendering may read this and nothing
// else about the account.
type MessageBrief struct {
	Sender     SenderIdentity
	Company    string
	RouteClass string
	Recipient  BriefRecipient
	Fact       PublicFactDigest
	// Practice is the one line of why CONFENGE is writing, in plain words.
	Practice string
	AskKind  string
	// ReasonCodes is non-empty when the brief cannot become a good message.
	ReasonCodes []string
}

// Messageable reports whether the brief can be rendered at all.
func (b MessageBrief) Messageable() bool { return len(b.ReasonCodes) == 0 }

// basePractice is what CONFENGE can say when the lead carries no service the
// playbook knows. It claims no specialty, because claiming one we cannot tie to
// a service code would be inventing the reason for writing.
const basePractice = "Trabalho com apoio a empresas de engenharia em contratos públicos"

// practiceForAccount says what CONFENGE would actually do for this lead, read
// from the service playbook. Two accounts on different services say different
// things because the work differs, not because a phrase was rotated.
func practiceForAccount(acc *models.OutreachAccount) string {
	if acc == nil {
		return basePractice
	}
	pb, err := LoadPlaybook()
	if err != nil {
		return basePractice
	}
	svc := pb.ResolveServicePlaybook(acc.ServiceCode)
	// With no service on the account the moment is the only thing that says what
	// work is in play, so it is read as the service. With a service already set
	// the moment may only sharpen it, and only when it names a concrete
	// contractual event: a context moment such as a portfolio review names no
	// work and must not pull every lead onto one sentence.
	if svc == nil {
		svc = pb.ResolveServicePlaybook(acc.MomentCode)
	} else if t := pb.ResolveTrigger(acc.MomentCode); t != nil && strings.TrimSpace(t.RefinesService) != "" {
		if sharper := pb.ResolveServicePlaybook(t.RefinesService); sharper != nil {
			svc = sharper
		}
	}
	if svc != nil {
		if p := strings.TrimSpace(svc.OutboundPractice); p != "" {
			return p
		}
	}
	return basePractice
}

// askKindForRoute picks the one question that fits the door being knocked on.
func askKindForRoute(class string, personProven bool) string {
	switch strings.ToUpper(strings.TrimSpace(class)) {
	case RouteClassDirectPerson:
		if personProven {
			return AskOwnerDirect
		}
		// A personal mailbox with no proven name: ask whether the subject is
		// theirs, never ask the owner of the box to route us past themselves.
		return AskPersonalMailbox
	case RouteClassRoleOrDepartment:
		return AskDepartmentOwns
	default:
		return AskRouting
	}
}

// renderAsk writes the closing paragraph. Every branch states the practice
// first, so the question is anchored to a reason and not to a template slot.
// The practice line is always closed with a period rather than joined with a
// conjunction, because a practice sentence that already contains one would
// otherwise read as two clauses stapled together.
func renderAsk(practice, kind string) string {
	switch kind {
	case AskOwnerDirect:
		return practice + ". Faz sentido conversarmos sobre essa frente?"
	case AskPersonalMailbox:
		return practice + ". Essa frente passa por você ou por outra pessoa aí?"
	case AskDepartmentOwns:
		return practice + ". Essa frente fica com a área de vocês ou devo procurar outra?"
	default:
		return practice + ". Queria falar com quem cuida dessa frente por aí. Você consegue me indicar a pessoa certa?"
	}
}

// BuildMessageBrief decides the message. It refuses rather than padding: a
// lead with no sayable public fact is enrichment work, not filler copy.
func BuildMessageBrief(acc *models.OutreachAccount, cand *models.OutreachContactCandidate, class string) MessageBrief {
	b := MessageBrief{RouteClass: strings.ToUpper(strings.TrimSpace(class))}
	sender, err := ResolveSenderIdentity()
	if err != nil {
		b.ReasonCodes = append(b.ReasonCodes, "sender_identity_unresolved")
		return b
	}
	b.Sender = sender
	if acc == nil {
		b.ReasonCodes = append(b.ReasonCodes, "account_absent")
		return b
	}
	b.Company = editorialCompanyName(acc)
	if b.Company == "" && strings.TrimSpace(firstNonEmpty(acc.NomeFantasia, acc.RazaoSocial)) != "" {
		// The account has a name and it is unsayable; enrichment, not copy.
		b.ReasonCodes = append(b.ReasonCodes, "company_name_unusable")
		return b
	}
	b.Practice = practiceForAccount(acc)

	// Only a proven person may be named, and only on a direct-person route.
	if composerMaySeePersonName(cand) {
		if fn := titleFirstName(firstName(cand.Name)); fn != "" {
			b.Recipient = BriefRecipient{PersonFirstName: fn, PersonProven: true}
		}
	}

	// A department mailbox is the right door, never proof of the right reader.
	b.AskKind = askKindForRoute(b.RouteClass, b.Recipient.PersonProven)

	b.Fact = DigestPublicFact(firstNonEmpty(acc.FactToMention, acc.MomentSummary))
	if b.Fact.Phrase == "" {
		// Our own earlier sentence is not evidence. Falling back on it would let
		// a recompose launder its own output as a public record.
		for _, r := range b.Fact.Reasons {
			if r == "fact_is_composed_prose" {
				b.ReasonCodes = append(b.ReasonCodes, r)
				return b
			}
		}
		// Tier B keeps the lead alive without laundering a missing procurement
		// record into a claim. Target-fit/imported company identity is enough to
		// ask a short routing question about contracts of engineering.
		if b.Company != "" {
			b.Fact = PublicFactDigest{
				Phrase: "contratos públicos de engenharia", Subject: "Contratos públicos de engenharia",
				Contraction: "em", Relation: FactRelationCompanyContext,
				Reasons: []string{"specific_fact_absent_company_context_used"},
			}
		} else {
			b.ReasonCodes = append(b.ReasonCodes, "needs_enrichment")
			b.ReasonCodes = append(b.ReasonCodes, b.Fact.Reasons...)
		}
	}
	return b
}

// registryTailWords are the activity nouns a registry appends to a brand. A
// person drops them and says the brand: "Inplenitus", not the whole entry.
var registryTailWords = map[string]bool{
	"projetos": true, "projeto": true, "gerenciamento": true,
	"fiscalizacao": true, "consultoria": true, "assessoria": true,
	"servicos": true, "servico": true, "empreendimentos": true,
	"participacoes": true, "incorporacoes": true, "construcoes": true,
	"montagens": true, "instalacoes": true, "representacoes": true,
	"comercio": true, "industria": true, "terraplenagem": true,
	"transportes": true, "locacoes": true, "obras": true, "sistemas": true,
	"solucoes": true, "planejamento": true, "manutencao": true,
}

// editorialCompanyName picks the readable trade name and drops legal form. It
// returns empty when the registry entry cannot be spoken, which refuses the
// message rather than putting a cut name in front of a reader.
func editorialCompanyName(acc *models.OutreachAccount) string {
	name := firstNonEmpty(acc.NomeFantasia, acc.RazaoSocial)
	name = stripLegalVocative(strings.TrimSpace(name))
	if name == "" {
		return ""
	}
	name = shortTradingName(name)
	if name == "" {
		return ""
	}
	if allCapsWordIn(name) {
		name = strings.Join(mapFields(strings.Fields(name), func(w string) string {
			if knownAcronymUpper(foldASCII(strings.ToLower(w))) {
				return strings.ToUpper(w)
			}
			return titleWordPT(w)
		}), " ")
	}
	name = strings.TrimSpace(name)
	// A registry field cut at its width limit ends mid word; never say it.
	if truncatedWordIn(name) {
		return ""
	}
	return name
}

// shortTradingName reduces a registry entry to the brand a person says out
// loud, dropping the enumerated activities the registry appends to it.
func shortTradingName(name string) string {
	if i := strings.IndexAny(name, ",;/"); i > 0 {
		if head := strings.TrimSpace(name[:i]); head != "" {
			name = head
		}
	}
	fields := strings.Fields(name)
	for len(fields) > 1 {
		last := foldASCII(strings.ToLower(strings.Trim(fields[len(fields)-1], ".")))
		if !registryTailWords[last] && !prepositionsPT[last] {
			break
		}
		fields = fields[:len(fields)-1]
	}
	// Nobody says a five word company name in a first sentence.
	if len(fields) > 4 {
		fields = fields[:4]
	}
	return strings.Join(fields, " ")
}

func mapFields(in []string, f func(string) string) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		out = append(out, f(v))
	}
	return out
}

// RenderBrief writes the email. Every sentence traces to a brief field; there
// is no template slot that can be filled by an unexplained database value.
func RenderBrief(b MessageBrief) ComposedInitial {
	out := ComposedInitial{
		FactSource: FactSourceNone,
		CTASource:  CTASourceRouting,
	}
	if !b.Messageable() {
		return out
	}
	greeting := "Olá"
	if b.Recipient.PersonProven {
		greeting = "Olá, " + b.Recipient.PersonFirstName
	}
	out.Greeting = greeting

	// One observation sentence, phrased to the relation the evidence proves.
	subjectOfFact := "a empresa"
	if b.Company != "" {
		subjectOfFact = "a " + b.Company
	}
	contraction := b.Fact.Contraction
	if contraction == "" {
		contraction = "na"
	}
	observation := "Vi uma contratação envolvendo " + subjectOfFact + " " + contraction + " " + b.Fact.Phrase + "."
	if b.Fact.Relation == FactRelationCompanyContext {
		companyRef := "da empresa"
		if strings.TrimSpace(b.Company) != "" {
			companyRef = "da " + strings.TrimSpace(b.Company)
		}
		observation = "Entrei em contato por causa da atuação " + companyRef + " em contratos públicos de engenharia."
	}

	ask := renderAsk(b.Practice, b.AskKind)

	var sb strings.Builder
	sb.WriteString(greeting)
	sb.WriteString(",\n\n")
	sb.WriteString("Meu nome é ")
	sb.WriteString(b.Sender.FirstName)
	sb.WriteString(", da CONFENGE. ")
	sb.WriteString(observation)
	sb.WriteString("\n\n")
	sb.WriteString(ask)
	sb.WriteString("\n\nObrigado,\n")
	sb.WriteString(b.Sender.FirstName)

	out.Body = sb.String()
	out.Subject = b.Fact.Subject
	out.ObservedFact = observation
	out.FactSource = FactSourceFactToMention
	out.CTA = ask
	out.Theme = b.Practice
	if b.AskKind == AskOwnerDirect || b.AskKind == AskPersonalMailbox {
		out.CTASource = CTASourceAccount
	}
	return out
}

// ComposeEditorialInitial is the current composer: brief, then prose, then a
// refusal when the brief cannot carry a good message.
func ComposeEditorialInitial(acc *models.OutreachAccount, cand *models.OutreachContactCandidate, class string) (ComposedInitial, []string) {
	b := BuildMessageBrief(acc, cand, class)
	if !b.Messageable() {
		return ComposedInitial{FactSource: FactSourceDropped, CTASource: CTASourceDefault}, b.ReasonCodes
	}
	return RenderBrief(b), nil
}

// ComposeEditorialTouch renders the same semantic brief for an initial or a
// follow-up. Follow-ups are written as a continuation and never re-send the
// first pitch or introduce a new unsupported fact.
func ComposeEditorialTouch(acc *models.OutreachAccount, cand *models.OutreachContactCandidate, class, channel, priorSubject string) (ComposedInitial, []string) {
	b := BuildMessageBrief(acc, cand, class)
	if !b.Messageable() {
		return ComposedInitial{FactSource: FactSourceDropped, CTASource: CTASourceDefault}, b.ReasonCodes
	}
	if channel != ChannelEmailFollowup {
		return RenderBrief(b), nil
	}

	greeting := "Olá"
	if b.Recipient.PersonProven {
		greeting = "Olá, " + b.Recipient.PersonFirstName
	}
	ask := "Você consegue me indicar quem cuida dessa frente por aí?"
	switch b.AskKind {
	case AskOwnerDirect:
		ask = "Faz sentido conversarmos sobre essa frente?"
	case AskPersonalMailbox:
		ask = "Essa frente passa por você ou por outra pessoa aí?"
	case AskDepartmentOwns:
		ask = "Essa frente fica com vocês ou devo procurar outra área?"
	}
	body := greeting + ",\n\nRetomo meu contato sobre " + b.Fact.Phrase + ". " + ask +
		"\n\nObrigado,\n" + b.Sender.FirstName
	subject := strings.TrimSpace(priorSubject)
	if subject == "" {
		subject = b.Fact.Subject
	}
	if !strings.HasPrefix(strings.ToLower(subject), "re:") {
		subject = "Re: " + subject
	}
	return ComposedInitial{
		Subject:      subject,
		Body:         body,
		Greeting:     greeting,
		ObservedFact: b.Fact.Phrase,
		FactSource:   FactSourceFactToMention,
		CTA:          ask,
		CTASource:    CTASourceRouting,
		Theme:        b.Practice,
	}, nil
}

// editorialSenderFirstName is the resolved sender for QA context. An
// unresolvable sender already failed the brief, so the empty string here only
// ever reaches a path that is being refused anyway.
func editorialSenderFirstName() string {
	id, err := ResolveSenderIdentity()
	if err != nil {
		return ""
	}
	return id.FirstName
}
