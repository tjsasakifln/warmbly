//go:build minprofile

package notify

import (
	"context"
	"errors"
)

// ErrSESNotCompiled is returned when MAIL_TRANSPORT=ses on a min-profile build.
var ErrSESNotCompiled = errors.New("notify: ses backend not compiled in; rebuild without -tags minprofile (or use MAIL_TRANSPORT=smtp or log)")

// NewEmailNotficiationService is the stub used when SES is not compiled in.
func NewEmailNotficiationService(_ context.Context, _, _ string) (EmailNotificationService, error) {
	return nil, ErrSESNotCompiled
}
