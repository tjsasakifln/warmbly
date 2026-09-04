package handler

import (
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/app/confenge"
	"github.com/warmbly/warmbly/internal/app/confenge/intel"
	"github.com/warmbly/warmbly/internal/app/confenge/liveintel"
	"github.com/warmbly/warmbly/internal/errx"
)

const inboundHMACSkew = 5 * time.Minute

// ConfengeInboundHealth — GET /api/v1/webhooks/confenge/inbound/health
// Public, PII-free, secret-free READY/BLOCKED probe for web-cfg.
// Always 200 so timeout (unreachable) stays distinct from 401/5xx on POST.
func (h *Handler) ConfengeInboundHealth(c *gin.Context) {
	cfg := confenge.Config{}
	if h.ConfengeService != nil {
		cfg = h.ConfengeService.Config()
		if !h.ConfengeService.Enabled() {
			cfg.Enabled = false
		}
	}
	probe := confenge.EvaluateInboundReceive(cfg)
	c.JSON(http.StatusOK, gin.H{
		"status":                  probe.Status,
		"auto_send_enabled":       probe.AutoSendEnabled,
		"reasons":                 probe.Reasons,
		"dispatch_attempted":      probe.DispatchAttempted,
		"accepted_event_versions": probe.AcceptedEventVersions,
	})
}

// ConfengeInboundWebhook — POST /api/v1/webhooks/confenge/inbound
// HMAC body auth. PII on the query string is rejected.
func (h *Handler) ConfengeInboundWebhook(c *gin.Context) {
	if h.ConfengeService == nil || !h.ConfengeService.Enabled() {
		errx.JSON(c, errx.New(errx.NotFound, "CONFENGE outreach is not enabled on this server"))
		return
	}
	if xerr := confenge.RejectInboundQueryPII(c.Request.URL.Query()); xerr != nil {
		errx.JSON(c, xerr)
		return
	}
	cfg := h.ConfengeService.Config()
	if cfg.AutoSendEnabled {
		errx.JSON(c, errx.New(errx.Forbidden, "inbound receive is refused while auto_send is enabled"))
		return
	}
	if strings.TrimSpace(cfg.InboundWebhookSecret) == "" {
		errx.JSON(c, errx.New(errx.Unauthorized, "inbound webhook secret is not configured"))
		return
	}
	orgID := cfg.InboundOrgID
	if orgID == uuid.Nil {
		orgID = cfg.OperatorOrgID
	}
	if orgID == uuid.Nil {
		errx.JSON(c, errx.New(errx.ServiceUnavailable, "inbound org is not configured"))
		return
	}
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, 1<<20))
	if err != nil {
		errx.JSON(c, errx.New(errx.BadRequest, "unable to read inbound body"))
		return
	}
	sig := firstNonEmptyHeader(c, "X-Warmbly-Signature", "X-Confenge-Signature")
	if !confenge.VerifyOutcomeHMAC(cfg.InboundWebhookSecret, sig, body, time.Now().UTC(), inboundHMACSkew) {
		errx.JSON(c, errx.New(errx.Unauthorized, "invalid inbound signature"))
		return
	}
	if intel.IsSearchObservationEnvelope(body) {
		rec, xerr := h.ConfengeService.IngestSearchObservation(c.Request.Context(), orgID, body, confenge.IngestOptions{
			Now:   time.Now().UTC(),
			Query: c.Request.URL.Query(),
		})
		if xerr != nil {
			errx.JSON(c, xerr)
			return
		}
		status := http.StatusCreated
		if rec.Replay {
			status = http.StatusOK
		}
		c.JSON(status, gin.H{"data": rec})
		return
	}
	if intel.IsCommercialEventEnvelope(body) {
		ev, err := intel.ParseProducerCommercialEvent(body)
		if err != nil {
			errx.JSON(c, errx.New(errx.BadRequest, err.Error()))
			return
		}
		res, xerr := h.ConfengeService.IngestCommercialEvent(c.Request.Context(), orgID, ev)
		if xerr != nil {
			errx.JSON(c, xerr)
			return
		}
		if intel.JoinUnavailable(res) && !res.Replay {
			errx.JSON(c, errx.New(errx.ServiceUnavailable, "commercial event store unavailable"))
			return
		}
		status := http.StatusCreated
		if res.Replay {
			status = http.StatusOK
		}
		c.JSON(status, gin.H{"data": res})
		return
	}
	if confenge.IsNetNewInboundHandraiserEnvelope(body) {
		res, xerr := h.ConfengeService.IngestNetNewInboundHandraiser(c.Request.Context(), orgID, body, time.Now().UTC())
		if xerr != nil {
			errx.JSON(c, xerr)
			return
		}
		status := http.StatusCreated
		if res != nil && res.Replay {
			status = http.StatusOK
		}
		c.JSON(status, gin.H{"data": res})
		return
	}
	// Checked before the inbound-lead fallthrough, which would otherwise
	// swallow both of these: neither carries a lead shape.
	if intel.IsWebIntentEnvelope(body) {
		env, err := intel.ParseWebIntentEnvelope(body)
		if err != nil {
			errx.JSON(c, errx.New(errx.BadRequest, err.Error()))
			return
		}
		res, xerr := h.ConfengeService.IngestWebIntent(c.Request.Context(), orgID, env, time.Now().UTC())
		if xerr != nil {
			errx.JSON(c, xerr)
			return
		}
		c.JSON(http.StatusCreated, gin.H{"data": res})
		return
	}
	if liveintel.IsOfficialLiveIntelligenceBundle(body) {
		errx.JSON(c, errx.NewWithIdentifier(errx.BadRequest, "confenge_opportunity_event_"+liveintel.ReasonEventOfficialBundle,
			"official live-intelligence bundle is not an opportunity event"))
		return
	}
	if liveintel.IsOpportunityEventEnvelope(body) {
		event, err := liveintel.ParseOpportunityEvent(body)
		if err != nil {
			errx.JSON(c, errx.New(errx.BadRequest, err.Error()))
			return
		}
		receipt, xerr := h.ConfengeService.IngestOpportunityEvent(c.Request.Context(), orgID, event, time.Now().UTC())
		if xerr != nil {
			errx.JSON(c, xerr)
			return
		}
		status := http.StatusCreated
		if receipt.Replay {
			status = http.StatusOK
		}
		c.JSON(status, gin.H{"data": receipt})
		return
	}
	res, xerr := h.ConfengeService.IngestInboundLead(c.Request.Context(), orgID, body, confenge.IngestOptions{
		Now:   time.Now().UTC(),
		Query: c.Request.URL.Query(),
	})
	if xerr != nil {
		errx.JSON(c, xerr)
		return
	}
	status := http.StatusCreated
	if res.Duplicate || res.SecondaryDedupe {
		status = http.StatusOK
	}
	c.JSON(status, gin.H{"data": res})
}

// GetConfengeInboundHandraiser — GET /confenge/inbound/handraisers/:logicalId
func (h *Handler) GetConfengeInboundHandraiser(c *gin.Context) {
	orgID, ok := h.confengeOrg(c)
	if !ok {
		return
	}
	logicalID := strings.TrimSpace(c.Param("logicalId"))
	if logicalID == "" {
		errx.JSON(c, errx.New(errx.BadRequest, "logical_id is required"))
		return
	}
	res, xerr := h.ConfengeService.ReadbackNetNewInboundHandraiser(c.Request.Context(), orgID, logicalID)
	if xerr != nil {
		errx.JSON(c, xerr)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": res})
}

// ListConfengeInboundNow — GET /confenge/inbound
func (h *Handler) ListConfengeInboundNow(c *gin.Context) {
	orgID, ok := h.confengeOrg(c)
	if !ok {
		return
	}
	includeSynthetic := c.Query("include_synthetic") == "1"
	items, xerr := h.ConfengeService.CollectInboundNowFiltered(c.Request.Context(), orgID, includeSynthetic)
	if xerr != nil {
		errx.JSON(c, xerr)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": items})
}

// RecordConfengeInboundOutcome — POST /confenge/inbound/:leadId/outcome
func (h *Handler) RecordConfengeInboundOutcome(c *gin.Context) {
	orgID, ok := h.confengeOrg(c)
	if !ok {
		return
	}
	leadID := strings.TrimSpace(c.Param("leadId"))
	if leadID == "" {
		errx.JSON(c, errx.New(errx.BadRequest, "lead_id is required"))
		return
	}
	uid := confengeActorUUID(c)
	var body struct {
		OutcomeCode    string `json:"outcome_code"`
		Notes          string `json:"notes"`
		NextActionType string `json:"next_action_type"`
		NextActionAt   string `json:"next_action_at"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		errx.JSON(c, errx.New(errx.BadRequest, "invalid body"))
		return
	}
	req := confenge.OutcomeRequest{OutcomeCode: body.OutcomeCode, Notes: body.Notes, NextActionType: body.NextActionType, Actor: uid.String()}
	if ts := strings.TrimSpace(body.NextActionAt); ts != "" {
		if parsed, err := time.Parse(time.RFC3339, ts); err == nil {
			req.NextActionAt = &parsed
		}
	}
	res, xerr := h.ConfengeService.RecordInboundOutcome(c.Request.Context(), orgID, uid, leadID, req)
	if xerr != nil {
		errx.JSON(c, xerr)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": res})
}

// AcknowledgeConfengeInboundAlert — POST /confenge/inbound/:leadId/acknowledge
func (h *Handler) AcknowledgeConfengeInboundAlert(c *gin.Context) {
	orgID, ok := h.confengeOrg(c)
	if !ok {
		return
	}
	leadID := strings.TrimSpace(c.Param("leadId"))
	if leadID == "" {
		errx.JSON(c, errx.New(errx.BadRequest, "lead_id is required"))
		return
	}
	uid := confengeActorUUID(c)
	alert, xerr := h.ConfengeService.AcknowledgeInboundAlert(c.Request.Context(), orgID, uid, leadID, time.Now().UTC())
	if xerr != nil {
		errx.JSON(c, xerr)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": alert})
}

// ResolveConfengeInboundNoAction — POST /confenge/inbound/:leadId/resolve
func (h *Handler) ResolveConfengeInboundNoAction(c *gin.Context) {
	orgID, ok := h.confengeOrg(c)
	if !ok {
		return
	}
	leadID := strings.TrimSpace(c.Param("leadId"))
	if leadID == "" {
		errx.JSON(c, errx.New(errx.BadRequest, "lead_id is required"))
		return
	}
	uid := confengeActorUUID(c)
	var body struct {
		Reason string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		errx.JSON(c, errx.New(errx.BadRequest, "invalid body"))
		return
	}
	alert, xerr := h.ConfengeService.ResolveInboundNoAction(c.Request.Context(), orgID, uid, leadID, body.Reason, time.Now().UTC())
	if xerr != nil {
		errx.JSON(c, xerr)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": alert})
}

func confengeActorUUID(c *gin.Context) uuid.UUID {
	if s := strings.TrimSpace(c.GetString("user_id")); s != "" {
		if id, err := uuid.Parse(s); err == nil {
			return id
		}
	}
	if v, ok := c.Get("user_id"); ok {
		if id, ok := v.(uuid.UUID); ok {
			return id
		}
	}
	return uuid.Nil
}

func firstNonEmptyHeader(c *gin.Context, names ...string) string {
	for _, n := range names {
		if v := strings.TrimSpace(c.GetHeader(n)); v != "" {
			return v
		}
	}
	return ""
}
