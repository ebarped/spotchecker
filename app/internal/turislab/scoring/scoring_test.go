package scoring_test

import (
	"strings"
	"testing"
	"time"

	"github.com/datadope/turislab-doctor/internal/turislab/model"
	"github.com/datadope/turislab-doctor/internal/turislab/scoring"
)

func engine() *scoring.Engine {
	return scoring.NewEngine(scoring.DefaultScoringConfig())
}

func est(id string) *model.Establishment {
	return &model.Establishment{ID: id, Name: "Test " + id}
}

func signal(kind model.SignalKind, family model.SignalFamily, rawValue string, value, confidence, weight float64) *model.Signal {
	return &model.Signal{
		ID:          string(kind) + "-" + rawValue,
		Kind:        kind,
		Family:      family,
		RawValue:    rawValue,
		Value:       value,
		Confidence:  confidence,
		Weight:      weight,
		CollectedAt: time.Now(),
	}
}

func closedSignal() *model.Signal {
	s := signal(model.KindBusinessStatus, model.FamilyDirectStatus, "permanently_closed", 1.0, 0.75, 0.90)
	return s
}

func openSignal() *model.Signal {
	return signal(model.KindBusinessStatus, model.FamilyDirectStatus, "open", 0.0, 0.75, 0.90)
}

func TestNoSignalsGivesCollectData(t *testing.T) {
	score := engine().Compute(est("e1"), nil, nil, 730)
	if score.Status != model.StatusUnknown {
		t.Errorf("status = %q, want unknown", score.Status)
	}
	if score.Tier != model.TierCollectData {
		t.Errorf("tier = %q, want collect_data", score.Tier)
	}
	if score.Reason == "" {
		t.Error("reason must not be empty even without signals")
	}
}

func TestClosedSignalDrivesHighClosureRisk(t *testing.T) {
	score := engine().Compute(est("e1"), []*model.Signal{closedSignal()}, nil, 100)

	if score.ClosureRisk < 0.80 {
		t.Errorf("closure_risk = %.2f, want >= 0.80 with a direct closed signal", score.ClosureRisk)
	}
	if score.Status != model.StatusClosureConfirmed {
		t.Errorf("status = %q, want closure_confirmed", score.Status)
	}
	if score.Tier == model.TierCollectData {
		t.Errorf("tier = collect_data, but there is high-confidence evidence")
	}
	// PoC-calibrated tiers: a confirmed closure with a somewhat stale
	// inspection must at least reach P1.
	if score.Tier != model.TierP0 && score.Tier != model.TierP1 {
		t.Errorf("tier = %q, want P0 or P1 for a confirmed closure", score.Tier)
	}
	if score.Priority < 35 {
		t.Errorf("priority = %.1f, want >= 35 for a confirmed closure", score.Priority)
	}
}

func TestOpenSignalKeepsClosureRiskLow(t *testing.T) {
	score := engine().Compute(est("e1"), []*model.Signal{openSignal()}, nil, 30)

	if score.ClosureRisk > 0.35 {
		t.Errorf("closure_risk = %.2f, want <= 0.35 with a direct open signal", score.ClosureRisk)
	}
	if score.Status != model.StatusActive {
		t.Errorf("status = %q, want active", score.Status)
	}
}

func TestConflictingSourcesAreFlaggedAndCapped(t *testing.T) {
	signals := []*model.Signal{closedSignal(), openSignal()}
	score := engine().Compute(est("e1"), signals, nil, 100)

	if !score.ConflictDetected {
		t.Error("conflict_detected = false, want true with closed+open from different sources")
	}
	if score.ClosureRisk > 0.70 {
		t.Errorf("closure_risk = %.2f, want capped at 0.70 under conflict", score.ClosureRisk)
	}
	if got := score.Reason; !strings.HasPrefix(got, "⚠") {
		t.Errorf("reason = %q, want the conflict warning prefix", got)
	}
}

func TestStaleInspectionRaisesPriority(t *testing.T) {
	fresh := engine().Compute(est("e1"), []*model.Signal{openSignal()}, nil, 30)
	stale := engine().Compute(est("e1"), []*model.Signal{openSignal()}, nil, 1000)

	if stale.Priority <= fresh.Priority {
		t.Errorf("priority with stale inspection = %.1f, want > fresh %.1f", stale.Priority, fresh.Priority)
	}
}

func TestOldSignalsDecay(t *testing.T) {
	// A single signal cannot show decay: normalisation cancels it. With two
	// contradictory signals, decay decides which one dominates.
	mk := func(closedAge time.Duration) []*model.Signal {
		closed := closedSignal()
		closed.CollectedAt = time.Now().Add(-closedAge)
		open := openSignal()
		return []*model.Signal{closed, open}
	}

	fresh := engine().Compute(est("e1"), mk(0), nil, 100)
	decayed := engine().Compute(est("e1"), mk(180*24*time.Hour), nil, 100)

	if decayed.ClosureRisk >= fresh.ClosureRisk {
		t.Errorf("decayed closure_risk = %.2f, want < fresh %.2f (the aged closed signal must lose weight)", decayed.ClosureRisk, fresh.ClosureRisk)
	}
}

func TestHysteresisSmoothing(t *testing.T) {
	// Priority without previous score, for an open signal and a fresh
	// inspection: small (staleness only).
	baseline := engine().Compute(est("e1"), []*model.Signal{openSignal()}, nil, 30)

	prev := model.NewScore("e1")
	prev.Priority = baseline.Priority + 10 // delta < 15: no volatile flag

	score := engine().Compute(est("e1"), []*model.Signal{openSignal()}, prev, 30)

	if score.Volatile {
		t.Errorf("volatile = true with a delta of %.1f, want false", baseline.Priority+10-baseline.Priority)
	}
	if score.Priority == baseline.Priority {
		t.Errorf("hysteresis had no effect: %.2f == %.2f", score.Priority, baseline.Priority)
	}
	// The smoothed value must sit between the raw score and the previous one.
	if score.Priority <= baseline.Priority || score.Priority >= prev.Priority {
		t.Errorf("smoothed priority = %.2f, want between %.2f and %.2f", score.Priority, baseline.Priority, prev.Priority)
	}
}

func TestVolatileFlag(t *testing.T) {
	prev := model.NewScore("e1")
	prev.Priority = 10

	signals := []*model.Signal{closedSignal()}
	score := engine().Compute(est("e1"), signals, prev, 500)

	if !score.Volatile {
		t.Errorf("volatile = false, want true when priority jumps by >= %.0f", scoring.DefaultScoringConfig().VolatileDelta)
	}
}

func TestClampStaysInBounds(t *testing.T) {
	// Many weak signals must not push scores out of [0, 1].
	var signals []*model.Signal
	for range 20 {
		signals = append(signals, signal(model.KindWebKeyword, model.FamilyDirectStatus, "cerrado", 1.0, 0.65, 0.5))
	}
	score := engine().Compute(est("e1"), signals, nil, 100)

	if score.ClosureRisk > 1 || score.ClosureRisk < 0 {
		t.Errorf("closure_risk = %.2f, out of bounds", score.ClosureRisk)
	}
	if score.Priority > 100 || score.Priority < 0 {
		t.Errorf("priority = %.2f, out of bounds", score.Priority)
	}
}

func TestErroredSignalsAreIgnored(t *testing.T) {
	broken := closedSignal()
	broken.Error = "source failed"

	score := engine().Compute(est("e1"), []*model.Signal{broken}, nil, 100)
	if score.Status != model.StatusUnknown {
		t.Errorf("status = %q, want unknown when every signal errored", score.Status)
	}
}
