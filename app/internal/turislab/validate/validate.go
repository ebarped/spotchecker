// Package validate compares the scores produced by the scanner against an
// independently curated ground-truth file.
//
// Ground truth is NEVER used for scoring: it is read only here, after the scan
// has finished, to measure whether the priority queue puts the establishments
// an inspector should visit first at the top.
package validate

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/datadope/turislab-doctor/internal/turislab/model"
)

// Ground-truth status values accepted in ground-truth.json.
const (
	StatusActive            = "active"
	StatusClosedPermanently = "closed_permanently"
	StatusClosedTemporarily = "closed_temporarily"
	StatusSeasonal          = "seasonal"
	StatusClosureSuspected  = "closure_suspected"
	StatusUnknown           = "unknown"
)

// GroundTruth is the on-disk document.
type GroundTruth struct {
	Comment         string  `json:"_comment"`
	Updated         string  `json:"_updated"`
	SuccessCriteria string  `json:"poc_success_criterion"`
	Establishments  []Entry `json:"establishments"`
}

// Entry is one establishment's independently verified state.
type Entry struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	GroundTruthStatus string `json:"ground_truth_status"`
	Notes             string `json:"notes"`
}

// Load reads and validates a ground-truth file.
func Load(path string) (*GroundTruth, error) {
	if path == "" {
		return nil, fmt.Errorf("validate: no ground_truth_path configured")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("validate: read %q: %w", path, err)
	}
	var gt GroundTruth
	if err := json.Unmarshal(data, &gt); err != nil {
		return nil, fmt.Errorf("validate: parse %q: %w", path, err)
	}
	seen := make(map[string]bool, len(gt.Establishments))
	for i, e := range gt.Establishments {
		if e.ID == "" {
			return nil, fmt.Errorf("validate: entry %d has no id", i)
		}
		if seen[e.ID] {
			return nil, fmt.Errorf("validate: duplicate id %q", e.ID)
		}
		seen[e.ID] = true
		if !knownStatus(e.GroundTruthStatus) {
			return nil, fmt.Errorf("validate: %s has unknown ground_truth_status %q", e.ID, e.GroundTruthStatus)
		}
	}
	return &gt, nil
}

func knownStatus(s string) bool {
	switch s {
	case StatusActive, StatusClosedPermanently, StatusClosedTemporarily,
		StatusSeasonal, StatusClosureSuspected, StatusUnknown:
		return true
	}
	return false
}

// Options tunes the success criteria.
type Options struct {
	// TopN is the cut-off used for the PoC criterion (default 3).
	TopN int

	// MinHitRate is the minimum fraction of known-closed establishments that
	// must land in the top N (default 1.0).
	MinHitRate float64

	// FailOnFalsePositive fails the run when an establishment verified as
	// active is predicted as closure_confirmed.
	FailOnFalsePositive bool
}

func (o *Options) withDefaults() Options {
	out := *o
	if out.TopN <= 0 {
		out.TopN = 3
	}
	if out.MinHitRate <= 0 {
		out.MinHitRate = 1.0
	}
	return out
}

// Row is the per-establishment comparison.
type Row struct {
	ID            string
	Name          string
	Truth         string
	Rank          int // 1-based; 0 means not scored yet
	Tier          model.TierLabel
	Status        model.StatusSuggested
	ClosureRisk   float64
	Priority      float64
	Reason        string
	ExpectedVisit bool // ground truth says the inspector should check it
	InTopN        bool
	FalsePositive bool
	Missing       bool
}

// Result is the outcome of a validation run.
type Result struct {
	TopN             int
	Total            int
	Scored           int
	Missing          []string
	ClosedKnown      int
	ClosedInTopN     int
	HitRate          float64
	FalsePositives   []string
	Suspected        int
	SuspectedVisited int
	Rows             []Row
	Passed           bool
}

// Run compares the latest scores with the ground truth. Scores are ranked by
// priority, descending, exactly like the queue.
func Run(gt *GroundTruth, scores []*model.Score, opts Options) Result {
	opts = opts.withDefaults()

	byID := make(map[string]*model.Score, len(scores))
	for _, sc := range scores {
		byID[sc.EstablishmentID] = sc
	}

	// Rank only scored establishments, highest priority first.
	ranked := make([]*model.Score, 0, len(scores))
	for _, sc := range scores {
		ranked = append(ranked, sc)
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].Priority != ranked[j].Priority {
			return ranked[i].Priority > ranked[j].Priority
		}
		return ranked[i].EstablishmentID < ranked[j].EstablishmentID
	})
	rank := make(map[string]int, len(ranked))
	for i, sc := range ranked {
		rank[sc.EstablishmentID] = i + 1
	}

	res := Result{TopN: opts.TopN, Total: len(gt.Establishments)}

	for _, e := range gt.Establishments {
		row := Row{
			ID:            e.ID,
			Name:          e.Name,
			Truth:         e.GroundTruthStatus,
			ExpectedVisit: expectsVisit(e.GroundTruthStatus),
		}

		sc, ok := byID[e.ID]
		if !ok {
			row.Missing = true
			res.Missing = append(res.Missing, e.ID)
			res.Rows = append(res.Rows, row)
			continue
		}

		row.Rank = rank[e.ID]
		row.Tier = sc.Tier
		row.Status = sc.Status
		row.ClosureRisk = sc.ClosureRisk
		row.Priority = sc.Priority
		row.Reason = sc.Reason
		row.InTopN = row.Rank > 0 && row.Rank <= opts.TopN
		res.Scored++

		switch e.GroundTruthStatus {
		case StatusClosedPermanently:
			res.ClosedKnown++
			if row.InTopN {
				res.ClosedInTopN++
			}
		case StatusClosureSuspected:
			res.Suspected++
			if row.InTopN {
				res.SuspectedVisited++
			}
		case StatusActive:
			if sc.Status == model.StatusClosureConfirmed {
				row.FalsePositive = true
				res.FalsePositives = append(res.FalsePositives, e.ID)
			}
		}

		res.Rows = append(res.Rows, row)
	}

	if res.ClosedKnown > 0 {
		res.HitRate = float64(res.ClosedInTopN) / float64(res.ClosedKnown)
	} else {
		// No verified closure in the sample: the criterion cannot be measured.
		res.HitRate = 0
	}

	res.Passed = res.ClosedKnown > 0 &&
		res.Scored == res.Total &&
		res.HitRate >= opts.MinHitRate &&
		(!opts.FailOnFalsePositive || len(res.FalsePositives) == 0)

	return res
}

func expectsVisit(truth string) bool {
	switch truth {
	case StatusClosedPermanently, StatusClosedTemporarily, StatusClosureSuspected:
		return true
	}
	return false
}

// String renders a compact human-readable report.
func (r Result) String() string {
	out := "Validación contra ground truth\n"
	out += "─────────────────────────────────────────────────────────────────────\n"
	out += fmt.Sprintf("%-4s %-10s %-22s %-18s %-7s %s\n", "Rank", "ID", "Estado predicho", "Ground truth", "Prior.", "Resultado")

	for _, row := range r.Rows {
		rank := "-"
		if row.Rank > 0 {
			rank = fmt.Sprintf("%d", row.Rank)
		}
		status := string(row.Status)
		if row.Missing {
			status = "(sin score)"
		}
		outcome := "ok"
		switch {
		case row.Missing:
			outcome = "PENDIENTE de scan"
		case row.FalsePositive:
			outcome = "FALSO POSITIVO"
		case row.Truth == StatusClosedPermanently && row.InTopN:
			outcome = fmt.Sprintf("acierto (top %d)", r.TopN)
		case row.Truth == StatusClosedPermanently:
			outcome = fmt.Sprintf("FALLO: fuera del top %d", r.TopN)
		case row.Truth == StatusClosureSuspected && row.InTopN:
			outcome = fmt.Sprintf("acierto (top %d)", r.TopN)
		}
		out += fmt.Sprintf("%-4s %-10s %-22s %-18s %-7.2f %s\n", rank, row.ID, status, row.Truth, row.Priority, outcome)
	}

	out += "─────────────────────────────────────────────────────────────────────\n"
	out += fmt.Sprintf("Evaluados: %d/%d", r.Scored, r.Total)
	if len(r.Missing) > 0 {
		out += fmt.Sprintf(" · pendientes: %v", r.Missing)
	}
	out += "\n"
	out += fmt.Sprintf("Cerrados conocidos en el top %d: %d/%d (%.0f%%)", r.TopN, r.ClosedInTopN, r.ClosedKnown, r.HitRate*100)
	if r.Suspected > 0 {
		out += fmt.Sprintf(" · sospechas en el top %d: %d/%d", r.TopN, r.SuspectedVisited, r.Suspected)
	}
	out += "\n"
	if len(r.FalsePositives) > 0 {
		out += fmt.Sprintf("Falsos positivos (activos marcados como cierre confirmado): %v\n", r.FalsePositives)
	}
	if r.Passed {
		out += "Resultado: CRITERIO PoC CUMPLIDO\n"
	} else {
		out += "Resultado: CRITERIO PoC NO CUMPLIDO\n"
	}
	return out
}
