package repository

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/warmbly/warmbly/internal/models"
)

// extra-cli Track A QUALIDADE (00820854000114) publishes these as distinct ids.
const (
	qualidadeCandidateID = "9f4676aa79d0cd5f5fedd7a7"
	qualidadePersonID    = "17adca65031d71b21ebaac4c"
)

type assignRow struct{ src []any }

func (r assignRow) Scan(dest ...any) error {
	if len(dest) != len(r.src) {
		return fmt.Errorf("scan arity dest=%d src=%d", len(dest), len(r.src))
	}
	for i := range dest {
		dv := reflect.ValueOf(dest[i])
		sv := reflect.ValueOf(r.src[i])
		if dv.Kind() != reflect.Ptr || sv.Kind() != reflect.Ptr {
			return fmt.Errorf("scan dest/src %d not pointers", i)
		}
		if dv.Elem().Type() != sv.Elem().Type() {
			return fmt.Errorf("scan type mismatch at %d: %s vs %s", i, dv.Elem().Type(), sv.Elem().Type())
		}
		dv.Elem().Set(sv.Elem())
	}
	return nil
}

func TestOutreachSQLKeepsPersonIDColumn(t *testing.T) {
	if !strings.Contains(outreachCandidateSelect, "person_id") {
		t.Fatal("outreachCandidateSelect dropped person_id")
	}
	if !strings.Contains(outreachActionSelect, "person_id") {
		t.Fatal("outreachActionSelect dropped person_id")
	}
}

func TestQualidadePersonIDSurvivesShippedCandidateScan(t *testing.T) {
	in := models.OutreachContactCandidate{
		SourceContactID: qualidadeCandidateID,
		PersonID:        qualidadePersonID,
		Name:            "EDUARDO SCHMITT ESPINDOLA",
		Role:            "Sócio-Administrador",
	}
	got, err := scanCandidate(assignRow{src: candidateScanDest(&in)})
	if err != nil {
		t.Fatal(err)
	}
	if got.PersonID != qualidadePersonID {
		t.Fatalf("scan dropped extra-cli person_id: got %q want %q", got.PersonID, qualidadePersonID)
	}
	if got.SourceContactID != qualidadeCandidateID {
		t.Fatalf("scan dropped candidate_id: got %q", got.SourceContactID)
	}
	if got.PersonID == got.SourceContactID {
		t.Fatal("person_id must stay distinct from source_contact_id")
	}
	if got.Name != in.Name {
		t.Fatalf("name: %q", got.Name)
	}
}

func TestQualidadePersonIDSurvivesShippedActionScan(t *testing.T) {
	in := models.OutreachCommercialAction{
		PersonName: "EDUARDO SCHMITT ESPINDOLA",
		PersonID:   qualidadePersonID,
		ActionType: models.ActionRoutedCall,
		Lane:       models.LaneRoutedCallQueue,
	}
	var ev, warn, content, corr []byte
	got, err := scanCommercialAction(assignRow{src: commercialActionScanDest(&in, &ev, &warn, &content, &corr)})
	if err != nil {
		t.Fatal(err)
	}
	if got.PersonID != qualidadePersonID {
		t.Fatalf("action scan dropped person_id: got %q want %q", got.PersonID, qualidadePersonID)
	}
	if got.PersonName != in.PersonName {
		t.Fatalf("person name: %q", got.PersonName)
	}
}
