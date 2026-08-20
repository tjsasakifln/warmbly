package confenge

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/app/confenge/intel"
	"github.com/warmbly/warmbly/internal/errx"
)

// IngestSearchObservation is the shipped search_observation.v1 receive path.
// It never creates a lead, never emails the operator, and never writes
// web-cfg / extra-cli / SmartLic.
func (s *service) IngestSearchObservation(ctx context.Context, orgID uuid.UUID, raw []byte, opts IngestOptions) (*intel.SearchObservationReceipt, *errx.Error) {
	if xerr := s.requireEnabled(); xerr != nil {
		return nil, xerr
	}
	if s.cfg.AutoSendEnabled {
		return nil, errx.New(errx.Forbidden, "inbound receive is refused while auto_send is enabled")
	}
	if xerr := RejectInboundQueryPII(opts.Query); xerr != nil {
		return nil, xerr
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	obs, err := intel.ParseSearchObservation(raw, orgID.String(), now)
	if err != nil {
		return nil, errx.New(errx.BadRequest, err.Error())
	}
	rec, err := intel.PersistSearchObservation(s.intelStore(), obs, now)
	if err != nil {
		if _, ok := err.(intel.EnvelopeError); ok {
			return nil, errx.New(errx.BadRequest, err.Error())
		}
		return nil, errx.New(errx.Internal, "persist search observation: "+err.Error())
	}
	_ = ctx
	return &rec, nil
}

func mustListObservations(st intel.Store, orgID string) []intel.SearchObservation {
	if st == nil {
		return nil
	}
	rows, err := st.ListSearchObservations(orgID, "")
	if err != nil {
		return nil
	}
	return rows
}
