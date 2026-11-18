package main

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/datadope/turislab-doctor/internal/turislab/model"
)

// maxReportSignals caps the evidence rows one establishment carries in the
// report. The detail screen shows a handful; the rest would only bloat the
// payload the UI polls every few seconds.
const maxReportSignals = 8

// reportEvidence groups the signals collected since the cutoff by
// establishment and turns the strongest ones into the rows the detail screen
// shows. decayHalfLifeDays must match the scoring engine's so "peso" means
// the same in the table as in the risk.
func reportEvidence(signals []*model.Signal, cutoff time.Time, decayHalfLifeDays float64) map[string][]model.Evidence {
	now := time.Now()
	grouped := make(map[string][]model.Evidence)
	for _, sig := range signals {
		if sig == nil || sig.Error != "" || sig.CollectedAt.Before(cutoff) {
			continue
		}
		grouped[sig.EstablishmentID] = append(grouped[sig.EstablishmentID], toEvidence(sig, now, decayHalfLifeDays))
	}
	for id, rows := range grouped {
		sort.SliceStable(rows, func(i, j int) bool {
			return rows[i].EffectiveWeight > rows[j].EffectiveWeight
		})
		if len(rows) > maxReportSignals {
			rows = rows[:maxReportSignals]
		}
		grouped[id] = rows
	}
	return grouped
}

// toEvidence converts one raw signal into its UI row: a Spanish label, a
// human detail, and the effective weight the score gave it.
func toEvidence(sig *model.Signal, now time.Time, decayHalfLifeDays float64) model.Evidence {
	ageDays := now.Sub(sig.CollectedAt).Hours() / 24.0
	if ageDays < 0 {
		ageDays = 0
	}
	decay := 1.0
	if decayHalfLifeDays > 0 {
		decay = math.Pow(0.5, ageDays/decayHalfLifeDays)
	}

	label, detail := describeSignal(sig)
	return model.Evidence{
		SignalID:        sig.ID,
		EstablishmentID: sig.EstablishmentID,
		Family:          sig.Family,
		Kind:            sig.Kind,
		Source:          sig.SourceName,
		CollectedAt:     sig.CollectedAt,
		Value:           sig.Value,
		Confidence:      sig.Confidence,
		Weight:          sig.Weight,
		DecayFactor:     decay,
		EffectiveWeight: sig.Weight * sig.Confidence * decay,
		Label:           label,
		Detail:          detail,
	}
}

// describeSignal turns a kind and its raw value into the label and detail the
// detail screen shows. Unknown kinds fall back to the raw value so a new
// source still renders.
func describeSignal(sig *model.Signal) (label, detail string) {
	switch sig.Kind {
	case model.KindBusinessStatus:
		switch sig.RawValue {
		case "permanently_closed":
			return "Estado en Google Maps", "Cerrado permanentemente"
		case "temporarily_closed":
			return "Estado en Google Maps", "Cerrado temporalmente"
		case "open":
			return "Estado en Google Maps", "Abierto"
		}
		return "Estado en Google Maps", sig.RawValue
	case model.KindRating:
		return "Nota media en Google", fmt.Sprintf("%s/5", sig.RawValue)
	case model.KindReviewCount:
		return "Número de reseñas", fmt.Sprintf("%s reseñas", sig.RawValue)
	case model.KindReviewRecency:
		return "Última reseña de Google", sig.RawValue
	case model.KindWebsiteFound:
		return "Web oficial (Google Maps)", sig.RawValue
	case model.KindWebStatus:
		switch sig.RawValue {
		case "ok":
			return "Estado de la web oficial", "Responde correctamente"
		case "404":
			return "Estado de la web oficial", "No encontrada (404)"
		case "down":
			return "Estado de la web oficial", "No responde"
		}
		return "Estado de la web oficial", "Error al comprobar"
	case model.KindWebKeyword:
		return "Palabra clave en la web", fmt.Sprintf("«%s»", sig.RawValue)
	case model.KindOSMTag:
		return "Etiqueta OpenStreetMap", sig.RawValue
	default:
		return string(sig.Kind), sig.RawValue
	}
}
