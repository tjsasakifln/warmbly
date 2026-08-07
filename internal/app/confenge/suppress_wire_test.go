package confenge

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/errx"
)

type mockAdvancedSuppress struct {
	calls []string
	err   *errx.Error
}

func (m *mockAdvancedSuppress) SuppressRecipient(ctx context.Context, organizationID uuid.UUID, email, reason string) *errx.Error {
	m.calls = append(m.calls, email+"|"+reason)
	return m.err
}

func TestNewSuppressAdapterNilReturnsNil(t *testing.T) {
	if NewSuppressAdapter(nil) != nil {
		t.Fatal("nil advanced must yield nil adapter (WireSuppress no-op)")
	}
}

func TestNewSuppressAdapterCallsThrough(t *testing.T) {
	mock := &mockAdvancedSuppress{}
	api := NewSuppressAdapter(mock)
	if api == nil {
		t.Fatal("expected adapter")
	}
	org := uuid.New()
	if err := api.SuppressRecipient(context.Background(), org, "dnc@example.com", "reply unsub"); err != nil {
		t.Fatal(err)
	}
	if len(mock.calls) != 1 || !strings.Contains(mock.calls[0], "dnc@example.com") {
		t.Fatalf("adapter did not call through: %v", mock.calls)
	}
}

func TestNewSuppressAdapterPropagatesError(t *testing.T) {
	mock := &mockAdvancedSuppress{err: errx.New(errx.Internal, "db down")}
	api := NewSuppressAdapter(mock)
	err := api.SuppressRecipient(context.Background(), uuid.New(), "x@example.com", "r")
	if err == nil || !strings.Contains(err.Error(), "db down") {
		t.Fatalf("want propagated error, got %v", err)
	}
}

// TestBackendMainWiresSuppressAfterAdvancedConstruction is a structural
// regression guard for the bug where WireSuppress ran while advancedService
// was still nil (before advanced.NewService), so production never attached
// platform suppression.
func TestBackendMainWiresSuppressAfterAdvancedConstruction(t *testing.T) {
	src := readRepoFile(t, "cmd/backend/main.go")
	newIdx := strings.Index(src, "advancedService = advanced.NewService(")
	wireIdx := strings.Index(src, "WireSuppress(confenge.NewSuppressAdapter(advancedService))")
	if newIdx < 0 || wireIdx < 0 {
		t.Fatal("backend main missing advanced.NewService or WireSuppress(NewSuppressAdapter)")
	}
	if wireIdx < newIdx {
		t.Fatalf("WireSuppress must appear AFTER advanced.NewService (wire at %d, new at %d)", wireIdx, newIdx)
	}
	// Early block must not still gate WireSuppress on a nil advancedService
	// before construction.
	early := src[:newIdx]
	if strings.Contains(early, "WireSuppress(confenge.NewSuppressAdapter") {
		t.Fatal("WireSuppress adapter call must not appear before advanced.NewService")
	}
}

// TestConsumerMainWiresSuppressForClassifiedReplyDNC guards the live reply
// path: OnClassifiedReply → NoteDNC needs suppress wired on the consumer.
func TestConsumerMainWiresSuppressForClassifiedReplyDNC(t *testing.T) {
	src := readRepoFile(t, "cmd/consumer/main.go")
	if !strings.Contains(src, "WireSuppress(confenge.NewSuppressAdapter(advancedService))") {
		t.Fatal("consumer must WireSuppress(NewSuppressAdapter(advancedService)) for DNC on reply classification")
	}
	// confenge construction and suppress wire should both be inside Enabled block
	// and after advancedService is created.
	advIdx := strings.Index(src, "advancedService := advanced.NewService(")
	wireIdx := strings.Index(src, "WireSuppress(confenge.NewSuppressAdapter(advancedService))")
	if advIdx < 0 || wireIdx < 0 {
		t.Fatal("missing advanced.NewService or WireSuppress in consumer")
	}
	if wireIdx < advIdx {
		t.Fatal("consumer WireSuppress must run after advanced.NewService")
	}
}

func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	// Prefer repo root relative to this test file (…/internal/app/confenge → …/).
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..", ".."))
	b, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		// Fallback: cwd is often module root under `go test`.
		b, err = os.ReadFile(rel)
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
	}
	return string(b)
}
