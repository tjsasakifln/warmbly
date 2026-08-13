package handler

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/warmbly/warmbly/internal/app/confenge"
	"github.com/warmbly/warmbly/internal/app/token"
)

const (
	confengeOperatorWindow = time.Minute
	confengeOperatorLimit  = 10
)

var confengeOperatorAttempts sync.Map

type confengeOperatorRateState struct {
	mu      sync.Mutex
	started time.Time
	count   int
}

// ConfengeOperatorSession mints the loopback-only operator session.
func (h *Handler) ConfengeOperatorSession(c *gin.Context) {
	if !h.ConfengeConfig.OperatorMode {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	if !allowConfengeOperatorSession(c.ClientIP(), time.Now()) {
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusTooManyRequests, gin.H{
			"error":      "Too Many Requests",
			"message":    "Muitas tentativas de abrir a sessão. Aguarde um minuto.",
			"code":       "rate_limit_exceeded",
			"request_id": c.GetString("request_id"),
		})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), authRequestTimeout)
	defer cancel()

	user, err := confenge.ValidateOperatorIdentity(
		ctx,
		h.ConfengeConfig,
		h.UserRepo,
		h.OrganizationService,
	)
	if err != nil {
		confengeOperatorUnavailable(c)
		return
	}

	session, xerr := h.TokenService.GenerateSessionWithOrg(
		ctx,
		user.ID,
		user.Email,
		c.ClientIP(),
		c.Request.UserAgent(),
		token.AuthProviderConfenge,
		&h.ConfengeConfig.OperatorOrgID,
	)
	if xerr != nil {
		confengeOperatorUnavailable(c)
		return
	}

	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, session)
}

func allowConfengeOperatorSession(ip string, now time.Time) bool {
	value, _ := confengeOperatorAttempts.LoadOrStore(ip, &confengeOperatorRateState{started: now})
	state := value.(*confengeOperatorRateState)
	state.mu.Lock()
	defer state.mu.Unlock()
	if now.Sub(state.started) >= confengeOperatorWindow {
		state.started = now
		state.count = 0
	}
	if state.count >= confengeOperatorLimit {
		return false
	}
	state.count++
	return true
}

func confengeOperatorUnavailable(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusServiceUnavailable, gin.H{
		"error":      "Service Unavailable",
		"message":    "A sessão do operador CONFENGE não está disponível.",
		"code":       "CONFENGE_OPERATOR_SESSION_UNAVAILABLE",
		"request_id": c.GetString("request_id"),
	})
}
