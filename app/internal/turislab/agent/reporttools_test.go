package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/datadope/turislab-doctor/internal/turislab/model"
	adkmodel "google.golang.org/adk/v2/model"
)

var fixtureTime = time.Date(2026, 5, 2, 10, 0, 0, 0, time.UTC)

func fixtureEvidence(id string, weight float64) model.Evidence {
	return model.Evidence{
		SignalID:        "sig-" + id,
		EstablishmentID: "est-1",
		Family:          model.SignalFamily("direct_status"),
		Kind:            model.KindBusinessStatus,
		Source:          "google_maps",
		CollectedAt:     fixtureTime,
		Value:           1,
		Confidence:      0.9,
		Weight:          weight,
		Label:           "marked closed",
		Detail:          "CLOSED",
	}
}

func fixtureReport() *model.Report {
	est1 := &model.Establishment{
		ID: "est-1", Name: "Hotel Sol y Playa", Category: model.CategoryHotel,
		Municipality: "Fuengirola", Province: "Málaga",
		Website: "https://hotelsolyplaya.example", Phone: "+34 952 000 001",
	}
	est2 := &model.Establishment{
		ID: "est-2", Name: "La Mar de Tapas", Category: model.CategoryRestaurant,
		Municipality: "Málaga", Province: "Málaga",
	}
	est3 := &model.Establishment{
		ID: "est-3", Name: "Camping Costa del Sol", Category: "camping",
		Municipality: "Torremolinos", Province: "Málaga",
	}
	return &model.Report{
		GeneratedAt:        fixtureTime,
		EstablishmentCount: 3,
		Queue: []model.QueueItem{
			{
				Rank: 1, Establishment: est1,
				Score: &model.Score{
					EstablishmentID: "est-1", ComputedAt: fixtureTime,
					Tier: model.TierP0, Priority: 92, ClosureRisk: 0.85, QualityRisk: 0.3,
					Confidence: 0.9, EvidenceCount: 2,
					Status: model.StatusClosureConfirmed, Reason: "confirmed closed on maps",
				},
				PrimarySignals: []model.Evidence{fixtureEvidence("a", 0.4), fixtureEvidence("b", 0.9)},
			},
			{
				Rank: 2, Establishment: est2,
				Score: &model.Score{
					EstablishmentID: "est-2", ComputedAt: fixtureTime,
					Tier: model.TierP1, Priority: 55, ClosureRisk: 0.4, QualityRisk: 0.6,
					Confidence: 0.7, EvidenceCount: 1,
					Status: model.StatusClosureSuspected, Reason: "reviews mention it shut down",
				},
			},
			{
				Rank: 3, Establishment: est3,
				Score: &model.Score{
					EstablishmentID: "est-3", ComputedAt: fixtureTime,
					Tier: model.TierP2, Priority: 30, ClosureRisk: 0.1, QualityRisk: 0.2,
					Confidence: 0.6, EvidenceCount: 0,
					Status: model.StatusActive, Reason: "no closure evidence",
				},
			},
		},
		Summary: model.ReportSummary{
			Total: 3, P0: 1, P1: 1, P2: 1, ClosureConfirmed: 1, Coverage: 92.5,
		},
	}
}

// bigReport builds a report with n establishments whose names are long, so
// list pages reach the history budget of the tool quickly.
func bigReport(n int) *model.Report {
	name := strings.Repeat("E", 200)
	queue := make([]model.QueueItem, 0, n)
	for i := range n {
		queue = append(queue, model.QueueItem{
			Rank: i + 1,
			Establishment: &model.Establishment{
				ID:           fmt.Sprintf("est-%03d", i),
				Name:         name,
				Category:     model.CategoryHotel,
				Municipality: "Torremolinos",
			},
			Score: &model.Score{
				EstablishmentID: fmt.Sprintf("est-%03d", i),
				Tier:            model.TierP1,
				Priority:        float64(100 - i),
				Status:          model.StatusActive,
			},
		})
	}
	return &model.Report{Queue: queue, Summary: model.ReportSummary{Total: n}}
}

func TestReportSummaryShape(t *testing.T) {
	summary := reportSummary(fixtureReport())
	if summary["error"] != nil {
		t.Fatalf("summary reports an error: %v", summary["error"])
	}
	if summary["establishments"] != 3 || summary["total_scored"] != 3 {
		t.Errorf("establishments = %v, total_scored = %v, want 3 and 3", summary["establishments"], summary["total_scored"])
	}
	tiers, ok := summary["tiers"].(map[string]int)
	if !ok {
		t.Fatalf("tiers = %T, want map[string]int", summary["tiers"])
	}
	if tiers["P0"] != 1 || tiers["P1"] != 1 || tiers["P2"] != 1 || tiers["P3"] != 0 || tiers["collect_data"] != 0 {
		t.Errorf("tiers = %v, want P0/P1/P2 = 1 and the rest 0", tiers)
	}
	if summary["closure_confirmed"] != 1 || summary["coverage_pct"] != 92.5 {
		t.Errorf("closure_confirmed = %v, coverage_pct = %v, want 1 and 92.5", summary["closure_confirmed"], summary["coverage_pct"])
	}
	if summary["scan_running"] != false {
		t.Errorf("scan_running = %v, want false", summary["scan_running"])
	}

	if summary := reportSummary(nil); summary["error"] == nil {
		t.Errorf("summary of a nil report must report the error key")
	}
}

func TestListEstablishmentsOrderAndOutline(t *testing.T) {
	ests, total := listEstablishments(fixtureReport(), estQuery{}, 100_000)
	if total != 3 {
		t.Fatalf("total = %d, want 3", total)
	}
	if len(ests) != 3 {
		t.Fatalf("got %d establishments, want 3", len(ests))
	}
	for i, est := range ests {
		if est.Rank != i+1 {
			t.Errorf("rank = %d, want %d", est.Rank, i+1)
		}
		if est.ID == "" || est.Name == "" {
			t.Errorf("outline %d carries no id or name: %+v", i, est)
		}
	}
	first := ests[0]
	if first.Tier != "P0" || first.Status != "closure_confirmed" || first.Priority != 92 {
		t.Errorf("first outline = %+v, want tier P0, closure_confirmed, priority 92", first)
	}
	if first.Municipality != "Fuengirola" || first.Category != "hotel" {
		t.Errorf("first outline = %+v, want the registry fields", first)
	}
	if first.EvidenceCount != 2 {
		t.Errorf("evidence_count = %d, want 2", first.EvidenceCount)
	}
}

func TestListEstablishmentsFilters(t *testing.T) {
	tests := []struct {
		name  string
		query estQuery
		want  []string
	}{
		{"tier exact", estQuery{Tier: "P0"}, []string{"est-1"}},
		{"tier case-insensitive", estQuery{Tier: "p0"}, []string{"est-1"}},
		{"municipality substring", estQuery{Municipality: "fuengirola"}, []string{"est-1"}},
		{"name substring hits two", estQuery{Name: "sol"}, []string{"est-1", "est-3"}},
		{"status active", estQuery{Status: "active"}, []string{"est-3"}},
		{"status confirmed", estQuery{Status: "closure_confirmed"}, []string{"est-1"}},
		{"combined tier+municipality", estQuery{Tier: "P1", Municipality: "Málaga"}, []string{"est-2"}},
		{"nothing matches", estQuery{Tier: "collect_data"}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ests, total := listEstablishments(fixtureReport(), tc.query, 100_000)
			if total != len(tc.want) {
				t.Fatalf("total = %d, want %d", total, len(tc.want))
			}
			got := make([]string, 0, len(ests))
			for _, est := range ests {
				got = append(got, est.ID)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Errorf("got[%d] = %s, want %s", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestListEstablishmentsPagination(t *testing.T) {
	report := fixtureReport()

	ests, total := listEstablishments(report, estQuery{Limit: 2}, 100_000)
	if total != 3 || len(ests) != 2 || ests[0].ID != "est-1" || ests[1].ID != "est-2" {
		t.Errorf("first page = %v (total %d), want [est-1 est-2] of 3", ests, total)
	}

	ests, total = listEstablishments(report, estQuery{Offset: 2}, 100_000)
	if total != 3 || len(ests) != 1 || ests[0].ID != "est-3" {
		t.Errorf("second page = %v (total %d), want [est-3]", ests, total)
	}

	ests, total = listEstablishments(report, estQuery{Offset: 500}, 100_000)
	if total != 3 || len(ests) != 0 {
		t.Errorf("offset past the end = %v (total %d), want an empty page", ests, total)
	}

	ests, total = listEstablishments(report, estQuery{}, 100_000)
	if total != 3 || len(ests) != 3 {
		t.Errorf("default page = %v (total %d), want everything", ests, total)
	}

	ests, total = listEstablishments(report, estQuery{Offset: -5}, 100_000)
	if total != 3 || len(ests) != 3 || ests[0].ID != "est-1" {
		t.Errorf("negative offset = %v (total %d), want the first page", ests, total)
	}
}

func TestListEstablishmentsLimitIsCapped(t *testing.T) {
	report := bigReport(maxEstPage + 10)
	ests, total := listEstablishments(report, estQuery{Limit: maxEstPage + 40}, 1_000_000)
	if total != maxEstPage+10 {
		t.Errorf("total = %d, want %d", total, maxEstPage+10)
	}
	if len(ests) != maxEstPage {
		t.Errorf("returned = %d, want the cap %d", len(ests), maxEstPage)
	}
}

func TestListEstablishmentsFitsThePage(t *testing.T) {
	// Long names: each outline is several hundred bytes, so a small budget
	// fits a single entry.
	ests, total := listEstablishments(bigReport(100), estQuery{Limit: 50}, 600)
	if len(ests) != 1 {
		t.Errorf("got %d establishments within a 600B budget, want 1", len(ests))
	}
	if total != 100 {
		t.Errorf("total = %d, want 100 (the cut is in the page, not the count)", total)
	}
}

func TestEstablishmentDetailFound(t *testing.T) {
	detail, err := establishmentDetail(fixtureReport(), "est-1")
	if err != nil {
		t.Fatalf("establishmentDetail: %v", err)
	}
	if detail.Establishment["id"] != "est-1" || detail.Establishment["name"] != "Hotel Sol y Playa" {
		t.Errorf("establishment = %v", detail.Establishment)
	}
	if detail.Establishment["municipality"] != "Fuengirola" || detail.Establishment["category"] != "hotel" {
		t.Errorf("establishment registry fields = %v", detail.Establishment)
	}
	if detail.Score["tier"] != "P0" || detail.Score["status"] != "closure_confirmed" {
		t.Errorf("score = %v", detail.Score)
	}
	if detail.Score["priority"] != 92.0 || detail.Score["evidence_count"] != 2 {
		t.Errorf("score = %v, want priority 92 and evidence_count 2", detail.Score)
	}
	if detail.Score["reason"] != "confirmed closed on maps" {
		t.Errorf("score reason = %v", detail.Score["reason"])
	}
	if len(detail.Evidence) != 2 {
		t.Fatalf("evidence = %d entries, want 2", len(detail.Evidence))
	}
	// The strongest signal comes first.
	if detail.Evidence[0]["weight"] != 0.9 {
		t.Errorf("first evidence weight = %v, want 0.9", detail.Evidence[0]["weight"])
	}
	if detail.Evidence[1]["weight"] != 0.4 {
		t.Errorf("second evidence weight = %v, want 0.4", detail.Evidence[1]["weight"])
	}
}

func TestEstablishmentDetailMisses(t *testing.T) {
	if _, err := establishmentDetail(fixtureReport(), "nope"); err == nil {
		t.Errorf("an unknown id must fail, got no error")
	} else if !strings.Contains(err.Error(), "no establishment with id") {
		t.Errorf("error = %v, want it to name the id", err)
	}
	if _, err := establishmentDetail(nil, "est-1"); err == nil {
		t.Errorf("a nil report must fail, got no error")
	} else if !strings.Contains(err.Error(), "no scan report available") {
		t.Errorf("error = %v, want the no-report message", err)
	}
}

func TestBuildToolsRegistersTheFourHelpers(t *testing.T) {
	var source atomicReport
	source.set(fixtureReport())
	tools := buildTools(&source)
	if len(tools) != 4 {
		t.Fatalf("got %d tools, want 4", len(tools))
	}
	want := map[string]bool{"get_report_summary": false, "list_establishments": false, "get_establishment_detail": false, "explain_establishment": false}
	for _, tool := range tools {
		if _, ok := want[tool.Name()]; !ok {
			t.Errorf("unexpected tool %q", tool.Name())
			continue
		}
		want[tool.Name()] = true
		if tool.Description() == "" {
			t.Errorf("tool %q carries no description", tool.Name())
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("tool %q is not registered", name)
		}
	}
}

func TestSortEvidenceOrdersByWeight(t *testing.T) {
	evidence := []map[string]any{
		{"weight": 0.2},
		{"weight": 0.9},
		{"weight": 0.5},
	}
	sortEvidence(evidence)
	if evidence[0]["weight"] != 0.9 || evidence[1]["weight"] != 0.5 || evidence[2]["weight"] != 0.2 {
		t.Errorf("evidence = %v, want it ordered by descending weight", evidence)
	}
}

// toolResponse returns the result of the named tool as the model saw it in
// the second request of a two-round conversation.
func toolResponse(t *testing.T, m *scriptedModel, name string) map[string]any {
	t.Helper()
	if len(m.requests) < 2 {
		t.Fatalf("the model saw %d requests, want 2", len(m.requests))
	}
	raw, err := json.Marshal(m.requests[1].Contents)
	if err != nil {
		t.Fatalf("marshal history: %v", err)
	}
	// The tool result of the requested tool is somewhere in the history;
	// find its JSON shape by walking the contents the scripted way.
	for _, content := range m.requests[1].Contents {
		for _, part := range content.Parts {
			if part.FunctionResponse == nil || part.FunctionResponse.Name != name {
				continue
			}
			if part.FunctionResponse != nil && part.FunctionResponse.Name == name {
				return part.FunctionResponse.Response
			}
		}
	}
	_ = raw
	t.Fatalf("no function response of %s in the model history", name)
	return nil
}

// The list tool must hand the model a page that fits the history budget:
// a page cut by boundResult afterwards would skip establishments silently,
// and next_offset would not match what the model received.
func TestListEstablishmentsToolPageFitsAndNextOffsetIsExact(t *testing.T) {
	m := &scriptedModel{turns: [][]*adkmodel.LLMResponse{
		{toolCallResponse("list_establishments", map[string]any{"limit": 50})},
		{textResponse("listed", false)},
	}}
	svc := testService(t, m, bigReport(120))
	chat(t, svc, `{"message":"list"}`)

	result := toolResponse(t, m, "list_establishments")
	if result["truncated"] != nil {
		t.Fatalf("the page reached boundResult: the tool should have fit it: %v", result["truncation_note"])
	}
	raw, _ := json.Marshal(result)
	if len(raw) > maxToolHistoryChars {
		t.Errorf("page is %dB, want <= %d", len(raw), maxToolHistoryChars)
	}
	returned := int(result["returned"].(float64))
	if returned < 1 || returned >= 50 {
		t.Errorf("returned = %d, want a page cut below the 50 asked", returned)
	}
	if got := len(result["establishments"].([]any)); got != returned {
		t.Errorf("establishments has %d items, returned says %d", got, returned)
	}
	if next := int(result["next_offset"].(float64)); next != returned {
		t.Errorf("next_offset = %d, want %d (offset + returned)", next, returned)
	}
	if result["total"] != float64(120) {
		t.Errorf("total = %v, want 120", result["total"])
	}
}

func TestListEstablishmentsToolCapsLimitAndClampsOffset(t *testing.T) {
	m := &scriptedModel{turns: [][]*adkmodel.LLMResponse{
		{toolCallResponse("list_establishments", map[string]any{"limit": 9999, "offset": -5})},
		{textResponse("listed", false)},
	}}
	svc := testService(t, m, bigReport(10))
	chat(t, svc, `{"message":"list"}`)

	result := toolResponse(t, m, "list_establishments")
	if result["offset"] != float64(0) {
		t.Errorf("offset = %v, want 0 (negative offsets are clamped)", result["offset"])
	}
	if result["returned"] != float64(10) {
		t.Errorf("returned = %v, want the whole queue (10)", result["returned"])
	}
	if result["next_offset"] != nil {
		t.Errorf("next_offset = %v, want none on the last page", result["next_offset"])
	}
}
