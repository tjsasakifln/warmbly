package confenge

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/models"
)

const (
	FrozenCohortSchemaV1     = "confenge.frozen_cohort.v1"
	DefaultCohortDailyVolume = 50
	DefaultCohortTTL         = 24 * time.Hour
	DefaultCohortLimit       = 50
	MaxCohortLimit           = 100
	previewSamplePerClass    = 2
)

// CohortAccountInput is one ingested account plus its extra-cli candidates.
// Prepare never invents contacts; it only selects among published ones.
type CohortAccountInput struct {
	Account    models.OutreachAccount
	Candidates []models.OutreachContactCandidate
	Source     string
}

// FrozenCohortMember is the exact authorization composition for one account.
// One member = one initial route. Membership is frozen at preview time.
type FrozenCohortMember struct {
	AccountID        uuid.UUID `json:"account_id,omitempty"`
	AccountRef       string    `json:"account_ref"`
	CandidateID      uuid.UUID `json:"candidate_id,omitempty"`
	CandidateRef     string    `json:"candidate_ref"`
	Mailbox          string    `json:"mailbox"`
	RouteClass       string    `json:"route_class"`
	Source           string    `json:"source,omitempty"`
	ContentHash      string    `json:"content_hash"`
	EvidenceHash     string    `json:"evidence_hash"`
	ComposerVersion  string    `json:"composer_version"`
	Subject          string    `json:"subject"`
	BodyText         string    `json:"body_text"`
	Greeting         string    `json:"greeting"`
	PersonUnknown    bool      `json:"person_unknown"`
	PreferredInitial bool      `json:"preferred_initial"`
	TouchpointID     uuid.UUID `json:"touchpoint_id,omitempty"`
}

// CohortExclusion is one account left out of the frozen set.
type CohortExclusion struct {
	AccountRef string `json:"account_ref"`
	ReasonCode string `json:"reason_code"`
	Mailbox    string `json:"mailbox,omitempty"`
	RouteClass string `json:"route_class,omitempty"`
}

// CohortClassSample is a founder-reviewable excerpt. Aggregated logs should
// use RedactedMailbox, not the full address.
type CohortClassSample struct {
	AccountRef      string `json:"account_ref"`
	RouteClass      string `json:"route_class"`
	RedactedMailbox string `json:"redacted_mailbox"`
	Greeting        string `json:"greeting"`
	PersonUnknown   bool   `json:"person_unknown"`
}

// CohortPreviewReport is the deterministic operator preview. Totals reconcile.
type CohortPreviewReport struct {
	AccountsConsidered int                            `json:"accounts_considered"`
	AccountsEligible   int                            `json:"accounts_eligible"`
	AccountsExcluded   int                            `json:"accounts_excluded"`
	RecipientsFinal    int                            `json:"recipients_final"`
	ByRouteClass       map[string]int                 `json:"by_route_class"`
	BySource           map[string]int                 `json:"by_source"`
	ByExclusionReason  map[string]int                 `json:"by_exclusion_reason"`
	Suppressed         int                            `json:"suppressed"`
	OptOut             int                            `json:"opt_out"`
	HardBounce         int                            `json:"hard_bounce"`
	RiskyExcluded      int                            `json:"risky_excluded"`
	Duplicates         int                            `json:"duplicates"`
	MissingProvenance  int                            `json:"missing_provenance"`
	Stale              int                            `json:"stale"`
	CopyQAFailures     int                            `json:"copy_qa_failures"`
	SamplesByClass     map[string][]CohortClassSample `json:"samples_by_class"`
	Reconciled         bool                           `json:"reconciled"`
	ReconcileError     string                         `json:"reconcile_error,omitempty"`
}

// FrozenCohortSnapshot is the immutable prepare artifact. Hashes are derived
// here so the founder never copies them by hand.
type FrozenCohortSnapshot struct {
	SchemaVersion       string               `json:"schema_version"`
	CohortID            string               `json:"cohort_id"`
	CohortHash          string               `json:"cohort_hash"`
	RecipientSetHash    string               `json:"recipient_set_hash"`
	RepositorySHA       string               `json:"repository_sha"`
	FeedSchemaVersion   string               `json:"feed_schema_version"`
	FeedIdentity        string               `json:"feed_identity,omitempty"`
	SnapshotHash        string               `json:"snapshot_hash,omitempty"`
	PolicyVersion       string               `json:"policy_version"`
	ComposerVersion     string               `json:"composer_version"`
	EvidenceVersion     string               `json:"evidence_version"`
	AllowedRouteClasses []string             `json:"allowed_route_classes"`
	MaxDailyVolume      int                  `json:"max_daily_volume"`
	TTLSeconds          int64                `json:"ttl_seconds"`
	FrozenAt            time.Time            `json:"frozen_at"`
	Members             []FrozenCohortMember `json:"members"`
	Exclusions          []CohortExclusion    `json:"exclusions"`
	Preview             CohortPreviewReport  `json:"preview"`
	AuthorizationID     uuid.UUID            `json:"authorization_id,omitempty"`
	RealEmailSent       bool                 `json:"real_email_sent"`
	AutoSendEnabled     bool                 `json:"auto_send_enabled"`
	GreenAutorunEnabled bool                 `json:"green_autorun_enabled"`
	Warnings            []string             `json:"warnings,omitempty"`
}

// CohortPrepareOptions binds live runtime identity. Empty SHA is a warning
// at preview and a hard block at authorize.
type CohortPrepareOptions struct {
	Now                 time.Time
	Limit               int
	MaxDailyVolume      int
	TTL                 time.Duration
	RepositorySHA       string
	FeedSchemaVersion   string
	FeedIdentity        string
	SnapshotHash        string
	PolicyVersion       string
	ComposerVersion     string
	EvidenceVersion     string
	AllowedRouteClasses []string
	Source              string
}

// PrepareControlledCohort selects at most one initial route per account,
// composes class-honest copy, and freezes hashes. It does not persist a grant.
func PrepareControlledCohort(accounts []CohortAccountInput, opts CohortPrepareOptions) (*FrozenCohortSnapshot, error) {
	if opts.Now.IsZero() {
		opts.Now = time.Now().UTC()
	}
	if opts.Limit <= 0 {
		opts.Limit = DefaultCohortLimit
	}
	if opts.Limit > MaxCohortLimit {
		opts.Limit = MaxCohortLimit
	}
	if opts.MaxDailyVolume <= 0 {
		opts.MaxDailyVolume = DefaultCohortDailyVolume
	}
	if opts.TTL <= 0 {
		opts.TTL = DefaultCohortTTL
	}
	if opts.PolicyVersion == "" {
		opts.PolicyVersion = BoundedCohortPolicyV1
	}
	if opts.ComposerVersion == "" {
		opts.ComposerVersion = ComposerVersion
	}
	if opts.EvidenceVersion == "" {
		opts.EvidenceVersion = DefaultEvidenceVersion
	}
	if opts.FeedSchemaVersion == "" {
		opts.FeedSchemaVersion = models.OutreachSchemaV1
	}
	if len(opts.AllowedRouteClasses) == 0 {
		opts.AllowedRouteClasses = []string{
			RouteClassDirectPerson, RouteClassRoleOrDepartment,
			RouteClassGenericCompany, RouteClassPublicCompanyFreemail,
		}
	}
	for _, c := range opts.AllowedRouteClasses {
		if strings.EqualFold(strings.TrimSpace(c), RouteClassProbabilisticOrRisky) {
			return nil, fmt.Errorf("RISKY is outside the default controlled-email cohort")
		}
	}
	allowed := map[string]bool{}
	for _, c := range opts.AllowedRouteClasses {
		allowed[strings.ToUpper(strings.TrimSpace(c))] = true
	}

	preview := CohortPreviewReport{
		ByRouteClass:      map[string]int{},
		BySource:          map[string]int{},
		ByExclusionReason: map[string]int{},
		SamplesByClass:    map[string][]CohortClassSample{},
	}
	var members []FrozenCohortMember
	var exclusions []CohortExclusion
	seenMailbox := map[string]string{} // mailbox → account_ref
	seenAccount := map[string]bool{}

	exclude := func(ref, code, mailbox, class string) {
		preview.ByExclusionReason[code]++
		switch code {
		case "recipient_suppressed", "account_blocked", "account_suppressed":
			preview.Suppressed++
		case "recipient_opt_out", "account_dnc":
			preview.OptOut++
		case "recipient_hard_bounce":
			preview.HardBounce++
		case "risky_outside_default_pilot":
			preview.RiskyExcluded++
		case "duplicate_mailbox", "duplicate_account":
			preview.Duplicates++
		case "missing_provenance":
			preview.MissingProvenance++
		case "stale_evidence", "recipient_evidence_stale":
			preview.Stale++
		case "copy_qa_failure":
			preview.CopyQAFailures++
		}
		exclusions = append(exclusions, CohortExclusion{
			AccountRef: ref, ReasonCode: code, Mailbox: mailbox, RouteClass: class,
		})
	}

	for i := range accounts {
		in := accounts[i]
		preview.AccountsConsidered++
		ref := accountRef(&in.Account)
		if ref == "" {
			ref = fmt.Sprintf("account-%d", i)
		}
		if seenAccount[ref] {
			exclude(ref, "duplicate_account", "", "")
			continue
		}
		seenAccount[ref] = true

		if in.Account.DoNotContact {
			exclude(ref, "account_dnc", "", "")
			continue
		}
		if in.Account.Blocked {
			exclude(ref, "account_blocked", "", "")
			continue
		}

		cand, reason := SelectInitialRoute(in.Candidates, allowed, opts.Now)
		if cand == nil {
			if reason == "" {
				reason = "no_controlled_eligible_route"
			}
			exclude(ref, reason, "", "")
			continue
		}
		mailbox := canonicalPilotEmail(cand.Email)
		class := CandidateRouteClass(cand)
		if prev, ok := seenMailbox[mailbox]; ok {
			exclude(ref, "duplicate_mailbox", mailbox, class)
			_ = prev
			continue
		}

		subject, body, greeting := ComposeControlledInitial(&in.Account, cand, class)
		if qa := ValidateCopyForRouteClass(class, body, subject, cand); len(qa) > 0 {
			exclude(ref, "copy_qa_failure", mailbox, class)
			continue
		}
		if len(members) >= opts.Limit {
			exclude(ref, "cohort_limit_reached", mailbox, class)
			continue
		}

		seenMailbox[mailbox] = ref
		src := firstNonEmpty(in.Source, opts.Source)
		member := FrozenCohortMember{
			AccountID:        in.Account.ID,
			AccountRef:       ref,
			CandidateID:      cand.ID,
			CandidateRef:     candidateRef(cand),
			Mailbox:          mailbox,
			RouteClass:       class,
			Source:           src,
			ContentHash:      hashControlledContent(mailbox, class, subject, body),
			EvidenceHash:     hashControlledEvidence(cand),
			ComposerVersion:  opts.ComposerVersion,
			Subject:          subject,
			BodyText:         body,
			Greeting:         greeting,
			PersonUnknown:    CandidatePersonUnknown(cand),
			PreferredInitial: CandidatePreferredInitial(cand),
		}
		members = append(members, member)
		preview.ByRouteClass[class]++
		if src == "" {
			src = "UNKNOWN"
		}
		preview.BySource[src]++
		samples := preview.SamplesByClass[class]
		if len(samples) < previewSamplePerClass {
			preview.SamplesByClass[class] = append(samples, CohortClassSample{
				AccountRef:      ref,
				RouteClass:      class,
				RedactedMailbox: RedactMailbox(mailbox),
				Greeting:        greeting,
				PersonUnknown:   member.PersonUnknown,
			})
		}
	}

	preview.AccountsEligible = len(members)
	preview.AccountsExcluded = len(exclusions)
	preview.RecipientsFinal = len(members)
	preview.Reconciled, preview.ReconcileError = reconcileCohortPreview(preview, len(accounts))

	mailboxes := make([]string, 0, len(members))
	for _, m := range members {
		mailboxes = append(mailboxes, m.Mailbox)
	}
	sort.Slice(members, func(i, j int) bool {
		if members[i].AccountRef == members[j].AccountRef {
			return members[i].Mailbox < members[j].Mailbox
		}
		return members[i].AccountRef < members[j].AccountRef
	})

	var warnings []string
	if strings.TrimSpace(opts.RepositorySHA) == "" {
		warnings = append(warnings, "repository_sha missing; authorize will fail closed")
	}

	snap := &FrozenCohortSnapshot{
		SchemaVersion:       FrozenCohortSchemaV1,
		RecipientSetHash:    HashRecipientSet(mailboxes),
		RepositorySHA:       strings.TrimSpace(opts.RepositorySHA),
		FeedSchemaVersion:   opts.FeedSchemaVersion,
		FeedIdentity:        firstNonEmpty(opts.FeedIdentity, opts.SnapshotHash),
		SnapshotHash:        opts.SnapshotHash,
		PolicyVersion:       opts.PolicyVersion,
		ComposerVersion:     opts.ComposerVersion,
		EvidenceVersion:     opts.EvidenceVersion,
		AllowedRouteClasses: append([]string{}, opts.AllowedRouteClasses...),
		MaxDailyVolume:      opts.MaxDailyVolume,
		TTLSeconds:          int64(opts.TTL / time.Second),
		FrozenAt:            opts.Now.UTC(),
		Members:             members,
		Exclusions:          exclusions,
		Preview:             preview,
		RealEmailSent:       false,
		AutoSendEnabled:     false,
		GreenAutorunEnabled: false,
		Warnings:            warnings,
	}
	snap.CohortHash = HashFrozenMembership(members)
	snap.CohortID = deriveCohortID(opts.SnapshotHash, opts.FeedIdentity, snap.CohortHash)
	return snap, nil
}

func reconcileCohortPreview(p CohortPreviewReport, considered int) (bool, string) {
	if p.AccountsConsidered != considered {
		return false, fmt.Sprintf("considered %d != input %d", p.AccountsConsidered, considered)
	}
	if p.AccountsEligible+p.AccountsExcluded != p.AccountsConsidered {
		return false, fmt.Sprintf("eligible %d + excluded %d != considered %d", p.AccountsEligible, p.AccountsExcluded, p.AccountsConsidered)
	}
	if p.RecipientsFinal != p.AccountsEligible {
		return false, fmt.Sprintf("recipients %d != eligible %d", p.RecipientsFinal, p.AccountsEligible)
	}
	sumClass := 0
	for _, n := range p.ByRouteClass {
		sumClass += n
	}
	if sumClass != p.RecipientsFinal {
		return false, fmt.Sprintf("by_route_class %d != recipients %d", sumClass, p.RecipientsFinal)
	}
	sumEx := 0
	for _, n := range p.ByExclusionReason {
		sumEx += n
	}
	if sumEx != p.AccountsExcluded {
		return false, fmt.Sprintf("by_exclusion_reason %d != excluded %d", sumEx, p.AccountsExcluded)
	}
	return true, ""
}

// SelectInitialRoute picks at most one controlled-eligible mailbox.
// DIRECT_PERSON wins when proven; otherwise preferred_initial; else class rank.
func SelectInitialRoute(cands []models.OutreachContactCandidate, allowed map[string]bool, now time.Time) (*models.OutreachContactCandidate, string) {
	if allowed == nil {
		allowed = defaultPilotRouteClasses
	}
	var eligible []models.OutreachContactCandidate
	lastReason := "no_controlled_eligible_route"
	for i := range cands {
		c := cands[i]
		if reason := controlledPrepareBlock(&c, allowed, now); reason != "" {
			lastReason = reason
			continue
		}
		eligible = append(eligible, c)
	}
	if len(eligible) == 0 {
		return nil, lastReason
	}
	var best *models.OutreachContactCandidate
	bestScore := -1
	for i := range eligible {
		c := &eligible[i]
		score := routeSelectionScore(c)
		if score > bestScore {
			bestScore = score
			best = c
		}
	}
	return best, ""
}

func routeSelectionScore(c *models.OutreachContactCandidate) int {
	class := CandidateRouteClass(c)
	score := 0
	if CandidatePreferredInitial(c) {
		score += 40
	}
	switch class {
	case RouteClassDirectPerson:
		if provenPersonName(c) {
			score += 120
		} else {
			score += 10
		}
	case RouteClassRoleOrDepartment:
		score += 60
	case RouteClassGenericCompany:
		score += 40
	case RouteClassPublicCompanyFreemail:
		score += 30
	}
	return score
}

func controlledPrepareBlock(c *models.OutreachContactCandidate, allowed map[string]bool, now time.Time) string {
	if blk := classifyControlledRecipient(nil, c, now); blk != nil {
		return blk.Code
	}
	if canonicalPilotEmail(c.Email) == "" {
		return "missing_mailbox"
	}
	if !CandidateControlledEligible(c) {
		return "no_controlled_eligible_route"
	}
	class := CandidateRouteClass(c)
	if class == RouteClassProbabilisticOrRisky {
		return "risky_outside_default_pilot"
	}
	if !ControlledRouteAllowed(c, allowed) {
		return "route_class_outside_policy"
	}
	if class == RouteClassPublicCompanyFreemail {
		d := parseControlledDiscovery(c)
		if !strings.EqualFold(d.MailboxCompanyEvidence, "OBSERVED") {
			return "missing_company_mailbox_evidence"
		}
	}
	if provenanceMissing(c) {
		return "missing_provenance"
	}
	if evidenceStale(c, now) {
		return "stale_evidence"
	}
	return ""
}

func provenanceMissing(c *models.OutreachContactCandidate) bool {
	if c == nil {
		return true
	}
	if strings.TrimSpace(c.RouteSuppression) != "" {
		return true
	}
	if strings.Contains(strings.ToLower(c.BlockReason), "provenance") {
		return true
	}
	return false
}

func evidenceStale(c *models.OutreachContactCandidate, now time.Time) bool {
	if c == nil {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(c.RouteFreshness), "STALE") {
		return true
	}
	if c.SourceDate != nil && !now.IsZero() {
		if now.Sub(c.SourceDate.UTC()) > 18*30*24*time.Hour {
			return true
		}
	}
	return false
}

func HashFrozenMembership(members []FrozenCohortMember) string {
	lines := make([]string, 0, len(members))
	for _, m := range members {
		lines = append(lines, strings.Join([]string{
			strings.TrimSpace(m.AccountRef),
			strings.TrimSpace(m.CandidateRef),
			canonicalPilotEmail(m.Mailbox),
			strings.ToUpper(strings.TrimSpace(m.RouteClass)),
			strings.TrimSpace(m.ContentHash),
			strings.TrimSpace(m.EvidenceHash),
		}, "|"))
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
}

func hashControlledContent(mailbox, class, subject, body string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		canonicalPilotEmail(mailbox), strings.ToUpper(class),
		strings.TrimSpace(subject), strings.TrimSpace(body), ComposerVersion,
	}, "\x00")))
	return hex.EncodeToString(sum[:])
}

func hashControlledEvidence(c *models.OutreachContactCandidate) string {
	if c == nil {
		return ""
	}
	d := parseControlledDiscovery(c)
	date := ""
	if c.SourceDate != nil {
		date = c.SourceDate.UTC().Format("2006-01-02")
	}
	sum := sha256.Sum256([]byte(strings.Join([]string{
		strings.TrimSpace(c.SourceURL), strings.TrimSpace(c.SourceDocument), date,
		CandidateRouteClass(c), d.MailboxCompanyEvidence, d.MailboxPersonEvidence,
		d.MailboxDepartmentEvidence, strings.TrimSpace(c.RouteFreshness),
	}, "\x00")))
	return hex.EncodeToString(sum[:])
}

func deriveCohortID(snapshotHash, feedIdentity, cohortHash string) string {
	seed := firstNonEmpty(snapshotHash, feedIdentity, cohortHash)
	if len(cohortHash) >= 12 {
		return "controlled-" + cohortHash[:12]
	}
	sum := sha256.Sum256([]byte(seed))
	return "controlled-" + hex.EncodeToString(sum[:6])
}

func accountRef(acc *models.OutreachAccount) string {
	if acc == nil {
		return ""
	}
	if s := strings.TrimSpace(acc.SourceLeadID); s != "" {
		return s
	}
	if s := NormalizeCNPJ14(acc.CNPJ14); s != "" {
		return s
	}
	if acc.ID != uuid.Nil {
		return acc.ID.String()
	}
	return ""
}

func candidateRef(c *models.OutreachContactCandidate) string {
	if c == nil {
		return ""
	}
	if s := strings.TrimSpace(c.SourceContactID); s != "" {
		return s
	}
	if c.ID != uuid.Nil {
		return c.ID.String()
	}
	return canonicalPilotEmail(c.Email)
}

// RedactMailbox keeps domain, masks the local part. Safe for aggregated logs.
func RedactMailbox(email string) string {
	parts := strings.Split(canonicalPilotEmail(email), "@")
	if len(parts) != 2 {
		return "(redacted)"
	}
	local := parts[0]
	if local == "" {
		return "*@" + parts[1]
	}
	return string([]rune(local)[0]) + "***@" + parts[1]
}

// FormatCohortPreview is the founder-readable report. JSON is separate.
func FormatCohortPreview(snap *FrozenCohortSnapshot) string {
	if snap == nil {
		return "no cohort snapshot\n"
	}
	p := snap.Preview
	var b strings.Builder
	fmt.Fprintf(&b, "cohort_id=%s\n", snap.CohortID)
	fmt.Fprintf(&b, "cohort_hash=%s\n", snap.CohortHash)
	fmt.Fprintf(&b, "recipient_set_hash=%s\n", snap.RecipientSetHash)
	fmt.Fprintf(&b, "repository_sha=%s\n", snap.RepositorySHA)
	fmt.Fprintf(&b, "feed_schema=%s feed_identity=%s snapshot=%s\n", snap.FeedSchemaVersion, snap.FeedIdentity, snap.SnapshotHash)
	fmt.Fprintf(&b, "policy=%s composer=%s evidence=%s\n", snap.PolicyVersion, snap.ComposerVersion, snap.EvidenceVersion)
	fmt.Fprintf(&b, "max_daily_volume=%d ttl_seconds=%d\n", snap.MaxDailyVolume, snap.TTLSeconds)
	fmt.Fprintf(&b, "allowed_route_classes=%s\n", strings.Join(snap.AllowedRouteClasses, ","))
	fmt.Fprintf(&b, "\naccounts_considered=%d\naccounts_eligible=%d\naccounts_excluded=%d\nrecipients_final=%d\n",
		p.AccountsConsidered, p.AccountsEligible, p.AccountsExcluded, p.RecipientsFinal)
	fmt.Fprintf(&b, "suppressed=%d opt_out=%d hard_bounce=%d risky_excluded=%d duplicates=%d missing_provenance=%d stale=%d copy_qa_failures=%d\n",
		p.Suppressed, p.OptOut, p.HardBounce, p.RiskyExcluded, p.Duplicates, p.MissingProvenance, p.Stale, p.CopyQAFailures)
	fmt.Fprintf(&b, "reconciled=%v\n", p.Reconciled)
	fmt.Fprintf(&b, "real_email_sent=%v auto_send_enabled=%v\n", snap.RealEmailSent, snap.AutoSendEnabled)
	keys := make([]string, 0, len(p.ByRouteClass))
	for k := range p.ByRouteClass {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fmt.Fprintf(&b, "\nby_route_class:\n")
	for _, k := range keys {
		fmt.Fprintf(&b, "  %s=%d\n", k, p.ByRouteClass[k])
	}
	exKeys := make([]string, 0, len(p.ByExclusionReason))
	for k := range p.ByExclusionReason {
		exKeys = append(exKeys, k)
	}
	sort.Strings(exKeys)
	if len(exKeys) > 0 {
		fmt.Fprintf(&b, "\nby_exclusion_reason:\n")
		for _, k := range exKeys {
			fmt.Fprintf(&b, "  %s=%d\n", k, p.ByExclusionReason[k])
		}
	}
	classKeys := make([]string, 0, len(p.SamplesByClass))
	for k := range p.SamplesByClass {
		classKeys = append(classKeys, k)
	}
	sort.Strings(classKeys)
	if len(classKeys) > 0 {
		fmt.Fprintf(&b, "\nsamples (redacted mailbox):\n")
		for _, k := range classKeys {
			for _, s := range p.SamplesByClass[k] {
				fmt.Fprintf(&b, "  %s %s %s greeting=%q person_unknown=%v\n",
					s.RouteClass, s.AccountRef, s.RedactedMailbox, s.Greeting, s.PersonUnknown)
			}
		}
	}
	for _, w := range snap.Warnings {
		fmt.Fprintf(&b, "\nWARNING: %s\n", w)
	}
	return b.String()
}

func MarshalFrozenCohort(snap *FrozenCohortSnapshot) ([]byte, error) {
	if snap == nil {
		return nil, fmt.Errorf("nil frozen cohort")
	}
	return json.MarshalIndent(snap, "", "  ")
}

func UnmarshalFrozenCohort(raw []byte) (*FrozenCohortSnapshot, error) {
	var snap FrozenCohortSnapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return nil, err
	}
	if snap.SchemaVersion != "" && snap.SchemaVersion != FrozenCohortSchemaV1 {
		return nil, fmt.Errorf("unsupported frozen cohort schema %q", snap.SchemaVersion)
	}
	return &snap, nil
}

// GrantFromFrozenSnapshot builds a grant from a frozen preview. Actor is required.
func GrantFromFrozenSnapshot(snap *FrozenCohortSnapshot, orgID, actor uuid.UUID, now time.Time) (*BoundedCohortAuthorization, error) {
	if snap == nil {
		return nil, fmt.Errorf("frozen cohort required")
	}
	if actor == uuid.Nil {
		return nil, ErrCohortHumanActor
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	ttl := time.Duration(snap.TTLSeconds) * time.Second
	if ttl <= 0 {
		ttl = DefaultCohortTTL
	}
	auth := &BoundedCohortAuthorization{
		ID: uuid.New(), OrganizationID: orgID, ActorID: actor, AuthorizedAt: now,
		RepositorySHA: snap.RepositorySHA, FeedSchemaVersion: snap.FeedSchemaVersion,
		CohortID: snap.CohortID, CohortHash: snap.CohortHash,
		PolicyVersion: snap.PolicyVersion, AllowedRouteClasses: append([]string{}, snap.AllowedRouteClasses...),
		MaxDailyVolume: snap.MaxDailyVolume, RecipientSetHash: snap.RecipientSetHash,
		ComposerVersion: snap.ComposerVersion, EvidenceVersion: snap.EvidenceVersion,
		TTL: ttl, ExpiresAt: now.Add(ttl),
		FrozenManifest: snap,
	}
	if err := NormalizeBoundedCohortGrant(auth, now); err != nil {
		return nil, err
	}
	return auth, nil
}
