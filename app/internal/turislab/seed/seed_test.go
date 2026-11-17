package seed_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// writeDataset drops a file inside a temporary datasets directory.
func writeDataset(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func TestLoadDatasetsReadsEveryCSVInNameOrder(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	dir := t.TempDir()

	writeDataset(t, dir, "b.csv", "name,category,municipality\nBar Sin ID,bar,Alicante\n")
	writeDataset(t, dir, "a.csv", `id,name,category,stars,address,lat,lng,certifications,last_inspection_at,notes
rest-001,Restaurante El Ancla,restaurant,4,"Calle Falsa, 1",38.34,-0.49,"Calidad|Turismo",2024-05-14T00:00:00Z,"Nota con, coma"
`)
	writeDataset(t, dir, "ignore.txt", "id,name\nignored,Ignorado\n")
	if err := os.Mkdir(filepath.Join(dir, "subdir.csv"), 0o755); err != nil {
		t.Fatal(err)
	}

	res, err := seed.Load(ctx, s, seed.Options{DatasetsDir: dir, DefaultCountry: "ES"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if res.Total != 2 || res.Loaded != 2 || res.Skipped != 0 {
		t.Errorf("total/loaded/skipped = %d/%d/%d, want 2/2/0", res.Total, res.Loaded, res.Skipped)
	}
	if len(res.Files) != 2 {
		t.Fatalf("files = %d, want 2", len(res.Files))
	}
	if filepath.Base(res.Files[0].Path) != "a.csv" || filepath.Base(res.Files[1].Path) != "b.csv" {
		t.Errorf("file order = %q, %q; want a.csv, b.csv", res.Files[0].Path, res.Files[1].Path)
	}

	est, err := s.GetEstablishment(ctx, "rest-001")
	if err != nil {
		t.Fatalf("GetEstablishment: %v", err)
	}
	if est.Stars == nil || *est.Stars != 4 || est.Lat == nil || *est.Lat != 38.34 {
		t.Errorf("parsed numeric fields: %+v", est)
	}
	if est.LastInspectionAt == nil || !est.LastInspectionAt.Equal(time.Date(2024, 5, 14, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("last inspection = %v", est.LastInspectionAt)
	}
	if len(est.Certifications) != 2 || est.Certifications[1] != "Turismo" {
		t.Errorf("certifications = %v", est.Certifications)
	}
	if est.Notes != "Nota con, coma" || est.Country != "ES" {
		t.Errorf("notes/country = %q/%q", est.Notes, est.Country)
	}

	ests, _ := s.ListEstablishments(ctx)
	for _, e := range ests {
		if e.Name == "Bar Sin ID" {
			if !e.IDGenerated || !strings.HasPrefix(e.ID, "gen-") {
				t.Errorf("generated id = %q, generated = %v", e.ID, e.IDGenerated)
			}
			return
		}
	}
	t.Error("record from b.csv was not stored")
}

func TestLoadDatasetsSpanishHeadersSemicolonsAndBOM(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	dir := t.TempDir()

	writeDataset(t, dir, "export.csv", "\ufeffnombre;dirección;categoría;latitud;longitud;teléfono;certificaciones;fecha_inspección;notas\n"+
		"Bar El Puerto;Rúa Nova, 2;bar;43,0112;-7,5572;+34982000000;\"Q, Turismo\";14/05/2024;Abierto\n")

	res, err := seed.Load(ctx, s, seed.Options{DatasetsDir: dir, DefaultCountry: "ES"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if res.Loaded != 1 {
		t.Fatalf("loaded = %d, want 1 (errors: %v)", res.Loaded, res.Errors)
	}
	ests, _ := s.ListEstablishments(ctx)
	if len(ests) != 1 {
		t.Fatalf("establishments = %d, want 1", len(ests))
	}
	e := ests[0]
	if e.Name != "Bar El Puerto" || e.Address != "Rúa Nova, 2" || e.Category != model.CategoryBar {
		t.Errorf("mapped fields = %+v", e)
	}
	if e.Lat == nil || *e.Lat != 43.0112 || e.Lng == nil || *e.Lng != -7.5572 {
		t.Errorf("coords = %v / %v", e.Lat, e.Lng)
	}
	if e.Country != "ES" || e.Phone != "+34982000000" || e.Notes != "Abierto" {
		t.Errorf("country/phone/notes = %q/%q/%q", e.Country, e.Phone, e.Notes)
	}
	if len(e.Certifications) != 2 || e.Certifications[0] != "Q" || e.Certifications[1] != "Turismo" {
		t.Errorf("certifications = %v", e.Certifications)
	}
	if e.LastInspectionAt == nil || e.LastInspectionAt.Format("2006-01-02") != "2024-05-14" {
		t.Errorf("last inspection = %v", e.LastInspectionAt)
	}
}

func TestLoadDatasetsValidatesRows(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	body := "id,name\nok-1,Válido\nbad-1,\n"
	writeDataset(t, dir, "base.csv", body)

	s := openStore(t)
	if _, err := seed.Load(ctx, s, seed.Options{DatasetsDir: dir, StrictValidation: true}); err == nil {
		t.Fatal("strict Load succeeded with an empty name")
	}

	res, err := seed.Load(ctx, openStore(t), seed.Options{DatasetsDir: dir})
	if err != nil {
		t.Fatalf("lenient Load: %v", err)
	}
	if res.Loaded != 1 || res.Skipped != 1 || len(res.Errors) != 1 {
		t.Errorf("loaded/skipped/errors = %d/%d/%d, want 1/1/1", res.Loaded, res.Skipped, len(res.Errors))
	}
	if !strings.Contains(res.Errors[0], "base.csv: registro 2") {
		t.Errorf("error = %q, want the file and row number", res.Errors[0])
	}
}

func TestLoadDatasetsRequiresCSVFiles(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	writeDataset(t, dir, "notes.txt", "not a dataset")

	if _, err := seed.Load(ctx, openStore(t), seed.Options{DatasetsDir: dir}); err == nil {
		t.Error("Load succeeded in a directory with no CSV files")
	}
	if _, err := seed.Load(ctx, openStore(t), seed.Options{DatasetsDir: filepath.Join(dir, "missing")}); err == nil {
		t.Error("Load succeeded with a missing datasets directory")
	}
}
