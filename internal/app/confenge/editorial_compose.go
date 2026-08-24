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

// AskKind is the single action the message asks for.
const (
	AskRouting     = "ASK_ROUTING"
	AskOwnerDirect = "ASK_OWNER_DIRECT"
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

// practiceForAccount says what CONFENGE does, tied to the account's own
// service context so the sentence is not identical across the corpus.
func practiceForAccount(acc *models.OutreachAccount) string {
	const base = "Trabalho com apoio a empresas de engenharia em contratos públicos"
	if acc == nil {
		return base
	}
	switch strings.ToUpper(strings.TrimSpace(acc.MomentCode)) {
	case "REAJUSTE", "REAJUSTE_14133":
		return "Trabalho com reajuste e reequilíbrio em contratos públicos de engenharia"
	case "ADITIVO", "ADDENDUM", "CONTRACT_EXTENSION":
		return "Trabalho com aditivos e prorrogação em contratos públicos de engenharia"
	case "GLOSA_MEDICAO":
		return "Trabalho com medição e glosa em contratos públicos de engenharia"
	case "LICITACAO", "EDITAL":
		return "Trabalho com apoio a empresas de engenharia em licitações públicas"
	}
	return base
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
	b.Practice = practiceForAccount(acc)

	// Only a proven person may be named, and only on a direct-person route.
	if composerMaySeePersonName(cand) {
		if fn := titleFirstName(firstName(cand.Name)); fn != "" {
			b.Recipient = BriefRecipient{PersonFirstName: fn, PersonProven: true}
		}
	}

	// A department mailbox is the right door, never proof of the right reader,
	// so every route but a proven person asks to be routed.
	if b.Recipient.PersonProven && b.RouteClass == RouteClassDirectPerson {
		b.AskKind = AskOwnerDirect
	} else {
		b.AskKind = AskRouting
	}

	b.Fact = DigestPublicFact(firstNonEmpty(acc.FactToMention, acc.MomentSummary))
	if b.Fact.Phrase == "" {
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

// editorialCompanyName picks the readable trade name and drops legal form.
func editorialCompanyName(acc *models.OutreachAccount) string {
	name := firstNonEmpty(acc.NomeFantasia, acc.RazaoSocial)
	name = stripLegalVocative(strings.TrimSpace(name))
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
	return strings.TrimSpace(name)
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

	ask := ""
	switch b.AskKind {
	case AskOwnerDirect:
		ask = b.Practice + ". Faz sentido conversarmos sobre essa frente?"
	default:
		ask = b.Practice + " e queria falar com quem cuida dessa frente por aí. Você consegue me indicar a pessoa certa?"
	}

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
	if b.AskKind == AskOwnerDirect {
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
	if b.AskKind == AskOwnerDirect {
		ask = "Faz sentido conversarmos sobre essa frente?"
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
