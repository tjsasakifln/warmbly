package liveintel

import (
	"context"

	"github.com/google/uuid"
)

// NoopResolver is the default wiring: the hook stays inert until a real
// resolver is registered.
type NoopResolver struct{}

func (NoopResolver) Resolve(context.Context, uuid.UUID, uuid.UUID) (*LiveIntelligenceV1, bool) {
	return nil, false
}
