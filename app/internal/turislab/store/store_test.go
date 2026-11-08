package store_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/datadope/turislab-doctor/internal/turislab/model"
	"github.com/datadope/turislab-doctor/internal/turislab/store"
)

// openTestStore opens a fresh SQLiteStore backed by a temporary file and
// registers automatic cleanup with t.Cleanup.
func openTestStore(t *testing.T) store.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// ─── Establishment tests ───────────────────────────────────────────────────────

func TestSaveAndGetEstablishment(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	stars := 3
	lat := 40.4168
	lng := -3.7038
	inspAt := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)

	est := &model.Establishment{
		ID:                   "est-001",
		Name:                 "Restaurante La Prueba",
		Category:             model.CategoryRestaurant,
		Stars:                &stars,
		Address:              "Calle Mayor 1",
		Municipality:         "Madrid",
		Province:             "Madrid",
		PostalCode:           "28001",
		Country:              "ES",
		Lat:                  &lat,
		Lng:                  &lng,
		Phone:                "+34912345678",
		Email:                "info@laprueba.es",
		Website:              "https://laprueba.es",
		RegistryID:           "MAD-R-001",
		Certifications:       []string{"Q de Calidad", "TripAdvisor CoE"},
		LastInspectionAt:     &inspAt,
		LastInspectionResult: "Satisfactorio",
		GooglePlaceID:        "ChIJgplace123",
		TripAdvisorID:        "ta-456",
		YelpID:               "yelp-789",
		OSMID:                "node/111222333",
		Notes:                "Revisada en el PoC",
		IDGenerated:          false,
	}

	if err := s.SaveEstablishment(ctx, est); err != nil {
		t.Fatalf("SaveEstablishment: %v", err)
	}

	got, err := s.GetEstablishment(ctx, est.ID)
	if err != nil {
		t.Fatalf("GetEstablishment: %v", err)
	}

	// Core identity fields.
	if got.ID != est.ID {
		t.Errorf("ID: got %q, want %q", got.ID, est.ID)
	}
	if got.Name != est.Name {
		t.Errorf("Name: got %q, want %q", got.Name, est.Name)
	}
	if got.Category != est.Category {
		t.Errorf("Category: got %q, want %q", got.Category, est.Category)
	}

	// Nullable integer pointer.
	if got.Stars == nil {
		t.Fatal("Stars: got nil, want &3")
	}
	if *got.Stars != stars {
		t.Errorf("Stars: got %d, want %d", *got.Stars, stars)
	}

	// Nullable float64 pointers.
	if got.Lat == nil || *got.Lat != lat {
		t.Errorf("Lat: got %v, want %v", got.Lat, lat)
	}
	if got.Lng == nil || *got.Lng != lng {
		t.Errorf("Lng: got %v, want %v", got.Lng, lng)
	}

	// JSON-serialised certifications slice.
	if len(got.Certifications) != 2 {
		t.Errorf("Certifications len: got %d, want 2", len(got.Certifications))
	}

	// Time pointer (round-tripped via RFC3339, so truncate sub-second).
	if got.LastInspectionAt == nil {
		t.Fatal("LastInspectionAt: got nil")
	}
	if !got.LastInspectionAt.Equal(inspAt) {
		t.Errorf("LastInspectionAt: got %v, want %v", got.LastInspectionAt, inspAt)
	}

	// Upsert: update the name and re-save.
	est.Name = "Updated Name"
	if err := s.SaveEstablishment(ctx, est); err != nil {
		t.Fatalf("SaveEstablishment (update): %v", err)
	}
	got2, err := s.GetEstablishment(ctx, est.ID)
	if err != nil {
		t.Fatalf("GetEstablishment (after update): %v", err)
	}
	if got2.Name != "Updated Name" {
		t.Errorf("Name after update: got %q, want \"Updated Name\"", got2.Name)
	}

	// ListEstablishments should return exactly one record.
	all, err := s.ListEstablishments(ctx)
	if err != nil {
		t.Fatalf("ListEstablishments: %v", err)
	}
	if len(all) != 1 {
		t.Errorf("ListEstablishments count: got %d, want 1", len(all))
	}
}

// TestSaveAndGetEstablishmentNotFound verifies ErrNotFound is returned for
// missing IDs.
func TestSaveAndGetEstablishmentNotFound(t *testing.T) {
	s := openTestStore(t)
	_, err := s.GetEstablishment(context.Background(), "does-not-exist")
	if err != store.ErrNotFound {
		t.Errorf("expected ErrNotFound, got: %v", err)
	}
}

// ─── Signal / deduplication tests ─────────────────────────────────────────────

func TestSaveSignalDedupe(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	// Insert the parent establishment first (FK-less but good practice).
	est := &model.Establishment{ID: "est-dedupe", Name: "Dedupe Test"}
	if err := s.SaveEstablishment(ctx, est); err != nil {
		t.Fatalf("SaveEstablishment: %v", err)
	}

	base := &model.Signal{
		ID:              "sig-001",
		EstablishmentID: "est-dedupe",
		SourceName:      "osm",
		Kind:            model.KindOSMTag,
		Family:          model.FamilyDirectStatus,
		CollectedAt:     time.Now().UTC(),
		Value:           1,
		RawValue:        "disused",
		Confidence:      0.9,
		Weight:          0.8,
		DedupeKey:       "osm:est-dedupe:osm_tag:disused",
		TTLDays:         30,
	}

	// First insert must succeed.
	if err := s.SaveSignal(ctx, base); err != nil {
		t.Fatalf("SaveSignal (first): %v", err)
	}

	// Second insert with the same DedupeKey but a different ID must be silently
	// ignored (OR IGNORE semantics).
	dup := *base
	dup.ID = "sig-002" // different PK
	dup.Value = 0      // different payload — must NOT overwrite the original
	if err := s.SaveSignal(ctx, &dup); err != nil {
		t.Fatalf("SaveSignal (duplicate): %v", err)
	}

	// Only one signal should exist for the establishment.
	since := time.Time{} // zero = from the beginning of time
	signals, err := s.ListSignals(ctx, "est-dedupe", since)
	if err != nil {
		t.Fatalf("ListSignals: %v", err)
	}
	if len(signals) != 1 {
		t.Errorf("signal count after dedupe: got %d, want 1", len(signals))
	}
	// The stored value should be from the first insert, not the duplicate.
	if signals[0].Value != 1 {
		t.Errorf("signal Value: got %v, want 1 (first insert)", signals[0].Value)
	}
	if signals[0].ID != "sig-001" {
		t.Errorf("signal ID: got %q, want \"sig-001\"", signals[0].ID)
	}
}

// TestListSignalsSinceFilter verifies that the since timestamp filter works.
func TestListSignalsSinceFilter(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	est := &model.Establishment{ID: "est-since", Name: "Since Filter Test"}
	if err := s.SaveEstablishment(ctx, est); err != nil {
		t.Fatalf("SaveEstablishment: %v", err)
	}

	// Two signals collected at different times.
	old := &model.Signal{
		ID:              "sig-old",
		EstablishmentID: "est-since",
		SourceName:      "website",
		Kind:            model.KindWebStatus,
		Family:          model.FamilyContext,
		CollectedAt:     time.Now().Add(-48 * time.Hour).UTC(),
		DedupeKey:       "website:est-since:old",
		TTLDays:         7,
	}
	recent := &model.Signal{
		ID:              "sig-recent",
		EstablishmentID: "est-since",
		SourceName:      "website",
		Kind:            model.KindWebStatus,
		Family:          model.FamilyContext,
		CollectedAt:     time.Now().UTC(),
		DedupeKey:       "website:est-since:recent",
		TTLDays:         7,
	}

	for _, sig := range []*model.Signal{old, recent} {
		if err := s.SaveSignal(ctx, sig); err != nil {
			t.Fatalf("SaveSignal %s: %v", sig.ID, err)
		}
	}

	// Request only signals created in the last 24 hours.
	since := time.Now().Add(-24 * time.Hour)
	got, err := s.ListSignals(ctx, "est-since", since)
	if err != nil {
		t.Fatalf("ListSignals: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("ListSignals with since filter: got %d signals, want 1", len(got))
	}
	if len(got) > 0 && got[0].ID != "sig-recent" {
		t.Errorf("expected sig-recent, got %q", got[0].ID)
	}
}

// ─── Cursor tests ──────────────────────────────────────────────────────────────

func TestCursor(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	// Before any write the cursor returns empty string.
	cur, err := s.GetCursor(ctx)
	if err != nil {
		t.Fatalf("GetCursor (initial): %v", err)
	}
	if cur != "" {
		t.Errorf("initial cursor: got %q, want \"\"", cur)
	}

	// Set cursor for the first time.
	if err := s.SetCursor(ctx, "est-001"); err != nil {
		t.Fatalf("SetCursor: %v", err)
	}
	cur, err = s.GetCursor(ctx)
	if err != nil {
		t.Fatalf("GetCursor (after first set): %v", err)
	}
	if cur != "est-001" {
		t.Errorf("cursor after first set: got %q, want \"est-001\"", cur)
	}

	// Update the cursor.
	if err := s.SetCursor(ctx, "est-042"); err != nil {
		t.Fatalf("SetCursor (update): %v", err)
	}
	cur, err = s.GetCursor(ctx)
	if err != nil {
		t.Fatalf("GetCursor (after update): %v", err)
	}
	if cur != "est-042" {
		t.Errorf("cursor after update: got %q, want \"est-042\"", cur)
	}
}

// ─── Score tests ───────────────────────────────────────────────────────────────

func TestSaveAndGetScore(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	sc := &model.Score{
		EstablishmentID:  "est-score",
		ComputedAt:       time.Now().UTC().Truncate(time.Second),
		ClosureRisk:      0.72,
		QualityRisk:      0.35,
		QualityDelta:     -12.5,
		Priority:         81.3,
		Tier:             model.TierP0,
		Confidence:       0.68,
		EvidenceCount:    5,
		Status:           model.StatusClosureSuspected,
		Reason:           "Google Maps e OSM reportan cierre prolongado",
		Volatile:         true,
		ConflictDetected: false,
	}

	if err := s.SaveScore(ctx, sc); err != nil {
		t.Fatalf("SaveScore: %v", err)
	}

	got, err := s.GetScore(ctx, sc.EstablishmentID)
	if err != nil {
		t.Fatalf("GetScore: %v", err)
	}

	if got.EstablishmentID != sc.EstablishmentID {
		t.Errorf("EstablishmentID: got %q, want %q", got.EstablishmentID, sc.EstablishmentID)
	}
	if got.Tier != sc.Tier {
		t.Errorf("Tier: got %q, want %q", got.Tier, sc.Tier)
	}
	if got.Status != sc.Status {
		t.Errorf("Status: got %q, want %q", got.Status, sc.Status)
	}
	if got.ClosureRisk != sc.ClosureRisk {
		t.Errorf("ClosureRisk: got %v, want %v", got.ClosureRisk, sc.ClosureRisk)
	}
	if got.Priority != sc.Priority {
		t.Errorf("Priority: got %v, want %v", got.Priority, sc.Priority)
	}
	if got.Volatile != sc.Volatile {
		t.Errorf("Volatile: got %v, want %v", got.Volatile, sc.Volatile)
	}
	if got.ConflictDetected != sc.ConflictDetected {
		t.Errorf("ConflictDetected: got %v, want %v", got.ConflictDetected, sc.ConflictDetected)
	}
	if got.EvidenceCount != sc.EvidenceCount {
		t.Errorf("EvidenceCount: got %d, want %d", got.EvidenceCount, sc.EvidenceCount)
	}

	// Upsert: update priority and re-save.
	sc.Priority = 55.0
	sc.Tier = model.TierP1
	if err := s.SaveScore(ctx, sc); err != nil {
		t.Fatalf("SaveScore (update): %v", err)
	}
	got2, err := s.GetScore(ctx, sc.EstablishmentID)
	if err != nil {
		t.Fatalf("GetScore (after update): %v", err)
	}
	if got2.Priority != 55.0 {
		t.Errorf("Priority after update: got %v, want 55.0", got2.Priority)
	}
	if got2.Tier != model.TierP1 {
		t.Errorf("Tier after update: got %q, want P1", got2.Tier)
	}

	// GetScore on a missing ID returns ErrNotFound.
	_, err = s.GetScore(ctx, "does-not-exist")
	if err != store.ErrNotFound {
		t.Errorf("expected ErrNotFound for missing score, got: %v", err)
	}

	// ListScores should return exactly one row.
	all, err := s.ListScores(ctx)
	if err != nil {
		t.Fatalf("ListScores: %v", err)
	}
	if len(all) != 1 {
		t.Errorf("ListScores count: got %d, want 1", len(all))
	}
}

// TestNewScoreDefaultStatus verifies helper behaviour in the model package.
func TestNewScoreDefaultStatus(t *testing.T) {
	sc := model.NewScore("est-new")
	if sc.Status != model.StatusUnknown {
		t.Errorf("NewScore status: got %q, want %q", sc.Status, model.StatusUnknown)
	}
	if sc.EstablishmentID != "est-new" {
		t.Errorf("NewScore id: got %q, want \"est-new\"", sc.EstablishmentID)
	}
}

// TestAssignTier exercises the tier assignment logic.
func TestAssignTier(t *testing.T) {
	cfg := model.DefaultTierConfig()

	cases := []struct {
		name       string
		priority   float64
		confidence float64
		wantTier   model.TierLabel
	}{
		{"collect_data low conf", 50, 0.20, model.TierCollectData},
		{"p0 high conf", 46, 0.60, model.TierP0},
		{"p0 priority but low conf → p1", 46, 0.40, model.TierP1},
		{"p1", 40, 0.60, model.TierP1},
		{"p2", 25, 0.60, model.TierP2},
		{"p3", 10, 0.60, model.TierP3},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sc := model.NewScore("est-tier")
			sc.Priority = tc.priority
			sc.Confidence = tc.confidence
			sc.AssignTier(cfg)
			if sc.Tier != tc.wantTier {
				t.Errorf("priority=%.0f conf=%.2f: got tier %q, want %q",
					tc.priority, tc.confidence, sc.Tier, tc.wantTier)
			}
		})
	}
}
