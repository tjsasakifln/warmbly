package handler

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/app/confenge/liveintel"
)

// UnsubscribeIntelWatch stops one INTEL_WATCH subscription. Like the campaign
// List-Unsubscribe endpoint it is PUBLIC and unauthenticated: the signed token
// in the query is the capability. GET is a recipient clicking the link, POST is
// the mailbox provider's RFC 8058 one-click.
// The link shape is /unsubscribe/watch?oid=<org>&sid=<subscription>&t=<token>.
func (h *Handler) UnsubscribeIntelWatch(c *gin.Context) {
	isPost := c.Request.Method == http.MethodPost

	invalid := func() {
		if isPost {
			// RFC 8058: a bad or expired link is terminal, so 200 stops the
			// provider retrying something that can never succeed.
			c.Status(http.StatusOK)
			return
		}
		c.Data(http.StatusBadRequest, "text/html; charset=utf-8", unsubPage("This unsubscribe link is invalid."))
	}

	orgID, orgErr := uuid.Parse(c.Query("oid"))
	subscriptionID, subErr := uuid.Parse(c.Query("sid"))
	if orgErr != nil || subErr != nil {
		invalid()
		return
	}
	if !liveintel.VerifyUnsubscribeToken(orgID, subscriptionID, c.Query("t")) {
		invalid()
		return
	}
	if h.IntelWatchRepo == nil {
		if isPost {
			c.Status(http.StatusBadGateway)
			return
		}
		c.Data(http.StatusOK, "text/html; charset=utf-8", unsubPage("We couldn't process that unsubscribe link."))
		return
	}

	// A repeat opt-out is a success, not an error: the recipient's intent is
	// already recorded and re-confirming it must never look like a failure.
	_, err := h.IntelWatchRepo.Unsubscribe(c.Request.Context(), orgID, subscriptionID, time.Now().UTC())

	if isPost {
		if err != nil {
			c.Status(http.StatusBadGateway)
			return
		}
		c.Status(http.StatusOK)
		return
	}
	msg := "You've been unsubscribed."
	if err != nil {
		msg = "We couldn't process that unsubscribe link."
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", unsubPage(msg))
}
