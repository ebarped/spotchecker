package model_test

import (
	"testing"

	"github.com/datadope/turislab-doctor/internal/turislab/model"
)

func TestAssignTierRules(t *testing.T) {
	cfg := model.DefaultTierConfig()

	cases := []struct {
		name       string
		priority   float64
		confidence float64
		want       model.TierLabel
	}{
		{"high priority high confidence", 90, 0.8, model.TierP0},
		{"high priority low confidence capped to P1", 90, 0.4, model.TierP1},
		{"P1 range", 40, 0.8, model.TierP1},
		{"P2 range", 25, 0.8, model.TierP2},
		{"P3 range", 10, 0.8, model.TierP3},
		{"too little confidence to act", 90, 0.30, model.TierCollectData},
		{"zero everything", 0, 0, model.TierCollectData},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			score := model.NewScore("e1")
			score.Priority = tc.priority
			score.Confidence = tc.confidence
			score.AssignTier(cfg)

			if score.Tier != tc.want {
				t.Errorf("tier = %q, want %q (priority=%.0f confidence=%.2f)", score.Tier, tc.want, tc.priority, tc.confidence)
			}
		})
	}
}

func TestNewScoreDefaults(t *testing.T) {
	score := model.NewScore("e1")
	if score.EstablishmentID != "e1" {
		t.Errorf("establishment_id = %q", score.EstablishmentID)
	}
	if score.Status != model.StatusUnknown {
		t.Errorf("status = %q, want unknown", score.Status)
	}
	if score.ComputedAt.IsZero() {
		t.Error("computed_at must be set")
	}
}
