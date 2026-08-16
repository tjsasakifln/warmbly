//go:build minprofile

package notify

import (
	"context"
	"errors"
	"testing"

	"github.com/warmbly/warmbly/internal/config"
)

func TestNewTransport_MinProfileRejectsSES(t *testing.T) {
	t.Setenv("MAIL_TRANSPORT", "ses")
	_, err := NewTransport(context.Background(), &config.Config{}, "Warmbly", "dev@localhost")
	if !errors.Is(err, ErrSESNotCompiled) {
		t.Fatalf("ses transport on minprofile: %v", err)
	}
}
