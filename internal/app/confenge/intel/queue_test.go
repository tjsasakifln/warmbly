package intel

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestListQueueFiltersTypeLaneAgeSourceSeverity(t *testing.T) {
	st := NewMemoryStore()
	LoadOperatorQueue(st, OperatorQueueOrgID)
	now := OperatorQueueNow

	all, err := ListQueue(st, OperatorQueueOrgID, ExceptionFilter{}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) < 8 {
		t.Fatalf("want at least 8 fixture exceptions, got %d", len(all))
	}
	for _, ex := range all {
		if ex.OrganizationID != OperatorQueueOrgID {
			t.Fatalf("org leak: %+v", ex)
		}
		if ex.Code == "" || ex.Lane == "" || ex.Source == "" || ex.Severity == "" || ex.Status == "" {
			t.Fatalf("presentation incomplete: %+v", ex)
		}
		if strings.TrimSpace(ex.NextAction) == "" {
			t.Fatal("next_action empty")
		}
		if len(ex.Evidence) == 0 || len(ex.History) == 0 {
			t.Fatalf("missing evidence/history on %s", ex.ID)
		}
	}

	orphans, _ := ListQueue(st, OperatorQueueOrgID, ExceptionFilter{Type: ExceptionOrphan}, now)
	if len(orphans) == 0 {
		t.Fatal("type=orphan empty")
	}
	for _, ex := range orphans {
		if ex.Code != ExceptionOrphan {
			t.Fatalf("type filter leaked %s", ex.Code)
		}
	}

	inbound, _ := ListQueue(st, OperatorQueueOrgID, ExceptionFilter{Lane: FamilyInbound}, now)
	if len(inbound) == 0 {
		t.Fatal("lane=inbound empty")
	}
	for _, ex := range inbound {
		if ex.Lane != FamilyInbound {
			t.Fatalf("lane filter leaked %s", ex.Lane)
		}
	}

	webcfg, _ := ListQueue(st, OperatorQueueOrgID, ExceptionFilter{Source: "web-cfg"}, now)
	if len(webcfg) == 0 {
		t.Fatal("source=web-cfg empty")
	}
	for _, ex := range webcfg {
		if ex.Source != "web-cfg" {
			t.Fatalf("source filter leaked %s", ex.Source)
		}
	}

	high, _ := ListQueue(st, OperatorQueueOrgID, ExceptionFilter{Severity: SeverityHigh}, now)
	if len(high) == 0 {
		t.Fatal("severity=high empty")
	}
	for _, ex := range high {
		if ex.Severity != SeverityHigh {
			t.Fatalf("severity filter leaked %s", ex.Severity)
		}
	}

	aged, _ := ListQueue(st, OperatorQueueOrgID, ExceptionFilter{AgeMin: 24 * 3600}, now)
	if len(aged) == 0 {
		t.Fatal("age_min=24h empty")
	}
	for _, ex := range aged {
		if ex.AgeSeconds < 24*3600 {
			t.Fatalf("age filter leaked %d", ex.AgeSeconds)
		}
	}

	other, _ := ListQueue(st, "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", ExceptionFilter{}, now)
	if len(other) != 0 {
		t.Fatalf("org-scoped list leaked %d rows", len(other))
	}

	fmt.Printf("QUEUE_FILTER all=%d orphan=%d inbound=%d webcfg=%d high=%d aged24h=%d\n",
		len(all), len(orphans), len(inbound), len(webcfg), len(high), len(aged))
}

func TestGetQueueItemShowsEvidenceAndNextAction(t *testing.T) {
	st := NewMemoryStore()
	LoadOperatorQueue(st, OperatorQueueOrgID)
	all, _ := ListQueue(st, OperatorQueueOrgID, ExceptionFilter{Type: ExceptionOrphan}, OperatorQueueNow)
	if len(all) == 0 {
		t.Fatal("no orphan")
	}
	got, err := GetQueueItem(st, OperatorQueueOrgID, all[0].ID, OperatorQueueNow)
	if err != nil || got == nil {
		t.Fatalf("detail: %v %+v", err, got)
	}
	if got.NextAction == "" && got.Status != StatusOpen {
		t.Fatal("detail missing next_action and is not open")
	}
	if got.Status != StatusOpen {
		t.Fatalf("fresh item status=%s want open", got.Status)
	}
	if len(got.Evidence) == 0 || len(got.History) == 0 {
		t.Fatal("detail missing evidence/history")
	}
	if !containsAction(got.AllowedActions, ResolveLink) {
		t.Fatalf("orphan should allow link: %v", got.AllowedActions)
	}
	fmt.Printf("QUEUE_DETAIL id=%s code=%s next=%q open=%s\n", got.ID, got.Code, got.NextAction, got.Status)
}

func TestResolveLegalActionsIdempotentAndAudited(t *testing.T) {
	st := NewMemoryStore()
	LoadOperatorQueue(st, OperatorQueueOrgID)
	now := OperatorQueueNow
	orphans, _ := ListQueue(st, OperatorQueueOrgID, ExceptionFilter{Type: ExceptionOrphan}, now)
	if len(orphans) == 0 {
		t.Fatal("no orphan")
	}
	orphanID := orphans[0].ID

	link, err := Resolve(st, OperatorQueueOrgID, orphanID, ResolveRequest{
		Action:       ResolveLink,
		Actor:        "operator@confenge",
		Reason:       "matches existing inbound receipt",
		LinkIdentity: "lead:webcfg-syn-in-1",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if link.Refused {
		t.Fatalf("legal link refused: %s", link.Reason)
	}
	if link.After.Status != StatusLinked || link.Before.Status != StatusOpen {
		t.Fatalf("link before/after = %s -> %s", link.Before.Status, link.After.Status)
	}
	if link.Actor != "operator@confenge" || link.Reason == "" {
		t.Fatal("link missing actor/reason")
	}
	if link.Exception.LinkedIdentity != "lead:webcfg-syn-in-1" {
		t.Fatalf("linked identity=%s", link.Exception.LinkedIdentity)
	}

	replay, err := Resolve(st, OperatorQueueOrgID, orphanID, ResolveRequest{
		Action:       ResolveLink,
		Actor:        "operator@confenge",
		Reason:       "matches existing inbound receipt",
		LinkIdentity: "lead:webcfg-syn-in-1",
	}, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if !replay.Replay || replay.Refused {
		t.Fatalf("same command must replay: replay=%v refused=%v %s", replay.Replay, replay.Refused, replay.Reason)
	}
	if replay.After.Status != StatusLinked {
		t.Fatalf("replay changed status to %s", replay.After.Status)
	}

	missing, _ := ListQueue(st, OperatorQueueOrgID, ExceptionFilter{Type: ExceptionMissingVersion}, now)
	if len(missing) == 0 {
		t.Fatal("no missing_version")
	}
	deferRes, err := Resolve(st, OperatorQueueOrgID, missing[0].ID, ResolveRequest{
		Action: ResolveDefer, Actor: "operator@confenge", Reason: "wait for extra-cli watermark",
	}, now)
	if err != nil || deferRes.Refused {
		t.Fatalf("defer: %v %s", err, deferRes.Reason)
	}
	if deferRes.After.Status != StatusDeferred {
		t.Fatalf("defer status=%s", deferRes.After.Status)
	}
	if !isOpenStatus(deferRes.After.Status) {
		t.Fatal("defer must stay nominally open")
	}

	stale, _ := ListQueue(st, OperatorQueueOrgID, ExceptionFilter{Type: ExceptionStaleAttribution}, now)
	if len(stale) == 0 {
		t.Fatal("no stale")
	}
	ext, err := Resolve(st, OperatorQueueOrgID, stale[0].ID, ResolveRequest{
		Action: ResolveExternalEvidence, Actor: "operator@confenge", Reason: "need source system screenshot",
	}, now)
	if err != nil || ext.Refused {
		t.Fatalf("external evidence: %v %s", err, ext.Reason)
	}
	if ext.After.Status != StatusExternalEvidence {
		t.Fatalf("external status=%s", ext.After.Status)
	}

	dup, _ := ListQueue(st, OperatorQueueOrgID, ExceptionFilter{Type: ExceptionDuplicate}, now)
	if len(dup) == 0 {
		t.Fatal("no duplicate")
	}
	rej, err := Resolve(st, OperatorQueueOrgID, dup[0].ID, ResolveRequest{
		Action: ResolveReject, Actor: "operator@confenge", Reason: "duplicate of first chain; no second insert",
	}, now)
	if err != nil || rej.Refused {
		t.Fatalf("reject: %v %s", err, rej.Reason)
	}
	if rej.After.Status != StatusRejected {
		t.Fatalf("reject status=%s", rej.After.Status)
	}

	fmt.Printf("QUEUE_RESOLVE link=%s defer=%s external=%s reject=%s replay=%v\n",
		link.After.Status, deferRes.After.Status, ext.After.Status, rej.After.Status, replay.Replay)
}

func TestResolveRefusesLifecycleAndInventedOutcomes(t *testing.T) {
	st := NewMemoryStore()
	LoadOperatorQueue(st, OperatorQueueOrgID)
	now := OperatorQueueNow

	won, _ := ListQueue(st, OperatorQueueOrgID, ExceptionFilter{Type: ExceptionUnconfirmedWon}, now)
	if len(won) == 0 {
		t.Fatal("no unconfirmed_won")
	}
	inventWon, err := Resolve(st, OperatorQueueOrgID, won[0].ID, ResolveRequest{
		Action: ResolveReject, Actor: "operator@confenge", Reason: "force won", OutcomeType: OutcomeWon,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if !inventWon.Refused || !strings.Contains(inventWon.Reason, "outcome") {
		t.Fatalf("invent WON must refuse: %+v", inventWon)
	}
	still, _ := GetQueueItem(st, OperatorQueueOrgID, won[0].ID, now)
	if still == nil || !isOpenStatus(still.Status) {
		t.Fatalf("invent WON closed the exception: %+v", still)
	}

	inventLost, _ := Resolve(st, OperatorQueueOrgID, won[0].ID, ResolveRequest{
		Action: ResolveReject, Actor: "operator@confenge", Reason: "force lost", OutcomeType: OutcomeLost,
	}, now)
	if !inventLost.Refused {
		t.Fatal("invent LOST must refuse")
	}

	inventRev, _ := Resolve(st, OperatorQueueOrgID, won[0].ID, ResolveRequest{
		Action: ResolveReject, Actor: "operator@confenge", Reason: "book revenue", Revenue: "12000",
	}, now)
	if !inventRev.Refused || !strings.Contains(inventRev.Reason, "revenue") {
		t.Fatalf("invent revenue must refuse: %+v", inventRev)
	}

	inventID, _ := Resolve(st, OperatorQueueOrgID, won[0].ID, ResolveRequest{
		Action: ResolveLink, Actor: "operator@confenge", Reason: "mint identity", Identity: "lead:brand-new",
	}, now)
	if !inventID.Refused {
		t.Fatal("invent identity must refuse")
	}

	linkWon, _ := Resolve(st, OperatorQueueOrgID, won[0].ID, ResolveRequest{
		Action: ResolveLink, Actor: "operator@confenge", Reason: "try to confirm via link",
		LinkIdentity: "lead:webcfg-syn-in-1",
	}, now)
	if !linkWon.Refused {
		t.Fatal("link on unconfirmed_won must refuse")
	}
	afterLink, _ := GetQueueItem(st, OperatorQueueOrgID, won[0].ID, now)
	if afterLink == nil || !isOpenStatus(afterLink.Status) {
		t.Fatal("illegal link closed unconfirmed_won")
	}

	conflict, _ := ListQueue(st, OperatorQueueOrgID, ExceptionFilter{Type: ExceptionConflictingAccount}, now)
	if len(conflict) == 0 {
		t.Fatal("no conflict")
	}
	linkConflict, _ := Resolve(st, OperatorQueueOrgID, conflict[0].ID, ResolveRequest{
		Action: ResolveLink, Actor: "operator@confenge", Reason: "overwrite account",
		LinkIdentity: "lead:webcfg-syn-in-1",
	}, now)
	if !linkConflict.Refused {
		t.Fatal("link on conflicting_account must refuse")
	}

	oo, _ := ListQueue(st, OperatorQueueOrgID, ExceptionFilter{Type: ExceptionOutOfOrder}, now)
	if len(oo) == 0 {
		t.Fatal("no out_of_order")
	}
	linkOO, _ := Resolve(st, OperatorQueueOrgID, oo[0].ID, ResolveRequest{
		Action: ResolveLink, Actor: "operator@confenge", Reason: "reorder",
		LinkIdentity: "lead:webcfg-syn-in-1",
	}, now)
	if !linkOO.Refused {
		t.Fatal("link on out_of_order must refuse")
	}

	bogus, _ := Resolve(st, OperatorQueueOrgID, won[0].ID, ResolveRequest{
		Action: "mark_won", Actor: "operator@confenge", Reason: "invent",
	}, now)
	if !bogus.Refused {
		t.Fatal("unknown action must refuse")
	}

	fmt.Printf("QUEUE_REFUSE won=%q conflict=%q out_of_order=%q invent_identity=%q\n",
		linkWon.Reason, linkConflict.Reason, linkOO.Reason, inventID.Reason)
}

func TestResolveLinkMissingTargetRefused(t *testing.T) {
	st := NewMemoryStore()
	LoadOperatorQueue(st, OperatorQueueOrgID)
	orphans, _ := ListQueue(st, OperatorQueueOrgID, ExceptionFilter{Type: ExceptionOrphan}, OperatorQueueNow)
	got, err := Resolve(st, OperatorQueueOrgID, orphans[0].ID, ResolveRequest{
		Action: ResolveLink, Actor: "operator@confenge", Reason: "guess",
		LinkIdentity: "lead:does-not-exist",
	}, OperatorQueueNow)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Refused {
		t.Fatal("missing link target must refuse")
	}
	open, _ := GetQueueItem(st, OperatorQueueOrgID, orphans[0].ID, OperatorQueueNow)
	if open == nil || open.Status != StatusOpen {
		t.Fatalf("missing target closed orphan: %+v", open)
	}
}

func TestQueueJSONHasRequiredOperatorFields(t *testing.T) {
	st := NewMemoryStore()
	LoadOperatorQueue(st, OperatorQueueOrgID)
	xs, _ := ListQueue(st, OperatorQueueOrgID, ExceptionFilter{}, OperatorQueueNow)
	raw, err := json.Marshal(xs)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	for _, key := range []string{
		"code", "lane", "source", "severity", "age_seconds", "next_action",
		"status", "evidence", "history", "allowed_actions",
	} {
		if !strings.Contains(body, `"`+key+`"`) {
			t.Fatalf("json missing %s", key)
		}
	}
	if strings.Contains(body, `"mark_won"`) || strings.Contains(body, `"invent"`) {
		t.Fatal("json advertised an invent action")
	}
}

func containsAction(xs []string, want string) bool {
	for _, a := range xs {
		if a == want {
			return true
		}
	}
	return false
}
