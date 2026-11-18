// Package judge implements the LLM-as-a-judge verdict layer.
//
// The deterministic scoring engine turns signals into a risk, a priority and
// a suggested status. The judge sits one step after it: it hands the whole
// evidence dossier of one establishment — registry data, every collected
// signal with its source and age, and the engine's own verdict — to an LLM,
// which issues the final status with a rationale, its confidence and its own
// closure risk over the same evidence.
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

Cómo leer las señales:
- "value" va siempre en la misma escala: 0 = parece vivo/operativo, 1 = parece cerrado/inactivo.
- "age_days" es la antigüedad de la observación, no la fecha del hecho.
- "family=direct_status": estado que una fuente declara (p. ej. Google Maps marca cierre permanente). Es la evidencia más fuerte.
- "family=activity": señales indirectas de que el negocio sigue o no activo. Incluye "review_recency" (días desde la última reseña de Google, con la fecha en "raw_value"), "web_status" (la web oficial responde, da 404 o no carga) y "osm_tag". Una web oficial que no carga y una última reseña antigua SON evidencia de inactividad, no ausencia de datos: pesan menos que un cierre declarado, pero cuentan.
- "family=reviews" (nota media, número de reseñas) describe calidad, no si el negocio sigue abierto: no la uses para deducir un cierre.
- "website_found" solo aporta la URL que conoce Google: es un dato, no evidencia.
- "engine.risk_known=false" significa que el motor no midió el riesgo de cierre (no es que sea 0): no lo interpretes como "está abierto".

Reglas:
1. Juzga solo con la evidencia del expediente. No inventes hechos ni consultes nada externo.
2. Pesa cada señal por su tipo, fuente y frescura: un cierre permanente confirmado por Google Maps pesa más que una web caída o una señal de hace semanas.
3. El veredicto heurístico del motor es una referencia, no una orden: confírmalo o corrígelo razonando sobre la evidencia.
4. No confirmes un cierre ("closure_confirmed") con evidencia débil o contradictoria: usa "closure_suspected" o "unknown" y baja la confianza.
5. Un local "cerrado ahora" por horario no es un cierre permanente: distingue "closed_temporarily" y "seasonal" solo cuando la evidencia lo diga.
6. Con poca o nula evidencia, responde "unknown" con confianza baja en vez de adivinar. Pero si hay señales de actividad (web viva, reseñas recientes), no respondas "unknown": úsalas.
7. Si la web responde y hay reseñas recientes, el negocio probablemente sigue activo aunque falte el estado de Google.
8. El riesgo es tuyo: pondera todas las señales del expediente y da un número coherente con tu veredicto. No copies sin más el riesgo del motor: si tu veredicto lo corrige, el número debe reflejarlo.

Responde SOLO con un objeto JSON válido, sin markdown ni texto alrededor, con esta forma exacta:
{"verdict":"active|closure_suspected|closure_confirmed|closed_temporarily|seasonal|unknown","confidence":0.0,"risk":0.0,"rationale":"una o dos frases en español citando las señales que motivan la decisión"}
- confidence: 0.0-1.0, tu certeza sobre el veredicto.
- risk: 0.0-1.0, tu riesgo de cierre: 0 = sin riesgo, el negocio está claramente operativo; 1 = cierre seguro. Debe ser coherente con el veredicto (un "active" con evidencia viva va cerca de 0; un "closure_confirmed" va cerca de 1) y con la evidencia: una web caída o una última reseña de hace años suben el número aunque no haya estado directo.
- rationale: en español, menciona fuentes concretas (p. ej. "Google Maps lo marca como cerrado permanentemente desde hace días" o "la última reseña es de hace más de un año y la web no responde").`

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
	// Risk is the judge's own closure risk, 0-1, reasoned over every signal
	// in the dossier. It sits beside the engine's ClosureRisk: the engine
	// is the deterministic anchor, the judge is the second opinion.
	Risk float64
	// riskProvided records whether the model actually answered a risk.
	// When it did not, Review falls back to the engine's (or the status
	// implied by the verdict) so the UI never shows a fake 0%.
	riskProvided bool
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
	// Facts are the few human-readable conclusions the model should not have
	// to rebuild from signal values: when the newest review was, and whether
	// the official website answered.
	Facts struct {
		Website       string `json:"website,omitempty"`
		WebsiteStatus string `json:"website_status,omitempty"`
		LastReview    string `json:"last_review,omitempty"`
	} `json:"facts"`
	Engine struct {
		ClosureRisk   *float64              `json:"closure_risk,omitempty"`
		RiskKnown     bool                  `json:"risk_known"`
		Status        model.StatusSuggested `json:"status"`
		Reason        string                `json:"reason"`
		EvidenceCount int                   `json:"evidence_count"`
		Conflict      bool                  `json:"conflict"`
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
	d.Facts.Website = est.Website

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
		switch sig.Kind {
		case model.KindReviewRecency:
			// Raw value is "YYYY-MM-DD (hace N días)": exactly the fact the
			// model needs, so it is surfaced on its own too.
			d.Facts.LastReview = sig.RawValue
		case model.KindWebsiteFound:
			if d.Facts.Website == "" {
				d.Facts.Website = sig.RawValue
			}
		case model.KindWebStatus:
			d.Facts.WebsiteStatus = sig.RawValue
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
		d.Engine.RiskKnown = score.RiskKnown
		if score.RiskKnown {
			risk := score.ClosureRisk
			d.Engine.ClosureRisk = &risk
		}
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
	if !verdict.riskProvided {
		verdict.Risk = fallbackRisk(verdict.Status, score)
	}
	name := j.cfg.ModelName
	if name == "" && llm != nil {
		name = llm.Name()
	}
	verdict.Model = name

	j.log.Info("judge", "[%s] veredicto %s (confianza %.2f, riesgo %.0f%%) en %s — %s",
		est.ID, verdict.Status, verdict.Confidence, verdict.Risk*100,
		time.Since(started).Round(time.Millisecond), verdict.Rationale)
	return verdict, nil
}

// ─── verdict parsing ──────────────────────────────────────────────────────────

// rawVerdict mirrors the JSON shape the model must answer.
type rawVerdict struct {
	Verdict    string   `json:"verdict"`
	Confidence float64  `json:"confidence"`
	Risk       *float64 `json:"risk"`
	Rationale  string   `json:"rationale"`
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

	verdict := &Verdict{
		Status:     status,
		Confidence: confidence,
		Rationale:  rationale,
	}
	if raw.Risk != nil && !math.IsNaN(*raw.Risk) {
		verdict.Risk = clamp01(*raw.Risk)
		verdict.riskProvided = true
	}
	return verdict, nil
}

// clamp01 keeps a value inside the shared 0-1 risk scale.
func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// fallbackRisk is the judge risk used when the model omitted the field:
// the engine's risk when it was measured, otherwise the risk the verdict
// itself implies. It exists so an older or terse model answer still yields
// a number coherent with the status instead of a hardcoded 0%.
func fallbackRisk(status model.StatusSuggested, score *model.Score) float64 {
	if score != nil && score.RiskKnown {
		return clamp01(score.ClosureRisk)
	}
	switch status {
	case model.StatusClosureConfirmed:
		return 0.90
	case model.StatusClosureSuspected:
		return 0.65
	case model.StatusTemporary, model.StatusSeasonal:
		return 0.50
	case model.StatusActive:
		return 0.05
	default:
		return 0.50
	}
}

func ptr[T any](v T) *T { return &v }
