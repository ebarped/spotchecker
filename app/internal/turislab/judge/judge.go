// Package judge implements the LLM-as-a-judge verdict layer.
//
// The deterministic scoring engine turns signals into a risk, a priority and
// a suggested status. The judge sits one step after it: it hands the whole
// evidence dossier of one establishment — registry data, every collected
// signal with its source and age, and the engine's own verdict — to an LLM,
// which issues the final status with a rationale and its confidence.
//
// The judge is strictly optional: when it is disabled, or when its call
// fails, the engine's verdict stays the final one. It decides the *what*
// (the status an establishment is in); the engine still decides the *who
// first* (priority and tier), so the queue position never depends on a
// model round-trip.
package judge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	adkmodel "google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"github.com/datadope/turislab-doctor/internal/logx"
	"github.com/datadope/turislab-doctor/internal/turislab/model"
)

const (
	// defaultTimeout bounds one judge call. A verdict must not stall the
	// scan tick: past this the establishment keeps the engine's verdict.
	defaultTimeout = 60 * time.Second

	// defaultMaxSignals caps how many signals ride in the dossier. The
	// store returns them newest first, so the cap drops the oldest tail.
	defaultMaxSignals = 30

	// maxRationaleRunes bounds the rationale the UI and the queue show.
	maxRationaleRunes = 400
)

// judgeInstruction is the system prompt of the judge. The model sees only
// this and the dossier; it cannot call tools.
const judgeInstruction = `Eres el juez de Turislab Doctor. Decides el estado operativo final de un establecimiento turístico.

Recibes un expediente en JSON: los datos de registro del establecimiento, las señales recogidas por las fuentes públicas (web, OSM, Google Maps…) con su fuente, antigüedad y confianza, y el veredicto heurístico del motor determinista.

Reglas:
1. Juzga solo con la evidencia del expediente. No inventes hechos ni consultes nada externo.
2. Pesa cada señal por su tipo, fuente y frescura: un cierre permanente confirmado por Google Maps pesa más que una web caída o una señal de hace semanas.
3. El veredicto heurístico del motor es una referencia, no una orden: confírmalo o corrígelo razonando sobre la evidencia.
4. No confirmes un cierre ("closure_confirmed") con evidencia débil o contradictoria: usa "closure_suspected" o "unknown" y baja la confianza.
5. Un local "cerrado ahora" por horario no es un cierre permanente: distingue "closed_temporarily" y "seasonal" solo cuando la evidencia lo diga.
6. Con poca o nula evidencia, responde "unknown" con confianza baja en vez de adivinar.

Responde SOLO con un objeto JSON válido, sin markdown ni texto alrededor, con esta forma exacta:
{"verdict":"active|closure_suspected|closure_confirmed|closed_temporarily|seasonal|unknown","confidence":0.0,"rationale":"una o dos frases en español citando las señales que motivan la decisión"}
- confidence: 0.0-1.0, tu certeza sobre el veredicto.
- rationale: en español, menciona fuentes concretas (p. ej. "Google Maps lo marca como cerrado permanentemente desde hace días").`

// Config carries the judge settings, decoupled from any config package.
type Config struct {
	// ModelName labels the verdict in the UI and the report ("which model
	// judged"); the factory below selects the real client.
	ModelName string
	// Timeout bounds one Review call. Zero means defaultTimeout.
	Timeout time.Duration
	// MaxSignals caps the signals in the dossier. Zero means
	// defaultMaxSignals.
	MaxSignals int
}

// Verdict is the judge's decision for one establishment.
type Verdict struct {
	// Status is the final suggested status.
	Status model.StatusSuggested
	// Confidence is the model's own certainty, 0-1. It is reported
	// separately: the score's evidence confidence (and with it the tier)
	// stays what the engine computed.
	Confidence float64
	// Rationale explains the verdict in Spanish, citing the evidence.
	Rationale string
	// Model names the LLM that judged.
	Model string
}

// Judge reviews evidence dossiers with an LLM.
type Judge struct {
	cfg Config
	log *logx.Logger
	// modelFactory builds the LLM per review; injectable so tests drive
	// the judge with a scripted model.
	modelFactory func(context.Context) (adkmodel.LLM, error)
}

// Option customizes the Judge.
type Option func(*Judge)

// WithModelFactory replaces the configured LLM construction. Tests use it
// to judge without touching a real provider.
func WithModelFactory(factory func(context.Context) (adkmodel.LLM, error)) Option {
	return func(j *Judge) { j.modelFactory = factory }
}

// New builds the judge. The factory comes from the agent package
// (agent.ModelFactory) so both features share the same provider setup.
func New(cfg Config, factory func(context.Context) (adkmodel.LLM, error), log *logx.Logger, opts ...Option) *Judge {
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultTimeout
	}
	if cfg.MaxSignals <= 0 {
		cfg.MaxSignals = defaultMaxSignals
	}
	j := &Judge{cfg: cfg, log: log, modelFactory: factory}
	for _, opt := range opts {
		opt(j)
	}
	return j
}

// ModelName reports the configured judge model.
func (j *Judge) ModelName() string { return j.cfg.ModelName }

// ─── dossier ──────────────────────────────────────────────────────────────────

// dossier is the evidence pack the model reads. Field names stay terse so
// the prompt stays small.
type dossier struct {
	Establishment struct {
		ID           string `json:"id"`
		Name         string `json:"name"`
		Category     string `json:"category,omitempty"`
		Municipality string `json:"municipality,omitempty"`
		Website      string `json:"website,omitempty"`
	} `json:"establishment"`
	Signals []dossierSignal `json:"signals"`
	// FailedSignals counts signals dropped for carrying an error: the
	// model should know the sources tried and failed, without the noise.
	FailedSignals int `json:"failed_signals"`
	Engine        struct {
		ClosureRisk   float64                  `json:"closure_risk"`
		Status        model.StatusSuggested    `json:"status"`
		Reason        string                   `json:"reason"`
		EvidenceCount int                      `json:"evidence_count"`
		Conflict      bool                     `json:"conflict"`
	} `json:"engine"`
}

type dossierSignal struct {
	Source     string  `json:"source"`
	Kind       string  `json:"kind"`
	Family     string  `json:"family,omitempty"`
	RawValue   string  `json:"raw_value,omitempty"`
	Value      float64 `json:"value"`
	Confidence float64 `json:"confidence"`
	AgeDays    float64 `json:"age_days"`
}

// buildDossier flattens one establishment into the JSON the model reads.
func buildDossier(est *model.Establishment, signals []*model.Signal, score *model.Score, maxSignals int) dossier {
	var d dossier
	d.Establishment.ID = est.ID
	d.Establishment.Name = est.Name
	d.Establishment.Category = string(est.Category)
	d.Establishment.Municipality = est.Municipality
	d.Establishment.Website = est.Website

	now := time.Now()
	for _, sig := range signals {
		if sig == nil {
			continue
		}
		if sig.Error != "" {
			d.FailedSignals++
			continue
		}
		if len(d.Signals) >= maxSignals {
			continue // the store returns newest first: drop the oldest tail
		}
		age := now.Sub(sig.CollectedAt).Hours() / 24.0
		if age < 0 {
			age = 0
		}
		d.Signals = append(d.Signals, dossierSignal{
			Source:     sig.SourceName,
			Kind:       string(sig.Kind),
			Family:     string(sig.Family),
			RawValue:   sig.RawValue,
			Value:      sig.Value,
			Confidence: sig.Confidence,
			AgeDays:    math.Round(age*10) / 10,
		})
	}

	if score != nil {
		d.Engine.ClosureRisk = score.ClosureRisk
		d.Engine.Status = score.Status
		d.Engine.Reason = score.Reason
		d.Engine.EvidenceCount = score.EvidenceCount
		d.Engine.Conflict = score.ConflictDetected
	}
	return d
}

// ─── review ───────────────────────────────────────────────────────────────────

// Review judges one establishment from its evidence and the engine's
// score. It returns an error (and no verdict) when the model fails or
// answers garbage; the caller then keeps the engine's verdict.
func (j *Judge) Review(ctx context.Context, est *model.Establishment, signals []*model.Signal, score *model.Score) (*Verdict, error) {
	llm, err := j.modelFactory(ctx)
	if err != nil {
		return nil, err
	}

	dossierJSON, err := json.Marshal(buildDossier(est, signals, score, j.cfg.MaxSignals))
	if err != nil {
		return nil, fmt.Errorf("build dossier: %w", err)
	}

	req := &adkmodel.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: string(dossierJSON)}}},
		},
		Config: &genai.GenerateContentConfig{
			SystemInstruction: &genai.Content{
				Role:  "system",
				Parts: []*genai.Part{{Text: judgeInstruction}},
			},
			// A judge wants determinism, not creativity.
			Temperature: ptr(float32(0)),
		},
	}

	reviewCtx, cancel := context.WithTimeout(ctx, j.cfg.Timeout)
	defer cancel()

	started := time.Now()
	var text string
	for resp, roundErr := range llm.GenerateContent(reviewCtx, req, false) {
		if roundErr != nil {
			return nil, fmt.Errorf("model round: %w", roundErr)
		}
		if resp.Content != nil {
			for _, part := range resp.Content.Parts {
				text += part.Text
			}
		}
	}

	verdict, err := parseVerdict(text)
	if err != nil {
		j.log.Warn("judge", "[%s] respuesta no válida del modelo: %v", est.ID, err)
		return nil, err
	}
	name := j.cfg.ModelName
	if name == "" && llm != nil {
		name = llm.Name()
	}
	verdict.Model = name

	j.log.Info("judge", "[%s] veredicto %s (confianza %.2f) en %s — %s",
		est.ID, verdict.Status, verdict.Confidence, time.Since(started).Round(time.Millisecond), verdict.Rationale)
	return verdict, nil
}

// ─── verdict parsing ──────────────────────────────────────────────────────────

// rawVerdict mirrors the JSON shape the model must answer.
type rawVerdict struct {
	Verdict    string  `json:"verdict"`
	Confidence float64 `json:"confidence"`
	Rationale  string  `json:"rationale"`
}

// parseVerdict extracts the verdict JSON from a model answer. Models wrap
// JSON in prose or markdown fences despite the instruction, so the parse
// scans for the outermost object instead of trusting the whole text.
func parseVerdict(text string) (*Verdict, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil, errors.New("empty model answer")
	}

	body := trimmed
	// Strip a markdown code fence if present.
	if strings.HasPrefix(body, "```") {
		if _, rest, ok := strings.Cut(body, "\n"); ok {
			body = rest
		}
		body = strings.TrimSuffix(strings.TrimSpace(body), "```")
	}
	start := strings.IndexByte(body, '{')
	end := strings.LastIndexByte(body, '}')
	if start < 0 || end <= start {
		return nil, fmt.Errorf("no JSON object in the answer: %.120s", trimmed)
	}

	var raw rawVerdict
	if err := json.Unmarshal([]byte(body[start:end+1]), &raw); err != nil {
		return nil, fmt.Errorf("unmarshal verdict: %w", err)
	}

	status := model.StatusSuggested(strings.ToLower(strings.TrimSpace(raw.Verdict)))
	switch status {
	case model.StatusActive, model.StatusClosureSuspected, model.StatusClosureConfirmed,
		model.StatusTemporary, model.StatusSeasonal, model.StatusUnknown:
	default:
		return nil, fmt.Errorf("unknown verdict %q", raw.Verdict)
	}

	confidence := raw.Confidence
	if math.IsNaN(confidence) || confidence < 0 {
		confidence = 0
	}
	if confidence > 1 {
		confidence = 1
	}

	rationale := strings.Join(strings.Fields(raw.Rationale), " ")
	if rationale == "" {
		rationale = "El juez no aportó justificación."
	}
	if utf8.RuneCountInString(rationale) > maxRationaleRunes {
		rationale = string([]rune(rationale)[:maxRationaleRunes]) + "…"
	}

	return &Verdict{
		Status:     status,
		Confidence: confidence,
		Rationale:  rationale,
	}, nil
}

func ptr[T any](v T) *T { return &v }
