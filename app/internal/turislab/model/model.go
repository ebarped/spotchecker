// Package model holds the core domain types for the TurisLab Doctor system.
package model

import "time"

// ─── Establishment ────────────────────────────────────────────────────────────

// Category classifies an establishment into a broad bucket.
type Category string

const (
	CategoryRestaurant Category = "restaurant"
	CategoryHotel      Category = "hotel"
	CategoryBar        Category = "bar"
	CategoryOther      Category = "other"
)

// Establishment holds one entry from the input database.
type Establishment struct {
	ID                   string     `json:"id"`
	Name                 string     `json:"name"`
	Category             Category   `json:"category"`
	Stars                *int       `json:"stars,omitempty"`
	Address              string     `json:"address"`
	Municipality         string     `json:"municipality"`
	Province             string     `json:"province"`
	PostalCode           string     `json:"postal_code"`
	Country              string     `json:"country"`
	Lat                  *float64   `json:"lat,omitempty"`
	Lng                  *float64   `json:"lng,omitempty"`
	Phone                string     `json:"phone"`
	Email                string     `json:"email"`
	Website              string     `json:"website"`
	RegistryID           string     `json:"registry_id"`
	Certifications       []string   `json:"certifications,omitempty"`
	LastInspectionAt     *time.Time `json:"last_inspection_at,omitempty"`
	LastInspectionResult string     `json:"last_inspection_result"`
	GooglePlaceID        string     `json:"google_place_id"`
	TripAdvisorID        string     `json:"tripadvisor_id"`
	YelpID               string     `json:"yelp_id"`
	OSMID                string     `json:"osm_id"`
	Notes                string     `json:"notes"`
	IDGenerated          bool       `json:"id_generated"`
}

// ─── Signal ───────────────────────────────────────────────────────────────────

// SignalKind is the semantic category of a raw evidence signal.
type SignalKind string

const (
	KindBusinessStatus SignalKind = "business_status"
	KindRating         SignalKind = "rating"
	KindReviewCount    SignalKind = "review_count"
	KindWebStatus      SignalKind = "web_status"
	KindWebKeyword     SignalKind = "web_keyword"
	KindOSMTag         SignalKind = "osm_tag"
	KindOvertureStatus SignalKind = "overture_status"
	KindPhoneResult    SignalKind = "phone_result"
	KindReviewText     SignalKind = "review_text"
)

// SignalFamily groups signals by the type of evidence they contribute.
type SignalFamily string

const (
	FamilyDirectStatus SignalFamily = "direct_status"
	FamilyActivity     SignalFamily = "activity"
	FamilyContext      SignalFamily = "context"
	FamilyOfficial     SignalFamily = "official"
	FamilyReviews      SignalFamily = "reviews"
	FamilyProduct      SignalFamily = "product"
)

// Signal is one piece of raw evidence collected by a source adapter.
type Signal struct {
	ID              string       `json:"id"`
	EstablishmentID string       `json:"establishment_id"`
	SourceName      string       `json:"source_name"`
	Kind            SignalKind   `json:"kind"`
	CollectedAt     time.Time    `json:"collected_at"`
	Value           float64      `json:"value"`
	RawValue        string       `json:"raw_value"`
	Confidence      float64      `json:"confidence"`
	Weight          float64      `json:"weight"`
	Family          SignalFamily `json:"family"`
	DedupeKey       string       `json:"dedupe_key"`
	TTLDays         int          `json:"ttl_days"`
	Error           string       `json:"error,omitempty"`
	Payload         []byte       `json:"payload,omitempty"`
}

// ─── Evidence ─────────────────────────────────────────────────────────────────

// Evidence is a processed, decay-adjusted signal ready to be consumed by the scoring engine.
type Evidence struct {
	SignalID        string       `json:"signal_id"`
	EstablishmentID string       `json:"establishment_id"`
	Family          SignalFamily `json:"family"`
	Kind            SignalKind   `json:"kind"`
	Source          string       `json:"source"`
	CollectedAt     time.Time    `json:"collected_at"`
	Value           float64      `json:"value"`
	Confidence      float64      `json:"confidence"`
	Weight          float64      `json:"weight"`
	DecayFactor     float64      `json:"decay_factor"`
	EffectiveWeight float64      `json:"effective_weight"`
	Label           string       `json:"label"`
	Detail          string       `json:"detail"`
}

// ─── Score ────────────────────────────────────────────────────────────────────

type TierLabel string

const (
	TierP0          TierLabel = "P0"
	TierP1          TierLabel = "P1"
	TierP2          TierLabel = "P2"
	TierP3          TierLabel = "P3"
	TierCollectData TierLabel = "collect_data"
)

type StatusSuggested string

const (
	StatusActive           StatusSuggested = "active"
	StatusClosureSuspected StatusSuggested = "closure_suspected"
	StatusClosureConfirmed StatusSuggested = "closure_confirmed"
	StatusTemporary        StatusSuggested = "closed_temporarily"
	StatusSeasonal         StatusSuggested = "seasonal"
	StatusUnknown          StatusSuggested = "unknown"
)

type Score struct {
	EstablishmentID  string          `json:"establishment_id"`
	ComputedAt       time.Time       `json:"computed_at"`
	ClosureRisk      float64         `json:"closure_risk"`
	QualityRisk      float64         `json:"quality_risk"`
	QualityDelta     float64         `json:"quality_delta"`
	Priority         float64         `json:"priority"`
	Tier             TierLabel       `json:"tier"`
	Confidence       float64         `json:"confidence"`
	EvidenceCount    int             `json:"evidence_count"`
	Status           StatusSuggested `json:"status"`
	Reason           string          `json:"reason"`
	Volatile         bool            `json:"volatile"`
	ConflictDetected bool            `json:"conflict_detected"`
}

func NewScore(id string) *Score {
	return &Score{
		EstablishmentID: id,
		ComputedAt:      time.Now(),
		Status:          StatusUnknown,
	}
}

type TierConfig struct {
	P0Min                 float64
	P1Min                 float64
	P2Min                 float64
	MinConfidenceP0       float64
	CollectDataConfidence float64
}

func DefaultTierConfig() TierConfig {
	// Thresholds calibrated for the PoC evidence coverage: only closure and
	// staleness contribute, so priorities top out around 50. Raise them
	// (e.g. 80/65/45) when quality/impact sources come online.
	return TierConfig{
		P0Min:                 45,
		P1Min:                 35,
		P2Min:                 20,
		MinConfidenceP0:       0.5,
		CollectDataConfidence: 0.35,
	}
}

func (s *Score) AssignTier(cfg TierConfig) {
	switch {
	case s.Confidence < cfg.CollectDataConfidence:
		s.Tier = TierCollectData
	case s.Priority >= cfg.P0Min && s.Confidence >= cfg.MinConfidenceP0:
		s.Tier = TierP0
	case s.Priority >= cfg.P0Min:
		s.Tier = TierP1
	case s.Priority >= cfg.P1Min:
		s.Tier = TierP1
	case s.Priority >= cfg.P2Min:
		s.Tier = TierP2
	default:
		s.Tier = TierP3
	}
}

// ─── Queue ────────────────────────────────────────────────────────────────────

type QueueItem struct {
	Rank           int            `json:"rank"`
	Establishment  *Establishment `json:"establishment"`
	Score          *Score         `json:"score"`
	PrimarySignals []Evidence     `json:"primary_signals"`
	ScanAge        time.Duration  `json:"scan_age_ns"`
}

// ─── Report ───────────────────────────────────────────────────────────────────

type Report struct {
	GeneratedAt        time.Time      `json:"generated_at"`
	EstablishmentCount int            `json:"establishment_count"`
	Queue              []QueueItem    `json:"queue"`
	Summary            ReportSummary  `json:"summary"`
	ScanCursor         string         `json:"scan_cursor"`
	ScanRunning        bool           `json:"scan_running"`
	ScanError          string         `json:"scan_error,omitempty"`
	Sources            []SourceStatus `json:"sources"`
}

type ReportSummary struct {
	Total            int     `json:"total"`
	P0               int     `json:"p0"`
	P1               int     `json:"p1"`
	P2               int     `json:"p2"`
	P3               int     `json:"p3"`
	CollectData      int     `json:"collect_data"`
	ClosureConfirmed int     `json:"closure_confirmed"`
	LowConfidence    int     `json:"low_confidence"`
	Coverage         float64 `json:"coverage_pct"`
	ScanCostEUR      float64 `json:"scan_cost_eur"`
}

type SourceStatus struct {
	Name             string     `json:"name"`
	Enabled          bool       `json:"enabled"`
	Healthy          bool       `json:"healthy"`
	LastError        string     `json:"last_error,omitempty"`
	LastRunAt        *time.Time `json:"last_run_at,omitempty"`
	SignalsCollected int        `json:"signals_collected"`
	CacheTTLDays     int        `json:"cache_ttl_days"`
	QuotaUsed        int        `json:"quota_used"`
	QuotaLimit       int        `json:"quota_limit"`
}
