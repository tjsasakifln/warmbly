package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/api/middleware"
	"github.com/warmbly/warmbly/internal/app/confenge"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/repository"
)

// confengeOrg resolves the org when CONFENGE outreach is wired and enabled.
func (h *Handler) confengeOrg(c *gin.Context) (uuid.UUID, bool) {
	if h.ConfengeService == nil || !h.ConfengeService.Enabled() {
		errx.JSON(c, errx.New(errx.NotFound, "CONFENGE outreach is not enabled on this server"))
		return uuid.Nil, false
	}
	orgID := middleware.GetOrganizationID(c)
	if orgID == nil {
		errx.JSON(c, errx.New(errx.BadRequest, "no organization selected"))
		return uuid.Nil, false
	}
	return *orgID, true
}

// GetConfengeStatus — GET /confenge/status
// Reports whether the feature is on (always 200 when authenticated with org).
func (h *Handler) GetConfengeStatus(c *gin.Context) {
	enabled := h.ConfengeService != nil && h.ConfengeService.Enabled()
	cfg := confenge.Config{}
	if h.ConfengeService != nil {
		cfg = h.ConfengeService.Config()
	}
	c.JSON(http.StatusOK, gin.H{
		"enabled":                 enabled,
		"auto_send_enabled":       enabled && cfg.AutoSendEnabled,
		"require_human_approval":  cfg.RequireHumanApproval,
		"default_daily_limit":     cfg.DefaultDailyLimit,
		"max_initial_email_words": cfg.MaxInitialEmailWords,
		"feed_configured":         enabled && cfg.FeedURL != "",
	})
}

// GetConfengeSummary — GET /confenge/summary
func (h *Handler) GetConfengeSummary(c *gin.Context) {
	orgID, ok := h.confengeOrg(c)
	if !ok {
		return
	}
	sum, xerr := h.ConfengeService.Summary(c.Request.Context(), orgID)
	if xerr != nil {
		errx.JSON(c, xerr)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": sum})
}

// ListConfengeAccounts — GET /confenge/accounts
func (h *Handler) ListConfengeAccounts(c *gin.Context) {
	orgID, ok := h.confengeOrg(c)
	if !ok {
		return
	}
	filter := repository.OutreachAccountFilter{
		QueueState: c.Query("queue_state"),
		CNPJ14:     c.Query("cnpj14"),
		Search:     c.Query("q"),
	}
	if raw := c.Query("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 200 {
			errx.JSON(c, errx.New(errx.BadRequest, "limit must be between 1 and 200"))
			return
		}
		filter.Limit = n
	}
	if raw := c.Query("offset"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			errx.JSON(c, errx.New(errx.BadRequest, "invalid offset"))
			return
		}
		filter.Offset = n
	}
	list, xerr := h.ConfengeService.ListAccounts(c.Request.Context(), orgID, filter)
	if xerr != nil {
		errx.JSON(c, xerr)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": list})
}

// GetConfengeAccount — GET /confenge/accounts/:id
func (h *Handler) GetConfengeAccount(c *gin.Context) {
	orgID, ok := h.confengeOrg(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		errx.JSON(c, errx.ErrUuid)
		return
	}
	acc, xerr := h.ConfengeService.GetAccount(c.Request.Context(), orgID, id)
	if xerr != nil {
		errx.JSON(c, xerr)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": acc})
}

// BlockConfengeAccount — POST /confenge/accounts/:id/block
func (h *Handler) BlockConfengeAccount(c *gin.Context) {
	orgID, ok := h.confengeOrg(c)
	if !ok {
		return
	}
	userID, err := middleware.GetUserUUID(c)
	if err != nil {
		errx.JSON(c, errx.ErrUnauthorized)
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		errx.JSON(c, errx.ErrUuid)
		return
	}
	var body struct {
		Reason       string `json:"reason"`
		DoNotContact bool   `json:"do_not_contact"`
	}
	if err := c.ShouldBindJSON(&body); err != nil && err.Error() != "EOF" {
		// empty body is fine
		if c.Request.ContentLength != 0 {
			errx.JSON(c, errx.ErrInvalid)
			return
		}
	}
	acc, xerr := h.ConfengeService.BlockAccount(c.Request.Context(), orgID, userID, id, body.Reason, body.DoNotContact)
	if xerr != nil {
		errx.JSON(c, xerr)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": acc})
}

// ImportConfengeFeed — POST /confenge/import
// Accepts a raw feed body (native confenge.outreach.v1 or legacy) or
// {"uri":"https://...","dry_run":true}. Honour Idempotency-Key.
func (h *Handler) ImportConfengeFeed(c *gin.Context) {
	orgID, ok := h.confengeOrg(c)
	if !ok {
		return
	}
	var userPtr *uuid.UUID
	if uid, err := middleware.GetUserUUID(c); err == nil {
		userPtr = &uid
	}
	opts := confenge.ImportOptions{
		DryRun:         queryBool(c, "dry_run"),
		IdempotencyKey: strings.TrimSpace(c.GetHeader("Idempotency-Key")),
	}

	raw, err := io.ReadAll(io.LimitReader(c.Request.Body, 33<<20))
	if err != nil {
		errx.JSON(c, errx.New(errx.BadRequest, "failed to read body"))
		return
	}
	if len(raw) == 0 {
		// Empty body: import from configured feed URL.
		run, xerr := h.ConfengeService.ImportFromURI(c.Request.Context(), orgID, userPtr, "", opts)
		if xerr != nil {
			errx.JSON(c, xerr)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": run})
		return
	}

	// Envelope: fetch by URI without embedding the full feed.
	var env struct {
		URI           string          `json:"uri"`
		DryRun        *bool           `json:"dry_run"`
		SchemaVersion string          `json:"schema_version"`
		Leads         json.RawMessage `json:"leads"`
	}
	if err := json.Unmarshal(raw, &env); err == nil && env.URI != "" && env.SchemaVersion == "" && len(env.Leads) == 0 {
		if env.DryRun != nil {
			opts.DryRun = *env.DryRun
		}
		run, xerr := h.ConfengeService.ImportFromURI(c.Request.Context(), orgID, userPtr, env.URI, opts)
		if xerr != nil {
			errx.JSON(c, xerr)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": run})
		return
	}

	run, xerr := h.ConfengeService.ImportFromBytes(c.Request.Context(), orgID, userPtr, raw, opts)
	if xerr != nil {
		errx.JSON(c, xerr)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": run})
}

// ListConfengeImportRuns — GET /confenge/import-runs
func (h *Handler) ListConfengeImportRuns(c *gin.Context) {
	orgID, ok := h.confengeOrg(c)
	if !ok {
		return
	}
	limit := 20
	if raw := c.Query("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}
	list, xerr := h.ConfengeService.ListImportRuns(c.Request.Context(), orgID, limit)
	if xerr != nil {
		errx.JSON(c, xerr)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": list})
}

// GetConfengeImportRun — GET /confenge/import-runs/:id
func (h *Handler) GetConfengeImportRun(c *gin.Context) {
	orgID, ok := h.confengeOrg(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		errx.JSON(c, errx.ErrUuid)
		return
	}
	run, xerr := h.ConfengeService.GetImportRun(c.Request.Context(), orgID, id)
	if xerr != nil {
		errx.JSON(c, xerr)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": run})
}

func queryBool(c *gin.Context, key string) bool {
	v := strings.ToLower(strings.TrimSpace(c.Query(key)))
	return v == "1" || v == "true" || v == "yes"
}

// ListConfengeDrafts — GET /confenge/drafts
func (h *Handler) ListConfengeDrafts(c *gin.Context) {
	orgID, ok := h.confengeOrg(c)
	if !ok {
		return
	}
	limit, offset := 50, 0
	if raw := c.Query("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	if raw := c.Query("offset"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 0 {
			offset = n
		}
	}
	list, xerr := h.ConfengeService.ListDrafts(c.Request.Context(), orgID, c.Query("status"), limit, offset)
	if xerr != nil {
		errx.JSON(c, xerr)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": list})
}

// GetConfengeDraft — GET /confenge/drafts/:id
func (h *Handler) GetConfengeDraft(c *gin.Context) {
	orgID, ok := h.confengeOrg(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		errx.JSON(c, errx.ErrUuid)
		return
	}
	d, xerr := h.ConfengeService.GetDraft(c.Request.Context(), orgID, id)
	if xerr != nil {
		errx.JSON(c, xerr)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": d})
}

// GenerateConfengeDraft — POST /confenge/accounts/:id/generate
func (h *Handler) GenerateConfengeDraft(c *gin.Context) {
	orgID, ok := h.confengeOrg(c)
	if !ok {
		return
	}
	userID, err := middleware.GetUserUUID(c)
	if err != nil {
		errx.JSON(c, errx.ErrUnauthorized)
		return
	}
	accountID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		errx.JSON(c, errx.ErrUuid)
		return
	}
	var body struct {
		ContactCandidateID *uuid.UUID `json:"contact_candidate_id"`
	}
	_ = c.ShouldBindJSON(&body)
	d, xerr := h.ConfengeService.GenerateDraft(c.Request.Context(), orgID, userID, accountID, body.ContactCandidateID)
	if xerr != nil {
		errx.JSON(c, xerr)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": d})
}

// ReviewConfengeDraft — POST /confenge/drafts/:id/review
// Body: {"action":"approve|reject|skip|edit|block", "subject":"...", "body_text":"...", "do_not_contact":false}
func (h *Handler) ReviewConfengeDraft(c *gin.Context) {
	orgID, ok := h.confengeOrg(c)
	if !ok {
		return
	}
	userID, err := middleware.GetUserUUID(c)
	if err != nil {
		errx.JSON(c, errx.ErrUnauthorized)
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		errx.JSON(c, errx.ErrUuid)
		return
	}
	var body struct {
		Action       string  `json:"action" binding:"required"`
		Subject      *string `json:"subject"`
		BodyText     *string `json:"body_text"`
		Reason       *string `json:"reason"`
		DoNotContact bool    `json:"do_not_contact"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		errx.JSON(c, errx.ErrInvalid)
		return
	}
	edit := &confenge.DraftEdit{
		Subject: body.Subject, BodyText: body.BodyText,
		Reason: body.Reason, DoNotContact: body.DoNotContact,
	}
	d, xerr := h.ConfengeService.ReviewDraft(c.Request.Context(), orgID, userID, id, body.Action, edit)
	if xerr != nil {
		errx.JSON(c, xerr)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": d})
}

// BootstrapConfengeCampaign — POST /confenge/campaign/bootstrap
func (h *Handler) BootstrapConfengeCampaign(c *gin.Context) {
	orgID, ok := h.confengeOrg(c)
	if !ok {
		return
	}
	userID, err := middleware.GetUserUUID(c)
	if err != nil {
		errx.JSON(c, errx.ErrUnauthorized)
		return
	}
	camp, xerr := h.ConfengeService.BootstrapCampaign(c.Request.Context(), orgID, userID)
	if xerr != nil {
		errx.JSON(c, xerr)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": camp})
}

// EnrollConfengeDraft — POST /confenge/drafts/:id/enroll
func (h *Handler) EnrollConfengeDraft(c *gin.Context) {
	orgID, ok := h.confengeOrg(c)
	if !ok {
		return
	}
	userID, err := middleware.GetUserUUID(c)
	if err != nil {
		errx.JSON(c, errx.ErrUnauthorized)
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		errx.JSON(c, errx.ErrUuid)
		return
	}
	d, xerr := h.ConfengeService.EnrollDraft(c.Request.Context(), orgID, userID, id)
	if xerr != nil {
		errx.JSON(c, xerr)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": d})
}

// BootstrapConfengePipeline — POST /confenge/crm/bootstrap
func (h *Handler) BootstrapConfengePipeline(c *gin.Context) {
	orgID, ok := h.confengeOrg(c)
	if !ok {
		return
	}
	pipe, xerr := h.ConfengeService.BootstrapPipeline(c.Request.Context(), orgID)
	if xerr != nil {
		errx.JSON(c, xerr)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": pipe})
}

// BatchApproveConfengeDrafts — POST /confenge/drafts/batch-approve
// Body: {"draft_ids":["uuid",...]} — only GREEN / validated / enrollable items.
func (h *Handler) BatchApproveConfengeDrafts(c *gin.Context) {
	orgID, ok := h.confengeOrg(c)
	if !ok {
		return
	}
	userID, err := middleware.GetUserUUID(c)
	if err != nil {
		errx.JSON(c, errx.ErrUnauthorized)
		return
	}
	var body struct {
		DraftIDs []string `json:"draft_ids"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		errx.JSON(c, errx.ErrInvalid)
		return
	}
	ids := make([]uuid.UUID, 0, len(body.DraftIDs))
	for _, s := range body.DraftIDs {
		id, err := uuid.Parse(s)
		if err != nil {
			errx.JSON(c, errx.New(errx.BadRequest, "invalid draft_id"))
			return
		}
		ids = append(ids, id)
	}
	res, xerr := h.ConfengeService.BatchApproveDrafts(c.Request.Context(), orgID, userID, ids)
	if xerr != nil {
		errx.JSON(c, xerr)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": res})
}
