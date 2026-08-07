package confenge

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/warmbly/warmbly/internal/models"
)

// Native feed contract: confenge.outreach.v1

// Feed is the top-level document produced by extra-cli (or fixtures).
type Feed struct {
	SchemaVersion string         `json:"schema_version"`
	GeneratedAt   string         `json:"generated_at"`
	Source        FeedSource     `json:"source"`
	Pagination    FeedPagination `json:"pagination"`
	Leads         []FeedLead     `json:"leads"`
}

// FeedSource identifies the producing intelligence-plane run.
type FeedSource struct {
	System         string `json:"system"`
	RunID          string `json:"run_id"`
	SnapshotHash   string `json:"snapshot_hash"`
	RepoSHA        string `json:"repo_sha"`
	ProfileID      string `json:"profile_id"`
	ProfileVersion string `json:"profile_version"`
}

// FeedPagination supports paged feeds; all fields optional.
type FeedPagination struct {
	Cursor     *string `json:"cursor"`
	NextCursor *string `json:"next_cursor"`
	HasMore    bool    `json:"has_more"`
}

// FeedLead is one company opportunity in the feed.
type FeedLead struct {
	SourceLeadID     string            `json:"source_lead_id"`
	Company          FeedCompany       `json:"company"`
	Priority         FeedPriority      `json:"priority"`
	Moment           FeedMoment        `json:"moment"`
	Offer            FeedOffer         `json:"offer"`
	MessagingContext FeedMessaging     `json:"messaging_context"`
	Contacts         []FeedContact     `json:"contacts"`
	Contracts        []json.RawMessage `json:"contracts"`
	Evidence         []FeedEvidence    `json:"evidence"`
	CommercialState  string            `json:"commercial_state"`
}

// FeedCompany is Brazilian company identity from the datalake.
type FeedCompany struct {
	CNPJ14       string `json:"cnpj14"`
	CNPJRoot     string `json:"cnpj_root"`
	RazaoSocial  string `json:"razao_social"`
	NomeFantasia string `json:"nome_fantasia"`
	Municipio    string `json:"municipio"`
	UF           string `json:"uf"`
	Website      string `json:"website"`
}

// FeedPriority is rank/score from extra-cli (stored, never re-scored here).
type FeedPriority struct {
	Rank       int     `json:"rank"`
	Score      float64 `json:"score"`
	Tier       string  `json:"tier"`
	Confidence string  `json:"confidence"`
}

// FeedMoment is the commercial moment / fato gerador.
type FeedMoment struct {
	Code        string   `json:"code"`
	Summary     string   `json:"summary"`
	ObservedAt  string   `json:"observed_at"`
	Confidence  string   `json:"confidence"`
	EvidenceIDs []string `json:"evidence_ids"`
}

// FeedOffer is the suggested service entry offer.
type FeedOffer struct {
	ServiceCode string `json:"service_code"`
	ServiceName string `json:"service_name"`
	EntryOffer  string `json:"entry_offer"`
	Rationale   string `json:"rationale"`
}

// FeedMessaging is structured messaging context for generation.
type FeedMessaging struct {
	FactToMention string   `json:"fact_to_mention"`
	QuestionToAsk string   `json:"question_to_ask"`
	CTA           string   `json:"cta"`
	ClaimsToAvoid []string `json:"claims_to_avoid"`
}

// FeedContact is a candidate recipient (may lack email).
// Phone remains a legacy string; PhoneObj + WhatsApp are optional additive
// fields for structured provenance (backward compatible with older feeds).
type FeedContact struct {
	SourceContactID    string        `json:"source_contact_id"`
	Name               string        `json:"name"`
	Role               string        `json:"role"`
	Email              string        `json:"email"`
	Phone              string        `json:"phone"`
	PhoneObj           *FeedPhone    `json:"phone_detail,omitempty"`
	WhatsApp           *FeedWhatsApp `json:"whatsapp,omitempty"`
	LinkedInURL        string        `json:"linkedin_url"`
	SourceURL          string        `json:"source_url"`
	SourceDocument     string        `json:"source_document"`
	SourceDate         string        `json:"source_date"`
	VerificationStatus string        `json:"verification_status"`
	Confidence         string        `json:"confidence"`
	Recommended        bool          `json:"recommended"`
}

// FeedEvidence is one evidence item (text only; HTML stripped on import).
type FeedEvidence struct {
	ID             string `json:"id"`
	Type           string `json:"type"`
	Title          string `json:"title"`
	URL            string `json:"url"`
	Document       string `json:"document"`
	Date           string `json:"date"`
	Location       string `json:"location"`
	Excerpt        string `json:"excerpt"`
	Synthesis      string `json:"synthesis"`
	EpistemicClass string `json:"epistemic_class"`
	Reliability    string `json:"reliability"`
	ConsultedAt    string `json:"consulted_at"`
}

var (
	cnpj14Re   = regexp.MustCompile(`^\d{14}$`)
	cnpjRootRe = regexp.MustCompile(`^\d{8}$`)
)

var allowedVerification = map[string]bool{
	models.OutreachVerifyOfficialSource:       true,
	models.OutreachVerifyPublicDocumentRecent: true,
	models.OutreachVerifyMultipleSources:      true,
	models.OutreachVerifyInstitutionalGeneric: true,
	models.OutreachVerifyPublicPossiblyStale:  true,
	models.OutreachVerifyCandidateUnverified:  true,
	models.OutreachVerifyNotFound:             true,
	models.OutreachVerifyInvalid:              true,
	models.OutreachVerifyBounced:              true,
	models.OutreachVerifyDoNotContact:         true,
}

var allowedEpistemic = map[string]bool{
	models.OutreachEpistemicConfirmedFact:          true,
	models.OutreachEpistemicStrongInference:        true,
	models.OutreachEpistemicWeakInference:          true,
	models.OutreachEpistemicCommercialHypothesis:   true,
	models.OutreachEpistemicNotFound:               true,
	models.OutreachEpistemicRequiresCompanyConfirm: true,
	models.OutreachEpistemicContradictoryEvidence:  true,
}

// ParseFeed unmarshals JSON bytes into a Feed. Does not invent missing fields.
func ParseFeed(raw []byte) (*Feed, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("empty payload")
	}
	var f Feed
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	return &f, nil
}

// ValidateFeed checks schema_version and required identity fields.
// Per-lead errors are returned separately so a run can continue.
func ValidateFeed(f *Feed) error {
	if f == nil {
		return fmt.Errorf("nil feed")
	}
	if f.SchemaVersion != models.OutreachSchemaV1 {
		return fmt.Errorf("unsupported schema_version %q (want %s)", f.SchemaVersion, models.OutreachSchemaV1)
	}
	if strings.TrimSpace(f.Source.System) == "" {
		return fmt.Errorf("source.system is required")
	}
	return nil
}

// LeadValidationError is a non-fatal per-lead problem.
type LeadValidationError struct {
	Index        int
	SourceLeadID string
	CNPJ14       string
	Message      string
}

// ValidateLead checks one lead. Missing contact is allowed (NEEDS_CONTACT).
func ValidateLead(i int, lead FeedLead) *LeadValidationError {
	cnpj := digitsOnly(lead.Company.CNPJ14)
	sid := strings.TrimSpace(lead.SourceLeadID)
	if cnpj == "" {
		return &LeadValidationError{Index: i, SourceLeadID: sid, Message: "company.cnpj14 is required"}
	}
	if !cnpj14Re.MatchString(cnpj) {
		return &LeadValidationError{Index: i, SourceLeadID: sid, CNPJ14: cnpj, Message: "company.cnpj14 must be 14 digits"}
	}
	if root := digitsOnly(lead.Company.CNPJRoot); root != "" && !cnpjRootRe.MatchString(root) {
		return &LeadValidationError{Index: i, SourceLeadID: sid, CNPJ14: cnpj, Message: "company.cnpj_root must be 8 digits when set"}
	}
	if strings.TrimSpace(lead.Company.RazaoSocial) == "" && strings.TrimSpace(lead.Company.NomeFantasia) == "" {
		return &LeadValidationError{Index: i, SourceLeadID: sid, CNPJ14: cnpj, Message: "company needs razao_social or nome_fantasia"}
	}
	for j, c := range lead.Contacts {
		vs := strings.TrimSpace(c.VerificationStatus)
		if vs != "" && !allowedVerification[vs] {
			return &LeadValidationError{
				Index: i, SourceLeadID: sid, CNPJ14: cnpj,
				Message: fmt.Sprintf("contacts[%d].verification_status %q is not allowed", j, vs),
			}
		}
	}
	for j, e := range lead.Evidence {
		ec := strings.TrimSpace(e.EpistemicClass)
		if ec != "" && !allowedEpistemic[ec] {
			return &LeadValidationError{
				Index: i, SourceLeadID: sid, CNPJ14: cnpj,
				Message: fmt.Sprintf("evidence[%d].epistemic_class %q is not allowed", j, ec),
			}
		}
		if strings.TrimSpace(e.ID) == "" {
			return &LeadValidationError{
				Index: i, SourceLeadID: sid, CNPJ14: cnpj,
				Message: fmt.Sprintf("evidence[%d].id is required", j),
			}
		}
	}
	return nil
}

// CanonicalPayloadHash is a stable SHA-256 of the raw payload bytes (hex).
// Callers that re-serialize should use the original bytes for idempotency.
func CanonicalPayloadHash(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// LeadContentHash hashes the lead's machine-owned fields for change detection.
func LeadContentHash(lead FeedLead) string {
	// Exclude human-only outcomes; include messaging, priority, moment, offer, contacts, evidence ids.
	type slim struct {
		SourceLeadID string         `json:"source_lead_id"`
		Company      FeedCompany    `json:"company"`
		Priority     FeedPriority   `json:"priority"`
		Moment       FeedMoment     `json:"moment"`
		Offer        FeedOffer      `json:"offer"`
		Messaging    FeedMessaging  `json:"messaging_context"`
		Contacts     []FeedContact  `json:"contacts"`
		Evidence     []FeedEvidence `json:"evidence"`
		State        string         `json:"commercial_state"`
	}
	b, _ := json.Marshal(slim{
		SourceLeadID: lead.SourceLeadID,
		Company:      lead.Company,
		Priority:     lead.Priority,
		Moment:       lead.Moment,
		Offer:        lead.Offer,
		Messaging:    lead.MessagingContext,
		Contacts:     lead.Contacts,
		Evidence:     lead.Evidence,
		State:        lead.CommercialState,
	})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// NormalizeCNPJ14 returns digits-only 14-char CNPJ or empty.
func NormalizeCNPJ14(s string) string {
	d := digitsOnly(s)
	if !cnpj14Re.MatchString(d) {
		return ""
	}
	return d
}

// SanitizeText strips control chars and truncates; never invents content.
func SanitizeText(s string, maxRunes int) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	// Drop HTML tags roughly (no script execution surface).
	s = stripTags(s)
	s = strings.Map(func(r rune) rune {
		if r < 32 && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, s)
	if maxRunes > 0 && utf8.RuneCountInString(s) > maxRunes {
		runes := []rune(s)
		s = string(runes[:maxRunes])
	}
	return s
}

func stripTags(s string) string {
	// Simple angle-bracket strip; feed must not carry executable HTML.
	var b strings.Builder
	b.Grow(len(s))
	in := false
	for _, r := range s {
		switch {
		case r == '<':
			in = true
		case r == '>':
			in = false
		case !in:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func digitsOnly(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ParseDate accepts YYYY-MM-DD or RFC3339 date portion; empty -> nil.
func ParseDate(s string) *time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return &t
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		d := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
		return &d
	}
	// Date-only prefix of datetime
	if len(s) >= 10 {
		if t, err := time.Parse("2006-01-02", s[:10]); err == nil {
			return &t
		}
	}
	return nil
}

// DefaultQueueState for a lead after import (no invention of contacts).
func DefaultQueueState(lead FeedLead, existing *models.OutreachAccount) string {
	// Preserve human terminal states.
	if existing != nil {
		if existing.DoNotContact || existing.QueueState == models.OutreachQueueDoNotContact {
			return models.OutreachQueueDoNotContact
		}
		if existing.Blocked || existing.QueueState == models.OutreachQueueBlocked {
			return models.OutreachQueueBlocked
		}
		// Do not silently restart post-send states.
		switch existing.QueueState {
		case models.OutreachQueueEnrolled, models.OutreachQueueSent, models.OutreachQueueReplied,
			models.OutreachQueueMeeting, models.OutreachQueueProposal, models.OutreachQueueWon,
			models.OutreachQueueLost, models.OutreachQueueSkipped, models.OutreachQueueBounced,
			models.OutreachQueueApproved, models.OutreachQueueNeedsReview:
			return existing.QueueState
		}
	}
	if hasEnrollableContact(lead) {
		return models.OutreachQueueReadyToGenerate
	}
	return models.OutreachQueueNeedsContact
}

func hasEnrollableContact(lead FeedLead) bool {
	for _, c := range lead.Contacts {
		email := strings.TrimSpace(c.Email)
		if email == "" {
			continue
		}
		vs := strings.TrimSpace(c.VerificationStatus)
		if vs == "" {
			vs = models.OutreachVerifyCandidateUnverified
		}
		if models.OutreachUnenrollableVerification[vs] {
			continue
		}
		if vs == models.OutreachVerifyDoNotContact {
			continue
		}
		return true
	}
	return false
}

// NormalizeVerification returns a known status or CANDIDATE_UNVERIFIED when
// email is present without status; NOT_FOUND when empty.
func NormalizeVerification(status, email string) string {
	status = strings.TrimSpace(status)
	email = strings.TrimSpace(email)
	if status != "" && allowedVerification[status] {
		return status
	}
	if email == "" {
		return models.OutreachVerifyNotFound
	}
	return models.OutreachVerifyCandidateUnverified
}

// NormalizeEpistemic defaults missing class to COMMERCIAL_HYPOTHESIS.
func NormalizeEpistemic(class string) string {
	class = strings.TrimSpace(class)
	if class != "" && allowedEpistemic[class] {
		return class
	}
	return models.OutreachEpistemicCommercialHypothesis
}
