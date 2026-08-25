package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/api/middleware"
	"github.com/warmbly/warmbly/internal/app/contact"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
)

type blockedImportContactService struct {
	contact.ContactService
	result *models.ContactImportResult
}

func (s *blockedImportContactService) ImportCommit(context.Context, string, uuid.UUID, io.Reader, string, *models.ContactImportCommit) (*models.ContactImportResult, *errx.Error) {
	return s.result, errx.ErrContactImportQuality
}

func TestImportCommitContactsIncludesBlockedImportDetails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	quality := &models.ContactImportQuality{Invalid: 2, Disposable: 1, BadAddressRatio: 0.75, Blocked: true}
	result := &models.ContactImportResult{Total: 4, Failed: 4, Quality: quality}
	h := &Handler{ContactService: &blockedImportContactService{result: result}}

	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	file, err := form.CreateFormFile("file", "contacts.csv")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("email\nbad\n")); err != nil {
		t.Fatal(err)
	}
	if err := form.WriteField("options", `{"mapping":[{"index":0,"target":"email"}],"has_header":true}`); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/contacts/import/commit", &body)
	c.Request.Header.Set("Content-Type", form.FormDataContentType())
	c.Set(middleware.UserIDKey, uuid.NewString())
	c.Set(middleware.OrganizationIDKey, uuid.New())
	c.Set("request_id", "req_import_blocked")

	h.ImportCommitContacts(c)

	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnprocessableEntity)
	}
	var response struct {
		Error     string                      `json:"error"`
		Message   string                      `json:"message"`
		Code      string                      `json:"code"`
		RequestID string                      `json:"request_id"`
		Details   *models.ContactImportResult `json:"details"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Error != "Unprocessable" || response.Message != errx.ErrContactImportQuality.Message {
		t.Fatalf("unexpected error response: %+v", response)
	}
	if response.Code != errx.ErrContactImportQuality.Identifier || response.RequestID != "req_import_blocked" {
		t.Fatalf("missing stable error metadata: %+v", response)
	}
	if response.Details == nil || response.Details.Total != 4 || response.Details.Quality == nil || !response.Details.Quality.Blocked {
		t.Fatalf("missing blocked import details: %+v", response.Details)
	}
}
