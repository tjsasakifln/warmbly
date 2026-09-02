package liveintel

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func validPayload() *LiveIntelligenceV1 {
	return &LiveIntelligenceV1{
		Schema:      SchemaLiveIntelligenceV1,
		SubjectKey:  "contrato-2026-0001",
		Kind:        KindOpportunity,
		Headline:    "Aditivo publicado",
		Summary:     "Prazo prorrogado por 12 meses.",
		PublicURL:   "https://pncp.gov.br/app/contratos/2026-0001",
		ObservedAt:  time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC),
		Attestation: "attestation-signature",
	}
}

func TestValidateAcceptsAWellFormedPayload(t *testing.T) {
	if ok, reason := validPayload().Validate(); !ok {
		t.Fatalf("valid payload rejected: %s", reason)
	}
}

func TestValidateRejectsWithAReasonNeverAnError(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*LiveIntelligenceV1)
		reason  string
		nilCase bool
	}{
		{name: "nil payload", nilCase: true, reason: ReasonPayloadNil},
		{name: "wrong schema", mutate: func(v *LiveIntelligenceV1) { v.Schema = "CONFENGE_LIVE_INTELLIGENCE/2.0" }, reason: ReasonSchemaMismatch},
		{name: "no subject", mutate: func(v *LiveIntelligenceV1) { v.SubjectKey = "  " }, reason: ReasonSubjectMissing},
		{name: "unknown kind", mutate: func(v *LiveIntelligenceV1) { v.Kind = "GUESS" }, reason: ReasonKindUnknown},
		{name: "no public url", mutate: func(v *LiveIntelligenceV1) { v.PublicURL = "" }, reason: ReasonPublicURLMissing},
		{name: "unparseable url", mutate: func(v *LiveIntelligenceV1) { v.PublicURL = "://nope" }, reason: ReasonPublicURLInvalid},
		{name: "non-https url", mutate: func(v *LiveIntelligenceV1) { v.PublicURL = "http://example.com/a" }, reason: ReasonPublicURLInvalid},
		{name: "hostless url", mutate: func(v *LiveIntelligenceV1) { v.PublicURL = "https:///a" }, reason: ReasonPublicURLInvalid},
		{name: "no attestation", mutate: func(v *LiveIntelligenceV1) { v.Attestation = "" }, reason: ReasonAttestationEmpty},
		{name: "no observation time", mutate: func(v *LiveIntelligenceV1) { v.ObservedAt = time.Time{} }, reason: ReasonObservedAtUnset},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var payload *LiveIntelligenceV1
			if !tc.nilCase {
				payload = validPayload()
				tc.mutate(payload)
			}
			ok, reason := payload.Validate()
			if ok {
				t.Fatal("payload should not be usable")
			}
			if reason != tc.reason {
				t.Fatalf("reason=%q want %q", reason, tc.reason)
			}
		})
	}
}

type scriptedResolver struct {
	payload *LiveIntelligenceV1
	ok      bool
	calls   int
}

func (r *scriptedResolver) Resolve(context.Context, uuid.UUID, uuid.UUID) (*LiveIntelligenceV1, bool) {
	r.calls++
	return r.payload, r.ok
}

type panickingResolver struct{}

func (panickingResolver) Resolve(context.Context, uuid.UUID, uuid.UUID) (*LiveIntelligenceV1, bool) {
	panic("resolver exploded")
}

func TestNoopResolverIsAlwaysAbsent(t *testing.T) {
	value, ok := NoopResolver{}.Resolve(context.Background(), uuid.New(), uuid.New())
	if value != nil || ok {
		t.Fatalf("noop returned value=%+v ok=%v", value, ok)
	}
}

func TestAttachSwallowsEveryFailureMode(t *testing.T) {
	ctx := context.Background()
	orgID, accountID := uuid.New(), uuid.New()

	if got := Attach(ctx, nil, orgID, accountID); got != nil {
		t.Fatalf("nil resolver attached %+v", got)
	}
	if got := Attach(ctx, NoopResolver{}, orgID, accountID); got != nil {
		t.Fatalf("noop resolver attached %+v", got)
	}
	if got := Attach(ctx, &scriptedResolver{payload: nil, ok: false}, orgID, accountID); got != nil {
		t.Fatalf("absent lookup attached %+v", got)
	}
	// A resolver claiming ok on a nil or malformed payload is not believed.
	if got := Attach(ctx, &scriptedResolver{payload: nil, ok: true}, orgID, accountID); got != nil {
		t.Fatalf("nil payload claimed usable was attached: %+v", got)
	}
	malformed := validPayload()
	malformed.PublicURL = ""
	if got := Attach(ctx, &scriptedResolver{payload: malformed, ok: true}, orgID, accountID); got != nil {
		t.Fatalf("payload without a public URL was attached: %+v", got)
	}
	if got := Attach(ctx, panickingResolver{}, orgID, accountID); got != nil {
		t.Fatalf("panicking resolver attached %+v", got)
	}
	if got := Attach(ctx, &scriptedResolver{payload: validPayload(), ok: true}, orgID, accountID); got == nil {
		t.Fatal("a valid attested payload with a public URL should be attached")
	}
}

// blockingResolver never answers until its release channel closes, standing in
// for a slow upstream.
type blockingResolver struct {
	release chan struct{}
	entered chan struct{}
}

func (r *blockingResolver) Resolve(ctx context.Context, _, _ uuid.UUID) (*LiveIntelligenceV1, bool) {
	close(r.entered)
	select {
	case <-r.release:
		return validPayload(), true
	case <-ctx.Done():
		return nil, false
	}
}

// The send path holds a live dispatch reservation while Attach runs, so a
// resolver that does not answer inside the budget must cost the lease nothing.
func TestAttachIsBoundedByItsLookupBudget(t *testing.T) {
	resolver := &blockingResolver{release: make(chan struct{}), entered: make(chan struct{})}
	t.Cleanup(func() { close(resolver.release) })

	started := time.Now()
	got := Attach(context.Background(), resolver, uuid.New(), uuid.New())
	elapsed := time.Since(started)

	<-resolver.entered
	if got != nil {
		t.Fatalf("a resolver that never answered attached %+v", got)
	}
	if elapsed >= 5*time.Second {
		t.Fatalf("Attach waited %s; the reservation would have been held", elapsed)
	}
	if elapsed < LookupBudget/2 {
		t.Fatalf("Attach gave up after %s, far below its %s budget", elapsed, LookupBudget)
	}
}

// A caller whose context is already cancelled gets no intelligence and no wait.
func TestAttachHonoursAnAlreadyCancelledCaller(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	resolver := &blockingResolver{release: make(chan struct{}), entered: make(chan struct{})}
	t.Cleanup(func() { close(resolver.release) })

	if got := Attach(ctx, resolver, uuid.New(), uuid.New()); got != nil {
		t.Fatalf("a cancelled caller attached %+v", got)
	}
}

func TestLookupFuncSwallowsErrorsAndInvalidPayloads(t *testing.T) {
	ctx := context.Background()
	orgID, accountID := uuid.New(), uuid.New()

	failing := LookupFunc(func(context.Context, uuid.UUID, uuid.UUID) (*LiveIntelligenceV1, error) {
		return nil, errors.New("upstream unavailable")
	})
	if value, ok := failing.Resolve(ctx, orgID, accountID); value != nil || ok {
		t.Fatalf("a failing lookup produced value=%+v ok=%v", value, ok)
	}
	if got := Attach(ctx, failing, orgID, accountID); got != nil {
		t.Fatalf("a failing lookup attached %+v", got)
	}

	noURL := LookupFunc(func(context.Context, uuid.UUID, uuid.UUID) (*LiveIntelligenceV1, error) {
		payload := validPayload()
		payload.PublicURL = ""
		return payload, nil
	})
	if _, ok := noURL.Resolve(ctx, orgID, accountID); ok {
		t.Fatal("a payload without a public URL must not be usable")
	}

	var nilFunc LookupFunc
	if _, ok := nilFunc.Resolve(ctx, orgID, accountID); ok {
		t.Fatal("a nil LookupFunc must not be usable")
	}

	good := LookupFunc(func(context.Context, uuid.UUID, uuid.UUID) (*LiveIntelligenceV1, error) {
		return validPayload(), nil
	})
	value, ok := good.Resolve(ctx, orgID, accountID)
	if !ok || value == nil {
		t.Fatalf("a valid lookup was discarded: value=%+v ok=%v", value, ok)
	}
}

// contextIgnoringResolver never looks at its context. It stands in for a
// third-party lookup that does blocking I/O with no cancellation.
type contextIgnoringResolver struct {
	sleep    time.Duration
	returned chan struct{}
}

func (r *contextIgnoringResolver) Resolve(context.Context, uuid.UUID, uuid.UUID) (*LiveIntelligenceV1, bool) {
	time.Sleep(r.sleep)
	close(r.returned)
	return validPayload(), true
}

// The buffered channel does not stop a context-ignoring resolver from running
// past the budget; it only stops that goroutine blocking on its send. What must
// hold regardless is that the caller is released at the budget and attaches
// nothing, so a first touch is never delayed by an unresponsive lookup.
func TestAttachIsReleasedEvenByAContextIgnoringResolver(t *testing.T) {
	resolver := &contextIgnoringResolver{sleep: 3 * LookupBudget, returned: make(chan struct{})}

	started := time.Now()
	got := Attach(context.Background(), resolver, uuid.New(), uuid.New())
	elapsed := time.Since(started)

	if got != nil {
		t.Fatalf("a resolver that answered past the budget attached %+v", got)
	}
	if elapsed >= 2*LookupBudget {
		t.Fatalf("Attach waited %s for a %s budget; the reservation would have been held", elapsed, LookupBudget)
	}
	// The abandoned goroutine still finishes and sends without blocking, which
	// is all the buffered channel is there for.
	select {
	case <-resolver.returned:
	case <-time.After(5 * LookupBudget):
		t.Fatal("the abandoned lookup goroutine never completed")
	}
}
