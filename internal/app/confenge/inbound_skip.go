package confenge

import (
	"github.com/warmbly/warmbly/internal/app/confenge/intel"
	"github.com/warmbly/warmbly/internal/models"
)

const (
	InboundSkipSynthetic = intel.InboundSkipSynthetic
	InboundSkipQA        = intel.InboundSkipQA
	InboundSkipInternal  = intel.InboundSkipInternal
)

// InboundCommercialSkipReason is the commercial INBOUND NOW skip gate.
// It is the same classifier ObserveFromInbound uses so a persisted
// synthetic/qa/internal receipt cannot enter real executive rollups.
func InboundCommercialSkipReason(lead models.OutreachInboundLead) string {
	return intel.InboundCommercialSkipReason(lead)
}
