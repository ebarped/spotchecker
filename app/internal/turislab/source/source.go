// Package source defines the SignalSource interface every evidence collector
// implements, together with a Registry that manages a set of sources and a few
// shared helpers used by every collector.
package source

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/datadope/turislab-doctor/internal/turislab/model"
)

// ─── SignalSource ─────────────────────────────────────────────────────────────

// SignalSource is the contract every evidence collector must satisfy.
type SignalSource interface {
	// Name returns the stable machine-readable identifier for this source.
	Name() string

	// Enabled reports whether this source should be used during a scan.
	Enabled() bool

	// Capabilities lists the SignalKinds this source can produce.
	Capabilities() []model.SignalKind

	// Collect gathers signals for the given establishment.
	// Implementations must never return a non-nil error for expected transient
	// failures (network timeouts, 404s, …); those are captured in SourceHealth.
	Collect(ctx context.Context, est *model.Establishment) ([]*model.Signal, error)

	// Health returns a current snapshot of the source's operational state.
	Health() SourceHealth
}

// ─── SourceHealth ─────────────────────────────────────────────────────────────

// SourceHealth carries runtime diagnostics for a single source.
type SourceHealth struct {
	Name             string
	Enabled          bool
	Healthy          bool
	LastError        string
	LastRunAt        *time.Time
	SignalsCollected int
	CacheTTLDays     int
}

// toStatus converts a SourceHealth to the model wire type.
func (h SourceHealth) toStatus() model.SourceStatus {
	return model.SourceStatus{
		Name:             h.Name,
		Enabled:          h.Enabled,
		Healthy:          h.Healthy,
		LastError:        h.LastError,
		LastRunAt:        h.LastRunAt,
		SignalsCollected: h.SignalsCollected,
		CacheTTLDays:     h.CacheTTLDays,
	}
}

// ─── Registry ─────────────────────────────────────────────────────────────────

// Registry holds all configured sources and provides filtered views.
type Registry struct {
	sources []SignalSource
}

// NewRegistry creates a Registry pre-loaded with the supplied sources.
func NewRegistry(sources ...SignalSource) *Registry {
	return &Registry{sources: sources}
}

// All returns every source, regardless of whether it is enabled.
func (r *Registry) All() []SignalSource {
	out := make([]SignalSource, len(r.sources))
	copy(out, r.sources)
	return out
}

// Enabled returns only the sources that report Enabled() == true.
func (r *Registry) Enabled() []SignalSource {
	var out []SignalSource
	for _, s := range r.sources {
		if s.Enabled() {
			out = append(out, s)
		}
	}
	return out
}

// Statuses returns a model.SourceStatus snapshot for every registered source.
func (r *Registry) Statuses() []model.SourceStatus {
	out := make([]model.SourceStatus, 0, len(r.sources))
	for _, s := range r.sources {
		out = append(out, s.Health().toStatus())
	}
	return out
}

// ─── Shared helpers ───────────────────────────────────────────────────────────

// NewSignal allocates a Signal with sensible defaults and fills in the fields
// that are common to all collectors.
func NewSignal(estID, sourceName string, kind model.SignalKind, family model.SignalFamily) *model.Signal {
	return &model.Signal{
		ID:              uuidV4(),
		EstablishmentID: estID,
		SourceName:      sourceName,
		Kind:            kind,
		Family:          family,
		CollectedAt:     time.Now(),
		Confidence:      0.5,
		Weight:          0.5,
		TTLDays:         7,
	}
}

// DedupeKey returns a stable hex-encoded SHA-256 fingerprint for the
// (estID, source, kind, rawValue) tuple so that duplicate signals can be
// detected without storing them twice.
func DedupeKey(estID, source, kind, rawValue string) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%s|%s", estID, source, kind, rawValue)))
	return fmt.Sprintf("%x", h)
}
