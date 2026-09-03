package confenge

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/app/confenge/liveintel"
	"github.com/warmbly/warmbly/internal/errx"
)

// Inbound opportunity-event ingestion.
//
// The envelope is persisted before anything else happens to it. That ordering
// is the whole point: the upstream posts once and cannot be asked to post
// again, so a crash after the 200 must not be able to lose the event. Delivery
// is the reclaim worker's job, driven off the stored row, not off this request.

// OpportunityEventReceipt is what one accepted envelope did.
type OpportunityEventReceipt struct {
	Schema     string `json:"schema"`
	EventID    string `json:"event_id"`
	EventType  string `json:"event_type"`
	SubjectKey string `json:"subject_key"`
	// Replay is true when this event id was already stored. The envelope is not
	// rewritten: the first copy is the evidence of what the upstream said.
	Replay bool `json:"replay"`
}

// WireIntelWatchInbox installs the durable opportunity-event inbox. Without it
// inbound opportunity events are refused rather than accepted and dropped.
func (s *service) WireIntelWatchInbox(inbox liveintel.EventInbox) {
	if s == nil {
		return
	}
	s.intelWatchInbox = inbox
}

// IntelWatchInbox returns the wired inbox, or nil. The caller uses it to build
// the production EventProducer over the same store this endpoint writes.
func (s *service) IntelWatchInbox() liveintel.EventInbox {
	if s == nil {
		return nil
	}
	return s.intelWatchInbox
}

// IngestOpportunityEvent validates and durably stores one inbound event.
//
// orgID is the organization Warmbly's webhook auth resolved. It is written onto
// the event BEFORE validation and stored as its own column, so an external
// caller cannot reach another organization's watch list by naming it in the
// body. Any org id inside the payload is overwritten, never consulted.
func (s *service) IngestOpportunityEvent(ctx context.Context, orgID uuid.UUID, event liveintel.OpportunityEvent, now time.Time) (*OpportunityEventReceipt, *errx.Error) {
	if xerr := s.requireEnabled(); xerr != nil {
		return nil, xerr
	}
	if orgID == uuid.Nil {
		return nil, errx.New(errx.ServiceUnavailable, "inbound org is not configured")
	}
	event.Schema = liveintel.EventSchemaV1
	event.OrgID = orgID
	if ok, reason := event.Validate(); !ok {
		return nil, errx.NewWithIdentifier(errx.BadRequest, "confenge_opportunity_event_"+reason,
			"opportunity event rejected: "+reason)
	}
	if s.intelWatchInbox == nil {
		return nil, errx.NewWithIdentifier(errx.ServiceUnavailable, "confenge_opportunity_event_inbox_unavailable",
			"opportunity event inbox is not wired")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	inserted, err := s.intelWatchInbox.AppendOpportunityEvent(ctx, liveintel.InboxRowFromEvent(orgID, event, now))
	if err != nil {
		return nil, errx.New(errx.Internal, "opportunity event inbox write: "+err.Error())
	}
	return &OpportunityEventReceipt{
		Schema: liveintel.EventSchemaV1, EventID: event.EventID,
		EventType: string(event.EventType), SubjectKey: event.SubjectKey,
		Replay: !inserted,
	}, nil
}
