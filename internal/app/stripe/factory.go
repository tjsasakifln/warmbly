package stripe

import (
	"fmt"

	"github.com/warmbly/warmbly/internal/app/discount"
	"github.com/warmbly/warmbly/internal/app/worker"
	"github.com/warmbly/warmbly/internal/config"
	"github.com/warmbly/warmbly/internal/repository"
)

// NewFromEnv constructs the billing backend selected by BILLING_PROVIDER.
// stripe selects the hosted implementation (compiled out of the min-profile
// build). Anything else, including the self-host default "none", is disabled.
func NewFromEnv(
	cfg *config.StripeConfig,
	subRepo repository.SubscriptionRepository,
	planRepo repository.PlanRepository,
	workerAssignment worker.WorkerAssignmentService,
	discountService discount.DiscountService,
) (StripeService, error) {
	if config.BillingProvider() != "stripe" {
		return NewDisabledService(), nil
	}
	if cfg == nil {
		return nil, fmt.Errorf("stripe: BILLING_PROVIDER=stripe requires Stripe config")
	}
	return newLive(cfg, subRepo, planRepo, workerAssignment, discountService)
}
