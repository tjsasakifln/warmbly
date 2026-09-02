package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/app/confenge/liveintel"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

// fakeIntelWatchRepo records opt-outs and answers subject lookups from memory.
type fakeIntelWatchRepo struct {
	repository.IntelWatchRepository
	subscriptions map[uuid.UUID]*models.IntelWatchSubscription
	err           error
	calls         int
}

func (f *fakeIntelWatchRepo) Unsubscribe(_ context.Context, organizationID, subscriptionID uuid.UUID, at time.Time) (bool, error) {
	f.calls++
	if f.err != nil {
		return false, f.err
	}
	sub, ok := f.subscriptions[subscriptionID]
	if !ok || sub.OrganizationID != organizationID || sub.UnsubscribedAt != nil {
		return false, nil
	}
	stamped := at.UTC()
	sub.UnsubscribedAt = &stamped
	return true, nil
}

func (f *fakeIntelWatchRepo) ListActiveSubscriptionsBySubject(_ context.Context, organizationID uuid.UUID, subjectKey string) ([]models.IntelWatchSubscription, error) {
	var out []models.IntelWatchSubscription
	for _, sub := range f.subscriptions {
		if sub.OrganizationID == organizationID && sub.SubjectKey == subjectKey && sub.Active() {
			out = append(out, *sub)
		}
	}
	return out, nil
}

func newWatchUnsubRouter(repo repository.IntelWatchRepository) *gin.Engine {
	h := &Handler{IntelWatchRepo: repo}
	r := gin.New()
	r.GET("/unsubscribe/watch", h.UnsubscribeIntelWatch)
	r.POST("/unsubscribe/watch", h.UnsubscribeIntelWatch)
	return r
}

func newWatchSubscription(orgID uuid.UUID) *models.IntelWatchSubscription {
	return &models.IntelWatchSubscription{
		ID: uuid.New(), OrganizationID: orgID, ContactEmail: "watcher@example.com",
		IntentKind: models.IntelWatchIntentDeadlineChanged, SubjectKey: "contrato-2026-0001",
		Cadence: models.IntelWatchCadenceImmediate, ConsentProvenanceOK: true,
	}
}

func watchUnsubURL(orgID, subscriptionID uuid.UUID) string {
	return "/unsubscribe/watch?oid=" + orgID.String() + "&sid=" + subscriptionID.String() +
		"&t=" + liveintel.UnsubscribeToken(orgID, subscriptionID)
}

// Unsubscribing stamps unsubscribed_at, and the subject then matches no active
// subscription, so a later event for it reaches nobody.
func TestUnsubscribeIntelWatchStopsFutureEvents(t *testing.T) {
	t.Setenv(liveintel.EnvUnsubscribeSecret, "watch-secret")
	orgID := uuid.New()
	sub := newWatchSubscription(orgID)
	repo := &fakeIntelWatchRepo{subscriptions: map[uuid.UUID]*models.IntelWatchSubscription{sub.ID: sub}}

	active, _ := repo.ListActiveSubscriptionsBySubject(context.Background(), orgID, sub.SubjectKey)
	if len(active) != 1 {
		t.Fatalf("fixture is not active: %+v", active)
	}

	w := httptest.NewRecorder()
	newWatchUnsubRouter(repo).ServeHTTP(w, httptest.NewRequest(http.MethodGet, watchUnsubURL(orgID, sub.ID), nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "unsubscribed") {
		t.Fatalf("confirmation page: %s", w.Body.String())
	}
	if sub.UnsubscribedAt == nil {
		t.Fatal("unsubscribed_at was not stamped")
	}
	if active, _ := repo.ListActiveSubscriptionsBySubject(context.Background(), orgID, sub.SubjectKey); len(active) != 0 {
		t.Fatalf("subject still matches an active subscription: %+v", active)
	}

	// A watch consumer over the same store now has nobody to deliver to.
	consumer := liveintel.NewConsumer(repo, nil)
	result, err := consumer.HandleEvent(context.Background(), liveintel.OpportunityEvent{
		Schema: liveintel.EventSchemaV1, EventID: "evt-after-optout",
		EventType: liveintel.EventDeadlineChanged, SubjectKey: sub.SubjectKey, OrgID: orgID,
		OccurredAt: time.Now().UTC(), Payload: map[string]string{"deadline": "2026-12-01"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Matched != 0 {
		t.Fatalf("an unsubscribed watcher still matched: %+v", result)
	}
}

// RFC 8058 one-click: the provider's POST acknowledges with 200 and the opt-out
// is recorded. A repeat POST stays 200 without erroring.
func TestUnsubscribeIntelWatchOneClickPost(t *testing.T) {
	t.Setenv(liveintel.EnvUnsubscribeSecret, "watch-secret")
	orgID := uuid.New()
	sub := newWatchSubscription(orgID)
	repo := &fakeIntelWatchRepo{subscriptions: map[uuid.UUID]*models.IntelWatchSubscription{sub.ID: sub}}
	router := newWatchUnsubRouter(repo)

	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, watchUnsubURL(orgID, sub.ID),
			strings.NewReader("List-Unsubscribe=One-Click"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("attempt %d status=%d", i, w.Code)
		}
	}
	if sub.UnsubscribedAt == nil {
		t.Fatal("one-click did not record the opt-out")
	}
}

// An unsigned, wrongly signed, cross-organization or malformed link never
// reaches the repository.
func TestUnsubscribeIntelWatchRejectsUnauthenticatedLinks(t *testing.T) {
	t.Setenv(liveintel.EnvUnsubscribeSecret, "watch-secret")
	orgID, otherOrg := uuid.New(), uuid.New()
	sub := newWatchSubscription(orgID)
	repo := &fakeIntelWatchRepo{subscriptions: map[uuid.UUID]*models.IntelWatchSubscription{sub.ID: sub}}
	router := newWatchUnsubRouter(repo)

	token := liveintel.UnsubscribeToken(orgID, sub.ID)
	for name, target := range map[string]string{
		"no token":        "/unsubscribe/watch?oid=" + orgID.String() + "&sid=" + sub.ID.String(),
		"wrong token":     "/unsubscribe/watch?oid=" + orgID.String() + "&sid=" + sub.ID.String() + "&t=deadbeef",
		"other org":       "/unsubscribe/watch?oid=" + otherOrg.String() + "&sid=" + sub.ID.String() + "&t=" + token,
		"other id":        "/unsubscribe/watch?oid=" + orgID.String() + "&sid=" + uuid.NewString() + "&t=" + token,
		"unparseable ids": "/unsubscribe/watch?oid=nope&sid=nope&t=" + token,
	} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s: status=%d want 400", name, w.Code)
		}
	}
	if repo.calls != 0 {
		t.Fatalf("an unauthenticated link reached the repository %d times", repo.calls)
	}
	if sub.UnsubscribedAt != nil {
		t.Fatal("an unauthenticated link changed the subscription")
	}
}

// With no signing secret configured nothing verifies: the endpoint fails closed
// rather than accepting a bare subscription id.
func TestUnsubscribeIntelWatchFailsClosedWithoutASecret(t *testing.T) {
	t.Setenv(liveintel.EnvUnsubscribeSecret, "")
	orgID := uuid.New()
	sub := newWatchSubscription(orgID)
	repo := &fakeIntelWatchRepo{subscriptions: map[uuid.UUID]*models.IntelWatchSubscription{sub.ID: sub}}

	w := httptest.NewRecorder()
	target := "/unsubscribe/watch?oid=" + orgID.String() + "&sid=" + sub.ID.String() + "&t="
	newWatchUnsubRouter(repo).ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400", w.Code)
	}
	if repo.calls != 0 {
		t.Fatalf("repository was reached %d times without a secret", repo.calls)
	}
}

// A repository failure is reported, never presented as a successful opt-out.
func TestUnsubscribeIntelWatchSurfacesRepositoryFailure(t *testing.T) {
	t.Setenv(liveintel.EnvUnsubscribeSecret, "watch-secret")
	orgID := uuid.New()
	sub := newWatchSubscription(orgID)
	repo := &fakeIntelWatchRepo{
		subscriptions: map[uuid.UUID]*models.IntelWatchSubscription{sub.ID: sub},
		err:           errors.New("database unavailable"),
	}
	router := newWatchUnsubRouter(repo)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, watchUnsubURL(orgID, sub.ID), nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "couldn't process") {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}

	post := httptest.NewRecorder()
	router.ServeHTTP(post, httptest.NewRequest(http.MethodPost, watchUnsubURL(orgID, sub.ID), nil))
	if post.Code != http.StatusBadGateway {
		t.Fatalf("one-click status=%d want 502 so the provider retries", post.Code)
	}
}
