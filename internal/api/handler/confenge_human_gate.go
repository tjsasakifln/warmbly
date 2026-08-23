package handler

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/api/middleware"
	"github.com/warmbly/warmbly/internal/app/confenge"
	"github.com/warmbly/warmbly/internal/errx"
)

func humanGateIDs(c *gin.Context) (uuid.UUID, uuid.UUID, bool) {
	versionID, e1 := uuid.Parse(strings.TrimSpace(c.Param("id")))
	candidateID := uuid.Nil
	var e2 error
	if raw := strings.TrimSpace(c.Param("candidateId")); raw != "" {
		candidateID, e2 = uuid.Parse(raw)
	}
	if e1 != nil || e2 != nil || versionID == uuid.Nil {
		errx.JSON(c, errx.NewWithIdentifier(errx.BadRequest, "invalid_resource_id", "cohort or candidate id is invalid"))
		return uuid.Nil, uuid.Nil, false
	}
	return versionID, candidateID, true
}

func humanGateActor(c *gin.Context) (uuid.UUID, bool) {
	id, err := middleware.GetUserUUID(c)
	if err != nil || id == uuid.Nil {
		errx.JSON(c, errx.ErrUnauthorized)
		return uuid.Nil, false
	}
	return id, true
}

func humanGateMeta(c *gin.Context, reason []string) gin.H {
	return gin.H{"contract_version": confenge.HumanGateContractV1, "source": confenge.HumanGateSource, "as_of": time.Now().UTC(), "freshness": "SERVER_OBSERVED", "policy_version": confenge.BoundedCohortPolicyV1, "reason": reason, "correlation_id": c.GetString("request_id")}
}

func (h *Handler) ListConfengeHumanGateCohorts(c *gin.Context) {
	orgID, ok := h.confengeOrg(c)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "25"))
	var cursor time.Time
	if raw := c.Query("cursor"); raw != "" {
		cursor, _ = time.Parse(time.RFC3339Nano, raw)
	}
	list, x := h.ConfengeService.ListHumanGateCohorts(c.Request.Context(), orgID, limit, cursor, time.Now().UTC())
	if x != nil {
		errx.JSON(c, x)
		return
	}
	next := ""
	if len(list) == limit && len(list) > 0 {
		next = list[len(list)-1].CreatedAt.Format(time.RFC3339Nano)
	}
	c.JSON(http.StatusOK, gin.H{"data": list, "pagination": gin.H{"next_cursor": next, "has_more": next != ""}, "meta": humanGateMeta(c, nil)})
}

func (h *Handler) CreateConfengeHumanGateCohort(c *gin.Context) {
	orgID, ok := h.confengeOrg(c)
	if !ok {
		return
	}
	actor, ok := humanGateActor(c)
	if !ok {
		return
	}
	var in confenge.HumanGateCreateInput
	if c.ShouldBindJSON(&in) != nil {
		errx.JSON(c, errx.NewWithIdentifier(errx.BadRequest, "invalid_payload", "JSON body is invalid"))
		return
	}
	in.IdempotencyKey = strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	in.CorrelationID = c.GetString("request_id")
	v, x := h.ConfengeService.CreateHumanGateCohort(c.Request.Context(), orgID, actor, in)
	if x != nil {
		errx.JSON(c, x)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"data": v, "meta": humanGateMeta(c, v.Reason), "receipt": v.Receipt})
}

func (h *Handler) ReproduceConfengeHumanGateCohort(c *gin.Context) {
	orgID, ok := h.confengeOrg(c)
	if !ok {
		return
	}
	actor, ok := humanGateActor(c)
	if !ok {
		return
	}
	id, _, ok := humanGateIDs(c)
	if !ok {
		return
	}
	in := confenge.HumanGateCreateInput{IdempotencyKey: strings.TrimSpace(c.GetHeader("Idempotency-Key")), CorrelationID: c.GetString("request_id")}
	v, x := h.ConfengeService.ReproduceHumanGateCohort(c.Request.Context(), orgID, actor, id, in)
	if x != nil {
		errx.JSON(c, x)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"data": v, "meta": humanGateMeta(c, v.Reason), "receipt": v.Receipt})
}

func (h *Handler) GetConfengeHumanGateCohort(c *gin.Context) {
	orgID, ok := h.confengeOrg(c)
	if !ok {
		return
	}
	id, _, ok := humanGateIDs(c)
	if !ok {
		return
	}
	v, x := h.ConfengeService.GetHumanGateCohort(c.Request.Context(), orgID, id, time.Now().UTC())
	if x != nil {
		errx.JSON(c, x)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": v, "meta": humanGateMeta(c, v.Reason), "receipt": v.Receipt})
}

func (h *Handler) GetConfengeHumanGateCandidate(c *gin.Context) {
	orgID, ok := h.confengeOrg(c)
	if !ok {
		return
	}
	versionID, candidateID, ok := humanGateIDs(c)
	if !ok {
		return
	}
	v, x := h.ConfengeService.GetHumanGateCohort(c.Request.Context(), orgID, versionID, time.Now().UTC())
	if x != nil {
		errx.JSON(c, x)
		return
	}
	for _, candidate := range v.Candidates {
		if candidate.CandidateID == candidateID {
			c.JSON(http.StatusOK, gin.H{"data": candidate, "meta": humanGateMeta(c, candidate.BlockedBy), "receipt": v.Receipt})
			return
		}
	}
	errx.JSON(c, errx.NewWithIdentifier(errx.NotFound, "candidate_not_found", "candidate is not in this immutable version"))
}

func (h *Handler) ValidateConfengeHumanGateCandidate(c *gin.Context) {
	orgID, ok := h.confengeOrg(c)
	if !ok {
		return
	}
	actor, ok := humanGateActor(c)
	if !ok {
		return
	}
	versionID, candidateID, ok := humanGateIDs(c)
	if !ok {
		return
	}
	if h.EmailVerifyService == nil {
		errx.JSON(c, errx.NewWithIdentifier(errx.ServiceUnavailable, "email_verifier_unavailable", "email verifier is unavailable"))
		return
	}
	v, x := h.ConfengeService.GetHumanGateCohort(c.Request.Context(), orgID, versionID, time.Now().UTC())
	if x != nil {
		errx.JSON(c, x)
		return
	}
	mailbox := ""
	for _, candidate := range v.Candidates {
		if candidate.CandidateID == candidateID {
			mailbox = candidate.Mailbox
			break
		}
	}
	if mailbox == "" {
		errx.JSON(c, errx.NewWithIdentifier(errx.NotFound, "candidate_not_found", "candidate is not in this immutable version"))
		return
	}
	result := h.EmailVerifyService.VerifyAddress(c.Request.Context(), mailbox)
	updated, x := h.ConfengeService.RecordHumanGateValidation(c.Request.Context(), orgID, actor, versionID, candidateID, result, strings.TrimSpace(c.GetHeader("Idempotency-Key")), c.GetString("request_id"))
	if x != nil {
		errx.JSON(c, x)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": updated, "meta": humanGateMeta(c, updated.Reason), "receipt": updated.Receipt})
}

func (h *Handler) ReviewConfengeHumanGateCandidate(c *gin.Context) {
	orgID, ok := h.confengeOrg(c)
	if !ok {
		return
	}
	actor, ok := humanGateActor(c)
	if !ok {
		return
	}
	versionID, candidateID, ok := humanGateIDs(c)
	if !ok {
		return
	}
	var in confenge.HumanGateReviewInput
	if c.ShouldBindJSON(&in) != nil {
		errx.JSON(c, errx.NewWithIdentifier(errx.BadRequest, "invalid_payload", "JSON body is invalid"))
		return
	}
	in.IdempotencyKey = strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	in.CorrelationID = c.GetString("request_id")
	v, x := h.ConfengeService.ReviewHumanGateCandidate(c.Request.Context(), orgID, actor, versionID, candidateID, in)
	if x != nil {
		errx.JSON(c, x)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": v, "meta": humanGateMeta(c, v.Reason), "receipt": v.Receipt})
}

func (h *Handler) DecideConfengeHumanGateCohort(c *gin.Context) {
	orgID, ok := h.confengeOrg(c)
	if !ok {
		return
	}
	actor, ok := humanGateActor(c)
	if !ok {
		return
	}
	versionID, _, ok := humanGateIDs(c)
	if !ok {
		return
	}
	var in confenge.HumanGateDecisionInput
	if c.ShouldBindJSON(&in) != nil {
		errx.JSON(c, errx.NewWithIdentifier(errx.BadRequest, "invalid_payload", "JSON body is invalid"))
		return
	}
	in.IdempotencyKey = strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	in.CorrelationID = c.GetString("request_id")
	v, x := h.ConfengeService.DecideHumanGateCohort(c.Request.Context(), orgID, actor, versionID, in)
	if x != nil {
		errx.JSON(c, x)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": v, "meta": humanGateMeta(c, v.Reason), "receipt": v.Receipt})
}
