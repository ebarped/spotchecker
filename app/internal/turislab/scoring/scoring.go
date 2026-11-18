// Package scoring computes closure_risk, quality_risk, quality_delta and priority.
//
// TBD (LLM as a judge): today the score is fully deterministic — signals,
// weights and thresholds. A future signal may hand the whole evidence set to
// the agent so the LLM itself issues the verdict (LLM-as-a-judge); that
// signal would carry the model's confidence instead of a rule-derived value.
package scoring

import (
	"math"
	"time"

	"github.com/datadope/turislab-doctor/internal/turislab/model"
)

type ScoringConfig struct {
	WClosureRisk  float64
	WQualityRisk  float64
	WQualityDelta float64
	WStaleness    float64
	WImpact       float64
	WComplaints   float64
	WDirectStatus float64
	WActivity     float64
	WContext      float64
	WOfficial     float64
	WReviews      float64
	WOfficialQ    float64
	WProduct      float64
	WNews         float64
	WFreshness    float64

	// RiskPrior is the closure risk assumed for an establishment whose only
	// evidence is indirect (a silent listing, a dead website). Blending it in
	// keeps one weak signal from claiming a closure, and it is what makes
	// "no direct evidence" render as a low-but-nonzero risk instead of 0%.
	RiskPrior float64
	// RiskPriorWeight is how many "units" of signal the prior is worth. It
	// is compared against sum(familyWeight·weight·confidence·decay): a web
	// status is ~0.03, a review-recency signal ~0.075, a direct Google
	// status ~0.30. It must stay well below one activity signal, or the
	// prior drowns the evidence (two real signals used to read as 20%).
	RiskPriorWeight float64
	// AliveBelow is the value under which a risk-bearing signal counts as
	// evidence of activity (0 = clearly alive, 1 = clearly closed).
	AliveBelow float64

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
		RiskPrior:         0.10,
		RiskPriorWeight:   0.05,
		AliveBelow:        0.35,
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
	// confidence nor flip the status toward active. Facts (a URL to check)
	// are carried for other consumers and say nothing about the business.
	var usable []*model.Signal
	for _, sig := range signals {
		if sig.Error == "" && !isFact(sig) {
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
	var aliveFound bool

	// Closure risk is the weighted average of every signal that says
	// something about the business being open — direct status AND activity
	// (website, review recency, OSM) — on the one scale Signal.Value
	// documents. Families that say nothing about it (reviews, product) are
	// left out: a 2.7 rating is not evidence of a closure.
	riskNum := 0.0
	riskDen := 0.0

	for _, sig := range usable {
		totalConfidence += sig.Confidence

		ageDays := now.Sub(sig.CollectedAt).Hours() / 24.0
		if ageDays < 0 {
			ageDays = 0
		}
		decay := math.Pow(0.5, ageDays/e.cfg.DecayHalfLifeDays)

		isDirect := sig.Family == model.FamilyDirectStatus || sig.Kind == model.KindBusinessStatus
		if isDirect {
			if sig.RawValue == "permanently_closed" || sig.RawValue == "closed" {
				directClosureFound = true
			} else if sig.RawValue == "open" {
				directOpenFound = true
			}
		}

		familyWeight, ok := e.riskWeight(sig)
		if !ok {
			continue
		}
		w := familyWeight * sig.Weight * sig.Confidence * decay
		if w <= 0 {
			continue
		}
		riskNum += sig.Value * w
		riskDen += w
		if sig.Value <= e.cfg.AliveBelow {
			aliveFound = true
		}
	}

	score.RiskKnown = riskDen > 0
	if score.RiskKnown {
		if directClosureFound || directOpenFound {
			// A direct status is evidence in its own right: average it with
			// the activity signals instead of diluting it with a prior.
			score.ClosureRisk = clamp(riskNum/riskDen, 0, 1)
		} else {
			// Activity alone (silent listing, dead website): blend a prior
			// so a single weak signal cannot claim a closure by itself.
			score.ClosureRisk = clamp(
				(riskNum+e.cfg.RiskPrior*e.cfg.RiskPriorWeight)/(riskDen+e.cfg.RiskPriorWeight),
				0, 1)
		}
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

	switch {
	case !score.RiskKnown:
		// Nothing measured whether the business is open. Saying "active"
		// here would turn "we did not look" into "we checked and it is fine".
		score.Status = model.StatusUnknown
		score.Reason = "Sin evidencia sobre el estado operativo"
	case score.ClosureRisk >= 0.85:
		score.Status = model.StatusClosureConfirmed
		score.Reason = "Fuertes evidencias de cierre permanente"
	case score.ClosureRisk >= 0.60:
		score.Status = model.StatusClosureSuspected
		score.Reason = "Indicios de posible cierre"
	case aliveFound:
		score.Status = model.StatusActive
		score.Reason = "Las señales de actividad apuntan a que sigue operativo"
	default:
		// Some indirect signal exists but none says "alive", and it is not
		// strong enough to call a closure. The honest answer is unknown.
		score.Status = model.StatusUnknown
		score.Reason = "Solo hay indicios débiles de inactividad"
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

// isFact reports whether a signal is a fact carried for other consumers
// rather than evidence about the business state. Facts never enter the
// score: KindWebsiteFound only tells the website collector which URL to
// check, and counting it would let a discovery inflate confidence.
func isFact(sig *model.Signal) bool {
	return sig.Kind == model.KindWebsiteFound
}

// riskWeight returns the weight of the family a signal belongs to, and
// whether that family bears on whether the business is still open. Reviews
// and product signals are deliberately left out: a 2.7 rating says how the
// place was, not whether it exists today.
func (e *Engine) riskWeight(sig *model.Signal) (float64, bool) {
	switch sig.Family {
	case model.FamilyDirectStatus:
		return e.cfg.WDirectStatus, true
	case model.FamilyActivity:
		return e.cfg.WActivity, true
	default:
		return 0, false
	}
}
