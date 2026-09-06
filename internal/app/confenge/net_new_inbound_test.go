package confenge

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/app/confenge/liveintel"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

func netNewConsentAt() *time.Time {
	at := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	return &at
}

func validNetNewMap(logicalID string) map[string]any {
	return map[string]any{
		"schema":          NetNewInboundHandraiserSchema,
		"contract_id":     NetNewInboundContractID,
		"policy_id":       NetNewInboundContractID,
		"version":         NetNewInboundPinVersion,
		"policy_version":  NetNewInboundPinVersion,
		"content_hash":    NetNewInboundPinnedHash,
		"schema_hash":     NetNewInboundPinnedHash,
		"policy":          NetNewInboundHandraiserSchema,
		"policy_hash":     NetNewInboundPinnedHash,
		"intake_schema":   NetNewInboundIntakeSchema,
		"taxonomy":        NetNewInboundTaxonomySchema,
		"catalog":         NetNewInboundCatalogSchema,
		"source":          NetNewInboundSource,
		"lane":            NetNewInboundLane,
		"logical_id":      logicalID,
		"event_id":        logicalID,
		"idempotency_key": logicalID,
		"correlation_id":  "corr-" + logicalID,
		"nucleus":         "property_valuation",
		"offer_candidate": NetNewInboundOfferCandidate,
		"source_asset":    NetNewInboundSourceAsset,
		"city_class":      "capital",
		"urgency":         "this_week",
		"why_now":         "requested a technical readiness assessment from the public form",
		"person":          map[string]any{"email": logicalID + "@example.test", "name": "Net New " + logicalID},
		"company":         map[string]any{"name": "Obra " + logicalID},
		"consent":         map[string]any{"granted": true, "source": "web_form:confenge.com/inbound", "at": "2026-09-04T12:00:00Z"},
		"conflict":        map[string]any{"status": "NONE", "ref": "conflict:none"},
		"sensitive_data":  false,
	}
}

// governanceNetNewMap models web-cfg #608's final, schema-conformant request.
// The published hash never blesses the older shorthand fixture shape.
func governanceNetNewMap(logicalID string) map[string]any {
	m := officialNetNewMap(logicalID)
	m["content_hash"] = "sha256:" + GovernanceInboundPolicyHash
	m["schema_hash"] = "sha256:" + GovernanceInboundPolicyHash
	m["policy_hash"] = "sha256:" + GovernanceInboundPolicyHash
	m["hash"] = "sha256:" + GovernanceInboundPolicyHash
	m["governance_source_sha"] = GovernanceInboundSourceSHA
	m["intake_schema"] = "CONFENGE_WEB_INTAKE/2.1.0-mv03.20260905"
	return m
}

func officialNetNewMap(logicalID string) map[string]any {
	return map[string]any{
		"schema":                          NetNewInboundHandraiserSchema,
		"schema_version":                  "net-new-inbound-handraiser-request.1.0.0-draft.20260904",
		"contract_id":                     NetNewInboundContractID,
		"policy_id":                       NetNewInboundContractID,
		"version":                         NetNewInboundPinVersion,
		"policy_version":                  NetNewInboundPinVersion,
		"hash":                            NetNewInboundPinnedHash,
		"canonical_name":                  NetNewInboundHandraiserSchema,
		"origin":                          NetNewInboundSource,
		"acquisition_lane":                "NET_NEW_INBOUND",
		"intent_kind":                     "HUMAN_REVIEW",
		"idempotency_key":                 logicalID,
		"correlation_id":                  "corr-" + logicalID,
		"receipt_id":                      "web-receipt-" + logicalID,
		"intake_source":                   NetNewInboundSource,
		"landing_asset":                   map[string]any{"id": NetNewInboundTriageSourceAsset, "kind": "TRIAGE"},
		"nucleus_id":                      "property_valuation",
		"offer_candidate_id":              NetNewInboundTriageOfferCandidate,
		"party_kind":                      "PERSON",
		"decision_role":                   "UNKNOWN",
		"site_location":                   map[string]any{"material": false},
		"urgency":                         "UNKNOWN",
		"why_now_class":                   "UNKNOWN",
		"desired_decision_or_deliverable": "UNKNOWN",
		"document_availability_class":     "UNKNOWN",
		"contact_evidence": map[string]any{
			"present": true, "channel": "WHATSAPP", "evidence_ref": "contact:" + logicalID,
			"identity_match_method": "EXPLICIT_CONTACT",
		},
		"consent_evidence": map[string]any{
			"captured": true, "basis": "EXPLICIT_FORM_SUBMIT", "evidence_ref": "consent:" + logicalID,
		},
		"sensitive_data":     map[string]any{"present": false, "class": "NONE"},
		"conflict_screening": map[string]any{"status": "NOT_SCREENED", "protected_ref": "conflict:" + logicalID},
		"source":             map[string]any{"system": "web-cfg"},
		"protected_contact": map[string]any{
			"name": "Pessoa " + logicalID, "phone": "+5541999887766", "preferred_channel": "WHATSAPP",
		},
	}
}

func marshalNetNew(t *testing.T, body map[string]any) []byte {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestNetNewInboundPinMatchesPublishedFixture(t *testing.T) {
	raw, err := os.ReadFile("testdata/net_new_inbound_handraiser/conformance.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		PinMaterial string `json:"pin_material"`
		SchemaHash  string `json:"schema_hash"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.PinMaterial != NetNewInboundPinMaterial() {
		t.Fatalf("fixture pin_material diverged from runtime pin")
	}
	sum := sha256.Sum256([]byte(doc.PinMaterial))
	got := hex.EncodeToString(sum[:])
	if got != NetNewInboundPinnedHash || got != doc.SchemaHash || got != NetNewInboundPinHash() {
		t.Fatalf("pin hash fixture=%s const=%s recomputed=%s", doc.SchemaHash, NetNewInboundPinnedHash, got)
	}
	runtimePin := RuntimeInboundAuthorityPin()
	if runtimePin.ContentHash != GovernanceInboundPolicyHash || runtimePin.SourceSHA != GovernanceInboundSourceSHA || runtimePin.TestOnly {
		t.Fatalf("runtime Governance authority pin diverged: %+v", runtimePin)
	}
}

func TestNetNewReadbackHMACPayloadBindsOneSafeLogicalID(t *testing.T) {
	first := NetNewInboundReadbackHMACPayload("nnhr-safe:1")
	second := NetNewInboundReadbackHMACPayload("nnhr-safe:2")
	if len(first) == 0 || string(first) == string(second) || !strings.HasPrefix(string(first), "GET\n/api/v1/webhooks/confenge/inbound/handraisers/") {
		t.Fatalf("readback HMAC material is not route-bound: %q %q", first, second)
	}
	if got := NetNewInboundReadbackHMACPayload(strings.Repeat("a", 128)); len(got) == 0 {
		t.Fatal("128-byte OPAQUE_REF was rejected")
	}
	for _, invalid := range []string{"", "lead@example.test", "has/slash", strings.Repeat("a", 129)} {
		if got := NetNewInboundReadbackHMACPayload(invalid); len(got) != 0 {
			t.Fatalf("unsafe logical ID %q produced HMAC material %q", invalid, got)
		}
	}
}

func TestNetNewIngestAndReadbackShareOpaqueRefValidator(t *testing.T) {
	for _, size := range []int{128, 129} {
		t.Run(fmt.Sprintf("size-%d", size), func(t *testing.T) {
			svc, _, org := netNewTestService(t)
			logicalID := strings.Repeat("a", size)
			res, xerr := svc.IngestNetNewInboundHandraiser(context.Background(), org, marshalNetNew(t, validNetNewMap(logicalID)), *netNewConsentAt())
			if xerr != nil {
				t.Fatal(xerr)
			}
			if (res.Outcome == NetNewInboundOutcomeAccepted) != (size == 128) {
				t.Fatalf("ingest boundary outcome=%s", res.Outcome)
			}
			if (len(NetNewInboundReadbackHMACPayload(logicalID)) > 0) != (size == 128) {
				t.Fatal("HMAC/readback validator diverged from ingest boundary")
			}
		})
	}
}

func TestFinalGovernancePinRequiresPublishedRequestConformance(t *testing.T) {
	baseline := governanceNetNewMap("lead-0123456789abcdef")
	parsed, err := ParseNetNewInboundEnvelope(marshalNetNew(t, baseline))
	if err != nil {
		t.Fatal(err)
	}
	if d := DecideNetNewInbound(parsed, RuntimeInboundAuthorityPin()); d.Outcome != NetNewInboundOutcomeAccepted {
		t.Fatalf("web-cfg 2.1 conformant request not accepted: %+v", d)
	}
	required := []string{
		"schema_version", "origin", "acquisition_lane", "intent_kind", "idempotency_key", "correlation_id", "receipt_id",
		"contact_evidence", "consent_evidence", "intake_source", "landing_asset", "nucleus_id", "offer_candidate_id",
		"party_kind", "decision_role", "site_location", "urgency", "why_now_class", "desired_decision_or_deliverable",
		"document_availability_class", "sensitive_data", "conflict_screening",
	}
	for _, field := range required {
		t.Run(field, func(t *testing.T) {
			body := governanceNetNewMap("lead-" + strings.Repeat("a", 24))
			delete(body, field)
			env, err := ParseNetNewInboundEnvelope(marshalNetNew(t, body))
			if err != nil {
				t.Fatal(err)
			}
			if d := DecideNetNewInbound(env, RuntimeInboundAuthorityPin()); d.Outcome == NetNewInboundOutcomeAccepted {
				t.Fatalf("final policy hash accepted missing official field %s", field)
			}
		})
	}
	legacyClaim := validNetNewMap("lead-legacy-final-pin")
	for _, key := range []string{"content_hash", "schema_hash", "policy_hash"} {
		legacyClaim[key] = GovernanceInboundPolicyHash
	}
	env, err := ParseNetNewInboundEnvelope(marshalNetNew(t, legacyClaim))
	if err != nil {
		t.Fatal(err)
	}
	if d := DecideNetNewInbound(env, RuntimeInboundAuthorityPin()); d.Outcome == NetNewInboundOutcomeAccepted {
		t.Fatal("legacy shorthand presented itself as the final Governance contract")
	}
}

func TestFinalGovernanceSafetyVetoesNeverCreateCommercialState(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"opt out", func(m map[string]any) { m["opt_out"] = true }},
		{"fuzzy identity", func(m map[string]any) {
			m["contact_evidence"].(map[string]any)["identity_match_method"] = "FUZZY"
		}},
		{"name identity", func(m map[string]any) {
			m["contact_evidence"].(map[string]any)["identity_match_method"] = "NAME"
		}},
		{"arbitrary consent basis", func(m map[string]any) {
			m["consent_evidence"].(map[string]any)["basis"] = "LEGITIMATE_INTEREST"
		}},
		{"consent not captured", func(m map[string]any) {
			m["consent_evidence"].(map[string]any)["captured"] = false
		}},
		{"identity field in contact evidence", func(m map[string]any) {
			m["contact_evidence"].(map[string]any)["name"] = "Protected Person"
		}},
		{"unminimized location", func(m map[string]any) {
			m["site_location"] = map[string]any{"material": true, "city": "Curitiba", "uf": "PR", "street": "Rua Secreta 1"}
		}},
		{"sensitive content", func(m map[string]any) {
			m["sensitive_data"] = map[string]any{"present": false, "class": "NONE", "content": "secret@example.test"}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, repo, org := inboundTestService(t)
			body := governanceNetNewMap("lead-veto-" + strings.ReplaceAll(tc.name, " ", "-"))
			tc.mutate(body)
			res, xerr := svc.IngestNetNewInboundHandraiser(context.Background(), org, marshalNetNew(t, body), *netNewConsentAt())
			if xerr != nil {
				t.Fatal(xerr)
			}
			if res.Outcome == NetNewInboundOutcomeAccepted || res.AccountID != nil || res.ActionID != nil || res.MeetcfgHandoff {
				t.Fatalf("final conformance veto created commercial state: %+v", res)
			}
			accounts, err := repo.ListAccounts(context.Background(), org, repository.OutreachAccountFilter{Limit: 10})
			if err != nil || len(accounts) != 0 {
				t.Fatalf("veto persisted accounts=%d err=%v", len(accounts), err)
			}
		})
	}
}

func TestFinalGovernanceEnumsFailClosedOutsideSnapshot(t *testing.T) {
	cases := []struct {
		field string
		value string
	}{
		{"intent_kind", "SEND_NOW"},
		{"party_kind", "PERSON_OR_COMPANY"},
		{"decision_role", "OWNER"},
		{"urgency", "TOMORROW"},
		{"why_now_class", "OTHER"},
		{"desired_decision_or_deliverable", "CALL"},
		{"document_availability_class", "MAYBE"},
	}
	for _, tc := range cases {
		t.Run(tc.field, func(t *testing.T) {
			body := governanceNetNewMap("lead-enum-" + strings.ReplaceAll(tc.field, "_", "-"))
			body[tc.field] = tc.value
			env, err := ParseNetNewInboundEnvelope(marshalNetNew(t, body))
			if err != nil {
				t.Fatal(err)
			}
			if d := DecideNetNewInbound(env, RuntimeInboundAuthorityPin()); d.Outcome == NetNewInboundOutcomeAccepted {
				t.Fatalf("out-of-contract %s=%s was accepted", tc.field, tc.value)
			}
		})
	}
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"sensitive class", func(m map[string]any) { m["sensitive_data"].(map[string]any)["class"] = "FREE_TEXT" }},
		{"conflict status", func(m map[string]any) { m["conflict_screening"].(map[string]any)["status"] = "DECLINE" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := governanceNetNewMap("lead-enum-" + strings.ReplaceAll(tc.name, " ", "-"))
			tc.mutate(body)
			env, err := ParseNetNewInboundEnvelope(marshalNetNew(t, body))
			if err != nil {
				t.Fatal(err)
			}
			if d := DecideNetNewInbound(env, RuntimeInboundAuthorityPin()); d.Outcome == NetNewInboundOutcomeAccepted {
				t.Fatalf("out-of-contract %s was accepted", tc.name)
			}
		})
	}
}

func TestFinalGovernanceLegacyDeclineIsExplicitlyRejected(t *testing.T) {
	body := governanceNetNewMap("lead-decline-explicit-rejection")
	body["conflict_screening"].(map[string]any)["status"] = "DECLINE"
	env, err := ParseNetNewInboundEnvelope(marshalNetNew(t, body))
	if err != nil {
		t.Fatal(err)
	}
	decision := DecideNetNewInbound(env, RuntimeInboundAuthorityPin())
	if decision.Outcome != NetNewInboundOutcomeRejected || decision.Reason != NetNewInboundReasonConflictDecline {
		t.Fatalf("DECLINE was not safely rejected: %+v", decision)
	}
}

func TestFinalSensitiveContentIsAbsentFromRawAndReadback(t *testing.T) {
	svc, _, org := inboundTestService(t)
	body := governanceNetNewMap("lead-sensitive-content-veto")
	body["sensitive_data"] = map[string]any{"present": false, "class": "NONE", "payload": "secret.person@example.test"}
	res, xerr := svc.IngestNetNewInboundHandraiser(context.Background(), org, marshalNetNew(t, body), *netNewConsentAt())
	if xerr != nil || res.Outcome == NetNewInboundOutcomeAccepted {
		t.Fatalf("sensitive content outcome: %+v %v", res, xerr)
	}
	lead, err := svc.inboundStore().GetInboundLeadByLeadID(context.Background(), org, res.LogicalID)
	if err != nil || lead == nil {
		t.Fatalf("receipt: %v", err)
	}
	if strings.Contains(strings.ToLower(string(lead.RawPayload)), "secret.person") {
		t.Fatalf("sensitive content leaked to raw: %s", lead.RawPayload)
	}
	rb, xerr := svc.ReadbackNetNewInboundHandraiser(context.Background(), org, res.LogicalID)
	if xerr != nil {
		t.Fatal(xerr)
	}
	readback, _ := json.Marshal(rb)
	if strings.Contains(strings.ToLower(string(readback)), "secret.person") {
		t.Fatalf("sensitive content leaked to readback: %s", readback)
	}
}

func TestFinalSensitiveContentKeySetFailsClosed(t *testing.T) {
	for _, key := range []string{"content", "raw", "text", "payload", "body", "message"} {
		t.Run(key, func(t *testing.T) {
			body := governanceNetNewMap("lead-sensitive-key-" + key)
			body["sensitive_data"].(map[string]any)[key] = "secret@example.test"
			env, err := ParseNetNewInboundEnvelope(marshalNetNew(t, body))
			if err != nil {
				t.Fatal(err)
			}
			if d := DecideNetNewInbound(env, RuntimeInboundAuthorityPin()); d.Outcome == NetNewInboundOutcomeAccepted {
				t.Fatalf("sensitive_data.%s was accepted", key)
			}
		})
	}
}

func TestFinalWebCfgWhatsAppPhoneSlotIsAccepted(t *testing.T) {
	svc, repo, org := inboundTestService(t)
	body := governanceNetNewMap("lead-webcfg-whatsapp-phone")
	contact := body["protected_contact"].(map[string]any)
	delete(contact, "whatsapp")
	contact["phone"] = "+5541999887766"
	contact["preferred_channel"] = "WHATSAPP"
	body["contact_evidence"].(map[string]any)["channel"] = "WHATSAPP"
	res, xerr := svc.IngestNetNewInboundHandraiser(context.Background(), org, marshalNetNew(t, body), *netNewConsentAt())
	if xerr != nil || res.Outcome != NetNewInboundOutcomeAccepted || res.AccountID == nil {
		t.Fatalf("web-cfg WhatsApp phone slot: %+v %v", res, xerr)
	}
	candidates, err := repo.ListCandidates(context.Background(), org, *res.AccountID)
	if err != nil || len(candidates) != 1 || candidates[0].PhoneE164 != "+5541999887766" || candidates[0].WhatsAppConsentStatus != "OPTED_IN" {
		t.Fatalf("web-cfg WhatsApp candidate: %+v err=%v", candidates, err)
	}
}

func TestNetNewWholeJSONIdempotencyAndCanonicalReserialization(t *testing.T) {
	a, err := ParseNetNewInboundEnvelope([]byte(`{"logical_id":"opaque-1","unknown":{"b":2,"a":1}}`))
	if err != nil {
		t.Fatal(err)
	}
	b, err := ParseNetNewInboundEnvelope([]byte(`{"unknown":{"a":1,"b":2},"logical_id":"opaque-1"}`))
	if err != nil {
		t.Fatal(err)
	}
	if NetNewAdmissionDigest(a) != NetNewAdmissionDigest(b) {
		t.Fatal("JSON key order changed the canonical whole-request digest")
	}

	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"unknown field", func(m map[string]any) { m["producer_extension"] = "changed" }},
		{"intake source", func(m map[string]any) { m["intake_source"] = "changed" }},
		{"why now", func(m map[string]any) { m["why_now"] = "changed free text" }},
		{"conflict", func(m map[string]any) { m["conflict"] = map[string]any{"status": "DECLINE", "ref": "conflict:changed"} }},
		{"correlation", func(m map[string]any) { m["correlation_id"] = "corr-changed" }},
		{"protected value", func(m map[string]any) {
			m["person"] = map[string]any{"email": "different@example.test", "name": "Net New"}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, _, org := netNewTestService(t)
			body := validNetNewMap("nnhr-whole-" + strings.ReplaceAll(tc.name, " ", "-"))
			body["producer_extension"] = "initial"
			body["intake_source"] = NetNewInboundSource
			firstRaw := marshalNetNew(t, body)
			first, xerr := svc.IngestNetNewInboundHandraiser(context.Background(), org, firstRaw, *netNewConsentAt())
			if xerr != nil || first.Outcome != NetNewInboundOutcomeAccepted {
				t.Fatalf("baseline: %+v %v", first, xerr)
			}
			var pretty bytes.Buffer
			if err := json.Indent(&pretty, firstRaw, "", "  "); err != nil {
				t.Fatal(err)
			}
			replay, xerr := svc.IngestNetNewInboundHandraiser(context.Background(), org, pretty.Bytes(), *netNewConsentAt())
			if xerr != nil || replay.Outcome != NetNewInboundOutcomeAccepted || !replay.Replay {
				t.Fatalf("reserialized replay: %+v %v", replay, xerr)
			}
			tc.mutate(body)
			conflict, xerr := svc.IngestNetNewInboundHandraiser(context.Background(), org, marshalNetNew(t, body), *netNewConsentAt())
			if xerr != nil || conflict.Outcome == NetNewInboundOutcomeAccepted || conflict.Reason != NetNewInboundReasonKeyConflict {
				t.Fatalf("different whole request did not conflict: %+v %v", conflict, xerr)
			}
		})
	}
}

func TestNetNewCanonicalMaterialAndDecisionIDMatchGovernance(t *testing.T) {
	env, err := ParseNetNewInboundEnvelope([]byte(`{"logical_id":"opaque-1","unknown":{"organization":"A & B <C>"}}`))
	if err != nil {
		t.Fatal(err)
	}
	const governanceMaterialHash = "2459c4208fd469da8c0b0d3d7e151a92fecdf1d2052d5662fc59abd2de06ed73"
	if got := NetNewAdmissionDigest(env); got != governanceMaterialHash {
		t.Fatalf("material hash=%s want Governance=%s", got, governanceMaterialHash)
	}
	const governanceDecisionID = "nihr_7f5e17f9ad06cfc106fb0605c56c668b"
	if got := netNewGovernanceDecisionID("opaque-1", governanceMaterialHash); got != governanceDecisionID {
		t.Fatalf("decision_id=%s want Governance=%s", got, governanceDecisionID)
	}
}

func TestNetNewRejectsCaseVariantDuplicateKeys(t *testing.T) {
	base := strings.TrimSuffix(string(marshalNetNew(t, governanceNetNewMap("lead-duplicate-012345"))), "}")
	for _, suffix := range []string{
		`,"Why_Now_Class":"duplicate"}`,
		`,"Conflict_Screening":{"status":"CLEAR"}}`,
		`,"Correlation_ID":"duplicate"}`,
	} {
		if _, err := ParseNetNewInboundEnvelope([]byte(base + suffix)); err == nil {
			t.Fatalf("case-variant duplicate accepted: %s", suffix)
		}
	}
}

func TestFinalGovernanceOfficialFieldsDefeatConflictingAliases(t *testing.T) {
	t.Run("conflict hit cannot be cleared", func(t *testing.T) {
		body := governanceNetNewMap("lead-alias-conflict")
		body["conflict_screening"].(map[string]any)["status"] = "HIT"
		body["conflict"] = map[string]any{"status": "CLEAR"}
		env, err := ParseNetNewInboundEnvelope(marshalNetNew(t, body))
		if err != nil {
			t.Fatal(err)
		}
		if d := DecideNetNewInbound(env, RuntimeInboundAuthorityPin()); d.Outcome == NetNewInboundOutcomeAccepted {
			t.Fatalf("conflicting legacy clearance overrode official HIT: %+v", d)
		}
	})

	t.Run("other technical need cannot be replaced", func(t *testing.T) {
		body := governanceNetNewMap("lead-alias-nucleus")
		body["nucleus_id"] = "other_technical_need"
		body["nucleus"] = "property_valuation"
		env, err := ParseNetNewInboundEnvelope(marshalNetNew(t, body))
		if err != nil {
			t.Fatal(err)
		}
		if d := DecideNetNewInbound(env, RuntimeInboundAuthorityPin()); d.Outcome == NetNewInboundOutcomeAccepted {
			t.Fatalf("conflicting legacy nucleus overrode official fallback: %+v", d)
		}
	})

	t.Run("phone preference cannot become whatsapp consent", func(t *testing.T) {
		body := governanceNetNewMap("lead-alias-channel")
		body["contact_evidence"].(map[string]any)["channel"] = NetNewInboundPreferredWhatsApp
		body["protected_contact"].(map[string]any)["preferred_channel"] = NetNewInboundPreferredPhone
		env, err := ParseNetNewInboundEnvelope(marshalNetNew(t, body))
		if err != nil {
			t.Fatal(err)
		}
		if d := DecideNetNewInbound(env, RuntimeInboundAuthorityPin()); d.Outcome == NetNewInboundOutcomeAccepted {
			t.Fatalf("conflicting PHONE preference invented WhatsApp consent: %+v", d)
		}
	})
}

func TestFinalGovernanceDispositionParity(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(map[string]any)
		outcome string
		reason  string
	}{
		{"consent refused", func(m map[string]any) { m["consent_evidence"].(map[string]any)["captured"] = false }, NetNewInboundOutcomeRejected, NetNewInboundReasonConsent},
		{"contact absent", func(m map[string]any) { m["contact_evidence"].(map[string]any)["present"] = false }, NetNewInboundOutcomeUnknown, NetNewInboundReasonContactUnknown},
		{"fuzzy identity", func(m map[string]any) { m["contact_evidence"].(map[string]any)["identity_match_method"] = "FUZZY_NAME" }, NetNewInboundOutcomeRejected, NetNewInboundReasonFuzzyIdentity},
		{"lowercase origin", func(m map[string]any) { m["origin"] = "confenge_web" }, NetNewInboundOutcomeAccepted, ""},
		{"old official version", func(m map[string]any) { m["policy_version"] = "v1" }, NetNewInboundOutcomeRejected, NetNewInboundReasonSchemaMismatch},
		{"unknown official policy", func(m map[string]any) { m["policy_id"] = "SOMETHING_ELSE" }, NetNewInboundOutcomeUnknown, NetNewInboundReasonContractMismatch},
		{"first touch policy", func(m map[string]any) { m["policy_id"] = "CFG-FIRST-TOUCH-ROUTING" }, NetNewInboundOutcomeRejected, NetNewInboundReasonSchemaMismatch},
		{"origin not admitted", func(m map[string]any) { m["origin"] = "OTHER_WEB" }, NetNewInboundOutcomeRejected, NetNewInboundReasonSource},
		{"lane not admitted", func(m map[string]any) { m["acquisition_lane"] = "OUTBOUND" }, NetNewInboundOutcomeRejected, NetNewInboundReasonLane},
		{"intent not admitted", func(m map[string]any) { m["intent_kind"] = "SEND_NOW" }, NetNewInboundOutcomeRejected, NetNewInboundReasonIntent},
		{"nucleus not admitted", func(m map[string]any) { m["nucleus_id"], m["nucleus"] = "not_a_nucleus", "not_a_nucleus" }, NetNewInboundOutcomeRejected, NetNewInboundReasonNucleus},
		{"location not minimized", func(m map[string]any) {
			m["site_location"] = map[string]any{"material": true, "city": "Curitiba", "uf": "PR", "street": "Rua Secreta"}
		}, NetNewInboundOutcomeRejected, NetNewInboundReasonLocation},
		{"sensitive content", func(m map[string]any) {
			m["sensitive_data"] = map[string]any{"present": false, "class": "NONE", "content": "secret"}
		}, NetNewInboundOutcomeRejected, NetNewInboundReasonSensitiveContent},
		{"old canonical name", func(m map[string]any) { m["canonical_name"] = "NET_NEW_INBOUND_HANDRAISER/v1" }, NetNewInboundOutcomeRejected, NetNewInboundReasonSchemaMismatch},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := governanceNetNewMap("lead-disposition-" + strings.ReplaceAll(tc.name, " ", "-"))
			tc.mutate(body)
			env, err := ParseNetNewInboundEnvelope(marshalNetNew(t, body))
			if err != nil {
				t.Fatal(err)
			}
			if got := DecideNetNewInbound(env, RuntimeInboundAuthorityPin()); got.Outcome != tc.outcome || got.Reason != tc.reason {
				t.Fatalf("disposition=%+v want=%s/%s", got, tc.outcome, tc.reason)
			}
		})
	}
}

func TestFinalGovernanceDerivesQualificationAndLogicalAdmission(t *testing.T) {
	body := governanceNetNewMap("lead-authority-derived")
	body["qualification_state"] = NetNewInboundQualificationQCO
	env, err := ParseNetNewInboundEnvelope(marshalNetNew(t, body))
	if err != nil {
		t.Fatal(err)
	}
	if got := NetNewQualificationState(env); got != NetNewInboundQualificationConflictCheckRequired {
		t.Fatalf("producer dictated qualification=%s", got)
	}

	svc, repo, org := inboundTestService(t)
	first, xerr := svc.IngestNetNewInboundHandraiser(context.Background(), org, marshalNetNew(t, body), *netNewConsentAt())
	if xerr != nil || first.Outcome != NetNewInboundOutcomeAccepted {
		t.Fatalf("first logical admission: %+v %v", first, xerr)
	}
	if first.ReceiptID != "web-receipt-lead-authority-derived" || first.CorrelationID != "corr-lead-authority-derived" {
		t.Fatalf("safe producer correlation missing from result: %+v", first)
	}
	readbackJSON, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	var readback map[string]any
	if err := json.Unmarshal(readbackJSON, &readback); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{
		"schema_version", "policy_id", "policy_version", "canonical_name", "decision_id", "logical_admission_id",
		"decision", "reason_codes", "idempotency_key", "correlation_id", "receipt_id", "replayed", "origin",
		"acquisition_lane", "intent_kind", "intake_source", "nucleus_id", "offer_candidate_id", "qualification_state",
		"inbound_only", "outbound_eligible", "auto_send", "smtp_authorized", "followup_authorized",
		"account_required_for_acceptance", "identity_authorization", "evaluated_at",
	} {
		if _, ok := readback[key]; !ok {
			t.Fatalf("Governance readback field %q missing: %s", key, readbackJSON)
		}
	}
	conflictingAlias := governanceNetNewMap("lead-authority-derived")
	conflictingAlias["logical_id"] = "different-ledger-id"
	second, xerr := svc.IngestNetNewInboundHandraiser(context.Background(), org, marshalNetNew(t, conflictingAlias), *netNewConsentAt())
	if xerr != nil || second.Outcome == NetNewInboundOutcomeAccepted || second.Reason != NetNewInboundReasonKeyConflict {
		t.Fatalf("same official key with different alias was not one logical admission: %+v %v", second, xerr)
	}
	leads, err := repo.ListInboundLeads(context.Background(), org, false, 10)
	if err != nil || len(leads) != 1 {
		t.Fatalf("logical admission count=%d err=%v", len(leads), err)
	}
}

func TestDecideNetNewInboundFailClosed(t *testing.T) {
	env, err := ParseNetNewInboundEnvelope(marshalNetNew(t, validNetNewMap("nnhr-ok")))
	if err != nil {
		t.Fatal(err)
	}
	if d := DecideNetNewInbound(env, testOnlyAuthorityPin()); d.Outcome != NetNewInboundOutcomeAccepted {
		t.Fatalf("valid envelope not accepted: %+v", d)
	}
	cases := []struct {
		name    string
		mutate  func(map[string]any)
		outcome string
		reason  string
	}{
		{"unknown version", func(m map[string]any) {
			m["schema"] = "NET_NEW_INBOUND_HANDRAISER/9.9.9"
			m["version"] = "9.9.9"
			m["policy_version"] = "9.9.9"
		}, NetNewInboundOutcomeUnknown, NetNewInboundReasonSchemaUnknown},
		{"missing hash", func(m map[string]any) { m["schema_hash"] = ""; m["policy_hash"] = ""; m["content_hash"] = "" }, NetNewInboundOutcomeRejected, NetNewInboundReasonHashUnpinned},
		{"divergent hash", func(m map[string]any) {
			bad := strings.Repeat("ab", 32)
			m["schema_hash"] = bad
			m["policy_hash"] = bad
			m["content_hash"] = bad
		}, NetNewInboundOutcomeRejected, NetNewInboundReasonHashMismatch},
		{"missing consent", func(m map[string]any) { m["consent"] = map[string]any{"granted": false} }, NetNewInboundOutcomeRejected, NetNewInboundReasonConsent},
		{"conflict decline", func(m map[string]any) { m["conflict"] = map[string]any{"status": "DECLINE", "ref": "conflict:abc"} }, NetNewInboundOutcomeRejected, NetNewInboundReasonConflictDecline},
		{"conflict hit", func(m map[string]any) { m["conflict"] = map[string]any{"status": "HIT", "ref": "conflict:xyz"} }, NetNewInboundOutcomeRejected, NetNewInboundReasonConflictHit},
		{"outbound claim", func(m map[string]any) { m["outbound_eligible"] = true }, NetNewInboundOutcomeRejected, NetNewInboundReasonOutboundClaim},
		{"auto send claim", func(m map[string]any) { m["auto_send"] = true }, NetNewInboundOutcomeRejected, NetNewInboundReasonAutoSendClaim},
		{"dispatch claim", func(m map[string]any) { m["dispatch_attempted"] = true }, NetNewInboundOutcomeRejected, NetNewInboundReasonAutoSendClaim},
		{"intel watch source", func(m map[string]any) { m["source"] = "INTEL_WATCH" }, NetNewInboundOutcomeRejected, NetNewInboundReasonIntelWatch},
		{"unknown nucleus", func(m map[string]any) { m["nucleus"] = "not_a_nucleus" }, NetNewInboundOutcomeRejected, NetNewInboundReasonNucleus},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := validNetNewMap("nnhr-neg")
			tc.mutate(body)
			parsed, err := ParseNetNewInboundEnvelope(marshalNetNew(t, body))
			if err != nil {
				t.Fatal(err)
			}
			d := DecideNetNewInbound(parsed, testOnlyAuthorityPin())
			if d.Outcome != tc.outcome || d.Reason != tc.reason {
				t.Fatalf("got %+v want %s/%s", d, tc.outcome, tc.reason)
			}
			if d.Outcome == NetNewInboundOutcomeAccepted {
				t.Fatal("negative case was accepted")
			}
		})
	}
	for _, status := range []string{"UNKNOWN", "NOT_SCREENED"} {
		body := validNetNewMap("nnhr-conflict-" + strings.ToLower(status))
		body["conflict"] = map[string]any{"status": status, "ref": "conflict:protected"}
		parsed, err := ParseNetNewInboundEnvelope(marshalNetNew(t, body))
		if err != nil {
			t.Fatal(err)
		}
		if d := DecideNetNewInbound(parsed, testOnlyAuthorityPin()); d.Outcome != NetNewInboundOutcomeAccepted {
			t.Fatalf("%s should be admitted for human conflict check: %+v", status, d)
		}
		if got := NetNewQualificationState(parsed); got != NetNewInboundQualificationConflictCheckRequired {
			t.Fatalf("%s qualification=%s", status, got)
		}
	}
}

func TestIsNetNewInboundDistinctFromIntelWatch(t *testing.T) {
	if !IsNetNewInboundHandraiserEnvelope(marshalNetNew(t, validNetNewMap("nnhr-det"))) {
		t.Fatal("valid envelope not detected")
	}
	opp := []byte(`{"schema":"` + liveintel.EventSchemaV1 + `","event_id":"e1","event_type":"NEW_OPPORTUNITY","subject_key":"company:x","payload":{"k":"v"}}`)
	if IsNetNewInboundHandraiserEnvelope(opp) {
		t.Fatal("opportunity event classified as net-new hand-raiser")
	}
	bundle := []byte(`{"schema":"` + liveintel.OfficialLiveIntelligenceSchema + `"}`)
	if IsNetNewInboundHandraiserEnvelope(bundle) {
		t.Fatal("live-intelligence bundle classified as net-new hand-raiser")
	}
	intent := []byte(`{"schema":"CONFENGE_WEB_INTENT/1.0","intent_kind":"REQUEST_HUMAN_REVIEW"}`)
	if IsNetNewInboundHandraiserEnvelope(intent) {
		t.Fatal("web intent classified as net-new hand-raiser")
	}
}

func TestNetNewAcceptedInboundOnlyNoSMTP(t *testing.T) {
	svc, repo, org := netNewTestService(t)
	now := *netNewConsentAt()
	sends := 0
	svc.cfg.OperatorAlertEmailEnabled = true
	svc.cfg.OperatorAlertEmailKillSwitch = false
	svc.cfg.OperatorAlertEmail = "ops@confenge.com.br"
	svc.operatorMail = func(to, subject, body string) error {
		sends++
		return nil
	}
	body := marshalNetNew(t, validNetNewMap("nnhr-accepted"))
	res, xerr := svc.IngestNetNewInboundHandraiser(context.Background(), org, body, now)
	if xerr != nil {
		t.Fatalf("ingest: %v", xerr)
	}
	if res.Outcome != NetNewInboundOutcomeAccepted {
		t.Fatalf("outcome=%s reason=%s", res.Outcome, res.Reason)
	}
	if sends != 0 {
		t.Fatalf("NET_NEW inbound invoked SMTP %d time(s)", sends)
	}
	if res.Receipt == "" || res.LogicalID != "nnhr-accepted" {
		t.Fatalf("receipt missing: %+v", res)
	}
	if !res.InboundOnly || res.OutboundEligible || res.AutoSend || res.DispatchAttempted {
		t.Fatalf("outbound leaked: %+v", res)
	}
	if !res.MeetcfgHandoff || !MeetcfgHandoffAllowed(res.Outcome) {
		t.Fatal("meetcfg handoff not allowed after ACCEPTED")
	}
	if res.Nucleus != "property_valuation" || res.OfferCandidate != NetNewInboundOfferCandidate ||
		res.SourceAsset != NetNewInboundSourceAsset || res.CityClass != "capital" || res.Urgency != "this_week" {
		t.Fatalf("fields not preserved: %+v", res)
	}
	if res.ActionID == nil || res.AccountID == nil {
		t.Fatalf("missing commercial row: %+v", res)
	}
	acc, err := repo.GetAccount(context.Background(), org, *res.AccountID)
	if err != nil || acc == nil {
		t.Fatalf("account: %v", err)
	}
	if !models.AccountIsInboundOnly(acc) {
		t.Fatal("net-new account is not inbound-only")
	}
	if FirstTouchEligibleAccount(acc) || svc.netNewInFirstTouchEligibleSet(context.Background(), org, acc.ID) {
		t.Fatal("accepted inbound appeared in first-touch eligible set")
	}
	actions, err := svc.actionStore().ListCommercialActions(context.Background(), org, acc.ID, false, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(actions) != 1 {
		t.Fatalf("want 1 hand-raiser got %d", len(actions))
	}
	if actions[0].EmailSendable || actions[0].Dispatchable {
		t.Fatal("hand-raiser is sendable")
	}
	if svc.governor != nil || svc.firstTouchTransport != nil {
		t.Fatal("ingest wired a send path")
	}
	progressed, err := svc.ProcessFastLaneOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if progressed || sends != 0 {
		t.Fatalf("provider mutation progressed=%v sends=%d", progressed, sends)
	}
	rb, xerr := svc.ReadbackNetNewInboundHandraiser(context.Background(), org, "nnhr-accepted")
	if xerr != nil {
		t.Fatal(xerr)
	}
	if rb.Outcome != NetNewInboundOutcomeAccepted || rb.Receipt != res.Receipt {
		t.Fatalf("readback: %+v", rb)
	}
	if rb.AcknowledgedBy != NetNewInboundAckActor || rb.AcknowledgedAt == nil || rb.PolicyVersion != NetNewInboundPinVersion || rb.Hash == "" || rb.Receipt == "" {
		t.Fatalf("readback ack/policy/hash/receipt: %+v", rb)
	}
	if rb.Reason != "" && rb.Outcome == NetNewInboundOutcomeAccepted {
		// accepted may carry empty reason
	}
	exp, xerr := svc.ExportSalesContext(context.Background(), org, 50, "")
	if xerr != nil {
		t.Fatal(xerr)
	}
	found := false
	for _, item := range exp.Items {
		if item.ActionID == *res.ActionID {
			found = true
			if !item.InboundOnly {
				t.Fatal("sales context lost inbound_only")
			}
		}
	}
	if !found {
		t.Fatal("ACCEPTED hand-raiser missing from meetcfg projection")
	}
}

func TestNetNewOfficialPhoneOnlyPersistsProtectedContactPIIFreeReadback(t *testing.T) {
	svc, repo, org := netNewTestService(t)
	now := *netNewConsentAt()
	body := officialNetNewMap("nnhr-official-phone")
	res, xerr := svc.IngestNetNewInboundHandraiser(context.Background(), org, marshalNetNew(t, body), now)
	if xerr != nil {
		t.Fatal(xerr)
	}
	if res.Outcome != NetNewInboundOutcomeAccepted || res.AccountID == nil || res.ActionID == nil {
		t.Fatalf("official phone-only ingest: %+v", res)
	}
	if res.PreferredChannel != NetNewInboundPreferredWhatsApp || res.QualificationState != NetNewInboundQualificationConflictCheckRequired {
		t.Fatalf("channel/qualification not preserved: %+v", res)
	}
	if res.OutboundEligible || res.AutoSend || res.DispatchAttempted || !res.InboundOnly {
		t.Fatalf("inbound authority leaked outbound: %+v", res)
	}
	lead, err := svc.inboundStore().GetInboundLeadByLeadID(context.Background(), org, "nnhr-official-phone")
	if err != nil || lead == nil {
		t.Fatalf("receipt: %v", err)
	}
	if lead.LeadPhone != "+5541999887766" || lead.LeadEmail != "" || lead.Channel != "whatsapp" {
		t.Fatalf("phone-only contact not persisted: %+v", lead)
	}
	raw := strings.ToLower(string(lead.RawPayload))
	if strings.Contains(raw, "5541999887766") || strings.Contains(raw, "pessoa nnhr-official-phone") || strings.Contains(raw, "protected_contact") {
		t.Fatalf("protected contact leaked into raw payload: %s", raw)
	}
	candidates, err := repo.ListCandidates(context.Background(), org, *res.AccountID)
	if err != nil || len(candidates) != 1 {
		t.Fatalf("candidate persistence: %v count=%d", err, len(candidates))
	}
	if candidates[0].Email != "" || candidates[0].PhoneE164 != "+5541999887766" || candidates[0].WhatsAppConsentStatus != "OPTED_IN" || !candidates[0].WhatsAppConsentProvenanceOK {
		t.Fatalf("phone-only candidate invented or lost contact: %+v", candidates[0])
	}
	rb, xerr := svc.ReadbackNetNewInboundHandraiser(context.Background(), org, "nnhr-official-phone")
	if xerr != nil {
		t.Fatal(xerr)
	}
	readbackJSON, err := json.Marshal(rb)
	if err != nil {
		t.Fatal(err)
	}
	readbackText := strings.ToLower(string(readbackJSON))
	if strings.Contains(readbackText, "5541999887766") || strings.Contains(readbackText, "pessoa nnhr-official-phone") {
		t.Fatalf("readback leaked PII: %s", readbackText)
	}
	if rb.Receipt != res.Receipt || rb.LogicalID != res.LogicalID || rb.Outcome != NetNewInboundOutcomeAccepted {
		t.Fatalf("readback lost receipt identity: %+v", rb)
	}
}

func TestNetNewOtherTechnicalNeedStaysNeedsContextWhenNotScreened(t *testing.T) {
	svc, _, org := netNewTestService(t)
	body := officialNetNewMap("nnhr-other-need")
	body["nucleus_id"] = "other_technical_need"
	res, xerr := svc.IngestNetNewInboundHandraiser(context.Background(), org, marshalNetNew(t, body), *netNewConsentAt())
	if xerr != nil {
		t.Fatal(xerr)
	}
	if res.Outcome != NetNewInboundOutcomeAccepted || res.QualificationState != NetNewInboundQualificationNeedsContext {
		t.Fatalf("other technical need was not safely admitted for context: %+v", res)
	}
	if res.ConflictStatus != netNewInboundConflictNotScreened {
		t.Fatalf("conflict status=%s", res.ConflictStatus)
	}
}

func TestNetNewOfficialPhoneChannelDoesNotInventWhatsAppConsent(t *testing.T) {
	svc, repo, org := netNewTestService(t)
	body := officialNetNewMap("nnhr-phone-channel")
	body["source"] = NetNewInboundSource
	body["contact_evidence"] = map[string]any{
		"present": true, "channel": "PHONE", "evidence_ref": "contact:nnhr-phone-channel",
		"identity_match_method": "EXPLICIT_CONTACT",
	}
	body["protected_contact"] = map[string]any{
		"name": "Pessoa Phone", "organization": "Organizacao Protegida",
		"phone": "+5541999887766", "preferred_channel": "PHONE",
	}
	res, xerr := svc.IngestNetNewInboundHandraiser(context.Background(), org, marshalNetNew(t, body), *netNewConsentAt())
	if xerr != nil {
		t.Fatal(xerr)
	}
	if res.Outcome != NetNewInboundOutcomeAccepted || res.PreferredChannel != NetNewInboundPreferredPhone || res.AccountID == nil {
		t.Fatalf("phone intake not admitted distinctly: %+v", res)
	}
	lead, err := svc.inboundStore().GetInboundLeadByLeadID(context.Background(), org, "nnhr-phone-channel")
	if err != nil || lead == nil {
		t.Fatalf("phone receipt: %v", err)
	}
	if lead.Channel != "phone" || lead.CompanyName != "Organizacao Protegida" {
		t.Fatalf("protected organization/channel not persisted: %+v", lead)
	}
	if strings.Contains(strings.ToLower(string(lead.RawPayload)), "organizacao protegida") {
		t.Fatalf("protected organization leaked into raw receipt: %s", lead.RawPayload)
	}
	candidates, err := repo.ListCandidates(context.Background(), org, *res.AccountID)
	if err != nil || len(candidates) != 1 {
		t.Fatalf("phone candidate: %v count=%d", err, len(candidates))
	}
	if candidates[0].WhatsAppConsentStatus == "OPTED_IN" {
		t.Fatalf("PHONE preference invented WhatsApp consent: %+v", candidates[0])
	}
	rb, xerr := svc.ReadbackNetNewInboundHandraiser(context.Background(), org, "nnhr-phone-channel")
	if xerr != nil {
		t.Fatal(xerr)
	}
	rawReadback, _ := json.Marshal(rb)
	if strings.Contains(strings.ToLower(string(rawReadback)), "organizacao protegida") {
		t.Fatalf("protected organization leaked into readback: %s", rawReadback)
	}
}

func TestNetNewContactChannelsStayDistinct(t *testing.T) {
	cases := []struct {
		name         string
		contact      map[string]any
		channel      string
		wantEmail    string
		wantPhone    string
		wantWhatsApp bool
	}{
		{
			name: "email only", channel: NetNewInboundPreferredEmail,
			contact:   map[string]any{"email": "only@example.test", "preferred_channel": "EMAIL"},
			wantEmail: "only@example.test",
		},
		{
			name: "phone only", channel: NetNewInboundPreferredPhone,
			contact:   map[string]any{"phone": "+5541999887766", "preferred_channel": "PHONE"},
			wantPhone: "+5541999887766",
		},
		{
			name: "whatsapp only", channel: NetNewInboundPreferredWhatsApp,
			contact:   map[string]any{"phone": "+5541999887766", "preferred_channel": "WHATSAPP"},
			wantPhone: "+5541999887766", wantWhatsApp: true,
		},
		{
			name: "dual chooses phone", channel: NetNewInboundPreferredPhone,
			contact:   map[string]any{"phone": "+5541999887766", "whatsapp": "+5541999887755", "preferred_channel": "PHONE"},
			wantPhone: "+5541999887766",
		},
		{
			name: "dual chooses whatsapp", channel: NetNewInboundPreferredWhatsApp,
			contact:   map[string]any{"phone": "+5541999887766", "whatsapp": "+5541999887755", "preferred_channel": "WHATSAPP"},
			wantPhone: "+5541999887755", wantWhatsApp: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, repo, org := netNewTestService(t)
			body := officialNetNewMap("nnhr-channel-" + strings.ReplaceAll(tc.name, " ", "-"))
			body["protected_contact"] = tc.contact
			body["contact_evidence"] = map[string]any{
				"present": true, "channel": tc.channel, "evidence_ref": "contact:channel",
				"identity_match_method": "EXPLICIT_CONTACT",
			}
			res, xerr := svc.IngestNetNewInboundHandraiser(context.Background(), org, marshalNetNew(t, body), *netNewConsentAt())
			if xerr != nil || res.Outcome != NetNewInboundOutcomeAccepted || res.AccountID == nil {
				t.Fatalf("ingest: %+v %v", res, xerr)
			}
			if res.PreferredChannel != tc.channel || res.OutboundEligible || res.AutoSend || res.DispatchAttempted {
				t.Fatalf("channel/safety: %+v", res)
			}
			candidates, err := repo.ListCandidates(context.Background(), org, *res.AccountID)
			if err != nil || len(candidates) != 1 {
				t.Fatalf("candidates: %v count=%d", err, len(candidates))
			}
			got := candidates[0]
			if got.Email != tc.wantEmail || got.PhoneE164 != tc.wantPhone {
				t.Fatalf("selected contact leaked across channels: %+v", got)
			}
			if (got.WhatsAppConsentStatus == "OPTED_IN") != tc.wantWhatsApp {
				t.Fatalf("WhatsApp consent channel mismatch: %+v", got)
			}
		})
	}
}

func TestNetNewPhoneWithoutChannelDefaultsToPhoneNotWhatsApp(t *testing.T) {
	env, err := ParseNetNewInboundEnvelope([]byte(`{"logical_id":"phone-only","person":{"phone":"+5541999887766"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := netNewPreferredChannel(env); got != NetNewInboundPreferredPhone {
		t.Fatalf("phone-only channel=%s want PHONE", got)
	}
	if got := netNewSelectedWhatsApp(env); got != "" {
		t.Fatalf("phone-only invented WhatsApp number %s", got)
	}
}

func TestNetNewPhoneOnlyRejectsInvalidOrMissingPreferredContact(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
		reason string
	}{
		{"invalid phone", func(m map[string]any) {
			m["protected_contact"] = map[string]any{"name": "Pessoa", "phone": "123", "preferred_channel": "WHATSAPP"}
		}, NetNewInboundReasonContact},
		{"email preferred without email", func(m map[string]any) {
			m["protected_contact"] = map[string]any{"name": "Pessoa", "phone": "+5541999887766", "preferred_channel": "EMAIL"}
		}, NetNewInboundReasonContact},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, repo, org := netNewTestService(t)
			body := officialNetNewMap("nnhr-bad-contact-" + strings.ReplaceAll(tc.name, " ", "-"))
			tc.mutate(body)
			res, xerr := svc.IngestNetNewInboundHandraiser(context.Background(), org, marshalNetNew(t, body), *netNewConsentAt())
			if xerr != nil {
				t.Fatal(xerr)
			}
			if res.Outcome != NetNewInboundOutcomeRejected || res.Reason != tc.reason || res.ActionID != nil {
				t.Fatalf("unsafe contact accepted: %+v", res)
			}
			accounts, _ := repo.ListAccounts(context.Background(), org, repository.OutreachAccountFilter{Limit: 20})
			if len(accounts) != 0 {
				t.Fatalf("rejected contact created %d accounts", len(accounts))
			}
		})
	}
}

func TestNetNewCanonicalIDReconcileDoesNotNameMerge(t *testing.T) {
	svc, repo, org := netNewTestService(t)
	now := *netNewConsentAt()
	canonical := "canonical-acme-1"
	existing := &models.OutreachAccount{
		OrganizationID: org, SourceLeadID: canonical, SourceSystem: "extra-cli",
		CNPJ14: "55444333000122", RazaoSocial: "Same Display Name LTDA",
		QueueState: models.OutreachQueueNeedsContact, InboundOnly: false,
		TargetFitEligible: true, EmailSendReady: true,
	}
	if _, err := repo.UpsertAccount(context.Background(), existing); err != nil {
		t.Fatal(err)
	}

	body := validNetNewMap("nnhr-canonical")
	body["canonical_entity_id"] = canonical
	body["company"] = map[string]any{"name": "Same Display Name LTDA", "canonical_id": canonical}
	res, xerr := svc.IngestNetNewInboundHandraiser(context.Background(), org, marshalNetNew(t, body), now)
	if xerr != nil {
		t.Fatal(xerr)
	}
	if res.Outcome != NetNewInboundOutcomeAccepted || res.AccountID == nil {
		t.Fatalf("canonical ingest: %+v", res)
	}
	if *res.AccountID != existing.ID {
		t.Fatalf("canonical ID did not reuse account %s vs %s", res.AccountID, existing.ID)
	}
	if !res.Reconciled {
		t.Fatal("canonical match was not marked reconciled")
	}
	if res.OutboundEligible || !res.InboundOnly || res.AutoSend || res.DispatchAttempted {
		t.Fatalf("inbound receipt inherited outbound authority: %+v", res)
	}

	a := validNetNewMap("nnhr-name-a")
	a["company"] = map[string]any{"name": "Same Display Name LTDA"}
	b := validNetNewMap("nnhr-name-b")
	b["company"] = map[string]any{"name": "Same Display Name LTDA"}
	first, xerr := svc.IngestNetNewInboundHandraiser(context.Background(), org, marshalNetNew(t, a), now)
	if xerr != nil {
		t.Fatal(xerr)
	}
	second, xerr := svc.IngestNetNewInboundHandraiser(context.Background(), org, marshalNetNew(t, b), now)
	if xerr != nil {
		t.Fatal(xerr)
	}
	if first.Outcome != NetNewInboundOutcomeAccepted || second.Outcome != NetNewInboundOutcomeAccepted {
		t.Fatalf("name-collision ingest: %+v %+v", first, second)
	}
	if first.AccountID == nil || second.AccountID == nil || *first.AccountID == *second.AccountID {
		t.Fatalf("same name without ID merged: %v vs %v", first.AccountID, second.AccountID)
	}
	if *first.AccountID == existing.ID || *second.AccountID == existing.ID {
		t.Fatal("name-only envelope fused onto canonical entity")
	}
}

func TestNetNewReplay100OneReceiptOneHandraiser(t *testing.T) {
	svc, _, org := netNewTestService(t)
	now := *netNewConsentAt()
	body := marshalNetNew(t, validNetNewMap("nnhr-replay"))
	var first *NetNewInboundResult
	for i := 0; i < 100; i++ {
		res, xerr := svc.IngestNetNewInboundHandraiser(context.Background(), org, body, now.Add(time.Duration(i)*time.Second))
		if xerr != nil {
			t.Fatalf("replay %d: %v", i, xerr)
		}
		if res.Outcome != NetNewInboundOutcomeAccepted {
			t.Fatalf("replay %d outcome=%s", i, res.Outcome)
		}
		if first == nil {
			first = res
			continue
		}
		if res.Receipt != first.Receipt || res.LogicalID != first.LogicalID {
			t.Fatalf("replay %d changed receipt", i)
		}
		if res.ActionID == nil || first.ActionID == nil || *res.ActionID != *first.ActionID {
			t.Fatalf("replay %d extra hand-raiser", i)
		}
		if res.AccountID == nil || *res.AccountID != *first.AccountID {
			t.Fatalf("replay %d extra account", i)
		}
	}
	lead, err := svc.inboundStore().GetInboundLeadByLeadID(context.Background(), org, "nnhr-replay")
	if err != nil || lead == nil {
		t.Fatalf("receipt: %v", err)
	}
	actions, err := svc.actionStore().ListCommercialActions(context.Background(), org, *first.AccountID, false, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(actions) != 1 {
		t.Fatalf("100 replays produced %d queue rows", len(actions))
	}
	acc, _ := svc.repo.GetAccount(context.Background(), org, *first.AccountID)
	if FirstTouchEligibleAccount(acc) {
		t.Fatal("replay made the account first-touch eligible")
	}
	t.Logf("REPLAY_100_LOSS=0 REPLAY_100_DUPLICATES=0 receipt=%s action=%s", first.Receipt, first.ActionID)
}

func TestNetNewRejectsDoNotCreateHandraiserOrMeetcfg(t *testing.T) {
	svc, repo, org := netNewTestService(t)
	now := *netNewConsentAt()
	cases := []struct {
		name   string
		mutate func(map[string]any)
		want   string
	}{
		{"decline", func(m map[string]any) {
			m["conflict"] = map[string]any{"status": "DECLINE", "ref": "conflict:only-ref"}
		}, NetNewInboundReasonConflictDecline},
		{"conflict hit", func(m map[string]any) { m["conflict"] = map[string]any{"status": "HIT", "ref": "conflict:hit"} }, NetNewInboundReasonConflictHit},
		{"no consent", func(m map[string]any) { delete(m, "consent") }, NetNewInboundReasonConsent},
		{"unpinned", func(m map[string]any) { m["schema_hash"] = ""; m["policy_hash"] = ""; m["content_hash"] = "" }, NetNewInboundReasonHashUnpinned},
	}
	before, _ := repo.ListAccounts(context.Background(), org, repository.OutreachAccountFilter{Limit: 500})
	for i, tc := range cases {
		body := validNetNewMap("nnhr-rej-" + string(rune('a'+i)))
		tc.mutate(body)
		res, xerr := svc.IngestNetNewInboundHandraiser(context.Background(), org, marshalNetNew(t, body), now)
		if xerr != nil {
			t.Fatalf("%s: %v", tc.name, xerr)
		}
		if res.Outcome == NetNewInboundOutcomeAccepted || res.ActionID != nil || res.MeetcfgHandoff {
			t.Fatalf("%s accepted: %+v", tc.name, res)
		}
		if res.Reason != tc.want {
			t.Fatalf("%s reason=%s want %s", tc.name, res.Reason, tc.want)
		}
		rb, xerr := svc.ReadbackNetNewInboundHandraiser(context.Background(), org, res.LogicalID)
		if xerr != nil {
			t.Fatalf("%s readback: %v", tc.name, xerr)
		}
		if rb.Outcome == NetNewInboundOutcomeAccepted {
			t.Fatalf("%s readback accepted", tc.name)
		}
		if strings.Contains(strings.ToLower(rb.ConflictRef), "corpus") || strings.Contains(strings.ToLower(rb.WhyNow), "parts") {
			t.Fatalf("%s leaked conflict corpus: %+v", tc.name, rb)
		}
	}
	after, _ := repo.ListAccounts(context.Background(), org, repository.OutreachAccountFilter{Limit: 500})
	if len(after) != len(before) {
		t.Fatalf("rejects created accounts: before=%d after=%d", len(before), len(after))
	}
	exp, xerr := svc.ExportSalesContext(context.Background(), org, 50, "")
	if xerr != nil {
		t.Fatal(xerr)
	}
	if len(exp.Items) != 0 {
		t.Fatalf("rejected events leaked to meetcfg: %d", len(exp.Items))
	}
}

func TestNetNewSensitiveDataStoresRefsOnly(t *testing.T) {
	svc, _, org := netNewTestService(t)
	body := validNetNewMap("nnhr-sensitive")
	body["sensitive_data"] = true
	body["person"] = map[string]any{"email": "secret.person@example.test", "name": "Secret Person"}
	body["why_now"] = "do not copy this corpus into analytics"
	res, xerr := svc.IngestNetNewInboundHandraiser(context.Background(), org, marshalNetNew(t, body), *netNewConsentAt())
	if xerr != nil {
		t.Fatal(xerr)
	}
	if res.Outcome != NetNewInboundOutcomeAccepted {
		t.Fatalf("sensitive rejected: %+v", res)
	}
	lead, err := svc.inboundStore().GetInboundLeadByLeadID(context.Background(), org, "nnhr-sensitive")
	if err != nil || lead == nil {
		t.Fatal(err)
	}
	raw := strings.ToLower(string(lead.RawPayload))
	if strings.Contains(raw, "secret.person@example.test") || strings.Contains(raw, "secret person") {
		t.Fatalf("raw payload kept sensitive data: %s", lead.RawPayload)
	}
	metrics, _ := json.Marshal(NetNewInboundMetric{Nucleus: res.Nucleus, State: res.Outcome, Reason: res.Reason})
	if strings.Contains(strings.ToLower(string(metrics)), "secret.person") {
		t.Fatalf("metrics leaked PII: %s", metrics)
	}
}

func TestNetNewRawReadbackAndMetricsArePositivePIIFreeProjections(t *testing.T) {
	svc, _, org := netNewTestService(t)
	body := validNetNewMap("nnhr-pii-projection")
	body["why_now"] = "Contact Ana at ana.secret@example.test or 5541999990000"
	body["correlation_id"] = "corr-secret-reference"
	body["conflict"] = map[string]any{"status": "NOT_SCREENED", "ref": "secret:reference"}
	body["unknown_extension"] = map[string]any{
		"Email": "unknown.secret@example.test", "Phone": "554188887777", "free_text": "Ana Secret",
	}
	res, xerr := svc.IngestNetNewInboundHandraiser(context.Background(), org, marshalNetNew(t, body), *netNewConsentAt())
	if xerr != nil || res.Outcome != NetNewInboundOutcomeAccepted {
		t.Fatalf("ingest: %+v %v", res, xerr)
	}
	lead, err := svc.inboundStore().GetInboundLeadByLeadID(context.Background(), org, res.LogicalID)
	if err != nil || lead == nil {
		t.Fatalf("lead: %v", err)
	}
	rawText := strings.ToLower(string(lead.RawPayload))
	for _, forbidden := range []string{"ana.secret", "5541999990000", "554188887777", "ana secret", "unknown_extension", "secret:reference", "why_now", "correlation_id"} {
		if strings.Contains(rawText, forbidden) {
			t.Fatalf("raw whitelist leaked %q: %s", forbidden, rawText)
		}
	}
	rb, xerr := svc.ReadbackNetNewInboundHandraiser(context.Background(), org, res.LogicalID)
	if xerr != nil {
		t.Fatal(xerr)
	}
	readbackJSON, err := json.Marshal(rb)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"why_now", "conflict_ref", "canonical_entity_id", "ana.secret", "5541999990000", "secret:reference"} {
		if strings.Contains(strings.ToLower(string(readbackJSON)), forbidden) {
			t.Fatalf("producer readback leaked %q: %s", forbidden, readbackJSON)
		}
	}
	if !strings.Contains(string(readbackJSON), `"correlation_id":"corr-secret-reference"`) {
		t.Fatalf("safe opaque correlation missing from readback: %s", readbackJSON)
	}
	metricJSON, err := json.Marshal(NetNewInboundMetric{Nucleus: res.Nucleus, State: res.Outcome, Reason: res.Reason})
	if err != nil {
		t.Fatal(err)
	}
	var metric map[string]any
	if err := json.Unmarshal(metricJSON, &metric); err != nil {
		t.Fatal(err)
	}
	for key := range metric {
		if key != "nucleus" && key != "state" && key != "reason" {
			t.Fatalf("metric exposed non-approved dimension %q: %s", key, metricJSON)
		}
	}
}

func TestNetNewDownstreamUnavailableThenReplay(t *testing.T) {
	t.Run("action store", func(t *testing.T) {
		svc, repo, org := netNewTestService(t)
		now := *netNewConsentAt()
		body := marshalNetNew(t, validNetNewMap("nnhr-rollback-action"))
		repo.actionUpsertErr = errors.New("commercial action store down")
		first, xerr := svc.IngestNetNewInboundHandraiser(context.Background(), org, body, now)
		if xerr != nil {
			t.Fatal(xerr)
		}
		if first.Outcome != NetNewInboundOutcomeUnknown || first.ActionID != nil {
			t.Fatalf("action-store failure accepted: %+v", first)
		}
		if first.Reason != NetNewInboundReasonDownstream {
			t.Fatalf("action-store reason=%s", first.Reason)
		}
		lead, err := svc.inboundStore().GetInboundLeadByLeadID(context.Background(), org, "nnhr-rollback-action")
		if err != nil || lead == nil {
			t.Fatalf("receipt: %v", err)
		}
		if netNewReceiptComplete(lead) {
			t.Fatal("downstream UNKNOWN marked complete; replay would skip PersistHandRaise")
		}
		rb, xerr := svc.ReadbackNetNewInboundHandraiser(context.Background(), org, "nnhr-rollback-action")
		if xerr != nil {
			t.Fatal(xerr)
		}
		if rb.Outcome == NetNewInboundOutcomeAccepted {
			t.Fatal("stale readback reported ACCEPTED")
		}
		if rb.Reason != NetNewInboundReasonStale && rb.Reason != NetNewInboundReasonDownstream {
			t.Fatalf("stale reason=%s", rb.Reason)
		}
		repo.actionUpsertErr = nil
		second, xerr := svc.IngestNetNewInboundHandraiser(context.Background(), org, body, now.Add(time.Minute))
		if xerr != nil {
			t.Fatal(xerr)
		}
		if second.Outcome != NetNewInboundOutcomeAccepted || second.ActionID == nil {
			t.Fatalf("replay after action-store failure: %+v", second)
		}
		actions, err := svc.actionStore().ListCommercialActions(context.Background(), org, *second.AccountID, false, 50)
		if err != nil {
			t.Fatal(err)
		}
		if len(actions) != 1 {
			t.Fatalf("action-store rollback/replay produced %d actions", len(actions))
		}
	})
	t.Run("account store", func(t *testing.T) {
		svc, repo, org := netNewTestService(t)
		now := *netNewConsentAt()
		body := marshalNetNew(t, validNetNewMap("nnhr-rollback-account"))
		repo.accountUpsertErr = errors.New("account store down")
		first, xerr := svc.IngestNetNewInboundHandraiser(context.Background(), org, body, now)
		if xerr != nil {
			t.Fatal(xerr)
		}
		if first.Outcome != NetNewInboundOutcomeUnknown || first.ActionID != nil || first.AccountID != nil {
			t.Fatalf("account-store failure accepted: %+v", first)
		}
		if first.Reason != NetNewInboundReasonDownstream {
			t.Fatalf("account-store reason=%s", first.Reason)
		}
		lead, err := svc.inboundStore().GetInboundLeadByLeadID(context.Background(), org, "nnhr-rollback-account")
		if err != nil || lead == nil {
			t.Fatalf("receipt: %v", err)
		}
		if netNewReceiptComplete(lead) {
			t.Fatal("admit failure marked complete; replay would skip AdmitInboundOnly")
		}
		repo.accountUpsertErr = nil
		second, xerr := svc.IngestNetNewInboundHandraiser(context.Background(), org, body, now.Add(time.Minute))
		if xerr != nil {
			t.Fatal(xerr)
		}
		if second.Outcome != NetNewInboundOutcomeAccepted || second.ActionID == nil {
			t.Fatalf("replay after account-store failure: %+v", second)
		}
		actions, err := svc.actionStore().ListCommercialActions(context.Background(), org, *second.AccountID, false, 50)
		if err != nil {
			t.Fatal(err)
		}
		if len(actions) != 1 {
			t.Fatalf("account-store rollback/replay produced %d actions", len(actions))
		}
	})
}

func TestNetNewTelemetryFailureDoesNotDropOrGrantOutbound(t *testing.T) {
	svc, _, org := netNewTestService(t)
	svc.netNewMetricSink = func(NetNewInboundMetric) { panic("metrics down") }
	res, xerr := svc.IngestNetNewInboundHandraiser(context.Background(), org, marshalNetNew(t, validNetNewMap("nnhr-metrics")), *netNewConsentAt())
	if xerr != nil {
		t.Fatal(xerr)
	}
	if res.Outcome != NetNewInboundOutcomeAccepted {
		t.Fatalf("telemetry panic dropped event: %+v", res)
	}
	if res.OutboundEligible || res.AutoSend || res.DispatchAttempted {
		t.Fatal("telemetry failure granted outbound")
	}
}

func TestIntelWatchFactualEventDoesNotCreateHandraiser(t *testing.T) {
	svc, repo, org := netNewTestService(t)
	inbox := &netNewFakeInbox{}
	svc.WireIntelWatchInbox(inbox)
	event := liveintel.OpportunityEvent{
		Schema: liveintel.EventSchemaV1, EventID: "intel-watch-fact-1",
		EventType: liveintel.EventNewOpportunity, SubjectKey: "company:watched",
		OrgID: org, OccurredAt: *netNewConsentAt(),
		Payload: map[string]string{"change": "new bid published"},
	}
	receipt, xerr := svc.IngestOpportunityEvent(context.Background(), org, event, *netNewConsentAt())
	if xerr != nil {
		t.Fatalf("opportunity ingest: %v", xerr)
	}
	if receipt == nil || receipt.EventID != "intel-watch-fact-1" {
		t.Fatalf("receipt: %+v", receipt)
	}
	accs, err := repo.ListAccounts(context.Background(), org, repository.OutreachAccountFilter{Limit: 500})
	if err != nil {
		t.Fatal(err)
	}
	for i := range accs {
		if models.AccountIsInboundOnly(&accs[i]) {
			t.Fatal("INTEL_WATCH factual event created inbound-only entity")
		}
	}
	lead, err := svc.inboundStore().GetInboundLeadByLeadID(context.Background(), org, "intel-watch-fact-1")
	if err != nil {
		t.Fatal(err)
	}
	if lead != nil {
		t.Fatal("INTEL_WATCH event created inbound lead/hand-raiser")
	}
	if inbox.n != 1 {
		t.Fatalf("inbox writes=%d", inbox.n)
	}

	watchBody := validNetNewMap("nnhr-watch-src")
	watchBody["source"] = "INTEL_WATCH"
	res, xerr := svc.IngestNetNewInboundHandraiser(context.Background(), org, marshalNetNew(t, watchBody), *netNewConsentAt())
	if xerr != nil {
		t.Fatal(xerr)
	}
	if res.Outcome == NetNewInboundOutcomeAccepted || res.ActionID != nil {
		t.Fatalf("INTEL_WATCH source created hand-raiser: %+v", res)
	}
}

func TestNetNewSixNucleiAcceptedInboundOnly(t *testing.T) {
	svc, repo, org := netNewTestService(t)
	now := *netNewConsentAt()
	if len(NetNewInboundNuclei) != 6 {
		t.Fatalf("closed nuclei want 6 got %d", len(NetNewInboundNuclei))
	}
	ids := map[uuid.UUID]string{}
	for _, nucleus := range NetNewInboundNuclei {
		body := validNetNewMap("nnhr-nucleus-" + nucleus)
		body["nucleus"] = nucleus
		res, xerr := svc.IngestNetNewInboundHandraiser(context.Background(), org, marshalNetNew(t, body), now)
		if xerr != nil {
			t.Fatalf("%s: %v", nucleus, xerr)
		}
		if res.Outcome != NetNewInboundOutcomeAccepted {
			t.Fatalf("%s outcome=%s reason=%s", nucleus, res.Outcome, res.Reason)
		}
		if !res.InboundOnly || res.OutboundEligible || res.AutoSend || res.DispatchAttempted {
			t.Fatalf("%s outbound leaked: %+v", nucleus, res)
		}
		if res.AccountID == nil || res.ActionID == nil {
			t.Fatalf("%s missing commercial row: %+v", nucleus, res)
		}
		if _, dup := ids[*res.AccountID]; dup {
			t.Fatalf("%s reused another nucleus account", nucleus)
		}
		ids[*res.AccountID] = nucleus
		acc, err := repo.GetAccount(context.Background(), org, *res.AccountID)
		if err != nil || acc == nil {
			t.Fatalf("%s account: %v", nucleus, err)
		}
		if !models.AccountIsInboundOnly(acc) || FirstTouchEligibleAccount(acc) {
			t.Fatalf("%s first-touch eligible", nucleus)
		}
		actions, err := svc.actionStore().ListCommercialActions(context.Background(), org, acc.ID, false, 50)
		if err != nil {
			t.Fatal(err)
		}
		if len(actions) != 1 {
			t.Fatalf("%s want 1 action got %d", nucleus, len(actions))
		}
	}
}

func TestNetNewConformanceFixtureIngest(t *testing.T) {
	raw, err := os.ReadFile("testdata/net_new_inbound_handraiser/conformance.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Envelope json.RawMessage `json:"envelope"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	svc, _, org := netNewTestService(t)
	res, xerr := svc.IngestNetNewInboundHandraiser(context.Background(), org, doc.Envelope, *netNewConsentAt())
	if xerr != nil {
		t.Fatal(xerr)
	}
	if res.Outcome != NetNewInboundOutcomeAccepted {
		t.Fatalf("conformance fixture: %+v", res)
	}
}

type netNewFakeInbox struct{ n int }

func (f *netNewFakeInbox) AppendOpportunityEvent(_ context.Context, _ models.IntelWatchInboxEvent) (bool, error) {
	f.n++
	return true, nil
}

func (f *netNewFakeInbox) ClaimReplayableEvents(_ context.Context, _ uuid.UUID, _ time.Time, _, _ time.Duration, _ int) ([]models.IntelWatchInboxEvent, error) {
	return nil, nil
}
