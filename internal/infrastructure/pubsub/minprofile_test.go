//go:build minprofile

package pubsub

import (
	"context"
	"errors"
	"testing"
)

func TestNewClient_MinProfileRejectsGCP(t *testing.T) {
	_, err := NewClient(context.Background(), "proj")
	if !errors.Is(err, ErrGCPNotCompiled) {
		t.Fatalf("gcp pubsub on minprofile: %v", err)
	}
}
