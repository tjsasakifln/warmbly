//go:build minprofile

package stripe

import (
	"errors"

	"github.com/warmbly/warmbly/internal/app/discount"
	"github.com/warmbly/warmbly/internal/app/worker"
	"github.com/warmbly/warmbly/internal/config"
	"github.com/warmbly/warmbly/internal/repository"
)

// ErrStripeNotCompiled is returned when BILLING_PROVIDER=stripe on a
// min-profile build that did not compile the Stripe backend.
var ErrStripeNotCompiled = errors.New("stripe: billing backend not compiled in; rebuild without -tags minprofile (or use BILLING_PROVIDER=none)")

func newLive(
	_ *config.StripeConfig,
	_ repository.SubscriptionRepository,
	_ repository.PlanRepository,
	_ worker.WorkerAssignmentService,
	_ discount.DiscountService,
) (StripeService, error) {
	return nil, ErrStripeNotCompiled
}
