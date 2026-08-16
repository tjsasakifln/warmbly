package handler

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/api/middleware"
	"github.com/warmbly/warmbly/internal/app/confenge/intel"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
)

// GetConfengeExecutiveIntel — GET /confenge/intel/executive
// Monthly commercial-intelligence query. Not a CRM board.
func (h *Handler) GetConfengeExecutiveIntel(c *gin.Context) {
	orgID, ok := h.confengeOrg(c)
	if !ok {
		return
	}
	month := strings.TrimSpace(c.Query("month"))
	includeSynthetic := queryBool(c, "include_synthetic")
	view, xerr := h.ConfengeService.CommercialExecutiveView(c.Request.Context(), orgID, month, includeSynthetic)
	if xerr != nil {
		errx.JSON(c, xerr)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": view})
}

func intelExceptionFilter(c *gin.Context) intel.ExceptionFilter {
	f := intel.ExceptionFilter{
		Type:     firstNonEmptyQuery(c, "type", "code"),
		Lane:     strings.TrimSpace(c.Query("lane")),
		Source:   strings.TrimSpace(c.Query("source")),
		Severity: strings.TrimSpace(c.Query("severity")),
		Status:   strings.TrimSpace(c.Query("status")),
	}
	if v := strings.TrimSpace(c.Query("age_min_seconds")); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			f.AgeMin = n
		}
	}
	if v := strings.TrimSpace(c.Query("age_max_seconds")); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			f.AgeMax = n
		}
	}
	return f
}

func firstNonEmptyQuery(c *gin.Context, keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(c.Query(k)); v != "" {
			return v
		}
	}
	return ""
}

// ListConfengeIntelExceptions — GET /confenge/intel/exceptions
func (h *Handler) ListConfengeIntelExceptions(c *gin.Context) {
	orgID, ok := h.confengeOrg(c)
	if !ok {
		return
	}
	xs, xerr := h.ConfengeService.ListIntelExceptions(c.Request.Context(), orgID, intelExceptionFilter(c))
	if xerr != nil {
		errx.JSON(c, xerr)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": xs})
}

// GetConfengeIntelException — GET /confenge/intel/exceptions/:id
func (h *Handler) GetConfengeIntelException(c *gin.Context) {
	orgID, ok := h.confengeOrg(c)
	if !ok {
		return
	}
	ex, xerr := h.ConfengeService.GetIntelException(c.Request.Context(), orgID, c.Param("id"))
	if xerr != nil {
		errx.JSON(c, xerr)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": ex})
}

// ResolveConfengeIntelException — POST /confenge/intel/exceptions/:id/resolve
func (h *Handler) ResolveConfengeIntelException(c *gin.Context) {
	orgID, ok := h.confengeOrg(c)
	if !ok {
		return
	}
	var body intel.ResolveRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		errx.JSON(c, errx.New(errx.BadRequest, "invalid body"))
		return
	}
	if strings.TrimSpace(body.Actor) == "" {
		if uid, err := middleware.GetUserUUID(c); err == nil {
			body.Actor = uid.String()
		}
	}
	if strings.TrimSpace(body.IdempotencyKey) == "" {
		body.IdempotencyKey = strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	}
	res, xerr := h.ConfengeService.ResolveIntelException(c.Request.Context(), orgID, c.Param("id"), body)
	if xerr != nil {
		errx.JSON(c, xerr)
		return
	}
	if h.AuditService != nil {
		if parsed, err := uuid.Parse(res.Exception.ID); err == nil {
			h.auditOrg(c, models.AuditActionUpdate, models.AuditEntityOutreachIntelException, &parsed,
				map[string]string{
					"before_status": res.Before.Status,
					"after_status":  res.After.Status,
				},
				map[string]string{
					"action": res.Action,
					"reason": res.Reason,
					"replay": strconv.FormatBool(res.Replay),
				})
		}
	}
	c.JSON(http.StatusOK, gin.H{"data": res})
}

// RecordConfengeIntelLearning — POST /confenge/intel/learning
func (h *Handler) RecordConfengeIntelLearning(c *gin.Context) {
	orgID, ok := h.confengeOrg(c)
	if !ok {
		return
	}
	var body intel.LearningInput
	if err := c.ShouldBindJSON(&body); err != nil {
		errx.JSON(c, errx.New(errx.BadRequest, "invalid body"))
		return
	}
	cand, xerr := h.ConfengeService.RecordIntelLearning(c.Request.Context(), orgID, body)
	if xerr != nil {
		errx.JSON(c, xerr)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"data": cand})
}

// IngestConfengeIntelEvent — POST /confenge/intel/events
// Versioned commercial envelope. Same event_id is a replay.
func (h *Handler) IngestConfengeIntelEvent(c *gin.Context) {
	orgID, ok := h.confengeOrg(c)
	if !ok {
		return
	}
	var ev intel.CommercialEvent
	if err := c.ShouldBindJSON(&ev); err != nil {
		errx.JSON(c, errx.New(errx.BadRequest, "invalid commercial event"))
		return
	}
	res, xerr := h.ConfengeService.IngestCommercialEvent(c.Request.Context(), orgID, ev)
	if xerr != nil {
		errx.JSON(c, xerr)
		return
	}
	status := http.StatusCreated
	if res.Replay {
		status = http.StatusOK
	}
	c.JSON(status, gin.H{"data": res})
}

// GetConfengeIntelReport — GET /confenge/intel/report
func (h *Handler) GetConfengeIntelReport(c *gin.Context) {
	orgID, ok := h.confengeOrg(c)
	if !ok {
		return
	}
	month := strings.TrimSpace(c.Query("month"))
	includeSynthetic := queryBool(c, "include_synthetic")
	rep, xerr := h.ConfengeService.CommercialIntelReport(c.Request.Context(), orgID, month, includeSynthetic)
	if xerr != nil {
		errx.JSON(c, xerr)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": rep})
}
