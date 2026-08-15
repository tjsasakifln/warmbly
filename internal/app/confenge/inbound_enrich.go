package confenge

import (
	"strings"

	"github.com/warmbly/warmbly/internal/models"
)

// InboundFacts is the merge of lead-supplied and extra-cli published
// material. Missing facts stay UNKNOWN. Nothing is invented.
type InboundFacts struct {
	AccountID         string
	ExtraCLIAccountID string
	PersonID          string
	PersonName        string
	Role              string
	ReachabilityClass string
	RouteType         string
	RouteRelation     string
	ChannelValue      string
	ChannelDisplay    string
	Email             string
	EmailValidated    bool
	Phone             string
	PhonePresent      bool
	GenericMailbox    bool
	NamedHuman        bool
	DNC               bool
	ContractMargin    bool
	WhyNow            string
	Evidence          []string
	Provenance        []string
	Warnings          []string
	Missing           []string
	Status            string
}

// EnrichInboundFacts merges extra-cli imported material onto lead-supplied
// facts. Lead-supplied identity is never replaced by weaker inference.
// Email is never promoted to VALIDATED here.
func EnrichInboundFacts(lead InboundLeadV1, acc *models.OutreachAccount, cands []models.OutreachContactCandidate, ev []models.OutreachEvidence, unavailable bool) InboundFacts {
	f := InboundFacts{
		Status:     models.InboundEnrichmentUnknown,
		Email:      strings.ToLower(strings.TrimSpace(lead.Email)),
		Phone:      strings.TrimSpace(lead.Phone),
		Provenance: []string{"web-cfg"},
	}
	if lead.Name != "" {
		f.PersonName = lead.Name
		f.Provenance = appendUnique(f.Provenance, "lead_supplied_name")
	}
	if f.Email != "" {
		f.Provenance = appendUnique(f.Provenance, "lead_supplied_email")
	}
	if f.Phone != "" {
		f.PhonePresent = len(digitsOnly(f.Phone)) >= 10
		f.Provenance = appendUnique(f.Provenance, "lead_supplied_phone")
	}
	if lead.Consent.DNC {
		f.DNC = true
		f.Provenance = appendUnique(f.Provenance, "lead_supplied_dnc")
	}
	if lead.ContractID != "" {
		f.Evidence = appendUnique(f.Evidence, "contract:"+lead.ContractID)
		f.Provenance = appendUnique(f.Provenance, "web-cfg_contract")
	}

	if unavailable {
		f.Status = models.InboundEnrichmentUnavailable
		f.Missing = append(f.Missing, "extra_cli")
		f.Warnings = append(f.Warnings, "Enriquecimento indisponivel. Lead preservado.")
		fillInboundMissing(&f, lead)
		return f
	}

	if acc != nil {
		f.AccountID = acc.ID.String()
		if acc.SourceLeadID != "" {
			f.ExtraCLIAccountID = acc.SourceLeadID
		}
		f.Provenance = appendUnique(f.Provenance, "extra-cli_account")
		if acc.DoNotContact || acc.Blocked {
			f.DNC = true
			f.Provenance = appendUnique(f.Provenance, "extra-cli_dnc")
		}
		f.WhyNow = firstNonEmpty(acc.MomentSummary, acc.FactToMention)
		if acc.MomentCode != "" {
			f.Evidence = appendUnique(f.Evidence, acc.MomentCode)
		}
		for _, id := range acc.MomentEvidenceIDs {
			f.Evidence = appendUnique(f.Evidence, id)
		}
	}

	for _, e := range ev {
		if e.SourceEvidenceID != "" {
			f.Evidence = appendUnique(f.Evidence, e.SourceEvidenceID)
		}
		blob := strings.ToUpper(e.EvidenceType + " " + e.SourceEvidenceID + " " + e.Title)
		if strings.Contains(blob, "CONTRACT_MARGIN_EVENT") {
			f.ContractMargin = true
			f.Provenance = appendUnique(f.Provenance, "CONTRACT_MARGIN_EVENT")
			f.Evidence = appendUnique(f.Evidence, firstNonEmpty(e.SourceEvidenceID, "CONTRACT_MARGIN_EVENT"))
		}
	}

	best := pickInboundCandidate(lead, cands)
	if best != nil {
		if best.PersonID != "" {
			f.PersonID = strings.TrimSpace(best.PersonID)
			f.Provenance = appendUnique(f.Provenance, "extra-cli_person_id")
		}
		if provenPersonName(best) {
			// Lead-supplied name wins when present; extra-cli fills only a gap.
			if f.PersonName == "" {
				f.PersonName = strings.TrimSpace(best.Name)
				f.Provenance = appendUnique(f.Provenance, "extra-cli_person_name")
			}
			f.NamedHuman = true
		}
		if provenRole(best) {
			f.Role = strings.TrimSpace(best.Role)
		}
		f.ReachabilityClass = MapReachability(best.ReachabilityClass)
		f.RouteType = best.RouteType
		f.RouteRelation = MapRouteRelation(best.RouteRelation)
		f.ChannelDisplay = best.ChannelDisplay
		if extraPhone := strings.TrimSpace(firstNonEmpty(best.PhoneE164, best.Phone, best.ChannelValue)); extraPhone != "" && f.Phone == "" {
			f.Phone = extraPhone
			f.PhonePresent = len(digitsOnly(extraPhone)) >= 10
			f.Provenance = appendUnique(f.Provenance, "extra-cli_phone")
		}
		if extraEmail := strings.ToLower(strings.TrimSpace(best.Email)); extraEmail != "" {
			if f.Email == "" {
				f.Email = extraEmail
				f.Provenance = appendUnique(f.Provenance, "extra-cli_email")
			}
			// Same address published as send-ready may be treated as validated.
			// Inferred or generic mailboxes never become VALIDATED.
			if extraEmail == f.Email && inboundEmailPublishedValidated(best) {
				f.EmailValidated = true
				f.Provenance = appendUnique(f.Provenance, "extra-cli_email_validated")
			}
		}
		if isGenericCorporateMailbox(best) || isGenericRecipient(best) || isRoleMailbox(best) {
			f.GenericMailbox = true
			f.NamedHuman = false
			if !provenPersonName(best) {
				// Do not treat a generic mailbox as a named human.
				if f.PersonName != "" && !leadSuppliedName(lead, f.PersonName) {
					f.PersonName = ""
				}
			}
			f.Warnings = append(f.Warnings, "Caixa generica ou funcional. Nao tratar como pessoa nomeada.")
		}
		if best.DoNotContact || best.Blocked || best.Bounced {
			f.DNC = true
		}
		if best.ChannelValue != "" {
			f.ChannelValue = best.ChannelValue
		}
	}

	if f.WhyNow == "" && lead.ContractID != "" {
		f.WhyNow = "Lead inbound com contexto de contrato " + lead.ContractID + "."
	}
	if f.WhyNow == "" && strings.TrimSpace(lead.Message) != "" {
		f.WhyNow = "Lead inbound: " + truncateRunes(lead.Message, 180)
	}
	if f.WhyNow == "" && lead.AssetID != "" {
		f.WhyNow = "Lead inbound no asset " + lead.AssetID + "."
	}

	fillInboundMissing(&f, lead)
	if len(f.Missing) == 0 && (acc != nil || len(cands) > 0) {
		f.Status = models.InboundEnrichmentCompleted
	} else if acc == nil && len(cands) == 0 {
		f.Status = models.InboundEnrichmentUnknown
	} else {
		f.Status = models.InboundEnrichmentCompleted
	}
	return f
}

func fillInboundMissing(f *InboundFacts, lead InboundLeadV1) {
	if f.AccountID == "" && lead.CNPJ == "" && lead.EntityID == "" && lead.CompanyName == "" {
		f.Missing = appendUnique(f.Missing, "account")
	}
	if !f.NamedHuman && f.PersonID == "" && lead.Name == "" {
		f.Missing = appendUnique(f.Missing, "person")
	}
	if f.Email == "" && !f.PhonePresent {
		f.Missing = appendUnique(f.Missing, "channel")
	}
}

func pickInboundCandidate(lead InboundLeadV1, cands []models.OutreachContactCandidate) *models.OutreachContactCandidate {
	if len(cands) == 0 {
		return nil
	}
	wantEmail := strings.ToLower(strings.TrimSpace(lead.Email))
	wantPhone := digitsOnly(lead.Phone)
	var fallback *models.OutreachContactCandidate
	for i := range cands {
		c := &cands[i]
		if wantEmail != "" && strings.ToLower(strings.TrimSpace(c.Email)) == wantEmail {
			return c
		}
		if wantPhone != "" && digitsOnly(firstNonEmpty(c.PhoneE164, c.Phone)) == wantPhone {
			return c
		}
		if fallback == nil && (c.Recommended || provenPersonName(c)) {
			fallback = c
		}
	}
	if fallback != nil {
		return fallback
	}
	return &cands[0]
}

func inboundEmailPublishedValidated(c *models.OutreachContactCandidate) bool {
	if c == nil {
		return false
	}
	if isGenericCorporateMailbox(c) || isGenericRecipient(c) || isRoleMailbox(c) {
		return false
	}
	if !c.EmailSendReady {
		return false
	}
	switch strings.ToUpper(strings.TrimSpace(c.VerificationStatus)) {
	case models.OutreachVerifyOfficialSource, models.OutreachVerifyVerified, models.OutreachVerifyHumanConfirmed:
		return true
	default:
		return false
	}
}

func leadSuppliedName(lead InboundLeadV1, name string) bool {
	return strings.EqualFold(strings.TrimSpace(lead.Name), strings.TrimSpace(name)) && strings.TrimSpace(lead.Name) != ""
}
