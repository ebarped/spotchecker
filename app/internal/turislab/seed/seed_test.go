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

// reatRestauracion reproduces the REAT restaurant directory: a five-line
// preamble, semicolons, and no coordinates, phone or website.
const reatRestauracion = `Directorio de restauración do REAT
 Data de referencia: 2026-11-01
 Área de Estudos e Investigación
 
 
"sinatura";"denominacion";"direccion";"lugar";"parroquia";"codigo_postal";"concello";"provincia";"tipoloxia"
"R-LU-000710";"LA OCA";"AVDA. RAMÓN FERREIRO, 21";"";"";27002;"Lugo";"Lugo";"Restaurantes"
"V-LU-003877";"BAR  RAYDY GRANDE LIGAS";"MILAGROSA nº 23, andar BJ";"";"";0;"Lugo";"Lugo";"Bares"
"C-LU-000267";"GRAN CAFE";"RAMON FERREIRO nº 21";"";"";27002;"Lugo";"Lugo";"Cafeterias"
`

// reatAloxamentos reproduces the REAT accommodation directory: `tipo` holds
// the establishment type, `categoria` the star rating, coordinates use comma
// decimals, and `correo_promocional` must never become the email.
const reatAloxamentos = `Directorio de aloxamentos do REAT
 Data de referencia: 2026-11-01
 Área de Estudos e Investigación
 
 
"sinatura";"denominacion";"telefono";"tipo";"categoria";"lonxitude";"latitude";"correo_promocional";"direccion";"codigo_postal";"concello";"provincia"
"H-LU-000469";"EUROSTARS GRAN HOTEL DE LUGO";"982224152";"HOTEIS";"4 ESTRELAS";"-7,55431";"43,00503";"CORREO@EXAMPLE.ES";"AVENIDA RAMÓN FERREIRO Nº 21";"27002";"LUGO";"LUGO"
"H-LU-001017";"PENSION ALBERGUE LUCUS";"";"ALBERGUES TURÍSTICOS";"SEGUNDA CATEGORÍA";"-7,5499";"43,00519";"";"CALLE REI DON GARCÍA Nº 1";"0";"LUGO";"LUGO"
"A-LU-000018";"APARTAMENTOS SIN COORDENADAS";"";"APARTAMENTOS TURÍSTICOS";"1 CHAVE";"0";"0";"";"CALLE CATASOL Nº 13";"27002";"LUGO";"LUGO"
`

func TestLoadDatasetsSkipsPreambleAndMapsREATRestauracion(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	dir := t.TempDir()
	writeDataset(t, dir, "restauracion.csv", reatRestauracion)

	res, err := seed.Load(ctx, s, seed.Options{DatasetsDir: dir, DefaultCountry: "ES", StrictValidation: true})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if res.Total != 3 || res.Loaded != 3 {
		t.Fatalf("total/loaded = %d/%d, want 3/3 (errors: %v)", res.Total, res.Loaded, res.Errors)
	}

	oca, err := s.GetEstablishment(ctx, "R-LU-000710")
	if err != nil {
		t.Fatalf("GetEstablishment: %v", err)
	}
	if oca.Name != "LA OCA" || oca.Category != model.CategoryRestaurant {
		t.Errorf("oca = %q / %q", oca.Name, oca.Category)
	}
	if oca.Address != "AVDA. RAMÓN FERREIRO, 21" || oca.Municipality != "Lugo" || oca.PostalCode != "27002" {
		t.Errorf("oca address = %q / %q / %q", oca.Address, oca.Municipality, oca.PostalCode)
	}
	if oca.Lat != nil || oca.Lng != nil {
		t.Errorf("restauración has no coordinates, got %v / %v", oca.Lat, oca.Lng)
	}

	bar, _ := s.GetEstablishment(ctx, "V-LU-003877")
	if bar.Category != model.CategoryBar {
		t.Errorf("Bares → %q, want bar", bar.Category)
	}
	if bar.PostalCode != "" {
		t.Errorf("postal code 0 should be empty, got %q", bar.PostalCode)
	}
	cafe, _ := s.GetEstablishment(ctx, "C-LU-000267")
	if cafe.Category != model.CategoryBar {
		t.Errorf("Cafeterias → %q, want bar", cafe.Category)
	}
}

func TestLoadDatasetsMapsREATAloxamentos(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	dir := t.TempDir()
	writeDataset(t, dir, "aloxamentos.csv", reatAloxamentos)

	if _, err := seed.Load(ctx, s, seed.Options{DatasetsDir: dir, DefaultCountry: "ES", StrictValidation: true}); err != nil {
		t.Fatalf("Load: %v", err)
	}

	hotel, err := s.GetEstablishment(ctx, "H-LU-000469")
	if err != nil {
		t.Fatalf("GetEstablishment: %v", err)
	}
	if hotel.Category != model.CategoryHotel {
		t.Errorf("HOTEIS → %q, want hotel", hotel.Category)
	}
	if hotel.Stars == nil || *hotel.Stars != 4 {
		t.Errorf("stars = %v, want 4", hotel.Stars)
	}
	if hotel.Lat == nil || *hotel.Lat != 43.00503 || hotel.Lng == nil || *hotel.Lng != -7.55431 {
		t.Errorf("coords = %v / %v", hotel.Lat, hotel.Lng)
	}
	if hotel.Municipality != "Lugo" || hotel.Province != "Lugo" {
		t.Errorf("place = %q / %q, want title-cased", hotel.Municipality, hotel.Province)
	}
	if hotel.Email != "" {
		t.Errorf("correo_promocional leaked into email: %q", hotel.Email)
	}
	if hotel.Phone != "982224152" {
		t.Errorf("phone = %q", hotel.Phone)
	}

	// "SEGUNDA CATEGORÍA" is not a star rating, and a 0/0 coordinate pair
	// must not become a point in the Atlantic.
	pension, _ := s.GetEstablishment(ctx, "H-LU-001017")
	if pension.Stars != nil {
		t.Errorf("SEGUNDA CATEGORÍA → stars %v, want nil", pension.Stars)
	}
	if pension.Category != model.CategoryOther {
		t.Errorf("ALBERGUES → %q, want other", pension.Category)
	}
	if pension.PostalCode != "" {
		t.Errorf("postal code 0 should be empty, got %q", pension.PostalCode)
	}

	// A 0/0 coordinate pair is a missing coordinate, not the Gulf of Guinea.
	apt, _ := s.GetEstablishment(ctx, "A-LU-000018")
	if apt.Lat != nil || apt.Lng != nil {
		t.Errorf("0/0 coords should be nil, got %v / %v", apt.Lat, apt.Lng)
	}
	if apt.Stars != nil {
		t.Errorf("1 CHAVE → stars %v, want nil", apt.Stars)
	}
}

func TestLoadDatasetsFindsHeaderAfterLongPreamble(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	var b strings.Builder
	for i := 0; i < 12; i++ {
		b.WriteString("NOTA: texto legal del rexistro, sen datos de establecementos.\n")
	}
	b.WriteString("id;nombre;municipio\nrest-1;Restaurante Uno;Lugo\n")
	writeDataset(t, dir, "export.csv", b.String())

	res, err := seed.Load(ctx, openStore(t), seed.Options{DatasetsDir: dir})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if res.Loaded != 1 {
		t.Errorf("loaded = %d, want 1", res.Loaded)
	}
}
