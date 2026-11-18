// Command reat2csv builds a datasets CSV from the REAT directories published
// by Turismo de Galicia.
//
// The REAT (Rexistro de Empresas e Actividades Turísticas) publishes one CSV
// per directory with a short preamble before the header. This command
// downloads them, filters by municipality and area, and writes the canonical
// 24-column CSV that the platform loads from datasets/:
//
//	go run ./cmd/reat2csv -out datasets/lugo.csv
//
// The result is reproducible: re-running it regenerates the same file.
package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const (
	defaultRestauracionURL = "https://descargascdn.xunta.gal/interno/smarxa/reat_directorio-restauracion_gal.csv"
	defaultAloxamentosURL  = "https://descargascdn.xunta.gal/interno/smarxa/reat_directorio-aloxamentos_gal.csv"
)

// outputColumns is the canonical dataset schema read by the seed package.
var outputColumns = []string{
	"id", "name", "category", "stars", "address", "municipality", "province",
	"postal_code", "country", "lat", "lng", "phone", "email", "website",
	"registry_id", "certifications", "last_inspection_at",
	"last_inspection_result", "google_place_id", "tripadvisor_id", "yelp_id",
	"osm_id", "notes", "id_generated",
}

// record is one selected establishment, already normalised.
type record struct {
	id, name, category, address, municipality, province, postalCode, phone string
	stars                                                                  *int
	lat, lng                                                               *float64
	priority                                                               int     // street order in the restaurant directory
	dist                                                                   float64 // metres from the anchor
}

func main() {
	var (
		out            = flag.String("out", "datasets/lugo.csv", "output CSV path")
		concello       = flag.String("concello", "Lugo", "municipality to keep")
		lat            = flag.Float64("lat", 43.00503, "anchor latitude (La Oca, Avda. Ramón Ferreiro 21, Lugo)")
		lng            = flag.Float64("lng", -7.55431, "anchor longitude")
		radius         = flag.Float64("radius", 500, "radius in metres around the anchor for the accommodation directory")
		streets        = flag.String("streets", "RAMON FERREIRO,NOREAS,CAMPO CASTELO", "comma-separated street fragments for the restaurant directory (it has no coordinates)")
		maxAlox        = flag.Int("max-alox", 6, "maximum accommodation records")
		maxRest        = flag.Int("max-rest", 4, "maximum restaurant records")
		restauracion   = flag.String("restauracion-url", defaultRestauracionURL, "restaurant directory URL")
		aloxamentosURL = flag.String("aloxamentos-url", defaultAloxamentosURL, "accommodation directory URL")
	)
	flag.Parse()

	client := &http.Client{Timeout: 90 * time.Second}

	restRows, err := fetch(client, *restauracion)
	if err != nil {
		fatal("restauración: %v", err)
	}
	aloxRows, err := fetch(client, *aloxamentosURL)
	if err != nil {
		fatal("aloxamentos: %v", err)
	}

	rest := selectRestaurants(restRows, *concello, splitList(*streets), *maxRest)
	alox := selectAccommodation(aloxRows, *concello, *lat, *lng, *radius, *maxAlox)

	// The restaurant directory has no coordinates. When two selected records
	// share street and number they are the same building, so the one with
	// coordinates lends them to the other (La Oca sits at the hotel's address).
	inheritCoordinates(rest, alox)

	all := append(rest, alox...)
	if len(all) == 0 {
		fatal("no records matched concello=%q", *concello)
	}
	if err := writeCSV(*out, all); err != nil {
		fatal("write %s: %v", *out, err)
	}

	fmt.Printf("wrote %s (%d records)\n", *out, len(all))
	for _, r := range all {
		coords := "sin coords"
		if r.lat != nil {
			coords = fmt.Sprintf("%.5f,%.5f", *r.lat, *r.lng)
		}
		stars := ""
		if r.stars != nil {
			stars = fmt.Sprintf(" %d★", *r.stars)
		}
		fmt.Printf("  %-13s %-38s %-10s%s %s\n", r.id, truncate(r.name, 38), r.category, stars, coords)
	}
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "reat2csv: "+format+"\n", args...)
	os.Exit(1)
}

func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// fetch downloads a REAT directory and returns its rows as header→value maps.
func fetch(client *http.Client, url string) ([]map[string]string, error) {
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return parseTable(string(body))
}

// parseTable skips the preamble before the header row and parses the rest.
func parseTable(text string) ([]map[string]string, error) {
	lines := strings.Split(text, "\n")
	headerAt := -1
	for i, line := range lines {
		if strings.Contains(strings.ToLower(line), `"sinatura"`) {
			headerAt = i
			break
		}
	}
	if headerAt < 0 {
		return nil, fmt.Errorf("header row not found")
	}

	r := csv.NewReader(strings.NewReader(strings.Join(lines[headerAt:], "\n")))
	r.Comma = ';'
	r.FieldsPerRecord = -1
	header, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("read header: %w", err)
	}
	for i := range header {
		header[i] = strings.TrimSpace(header[i])
	}

	var rows []map[string]string
	for {
		values, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		row := make(map[string]string, len(header))
		blank := true
		for i, h := range header {
			if i < len(values) {
				row[h] = strings.TrimSpace(values[i])
				if row[h] != "" {
					blank = false
				}
			}
		}
		if !blank {
			rows = append(rows, row)
		}
	}
	return rows, nil
}

// selectRestaurants keeps the rows of the municipality whose address contains
// one of the given streets, ordered by the street priority.
func selectRestaurants(rows []map[string]string, concello string, streets []string, max int) []record {
	var kept []record
	for _, row := range rows {
		if !samePlace(row["concello"], concello) {
			continue
		}
		address := row["direccion"]
		if address == "" {
			address = row["lugar"]
		}
		priority := streetPriority(address, streets)
		if priority < 0 {
			continue
		}
		kept = append(kept, record{
			id:           row["sinatura"],
			name:         row["denominacion"],
			category:     categoryFromLabel(row["tipoloxia"]),
			address:      address,
			municipality: titleCase(row["concello"]),
			province:     titleCase(row["provincia"]),
			postalCode:   postalCode(row["codigo_postal"]),
			priority:     priority,
		})
	}
	sort.SliceStable(kept, func(i, j int) bool {
		if kept[i].priority != kept[j].priority {
			return kept[i].priority < kept[j].priority
		}
		if kept[i].address != kept[j].address {
			return kept[i].address < kept[j].address
		}
		return kept[i].name < kept[j].name
	})
	return capRecords(kept, max)
}

// selectAccommodation keeps the rows of the municipality within radius metres
// of the anchor, ordered by distance. Viviendas de uso turístico are excluded:
// they are the bulk of the directory and carry no coordinates at all.
func selectAccommodation(rows []map[string]string, concello string, lat, lng, radius float64, max int) []record {
	var kept []record
	for _, row := range rows {
		if !samePlace(row["concello"], concello) {
			continue
		}
		if strings.Contains(normalise(row["tipo"]), "vivenda") {
			continue
		}
		rlat, ok1 := parseCoord(row["latitude"])
		rlng, ok2 := parseCoord(row["lonxitude"])
		if !ok1 || !ok2 {
			continue
		}
		d := distance(lat, lng, rlat, rlng)
		if d > radius {
			continue
		}
		address := row["direccion"]
		if address == "" {
			address = row["lugar"]
		}
		kept = append(kept, record{
			id:           row["sinatura"],
			name:         row["denominacion"],
			category:     categoryFromLabel(row["tipo"]),
			stars:        parseStars(row["categoria"]),
			address:      address,
			municipality: titleCase(row["concello"]),
			province:     titleCase(row["provincia"]),
			postalCode:   postalCode(row["codigo_postal"]),
			phone:        row["telefono"],
			lat:          &rlat,
			lng:          &rlng,
			dist:         d,
		})
	}
	sort.SliceStable(kept, func(i, j int) bool { return kept[i].dist < kept[j].dist })
	return capRecords(kept, max)
}

func capRecords(rs []record, max int) []record {
	if max > 0 && len(rs) > max {
		return rs[:max]
	}
	return rs
}

// inheritCoordinates copies coordinates between records that share street and
// number, i.e. the same building.
func inheritCoordinates(groups ...[]record) {
	type anchor struct{ lat, lng float64 }
	known := make(map[string]anchor)
	for _, group := range groups {
		for _, r := range group {
			if r.lat == nil {
				continue
			}
			if key := addressKey(r.address); key != "" {
				if _, ok := known[key]; !ok {
					known[key] = anchor{*r.lat, *r.lng}
				}
			}
		}
	}
	for _, group := range groups {
		for i := range group {
			if group[i].lat != nil {
				continue
			}
			if a, ok := known[addressKey(group[i].address)]; ok {
				lat, lng := a.lat, a.lng
				group[i].lat, group[i].lng = &lat, &lng
			}
		}
	}
}

// genericAddressWords are dropped before comparing addresses, so
// "AVDA. RAMÓN FERREIRO, 21" and "RAMON FERREIRO nº 21" match.
var genericAddressWords = map[string]bool{
	"avda": true, "avenida": true, "calle": true, "rua": true, "praza": true,
	"plaza": true, "pr": true, "pza": true, "n": true, "no": true, "num": true,
	"numero": true, "sn": true, "s": true, "de": true, "da": true, "do": true,
	"del": true, "la": true, "el": true, "a": true, "o": true, "e": true, "y": true,
}

func addressKey(address string) string {
	// "nº"/"ª" are letters for unicode, so they would survive as words.
	address = strings.NewReplacer("º", " ", "ª", " ", "°", " ").Replace(normalise(address))
	var words []string
	for _, w := range strings.FieldsFunc(address, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if genericAddressWords[w] {
			continue
		}
		words = append(words, w)
	}
	// Require a house number: without one the match is too loose.
	if len(words) < 2 || !hasDigit(words[len(words)-1]) {
		return ""
	}
	return strings.Join(words, " ")
}

func hasDigit(s string) bool {
	for _, r := range s {
		if unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

func streetPriority(address string, streets []string) int {
	n := normalise(address)
	for i, street := range streets {
		if strings.Contains(n, normalise(street)) {
			return i
		}
	}
	return -1
}

// categoryFromLabel mirrors the mapping of the seed package.
func categoryFromLabel(v string) string {
	n := normalise(v)
	switch {
	case n == "":
		return ""
	case strings.HasPrefix(n, "bar"), strings.HasPrefix(n, "caf"):
		return "bar"
	case strings.HasPrefix(n, "restaurant"):
		return "restaurant"
	case strings.HasPrefix(n, "hotel"), strings.HasPrefix(n, "hotei"),
		strings.HasPrefix(n, "pension"), strings.HasPrefix(n, "hostal"):
		return "hotel"
	}
	return "other"
}

func parseStars(v string) *int {
	fields := strings.Fields(v)
	if len(fields) == 0 {
		return nil
	}
	n, err := strconv.Atoi(fields[0])
	if err != nil {
		return nil
	}
	if len(fields) > 1 {
		switch normalise(fields[1]) {
		case "estrela", "estrelas", "estrella", "estrellas":
		default:
			return nil
		}
	}
	return &n
}

func parseCoord(v string) (float64, bool) {
	f, err := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(v), ",", "."), 64)
	if err != nil || f == 0 {
		return 0, false
	}
	return f, true
}

func postalCode(v string) string {
	if v == "0" {
		return ""
	}
	return v
}

func samePlace(a, b string) bool {
	return normalise(a) == normalise(b)
}

// normalise lowercases, strips accents and collapses whitespace.
func normalise(s string) string {
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

var placeParticles = map[string]bool{
	"a": true, "o": true, "as": true, "os": true,
	"da": true, "de": true, "do": true, "das": true, "dos": true, "del": true,
	"la": true, "el": true, "las": true, "los": true, "e": true, "y": true,
}

// titleCase turns "A CORUÑA" into "A Coruña" and leaves mixed-case values
// untouched.
func titleCase(v string) string {
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

// distance returns the great-circle distance in metres.
func distance(lat1, lng1, lat2, lng2 float64) float64 {
	const earthRadius = 6371000.0
	p1, p2 := lat1*math.Pi/180, lat2*math.Pi/180
	dp := p2 - p1
	dl := (lng2 - lng1) * math.Pi / 180
	h := math.Sin(dp/2)*math.Sin(dp/2) + math.Cos(p1)*math.Cos(p2)*math.Sin(dl/2)*math.Sin(dl/2)
	return 2 * earthRadius * math.Asin(math.Sqrt(h))
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// writeCSV writes the canonical dataset schema. Empty fields are left empty:
// no ground truth (notes), no promotional email and no invented website.
func writeCSV(path string, records []record) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	w := csv.NewWriter(f)
	defer w.Flush()
	if err := w.Write(outputColumns); err != nil {
		return err
	}

	for _, r := range records {
		row := make([]string, len(outputColumns))
		set := func(column, value string) {
			for i, c := range outputColumns {
				if c == column {
					row[i] = value
					return
				}
			}
		}
		set("id", r.id)
		set("name", r.name)
		set("category", r.category)
		if r.stars != nil {
			set("stars", strconv.Itoa(*r.stars))
		}
		set("address", r.address)
		set("municipality", r.municipality)
		set("province", r.province)
		set("postal_code", r.postalCode)
		set("country", "ES")
		if r.lat != nil {
			set("lat", strconv.FormatFloat(*r.lat, 'f', 6, 64))
			set("lng", strconv.FormatFloat(*r.lng, 'f', 6, 64))
		}
		set("phone", r.phone)
		set("registry_id", r.id)
		set("id_generated", "false")
		if err := w.Write(row); err != nil {
			return err
		}
	}
	return w.Error()
}
