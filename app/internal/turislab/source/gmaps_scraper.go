// Package source - Google Maps scraper source (gosom/kit, external process).
//
// GMapsScraper runs an external binary that scrapes Google Maps Place data
// and converts the results into Signals.
package source

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/datadope/turislab-doctor/internal/turislab/model"
)

// GMapsScraperConfig holds tunable parameters for the Google Maps scraper.
type GMapsScraperConfig struct {
	// Provider selects which scraper flavour to use: "gosom" or "kit".
	Provider string

	// Binary is the absolute or PATH-resolvable path to the scraper executable.
	Binary string

	TimeoutSeconds int
	MaxPlaces      int
	Depth          int
	Language       string
	CacheTTLDays   int
	IncludeReviews bool

	// ProxyURL is resolved from the environment by the caller before
	// constructing the source.
	ProxyURL string

	// FallbackProvider is used if the primary provider binary fails.
	FallbackProvider string

	// Headless controls whether the browser (if any) runs without a GUI.
	Headless bool
}

// GMapsScraper implements SignalSource by executing an external scraper binary.
type GMapsScraper struct {
	cfg GMapsScraperConfig

	mu               sync.Mutex
	healthy          bool
	lastError        string
	lastRunAt        *time.Time
	signalsCollected int
}

// NewGMapsScraper creates a ready-to-use GMapsScraper.
func NewGMapsScraper(cfg GMapsScraperConfig) *GMapsScraper {
	if cfg.TimeoutSeconds <= 0 {
		cfg.TimeoutSeconds = 120
	}
	if cfg.MaxPlaces <= 0 {
		cfg.MaxPlaces = 5
	}
	if cfg.Depth <= 0 {
		cfg.Depth = 1
	}
	if cfg.Language == "" {
		cfg.Language = "en"
	}
	if cfg.CacheTTLDays <= 0 {
		cfg.CacheTTLDays = 30
	}

	return &GMapsScraper{
		cfg:     cfg,
		healthy: true,
	}
}

// Name implements SignalSource.
func (g *GMapsScraper) Name() string { return "google_maps_scraper" }

// Enabled implements SignalSource — only enabled when both Binary and Provider
// are configured.
func (g *GMapsScraper) Enabled() bool {
	return g.cfg.Binary != "" && g.cfg.Provider != ""
}

// Capabilities implements SignalSource.
func (g *GMapsScraper) Capabilities() []model.SignalKind {
	return []model.SignalKind{
		model.KindBusinessStatus,
		model.KindRating,
		model.KindReviewCount,
	}
}

// Health implements SignalSource.
func (g *GMapsScraper) Health() SourceHealth {
	g.mu.Lock()
	defer g.mu.Unlock()

	return SourceHealth{
		Name:             g.Name(),
		Enabled:          g.Enabled(),
		Healthy:          g.healthy,
		LastError:        g.lastError,
		LastRunAt:        g.lastRunAt,
		SignalsCollected: g.signalsCollected,
		CacheTTLDays:     g.cfg.CacheTTLDays,
	}
}

// gosomPlace mirrors the JSON record produced by the gosom scraper. Only the
// fields turislab consumes are declared; gosom emits about three dozen more.
// Note that the business name lives in "title", not "name".
type gosomPlace struct {
	PlaceID     string  `json:"place_id"`
	Title       string  `json:"title"`
	Status      string  `json:"status"`
	Rating      float64 `json:"review_rating"`
	ReviewCount int     `json:"review_count"`
	Latitude    float64 `json:"latitude"`
	Longitude   float64 `json:"longitude"`
	Address     string  `json:"address"`
}

// placeStatus is the closed-state classification turislab needs.
type placeStatus int

const (
	placeOpen placeStatus = iota
	placeTemporarilyClosed
	placePermanentlyClosed
)

// classifyStatus interprets gosom's Status field. gosom does NOT expose a
// clean enum: for open places the field usually carries the opening-hours
// tagline ("Closed ⋅ Opens 12:30 pm Tue" / "Cerrado ⋅ Abre a las 13:00") or
// the business tagline, while permanently closed places carry a closed-state
// enum that is localized according to -lang (gmaps/entry.go falls back to
// [88][0]). With -lang es a closed place reports "CERRADO", not "CLOSED".
//
// Matching the bare substring "closed"/"cerrado" would flag every place that
// is merely outside its opening hours, which is the common case for
// restaurants, so the enum is matched exactly and the prose variants require
// an explicit "permanently"/"temporarily" qualifier.
func classifyStatus(raw string) placeStatus {
	s := strings.ToLower(strings.TrimSpace(raw))
	switch {
	case s == "":
		return placeOpen
	case s == "closed", s == "cerrado", s == "tancat", s == "pechado", s == "itxita",
		strings.Contains(s, "permanently closed"),
		strings.Contains(s, "permanentemente cerrado"),
		strings.Contains(s, "cerrado permanentemente"),
		strings.Contains(s, "cerrado definitivamente"),
		strings.Contains(s, "cierre permanente"):
		return placePermanentlyClosed
	case strings.Contains(s, "temporarily closed"),
		strings.Contains(s, "temporalmente cerrado"),
		strings.Contains(s, "cerrado temporalmente"),
		strings.Contains(s, "cierre temporal"),
		strings.Contains(s, "tancat temporalment"):
		return placeTemporarilyClosed
	default:
		return placeOpen
	}
}

// Collect implements SignalSource.
func (g *GMapsScraper) Collect(ctx context.Context, est *model.Establishment) ([]*model.Signal, error) {
	now := time.Now()

	// Build the search query.
	query := strings.TrimSpace(fmt.Sprintf("%s %s %s", est.Name, est.Address, est.Municipality))

	places, err := g.runScraper(ctx, est, query)
	if err != nil {
		log.Printf("[gmaps_scraper] binary execution failed for %q: %v", est.Name, err)
		g.recordRun(now, err.Error(), 0, false)
		return nil, nil
	}

	if len(places) == 0 {
		g.recordRun(now, "", 0, true)
		return nil, nil
	}

	// Use the first result as the best match.
	place := places[0]

	var signals []*model.Signal

	// ── Business status signal ────────────────────────────────────────────────
	if place.Status != "" {
		sig := NewSignal(est.ID, g.Name(), model.KindBusinessStatus, model.FamilyDirectStatus)
		sig.TTLDays = g.cfg.CacheTTLDays
		sig.Confidence = 0.75
		sig.Weight = 0.90

		switch classifyStatus(place.Status) {
		case placePermanentlyClosed:
			sig.RawValue = "permanently_closed"
			sig.Value = 1.0
		case placeTemporarilyClosed:
			sig.RawValue = "temporarily_closed"
			sig.Value = 0.4
			sig.Confidence = 0.70
			sig.Weight = 0.40
		default:
			// "Open ⋅ Closes …", "Closed ⋅ Opens …" or a tagline: the place
			// is open, possibly outside its current opening hours.
			sig.RawValue = "open"
			sig.Value = 0.0
		}

		sig.DedupeKey = DedupeKey(est.ID, g.Name(), string(model.KindBusinessStatus), sig.RawValue)
		signals = append(signals, sig)
	}

	// ── Rating signal ─────────────────────────────────────────────────────────
	if place.Rating > 0 {
		rs := NewSignal(est.ID, g.Name(), model.KindRating, model.FamilyReviews)
		rs.RawValue = fmt.Sprintf("%.1f", place.Rating)
		rs.Value = place.Rating
		rs.Confidence = 0.70
		rs.Weight = 0.30
		rs.TTLDays = g.cfg.CacheTTLDays
		rs.DedupeKey = DedupeKey(est.ID, g.Name(), string(model.KindRating), rs.RawValue)
		signals = append(signals, rs)
	}

	// ── Review count signal ───────────────────────────────────────────────────
	if place.ReviewCount > 0 {
		cs := NewSignal(est.ID, g.Name(), model.KindReviewCount, model.FamilyReviews)
		cs.RawValue = strconv.Itoa(place.ReviewCount)
		cs.Value = float64(place.ReviewCount)
		cs.Confidence = 0.70
		cs.Weight = 0.20
		cs.TTLDays = g.cfg.CacheTTLDays
		cs.DedupeKey = DedupeKey(est.ID, g.Name(), string(model.KindReviewCount), cs.RawValue)
		signals = append(signals, cs)
	}

	g.recordRun(now, "", len(signals), true)
	return signals, nil
}

// runScraper executes the configured binary and returns parsed places.
// On any execution or parse error it returns the error so Collect can log it
// and return gracefully.
func (g *GMapsScraper) runScraper(ctx context.Context, est *model.Establishment, query string) ([]gosomPlace, error) {
	// Write the query to a temp input file.
	inputFile, err := os.CreateTemp("", "turislab-gmaps-in-*.txt")
	if err != nil {
		return nil, fmt.Errorf("creating temp input file: %w", err)
	}
	defer os.Remove(inputFile.Name())

	if _, err := fmt.Fprintln(inputFile, query); err != nil {
		inputFile.Close()
		return nil, fmt.Errorf("writing query to temp file: %w", err)
	}
	inputFile.Close()

	// Prepare temp output file.
	outputFile, err := os.CreateTemp("", "turislab-gmaps-out-*.json")
	if err != nil {
		return nil, fmt.Errorf("creating temp output file: %w", err)
	}
	outputFile.Close()
	defer os.Remove(outputFile.Name())

	// Build args for the supported provider.
	var args []string
	switch strings.ToLower(g.cfg.Provider) {
	case "gosom":
		args = []string{
			"-input", inputFile.Name(),
			"-results", outputFile.Name(),
			"-json",
			"-depth", strconv.Itoa(g.cfg.Depth),
			"-lang", g.cfg.Language,
			"-c", "1",
			// gosom has no result-count flag (MaxPlaces is applied after
			// parsing). Stop the crawl when Google stalls so a blocked run
			// does not hang until the context deadline.
			"-exit-on-inactivity", "30s",
		}
		if est != nil && est.Lat != nil && est.Lng != nil {
			args = append(args, "-geo", fmt.Sprintf("%.6f,%.6f", *est.Lat, *est.Lng))
		}
		if !g.cfg.Headless {
			args = append(args, "-debug")
		}
		if g.cfg.ProxyURL != "" {
			args = append(args, "-proxies", g.cfg.ProxyURL)
		}
	default:
		// kit or unknown — use same convention; callers can extend.
		args = []string{
			"--input", inputFile.Name(),
			"--output", outputFile.Name(),
			"--lang", g.cfg.Language,
			"--count", strconv.Itoa(g.cfg.MaxPlaces),
		}
	}

	timeout := time.Duration(g.cfg.TimeoutSeconds) * time.Second
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, g.cfg.Binary, args...) //nolint:gosec
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("scraper exited with error: %w (stderr: %s)", err, stderr.String())
	}

	// Read the output file.
	data, err := os.ReadFile(outputFile.Name())
	if err != nil {
		return nil, fmt.Errorf("reading scraper output: %w", err)
	}
	if len(data) == 0 {
		return nil, nil
	}

	places, err := parsePlaces(data)
	if err != nil {
		return nil, err
	}
	// gosom exposes no "max results" flag; enforce the configured cap here.
	if g.cfg.MaxPlaces > 0 && len(places) > g.cfg.MaxPlaces {
		places = places[:g.cfg.MaxPlaces]
	}
	return places, nil
}

// parsePlaces handles the shapes gosom can produce: one JSON object per line
// (NDJSON, its normal output), a JSON array, or a single pretty-printed
// object.
func parsePlaces(data []byte) ([]gosomPlace, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil, nil
	}

	// Try JSON array first.
	if data[0] == '[' {
		var places []gosomPlace
		if err := json.Unmarshal(data, &places); err != nil {
			return nil, fmt.Errorf("parsing JSON array: %w", err)
		}
		return places, nil
	}

	// Fall back to NDJSON.
	var places []gosomPlace
	var skipped int
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var p gosomPlace
		if err := json.Unmarshal(line, &p); err != nil {
			skipped++
			continue
		}
		places = append(places, p)
	}
	if err := scanner.Err(); err != nil {
		return places, fmt.Errorf("scanning NDJSON: %w", err)
	}
	if len(places) > 0 {
		if skipped > 0 {
			log.Printf("[gmaps_scraper] skipped %d malformed NDJSON line(s)", skipped)
		}
		return places, nil
	}

	// A single pretty-printed object spans several lines and cannot be read
	// as NDJSON. Accept it as a last resort.
	var single gosomPlace
	if err := json.Unmarshal(data, &single); err == nil && single.PlaceID != "" {
		return []gosomPlace{single}, nil
	}
	if skipped > 0 {
		log.Printf("[gmaps_scraper] output is neither NDJSON nor a single place object")
	}
	return nil, nil
}

// recordRun updates the health state under the mutex.
func (g *GMapsScraper) recordRun(at time.Time, errMsg string, count int, healthy bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.lastRunAt = &at
	g.lastError = errMsg
	g.healthy = healthy
	g.signalsCollected += count
}
