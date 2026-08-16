package stripe

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
)

// CheckoutSession is the hosted billing checkout handle. Handlers only need
// ID and URL; the min-profile build never constructs a live session.
type CheckoutSession struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

// Customer is the billing-package view of a payment customer.
type Customer struct {
	ID    string `json:"id"`
	Email string `json:"email,omitempty"`
}

// Subscription is the billing-package view of a paid subscription.
type Subscription struct {
	ID                string `json:"id"`
	Status            string `json:"status"`
	CurrentPeriodEnd  int64  `json:"current_period_end,omitempty"`
	CancelAtPeriodEnd bool   `json:"cancel_at_period_end,omitempty"`
}

// Event is a verified webhook payload. ProcessWebhookEvent is the only
// consumer; the hosted implementation stores the raw Stripe event JSON.
type Event struct {
	ID      string
	Type    string
	Payload []byte
}

// ProrationPreview represents the preview of a plan change proration.
type ProrationPreview struct {
	CurrentPlan     *models.Plan `json:"current_plan"`
	NewPlan         *models.Plan `json:"new_plan"`
	ProrationAmount int64        `json:"proration_amount"`
	AmountDue       int64        `json:"amount_due"`
	NextBillingDate time.Time    `json:"next_billing_date"`
	Currency        string       `json:"currency"`
}

// StripeService is the billing backend used by HTTP handlers and credit watch.
// The hosted implementation talks to Stripe; the min-profile build only
// compiles NewDisabledService plus a fail-closed NewFromEnv.
type StripeService interface {
	CreateCustomer(ctx context.Context, userID uuid.UUID, email, name string) (string, *errx.Error)
	GetCustomer(ctx context.Context, customerID string) (*Customer, *errx.Error)

	CreateCheckoutSession(ctx context.Context, userID uuid.UUID, orgID uuid.UUID, priceID, successURL, cancelURL, discountCode string) (*CheckoutSession, *errx.Error)
	CreatePortalSession(ctx context.Context, customerID, returnURL string) (string, *errx.Error)

	GetSubscription(ctx context.Context, subscriptionID string) (*Subscription, *errx.Error)
	CancelSubscription(ctx context.Context, subscriptionID string, cancelAtPeriodEnd bool) *errx.Error

	ChangePlan(ctx context.Context, orgID uuid.UUID, newPlanID uuid.UUID, prorationBehavior, discountCode, interval string) (*Subscription, *errx.Error)
	PreviewPlanChange(ctx context.Context, orgID uuid.UUID, newPlanID uuid.UUID) (*ProrationPreview, *errx.Error)

	VerifyWebhook(payload []byte, signature string) (*Event, *errx.Error)
	ProcessWebhookEvent(ctx context.Context, event *Event) *errx.Error

	ApplyCustomerCredit(ctx context.Context, customerID string, amountCents int64, currency, idempotencyKey string) (string, *errx.Error)
	CreateCreditCheckoutSession(ctx context.Context, userID, orgID uuid.UUID, packKey string, credits int, successURL, cancelURL string) (*CheckoutSession, *errx.Error)
	AutoTopUpCredits(ctx context.Context, orgID uuid.UUID, packKey string, creditAmount int) (bool, error)

	WireReferral(r ReferralRewarder)
	WireCredits(g CreditGranter, a AuditLogger)
}

// CreditGranter is the slice of the credit service the Stripe webhook flow
// drives: reset the monthly allowance each billing cycle and fulfill top-up
// purchases. *credits.creditService satisfies it (plain error returns).
type CreditGranter interface {
	ResetMonthlyAllowance(ctx context.Context, orgID uuid.UUID, allowance int, idempotencyKey string) error
	GrantPurchased(ctx context.Context, orgID uuid.UUID, amount int, reason, idempotencyKey string) (int, error)
}

// AuditLogger is the slice of the audit service the webhook flow uses to fire
// AUDIT_CREATED so teammates' billing/credits views refresh live after a grant
// or purchase. *audit.auditService satisfies it.
type AuditLogger interface {
	LogAction(ctx context.Context, orgID, actorID uuid.UUID, action models.AuditAction, entityType models.AuditEntityType, entityID *uuid.UUID, ip, userAgent string, changes, metadata map[string]string)
}

// ReferralRewarder is the slice of the referral service the Stripe webhook flow
// drives. *referral.Service satisfies it; injected via WireReferral so the
// stripe package needs no import of referral (no cycle).
type ReferralRewarder interface {
	QualifyOnConversion(ctx context.Context, inviteeOrgID uuid.UUID)
	RewardOnFirstInvoice(ctx context.Context, inviteeOrgID, planID uuid.UUID, eventID string) *errx.Error
	ClawbackForInvitee(ctx context.Context, inviteeOrgID uuid.UUID, eventID, reason string)
	SyncStripeBalance(ctx context.Context, orgID uuid.UUID)
	InviteeDiscountCode(ctx context.Context, inviteeOrgID uuid.UUID) string
}
