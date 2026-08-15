package handler

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/warmbly/warmbly/internal/app/confenge/intel"
	"github.com/warmbly/warmbly/internal/errx"
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

// ListConfengeIntelExceptions — GET /confenge/intel/exceptions
func (h *Handler) ListConfengeIntelExceptions(c *gin.Context) {
	orgID, ok := h.confengeOrg(c)
	if !ok {
		return
	}
	xs, xerr := h.ConfengeService.ListIntelExceptions(c.Request.Context(), orgID)
	if xerr != nil {
		errx.JSON(c, xerr)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": xs})
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
