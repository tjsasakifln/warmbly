//go:build minprofile

package gtasks

import (
	"context"
	"errors"
	"testing"
)

func TestNewClient_MinProfileRejectsGCloud(t *testing.T) {
	_, err := NewClient(context.Background(), "q", "http://example", "sa@example", "")
	if !errors.Is(err, ErrGCPNotCompiled) {
		t.Fatalf("gcloud client on minprofile: %v", err)
	}
}
