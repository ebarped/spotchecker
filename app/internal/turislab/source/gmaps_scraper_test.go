package source

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/datadope/turislab-doctor/internal/turislab/model"
)

// classifyStatus must distinguish a permanently closed place from one that is
// merely outside its opening hours. gosom reports the latter as
// "Closed ⋅ Opens 12:30 pm Tue", and matching the substring "closed" would
// flag every restaurant between services as permanently closed.
func TestClassifyStatus(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want placeStatus
	}{
		{"empty", "", placeOpen},
		{"literal closed enum", "CLOSED", placePermanentlyClosed},
		{"literal closed lowercased", "closed", placePermanentlyClosed},
		{"localized closed enum", "CERRADO", placePermanentlyClosed},
		{"localized closed enum padded", "  CERRADO\n", placePermanentlyClosed},
		{"permanently closed prose", "Permanently closed", placePermanentlyClosed},
		{"localized permanently closed prose", "Cerrado permanentemente", placePermanentlyClosed},
		{"outside opening hours is open", "Closed ⋅ Opens 12:30 pm Tue", placeOpen},
		{"outside opening hours in Spanish is open", "Cerrado ⋅ Abre a las 13:00", placeOpen},
		{"open with closing time is open", "Open ⋅ Closes 11 pm", placeOpen},
		{"tagline is open", "Destacada colección de arte europeo", placeOpen},
		{"price tag is open", "819 €", placeOpen},
		{"temporarily closed", "Temporarily closed", placeTemporarilyClosed},
		{"localized temporarily closed", "Cerrado temporalmente", placeTemporarilyClosed},
		{"whitespace around the enum", "  CLOSED \n", placePermanentlyClosed},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyStatus(tc.raw); got != tc.want {
				t.Errorf("classifyStatus(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}

// A trimmed-down record copied from a real gosom run against Google Maps. It
// documents the wire shape: the name is "title", the status may be a tagline,
// and the JSON is one object per line.
const realGosomRecord = `{"longitude":-3.6921271,"input_id":"7e35dad1","link":"https://www.google.com/maps/place/x","cid":"1194844223299225690","title":"Museo Nacional del Prado","categories":["Museo de arte"],"category":"Museo de arte","address":"Retiro, 28014 Madrid","web_site":"https://www.museodelprado.es/","phone":"913 30 28 00","review_count":158316,"review_rating":4.7,"latitude":40.413781799999995,"longtitude":-3.6921271,"status":"Destacada colección de arte europeo","place_id":"ChIJ7aLYZp0oQg0RWoitk33wlBA"}`

func TestParsePlacesRealRecord(t *testing.T) {
	places, err := parsePlaces([]byte(realGosomRecord))
	if err != nil {
		t.Fatalf("parsePlaces: %v", err)
	}
	if len(places) != 1 {
		t.Fatalf("places = %d, want 1", len(places))
	}

	p := places[0]
	if p.Title != "Museo Nacional del Prado" {
		t.Errorf("title = %q, want the business name", p.Title)
	}
	if p.PlaceID != "ChIJ7aLYZp0oQg0RWoitk33wlBA" {
		t.Errorf("place_id = %q", p.PlaceID)
	}
	if p.Rating != 4.7 {
		t.Errorf("rating = %v, want 4.7", p.Rating)
	}
	if p.ReviewCount != 158316 {
		t.Errorf("review_count = %d, want 158316", p.ReviewCount)
	}
}

func TestParsePlacesNDJSON(t *testing.T) {
	body := realGosomRecord + "\n" + `{"title":"Segundo","place_id":"p2","status":"CLOSED"}` + "\n"
	places, err := parsePlaces([]byte(body))
	if err != nil {
		t.Fatalf("parsePlaces: %v", err)
	}
	if len(places) != 2 {
		t.Fatalf("places = %d, want 2", len(places))
	}
	if places[1].Title != "Segundo" || classifyStatus(places[1].Status) != placePermanentlyClosed {
		t.Errorf("second place = %+v", places[1])
	}
}

func TestParsePlacesArray(t *testing.T) {
	body := `[{"title":"Uno","place_id":"p1"},{"title":"Dos","place_id":"p2"}]`
	places, err := parsePlaces([]byte(body))
	if err != nil {
		t.Fatalf("parsePlaces: %v", err)
	}
	if len(places) != 2 || places[0].Title != "Uno" {
		t.Errorf("places = %+v", places)
	}
}

func TestParsePlacesPrettyPrintedObject(t *testing.T) {
	body := "{\n  \"title\": \"Formateado\",\n  \"place_id\": \"p1\"\n}\n"
	places, err := parsePlaces([]byte(body))
	if err != nil {
		t.Fatalf("parsePlaces: %v", err)
	}
	if len(places) != 1 || places[0].Title != "Formateado" {
		t.Errorf("places = %+v", places)
	}
}

func TestParsePlacesEmpty(t *testing.T) {
	for _, body := range []string{"", "   \n  "} {
		places, err := parsePlaces([]byte(body))
		if err != nil {
			t.Fatalf("parsePlaces(%q): %v", body, err)
		}
		if len(places) != 0 {
			t.Errorf("parsePlaces(%q) = %+v, want none", body, places)
		}
	}
}

// fakeGosom writes a fixed NDJSON record to the -results path and fails unless
// the caller passed the flags the real CLI expects. It exercises the whole
// Collect path: argument building, process execution and parsing.
func fakeGosom(t *testing.T, record string) string {
	t.Helper()

	dir := t.TempDir()
	script := filepath.Join(dir, "fake-gosom.sh")
	body := `#!/bin/sh
out=""
saw_json=0
saw_input=0
while [ $# -gt 0 ]; do
  case "$1" in
    -results) out="$2"; shift 2 ;;
    -json) saw_json=1; shift ;;
    -input) saw_input=1; shift 2 ;;
    *) shift ;;
  esac
done
if [ -z "$out" ] || [ "$saw_json" -ne 1 ] || [ "$saw_input" -ne 1 ]; then
  echo "missing expected flags" >&2
  exit 2
fi
cat > "$out" <<'JSON'
` + record + `
JSON
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}
	return script
}

func TestCollectClosedPlaceFromGosom(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a POSIX shell script")
	}

	record := `{"title":"Punto MX","place_id":"p-closed","status":"CERRADO","review_rating":4.2,"review_count":815}`
	src := NewGMapsScraper(GMapsScraperConfig{
		Provider:       "gosom",
		Binary:         fakeGosom(t, record),
		TimeoutSeconds: 10,
		MaxPlaces:      1,
		Depth:          1,
		Language:       "es",
		Headless:       true,
	})

	signals, err := src.Collect(context.Background(), &model.Establishment{ID: "rest-002", Name: "La Tasca Cerrada"})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	var status *model.Signal
	for _, sig := range signals {
		if sig.Kind == model.KindBusinessStatus {
			status = sig
		}
	}
	if status == nil {
		t.Fatalf("no business status signal in %+v", signals)
	}
	if status.RawValue != "permanently_closed" {
		t.Errorf("raw value = %q, want permanently_closed", status.RawValue)
	}
	if status.Value != 1.0 {
		t.Errorf("value = %v, want 1.0", status.Value)
	}
	if !src.Health().Healthy {
		t.Error("source marked unhealthy after a successful run")
	}
}

func TestCollectOpenPlaceIsNotFlaggedClosed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a POSIX shell script")
	}

	// This is the status shape that used to trigger a false positive: a place
	// outside its opening hours is open, not permanently closed.
	record := `{"title":"Restaurante Botin","place_id":"p-open","status":"Closed ⋅ Opens 1 pm","review_rating":4.5,"review_count":9000}`
	src := NewGMapsScraper(GMapsScraperConfig{
		Provider:       "gosom",
		Binary:         fakeGosom(t, record),
		TimeoutSeconds: 10,
		MaxPlaces:      1,
		Depth:          1,
		Language:       "es",
		Headless:       true,
	})

	signals, err := src.Collect(context.Background(), &model.Establishment{ID: "rest-001", Name: "Restaurante Botin"})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	for _, sig := range signals {
		if sig.Kind == model.KindBusinessStatus {
			if sig.RawValue != "open" {
				t.Errorf("raw value = %q, want open for a place merely outside opening hours", sig.RawValue)
			}
			if sig.Value != 0 {
				t.Errorf("value = %v, want 0", sig.Value)
			}
			return
		}
	}
	t.Fatalf("no business status signal in %+v", signals)
}

func TestCollectFailingBinaryIsGraceful(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a POSIX shell script")
	}

	dir := t.TempDir()
	script := filepath.Join(dir, "broken.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho boom >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}

	src := NewGMapsScraper(GMapsScraperConfig{
		Provider:       "gosom",
		Binary:         script,
		TimeoutSeconds: 10,
		MaxPlaces:      1,
		Depth:          1,
		Language:       "es",
	})

	signals, err := src.Collect(context.Background(), &model.Establishment{ID: "e1", Name: "X"})
	if err != nil {
		t.Errorf("Collect returned an error, want graceful handling: %v", err)
	}
	if len(signals) != 0 {
		t.Errorf("signals = %+v, want none from a failing binary", signals)
	}
	if src.Health().Healthy {
		t.Error("source still healthy after a failed run")
	}
}
