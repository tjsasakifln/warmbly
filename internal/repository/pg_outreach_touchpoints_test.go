package repository

import (
	"strings"
	"testing"
)

func TestOutreachTouchpointSelectQualifiesJoinedColumns(t *testing.T) {
	for _, column := range []string{
		"id", "organization_id", "account_id", "contact_candidate_id",
		"draft_id", "created_at", "updated_at",
	} {
		if !strings.Contains(outreachTouchpointSelect, "t."+column) {
			t.Fatalf("touchpoint select must qualify %s for joined queries", column)
		}
	}
}
