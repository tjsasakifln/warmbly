package confenge

import (
	"encoding/json"
	"strings"

	"github.com/warmbly/warmbly/internal/models"
)

// Email route classes published by extra-cli. Warmbly never invents a class.
const (
	RouteClassDirectPerson          = "DIRECT_PERSON"
	RouteClassRoleOrDepartment      = "ROLE_OR_DEPARTMENT"
	RouteClassGenericCompany        = "GENERIC_COMPANY"
	RouteClassPublicCompanyFreemail = "PUBLIC_COMPANY_FREEMAIL"
	RouteClassProbabilisticOrRisky  = "PROBABILISTIC_OR_RISKY"
)

const ControlledEmailPolicyV1 = "controlled-email-policy.v1"

var defaultPilotRouteClasses = map[string]bool{
	RouteClassDirectPerson:          true,
	RouteClassRoleOrDepartment:      true,
	RouteClassGenericCompany:        true,
	RouteClassPublicCompanyFreemail: true,
}

type controlledDiscovery struct {
	RouteClass                string `json:"route_class,omitempty"`
	ControlledEmailEligible   *bool  `json:"controlled_email_eligible,omitempty"`
	PreferredInitial          *bool  `json:"preferred_initial,omitempty"`
	MailboxCompanyEvidence    string `json:"mailbox_company_evidence,omitempty"`
	MailboxPersonEvidence     string `json:"mailbox_person_evidence,omitempty"`
	MailboxDepartmentEvidence string `json:"mailbox_department_evidence,omitempty"`
	PersonUnknown             *bool  `json:"person_unknown,omitempty"`
	EmailValidated            *bool  `json:"email_validated,omitempty"`
	RiskClass                 string `json:"risk_class,omitempty"`
	PolicyVersion             string `json:"policy_version,omitempty"`
	SchemaVersion             string `json:"schema_version,omitempty"`
}

func parseControlledDiscovery(c *models.OutreachContactCandidate) controlledDiscovery {
	var d controlledDiscovery
	if c == nil || len(c.DiscoveryJSON) == 0 {
		return d
	}
	_ = json.Unmarshal(c.DiscoveryJSON, &d)
	return d
}

func CandidateRouteClass(c *models.OutreachContactCandidate) string {
	d := parseControlledDiscovery(c)
	if class := strings.ToUpper(strings.TrimSpace(d.RouteClass)); class != "" {
		return class
	}
	if c == nil {
		return ""
	}
	if isRoleMailbox(c) {
		return RouteClassRoleOrDepartment
	}
	email := canonicalPilotEmail(c.Email)
	if isFreemailAddress(email) {
		if strings.EqualFold(d.MailboxCompanyEvidence, "OBSERVED") {
			return RouteClassPublicCompanyFreemail
		}
		return RouteClassProbabilisticOrRisky
	}
	if isGenericRecipient(c) {
		return RouteClassGenericCompany
	}
	if provenPersonName(c) {
		return RouteClassDirectPerson
	}
	return ""
}

func CandidateControlledEligible(c *models.OutreachContactCandidate) bool {
	if c == nil {
		return false
	}
	if c.DoNotContact || c.Bounced || c.Blocked {
		return false
	}
	d := parseControlledDiscovery(c)
	if d.ControlledEmailEligible == nil || !*d.ControlledEmailEligible {
		class := CandidateRouteClass(c)
		if class == RouteClassDirectPerson {
			return c.EmailSendReady && provenPersonName(c)
		}
		return false
	}
	class := CandidateRouteClass(c)
	if class == RouteClassProbabilisticOrRisky || class == "" {
		return false
	}
	return defaultPilotRouteClasses[class]
}

func CandidatePreferredInitial(c *models.OutreachContactCandidate) bool {
	d := parseControlledDiscovery(c)
	return d.PreferredInitial != nil && *d.PreferredInitial
}

func CandidatePersonUnknown(c *models.OutreachContactCandidate) bool {
	class := CandidateRouteClass(c)
	if class == RouteClassDirectPerson {
		return false
	}
	d := parseControlledDiscovery(c)
	if d.PersonUnknown != nil {
		return *d.PersonUnknown
	}
	return !provenPersonName(c)
}

func CandidateEmailValidated(c *models.OutreachContactCandidate) bool {
	d := parseControlledDiscovery(c)
	if d.EmailValidated != nil {
		return *d.EmailValidated
	}
	return CandidateRouteClass(c) == RouteClassDirectPerson && c != nil && c.EmailSendReady
}

func ControlledRouteAllowed(c *models.OutreachContactCandidate, allowed map[string]bool) bool {
	if allowed == nil {
		allowed = defaultPilotRouteClasses
	}
	class := CandidateRouteClass(c)
	if class == "" || class == RouteClassProbabilisticOrRisky {
		return false
	}
	return allowed[class]
}

func isFreemailAddress(email string) bool {
	parts := strings.Split(canonicalPilotEmail(email), "@")
	if len(parts) != 2 {
		return false
	}
	switch parts[1] {
	case "gmail.com", "hotmail.com", "yahoo.com", "yahoo.com.br", "outlook.com", "live.com", "icloud.com", "uol.com.br", "bol.com.br", "terra.com.br", "msn.com", "protonmail.com", "aol.com":
		return true
	}
	return false
}

func mergeControlledDiscovery(existing []byte, fc FeedContact) []byte {
	d := controlledDiscovery{}
	if len(existing) > 0 {
		_ = json.Unmarshal(existing, &d)
	}
	if fc.RouteClass != "" {
		d.RouteClass = fc.RouteClass
	}
	if fc.ControlledEmailEligible != nil {
		d.ControlledEmailEligible = fc.ControlledEmailEligible
	}
	if fc.PreferredInitial != nil {
		d.PreferredInitial = fc.PreferredInitial
	}
	if fc.MailboxCompanyEvidence != "" {
		d.MailboxCompanyEvidence = fc.MailboxCompanyEvidence
	}
	if fc.MailboxPersonEvidence != "" {
		d.MailboxPersonEvidence = fc.MailboxPersonEvidence
	}
	if fc.MailboxDepartmentEvidence != "" {
		d.MailboxDepartmentEvidence = fc.MailboxDepartmentEvidence
	}
	if fc.PersonUnknown != nil {
		d.PersonUnknown = fc.PersonUnknown
	}
	if fc.EmailValidated != nil {
		d.EmailValidated = fc.EmailValidated
	}
	if fc.RiskClass != "" {
		d.RiskClass = fc.RiskClass
	}
	if d.RouteClass == "" && d.ControlledEmailEligible == nil {
		return existing
	}
	out, err := json.Marshal(d)
	if err != nil {
		return existing
	}
	return out
}

// ValidateCopyForRouteClass rejects invented identity and class-incompatible copy.
func ValidateCopyForRouteClass(class, body, subject string, cand *models.OutreachContactCandidate) []string {
	var errs []string
	blob := strings.ToLower(subject + "\n" + body)
	add := func(code string) { errs = append(errs, code) }
	if strings.Contains(blob, "decision_unit") || strings.Contains(blob, "email_discovery_class") || strings.Contains(blob, "reachability_class") {
		add("internal_dump")
	}
	switch class {
	case RouteClassDirectPerson:
		if cand != nil && provenPersonName(cand) {
			first := strings.ToLower(strings.TrimSpace(strings.Split(cand.Name, " ")[0]))
			if first != "" && !strings.Contains(blob, first) {
				// named greeting is allowed but not mandatory; no error
			}
		}
	case RouteClassRoleOrDepartment, RouteClassGenericCompany, RouteClassPublicCompanyFreemail:
		if cand != nil && strings.TrimSpace(cand.Name) != "" && provenPersonName(cand) == false {
			name := strings.ToLower(strings.TrimSpace(cand.Name))
			if name != "" && name != "equipe" && strings.Contains(blob, name) {
				add("invented_name")
			}
		}
		if CandidatePersonUnknown(cand) && looksInventedPersonGreeting(blob) {
			add("invented_name")
		}
		if strings.Contains(blob, "diretor comprovado") || strings.Contains(blob, "você é o gerente") {
			add("invented_role")
		}
	}
	if class == RouteClassPublicCompanyFreemail {
		if strings.Contains(blob, "domínio corporativo") || strings.Contains(blob, "dominio corporativo") || strings.Contains(blob, "corporate domain") {
			add("gmail_is_not_corporate_domain")
		}
	}
	if class == RouteClassProbabilisticOrRisky {
		add("risky_outside_default_pilot")
	}
	if strings.Contains(blob, "certeza de identidade") || strings.Contains(blob, "identidade validada como pessoa") {
		if class != RouteClassDirectPerson {
			add("false_identity_certainty")
		}
	}
	return errs
}

func candEmail(c *models.OutreachContactCandidate) string {
	if c == nil {
		return ""
	}
	return c.Email
}
