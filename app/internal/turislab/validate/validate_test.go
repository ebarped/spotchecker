package validate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datadope/turislab-doctor/internal/turislab/model"
)

func score(id string, priority float64, status model.StatusSuggested, tier model.TierLabel) *model.Score {
	return &model.Score{
		EstablishmentID: id,
		Priority:        priority,
		Status:          status,
		Tier:            tier,
		ClosureRisk:     priority / 50,
	}
}

func sampleGroundTruth() *GroundTruth {
	return &GroundTruth{Establishments: []Entry{
		{ID: "closed-a", Name: "Cerrado A", GroundTruthStatus: StatusClosedPermanently},
		{ID: "closed-b", Name: "Cerrado B", GroundTruthStatus: StatusClosedPermanently},
		{ID: "active-a", Name: "Activo A", GroundTruthStatus: StatusActive},
		{ID: "active-b", Name: "Activo B", GroundTruthStatus: StatusActive},
		{ID: "suspect", Name: "Sospecha", GroundTruthStatus: StatusClosureSuspected},
	}}
}

func TestRunClosedInTopNPasses(t *testing.T) {
	scores := []*model.Score{
		score("closed-a", 45.75, model.StatusClosureConfirmed, model.TierP0),
		score("closed-b", 44.00, model.StatusClosureConfirmed, model.TierP1),
		score("suspect", 30.00, model.StatusClosureSuspected, model.TierP2),
		score("active-a", 9.15, model.StatusActive, model.TierP3),
		score("active-b", 4.00, model.StatusActive, model.TierP3),
	}

	res := Run(sampleGroundTruth(), scores, Options{})

	if !res.Passed {
		t.Fatalf("expected pass, got %+v", res)
	}
	if res.ClosedInTopN != 2 || res.ClosedKnown != 2 {
		t.Errorf("closed in top N = %d/%d, want 2/2", res.ClosedInTopN, res.ClosedKnown)
	}
	if res.HitRate != 1 {
		t.Errorf("hit rate = %v, want 1", res.HitRate)
	}
	if len(res.FalsePositives) != 0 {
		t.Errorf("false positives = %v, want none", res.FalsePositives)
	}
	if res.SuspectedVisited != 1 {
		t.Errorf("suspected in top N = %d, want 1", res.SuspectedVisited)
	}
}

func TestRunClosedOutsideTopNFails(t *testing.T) {
	scores := []*model.Score{
		score("active-a", 50.00, model.StatusActive, model.TierP0),
		score("active-b", 49.00, model.StatusActive, model.TierP0),
		score("suspect", 48.00, model.StatusClosureSuspected, model.TierP0),
		score("closed-a", 20.00, model.StatusClosureSuspected, model.TierP2),
		score("closed-b", 10.00, model.StatusActive, model.TierP3),
	}

	res := Run(sampleGroundTruth(), scores, Options{})

	if res.Passed {
		t.Fatal("expected failure when known closures are outside the top 3")
	}
	if res.ClosedInTopN != 0 {
		t.Errorf("closed in top N = %d, want 0", res.ClosedInTopN)
	}
	if res.HitRate != 0 {
		t.Errorf("hit rate = %v, want 0", res.HitRate)
	}
}

func TestRunFalsePositiveFailsWhenConfigured(t *testing.T) {
	scores := []*model.Score{
		score("closed-a", 46, model.StatusClosureConfirmed, model.TierP0),
		score("closed-b", 45, model.StatusClosureConfirmed, model.TierP0),
		score("active-a", 44, model.StatusClosureConfirmed, model.TierP0),
		score("active-b", 10, model.StatusActive, model.TierP3),
		score("suspect", 5, model.StatusClosureSuspected, model.TierP3),
	}

	lenient := Run(sampleGroundTruth(), scores, Options{})
	if !lenient.Passed {
		t.Fatalf("expected pass without false-positive gate: %+v", lenient)
	}
	if len(lenient.FalsePositives) != 1 || lenient.FalsePositives[0] != "active-a" {
		t.Errorf("false positives = %v, want [active-a]", lenient.FalsePositives)
	}

	strict := Run(sampleGroundTruth(), scores, Options{FailOnFalsePositive: true})
	if strict.Passed {
		t.Error("expected failure with the false-positive gate enabled")
	}
}

func TestRunMissingScoresAreReported(t *testing.T) {
	scores := []*model.Score{
		score("closed-a", 46, model.StatusClosureConfirmed, model.TierP0),
	}

	res := Run(sampleGroundTruth(), scores, Options{})

	if res.Scored != 1 {
		t.Errorf("scored = %d, want 1", res.Scored)
	}
	if len(res.Missing) != 4 {
		t.Errorf("missing = %v, want 4 ids", res.Missing)
	}
	if res.Passed {
		t.Error("expected failure when the sample is only partially scanned")
	}
}

func TestRunRanksByPriorityDescending(t *testing.T) {
	scores := []*model.Score{
		score("active-a", 9, model.StatusActive, model.TierP3),
		score("closed-a", 46, model.StatusClosureConfirmed, model.TierP0),
		score("closed-b", 44, model.StatusClosureConfirmed, model.TierP1),
	}

	res := Run(sampleGroundTruth(), scores, Options{})
	rank := map[string]int{}
	for _, row := range res.Rows {
		rank[row.ID] = row.Rank
	}
	if rank["closed-a"] != 1 || rank["closed-b"] != 2 || rank["active-a"] != 3 {
		t.Errorf("unexpected ranks: %v", rank)
	}
}

func TestRunNoVerifiedClosureCannotPass(t *testing.T) {
	gt := &GroundTruth{Establishments: []Entry{
		{ID: "a", GroundTruthStatus: StatusActive},
	}}
	res := Run(gt, []*model.Score{score("a", 9, model.StatusActive, model.TierP3)}, Options{})
	if res.Passed {
		t.Error("validation without any verified closure must not pass")
	}
}

func TestLoadRejectsUnknownStatusAndDuplicates(t *testing.T) {
	dir := t.TempDir()

	unknown := filepath.Join(dir, "unknown.json")
	if err := os.WriteFile(unknown, []byte(`{"establishments":[{"id":"a","ground_truth_status":"gone"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(unknown); err == nil || !strings.Contains(err.Error(), "unknown ground_truth_status") {
		t.Errorf("Load(unknown status) = %v, want unknown status error", err)
	}

	dup := filepath.Join(dir, "dup.json")
	if err := os.WriteFile(dup, []byte(`{"establishments":[{"id":"a","ground_truth_status":"active"},{"id":"a","ground_truth_status":"active"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dup); err == nil || !strings.Contains(err.Error(), "duplicate id") {
		t.Errorf("Load(duplicate) = %v, want duplicate error", err)
	}
}

func TestLoadValidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gt.json")
	body := `{"establishments":[{"id":"a","name":"A","ground_truth_status":"closed_permanently"}]}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	gt, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(gt.Establishments) != 1 || gt.Establishments[0].ID != "a" {
		t.Errorf("ground truth = %+v", gt)
	}
}

func TestStringMentionsOutcome(t *testing.T) {
	scores := []*model.Score{
		score("closed-a", 46, model.StatusClosureConfirmed, model.TierP0),
		score("closed-b", 45, model.StatusClosureConfirmed, model.TierP0),
		score("active-a", 9, model.StatusActive, model.TierP3),
		score("active-b", 8, model.StatusActive, model.TierP3),
		score("suspect", 7, model.StatusClosureSuspected, model.TierP3),
	}
	out := Run(sampleGroundTruth(), scores, Options{}).String()
	for _, want := range []string{"CRITERIO PoC CUMPLIDO", "Cerrados conocidos en el top 3: 2/2"} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q:\n%s", want, out)
		}
	}
}
