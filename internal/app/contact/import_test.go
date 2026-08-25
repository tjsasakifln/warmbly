package contact

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

type importContactRepositoryStub struct {
	repository.ContactRepository
	getByEmailsCalls int
	addCalls         int
}

func (s *importContactRepositoryStub) GetByEmailsAndUser(context.Context, uuid.UUID, []string) (map[string]models.Contact, *errx.Error) {
	s.getByEmailsCalls++
	return map[string]models.Contact{}, nil
}

func (s *importContactRepositoryStub) Add(context.Context, string, uuid.UUID, []models.AddContact) ([]models.Contact, *errx.Error) {
	s.addCalls++
	return nil, nil
}

func TestImportCommitBlockedReturnsAggregateWithoutWriting(t *testing.T) {
	service := &contactService{}
	opts := &models.ContactImportCommit{
		HasHeader: true,
		Mapping: []models.ContactImportColumnMapping{
			{Index: 0, Target: models.ContactImportTargetEmail},
		},
	}

	result, xerr := service.ImportCommit(
		context.Background(),
		uuid.NewString(),
		uuid.New(),
		strings.NewReader("email\nbad\nalso-bad\ngood@example.com\ntemporary@mailinator.com\n"),
		"contacts.csv",
		opts,
	)

	if xerr == nil || xerr.Identifier != errx.ErrContactImportQuality.Identifier {
		t.Fatalf("error = %#v, want %q", xerr, errx.ErrContactImportQuality.Identifier)
	}
	if result == nil {
		t.Fatal("expected aggregate result for blocked import")
	}
	if result.Total != 4 || result.Failed != 4 || result.Imported != 0 || result.Updated != 0 {
		t.Fatalf("unexpected result counts: %+v", result)
	}
	if result.Quality == nil || !result.Quality.Blocked {
		t.Fatalf("quality = %+v, want blocked", result.Quality)
	}
	if result.Quality.Invalid != 2 || result.Quality.Disposable != 1 || result.Quality.BadAddressRatio != 0.75 {
		t.Fatalf("unexpected quality summary: %+v", result.Quality)
	}
	if result.EndedAt.IsZero() {
		t.Fatal("blocked result must include ended_at")
	}
}

func TestImportCommitDoesNotCountOtherRowErrorsAsInvalidAddresses(t *testing.T) {
	repo := &importContactRepositoryStub{}
	service := &contactService{contactRepository: repo}
	opts := &models.ContactImportCommit{
		HasHeader: true,
		Mapping: []models.ContactImportColumnMapping{
			{Index: 0, Target: models.ContactImportTargetEmail},
			{Index: 1, Target: models.ContactImportTargetSubscribed},
		},
	}

	result, xerr := service.ImportCommit(
		context.Background(),
		uuid.NewString(),
		uuid.New(),
		strings.NewReader("email,subscribed\ngood@example.com,maybe\nsales@example.com,maybe\n"),
		"contacts.csv",
		opts,
	)

	if xerr != nil {
		t.Fatalf("ImportCommit error: %v", xerr)
	}
	if result.Quality == nil || result.Quality.Invalid != 0 || result.Quality.BadAddressRatio != 0 {
		t.Fatalf("unrelated row errors polluted address quality: %+v", result.Quality)
	}
	if result.Quality.Role != 1 {
		t.Fatalf("role addresses = %d, want 1", result.Quality.Role)
	}
	if result.Failed != 2 || result.Quality.Blocked {
		t.Fatalf("unexpected import result: %+v", result)
	}
	if repo.getByEmailsCalls != 1 || repo.addCalls != 0 {
		t.Fatalf("repository calls: get=%d add=%d", repo.getByEmailsCalls, repo.addCalls)
	}
}
