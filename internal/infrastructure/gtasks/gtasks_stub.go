//go:build minprofile

package gtasks

import (
	"context"
	"errors"
	"time"

	"github.com/warmbly/warmbly/internal/tasks/proto"
)

// ErrGCPNotCompiled is returned when TASKS_PROVIDER=gcloud on a min-profile build.
var ErrGCPNotCompiled = errors.New("gtasks: gcloud backend not compiled in; rebuild without -tags minprofile (or use TASKS_PROVIDER=local)")

// Client is a placeholder so cmd/backend type-checks the gcloud branch.
type Client struct{}

func NewClient(_ context.Context, _, _, _, _ string) (*Client, error) {
	return nil, ErrGCPNotCompiled
}

func (c *Client) CreateTask(_ context.Context, _ *proto.ProcessTask, _ time.Time) (string, error) {
	return "", ErrGCPNotCompiled
}

func (c *Client) DeleteTask(_ context.Context, _ string) error {
	return ErrGCPNotCompiled
}
