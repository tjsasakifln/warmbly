package confenge

import (
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestEvaluateInboundReceiveReadyAndBlocked(t *testing.T) {
	org := uuid.MustParse("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee")
	ready := EvaluateInboundReceive(Config{
		Enabled: true, AutoSendEnabled: false,
		InboundWebhookSecret: "shared-secret", InboundOrgID: org,
	})
	if ready.Status != InboundReceiveReady {
		t.Fatalf("want READY got %+v", ready)
	}
	joinedVer := strings.Join(ready.AcceptedEventVersions, ",")
	if !strings.Contains(joinedVer, "confenge.commercial_event.v1") || !strings.Contains(joinedVer, "confenge.search_observation.v1") {
		t.Fatalf("accepted_event_versions=%v", ready.AcceptedEventVersions)
	}
	if !strings.Contains(joinedVer, NetNewInboundHandraiserSchema) {
		t.Fatalf("net-new schema missing from accepted_event_versions=%v", ready.AcceptedEventVersions)
	}
	if ready.AutoSendEnabled || ready.DispatchAttempted {
		t.Fatalf("auto-send leaked: %+v", ready)
	}
	if len(ready.Reasons) != 0 {
		t.Fatalf("READY must have empty reasons: %v", ready.Reasons)
	}
	fmt.Printf("INBOUND_HEALTH status=%s auto_send=%v reasons=%v\n", ready.Status, ready.AutoSendEnabled, ready.Reasons)

	blocked := EvaluateInboundReceive(Config{Enabled: true, AutoSendEnabled: false})
	if blocked.Status != InboundReceiveBlocked {
		t.Fatalf("unset secret/org must BLOCK: %+v", blocked)
	}
	joined := strings.Join(blocked.Reasons, ",")
	if !strings.Contains(joined, InboundReasonSecretMissing) || !strings.Contains(joined, InboundReasonOrgMissing) {
		t.Fatalf("missing reasons: %s", joined)
	}
	if strings.Contains(joined, "shared-secret") || strings.Contains(joined, "@") {
		t.Fatalf("probe leaked secret or PII: %s", joined)
	}
	fmt.Printf("INBOUND_HEALTH_BLOCKED reasons=%s auto_send=false\n", joined)

	auto := EvaluateInboundReceive(Config{
		Enabled: true, AutoSendEnabled: true,
		InboundWebhookSecret: "shared-secret", InboundOrgID: org,
	})
	if auto.Status != InboundReceiveBlocked || !auto.AutoSendEnabled {
		t.Fatalf("auto-send must BLOCK: %+v", auto)
	}
	if !strings.Contains(strings.Join(auto.Reasons, ","), InboundReasonAutoSend) {
		t.Fatalf("auto_send reason missing: %v", auto.Reasons)
	}
	fmt.Printf("INBOUND_HEALTH_AUTOSEND status=%s auto_send=%v\n", auto.Status, auto.AutoSendEnabled)
}
