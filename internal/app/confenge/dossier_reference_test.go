package confenge

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/models"
)

func dossierManifestJSON(t *testing.T, catalogMode, dataState string) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"dossier_id":          "dsr_0082085400011_2026_08",
		"schema":              DossierSchemaV1,
		"catalog_mode":        catalogMode,
		"data_state":          dataState,
		"as_of":               "2026-08-22",
		"content_hash":        "sha256:aaaa",
		"public_content_hash": "sha256:bbbb",
		"producer_sha":        "extra-cli@deadbeef",
		"files":               []string{"dossier.json", "public-read.json", "dossier.md", "manifest.json"},
		"reason_codes":        []string{"PANEL_OK"},
	})
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	return b
}

func newDossierService(t *testing.T) (*service, uuid.UUID, uuid.UUID, uuid.UUID) {
	t.Helper()
	repo := newMemRepo()
	org, actor, accID := uuid.New(), uuid.New(), uuid.New()
	acc := &models.OutreachAccount{
		ID: accID, OrganizationID: org, CNPJ14: "00820854000114",
		RazaoSocial: "Engenharia Alpha LTDA", ServiceCode: "REAJUSTE",
	}
	if _, err := repo.UpsertAccount(context.Background(), acc); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	svc := NewService(Config{Enabled: true}, repo, nil).(*service)
	return svc, org, actor, accID
}

// official_live + DATA_READY is the only deliverable combination.
func TestDossierDeliverabilityIsDerivedFromCatalogModeAndDataState(t *testing.T) {
	cases := []struct {
		name        string
		catalogMode string
		dataState   string
		deliverable bool
		reasonHas   string
	}{
		{"official_live_ready", DossierCatalogOfficialLive, DossierDataReady, true, ""},
		{"fixture_ready", DossierCatalogFixture, DossierDataReady, false, "catalog_mode=fixture"},
		{"official_live_hold", DossierCatalogOfficialLive, DossierDataHold, false, "data_state=DATA_HOLD"},
		{"official_live_reject", DossierCatalogOfficialLive, DossierDataReject, false, "data_state=DATA_REJECT"},
		{"fixture_hold", DossierCatalogFixture, DossierDataHold, false, "catalog_mode=fixture"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, reason := DossierDeliverability(tc.catalogMode, tc.dataState)
			if got != tc.deliverable {
				t.Fatalf("deliverable=%v want %v", got, tc.deliverable)
			}
			if tc.deliverable && reason != "" {
				t.Fatalf("deliverable reference must carry no blocking reason, got %q", reason)
			}
			if !tc.deliverable && !strings.Contains(reason, tc.reasonHas) {
				t.Fatalf("reason %q must explain %q", reason, tc.reasonHas)
			}
		})
	}
}

// A fixture or DATA_HOLD manifest is storable, and is never presented as ready.
func TestNonReadyDossierIsStoredButNeverDeliverable(t *testing.T) {
	cases := []struct {
		name        string
		catalogMode string
		dataState   string
		deliverable bool
	}{
		{"official_live_ready", DossierCatalogOfficialLive, DossierDataReady, true},
		{"fixture_ready", DossierCatalogFixture, DossierDataReady, false},
		{"official_live_hold", DossierCatalogOfficialLive, DossierDataHold, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, org, actor, accID := newDossierService(t)
			ref, xerr := svc.AttachDossierReference(context.Background(), org, actor, DossierAttachInput{
				AccountID:    accID,
				ManifestJSON: dossierManifestJSON(t, tc.catalogMode, tc.dataState),
				ArtifactURI:  "file:///srv/artifacts/dossier/alpha",
			})
			if xerr != nil {
				t.Fatalf("attach must store any manifest state: %v", xerr)
			}
			if ref.Deliverable != tc.deliverable {
				t.Fatalf("deliverable=%v want %v", ref.Deliverable, tc.deliverable)
			}
			if ref.Delivered() {
				t.Fatal("attach must never mark a reference delivered")
			}
			badge := BuildDossierBadge(*ref)
			if tc.deliverable {
				if strings.Contains(badge.Label, "NAO entregavel") {
					t.Fatalf("deliverable badge must not read as blocked: %q", badge.Label)
				}
			} else {
				if !strings.Contains(badge.Label, "NAO entregavel") {
					t.Fatalf("non-deliverable badge must say so: %q", badge.Label)
				}
				if badge.Deliverable {
					t.Fatal("badge must not present a non-ready dossier as deliverable")
				}
			}
			stored, lerr := svc.ListDossierReferences(context.Background(), org, accID)
			if lerr != nil || len(stored) != 1 {
				t.Fatalf("reference must be stored regardless of data_state: %v %d", lerr, len(stored))
			}
		})
	}
}

// The private body and the prospect identity can never reach Warmbly.
func TestDossierPrivateBodyAndIdentityCanNeverBeStored(t *testing.T) {
	base := map[string]any{
		"dossier_id":   "dsr_1",
		"schema":       DossierSchemaV1,
		"catalog_mode": DossierCatalogOfficialLive,
		"data_state":   DossierDataReady,
		"content_hash": "sha256:aaaa",
	}
	with := func(extra map[string]any) []byte {
		doc := map[string]any{}
		for k, v := range base {
			doc[k] = v
		}
		for k, v := range extra {
			doc[k] = v
		}
		b, _ := json.Marshal(doc)
		return b
	}
	cases := []struct {
		name    string
		payload []byte
		wantErr error
	}{
		{"clean_manifest", with(nil), nil},
		{"identity_section", with(map[string]any{"identity": map[string]any{"x": 1}}), ErrDossierPrivateBody},
		{"buyer_map_section", with(map[string]any{"buyer_map": []any{}}), ErrDossierPrivateBody},
		{"price_panel_section", with(map[string]any{"price_panel": map[string]any{}}), ErrDossierPrivateBody},
		{"cnpj14_field", with(map[string]any{"cnpj14": "00820854000114"}), ErrDossierPrivateBody},
		{"razao_social_field", with(map[string]any{"razao_social": "Engenharia Alpha LTDA"}), ErrDossierPrivateBody},
		{"nested_identity", with(map[string]any{"meta": map[string]any{"deep": map[string]any{"municipio": "Curitiba"}}}), ErrDossierPrivateBody},
		{"identity_inside_array", with(map[string]any{"files": []any{map[string]any{"fornecedor_cnpj": "008"}}}), ErrDossierPrivateBody},
		{"markdown_body", with(map[string]any{"markdown": "# Dossie"}), ErrDossierPrivateBody},
		{"not_json", []byte("not json"), ErrDossierManifestInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseDossierManifest(tc.payload)
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("clean manifest must parse: %v", err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err=%v want %v", err, tc.wantErr)
			}
			svc, org, actor, accID := newDossierService(t)
			if _, xerr := svc.AttachDossierReference(context.Background(), org, actor, DossierAttachInput{
				AccountID: accID, ManifestJSON: tc.payload,
			}); xerr == nil {
				t.Fatal("attach must refuse a payload carrying the dossier body or identity")
			}
			stored, _ := svc.ListDossierReferences(context.Background(), org, accID)
			if len(stored) != 0 {
				t.Fatalf("nothing may be stored from a rejected payload, got %d", len(stored))
			}
		})
	}
}

// Delivery is a human act: never defaulted, never inferred, never on a
// non-deliverable reference, never without an actor.
func TestDossierDeliveryIsNeverInferred(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()

	t.Run("attach_defaults_to_not_delivered", func(t *testing.T) {
		svc, org, actor, accID := newDossierService(t)
		ref, xerr := svc.AttachDossierReference(ctx, org, actor, DossierAttachInput{
			AccountID:    accID,
			ManifestJSON: dossierManifestJSON(t, DossierCatalogOfficialLive, DossierDataReady),
		})
		if xerr != nil {
			t.Fatalf("attach: %v", xerr)
		}
		if ref.Delivered() || ref.DeliveredAt != nil || ref.DeliveredBy != nil {
			t.Fatal("a freshly attached reference must default to not-delivered")
		}
	})

	t.Run("normalize_rejects_pre_stamped_delivery", func(t *testing.T) {
		stamped := now
		actor := uuid.New()
		ref := &DossierReference{
			OrganizationID: uuid.New(), AccountID: uuid.New(), AttachedBy: actor,
			DossierID: "d1", ContentHash: "h1",
			CatalogMode: DossierCatalogOfficialLive, DataState: DossierDataReady,
			DeliveredAt: &stamped, DeliveredBy: &actor,
		}
		if err := NormalizeDossierReference(ref, now); !errors.Is(err, ErrDossierDeliveryNotInferred) {
			t.Fatalf("err=%v want ErrDossierDeliveryNotInferred", err)
		}
	})

	t.Run("mark_requires_human_actor", func(t *testing.T) {
		ref := &DossierReference{Deliverable: true}
		if err := MarkDossierDelivered(ref, uuid.Nil, now, ""); !errors.Is(err, ErrDossierHumanActor) {
			t.Fatalf("err=%v want ErrDossierHumanActor", err)
		}
	})

	t.Run("mark_refuses_non_deliverable", func(t *testing.T) {
		svc, org, actor, accID := newDossierService(t)
		ref, xerr := svc.AttachDossierReference(ctx, org, actor, DossierAttachInput{
			AccountID:    accID,
			ManifestJSON: dossierManifestJSON(t, DossierCatalogFixture, DossierDataReady),
		})
		if xerr != nil {
			t.Fatalf("attach: %v", xerr)
		}
		if _, derr := svc.MarkDossierReferenceDelivered(ctx, org, actor, ref.ID, "entregue"); derr == nil {
			t.Fatal("a fixture dossier must never be markable as delivered")
		}
	})

	t.Run("mark_records_explicit_human_delivery", func(t *testing.T) {
		svc, org, actor, accID := newDossierService(t)
		ref, xerr := svc.AttachDossierReference(ctx, org, actor, DossierAttachInput{
			AccountID:    accID,
			ManifestJSON: dossierManifestJSON(t, DossierCatalogOfficialLive, DossierDataReady),
		})
		if xerr != nil {
			t.Fatalf("attach: %v", xerr)
		}
		got, derr := svc.MarkDossierReferenceDelivered(ctx, org, actor, ref.ID, "entregue na reuniao")
		if derr != nil {
			t.Fatalf("mark delivered: %v", derr)
		}
		if !got.Delivered() || got.DeliveredBy == nil || *got.DeliveredBy != actor {
			t.Fatal("delivery must record who handed the dossier over")
		}
		if got.DeliveryNote != "entregue na reuniao" {
			t.Fatalf("delivery note=%q", got.DeliveryNote)
		}
	})
}

// Attaching a reference must not reach auto-send, GREEN autorun, dispatch, or
// the kill switch, and must not flip any sendability flag on the card.
func TestAttachingDossierReferenceReachesNoSendPath(t *testing.T) {
	forbidden := []string{
		"Dispatch", "SendApproved", "QueueTouchpoint", "ApproveTouchpoint",
		"GreenAutorun", "AutoSendEnabled", "ReserveSlot", "CommitSlot",
		"RecordSent", "EnqueueOutcome", "KillSwitch",
	}
	files := []string{
		"dossier_reference.go",
		"dossier_reference_service.go",
		"dossier_reference_store_pg.go",
	}
	for _, name := range files {
		t.Run(name, func(t *testing.T) {
			body, err := os.ReadFile(name)
			if err != nil {
				t.Fatalf("read %s: %v", name, err)
			}
			for _, token := range forbidden {
				if strings.Contains(string(body), token) {
					t.Fatalf("%s must not reference the send path symbol %q", name, token)
				}
			}
		})
	}

	t.Run("card_flags_are_untouched", func(t *testing.T) {
		svc, org, actor, accID := newDossierService(t)
		if svc.cfg.AutoSendEnabled {
			t.Fatal("CONFENGE_AUTO_SEND_ENABLED must stay false")
		}
		ref, xerr := svc.AttachDossierReference(context.Background(), org, actor, DossierAttachInput{
			AccountID:    accID,
			ManifestJSON: dossierManifestJSON(t, DossierCatalogOfficialLive, DossierDataReady),
		})
		if xerr != nil {
			t.Fatalf("attach: %v", xerr)
		}
		view := &TodayView{Actions: []ActionCard{{
			ActionID: uuid.New().String(), AccountID: accID.String(),
			ActionType: models.ActionDirectCall, Actionable: true,
		}}}
		ApplyDossierBadges(view, []DossierReference{*ref})
		card := view.Actions[0]
		if card.Dossier == nil {
			t.Fatal("card must carry the dossier badge")
		}
		if card.EmailSendable || card.Dispatchable {
			t.Fatal("a dossier reference must never make a card sendable or dispatchable")
		}
	})
}

// The reference table must hold hashes and state, never the dossier body.
func TestDossierReferenceMigrationCarriesNoBodyOrIdentityColumn(t *testing.T) {
	path := filepath.Join("..", "..", "infrastructure", "db", "migrations",
		"000111_confenge_dossier_reference.up.sql")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	body := strings.ToLower(string(raw))
	for _, forbidden := range dossierForbiddenKeys {
		if strings.Contains(body, "\n    "+forbidden+" ") {
			t.Fatalf("migration declares a forbidden column %q", forbidden)
		}
	}
	required := []string{
		"dossier_id", "content_hash", "public_content_hash", "as_of",
		"data_state", "catalog_mode", "producer_sha", "artifact_uri",
		"delivered_at", "delivered_by",
	}
	for _, col := range required {
		if !strings.Contains(body, col) {
			t.Fatalf("migration must declare %q", col)
		}
	}
	for _, guard := range []string{
		"confenge_dossier_references_deliverable_check",
		"confenge_dossier_references_delivery_pair_check",
		"confenge_dossier_references_delivery_gate_check",
	} {
		if !strings.Contains(body, guard) {
			t.Fatalf("migration must enforce %q in SQL, not only in Go", guard)
		}
	}
}
