package source

import (
	"context"
	"crypto/sha256"
	"fmt"
	"testing"
	"time"

	"github.com/datadope/turislab-doctor/internal/turislab/model"
)

// uuidPattern is declared in uuid_test.go; reuse it here.

func TestNewSignalDefaults(t *testing.T) {
	sig := NewSignal("est-1", "website", model.KindWebStatus, model.FamilyActivity)

	if sig.ID == "" {
		t.Error("ID must not be empty")
	}
	if !uuidRe.MatchString(sig.ID) {
		t.Errorf("ID = %q, not a valid UUID v4", sig.ID)
	}
	if sig.EstablishmentID != "est-1" {
		t.Errorf("EstablishmentID = %q, want est-1", sig.EstablishmentID)
	}
	if sig.SourceName != "website" {
		t.Errorf("SourceName = %q, want website", sig.SourceName)
	}
	if sig.Kind != model.KindWebStatus {
		t.Errorf("Kind = %v, want %v", sig.Kind, model.KindWebStatus)
	}
	if sig.Family != model.FamilyActivity {
		t.Errorf("Family = %v, want %v", sig.Family, model.FamilyActivity)
	}
	if sig.Confidence != 0.5 {
		t.Errorf("Confidence = %v, want 0.5", sig.Confidence)
	}
	if sig.Weight != 0.5 {
		t.Errorf("Weight = %v, want 0.5", sig.Weight)
	}
	if sig.TTLDays != 7 {
		t.Errorf("TTLDays = %d, want 7", sig.TTLDays)
	}
	if sig.CollectedAt.IsZero() {
		t.Error("CollectedAt must be set")
	}
}

func TestNewSignalUniqueIDs(t *testing.T) {
	seen := make(map[string]bool, 100)
	for i := 0; i < 100; i++ {
		sig := NewSignal("est-1", "website", model.KindWebStatus, model.FamilyActivity)
		if seen[sig.ID] {
			t.Fatalf("duplicate signal ID %q at iteration %d", sig.ID, i)
		}
		seen[sig.ID] = true
	}
}

func TestDedupeKey(t *testing.T) {
	key := DedupeKey("est-1", "website", "web_status", "ok")

	want := fmt.Sprintf("%x", sha256.Sum256([]byte("est-1|website|web_status|ok")))
	if key != want {
		t.Errorf("DedupeKey = %q, want %q", key, want)
	}

	if again := DedupeKey("est-1", "website", "web_status", "ok"); again != key {
		t.Errorf("DedupeKey is not deterministic: %q != %q", again, key)
	}

	if other := DedupeKey("est-1", "website", "web_status", "404"); other == key {
		t.Error("different rawValue must produce a different key")
	}
	if other := DedupeKey("est-2", "website", "web_status", "ok"); other == key {
		t.Error("different establishment must produce a different key")
	}
}

// fakeSource is a minimal SignalSource for Registry tests.
type fakeSource struct {
	name    string
	enabled bool
	health  SourceHealth
}

func (f *fakeSource) Name() string                     { return f.name }
func (f *fakeSource) Enabled() bool                    { return f.enabled }
func (f *fakeSource) Capabilities() []model.SignalKind { return nil }
func (f *fakeSource) Collect(_ context.Context, _ *model.Establishment) ([]*model.Signal, error) {
	return nil, nil
}
func (f *fakeSource) Health() SourceHealth { return f.health }

func TestRegistryAllAndEnabled(t *testing.T) {
	active := &fakeSource{name: "website", enabled: true}
	disabled := &fakeSource{name: "osm", enabled: false}
	reg := NewRegistry(active, disabled)

	if all := reg.All(); len(all) != 2 {
		t.Errorf("All() = %d sources, want 2", len(all))
	}

	enabled := reg.Enabled()
	if len(enabled) != 1 {
		t.Fatalf("Enabled() = %d sources, want 1", len(enabled))
	}
	if enabled[0].Name() != "website" {
		t.Errorf("Enabled()[0] = %q, want website", enabled[0].Name())
	}
}

func TestRegistryAllReturnsCopy(t *testing.T) {
	reg := NewRegistry(&fakeSource{name: "website", enabled: true})

	all := reg.All()
	all[0] = nil
	if reg.All()[0] == nil {
		t.Error("All() must return a copy; mutating the result changed the registry")
	}
}

func TestRegistryStatuses(t *testing.T) {
	now := time.Now()
	reg := NewRegistry(
		&fakeSource{name: "website", enabled: true, health: SourceHealth{
			Name: "website", Enabled: true, Healthy: true,
			LastRunAt: &now, SignalsCollected: 3, CacheTTLDays: 7,
		}},
	)

	statuses := reg.Statuses()
	if len(statuses) != 1 {
		t.Fatalf("Statuses() = %d entries, want 1", len(statuses))
	}
	st := statuses[0]
	if st.Name != "website" || !st.Enabled || !st.Healthy {
		t.Errorf("unexpected status: %+v", st)
	}
	if st.SignalsCollected != 3 || st.CacheTTLDays != 7 {
		t.Errorf("unexpected status metrics: %+v", st)
	}
	if st.LastRunAt == nil || !st.LastRunAt.Equal(now) {
		t.Errorf("LastRunAt = %v, want %v", st.LastRunAt, now)
	}
}

func TestSourceHealthToStatus(t *testing.T) {
	at := time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)
	h := SourceHealth{
		Name:             "gmaps",
		Enabled:          true,
		Healthy:          false,
		LastError:        "boom",
		LastRunAt:        &at,
		SignalsCollected: 5,
		CacheTTLDays:     14,
	}

	st := h.toStatus()
	if st.Name != h.Name || st.Enabled != h.Enabled || st.Healthy != h.Healthy {
		t.Errorf("basic fields mismatch: %+v", st)
	}
	if st.LastError != h.LastError {
		t.Errorf("LastError = %q, want %q", st.LastError, h.LastError)
	}
	if st.SignalsCollected != h.SignalsCollected || st.CacheTTLDays != h.CacheTTLDays {
		t.Errorf("metrics mismatch: %+v", st)
	}
	if st.LastRunAt == nil || !st.LastRunAt.Equal(at) {
		t.Errorf("LastRunAt = %v, want %v", st.LastRunAt, at)
	}
}
