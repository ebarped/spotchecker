package seed

import (
	"bufio"
	"bytes"
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

	"github.com/datadope/turislab-doctor/internal/turislab/model"
)

// csvColumns maps a (normalised) CSV header to the Establishment field it
// fills. Spanish aliases let registry exports be dropped into datasets/ as
// they arrive, without renaming columns first. Unknown columns are ignored.
var csvColumns = map[string]string{
	"id": "id", "identifier": "id", "identificador": "id", "codigo": "id", "código": "id",
	"name": "name", "nombre": "name", "establecimiento": "name", "razon_social": "name", "razón_social": "name",
	"category": "category", "categoria": "category", "categoría": "category", "tipo": "category",
	"stars": "stars", "estrellas": "stars",
	"address": "address", "direccion": "address", "dirección": "address",
	"municipality": "municipality", "municipio": "municipality",
	"province": "province", "provincia": "province",
	"postal_code": "postal_code", "postalcode": "postal_code", "zip": "postal_code", "cp": "postal_code",
	"codigo_postal": "postal_code", "código_postal": "postal_code",
	"country": "country", "pais": "country", "país": "country",
	"lat": "lat", "latitude": "lat", "latitud": "lat",
	"lng": "lng", "lon": "lng", "long": "lng", "longitude": "lng", "longitud": "lng",
	"phone": "phone", "telefono": "phone", "teléfono": "phone", "tel": "phone",
	"email": "email", "correo": "email", "correo_electronico": "email", "correo_electrónico": "email",
	"website": "website", "web": "website", "url": "website", "sitio_web": "website",
	"registry_id": "registry_id", "registro": "registry_id", "id_registro": "registry_id",
	"numero_registro": "registry_id", "número_registro": "registry_id",
	"certifications": "certifications", "certificaciones": "certifications",
	"last_inspection_at": "last_inspection_at", "fecha_inspeccion": "last_inspection_at", "fecha_inspección": "last_inspection_at",
	"last_inspection_result": "last_inspection_result", "resultado_inspeccion": "last_inspection_result", "resultado_inspección": "last_inspection_result",
	"google_place_id": "google_place_id",
	"tripadvisor_id":  "tripadvisor_id",
	"yelp_id":         "yelp_id",
	"osm_id":          "osm_id",
	"notes":           "notes", "notas": "notes", "observaciones": "notes",
	"id_generated": "id_generated",
}

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

// loadCSVFile reads one CSV file with a header row. Commas and semicolons are
// both accepted as delimiters (the first buffer decides), and a UTF-8 BOM is
// tolerated.
func loadCSVFile(ctx context.Context, s Store, opts Options, path string) (FileResult, error) {
	var res FileResult
	res.Path = path

	f, err := os.Open(path)
	if err != nil {
		return res, fmt.Errorf("seed: open %q: %w", path, err)
	}
	defer f.Close()

	br := bufio.NewReader(f)
	peek, _ := br.Peek(64 * 1024) // short files return the bytes read plus io.EOF
	comma := ','
	if bytes.Count(peek, []byte{';'}) > bytes.Count(peek, []byte{','}) {
		comma = ';'
	}

	r := csv.NewReader(br)
	r.Comma = comma
	r.FieldsPerRecord = -1 // exports often pad or omit trailing columns
	r.TrimLeadingSpace = true

	header, err := r.Read()
	if err != nil {
		if err == io.EOF {
			return res, fmt.Errorf("seed: %s: empty CSV", path)
		}
		return res, fmt.Errorf("seed: %s: read header: %w", path, err)
	}
	if len(header) > 0 {
		header[0] = strings.TrimPrefix(header[0], "\ufeff")
	}

	cols := make(map[string]int, len(header))
	for i, h := range header {
		if field, ok := csvColumns[normaliseHeader(h)]; ok {
			cols[field] = i
		}
	}
	if _, ok := cols["name"]; !ok {
		return res, fmt.Errorf("seed: %s: header has no name column (found %v)", path, header)
	}

	for {
		row, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return res, fmt.Errorf("seed: %s: read row %d: %w", path, res.Total+1, err)
		}
		res.Total++

		est, err := establishmentFromCSV(row, cols, comma, opts.DefaultCountry)
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

func normaliseHeader(s string) string {
	s = strings.TrimPrefix(strings.TrimSpace(strings.ToLower(s)), "\ufeff")
	s = strings.ReplaceAll(s, " ", "_")
	return strings.Trim(s, "_")
}

// establishmentFromCSV maps one row onto an Establishment, using the header
// index built by loadCSVFile. A missing or empty cell leaves the field at its
// zero value, which normalise then fills in where possible.
func establishmentFromCSV(row []string, cols map[string]int, comma rune, defaultCountry string) (*model.Establishment, error) {
	get := func(field string) string {
		i, ok := cols[field]
		if !ok || i >= len(row) {
			return ""
		}
		return strings.TrimSpace(row[i])
	}

	e := &model.Establishment{
		ID:                   get("id"),
		Name:                 get("name"),
		Category:             model.Category(get("category")),
		Address:              get("address"),
		Municipality:         get("municipality"),
		Province:             get("province"),
		PostalCode:           get("postal_code"),
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
		n, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("stars %q: %w", v, err)
		}
		e.Stars = &n
	}
	if v := get("lat"); v != "" {
		f, err := parseFloat(v, comma)
		if err != nil {
			return nil, fmt.Errorf("lat %q: %w", v, err)
		}
		e.Lat = &f
	}
	if v := get("lng"); v != "" {
		f, err := parseFloat(v, comma)
		if err != nil {
			return nil, fmt.Errorf("lng %q: %w", v, err)
		}
		e.Lng = &f
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
		if comma == ';' {
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

// parseFloat accepts both "43.0112" and, in semicolon-delimited exports,
// "43,0112".
func parseFloat(v string, comma rune) (float64, error) {
	if comma == ';' {
		v = strings.ReplaceAll(v, ",", ".")
	}
	return strconv.ParseFloat(v, 64)
}

func parseDate(v string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339, "2006-01-02", "02/01/2006"} {
		if t, err := time.Parse(layout, v); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported date format")
}
