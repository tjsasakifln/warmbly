//go:build minprofile

package pubsub

import (
	"context"
	"errors"
)

// ErrGCPNotCompiled is returned when PUBSUB_ENABLED=true on a min-profile build.
var ErrGCPNotCompiled = errors.New("pubsub: google pub/sub backend not compiled in; rebuild without -tags minprofile (or set PUBSUB_ENABLED=false)")

// Client is a placeholder so cmd mains type-check the GCP branch.
type Client struct{}

func NewClient(_ context.Context, _ string) (*Client, error) {
	return nil, ErrGCPNotCompiled
}

func (c *Client) Close() error { return ErrGCPNotCompiled }

func (c *Client) EnsureRealtimeTopology(_ context.Context) error { return ErrGCPNotCompiled }

func (c *Client) Publish(_ context.Context, _ string, _ interface{}, _ map[string]string) error {
	return ErrGCPNotCompiled
}
