package handler

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"github.com/warmbly/warmbly/internal/app/whatsapp"
	"github.com/warmbly/warmbly/internal/app/whatsapp/evolution"
	"github.com/warmbly/warmbly/internal/models"
)

// EvolutionWebhook is the public ingress for Evolution API events.
// POST /api/v1/webhooks/evolution/:instance
//
// Auth: Authorization Bearer <secret> or X-Webhook-Secret, matched against
// the instance's stored secret (or WHATSAPP_WEBHOOK_SECRET when single-tenant).
// Body is size-limited; duplicates are idempotent on provider event id.
func (h *Handler) EvolutionWebhook(c *gin.Context) {
	if h.WhatsAppService == nil || !h.WhatsAppService.Config().Enabled {
		c.JSON(http.StatusNotFound, gin.H{"error": "whatsapp channel disabled"})
		return
	}
	cfg := h.WhatsAppService.Config()
	instance := strings.TrimSpace(c.Param("instance"))
	if instance == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "instance required", "code": "missing_instance"})
		return
	}

	maxBytes := cfg.MaxWebhookBytes
	if maxBytes <= 0 {
		maxBytes = whatsapp.DefaultMaxWebhookBytes
	}
	// Prefer Content-Length check before reading.
	if c.Request.ContentLength > maxBytes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "payload too large", "code": "payload_too_large"})
		return
	}

	secret := cfg.WebhookSecret
	var orgID uuid.UUID
	if h.WhatsAppRepo != nil {
		inst, xerr := h.WhatsAppRepo.GetInstanceByName(c.Request.Context(), whatsapp.ProviderEvolution, instance)
		if xerr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "lookup failed", "code": "internal"})
			return
		}
		if inst == nil {
			// Fall back to configured default instance only.
			if cfg.EvolutionInstance == "" || !strings.EqualFold(instance, cfg.EvolutionInstance) {
				c.JSON(http.StatusNotFound, gin.H{"error": "instance not mapped", "code": "instance_mismatch"})
				return
			}
			// Single-tenant: org mapping deferred; require global secret.
		} else {
			orgID = inst.OrganizationID
			if inst.WebhookSecret != "" {
				secret = inst.WebhookSecret
			}
			// Baileys mode must not be used in production; warn only.
			if inst.IntegrationMode == "WHATSAPP-BAILEYS" && cfg.IsProduction() {
				log.Error().Str("instance", instance).Msg("rejecting baileys instance webhook in production")
				c.JSON(http.StatusForbidden, gin.H{"error": "baileys disabled in production", "code": "baileys_forbidden"})
				return
			}
		}
	} else if cfg.EvolutionInstance != "" && !strings.EqualFold(instance, cfg.EvolutionInstance) {
		c.JSON(http.StatusNotFound, gin.H{"error": "instance not mapped", "code": "instance_mismatch"})
		return
	}

	auth := whatsapp.WebhookAuth{Secret: secret, InstanceAllow: "", MaxBytes: maxBytes}
	if err := auth.ValidateHeaders(
		c.GetHeader("Authorization"),
		c.GetHeader("X-Webhook-Secret"),
		c.GetHeader("Content-Type"),
		c.Request.ContentLength,
	); err != nil {
		if ae, ok := err.(*whatsapp.AuthError); ok {
			c.JSON(ae.StatusCode, gin.H{"error": ae.Message, "code": ae.Code})
			return
		}
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized", "code": "unauthorized"})
		return
	}

	body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxBytes+1))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "read body failed", "code": "bad_body"})
		return
	}
	if int64(len(body)) > maxBytes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "payload too large", "code": "payload_too_large"})
		return
	}

	ev, err := evolution.NormalizeWebhook(body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "malformed payload", "code": "malformed"})
		return
	}
	if ev.Instance == "" {
		ev.Instance = instance
	}
	if !strings.EqualFold(ev.Instance, instance) && ev.Instance != "" {
		// Path instance is authoritative for mapping.
		ev.Instance = instance
	}
	if ev.EventType == whatsapp.EventUnsupported {
		c.JSON(http.StatusOK, gin.H{"received": true, "ignored": true})
		return
	}

	// Persist idempotency when org is known.
	if orgID != uuid.Nil && h.WhatsAppRepo != nil {
		sum := sha256.Sum256(body)
		inserted, xerr := h.WhatsAppRepo.InsertWebhookEvent(
			c.Request.Context(), orgID, whatsapp.ProviderEvolution,
			ev.IdempotencyKey(), ev.EventType, ev.ExternalMessageID, hex.EncodeToString(sum[:]),
		)
		if xerr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "idempotency failed", "code": "internal"})
			return
		}
		if !inserted {
			c.JSON(http.StatusOK, gin.H{"received": true, "duplicate": true})
			return
		}
		ev.OrganizationID = orgID

		// Load or create contact channel state by phone.
		phone := ev.FromE164
		if phone == "" {
			phone = ev.ToE164
		}
		var state *models.WhatsAppContactState
		if phone != "" {
			state, _ = h.WhatsAppRepo.GetContactStateByPhone(c.Request.Context(), orgID, phone)
		}
		domainState := modelsToDomainState(state, orgID)
		if domainState.PhoneE164 == "" {
			domainState.PhoneE164 = phone
		}

		inRes, _ := h.WhatsAppService.ProcessInbound(c.Request.Context(), &domainState, ev)
		if inRes.Duplicate {
			c.JSON(http.StatusOK, gin.H{"received": true, "duplicate": true})
			return
		}

		// Persist inbound message.
		if ev.EventType == whatsapp.EventMessageReceived {
			msg := &models.WhatsAppMessage{
				OrganizationID:    orgID,
				ThreadKey:         phone,
				Direction:         "inbound",
				Channel:           whatsapp.ChannelWhatsApp,
				Provider:          whatsapp.ProviderEvolution,
				ProviderMessageID: ev.ExternalMessageID,
				IdempotencyKey:    ev.IdempotencyKey(),
				BodyText:          ev.Content.Text,
				Status:            "received",
				OccurredAt:        ev.OccurredAt,
			}
			if state != nil && state.ContactID != nil {
				msg.ContactID = state.ContactID
			}
			_, _ = h.WhatsAppRepo.InsertMessage(c.Request.Context(), msg)

			// Persist updated consent / service window.
			persistState := domainToModelsState(domainState, state)
			persistState.OrganizationID = orgID
			if state != nil {
				persistState.ID = state.ID
				persistState.ContactID = state.ContactID
			}
			_ = h.WhatsAppRepo.UpsertContactState(c.Request.Context(), &persistState)
		}

		c.JSON(http.StatusOK, gin.H{
			"received":       true,
			"event_type":     ev.EventType,
			"stop_sequences": inRes.StopSequences,
			"needs_review":   inRes.NeedsHumanReview,
			"opt_out":        inRes.OptOut.Matched && inRes.OptOut.Confident,
		})
		return
	}

	// No org mapping: still ack valid authenticated events (single-node lab).
	// In-process idempotency only.
	var domainState whatsapp.ContactChannelState
	inRes, _ := h.WhatsAppService.ProcessInbound(c.Request.Context(), &domainState, ev)
	c.JSON(http.StatusOK, gin.H{
		"received":   true,
		"event_type": ev.EventType,
		"duplicate":  inRes.Duplicate,
		"lab_mode":   true,
	})
}

func modelsToDomainState(st *models.WhatsAppContactState, orgID uuid.UUID) whatsapp.ContactChannelState {
	out := whatsapp.ContactChannelState{
		OrganizationID: orgID,
		ConsentStatus:  whatsapp.ConsentUnknown,
	}
	if st == nil {
		return out
	}
	if st.ContactID != nil {
		out.ContactID = *st.ContactID
	}
	out.PhoneE164 = st.PhoneE164
	out.PhoneSource = st.PhoneSource
	out.PhoneSourceURL = st.PhoneSourceURL
	out.PhoneVerifiedAt = st.PhoneVerifiedAt
	out.ConsentStatus = st.ConsentStatus
	out.ConsentSource = st.ConsentSource
	out.ConsentAt = st.ConsentAt
	out.ConsentScope = st.ConsentScope
	out.ConsentProvenanceOK = st.ConsentProvenanceOK
	out.LastInboundAt = st.LastInboundAt
	out.ServiceWindowUntil = st.ServiceWindowUntil
	out.ChannelStatus = st.ChannelStatus
	out.OptOutAt = st.OptOutAt
	out.DoNotContact = st.DoNotContact
	out.LastEmailOutboundAt = st.LastEmailOutboundAt
	out.LastWhatsAppOutboundAt = st.LastWhatsAppOutboundAt
	return out
}

func domainToModelsState(d whatsapp.ContactChannelState, prev *models.WhatsAppContactState) models.WhatsAppContactState {
	out := models.WhatsAppContactState{
		OrganizationID:         d.OrganizationID,
		PhoneE164:              d.PhoneE164,
		PhoneSource:            d.PhoneSource,
		PhoneSourceURL:         d.PhoneSourceURL,
		PhoneVerifiedAt:        d.PhoneVerifiedAt,
		ConsentStatus:          d.ConsentStatus,
		ConsentSource:          d.ConsentSource,
		ConsentAt:              d.ConsentAt,
		ConsentScope:           d.ConsentScope,
		ConsentProvenanceOK:    d.ConsentProvenanceOK,
		LastInboundAt:          d.LastInboundAt,
		ServiceWindowUntil:     d.ServiceWindowUntil,
		ChannelStatus:          d.ChannelStatus,
		OptOutAt:               d.OptOutAt,
		DoNotContact:           d.DoNotContact,
		LastEmailOutboundAt:    d.LastEmailOutboundAt,
		LastWhatsAppOutboundAt: d.LastWhatsAppOutboundAt,
		UpdatedAt:              time.Now().UTC(),
	}
	if d.ContactID != uuid.Nil {
		id := d.ContactID
		out.ContactID = &id
	}
	if prev != nil {
		out.PhoneRaw = prev.PhoneRaw
		out.PhoneCountry = prev.PhoneCountry
		out.PhoneValid = prev.PhoneValid
	}
	return out
}
