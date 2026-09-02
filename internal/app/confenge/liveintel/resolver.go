package liveintel

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

// Resolver looks up live intelligence for one organization/account pair. The
// bool is true only when a valid, attested payload with a public URL is
// available; every other condition is (nil, false), never an error.
type Resolver interface {
	Resolve(ctx context.Context, orgID, accountID uuid.UUID) (*LiveIntelligenceV1, bool)
}

// LookupFunc adapts an ordinary error-returning source into a Resolver. The
// error is swallowed and logged: an intelligence lookup that fails is absent
// intelligence, not a reason to hold a first touch.
type LookupFunc func(ctx context.Context, orgID, accountID uuid.UUID) (*LiveIntelligenceV1, error)

func (f LookupFunc) Resolve(ctx context.Context, orgID, accountID uuid.UUID) (*LiveIntelligenceV1, bool) {
	if f == nil {
		return nil, false
	}
	value, err := f(ctx, orgID, accountID)
	if err != nil {
		log.Warn().Err(err).Str("organization_id", orgID.String()).Str("account_id", accountID.String()).
			Msg("confenge live intelligence: lookup failed, continuing without it")
		return nil, false
	}
	if ok, reason := value.Validate(); !ok {
		log.Warn().Str("organization_id", orgID.String()).Str("account_id", accountID.String()).
			Str("reason", reason).Msg("confenge live intelligence: payload rejected, continuing without it")
		return nil, false
	}
	return value, true
}

// LookupBudget bounds how long the send path will wait for intelligence. The
// caller holds a live dispatch reservation while this runs, so a slow resolver
// must cost the lease nothing: past the budget the lookup is simply absent.
const LookupBudget = 750 * time.Millisecond

// Attach is the only entry point the send path uses. It is nil-safe, time-
// bounded, swallows every failure mode including a panicking resolver, and
// returns nil when there is nothing trustworthy to attach.
func Attach(ctx context.Context, resolver Resolver, orgID, accountID uuid.UUID) (result *LiveIntelligenceV1) {
	if resolver == nil {
		return nil
	}
	defer func() {
		if r := recover(); r != nil {
			log.Warn().Interface("panic", r).Str("organization_id", orgID.String()).
				Msg("confenge live intelligence: resolver panicked, continuing without it")
			result = nil
		}
	}()
	lookupCtx, cancel := context.WithTimeout(ctx, LookupBudget)
	defer cancel()
	value, ok := resolveWithinBudget(lookupCtx, resolver, orgID, accountID)
	if !ok || value == nil {
		return nil
	}
	// Re-validate whatever a third-party resolver claims is usable. The
	// contract, not the implementation, decides what may be attached.
	if valid, reason := value.Validate(); !valid {
		log.Warn().Str("organization_id", orgID.String()).Str("account_id", accountID.String()).
			Str("reason", reason).Msg("confenge live intelligence: resolver returned an invalid payload")
		return nil
	}
	return value
}

// resolveWithinBudget returns as soon as the budget expires, whether or not the
// resolver has answered, so the caller's dispatch lease is never pinned by a
// slow lookup.
//
// What the buffered channel does and does not buy: it lets the lookup goroutine
// SEND its late answer and exit even though nobody is receiving any more, so the
// goroutine is not blocked forever on the send. It does NOT bound Resolve
// itself. A resolver that ignores its context keeps that goroutine alive for as
// long as it runs, and one that never returns leaks it. That is a deliberate
// trade: the caller is always released at the budget, and a misbehaving
// third-party resolver costs a goroutine rather than a held reservation.
func resolveWithinBudget(ctx context.Context, resolver Resolver, orgID, accountID uuid.UUID) (*LiveIntelligenceV1, bool) {
	type outcome struct {
		value *LiveIntelligenceV1
		ok    bool
	}
	done := make(chan outcome, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Warn().Interface("panic", r).Str("organization_id", orgID.String()).
					Msg("confenge live intelligence: resolver panicked, continuing without it")
				done <- outcome{}
			}
		}()
		value, ok := resolver.Resolve(ctx, orgID, accountID)
		done <- outcome{value: value, ok: ok}
	}()
	select {
	case got := <-done:
		return got.value, got.ok
	case <-ctx.Done():
		log.Warn().Str("organization_id", orgID.String()).Str("account_id", accountID.String()).
			Dur("budget", LookupBudget).
			Msg("confenge live intelligence: lookup exceeded its budget, continuing without it")
		return nil, false
	}
}
