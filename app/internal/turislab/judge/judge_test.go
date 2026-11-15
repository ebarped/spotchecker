package judge_test

import (
	"context"
	"errors"
	"io"
	"iter"
	"strings"
	"testing"
	"time"

	adkmodel "google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"github.com/datadope/turislab-doctor/internal/logx"
	"github.com/datadope/turislab-doctor/internal/turislab/judge"
	"github.com/datadope/turislab-doctor/internal/turislab/model"
)

// fakeLLM answers one canned text (or error) for every GenerateContent.
type fakeLLM struct {
	name    string
	text    string
	err     error
	request *adkmodel.LLMRequest
}

func (f *fakeLLM) Name() string { return f.name }

func (f *fakeLLM) GenerateContent(_ context.Context, req *adkmodel.LLMRequest, _ bool) iter.Seq2[*adkmodel.LLMResponse, error] {
	f.request = req
	return func(yield func(*adkmodel.LLMResponse, error) bool) {
		if f.err != nil {
			yield(nil, f.err)
			return
		}
		yield(&adkmodel.LLMResponse{
			Content: &genai.Content{Role: "model", Parts: []*genai.Part{{Text: f.text}}},
		}, nil)
	}
}

func fakeFactory(llm *fakeLLM) func(context.Context) (adkmodel.LLM, error) {
	return func(context.Context) (adkmodel.LLM, error) { return llm, nil }
}

func newJudge(llm *fakeLLM) *judge.Judge {
	return judge.New(judge.Config{ModelName: "test-model"}, fakeFactory(llm), logx.New(io.Discard))
}

func testEstablishment() *model.Establishment {
	return &model.Establishment{ID: "rest-001", Name: "Restaurante El Ancla", Category: model.CategoryRestaurant, Municipality: "Lugo", Website: "https://elancla.example"}
}

func testSignals() []*model.Signal {
	return []*model.Signal{
		{
			ID: "s1", EstablishmentID: "rest-001", SourceName: "google_maps_scraper",
			Kind: model.KindBusinessStatus, Family: model.FamilyDirectStatus,
			RawValue: "permanently_closed", Value: 1, Confidence: 0.75, Weight: 0.9,
			CollectedAt: time.Now().Add(-24 * time.Hour), DedupeKey: "s1",
		},
		{
			ID: "s2", EstablishmentID: "rest-001", SourceName: "website",
			Kind: model.KindWebStatus, Family: model.FamilyActivity,
			RawValue: "down", Value: 0, Confidence: 0.75, Weight: 0.3,
			CollectedAt: time.Now().Add(-2 * time.Hour), DedupeKey: "s2",
		},
		{
			// Errored signals must not reach the dossier.
			ID: "s3", EstablishmentID: "rest-001", SourceName: "osm",
			Kind: model.KindOSMTag, Family: model.FamilyActivity,
			Error: "overpass timeout", CollectedAt: time.Now(), DedupeKey: "s3",
		},
	}
}

func TestReviewParsesVerdict(t *testing.T) {
	llm := &fakeLLM{name: "test-model", text: `{"verdict":"closure_confirmed","confidence":0.9,"rationale":"Google Maps lo marca  permanentemente cerrado."}`}
	j := newJudge(llm)

	verdict, err := j.Review(context.Background(), testEstablishment(), testSignals(), nil)
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if verdict.Status != model.StatusClosureConfirmed {
		t.Errorf("status = %q, want closure_confirmed", verdict.Status)
	}
	if verdict.Confidence != 0.9 {
		t.Errorf("confidence = %v, want 0.9", verdict.Confidence)
	}
	if verdict.Model != "test-model" {
		t.Errorf("model = %q, want test-model", verdict.Model)
	}
	// Whitespace must collapse: two spaces between words above.
	if strings.Contains(verdict.Rationale, "  ") {
		t.Errorf("rationale not normalized: %q", verdict.Rationale)
	}
}

func TestReviewStripsFencesAndProse(t *testing.T) {
	llm := &fakeLLM{name: "m", text: "Here is my decision:\n```json\n{\"verdict\":\"active\",\"confidence\":1.4,\"rationale\":\"todo apunta a que está abierto\"}\n```\nGracias."}
	j := newJudge(llm)

	verdict, err := j.Review(context.Background(), testEstablishment(), testSignals(), nil)
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if verdict.Status != model.StatusActive {
		t.Errorf("status = %q, want active", verdict.Status)
	}
	if verdict.Confidence != 1 {
		t.Errorf("confidence = %v, want clamped to 1", verdict.Confidence)
	}
}

func TestReviewDossier(t *testing.T) {
	llm := &fakeLLM{name: "m", text: `{"verdict":"unknown","confidence":0.2,"rationale":"sin evidencia suficiente"}`}
	j := newJudge(llm)

	score := model.NewScore("rest-001")
	score.Status = model.StatusClosureSuspected
	score.Reason = "Indicios de posible cierre"
	score.ClosureRisk = 0.7
	score.ConflictDetected = true

	if _, err := j.Review(context.Background(), testEstablishment(), testSignals(), score); err != nil {
		t.Fatalf("Review: %v", err)
	}
	req := llm.request
	if req == nil || len(req.Contents) != 1 || req.Contents[0].Parts[0].Text == "" {
		t.Fatalf("no dossier sent to the model")
	}
	dossier := req.Contents[0].Parts[0].Text
	for _, want := range []string{
		`"name":"Restaurante El Ancla"`,
		`"website":"https://elancla.example"`,
		`"raw_value":"permanently_closed"`,
		`"failed_signals":1`,
		`"status":"closure_suspected"`,
		`"conflict":true`,
		`"age_days":1`,
	} {
		if !strings.Contains(dossier, want) {
			t.Errorf("dossier missing %s:\n%s", want, dossier)
		}
	}
	if strings.Contains(dossier, "overpass timeout") {
		t.Error("errored signal leaked into the dossier")
	}
	if req.Config == nil || req.Config.Temperature == nil || *req.Config.Temperature != 0 {
		t.Error("judge call must run at temperature 0")
	}
	if req.Config.SystemInstruction == nil {
		t.Error("judge call missing the system instruction")
	}
}

func TestReviewCapsDossierSignals(t *testing.T) {
	llm := &fakeLLM{name: "m", text: `{"verdict":"active","confidence":0.5,"rationale":"ok"}`}
	j := judge.New(judge.Config{ModelName: "m", MaxSignals: 1}, fakeFactory(llm), logx.New(io.Discard))

	signals := testSignals()[:2] // both without error
	if _, err := j.Review(context.Background(), testEstablishment(), signals, nil); err != nil {
		t.Fatalf("Review: %v", err)
	}
	dossier := llm.request.Contents[0].Parts[0].Text
	if !strings.Contains(dossier, `"raw_value":"permanently_closed"`) {
		t.Error("newest signal missing from the capped dossier")
	}
	if strings.Contains(dossier, `"raw_value":"down"`) {
		t.Error("cap did not drop the older signal")
	}
}

func TestReviewRejectsBadAnswers(t *testing.T) {
	cases := []struct {
		name string
		text string
		want string
	}{
		{"empty", "", "empty model answer"},
		{"no json", "Está activo, sin duda.", "no JSON object"},
		{"bad json", `{"verdict":"active","confidence":"alto","rationale":"x"}`, "unmarshal verdict"},
		{"unknown enum", `{"verdict":"abierto","confidence":0.5,"rationale":"x"}`, "unknown verdict"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			j := newJudge(&fakeLLM{name: "m", text: tc.text})
			_, err := j.Review(context.Background(), testEstablishment(), nil, nil)
			if err == nil {
				t.Fatalf("expected an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not contain %q", err, tc.want)
			}
		})
	}
}

func TestReviewModelFailure(t *testing.T) {
	j := newJudge(&fakeLLM{name: "m", err: errors.New("gateway 502")})
	if _, err := j.Review(context.Background(), testEstablishment(), testSignals(), nil); err == nil {
		t.Fatal("expected the model error to surface")
	}
}

func TestParseVerdictTruncatesRationale(t *testing.T) {
	long := strings.Repeat("a", 600)
	llm := &fakeLLM{name: "m", text: `{"verdict":"active","confidence":0.5,"rationale":"` + long + `"}`}
	j := newJudge(llm)

	verdict, err := j.Review(context.Background(), testEstablishment(), nil, nil)
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if got := len([]rune(verdict.Rationale)); got > 401 { // 400 + ellipsis
		t.Errorf("rationale has %d runes, want at most 401", got)
	}
	if !strings.HasSuffix(verdict.Rationale, "…") {
		t.Errorf("truncated rationale must end with an ellipsis: %q", verdict.Rationale)
	}
}

func TestParseVerdictDefaultsRationale(t *testing.T) {
	llm := &fakeLLM{name: "m", text: `{"verdict":"seasonal","confidence":0.5}`}
	j := newJudge(llm)

	verdict, err := j.Review(context.Background(), testEstablishment(), nil, nil)
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if verdict.Rationale == "" {
		t.Error("empty rationale must default to a placeholder")
	}
}
