package seed

import (
	"bufio"
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/datadope/turislab-doctor/internal/turislab/model"
)

// csvColumns maps a (normalised) CSV header to the Establishment field it
// fills. Spanish aliases let registry exports be dropped into datasets/ as
// they arrive, without renaming columns first. Unknown columns are ignored.
//
// `categoria` and `tipo` are ambiguous and resolved by buildLayout: in the
// REAT accommodation directory `tipo` is the establishment type and
// `categoria` the star rating ("4 ESTRELAS"), while in other exports
// `categoria` is the type itself.
var csvColumns = map[string]string{
	"id": "id", "identifier": "id", "identificador": "id", "codigo": "codigo",
	"código": "codigo", "sinatura": "id", "signatura": "id",
	"name": "name", "nombre": "name", "establecimiento": "name", "denominacion": "name",
	"denominación": "name", "razon_social": "name", "razón_social": "name",
	"category": "category", "categoria": "categoria", "categoría": "categoria",
	"tipo": "tipo", "tipoloxia": "tipo", "tipología": "tipo", "tipologia": "tipo",
	"stars": "stars", "estrellas": "stars",
	"address": "address", "direccion": "address", "dirección": "address", "enderezo": "address",
	"municipality": "municipality", "municipio": "municipality", "concello": "municipality",
	"province": "province", "provincia": "province",
	"postal_code": "postal_code", "postalcode": "postal_code", "zip": "postal_code", "cp": "postal_code",
	"codigo_postal": "postal_code", "código_postal": "postal_code",
	"country": "country", "pais": "country", "país": "country",
	"lat": "lat", "latitude": "lat", "latitud": "lat",
	"lng": "lng", "lon": "lng", "long": "lng", "longitude": "lng", "longitud": "lng",
	"lonxitude": "lng", "lonxitud": "lng",
	"phone": "phone", "telefono": "phone", "teléfono": "phone", "tel": "phone",
	"email": "email", "mail": "email", "correo": "email", "correo_electronico": "email",
	"correo_electrónico": "email",
	"website":            "website", "web": "website", "url": "website", "sitio_web": "website",
	"registry_id": "registry_id", "registro": "registry_id", "id_registro": "registry_id",
	"numero_registro": "registry_id", "número_registro": "registry_id",
	"certifications": "certifications", "certificaciones": "certifications",
	"last_inspection_at": "last_inspection_at", "fecha_inspeccion": "last_inspection_at",
	"fecha_inspección":       "last_inspection_at",
	"last_inspection_result": "last_inspection_result", "resultado_inspeccion": "last_inspection_result",
	"resultado_inspección": "last_inspection_result",
	"google_place_id":      "google_place_id",
	"tripadvisor_id":       "tripadvisor_id",
	"yelp_id":              "yelp_id",
	"osm_id":               "osm_id",
	"notes":                "notes", "notas": "notes", "observaciones": "notes",
	"id_generated": "id_generated",
}

// ignoredColumns are deliberately never read, even though they look like a
// field we know. `correo_promocional` in the REAT accommodation export is a
// promotional address and Turismo de Galicia forbids using it commercially,
// so it must not become Establishment.Email.
var ignoredColumns = map[string]bool{
	"correo_promocional": true,
}

// headerScanLimit is how many leading records are inspected before giving up
// on finding the header. Official exports carry a short preamble (title,
// reference date, area) before the header row.
const headerScanLimit = 40

// loadDatasets lists every CSV in opts.DatasetsDir and upserts all their
// records. Files are processed in name order so overlapping IDs resolve
// deterministically (the later file wins).
func loadDatasets(ctx context.Context, s Store, opts Options) (Result, error) {
	entries, err := os.ReadDir(opts.DatasetsDir)
	if err != nil {
		return Result{}, fmt.Errorf("seed: read datasets dir %q: %w", opts.DatasetsDir, err)
	}

	var paths []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".csv") {
			continue
		}
		paths = append(paths, filepath.Join(opts.DatasetsDir, entry.Name()))
	}
	sort.Strings(paths)
	if len(paths) == 0 {
		return Result{}, fmt.Errorf("seed: no .csv files in %q", opts.DatasetsDir)
	}

	var res Result
	for _, path := range paths {
		file, err := loadCSVFile(ctx, s, opts, path)
		res.Files = append(res.Files, file)
		res.Total += file.Total
		res.Loaded += file.Loaded
		res.Skipped += file.Skipped
		res.Errors = append(res.Errors, file.Errors...)
		if err != nil {
			return res, err
		}
	}
	return res, nil
}

// loadCSVFile reads one CSV file. Commas and semicolons are both accepted as
// delimiters (the first buffer decides), a UTF-8 BOM is tolerated and any
// preamble before the header row is skipped.
func loadCSVFile(ctx context.Context, s Store, opts Options, path string) (FileResult, error) {
	var res FileResult
	res.Path = path

	f, err := os.Open(path)
	if err != nil {
		return res, fmt.Errorf("seed: open %q: %w", path, err)
	}
	defer f.Close()

	br := bufio.NewReader(f)
	headerLine, comma, err := scanHeaderLine(br)
	if err != nil {
		return res, fmt.Errorf("seed: %s: %w", path, err)
	}

	// Replay the header line through the CSV reader so quoting is handled by
	// encoding/csv rather than by the detector.
	r := csv.NewReader(io.MultiReader(strings.NewReader(headerLine), br))
	r.Comma = comma
	r.FieldsPerRecord = -1 // exports often pad or omit trailing columns
	r.TrimLeadingSpace = true

	header, err := r.Read()
	if err != nil {
		return res, fmt.Errorf("seed: %s: read header: %w", path, err)
	}
	layout, err := buildLayout(header, comma)
	if err != nil {
		return res, fmt.Errorf("seed: %s: %w", path, err)
	}

	for {
		row, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return res, fmt.Errorf("seed: %s: read row %d: %w", path, res.Total+1, err)
		}
		if isBlankRow(row) {
			continue
		}
		res.Total++

		est, err := establishmentFromCSV(row, layout, opts.DefaultCountry)
		if err != nil {
			msg := fmt.Sprintf("%s: registro %d: %v", filepath.Base(path), res.Total, err)
			if opts.StrictValidation {
				return res, fmt.Errorf("seed: %s", msg)
			}
			res.Skipped++
			res.Errors = append(res.Errors, msg)
			continue
		}
		if err := s.SaveEstablishment(ctx, est); err != nil {
			return res, fmt.Errorf("seed: %s: save %q: %w", path, est.ID, err)
		}
		res.Loaded++
	}
	return res, nil
}

// scanHeaderLine walks the leading lines of a file until it finds the header
// row, returning that raw line and the delimiter it uses. Official exports
// carry a short preamble (title, reference date, legal note) before the
// header, and the preamble may itself contain commas, so the delimiter is
// detected on the header line only.
func scanHeaderLine(br *bufio.Reader) (string, rune, error) {
	for i := 0; i < headerScanLimit; i++ {
		line, err := br.ReadString('\n')
		if comma, ok := detectDelimiter(line); ok {
			return line, comma, nil
		}
		if err != nil {
			if i == 0 && line == "" {
				return "", 0, fmt.Errorf("empty CSV")
			}
			break
		}
	}
	return "", 0, fmt.Errorf("no header row in the first %d lines", headerScanLimit)
}

// detectDelimiter reports whether a line is the header and which delimiter it
// uses. A header is a line where one of the cells is a name alias.
func detectDelimiter(line string) (rune, bool) {
	line = strings.TrimPrefix(line, "\ufeff")
	for _, comma := range []rune{';', ','} {
		for _, cell := range strings.Split(line, string(comma)) {
			if field, ok := csvColumns[normaliseHeader(cell)]; ok && field == "name" {
				return comma, true
			}
		}
	}
	return 0, false
}

func isBlankRow(row []string) bool {
	for _, v := range row {
		if strings.TrimSpace(v) != "" {
			return false
		}
	}
	return true
}

func normaliseHeader(s string) string {
	s = strings.TrimPrefix(strings.TrimSpace(strings.ToLower(s)), "\ufeff")
	s = strings.Trim(s, `"' `)
	s = strings.ReplaceAll(s, " ", "_")
	return strings.Trim(s, "_")
}

// layout is the resolved column map of one CSV file.
type layout struct {
	cols map[string]int
	// starsFromRating marks that `stars` points at the REAT `categoria`
	// column ("4 ESTRELAS", "SEGUNDA CATEGORÍA"), whose values are not always
	// a number. Those are parsed leniently; a canonical `stars` column stays
	// strict.
	starsFromRating bool
	comma           rune
}

// buildLayout resolves the ambiguous `categoria`/`tipo` pair:
//
//   - `tipo` + `categoria`  → `tipo` is the category, `categoria` the rating.
//   - only `tipo`           → `tipo` is the category.
//   - only `categoria`      → `categoria` is the category.
//   - explicit `category`   → wins over both.
func buildLayout(header []string, comma rune) (layout, error) {
	raw := make(map[string]int, len(header))
	for i, h := range header {
		key := normaliseHeader(h)
		if ignoredColumns[key] {
			continue
		}
		if field, ok := csvColumns[key]; ok {
			raw[field] = i
		}
	}

	l := layout{cols: make(map[string]int, len(raw)), comma: comma}
	for field, idx := range raw {
		switch field {
		case "categoria", "tipo", "codigo":
			continue
		default:
			l.cols[field] = idx
		}
	}

	// `codigo` is the agency code in the REAT agency directory; only use it
	// as the identifier when the file has no proper one.
	if _, ok := l.cols["id"]; !ok {
		if idx, ok := raw["codigo"]; ok {
			l.cols["id"] = idx
		}
	}

	_, hasTipo := raw["tipo"]
	catIdx, hasCategoria := raw["categoria"]
	_, hasCategory := l.cols["category"]
	_, hasStars := l.cols["stars"]

	if hasTipo {
		if !hasCategory {
			l.cols["category"] = raw["tipo"]
		}
		if hasCategoria && !hasStars {
			l.cols["stars"] = catIdx
			l.starsFromRating = true
		}
	} else if hasCategoria && !hasCategory {
		l.cols["category"] = catIdx
	}

	if _, ok := l.cols["name"]; !ok {
		return layout{}, fmt.Errorf("header has no name column (found %v)", header)
	}
	return l, nil
}

// establishmentFromCSV maps one row onto an Establishment. A missing or empty
// cell leaves the field at its zero value, which normalise then fills in where
// possible.
func establishmentFromCSV(row []string, l layout, defaultCountry string) (*model.Establishment, error) {
	get := func(field string) string {
		i, ok := l.cols[field]
		if !ok || i >= len(row) {
			return ""
		}
		return strings.TrimSpace(row[i])
	}

	e := &model.Establishment{
		ID:                   get("id"),
		Name:                 get("name"),
		Category:             categoryFromLabel(get("category")),
		Address:              get("address"),
		Municipality:         normalisePlace(get("municipality")),
		Province:             normalisePlace(get("province")),
		PostalCode:           postalCode(get("postal_code")),
		Country:              get("country"),
		Phone:                get("phone"),
		Email:                get("email"),
		Website:              get("website"),
		RegistryID:           get("registry_id"),
		LastInspectionResult: get("last_inspection_result"),
		GooglePlaceID:        get("google_place_id"),
		TripAdvisorID:        get("tripadvisor_id"),
		YelpID:               get("yelp_id"),
		OSMID:                get("osm_id"),
		Notes:                get("notes"),
	}

	if v := get("stars"); v != "" {
		stars, err := parseStars(v, l.starsFromRating)
		if err != nil {
			return nil, err
		}
		e.Stars = stars
	}
	lat, err := parseCoord(get("lat"), l.comma)
	if err != nil {
		return nil, fmt.Errorf("lat %q: %w", get("lat"), err)
	}
	lng, err := parseCoord(get("lng"), l.comma)
	if err != nil {
		return nil, fmt.Errorf("lng %q: %w", get("lng"), err)
	}
	// A lone zero is a missing coordinate, not the Gulf of Guinea.
	if lat != nil && lng != nil {
		e.Lat, e.Lng = lat, lng
	}
	if v := get("id_generated"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return nil, fmt.Errorf("id_generated %q: %w", v, err)
		}
		e.IDGenerated = b
	}
	if v := get("last_inspection_at"); v != "" {
		t, err := parseDate(v)
		if err != nil {
			return nil, fmt.Errorf("last_inspection_at %q: %w", v, err)
		}
		e.LastInspectionAt = &t
	}
	if v := get("certifications"); v != "" {
		sep := "|"
		if l.comma == ';' {
			sep = ","
		}
		for _, cert := range strings.Split(v, sep) {
			if cert = strings.TrimSpace(cert); cert != "" {
				e.Certifications = append(e.Certifications, cert)
			}
		}
	}

	if err := normalise(e, defaultCountry); err != nil {
		return nil, err
	}
	return e, nil
}

// categoryFromLabel maps a registry label onto the model's categories.
// "Bares" → bar, "Restaurantes" → restaurant, "HOTEIS"/"PENSIÓNS" → hotel;
// anything else (albergues, apartamentos, turismo rural, campamentos,
// viviendas de uso turístico…) stays "other".
func categoryFromLabel(v string) model.Category {
	n := normaliseLabel(v)
	switch model.Category(n) {
	case model.CategoryRestaurant, model.CategoryHotel, model.CategoryBar, model.CategoryOther:
		return model.Category(n)
	}
	switch {
	case n == "":
		return ""
	case strings.HasPrefix(n, "bar"), strings.HasPrefix(n, "caf"):
		return model.CategoryBar
	case strings.HasPrefix(n, "restaurant"):
		return model.CategoryRestaurant
	case strings.HasPrefix(n, "hotel"), strings.HasPrefix(n, "hotei"),
		strings.HasPrefix(n, "pension"), strings.HasPrefix(n, "hostal"):
		return model.CategoryHotel
	}
	return model.CategoryOther
}

// parseStars reads "4" and "4 ESTRELAS" as 4. When the value comes from the
// REAT rating column, non-numeric labels ("SEGUNDA CATEGORÍA", "1 CHAVE",
// "GRUPO-B") mean "no star rating" rather than an error.
func parseStars(v string, lenient bool) (*int, error) {
	fields := strings.Fields(v)
	if len(fields) == 0 {
		return nil, nil
	}
	n, err := strconv.Atoi(fields[0])
	if err != nil {
		if lenient {
			return nil, nil
		}
		return nil, fmt.Errorf("stars %q: %w", v, err)
	}
	if lenient && len(fields) > 1 && !starUnits[normaliseLabel(strings.Join(fields[1:], " "))] {
		return nil, nil
	}
	return &n, nil
}

// starUnits are the only words accepted after the number in a rating column,
// so "1 CHAVE" (a key, not a star) does not become 1 star.
var starUnits = map[string]bool{
	"estrela": true, "estrelas": true,
	"estrella": true, "estrellas": true,
	"star": true, "stars": true,
}

// postalCode drops the "0" placeholder the registry uses when the code is
// unknown.
func postalCode(v string) string {
	if v == "0" {
		return ""
	}
	return v
}

// parseCoord accepts "43.0112" and, in semicolon-delimited exports,
// "43,0112". Zero and empty mean "no coordinate".
func parseCoord(v string, comma rune) (*float64, error) {
	v = strings.TrimSpace(v)
	if v == "" || v == "0" || v == "0.0" || v == "0,0" {
		return nil, nil
	}
	f, err := parseFloat(v, comma)
	if err != nil {
		return nil, err
	}
	if f == 0 {
		return nil, nil
	}
	return &f, nil
}

// parseFloat accepts both "43.0112" and, in semicolon-delimited exports,
// "43,0112".
func parseFloat(v string, comma rune) (float64, error) {
	if comma == ';' {
		v = strings.ReplaceAll(v, ",", ".")
	}
	return strconv.ParseFloat(v, 64)
}

// normaliseLabel lowercases, strips accents and collapses whitespace so
// "PENSIÓNS", "Pensions" and "pensións" compare equal.
func normaliseLabel(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		switch r {
		case 'á', 'à', 'ä', 'â':
			r = 'a'
		case 'é', 'è', 'ë', 'ê':
			r = 'e'
		case 'í', 'ì', 'ï', 'î':
			r = 'i'
		case 'ó', 'ò', 'ö', 'ô':
			r = 'o'
		case 'ú', 'ù', 'ü', 'û':
			r = 'u'
		case 'ñ':
			r = 'n'
		}
		b.WriteRune(r)
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// placeParticles stay lowercase inside a municipality or province name.
var placeParticles = map[string]bool{
	"a": true, "o": true, "as": true, "os": true,
	"da": true, "de": true, "do": true, "das": true, "dos": true, "del": true,
	"la": true, "el": true, "las": true, "los": true, "e": true, "y": true,
}

// normalisePlace title-cases names exported in full uppercase ("A CORUÑA" →
// "A Coruña") and leaves already mixed-case values untouched, so the same
// municipality compares equal across directories.
func normalisePlace(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || v != strings.ToUpper(v) {
		return v
	}
	words := strings.Fields(strings.ToLower(v))
	for i, w := range words {
		if i > 0 && placeParticles[w] {
			continue
		}
		r := []rune(w)
		r[0] = unicode.ToUpper(r[0])
		words[i] = string(r)
	}
	return strings.Join(words, " ")
}

func parseDate(v string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339, "2006-01-02", "02/01/2006"} {
		if t, err := time.Parse(layout, v); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported date format")
}
