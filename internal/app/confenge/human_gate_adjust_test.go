package confenge

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/warmbly/warmbly/internal/errx"
)

// Every protected field must be refused by name. A typed struct would silently
// drop these keys, so the caller would believe the recipient changed.
func TestHumanGateAdjustRefusesEveryImmutableFieldByName(t *testing.T) {
	cases := map[string]string{
		"mailbox":          `{"mailbox":"ops@company.invalid"}`,
		"mailbox_purpose":  `{"mailbox_purpose":"PERSONAL_WORK"}`,
		"recipient":        `{"recipient":"someone"}`,
		"recipient_hash":   `{"recipient_hash":"deadbeef"}`,
		"evidence":         `{"evidence":"fabricated"}`,
		"evidence_hash":    `{"evidence_hash":"deadbeef"}`,
		"source":           `{"source":"typed-by-hand"}`,
		"source_run_id":    `{"source_run_id":"run-2"}`,
		"policy_version":   `{"policy_version":"policy-v9"}`,
		"route_class":      `{"route_class":"DIRECT_PERSON"}`,
		"composer_version": `{"composer_version":"confenge.composer.v9"}`,
	}
	for want, body := range cases {
		t.Run(want, func(t *testing.T) {
			if got := humanGateImmutableField([]byte(body)); got != want {
				t.Fatalf("immutable field = %q, want %q", got, want)
			}
			x := humanGateImmutableFieldError(want)
			if x.Code != errx.Unprocessable || x.Identifier != "immutable_field" {
				t.Fatalf("code=%d identifier=%q", x.Code, x.Identifier)
			}
		})
	}
}

func TestHumanGateAdjustAcceptsOnlyCopyFields(t *testing.T) {
	body := `{"subject":"Assunto","body_text":"Corpo","reason":"typo no assunto","confirmation":"v1","expected_frozen_hash":"abc"}`
	if got := humanGateImmutableField([]byte(body)); got != "" {
		t.Fatalf("copy-only body must be accepted, got %q", got)
	}
	if got := humanGateImmutableField(nil); got != "" {
		t.Fatalf("absent raw body must not invent a violation, got %q", got)
	}
}

// The immutable-field refusal must be reported before any other validation, so
// a caller that tried to retype a mailbox is told that, not that its reason was
// too short.
func TestHumanGateAdjustImmutableFieldOutranksOtherValidation(t *testing.T) {
	svc := &service{humanGateDB: new(pgxpool.Pool)}
	_, x := svc.AdjustHumanGateCandidate(t.Context(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), HumanGateAdjustInput{
		RawBody: []byte(`{"mailbox":"ops@company.invalid","subject":"","reason":"x"}`),
	})
	if x == nil || x.Identifier != "immutable_field" {
		t.Fatalf("got %#v, want immutable_field", x)
	}
}

func TestHumanGateAdjustRequiresHumanActor(t *testing.T) {
	svc := &service{humanGateDB: new(pgxpool.Pool)}
	if _, x := svc.AdjustHumanGateCandidate(t.Context(), uuid.New(), uuid.Nil, uuid.New(), uuid.New(), HumanGateAdjustInput{}); x != errx.ErrUnauthorized {
		t.Fatalf("got %#v, want unauthorized", x)
	}
}

// Recomputing hashes must reuse the freeze path's own helpers, and must be a
// pure function of the frozen bytes: the same edit applied twice produces the
// same content hash and the same cohort hash.
func TestHumanGateAdjustHashRecomputationIsDeterministic(t *testing.T) {
	member := FrozenCohortMember{
		AccountRef: "acc-1", CandidateRef: "cand-1", Mailbox: "contato@empresa.invalid",
		RouteClass: RouteClassGenericCompany, EvidenceHash: "evidence-1",
	}
	subject, body := "Reajuste contratual", "Bom dia. Posso enviar o recorte do contrato?"
	first := hashControlledContent(member.Mailbox, member.RouteClass, subject, body)
	second := hashControlledContent(member.Mailbox, member.RouteClass, subject, body)
	if first != second || first == "" {
		t.Fatalf("content hash is not deterministic: %q vs %q", first, second)
	}
	if first == hashControlledContent(member.Mailbox, member.RouteClass, subject, body+" ok") {
		t.Fatal("a different body must produce a different content hash")
	}

	snap := &FrozenCohortSnapshot{Members: []FrozenCohortMember{member}}
	snap.Members[0].ContentHash = first
	a := HashFrozenCohort(snap)
	clone, err := humanGateCloneManifest(snap)
	if err != nil {
		t.Fatal(err)
	}
	if b := HashFrozenCohort(clone); a != b {
		t.Fatalf("cohort hash is not deterministic across a clone: %q vs %q", a, b)
	}
	clone.Members[0].ContentHash = "other"
	if HashFrozenCohort(clone) == a {
		t.Fatal("changed member content must change the cohort hash")
	}
	// The clone must not alias the parent's backing array.
	if snap.Members[0].ContentHash != first {
		t.Fatal("cloning a manifest must not mutate the parent")
	}
}

func TestHumanGateAdjustDiffCarriesBothCopyFields(t *testing.T) {
	diff := []HumanGateAdjustmentDiffEntry{
		{Field: "subject", Before: "a", After: "b"},
		{Field: "body_text", Before: "c", After: "d"},
	}
	raw, err := json.Marshal(diff)
	if err != nil {
		t.Fatal(err)
	}
	var back []HumanGateAdjustmentDiffEntry
	if err = json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if len(back) != 2 || back[0].Field != "subject" || back[1].Field != "body_text" {
		t.Fatalf("diff round trip = %+v", back)
	}
}
