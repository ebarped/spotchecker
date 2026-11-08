// Package scoring computes closure_risk, quality_risk, quality_delta and priority.
package scoring

import (
	"math"
	"time"

	"github.com/datadope/turislab-doctor/internal/turislab/model"
)

type ScoringConfig struct {
	WClosureRisk      float64
	WQualityRisk      float64
	WQualityDelta     float64
	WStaleness        float64
	WImpact           float64
	WComplaints       float64
	WDirectStatus     float64
	WActivity         float64
	WContext          float64
	WOfficial         float64
	WReviews          float64
	WOfficialQ        float64
	WProduct          float64
	WNews             float64
	WFreshness        float64
	ConfidenceFactor  float64
	HysteresisWeight  float64
	VolatileDelta     float64
	TierConfig        model.TierConfig
	DecayHalfLifeDays float64
}

func DefaultScoringConfig() ScoringConfig {
	return ScoringConfig{
		WClosureRisk:      0.40,
		WQualityRisk:      0.25,
		WQualityDelta:     0.15,
		WStaleness:        0.10,
		WImpact:           0.05,
		WComplaints:       0.05,
		WDirectStatus:     0.45,
		WActivity:         0.25,
		WContext:          0.15,
		WOfficial:         0.15,
		WReviews:          0.35,
		WOfficialQ:        0.35,
		WProduct:          0.15,
		WNews:             0.10,
		WFreshness:        0.05,
		ConfidenceFactor:  0.3,
		HysteresisWeight:  0.3,
		VolatileDelta:     15.0,
		TierConfig:        model.DefaultTierConfig(),
		DecayHalfLifeDays: 30.0,
	}
}

type Engine struct {
	cfg ScoringConfig
}

func NewEngine(cfg ScoringConfig) *Engine {
	return &Engine{cfg: cfg}
}

func (e *Engine) Compute(est *model.Establishment, signals []*model.Signal, prevScore *model.Score, lastInspectionDays float64) *model.Score {
	score := model.NewScore(est.ID)

	// Signals carrying an error are not evidence: they must neither raise
	// confidence nor flip the status toward active.
	var usable []*model.Signal
	for _, sig := range signals {
		if sig.Error == "" {
			usable = append(usable, sig)
		}
	}
	score.EvidenceCount = len(usable)

	if len(usable) == 0 {
		score.Confidence = 0.1
		score.Reason = "Sin señales válidas recogidas"
		score.AssignTier(e.cfg.TierConfig)
		return score
	}

	now := time.Now()
	var totalConfidence float64
	var directClosureFound bool
	var directOpenFound bool

	closureRaw := 0.0
	closureWeightSum := 0.0

	for _, sig := range usable {
		totalConfidence += sig.Confidence

		ageDays := now.Sub(sig.CollectedAt).Hours() / 24.0
		if ageDays < 0 {
			ageDays = 0
		}
		decay := math.Pow(0.5, ageDays/e.cfg.DecayHalfLifeDays)

		if sig.Family == model.FamilyDirectStatus || sig.Kind == model.KindBusinessStatus {
			if sig.RawValue == "permanently_closed" || sig.RawValue == "closed" {
				directClosureFound = true
			} else if sig.RawValue == "open" {
				directOpenFound = true
			}
			w := sig.Weight * sig.Confidence * decay
			closureRaw += sig.Value * w
			closureWeightSum += w
		}
	}

	if closureWeightSum > 0 {
		score.ClosureRisk = clamp(closureRaw/closureWeightSum, 0, 1)
	}

	if directClosureFound && directOpenFound {
		score.ConflictDetected = true
		if score.ClosureRisk > 0.70 {
			score.ClosureRisk = 0.70
		}
	} else if directClosureFound {
		if score.ClosureRisk < 0.80 {
			score.ClosureRisk = 0.80
		}
	} else if directOpenFound {
		if score.ClosureRisk > 0.35 {
			score.ClosureRisk = 0.35
		}
	}

	score.Confidence = clamp(totalConfidence/float64(len(usable)), 0.1, 1.0)

	staleness := clamp(lastInspectionDays/730.0, 0, 1)

	priorityRaw := e.cfg.WClosureRisk*score.ClosureRisk +
		e.cfg.WQualityRisk*score.QualityRisk +
		e.cfg.WQualityDelta*(-score.QualityDelta/20.0) +
		e.cfg.WStaleness*staleness

	confFactor := (1.0 - e.cfg.ConfidenceFactor) + e.cfg.ConfidenceFactor*score.Confidence
	score.Priority = clamp(100.0*priorityRaw*confFactor, 0, 100)

	if prevScore != nil {
		delta := math.Abs(score.Priority - prevScore.Priority)
		if delta >= e.cfg.VolatileDelta {
			score.Volatile = true
		}
		if delta < 15.0 {
			score.Priority = (1.0-e.cfg.HysteresisWeight)*score.Priority + e.cfg.HysteresisWeight*prevScore.Priority
		}
	}

	if score.ClosureRisk >= 0.85 {
		score.Status = model.StatusClosureConfirmed
		score.Reason = "Fuertes evidencias de cierre permanente"
	} else if score.ClosureRisk >= 0.60 {
		score.Status = model.StatusClosureSuspected
		score.Reason = "Indicios de posible cierre"
	} else {
		score.Status = model.StatusActive
		score.Reason = "Sin indicios significativos de cierre"
	}

	if score.ConflictDetected {
		score.Reason = "⚠ Conflicto entre fuentes. " + score.Reason
	}

	score.AssignTier(e.cfg.TierConfig)
	return score
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
