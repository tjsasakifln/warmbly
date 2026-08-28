package confenge

import (
	"os"
	"strings"
	"testing"
)

func TestRetireStaleAcceptsFirstTouchV2(t *testing.T) {
	b, err := os.ReadFile("delegated_first_touch_refresh.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, "DelegatedFirstTouchPolicyV2") || !strings.Contains(s, "DelegatedFirstTouchPolicyHashV2") {
		t.Fatal("retireStale must keep v2 approvals; hardcoding v1 cancelled live DELEGATED_POLICY_APPROVE")
	}
	if !strings.Contains(s, "d.policy_version NOT IN (") {
		t.Fatal("v1 remains valid; v2 is additive")
	}
}

func TestRetireStaleIgnoresAcquisitionProvenance(t *testing.T) {
	b, err := os.ReadFile("delegated_first_touch_refresh.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, provenance := range []string{
		"a.source_run_id<>",
		"d.evidence_source_run_id<>",
		"d.source_snapshot_hash<>",
		"d.source_freshness_hash<>",
		"d.source_expires_at IS DISTINCT FROM",
	} {
		if strings.Contains(s, provenance) {
			t.Fatalf("%s is acquisition provenance and must not retire a qualified decision", provenance)
		}
	}
	if !strings.Contains(s, "a.commercial_qualification_state<>'QUALIFIED'") {
		t.Fatal("retirement must gate on the commercial fact, not on which run emitted the row")
	}
	// A build identity and a population revision are provenance too. Comparing
	// them retired every approved touch on each deploy and on each republish of
	// the same membership: six release cohorts, 7141 qualified decisions, and a
	// production first-touch queue that could never survive long enough to send.
	for _, provenance := range []string{
		"d.runtime_release_sha<>",
		"d.target_membership_hash<>",
		"d.target_membership_count<>",
	} {
		if strings.Contains(s, provenance) {
			t.Fatalf("%s is build or import provenance and must not retire a qualified decision", provenance)
		}
	}
	for _, integrity := range []string{"d.policy_hash<>", "d.policy_version NOT IN ("} {
		if !strings.Contains(s, integrity) {
			t.Fatalf("%s is a policy binding integrity term and must stay", integrity)
		}
	}
}

// Retirement cancels durable queued work, so it must require positive proof of
// revocation. A feed that could not be read has revoked nothing; sweeping on it
// cancelled the whole org queue on a transient database error.
func TestRetireStaleDoesNotSweepOnUnreadableFeed(t *testing.T) {
	b, err := os.ReadFile("delegated_first_touch_refresh.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, "if feedErr != nil {\n\t\treturn 0, feedErr\n\t}") {
		t.Fatal("a feed read error must abort the sweep, not fall through into a total cancel")
	}
	if strings.Contains(s, "OR NOT $9") {
		t.Fatal("absence of a qualified attestation is not revocation; the sweep must be symmetric with the approval path")
	}
	if !strings.Contains(s, "authorityRevoked := authority.Present && authority.State != CommercialQualified") {
		t.Fatal("only a present-and-unqualified attestation retires population-wide")
	}
}

// The autorun must not conflate "policy could not be read" with "policy revoked".
func TestAutorunDoesNotRetireOnPolicyReadError(t *testing.T) {
	b, err := os.ReadFile("delegated_first_touch_worker.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if strings.Contains(s, "if err != nil || auth == nil {") {
		t.Fatal("a transient policy read error must not take the retirement branch")
	}
	if !strings.Contains(s, "var noAuthorization *uuid.UUID") {
		t.Fatal("the no-active-policy sentinel must be explicit, not a bare nil variadic")
	}
}

// 'attempted' is the terminal hand-off state, not canonical drift. Omitting it
// made every backend restart un-enrol correctly dispatched touches and strip
// their approval.
func TestLedgerReconcilerTreatsAttemptedAsValidTransit(t *testing.T) {
	b, err := os.ReadFile("delegated_first_touch.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if strings.Contains(s, "'queued','reserved','sent'") {
		t.Fatal("a queue row in 'attempted' is valid transit and must not be reconciled as drift")
	}
	if !strings.Contains(s, "'queued','reserved','attempted','sent'") {
		t.Fatal("the transit allow-list must include the attempted hand-off state")
	}
}

// An attempted row whose touchpoint lost approval can never receive an outcome,
// and Enqueue treats 'attempted' as terminal, so it would block that draft
// forever.
func TestAttemptedReconcilerRecoversOrphanedRows(t *testing.T) {
	b, err := os.ReadFile("dispatch_queue_worker.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, "touchpoint_reverted_before_provider_outcome") {
		t.Fatal("an approval-stripped attempted row must reach a terminal, re-queueable state")
	}
	if strings.Contains(s, "models.TouchpointOpenStates[tp.State]") {
		t.Fatal("QUEUED is the healthy in-flight state and must not be failed as an orphan")
	}
}

func TestNextDelegatedCandidateSkipsCancelledMatchingBinding(t *testing.T) {
	b, err := os.ReadFile("delegated_first_touch_worker.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if strings.Contains(s, "d.state<>'CANCELLED'") {
		t.Fatal("a CANCELLED v2 approval with the current binding must not be selected again; that stalls the autorun burst")
	}
}

// The transport-time assertion must not treat the runtime release sha as
// authority either. It cancelled a queued first touch the moment a new release
// shipped: production cancelled construtoralsg@hotmail.com 16 seconds after it
// came due, with policy_or_authority_drift, because the decision was stamped by
// the previous release.
func TestTransportAssertionIgnoresRuntimeReleaseSHA(t *testing.T) {
	b, err := os.ReadFile("delegated_first_touch.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if strings.Contains(s, "got.RuntimeReleaseSHA != s.cfg.RepositorySHA") {
		t.Fatal("a deploy is not authority drift; the release sha must not cancel a queued first touch at transport")
	}
	for _, binding := range []string{
		"got.PolicyHash != expected",
		"got.AuthorityReference != DelegatedFirstTouchAuthorityRef",
		"got.EvidenceVersion != DelegatedFirstTouchEvidenceV1",
	} {
		if !strings.Contains(s, binding) {
			t.Fatalf("%s is a real authority binding and must stay", binding)
		}
	}
}

// A republish of the population over the same members must not cancel a queued
// decision at transport either, for the same reason the retire sweep no longer
// compares it: revocation is per account, not per attestation revision.
func TestTransportAssertionIgnoresMembershipRevision(t *testing.T) {
	b, err := os.ReadFile("delegated_first_touch.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if strings.Contains(s, "source_authority_binding_drift") {
		t.Fatal("an import revision is not revocation; it must not cancel a queued first touch at transport")
	}
	// The per-account commercial fact is what actually gates transport.
	if !strings.Contains(s, "AccountCommercialQualification(acc, now); !qual.AllowsTransport()") {
		t.Fatal("the three-year commercial qualification must still gate transport")
	}
}
