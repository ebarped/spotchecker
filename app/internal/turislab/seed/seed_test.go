package seed_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/datadope/turislab-doctor/internal/turislab/model"
	"github.com/datadope/turislab-doctor/internal/turislab/seed"
	"github.com/datadope/turislab-doctor/internal/turislab/store"
)

func openStore(t *testing.T) store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

const twoEstablishments = `[
  {"id": "rest-001", "name": "Restaurante El Ancla", "category": "restaurant"},
  {"id": "hotel-001", "name": "Hotel Palacio del Sol", "category": "hotel", "stars": 4}
]`

func TestLoadJSON(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	path := writeFile(t, "base.json", twoEstablishments)

	res, err := seed.Load(ctx, s, seed.Options{Path: path})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if res.Loaded != 2 || res.Skipped != 0 {
		t.Errorf("loaded = %d, skipped = %d, want 2 and 0", res.Loaded, res.Skipped)
	}

	est, err := s.GetEstablishment(ctx, "rest-001")
	if err != nil || est == nil {
		t.Fatalf("GetEstablishment(rest-001): %v, %v", est, err)
	}
	if est.Category != model.CategoryRestaurant {
		t.Errorf("category = %q, want restaurant", est.Category)
	}

	hotel, _ := s.GetEstablishment(ctx, "hotel-001")
	if hotel.Stars == nil || *hotel.Stars != 4 {
		t.Errorf("stars = %v, want 4", hotel.Stars)
	}
}

func TestLoadUpsertsExistingRecords(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	path := writeFile(t, "base.json", twoEstablishments)

	if _, err := seed.Load(ctx, s, seed.Options{Path: path}); err != nil {
		t.Fatalf("first Load: %v", err)
	}

	// Change the name and re-load: the store must hold the new value, not a duplicate.
	updated := `[{"id": "rest-001", "name": "El Ancla Renovado", "category": "restaurant"}]`
	path2 := writeFile(t, "base2.json", updated)
	if _, err := seed.Load(ctx, s, seed.Options{Path: path2}); err != nil {
		t.Fatalf("second Load: %v", err)
	}

	ests, _ := s.ListEstablishments(ctx)
	var count int
	for _, e := range ests {
		if e.ID == "rest-001" {
			count++
			if e.Name != "El Ancla Renovado" {
				t.Errorf("name = %q, want the updated value", e.Name)
			}
		}
	}
	if count != 1 {
		t.Errorf("rest-001 appears %d times, want exactly 1", count)
	}
}

func TestGeneratedIDIsDeterministic(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	body := `[{"name": "Bar Sin Identificador", "address": "Calle Falsa 123", "municipality": "Alicante"}]`
	path := writeFile(t, "base.json", body)

	if _, err := seed.Load(ctx, s, seed.Options{Path: path}); err != nil {
		t.Fatalf("Load: %v", err)
	}

	ests, _ := s.ListEstablishments(ctx)
	if len(ests) != 1 {
		t.Fatalf("establishments = %d, want 1", len(ests))
	}
	if !ests[0].IDGenerated {
		t.Errorf("id_generated = false, want true")
	}
	if len(ests[0].ID) == 0 {
		t.Error("id must not be empty")
	}

	// Re-loading the same record must reuse the same generated ID.
	s2 := openStore(t)
	if _, err := seed.Load(ctx, s2, seed.Options{Path: path}); err != nil {
		t.Fatalf("reload: %v", err)
	}
	ests2, _ := s2.ListEstablishments(ctx)
	if len(ests2) != 1 || ests2[0].ID != ests[0].ID {
		t.Errorf("generated ID changed across runs: %q vs %q", ests[0].ID, ests2[0].ID)
	}
}

func TestStrictValidationRejectsInvalidRecords(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	body := `[{"id": "ok-1", "name": "Válido"}, {"id": "bad-1", "name": ""}]`
	path := writeFile(t, "base.json", body)

	if _, err := seed.Load(ctx, s, seed.Options{Path: path, StrictValidation: true}); err == nil {
		t.Fatal("Load succeeded, want an error in strict mode with an empty name")
	}

	res, err := seed.Load(ctx, s, seed.Options{Path: path, StrictValidation: false})
	if err != nil {
		t.Fatalf("lenient Load: %v", err)
	}
	if res.Loaded != 1 || res.Skipped != 1 {
		t.Errorf("loaded = %d, skipped = %d, want 1 and 1", res.Loaded, res.Skipped)
	}
}

func TestDefaultCountryApplied(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	body := `[{"id": "rest-9", "name": "Sin País"}]`
	path := writeFile(t, "base.json", body)

	if _, err := seed.Load(ctx, s, seed.Options{Path: path, DefaultCountry: "ES"}); err != nil {
		t.Fatalf("Load: %v", err)
	}
	est, _ := s.GetEstablishment(ctx, "rest-9")
	if est.Country != "ES" {
		t.Errorf("country = %q, want ES", est.Country)
	}
}
