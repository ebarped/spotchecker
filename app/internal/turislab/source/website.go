// Package source - website collector.
//
// WebsiteSource performs a single HTTP GET against the establishment's website
// and emits signals about its availability and keyword presence.
package source

import (
	"context"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/datadope/turislab-doctor/internal/turislab/model"
)

// WebsiteConfig holds tunable parameters for the website collector.
type WebsiteConfig struct {
	TimeoutSeconds int
	UserAgent      string
	MaxPages       int
	CacheTTLDays   int
	CheckSSL       bool
	Keywords       []string
}

// WebsiteSource implements SignalSource for plain HTTP website checks.
type WebsiteSource struct {
	cfg    WebsiteConfig
	client *http.Client

	mu               sync.Mutex
	healthy          bool
	lastError        string
	lastRunAt        *time.Time
	signalsCollected int
}

// NewWebsite creates a ready-to-use WebsiteSource.
func NewWebsite(cfg WebsiteConfig) *WebsiteSource {
	if cfg.TimeoutSeconds <= 0 {
		cfg.TimeoutSeconds = 15
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = "TurisLab-Doctor/1.0"
	}
	if cfg.CacheTTLDays <= 0 {
		cfg.CacheTTLDays = 7
	}

	transport := &http.Transport{
		// Allow up to 3 redirects by leaving CheckRedirect at default (10),
		// but we override it below on the Client.
	}

	client := &http.Client{
		Timeout:   time.Duration(cfg.TimeoutSeconds) * time.Second,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}

	return &WebsiteSource{
		cfg:     cfg,
		client:  client,
		healthy: true,
	}
}

// Name implements SignalSource.
func (w *WebsiteSource) Name() string { return "website" }

// Enabled implements SignalSource — always enabled when instantiated.
func (w *WebsiteSource) Enabled() bool { return true }

// Capabilities implements SignalSource.
func (w *WebsiteSource) Capabilities() []model.SignalKind {
	return []model.SignalKind{model.KindWebStatus, model.KindWebKeyword}
}

// Health implements SignalSource.
func (w *WebsiteSource) Health() SourceHealth {
	w.mu.Lock()
	defer w.mu.Unlock()
	return SourceHealth{
		Name:             w.Name(),
		Enabled:          w.Enabled(),
		Healthy:          w.healthy,
		LastError:        w.lastError,
		LastRunAt:        w.lastRunAt,
		SignalsCollected: w.signalsCollected,
		CacheTTLDays:     w.cfg.CacheTTLDays,
	}
}

// Collect implements SignalSource.
func (w *WebsiteSource) Collect(ctx context.Context, est *model.Establishment) ([]*model.Signal, error) {
	if est.Website == "" {
		return nil, nil
	}

	now := time.Now()
	var signals []*model.Signal

	// ── HTTP request ─────────────────────────────────────────────────────────
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, est.Website, nil)
	if err != nil {
		w.recordRun(now, err.Error(), 0)
		return nil, nil //nolint:nilerr // bad URL is not a collector error
	}
	req.Header.Set("User-Agent", w.cfg.UserAgent)

	var (
		statusText string
		bodyBytes  []byte
		statusOK   bool
		reqErr     string
	)

	resp, err := w.client.Do(req)
	if err != nil {
		errStr := err.Error()
		switch {
		case strings.Contains(errStr, "connection refused"):
			statusText = "down"
		case strings.Contains(errStr, "no such host"):
			statusText = "down"
		case strings.Contains(errStr, "i/o timeout"), strings.Contains(errStr, "context deadline exceeded"):
			statusText = "down"
		default:
			statusText = "error"
		}
		reqErr = errStr
		log.Printf("[website] %s: request failed: %v", est.Website, err)
	} else {
		defer resp.Body.Close()
		switch {
		case resp.StatusCode == 404:
			statusText = "404"
		case resp.StatusCode >= 200 && resp.StatusCode <= 299:
			statusText = "ok"
			statusOK = true
			// Read body — capped at 512 KB.
			lr := io.LimitReader(resp.Body, 512*1024)
			bodyBytes, _ = io.ReadAll(lr)
		default:
			statusText = "error"
		}
	}

	// ── KindWebStatus signal ──────────────────────────────────────────────────
	sig := NewSignal(est.ID, w.Name(), model.KindWebStatus, model.FamilyActivity)
	sig.RawValue = statusText
	sig.Confidence = 0.75
	sig.Weight = 0.30
	sig.TTLDays = w.cfg.CacheTTLDays
	if statusOK {
		sig.Value = 1.0
	} else {
		sig.Value = 0.0
	}
	sig.DedupeKey = DedupeKey(est.ID, w.Name(), string(model.KindWebStatus), statusText)
	signals = append(signals, sig)

	// ── Keyword signals ───────────────────────────────────────────────────────
	if statusOK && len(bodyBytes) > 0 {
		bodyLower := strings.ToLower(string(bodyBytes))
		for _, kw := range w.cfg.Keywords {
			if strings.Contains(bodyLower, strings.ToLower(kw)) {
				ks := NewSignal(est.ID, w.Name(), model.KindWebKeyword, model.FamilyDirectStatus)
				ks.RawValue = kw
				ks.Value = 0.5
				ks.Confidence = 0.65
				ks.Weight = 0.50
				ks.TTLDays = w.cfg.CacheTTLDays
				ks.DedupeKey = DedupeKey(est.ID, w.Name(), string(model.KindWebKeyword), kw)
				signals = append(signals, ks)
			}
		}
	}

	w.recordRun(now, reqErr, len(signals))
	return signals, nil
}

// recordRun updates the health state under the mutex.
func (w *WebsiteSource) recordRun(at time.Time, errMsg string, count int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.lastRunAt = &at
	w.lastError = errMsg
	w.healthy = errMsg == ""
	w.signalsCollected += count
}
