package handler

import (
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/app/confenge"
	"github.com/warmbly/warmbly/internal/errx"
)

const inboundHMACSkew = 5 * time.Minute

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

// ListConfengeInboundNow — GET /confenge/inbound
func (h *Handler) ListConfengeInboundNow(c *gin.Context) {
	orgID, ok := h.confengeOrg(c)
	if !ok {
		return
	}
	items, xerr := h.ConfengeService.CollectInboundNow(c.Request.Context(), orgID)
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
	userID, _ := c.Get("user_id")
	uid, _ := userID.(uuid.UUID)
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

func firstNonEmptyHeader(c *gin.Context, names ...string) string {
	for _, n := range names {
		if v := strings.TrimSpace(c.GetHeader(n)); v != "" {
			return v
		}
	}
	return ""
}
