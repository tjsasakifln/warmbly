package intel

import "time"

const (
	EventOfferViewed             = "offer_viewed"
	EventOfferSelected           = "offer_selected"
	EventEligibilitySubmitted    = "eligibility_submitted"
	EventCapacityApproved        = "capacity_approved"
	EventCapacityRejected        = "capacity_rejected"
	EventCapacityWaitlisted      = "capacity_waitlisted"
	EventCapacityHoldExpired     = "capacity_hold_expired"
	EventCapacityReserved        = "capacity_reserved"
	EventCapacityReleased        = "capacity_released"
	EventTermsAccepted           = "terms_accepted"
	EventCheckoutCreated         = "checkout_created"
	EventCheckoutExpired         = "checkout_expired"
	EventPaymentCreated          = "payment_created"
	EventPaymentPending          = "payment_pending"
	EventPaymentConfirmed        = "payment_confirmed"
	EventPaymentReceived         = "payment_received"
	EventPaymentOverdue          = "payment_overdue"
	EventPaymentRefunded         = "payment_refunded"
	EventPaymentFailed           = "payment_failed"
	EventSubscriptionCreated     = "subscription_created"
	EventSubscriptionActive      = "subscription_active"
	EventSubscriptionEnded       = "subscription_ended"
	EventSubscriptionCanceled    = "subscription_canceled"
	EventOnboardingStarted       = "onboarding_started"
	EventServiceActivated        = "service_activated"
	EventServicePaused           = "service_paused"
	EventServiceEnded            = "service_ended"
	EventRenewalDue              = "renewal_due"
	EventRenewed                 = "renewed"
	EventCommercialExceptionOpen = "commercial_exception_opened"
	EventCommercialExceptionRes  = "commercial_exception_resolved"
	EventUnknownProvider         = "unknown_provider_event"

	OfferDiagnostico = "CFG-DIAG-EXP-v1"
	OfferDirB2G180   = "CFG-DIRB2G-180-v1"
	OfferDirB2G365   = "CFG-DIRB2G-365-v1"
	ExtraPrivateCode = "CFG-EXTRA-HIST-10K"

	BillingOneTime   = "one_time"
	BillingRecurring = "recurring"

	CurrencyBRL = "BRL"

	CapacityPolicyV1     = "capacity.policy.v1"
	CapacityLimitV1      = 50
	CapacityHoldTTL      = 72 * time.Hour
	CanonicalAPIHost     = "api.confenge.com.br"
	TaxPremisePercent    = 6
	ExceptionCodeVersion = "commercial.exception.v1"

	EligibilityUnknown   = "UNKNOWN"
	EligibilityPending   = "pending"
	EligibilityEligible  = "eligible"
	EligibilityRejected  = "rejected"
	EligibilityWaitlist  = "waitlisted"
	CapacityStateNone    = "none"
	CapacityStateHold    = "held"
	CapacityStateOK      = "approved"
	CapacityStateReject  = "rejected"
	CapacityStateWait    = "waitlisted"
	CapacityStateExpired = "expired"
	CapacityStateFinal   = "reserved"
	CapacityStateRelease = "released"

	PaymentStatusNone      = "none"
	PaymentStatusCreated   = "created"
	PaymentStatusPending   = "pending"
	PaymentStatusConfirmed = "confirmed"
	PaymentStatusReceived  = "received"
	PaymentStatusOverdue   = "overdue"
	PaymentStatusRefunded  = "refunded"
	PaymentStatusFailed    = "failed"
	PaymentStatusUnknown   = "UNKNOWN"

	ReviewRequired = "review_required"
	ReviewApproved = "review_approved"
	ReviewRejected = "review_rejected"

	GatePending  = "pending"
	GateApproved = "approved"
	GateRejected = "rejected"
	GateUnknown  = "UNKNOWN"

	SLADiagnosticoMinDays = 10
	SLADiagnosticoMaxDays = 15
	SLAOneOffHours        = 48

	EarlyExit180Cents = int64(500000)
	EarlyExit365Cents = int64(750000)
	PenaltyRateBPS    = 200 // 2%
	InterestMonthBPS  = 100 // 1%
	CommercialDays    = 30

	EnvCatalogAuthorityHash = "CONFENGE_CATALOG_AUTHORITY_HASH"
	EnvProviderMode         = "CONFENGE_PROVIDER_MODE"
	ProviderModeDisabled    = "disabled"
	ProviderModeSandbox     = "sandbox"

	ExceptionTermsDrift           = "terms_price_version_drift"
	ExceptionCapacityExpired      = "capacity_expired"
	ExceptionCapacityLost         = "capacity_lost"
	ExceptionNoCapacity           = "no_capacity"
	ExceptionDuplicateCNPJ        = "duplicate_cnpj"
	ExceptionPaymentOverdue       = "payment_overdue"
	ExceptionPaymentRefund        = "payment_refund"
	ExceptionChargeback           = "payment_chargeback"
	ExceptionCounselReviewDue     = "counsel_review_due"
	ExceptionNfseManualQueue      = "nfse_manual_queue"
	ExceptionSubscriptionEnded    = "subscription_ended"
	ExceptionUnknownProviderEvent = "unknown_provider_event"
	ExceptionInvalidSecret        = "invalid_secret"
	ExceptionProviderUnavailable  = "provider_unavailable"
	ExceptionOnboardingBeforePay  = "onboarding_before_payment"
	ExceptionSilentRenewal        = "silent_renewal_refused"
	ExceptionPrivateExtraAsOffer  = "private_extra_as_offer"
	ExceptionConflictingExternal  = "conflicting_external_reference"
	ExceptionCreatedAsRevenue     = "created_object_as_revenue"
	ExceptionImpossibleCommercial = "impossible_commercial_transition"
	ExceptionPIIRejected          = "pii_rejected"
	ExceptionWaitlisted           = "capacity_waitlisted"
	ExceptionCapacityRejected     = "capacity_rejected"
	ExceptionCheckoutExpired      = "checkout_expired"
	ExceptionEndState6            = "recurring_end_state"
)

// OfferSnapshot is an immutable offer/terms capture. Never a billing product.
type OfferSnapshot struct {
	OfferID              string `json:"offer_id,omitempty"`
	OfferVersion         string `json:"offer_version,omitempty"`
	PublicCode           string `json:"public_code,omitempty"`
	InternalCode         string `json:"internal_code,omitempty"`
	TermsVersion         string `json:"terms_version,omitempty"`
	TermsHash            string `json:"terms_hash,omitempty"`
	AmountCents          int64  `json:"amount_cents,omitempty"`
	Currency             string `json:"currency,omitempty"`
	BillingMode          string `json:"billing_mode,omitempty"`
	Cycle                string `json:"cycle,omitempty"`
	CommitmentMonths     int    `json:"commitment_months,omitempty"`
	MaxPayments          int    `json:"max_payments,omitempty"`
	TotalCommitmentCents int64  `json:"total_commitment_cents,omitempty"`
	NoticeDays           int    `json:"notice_days,omitempty"`
	ScopeVersion         string `json:"scope_version,omitempty"`
	SnapshotHash         string `json:"snapshot_hash,omitempty"`
	CatalogAuthorityHash string `json:"catalog_authority_hash,omitempty"`
	TaxPremisePercent    int    `json:"tax_premise_percent,omitempty"`
	CanonicalAPIHost     string `json:"canonical_api_host,omitempty"`
	Public               bool   `json:"public,omitempty"`
}

// CapacitySnapshot is versioned capacity policy plus this chain's hold.
type CapacitySnapshot struct {
	PolicyVersion   string     `json:"policy_version,omitempty"`
	Limit           int        `json:"capacity_limit,omitempty"`
	Units           int        `json:"capacity_units,omitempty"`
	Eligibility     string     `json:"eligibility_state,omitempty"`
	State           string     `json:"reservation_state,omitempty"`
	HoldID          string     `json:"hold_id,omitempty"`
	HoldCreatedAt   *time.Time `json:"hold_created_at,omitempty"`
	HoldExpiresAt   *time.Time `json:"hold_expires_at,omitempty"`
	FinalReservedAt *time.Time `json:"final_reserved_at,omitempty"`
	WaitlistReason  string     `json:"waitlist_reason,omitempty"`
	RejectReason    string     `json:"reject_reason,omitempty"`
}

// ProviderRefs are optional IDs created outside Warmbly. Never mutated here.
type ProviderRefs struct {
	CustomerID      string `json:"provider_customer_id,omitempty"`
	CheckoutID      string `json:"provider_checkout_id,omitempty"`
	SubscriptionID  string `json:"provider_subscription_id,omitempty"`
	PaymentID       string `json:"provider_payment_id,omitempty"`
	ExternalRef     string `json:"external_reference,omitempty"`
	ProviderEventID string `json:"provider_event_id,omitempty"`
	PaymentMethod   string `json:"payment_method,omitempty"`
}

// PaymentState keeps created objects distinct from received revenue.
type PaymentState struct {
	CanonicalStatus   string     `json:"canonical_status,omitempty"`
	RawProviderStatus string     `json:"raw_provider_status,omitempty"`
	PrincipalCents    int64      `json:"principal_cents,omitempty"`
	ReceivedCents     int64      `json:"received_amount_cents,omitempty"`
	RefundedCents     int64      `json:"refunded_amount_cents,omitempty"`
	ContractedCents   int64      `json:"contracted_revenue_cents,omitempty"`
	MRRCents          int64      `json:"mrr_cents,omitempty"`
	ConfirmedCount    int        `json:"confirmed_count,omitempty"`
	ReceivedCount     int        `json:"received_count,omitempty"`
	CreatedCount      int        `json:"created_count,omitempty"`
	LastPaymentAt     *time.Time `json:"last_payment_at,omitempty"`
	ConfirmedAt       *time.Time `json:"confirmed_at,omitempty"`
	ReceivedAt        *time.Time `json:"received_at,omitempty"`
	OverdueAt         *time.Time `json:"overdue_at,omitempty"`
	ReviewStatus      string     `json:"review_status,omitempty"`
	EvidenceRef       string     `json:"evidence_ref,omitempty"`
	FinanceReviewReq  bool       `json:"finance_review_required,omitempty"`
}

// SubscriptionState is observed provider/subscription status, not a billing engine.
type SubscriptionState struct {
	CanonicalStatus string     `json:"canonical_status,omitempty"`
	RawStatus       string     `json:"raw_status,omitempty"`
	CreatedAt       *time.Time `json:"created_at,omitempty"`
	ActiveAt        *time.Time `json:"active_at,omitempty"`
	EndedAt         *time.Time `json:"ended_at,omitempty"`
	CanceledAt      *time.Time `json:"canceled_at,omitempty"`
	MaxPayments     int        `json:"max_payments,omitempty"`
	EndedAfterMax   bool       `json:"ended_after_max,omitempty"`
}

// DeliveryState is onboarding/service clocks. SLA is policy, not invented.
type DeliveryState struct {
	OnboardingStartedAt *time.Time `json:"onboarding_started_at,omitempty"`
	ServiceActivatedAt  *time.Time `json:"service_activated_at,omitempty"`
	ServicePausedAt     *time.Time `json:"service_paused_at,omitempty"`
	ServiceEndedAt      *time.Time `json:"service_ended_at,omitempty"`
	OneOffAcceptedAt    *time.Time `json:"one_off_accepted_at,omitempty"`
	CompleteInputAt     *time.Time `json:"complete_input_at,omitempty"`
	DueAt               *time.Time `json:"due_at,omitempty"`
	ClockPauseReason    string     `json:"clock_pause_reason,omitempty"`
	SLAMinBusinessDays  int        `json:"sla_min_business_days,omitempty"`
	SLAMaxBusinessDays  int        `json:"sla_max_business_days,omitempty"`
	SLABusinessHours    int        `json:"sla_business_hours,omitempty"`
	ServiceStatus       string     `json:"service_status,omitempty"`
}

// GateStates record external approvals. Warmbly never self-approves them.
type GateStates struct {
	Legal        string `json:"legal,omitempty"`
	Accounting   string `json:"accounting,omitempty"`
	Security     string `json:"security,omitempty"`
	Delivery     string `json:"delivery,omitempty"`
	Capacity     string `json:"capacity,omitempty"`
	Publication  string `json:"publication,omitempty"`
	Finance      string `json:"finance,omitempty"`
	ApproverRef  string `json:"approver_ref,omitempty"`
	EvidenceRef  string `json:"evidence_ref,omitempty"`
	EvidenceHash string `json:"evidence_hash,omitempty"`
}

// CommercialReceipt is one immutable commercial event on the chain timeline.
type CommercialReceipt struct {
	EventID         string    `json:"event_id"`
	Type            string    `json:"type"`
	RawType         string    `json:"raw_type,omitempty"`
	ProviderEventID string    `json:"provider_event_id,omitempty"`
	ExternalRef     string    `json:"external_reference,omitempty"`
	OccurredAt      time.Time `json:"occurred_at"`
	IngestedAt      time.Time `json:"ingested_at"`
	EvidenceRef     string    `json:"evidence_ref,omitempty"`
	RawStatus       string    `json:"raw_status,omitempty"`
	CanonicalStatus string    `json:"canonical_status,omitempty"`
	ActorRef        string    `json:"actor_ref,omitempty"`
}

// CommercialState is derived canonical commercial state on the existing chain.
type CommercialState struct {
	Offer          OfferSnapshot       `json:"offer,omitempty"`
	Capacity       CapacitySnapshot    `json:"capacity,omitempty"`
	Provider       ProviderRefs        `json:"provider,omitempty"`
	Payment        PaymentState        `json:"payment,omitempty"`
	Subscription   SubscriptionState   `json:"subscription,omitempty"`
	Delivery       DeliveryState       `json:"delivery,omitempty"`
	Gates          GateStates          `json:"gates,omitempty"`
	Timeline       []CommercialReceipt `json:"timeline,omitempty"`
	CompanyRef     string              `json:"company_ref,omitempty"`
	CNPJHash       string              `json:"cnpj_hash,omitempty"`
	QueryClass     string              `json:"query_class,omitempty"`
	ReferrerClass  string              `json:"referrer_class,omitempty"`
	ManualTouches  int                 `json:"manual_touches,omitempty"`
	HumanCorrected bool                `json:"human_corrected,omitempty"`
}

// TransitionResult is the shipped commercial transition outcome.
type TransitionResult struct {
	Facts      ObservedFacts `json:"facts"`
	Exceptions []Exception   `json:"exceptions,omitempty"`
	Held       bool          `json:"held"`
	Rejected   bool          `json:"rejected"`
	Replay     bool          `json:"replay"`
	Reason     string        `json:"reason,omitempty"`
}

// EventReceipt is the durable provider/manual receipt. Duplicate IDs return the first.
type EventReceipt struct {
	ID              string    `json:"id"`
	OrganizationID  string    `json:"organization_id,omitempty"`
	ProviderEventID string    `json:"provider_event_id,omitempty"`
	ExternalRef     string    `json:"external_reference,omitempty"`
	EventID         string    `json:"event_id,omitempty"`
	Identity        string    `json:"identity,omitempty"`
	Type            string    `json:"type,omitempty"`
	RawType         string    `json:"raw_type,omitempty"`
	RawStatus       string    `json:"raw_status,omitempty"`
	Acked           bool      `json:"acked"`
	Processed       bool      `json:"processed"`
	Synthetic       bool      `json:"synthetic,omitempty"`
	At              time.Time `json:"at"`
}

// CapacityHold is one versioned hold against the org pool.
type CapacityHold struct {
	HoldID      string     `json:"hold_id"`
	OrgID       string     `json:"organization_id,omitempty"`
	LeadID      string     `json:"lead_id,omitempty"`
	Units       int        `json:"units"`
	State       string     `json:"state"`
	CreatedAt   time.Time  `json:"created_at"`
	ExpiresAt   time.Time  `json:"expires_at"`
	FinalizedAt *time.Time `json:"finalized_at,omitempty"`
}

// CapacityPool is the org-level view. Limit 50 is policy, not a magic number.
type CapacityPool struct {
	PolicyVersion string `json:"policy_version"`
	Limit         int    `json:"limit"`
	Used          int    `json:"used"`
	Held          int    `json:"held"`
	Available     int    `json:"available"`
	Expired       int    `json:"expired"`
}

// IPCAInput is a versioned index supplied by the operator. Never invented.
type IPCAInput struct {
	Version         string `json:"version"`
	IndexRef        string `json:"index_ref"`
	AdjustmentCents int64  `json:"adjustment_cents"`
}

// OverdueInput is the pure overdue calculator request.
type OverdueInput struct {
	PrincipalCents int64      `json:"principal_cents"`
	DueAt          time.Time  `json:"due_at"`
	AsOf           time.Time  `json:"as_of"`
	Location       string     `json:"location,omitempty"`
	IPCA           *IPCAInput `json:"ipca,omitempty"`
	NoticeDays     int        `json:"notice_days,omitempty"`
}

// OverdueResult is reproducible overdue math. Always review-required.
type OverdueResult struct {
	PrincipalCents        int64      `json:"principal_cents"`
	PenaltyCents          int64      `json:"penalty_cents"`
	InterestCents         int64      `json:"interest_cents"`
	IPCAAdjustmentCents   int64      `json:"ipca_adjustment_cents"`
	IPCAApplied           bool       `json:"ipca_applied"`
	IPCAMissing           bool       `json:"ipca_missing"`
	DaysLate              int        `json:"days_late"`
	TotalCents            int64      `json:"total_cents"`
	NoticeAt              *time.Time `json:"notice_at,omitempty"`
	SuspensionAt          *time.Time `json:"suspension_at,omitempty"`
	TerminationAt         *time.Time `json:"termination_at,omitempty"`
	FinanceReviewRequired bool       `json:"finance_review_required"`
}

// BreachWaiver is a human/document CONFENGE-breach waiver.
type BreachWaiver struct {
	Present     bool   `json:"present"`
	EvidenceRef string `json:"evidence_ref,omitempty"`
	Actor       string `json:"actor,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

// EarlyExitInput is the pure early-exit calculator request.
type EarlyExitInput struct {
	Plan                    string       `json:"plan"`
	StartedMonths           int          `json:"started_months"`
	OriginalCommitmentCents int64        `json:"original_commitment_cents"`
	UnpaidNominalCents      int64        `json:"unpaid_nominal_cents"`
	Waiver                  BreachWaiver `json:"waiver"`
}

// EarlyExitResult is min(cálculo, 20% commitment, unpaid). Always review-required.
type EarlyExitResult struct {
	CalculatedCents       int64  `json:"calculated_cents"`
	Cap20PercentCents     int64  `json:"cap_20_percent_cents"`
	UnpaidNominalCents    int64  `json:"unpaid_nominal_cents"`
	SelectedCents         int64  `json:"selected_cents"`
	SelectedCap           string `json:"selected_cap"`
	WaiverApplied         bool   `json:"waiver_applied"`
	WaiverValid           bool   `json:"waiver_valid"`
	FinanceReviewRequired bool   `json:"finance_review_required"`
}

// OperatorRequest is the authenticated manual-first write.
type OperatorRequest struct {
	Action           string           `json:"action"`
	LeadID           string           `json:"lead_id,omitempty"`
	ReceiptID        string           `json:"receipt_id,omitempty"`
	CorrelationID    string           `json:"correlation_id,omitempty"`
	IdempotencyKey   string           `json:"idempotency_key,omitempty"`
	EventID          string           `json:"event_id,omitempty"`
	ActorRef         string           `json:"actor_ref,omitempty"`
	EvidenceRef      string           `json:"evidence_ref,omitempty"`
	EvidenceHash     string           `json:"evidence_hash,omitempty"`
	Offer            OfferSnapshot    `json:"offer,omitempty"`
	Capacity         CapacitySnapshot `json:"capacity,omitempty"`
	Provider         ProviderRefs     `json:"provider,omitempty"`
	Payment          PaymentState     `json:"payment,omitempty"`
	Gates            GateStates       `json:"gates,omitempty"`
	CompanyRef       string           `json:"company_ref,omitempty"`
	CNPJ             string           `json:"cnpj,omitempty"`
	Source           string           `json:"source,omitempty"`
	AssetID          string           `json:"asset_id,omitempty"`
	CTAID            string           `json:"cta_id,omitempty"`
	Query            string           `json:"query,omitempty"`
	Referrer         string           `json:"referrer,omitempty"`
	RouteFamily      string           `json:"route_family,omitempty"`
	OccurredAt       time.Time        `json:"occurred_at,omitempty"`
	HumanConfirmed   bool             `json:"human_confirmed,omitempty"`
	Correction       bool             `json:"correction,omitempty"`
	CorrectionNote   string           `json:"correction_note,omitempty"`
	Synthetic        bool             `json:"synthetic,omitempty"`
	ClockPauseReason string           `json:"clock_pause_reason,omitempty"`
}

// OperatorResult is the manual-first outcome.
type OperatorResult struct {
	Join      JoinResult      `json:"join"`
	Canonical *CanonicalState `json:"canonical,omitempty"`
	Rejected  bool            `json:"rejected,omitempty"`
	Reason    string          `json:"reason,omitempty"`
}

// CanonicalState is the operator timeline + derived commercial state.
type CanonicalState struct {
	Identity    string              `json:"identity"`
	LeadID      string              `json:"lead_id"`
	Held        bool                `json:"held"`
	Outcome     string              `json:"outcome_type"`
	Commercial  CommercialState     `json:"commercial"`
	Timeline    []CommercialReceipt `json:"timeline"`
	Exceptions  []Exception         `json:"exceptions,omitempty"`
	CausalProof bool                `json:"causal_proof"`
	Synthetic   bool                `json:"synthetic"`
}

// WebhookAck is the fast durable receipt response.
type WebhookAck struct {
	ReceiptID string     `json:"receipt_id"`
	Replay    bool       `json:"replay"`
	Acked     bool       `json:"acked"`
	Processed bool       `json:"processed"`
	Held      bool       `json:"held"`
	Join      JoinResult `json:"join,omitempty"`
}

// ProviderEvent is a parsed provider callback/webhook. Not revenue authority.
type ProviderEvent struct {
	ProviderEventID string         `json:"provider_event_id"`
	ExternalRef     string         `json:"external_reference,omitempty"`
	RawType         string         `json:"raw_type"`
	RawStatus       string         `json:"raw_status,omitempty"`
	CustomerID      string         `json:"customer_id,omitempty"`
	CheckoutID      string         `json:"checkout_id,omitempty"`
	SubscriptionID  string         `json:"subscription_id,omitempty"`
	PaymentID       string         `json:"payment_id,omitempty"`
	AmountCents     int64          `json:"amount_cents,omitempty"`
	Currency        string         `json:"currency,omitempty"`
	OccurredAt      time.Time      `json:"occurred_at"`
	PaymentMethod   string         `json:"payment_method,omitempty"`
	UnknownFields   []string       `json:"unknown_fields,omitempty"`
	RawMinimized    map[string]any `json:"raw_minimized,omitempty"`
}

// CommercialCounts is the executive commercial slice. Not mixed with inbound QCO.
type CommercialCounts struct {
	OfferViewed        int   `json:"offer_viewed"`
	Eligibility        int   `json:"eligibility"`
	CapacityApproved   int   `json:"capacity_approved"`
	CapacityHeld       int   `json:"capacity_held"`
	CapacityWaitlisted int   `json:"capacity_waitlisted"`
	CapacityRejected   int   `json:"capacity_rejected"`
	CapacityExpired    int   `json:"capacity_expired"`
	CapacityUsed       int   `json:"capacity_used"`
	CapacityAvailable  int   `json:"capacity_available"`
	TermsAccepted      int   `json:"terms_accepted"`
	CheckoutCreated    int   `json:"checkout_created"`
	PaymentCreated     int   `json:"payment_created"`
	PaymentPending     int   `json:"payment_pending"`
	PaymentConfirmed   int   `json:"payment_confirmed"`
	PaymentReceived    int   `json:"payment_received"`
	PaymentOverdue     int   `json:"payment_overdue"`
	PaymentRefunded    int   `json:"payment_refunded"`
	Onboarding         int   `json:"onboarding"`
	ServiceActive      int   `json:"service_active"`
	Renewal            int   `json:"renewal"`
	Canceled           int   `json:"canceled"`
	Expansion          int   `json:"expansion"`
	ContractedCents    int64 `json:"contracted_revenue_cents"`
	MRRCents           int64 `json:"mrr_cents"`
	ReceivedCents      int64 `json:"received_revenue_cents"`
	ExceptionCount     int   `json:"exception_count"`
	ManualTouches      int   `json:"manual_touches"`
	QualifiedPipeline  int   `json:"qualified_pipeline"`
}
