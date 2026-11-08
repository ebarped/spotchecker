// Package source - OSM/Overpass collector.
//
// OSMSource queries the Overpass API for OpenStreetMap elements near an
// establishment and emits signals based on the tags found.
package source

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/datadope/turislab-doctor/internal/turislab/model"
)

// OSMConfig holds tunable parameters for the Overpass/OSM collector.
type OSMConfig struct {
	OverpassURL    string
	TimeoutSeconds int
	CacheTTLDays   int
	MaxElements    int
	UserAgent      string
}

// OSMSource implements SignalSource using the Overpass API.
type OSMSource struct {
	cfg    OSMConfig
	client *http.Client

	mu               sync.Mutex
	healthy          bool
	lastError        string
	lastRunAt        *time.Time
	signalsCollected int
}

// NewOSM creates a ready-to-use OSMSource.
func NewOSM(cfg OSMConfig) *OSMSource {
	if cfg.OverpassURL == "" {
		cfg.OverpassURL = "https://overpass-api.de/api/interpreter"
	}
	if cfg.TimeoutSeconds <= 0 {
		cfg.TimeoutSeconds = 30
	}
	if cfg.CacheTTLDays <= 0 {
		cfg.CacheTTLDays = 14
	}
	if cfg.MaxElements <= 0 {
		cfg.MaxElements = 50
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = "TurisLab-Doctor/1.0"
	}

	return &OSMSource{
		cfg:     cfg,
		client:  &http.Client{Timeout: time.Duration(cfg.TimeoutSeconds) * time.Second},
		healthy: true,
	}
}

// Name implements SignalSource.
func (o *OSMSource) Name() string { return "osm" }

// Enabled implements SignalSource.
func (o *OSMSource) Enabled() bool { return true }

// Capabilities implements SignalSource.
func (o *OSMSource) Capabilities() []model.SignalKind {
	return []model.SignalKind{model.KindOSMTag}
}

// Health implements SignalSource.
func (o *OSMSource) Health() SourceHealth {
	o.mu.Lock()
	defer o.mu.Unlock()
	return SourceHealth{
		Name:             o.Name(),
		Enabled:          o.Enabled(),
		Healthy:          o.healthy,
		LastError:        o.lastError,
		LastRunAt:        o.lastRunAt,
		SignalsCollected: o.signalsCollected,
		CacheTTLDays:     o.cfg.CacheTTLDays,
	}
}

// overpassResponse is the top-level Overpass JSON envelope.
type overpassResponse struct {
	Elements []overpassElement `json:"elements"`
}

// overpassElement represents a single OSM node/way/relation.
type overpassElement struct {
	Tags map[string]string `json:"tags"`
}

// Collect implements SignalSource.
func (o *OSMSource) Collect(ctx context.Context, est *model.Establishment) ([]*model.Signal, error) {
	if est.Lat == nil || est.Lng == nil {
		return nil, nil
	}

	now := time.Now()

	query := fmt.Sprintf(
		"[out:json][timeout:25];\n(\n  node(around:100,%f,%f)[amenity];\n  node(around:100,%f,%f)[tourism];\n);\nout body;",
		*est.Lat, *est.Lng, *est.Lat, *est.Lng,
	)

	// POST to Overpass with form-encoded body.
	formData := url.Values{}
	formData.Set("data", query)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.cfg.OverpassURL,
		strings.NewReader(formData.Encode()))
	if err != nil {
		o.recordRun(now, err.Error(), 0)
		return nil, nil //nolint:nilerr
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", o.cfg.UserAgent)

	resp, err := o.client.Do(req)
	if err != nil {
		log.Printf("[osm] overpass request failed for %s: %v", est.ID, err)
		o.recordRun(now, err.Error(), 0)
		return nil, nil
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if err != nil {
		log.Printf("[osm] reading overpass body for %s: %v", est.ID, err)
		o.recordRun(now, err.Error(), 0)
		return nil, nil
	}

	var ovResp overpassResponse
	if err := json.Unmarshal(body, &ovResp); err != nil {
		log.Printf("[osm] parsing overpass response for %s: %v", est.ID, err)
		o.recordRun(now, err.Error(), 0)
		return nil, nil
	}

	var signals []*model.Signal
	estNameLower := strings.ToLower(est.Name)

	for _, el := range ovResp.Elements {
		tags := el.Tags

		// Disused amenity / tourism.
		if _, ok := tags["disused:amenity"]; ok {
			signals = append(signals, o.tagSignal(est, "disused", 0.6, 0.55, 0.60, model.FamilyDirectStatus))
		} else if _, ok := tags["disused:tourism"]; ok {
			signals = append(signals, o.tagSignal(est, "disused", 0.6, 0.55, 0.60, model.FamilyDirectStatus))
		}

		// Explicitly closed.
		if strings.EqualFold(tags["closed"], "yes") {
			signals = append(signals, o.tagSignal(est, "closed", 0.6, 0.55, 0.60, model.FamilyDirectStatus))
		}

		// Vacant shop.
		if strings.EqualFold(tags["shop"], "vacant") {
			signals = append(signals, o.tagSignal(est, "vacant", 0.5, 0.55, 0.60, model.FamilyDirectStatus))
		}

		// Active element: has opening hours and name matches.
		if _, hasHours := tags["opening_hours"]; hasHours {
			elemName := strings.ToLower(tags["name"])
			if elemName != "" && strings.Contains(elemName, estNameLower) {
				signals = append(signals, o.tagSignal(est, "found_active", 1.0, 0.55, 0.30, model.FamilyActivity))
			}
		}
	}

	// No elements found at all — emit a weak negative signal.
	if len(ovResp.Elements) == 0 {
		sig := NewSignal(est.ID, o.Name(), model.KindOSMTag, model.FamilyActivity)
		sig.RawValue = "not_found"
		sig.Value = 0.0
		sig.Confidence = 0.40
		sig.Weight = 0.20
		sig.TTLDays = o.cfg.CacheTTLDays
		sig.DedupeKey = DedupeKey(est.ID, o.Name(), string(model.KindOSMTag), "not_found")
		signals = append(signals, sig)
	}

	o.recordRun(now, "", len(signals))
	return signals, nil
}

// tagSignal builds an OSM tag signal with the given parameters.
func (o *OSMSource) tagSignal(
	est *model.Establishment,
	rawValue string,
	value, confidence, weight float64,
	family model.SignalFamily,
) *model.Signal {
	sig := NewSignal(est.ID, o.Name(), model.KindOSMTag, family)
	sig.RawValue = rawValue
	sig.Value = value
	sig.Confidence = confidence
	sig.Weight = weight
	sig.TTLDays = o.cfg.CacheTTLDays
	sig.DedupeKey = DedupeKey(est.ID, o.Name(), string(model.KindOSMTag), rawValue)
	return sig
}

// recordRun updates the health state under the mutex.
func (o *OSMSource) recordRun(at time.Time, errMsg string, count int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.lastRunAt = &at
	o.lastError = errMsg
	o.healthy = errMsg == ""
	o.signalsCollected += count
}
