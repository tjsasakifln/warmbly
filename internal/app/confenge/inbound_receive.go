package confenge

import (
	"strings"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/app/confenge/intel"
	"github.com/warmbly/warmbly/internal/app/confenge/liveintel"
)

const (
	InboundReceiveReady   = "READY"
	InboundReceiveBlocked = "BLOCKED"

	InboundReasonOutreachOff   = "outreach_disabled"
	InboundReasonAutoSend      = "auto_send_enabled"
	InboundReasonSecretMissing = "inbound_secret_missing"
	InboundReasonOrgMissing    = "inbound_org_missing"
)

// acceptedInboundEventVersions is the full capability list this endpoint
// branches on. It is assembled here because the two intelligence schemas live
// in sibling packages and the probe must not advertise a body the dispatch
// cannot actually route.
func acceptedInboundEventVersions() []string {
	return append(intel.AcceptedEventVersions(), intel.WebIntentSchemaV1, liveintel.EventSchemaV1)
}

// InboundReceiveProbe is the PII-free, secret-free signal web-cfg uses
// to decide whether to POST. Timeout is a client-side class (process
// unreachable). This body is never 401 and never leaks configuration
// values. A lying READY when secret/org/auto-send are wrong is a defect.
type InboundReceiveProbe struct {
	Status                string   `json:"status"`
	AutoSendEnabled       bool     `json:"auto_send_enabled"`
	Reasons               []string `json:"reasons"`
	DispatchAttempted     bool     `json:"dispatch_attempted"`
	AcceptedEventVersions []string `json:"accepted_event_versions"`
}

// EvaluateInboundReceive is the single READY/BLOCKED source for the
// public health surface, operator readiness, and preflight.
func EvaluateInboundReceive(cfg Config) InboundReceiveProbe {
	p := InboundReceiveProbe{
		Status:                InboundReceiveBlocked,
		AutoSendEnabled:       cfg.AutoSendEnabled,
		Reasons:               nil,
		DispatchAttempted:     false,
		AcceptedEventVersions: acceptedInboundEventVersions(),
	}
	if !cfg.Enabled {
		p.Reasons = append(p.Reasons, InboundReasonOutreachOff)
	}
	if cfg.AutoSendEnabled {
		p.Reasons = append(p.Reasons, InboundReasonAutoSend)
	}
	if strings.TrimSpace(cfg.InboundWebhookSecret) == "" {
		p.Reasons = append(p.Reasons, InboundReasonSecretMissing)
	}
	if cfg.InboundOrgID == uuid.Nil && cfg.OperatorOrgID == uuid.Nil {
		p.Reasons = append(p.Reasons, InboundReasonOrgMissing)
	}
	if len(p.Reasons) == 0 {
		p.Status = InboundReceiveReady
		p.Reasons = []string{}
	}
	return p
}
