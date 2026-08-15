package intel

import (
	"fmt"
	"testing"
)

func TestLearningCandidateFromCorrectionAndOutcome(t *testing.T) {
	st := NewMemoryStore()
	keys := JoinKeys{
		OrganizationID: "org",
		LeadID:         "lead-learn",
		AssetID:        "landing-segunda-leitura",
		OfferID:        "segunda-leitura",
		ActionID:       "act-learn",
		OutcomeID:      "out-learn",
	}
	fromCorr := EmitLearning(st, LearningInput{
		From:            LearningFromCorrection,
		Reason:          "wrong_service",
		CorrectionCodes: []string{"wrong_service"},
		Keys:            keys,
		Synthetic:       true,
	})
	if fromCorr.Kind != LearningKind {
		t.Fatalf("kind=%s", fromCorr.Kind)
	}
	if fromCorr.Target != TargetOffer {
		t.Fatalf("correction target=%s want offer", fromCorr.Target)
	}
	if len(fromCorr.UpstreamWrites) != 0 {
		t.Fatalf("upstream writes attempted: %v", fromCorr.UpstreamWrites)
	}
	if fromCorr.Status != LearningPending {
		t.Fatalf("status=%s", fromCorr.Status)
	}

	fromOut := EmitLearning(st, LearningInput{
		From:        LearningFromOutcome,
		OutcomeType: OutcomeQualifiedConversation,
		Keys:        keys,
		Synthetic:   true,
	})
	if fromOut.Target != TargetDemand {
		t.Fatalf("outcome target=%s want demand", fromOut.Target)
	}
	if len(fromOut.UpstreamWrites) != 0 {
		t.Fatalf("outcome attempted upstream write: %v", fromOut.UpstreamWrites)
	}

	unknown := EmitLearning(st, LearningInput{From: LearningFromOutcome, OutcomeType: OutcomeUnknown})
	if unknown.Target != Unknown && unknown.OutcomeType != OutcomeUnknown {
		t.Fatalf("UNKNOWN input invented target=%s outcome=%s", unknown.Target, unknown.OutcomeType)
	}

	unconfirmed := EmitLearning(st, LearningInput{
		From: LearningFromOutcome, OutcomeType: OutcomeWon, HumanConfirmed: false, Keys: keys,
	})
	if unconfirmed.OutcomeType == OutcomeWon && unconfirmed.Target == TargetOffer {
		t.Fatal("unconfirmed WON must not become an offer learning fact")
	}

	got, _ := st.ListLearning("")
	if len(got) < 2 {
		t.Fatalf("learning rows=%d", len(got))
	}
	if fromCorr.Recommendation != LearningChange {
		t.Fatalf("correction rec=%s want change", fromCorr.Recommendation)
	}
	if fromOut.Recommendation != LearningRepeat {
		t.Fatalf("qco rec=%s want repeat", fromOut.Recommendation)
	}
	if fromCorr.CausalProof || fromOut.CausalProof {
		t.Fatal("causal_proof must stay false")
	}
	fmt.Printf("LEARNING correction_target=%s outcome_target=%s rec=%s/%s upstream_writes=%d unknown_stays=%s causal_proof=false\n",
		fromCorr.Target, fromOut.Target, fromCorr.Recommendation, fromOut.Recommendation, len(fromCorr.UpstreamWrites)+len(fromOut.UpstreamWrites), unknown.Target)
}

func TestLearningDoesNotCallOtherRepos(t *testing.T) {
	// EmitLearning only talks to Store. A nil store still returns a local
	// candidate and performs no extra-cli / web-cfg / SmartLic write.
	cand := EmitLearning(nil, LearningInput{
		From:            LearningFromCorrection,
		CorrectionCodes: []string{"weak_hook"},
		Keys:            JoinKeys{AssetID: "landing-x"},
	})
	if cand.Kind != LearningKind || cand.Target != TargetContent {
		t.Fatalf("nil-store candidate broken: %+v", cand)
	}
	if len(cand.UpstreamWrites) != 0 {
		t.Fatalf("nil-store wrote upstream: %v", cand.UpstreamWrites)
	}
}
