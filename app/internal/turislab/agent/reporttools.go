package agent

// Local function tools over the current scan report. The report never goes
// whole into the prompt, so the agent pulls what it needs through these
// tools: the priority queue, the score breakdown of one establishment and
// the summary of the whole scan.

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/datadope/turislab-doctor/internal/turislab/model"
	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"
)

// Page sizes of list_establishments. Results go whole into the model
// history, so a page is kept well under maxToolHistoryChars: the model
// narrows with filters and offset instead of asking for everything again.
const (
	defaultEstPage = 20
	maxEstPage     = 50
)

// reportSource hands the tools the latest scan report.
type reportSource interface {
	report() *model.Report
}

// atomicReport is the production reportSource: a pointer swapped in when a
// fresh report is built.
type atomicReport struct {
	value atomic.Pointer[model.Report]
}

func (a *atomicReport) set(report *model.Report) { a.value.Store(report) }

func (a *atomicReport) report() *model.Report { return a.value.Load() }

// establishmentOutline is one queue entry as the agent sees it: enough to
// rank and pick, without the full score breakdown.
type establishmentOutline struct {
	Rank          int     `json:"rank"`
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	Category      string  `json:"category,omitempty"`
	Municipality  string  `json:"municipality,omitempty"`
	Tier          string  `json:"tier"`
	Priority      float64 `json:"priority"`
	ClosureRisk   float64 `json:"closure_risk"`
	QualityRisk   float64 `json:"quality_risk"`
	Confidence    float64 `json:"confidence"`
	Status        string  `json:"status,omitempty"`
	EvidenceCount int     `json:"evidence_count"`
}

// estQuery selects a page of the queue. Empty filters match everything;
// Limit <= 0 means the default page size.
type estQuery struct {
	Tier         string
	Municipality string
	Name         string
	Status       string
	Offset       int
	Limit        int
}

// estMatches reports whether a queue entry passes the filters of q.
func estMatches(item model.QueueItem, q estQuery) bool {
	tier, status := "", ""
	if item.Score != nil {
		tier, status = string(item.Score.Tier), string(item.Score.Status)
	}
	if q.Tier != "" && !strings.EqualFold(tier, q.Tier) {
		return false
	}
	if q.Status != "" && !strings.EqualFold(status, q.Status) {
		return false
	}
	if q.Municipality != "" && !strings.Contains(strings.ToLower(item.Establishment.Municipality), strings.ToLower(q.Municipality)) {
		return false
	}
	if q.Name != "" && !strings.Contains(strings.ToLower(item.Establishment.Name), strings.ToLower(q.Name)) {
		return false
	}
	return true
}

// estOutline flattens one queue entry for the agent.
func estOutline(item model.QueueItem) establishmentOutline {
	out := establishmentOutline{
		Rank:         item.Rank,
		ID:           item.Establishment.ID,
		Name:         item.Establishment.Name,
		Category:     string(item.Establishment.Category),
		Municipality: item.Establishment.Municipality,
	}
	if item.Score != nil {
		out.Tier = string(item.Score.Tier)
		out.Priority = item.Score.Priority
		out.ClosureRisk = item.Score.ClosureRisk
		out.QualityRisk = item.Score.QualityRisk
		out.Confidence = item.Score.Confidence
		out.Status = string(item.Score.Status)
		out.EvidenceCount = item.Score.EvidenceCount
	}
	return out
}

// listEstablishments returns the filtered page of the queue plus the total
// number of entries matching the filters. The page is cut to fit maxBytes:
// a page that boundResult would trim afterwards would skip entries silently,
// so the tool must return one that fits whole.
func listEstablishments(report *model.Report, q estQuery, maxBytes int) ([]establishmentOutline, int) {
	if report == nil {
		return nil, 0
	}
	if q.Offset < 0 {
		q.Offset = 0
	}
	var matching []model.QueueItem
	for _, item := range report.Queue {
		if estMatches(item, q) {
			matching = append(matching, item)
		}
	}
	total := len(matching)
	if q.Offset > 0 {
		if q.Offset >= total {
			return []establishmentOutline{}, total
		}
		matching = matching[q.Offset:]
	}
	limit := q.Limit
	if limit <= 0 {
		limit = defaultEstPage
	}
	limit = min(limit, maxEstPage)
	if limit < len(matching) {
		matching = matching[:limit]
	}
	out := make([]establishmentOutline, 0, len(matching))
	used := 0
	for _, item := range matching {
		o := estOutline(item)
		size := len(compactJSON(o)) + 1 // +1 for the JSON separator
		if maxBytes > 0 && used+size > maxBytes && len(out) > 0 {
			break
		}
		used += size
		out = append(out, o)
	}
	return out, total
}

// reportSummary describes the whole scan: tier counts, coverage and scan
// state. Mirrors model.ReportSummary in tool-friendly JSON.
func reportSummary(report *model.Report) map[string]any {
	if report == nil {
		return map[string]any{"error": "no scan report available yet"}
	}
	return map[string]any{
		"generated_at":     report.GeneratedAt,
		"establishments":   report.EstablishmentCount,
		"total_scored":     report.Summary.Total,
		"tiers": map[string]int{
			"P0":           report.Summary.P0,
			"P1":           report.Summary.P1,
			"P2":           report.Summary.P2,
			"P3":           report.Summary.P3,
			"collect_data": report.Summary.CollectData,
		},
		"closure_confirmed": report.Summary.ClosureConfirmed,
		"low_confidence":    report.Summary.LowConfidence,
		"coverage_pct":      report.Summary.Coverage,
		"scan_running":      report.ScanRunning,
		"scan_error":        report.ScanError,
	}
}

// estDetail is one establishment as the agent sees it: the registry fields,
// the full score breakdown and the primary evidence signals.
type estDetail struct {
	Establishment map[string]any `json:"establishment"`
	Score         map[string]any `json:"score"`
	Evidence      []map[string]any `json:"evidence,omitempty"`
}

// establishmentDetail looks one queue entry up by id.
func establishmentDetail(report *model.Report, id string) (*estDetail, error) {
	if report == nil {
		return nil, fmt.Errorf("no scan report available yet")
	}
	for _, item := range report.Queue {
		if item.Establishment.ID != id {
			continue
		}
		e := item.Establishment
		est := map[string]any{
			"id":           e.ID,
			"name":         e.Name,
			"category":     string(e.Category),
			"address":      e.Address,
			"municipality": e.Municipality,
			"province":     e.Province,
			"website":      e.Website,
			"phone":        e.Phone,
		}
		if e.Stars != nil {
			est["stars"] = *e.Stars
		}
		if e.LastInspectionAt != nil {
			est["last_inspection_at"] = e.LastInspectionAt
		}
		if e.LastInspectionResult != "" {
			est["last_inspection_result"] = e.LastInspectionResult
		}
		if e.GooglePlaceID != "" {
			est["google_place_id"] = e.GooglePlaceID
		}

		score := map[string]any{}
		if s := item.Score; s != nil {
			score = map[string]any{
				"tier":            string(s.Tier),
				"priority":        s.Priority,
				"closure_risk":    s.ClosureRisk,
				"quality_risk":    s.QualityRisk,
				"quality_delta":   s.QualityDelta,
				"confidence":      s.Confidence,
				"evidence_count":  s.EvidenceCount,
				"status":          string(s.Status),
				"reason":          s.Reason,
				"volatile":        s.Volatile,
				"conflict":        s.ConflictDetected,
				"computed_at":     s.ComputedAt,
			}
		}

		var evidence []map[string]any
		for _, ev := range item.PrimarySignals {
			evidence = append(evidence, map[string]any{
				"source":     ev.Source,
				"kind":       string(ev.Kind),
				"family":     string(ev.Family),
				"value":      ev.Value,
				"label":      ev.Label,
				"weight":     ev.Weight,
				"confidence": ev.Confidence,
				"collected":  ev.CollectedAt,
			})
		}
		sortEvidence(evidence)
		return &estDetail{Establishment: est, Score: score, Evidence: evidence}, nil
	}
	return nil, fmt.Errorf("no establishment with id %q in the current queue", id)
}

// buildTools turns the pure helpers into ADK function tools over source.
func buildTools(source reportSource) []tool.Tool {
	newTool := func(t tool.Tool, err error) tool.Tool {
		if err != nil {
			panic("agent: broken tool definition: " + err.Error())
		}
		return t
	}

	summaryTool := newTool(functiontool.New(functiontool.Config{
		Name: "get_report_summary",
		Description: "Summary of the current scan: how many establishments are scored per tier (P0 highest priority), how many are confirmed closed, overall coverage and whether a scan is running. " +
			"Call this first for any question about the state of the whole scan.",
	}, func(_ agent.Context, _ struct{}) (map[string]any, error) {
		return reportSummary(source.report()), nil
	}))

	type listEstsArgs struct {
		Tier         string `json:"tier,omitempty"`
		Municipality string `json:"municipality,omitempty"`
		Name         string `json:"name,omitempty"`
		Status       string `json:"status,omitempty"`
		Offset       int    `json:"offset,omitempty"`
		Limit        int    `json:"limit,omitempty"`
	}
	type listEstsResult struct {
		Total         int                    `json:"total"`
		Offset        int                    `json:"offset"`
		Returned      int                    `json:"returned"`
		NextOffset    int                    `json:"next_offset,omitempty"`
		Establishments []establishmentOutline `json:"establishments"`
	}
	listEsts := newTool(functiontool.New(functiontool.Config{
		Name: "list_establishments",
		Description: fmt.Sprintf("Page through the priority queue of the scan, worst (highest priority) first. "+
			"Filter by tier (P0/P1/P2/P3/collect_data), municipality, name substring or suggested status (e.g. closure_confirmed); "+
			"page with offset and limit (default %d, max %d).", defaultEstPage, maxEstPage),
	}, func(_ agent.Context, args listEstsArgs) (listEstsResult, error) {
		if args.Offset < 0 {
			args.Offset = 0
		}
		// The page itself must fit the history budget: boundResult trimming
		// it afterwards would silently skip entries. The margin covers the
		// wrapper fields the tool adds around the page.
		ests, total := listEstablishments(source.report(), estQuery{
			Tier:         args.Tier,
			Municipality: args.Municipality,
			Name:         args.Name,
			Status:       args.Status,
			Offset:       args.Offset,
			Limit:        args.Limit,
		}, maxToolHistoryChars-1024)
		res := listEstsResult{
			Total:         total,
			Offset:        args.Offset,
			Returned:      len(ests),
			Establishments: ests,
		}
		if args.Offset+len(ests) < total {
			res.NextOffset = args.Offset + len(ests)
		}
		return res, nil
	}))

	type detailArgs struct {
		ID string `json:"id"`
	}
	detailTool := newTool(functiontool.New(functiontool.Config{
		Name:        "get_establishment_detail",
		Description: "Full detail of one establishment by id: registry fields, the score breakdown (closure risk, quality risk, confidence, suggested status and reason) and the primary evidence signals behind the score. Get ids from list_establishments.",
	}, func(_ agent.Context, args detailArgs) (*estDetail, error) {
		return establishmentDetail(source.report(), strings.TrimSpace(args.ID))
	}))

	return []tool.Tool{summaryTool, listEsts, detailTool}
}

// sortEvidence orders the primary signals of a queue entry by weight, used
// when a tool result must present the strongest evidence first.
func sortEvidence(evs []map[string]any) {
	sort.SliceStable(evs, func(i, j int) bool {
		a, _ := evs[i]["weight"].(float64)
		b, _ := evs[j]["weight"].(float64)
		return a > b
	})
}

// compactJSON returns a compact JSON string, used in tests of the tools.
func compactJSON(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(raw)
}
