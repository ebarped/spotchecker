package scan_test

// End-to-end test of the scan pipeline against the real SQLite store,
// validating the PoC success criterion: establishments with strong closure
// evidence must land in the top 3 of the priority queue.

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/datadope/turislab-doctor/internal/logx"
	"github.com/datadope/turislab-doctor/internal/turislab/model"
	"github.com/datadope/turislab-doctor/internal/turislab/scan"
	"github.com/datadope/turislab-doctor/internal/turislab/scoring"
	"github.com/datadope/turislab-doctor/internal/turislab/seed"
	"github.com/datadope/turislab-doctor/internal/turislab/store"
)

// fakeSource emits a configurable signal for every establishment it is asked
// to collect, or an error when failWith is set.
type fakeSource struct {
	name     string
	match    func(*model.Establishment) bool
	rawValue string
	kind     model.SignalKind
	family   model.SignalFamily
	value    float64
	failWith error
}

func (f *fakeSource) Name() string               { return f.name }
func (f *fakeSource) Enabled() bool              { return true }
func (f *fakeSource) Health() model.SourceStatus { return model.SourceStatus{Name: f.name} }

func (f *fakeSource) Collect(_ context.Context, est *model.Establishment) ([]*model.Signal, error) {
	if f.failWith != nil {
		return nil, f.failWith
	}
	if !f.match(est) {
		return nil, nil
	}
	sig := &model.Signal{
		ID:              f.name + "-" + est.ID,
		EstablishmentID: est.ID,
		SourceName:      f.name,
		Kind:            f.kind,
		Family:          f.family,
		RawValue:        f.rawValue,
		Value:           f.value,
		Confidence:      0.9,
		Weight:          0.9,
		CollectedAt:     time.Now(),
		DedupeKey:       f.name + "-" + est.ID + "-" + f.rawValue,
		TTLDays:         7,
	}
	return []*model.Signal{sig}, nil
}

func setup(t *testing.T) (store.Store, context.Context) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, context.Background()
}

func seedEstablishments(t *testing.T, s store.Store, ctx context.Context) {
	t.Helper()
	dir := t.TempDir()
	// Six establishments, two of them known-closed.
	input := `[
		{"id": "rest-001", "name": "Restaurante El Ancla", "category": "restaurant"},
		{"id": "rest-002", "name": "La Tasca Cerrada", "category": "restaurant"},
		{"id": "hotel-001", "name": "Hotel Palacio del Sol", "category": "hotel"},
		{"id": "hotel-002", "name": "Hostal El Pinar", "category": "hotel"},
		{"id": "bar-001", "name": "Bar La Esquina", "category": "bar"},
		{"id": "rest-003", "name": "Arrocería Marina", "category": "restaurant"}
	]`
	path := filepath.Join(dir, "base.json")
	if err := os.WriteFile(path, []byte(input), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	res, err := seed.Load(ctx, s, seed.Options{Path: path})
	if err != nil {
		t.Fatalf("seed.Load: %v", err)
	}
	if res.Loaded != 6 {
		t.Fatalf("seeded %d establishments, want 6", res.Loaded)
	}
}

func newScanner(s store.Store, sources ...scan.Source) *scan.Scanner {
	log := logx.New(io.Discard)
	return scan.New(scan.ScanConfig{
		TriggerInterval:      time.Minute,
		TargetsPerTick:       1,
		MaxConcurrentSources: 2,
		StaleAfterDays:       7,
	}, s, sources, scoring.NewEngine(scoring.DefaultScoringConfig()), log)
}

// buildQueue replicates what cmd/turislab does after a scan: read the scores
// and order them by priority (1 = first in the visit queue).
func buildQueue(ctx context.Context, s store.Store) []*model.Score {
	scores, _ := s.ListScores(ctx)
	sorted := append([]*model.Score(nil), scores...)
	for i := range sorted {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j].Priority > sorted[i].Priority {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}
	return sorted
}

func rankOf(queue []*model.Score, id string) int {
	for i, sc := range queue {
		if sc.EstablishmentID == id {
			return i + 1
		}
	}
	return -1
}

func TestPOCCriterionClosedEstablishmentsInTop3(t *testing.T) {
	s, ctx := setup(t)
	seedEstablishments(t, s, ctx)

	closed := &fakeSource{
		name:     "maps",
		match:    func(e *model.Establishment) bool { return e.ID == "rest-002" || e.ID == "hotel-002" },
		kind:     model.KindBusinessStatus,
		family:   model.FamilyDirectStatus,
		rawValue: "permanently_closed",
		value:    1.0,
	}
	open := &fakeSource{
		name:     "maps-open",
		match:    func(e *model.Establishment) bool { return e.ID != "rest-002" && e.ID != "hotel-002" },
		kind:     model.KindBusinessStatus,
		family:   model.FamilyDirectStatus,
		rawValue: "open",
		value:    0.0,
	}

	sc := newScanner(s, closed, open)
	if err := sc.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	queue := buildQueue(ctx, s)
	if len(queue) != 6 {
		t.Fatalf("queue length = %d, want 6 scored establishments", len(queue))
	}

	for _, id := range []string{"rest-002", "hotel-002"} {
		rank := rankOf(queue, id)
		if rank < 1 || rank > 3 {
			t.Errorf("%s ranked #%d, want within the top 3 (PoC success criterion)", id, rank)
		}
	}

	top := queue[0]
	if top.Tier != model.TierP0 && top.Tier != model.TierP1 {
		t.Errorf("top item tier = %q, want P0 or P1", top.Tier)
	}
	if top.Reason == "" {
		t.Error("the top item must carry a human-readable reason")
	}

	// Active establishments must rank below the closed ones.
	if rankOf(queue, "rest-001") < 3 {
		t.Errorf("an active establishment ranked #%d, want below the closed pair", rankOf(queue, "rest-001"))
	}
}

func TestCursorPersistsAcrossRuns(t *testing.T) {
	s, ctx := setup(t)
	seedEstablishments(t, s, ctx)

	src := &fakeSource{
		name:     "always",
		match:    func(*model.Establishment) bool { return true },
		kind:     model.KindWebStatus,
		family:   model.FamilyActivity,
		rawValue: "ok",
		value:    0.0,
	}
	sc := newScanner(s, src)

	if err := sc.ScanOne(ctx, "rest-001"); err != nil {
		t.Fatalf("ScanOne: %v", err)
	}
	// ScanOne does not move the cursor; only ticks do.
	if cursor, _ := s.GetCursor(ctx); cursor != "" {
		t.Errorf("cursor = %q after ScanOne, want empty", cursor)
	}

	// A second ScanOne on the same establishment must not duplicate signals:
	// the dedupe key makes the second insert a no-op.
	if err := sc.ScanOne(ctx, "rest-001"); err != nil {
		t.Fatalf("second ScanOne: %v", err)
	}
	signals, _ := s.ListSignals(ctx, "rest-001", time.Now().Add(-time.Hour))
	if len(signals) != 1 {
		t.Errorf("signals = %d, want 1 (dedupe must collapse repeats)", len(signals))
	}
}

func TestSourceErrorDoesNotAbortTheScan(t *testing.T) {
	s, ctx := setup(t)
	seedEstablishments(t, s, ctx)

	broken := &fakeSource{name: "broken", failWith: errors.New("429 rate limit"), match: func(*model.Establishment) bool { return true }}
	healthy := &fakeSource{
		name:     "healthy",
		match:    func(e *model.Establishment) bool { return e.ID == "rest-002" },
		kind:     model.KindBusinessStatus,
		family:   model.FamilyDirectStatus,
		rawValue: "permanently_closed",
		value:    1.0,
	}

	sc := newScanner(s, broken, healthy)
	if err := sc.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce aborted on a failing source: %v", err)
	}

	queue := buildQueue(ctx, s)
	if len(queue) == 0 {
		t.Fatal("no scores were computed")
	}
	if rank := rankOf(queue, "rest-002"); rank < 1 || rank > 3 {
		t.Errorf("rest-002 ranked #%d, want top 3 despite the failing source", rank)
	}
}

func TestStatusReflectsActivity(t *testing.T) {
	s, _ := setup(t)
	sc := newScanner(s)
	st := sc.Status()
	if st.Running {
		t.Error("running = true before Start, want false")
	}
}

// A one-pass run counts as activity: the running flag is up while the pass
// is in flight and back down once it completes, so /api/scan/status and the
// UI's poll loop can follow it.
func TestRunOnceTogglesTheRunningFlag(t *testing.T) {
	s, ctx := setup(t)
	seedEstablishments(t, s, ctx)

	release := make(chan struct{})
	blocking := &gateSource{gate: release}
	sc := newScanner(s, blocking)

	done := make(chan error, 1)
	go func() { done <- sc.RunOnce(ctx) }()

	// Wait until the pass is running, then confirm the flag is visible.
	deadline := time.Now().Add(5 * time.Second)
	for !sc.Status().Running && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !sc.Status().Running {
		t.Fatal("the pass never reported running")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if sc.Status().Running {
		t.Error("running = true after RunOnce, want false")
	}
}

// gateSource blocks every Collect until its gate closes, giving the test a
// window to observe the running flag mid-pass.
type gateSource struct {
	gate chan struct{}
}

func (g *gateSource) Name() string               { return "gate" }
func (g *gateSource) Enabled() bool              { return true }
func (g *gateSource) Health() model.SourceStatus { return model.SourceStatus{Name: g.Name()} }

func (g *gateSource) Collect(ctx context.Context, _ *model.Establishment) ([]*model.Signal, error) {
	select {
	case <-g.gate:
		return nil, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
