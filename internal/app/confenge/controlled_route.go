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
	Source                    string `json:"source,omitempty"`
	SourceType                string `json:"source_type,omitempty"`
	PolicyVersion             string `json:"policy_version,omitempty"`
	SchemaVersion             string `json:"schema_version,omitempty"`
}

// FeedControlledReviewAuthority recognizes the strong, explicit upstream
// contract for an institutional route that may be prepared for human review.
// This is deliberately narrower than CandidateControlledEligible: it is used
// only to recover a strict/named-person provenance failure and never grants
// approval, queueing or transport authority.
func FeedControlledReviewAuthority(fc FeedContact) bool {
	if fc.ControlledEmailEligible == nil || !*fc.ControlledEmailEligible {
		return false
	}
	if fc.DerivedFromFixture == nil || *fc.DerivedFromFixture {
		return false
	}
	class := strings.ToUpper(strings.TrimSpace(fc.RouteClass))
	if !defaultPilotRouteClasses[class] || class == RouteClassProbabilisticOrRisky {
		return false
	}
	if strings.ToUpper(strings.TrimSpace(fc.MailboxCompanyEvidence)) != "OBSERVED" ||
		strings.ToUpper(strings.TrimSpace(fc.ChannelEpistemic)) != "OBSERVED" ||
		strings.ToUpper(strings.TrimSpace(fc.RouteFreshness)) != "FRESH" ||
		strings.ToUpper(strings.TrimSpace(fc.RiskClass)) != "ALLOWED" ||
		strings.ToUpper(strings.TrimSpace(fc.RouteSuppression)) != "NONE" ||
		strings.ToUpper(strings.TrimSpace(fc.OwnershipStatus)) != "COMPANY_OWNED" ||
		strings.ToUpper(strings.TrimSpace(fc.VerificationStatus)) != models.OutreachVerifyOfficialSource {
		return false
	}
	if !validExactEmail(fc.Email) {
		return false
	}
	// Re-run the immutable fixture/demo checks without the strict root-source
	// label. UNKNOWN is accepted only because the stronger controlled contract
	// above explicitly proves a fresh observed company association.
	if tainted, _ := ContactProvenanceTainted(fc.Email, fc.SourceURL, "", fc.VerificationStatus, *fc.DerivedFromFixture); tainted {
		return false
	}
	return true
}

func parseControlledDiscovery(c *models.OutreachContactCandidate) controlledDiscovery {
	var d controlledDiscovery
	if c == nil || len(c.DiscoveryJSON) == 0 {
		return d
	}
	_ = json.Unmarshal(c.DiscoveryJSON, &d)
	return d
}

func candidateRegistrySource(c *models.OutreachContactCandidate) string {
	d := parseControlledDiscovery(c)
	src := strings.ToLower(strings.TrimSpace(d.Source))
	if src == "" {
		src = strings.ToLower(strings.TrimSpace(d.SourceType))
	}
	return src
}

func candidateIsObservedRegistry(c *models.OutreachContactCandidate) bool {
	if c == nil {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(c.VerificationStatus), models.OutreachVerifyOfficialSource) {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(c.ChannelEpistemic), "OBSERVED") {
		return false
	}
	switch candidateRegistrySource(c) {
	case "company_registry", "official_registry", "real_registry":
		return true
	}
	// extra-cli READY company_registry ammo imported before source_type was persisted.
	return strings.TrimSpace(c.SourceURL) == ""
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
	if c.DoNotContact || c.Bounced || c.Blocked || candidateMailboxPurposeSuppressed(c) {
		return false
	}
	d := parseControlledDiscovery(c)
	if d.ControlledEmailEligible == nil {
		class := CandidateRouteClass(c)
		if class == RouteClassDirectPerson {
			return c.EmailSendReady && provenPersonName(c)
		}
		return legacyControlledPublicRoute(c, class)
	}
	if !*d.ControlledEmailEligible {
		return false
	}
	class := CandidateRouteClass(c)
	if class == RouteClassProbabilisticOrRisky || class == "" {
		return false
	}
	return defaultPilotRouteClasses[class]
}

// CandidateDelegatedControlledEligible is the admission contract for the
// delegated first-touch lane. The upstream controlled flag authorizes an
// observed institutional mailbox even when the separate named-person ESR
// projection is false. No other outreach lane inherits this exception.
func CandidateDelegatedControlledEligible(c *models.OutreachContactCandidate) bool {
	if c == nil || c.Blocked || c.DoNotContact || c.Bounced ||
		!c.EnrollableIgnoringVerification() || !validExactEmail(c.Email) {
		return false
	}
	d := parseControlledDiscovery(c)
	if d.ControlledEmailEligible == nil || !*d.ControlledEmailEligible ||
		d.PreferredInitial == nil || !*d.PreferredInitial {
		return false
	}
	switch CandidateRouteClass(c) {
	case RouteClassRoleOrDepartment, RouteClassGenericCompany, RouteClassPublicCompanyFreemail:
	default:
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(c.OwnershipStatus), "COMPANY_OWNED") ||
		!strings.EqualFold(strings.TrimSpace(c.ChannelEpistemic), "OBSERVED") ||
		!strings.EqualFold(strings.TrimSpace(c.RouteFreshness), "FRESH") ||
		!strings.EqualFold(strings.TrimSpace(c.VerificationStatus), models.OutreachVerifyOfficialSource) ||
		!strings.EqualFold(strings.TrimSpace(d.MailboxCompanyEvidence), "OBSERVED") ||
		strings.EqualFold(strings.TrimSpace(c.EmailDerivation), "INFERRED") {
		return false
	}
	suppression := strings.TrimSpace(c.RouteSuppression)
	if suppression != "" && !strings.EqualFold(suppression, "NONE") {
		return false
	}
	return candidateIsObservedRegistry(c) || strings.TrimSpace(c.SourceURL) != ""
}

// legacyControlledPublicRoute bridges pre-contract institutional candidates
// whose imported fields already prove a public company-owned route.
func legacyControlledPublicRoute(c *models.OutreachContactCandidate, class string) bool {
	if c == nil || !c.EmailSendReady || c.MailboxPurposeSendBlocked || !c.EnrollableIgnoringVerification() {
		return false
	}
	if !validExactEmail(c.Email) {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(c.OwnershipStatus), "COMPANY_OWNED") {
		return false
	}
	if isFreemailAddress(c.Email) || (class != RouteClassRoleOrDepartment && class != RouteClassGenericCompany) {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(c.EmailDerivation), "INFERRED") {
		return false
	}
	if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(c.RecipientCommercialSuitability)), "UNSUITABLE") {
		return false
	}
	if suppression := strings.ToUpper(strings.TrimSpace(c.RouteSuppression)); suppression != "" && suppression != "NONE" {
		return false
	}
	switch strings.ToUpper(strings.TrimSpace(c.VerificationStatus)) {
	case models.OutreachVerifyOfficialSource,
		models.OutreachVerifyPublicDocumentRecent,
		models.OutreachVerifyMultipleSources,
		models.OutreachVerifyInstitutionalGeneric,
		models.OutreachVerifyHumanConfirmed,
		models.OutreachVerifyVerified:
		return true
	default:
		return false
	}
}

// CandidateEnrollable is the controlled-route enrollability rule: a classified
// institutional route is contactable without nominal-person verification, while
// suppression, provenance and fixture guards still apply. Legacy nominal paths
// keep using CanEnroll directly.
func CandidateEnrollable(c *models.OutreachContactCandidate) bool {
	if candidateMailboxPurposeSuppressed(c) {
		return false
	}
	if c.CanEnroll() {
		return true
	}
	if !CandidateControlledEligible(c) || !ControlledRouteAllowed(c, nil) {
		return false
	}
	return c.EnrollableIgnoringVerification()
}

func candidateMailboxPurposeSuppressed(c *models.OutreachContactCandidate) bool {
	return c == nil || c.MailboxPurposeSendBlocked ||
		strings.EqualFold(strings.TrimSpace(c.RecipientCommercialSuitability), "UNSUITABLE_MAILBOX")
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
	if src := strings.TrimSpace(fc.Source); src != "" {
		d.Source = src
	}
	if st := strings.TrimSpace(fc.SourceType); st != "" {
		d.SourceType = st
	}
	if d.Source == "" && d.SourceType != "" {
		d.Source = d.SourceType
	}
	if d.SourceType == "" && d.Source != "" {
		d.SourceType = d.Source
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
	// Freeze-time QA must reject what the send-time hard QA rejects, otherwise a
	// cohort freezes clean and then fails for every member at dispatch.
	if looksLikeMetadataDump(body) || containsDumpLabel(body) {
		add("metadata_dump")
	}
	if containsDumpLabel(subject) {
		add("subject_dump_label")
	}
	if looksMidTokenTruncation(subject) || looksMidTokenTruncation(body) {
		add("mid_token_truncation")
	}
	// Cohort v1 froze five members with admission_reasons saying
	// "copy_qa=passed" while two messages carried ",." and one repeated a
	// three-word run. The composer now prevents all three, and these gates make
	// a regression refuse admission instead of freezing quietly.
	for _, code := range copyProjectionDefects(subject, body) {
		add(code)
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
