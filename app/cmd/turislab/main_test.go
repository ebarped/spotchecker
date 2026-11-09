package main

import (
	"slices"
	"testing"

	"github.com/datadope/turislab-doctor/internal/turislab/config"
	"github.com/datadope/turislab-doctor/internal/turislab/model"
	"github.com/datadope/turislab-doctor/internal/turislab/scoring"
)

func TestScoringConfigDefaultsWhenEmpty(t *testing.T) {
	sc := scoringConfig(config.Config{})
	def := scoring.DefaultScoringConfig()
	if sc != def {
		t.Errorf("empty config must yield the default scoring config:\n got %+v\nwant %+v", sc, def)
	}
}

func TestScoringConfigMapsPriorityWeights(t *testing.T) {
	cfg := config.Config{}
	cfg.Scoring.PriorityWeights = config.PriorityWeights{
		ClosureRisk:  0.50,
		QualityRisk:  0.20,
		QualityDelta: 0.10,
		Staleness:    0.10,
		Impact:       0.05,
		Complaints:   0.05,
	}

	sc := scoringConfig(cfg)
	if sc.WClosureRisk != 0.50 {
		t.Errorf("WClosureRisk = %v, want 0.50", sc.WClosureRisk)
	}
	if sc.WQualityRisk != 0.20 {
		t.Errorf("WQualityRisk = %v, want 0.20", sc.WQualityRisk)
	}
	if sc.WQualityDelta != 0.10 {
		t.Errorf("WQualityDelta = %v, want 0.10", sc.WQualityDelta)
	}
	if sc.WStaleness != 0.10 {
		t.Errorf("WStaleness = %v, want 0.10", sc.WStaleness)
	}
	if sc.WImpact != 0.05 {
		t.Errorf("WImpact = %v, want 0.05", sc.WImpact)
	}
	if sc.WComplaints != 0.05 {
		t.Errorf("WComplaints = %v, want 0.05", sc.WComplaints)
	}
}

func TestScoringConfigZeroClosureRiskKeepsDefaults(t *testing.T) {
	// Only ClosureRisk > 0 unlocks the whole priority block; a zeroed
	// ClosureRisk must leave the defaults untouched.
	cfg := config.Config{}
	cfg.Scoring.PriorityWeights = config.PriorityWeights{QualityRisk: 0.9}

	sc := scoringConfig(cfg)
	if sc.WQualityRisk != scoring.DefaultScoringConfig().WQualityRisk {
		t.Errorf("WQualityRisk = %v, want the default", sc.WQualityRisk)
	}
}

func TestScoringConfigMapsFamilyWeights(t *testing.T) {
	cfg := config.Config{}
	cfg.Scoring.ClosureFamilyWeights = config.ClosureFamilyWeights{
		DirectStatus: 0.60, Activity: 0.25, Context: 0.10, Official: 0.05,
	}
	cfg.Scoring.QualityFamilyWeights = config.QualityFamilyWeights{
		Reviews: 0.50, Official: 0.20, Product: 0.15, News: 0.10, Freshness: 0.05,
	}

	sc := scoringConfig(cfg)
	if sc.WDirectStatus != 0.60 || sc.WActivity != 0.25 || sc.WContext != 0.10 || sc.WOfficial != 0.05 {
		t.Errorf("closure weights = %v/%v/%v/%v", sc.WDirectStatus, sc.WActivity, sc.WContext, sc.WOfficial)
	}
	if sc.WReviews != 0.50 || sc.WOfficialQ != 0.20 || sc.WProduct != 0.15 || sc.WNews != 0.10 || sc.WFreshness != 0.05 {
		t.Errorf("quality weights = %v/%v/%v/%v/%v", sc.WReviews, sc.WOfficialQ, sc.WProduct, sc.WNews, sc.WFreshness)
	}
}

func TestScoringConfigMapsTuningAndTiers(t *testing.T) {
	cfg := config.Config{}
	cfg.Scoring.ConfidenceFactor = 0.8
	cfg.Scoring.HysteresisWeight = 0.3
	cfg.Scoring.VolatileDelta = 0.05
	cfg.Tiers = config.TierConfig{
		P0Min: 0.9, P1Min: 0.7, P2Min: 0.5, MinConfidence: 0.6, CollectDataConfidence: 0.35,
	}

	sc := scoringConfig(cfg)
	if sc.ConfidenceFactor != 0.8 || sc.HysteresisWeight != 0.3 || sc.VolatileDelta != 0.05 {
		t.Errorf("tuning = %v/%v/%v", sc.ConfidenceFactor, sc.HysteresisWeight, sc.VolatileDelta)
	}
	tc := sc.TierConfig
	if tc.P0Min != 0.9 || tc.P1Min != 0.7 || tc.P2Min != 0.5 {
		t.Errorf("tier minimums = %v/%v/%v", tc.P0Min, tc.P1Min, tc.P2Min)
	}
	if tc.MinConfidenceP0 != 0.6 || tc.CollectDataConfidence != 0.35 {
		t.Errorf("tier confidences = %v/%v", tc.MinConfidenceP0, tc.CollectDataConfidence)
	}
}

// The waiting tail of the queue must follow the upcoming scan order: the
// scanner walks the list starting right after the cursor, so rotating from
// cursor+1 puts the next target first. Scored establishments never appear
// in the waiting tail.
func TestWaitingOrderFollowsTheScanCursor(t *testing.T) {
	ests := []*model.Establishment{
		{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"}, {ID: "e"},
	}
	byID := map[string]*model.Score{"b": {EstablishmentID: "b"}, "e": {EstablishmentID: "e"}}

	ids := func(list []*model.Establishment) []string {
		out := make([]string, 0, len(list))
		for _, e := range list {
			out = append(out, e.ID)
		}
		return out
	}

	// Cursor on "c": the scan continues at "d", wraps to "a", skips "b"
	// (scored) and "e" (scored); "c" itself comes back last, after the
	// rotation wraps around.
	got := waitingOrder(ests, byID, "c")
	if want := []string{"d", "a", "c"}; !slices.Equal(ids(got), want) {
		t.Errorf("waitingOrder(cursor=c) = %v, want %v", ids(got), want)
	}

	// Nothing scanned yet: natural order minus the scored ones.
	got = waitingOrder(ests, byID, "")
	if want := []string{"a", "c", "d"}; !slices.Equal(ids(got), want) {
		t.Errorf("waitingOrder(no cursor) = %v, want %v", ids(got), want)
	}

	// Unknown cursor: same as none.
	got = waitingOrder(ests, byID, "zzz")
	if want := []string{"a", "c", "d"}; !slices.Equal(ids(got), want) {
		t.Errorf("waitingOrder(unknown cursor) = %v, want %v", ids(got), want)
	}
}
