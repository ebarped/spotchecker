package source

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/datadope/turislab-doctor/internal/turislab/model"
)

func TestNewOSMDefaults(t *testing.T) {
	src := NewOSM(OSMConfig{})
	if src.cfg.OverpassURL != "https://overpass-api.de/api/interpreter" {
		t.Errorf("OverpassURL = %q, want the public endpoint", src.cfg.OverpassURL)
	}
	if src.cfg.TimeoutSeconds != 30 {
		t.Errorf("TimeoutSeconds = %d, want 30", src.cfg.TimeoutSeconds)
	}
	if src.cfg.CacheTTLDays != 14 {
		t.Errorf("CacheTTLDays = %d, want 14", src.cfg.CacheTTLDays)
	}
	if src.cfg.MaxElements != 50 {
		t.Errorf("MaxElements = %d, want 50", src.cfg.MaxElements)
	}
	if src.cfg.UserAgent != "TurisLab-Doctor/1.0" {
		t.Errorf("UserAgent = %q, want TurisLab-Doctor/1.0", src.cfg.UserAgent)
	}
	if src.Name() != "osm" || !src.Enabled() {
		t.Errorf("Name() = %q, Enabled() = %v", src.Name(), src.Enabled())
	}
	if caps := src.Capabilities(); len(caps) != 1 || caps[0] != model.KindOSMTag {
		t.Errorf("Capabilities() = %v, want [osm_tag]", caps)
	}
}

// osmEst returns an establishment with coordinates at (41.5, -1.2).
func osmEst(id, name string) *model.Establishment {
	lat, lng := 41.5, -1.2
	return &model.Establishment{ID: id, Name: name, Lat: &lat, Lng: &lng}
}

// overpassServer spins up a fake Overpass API returning the given elements.
func overpassServer(t *testing.T, elements string) *httptest.Server {
	t.Helper()
	body := `{"version":0.6,"elements":` + elements + `}`
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm: %v", err)
		}
		data := r.FormValue("data")
		if !strings.Contains(data, "around:100") {
			t.Errorf("query %q must search around the establishment", data)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
}

func el(tags map[string]string) string {
	b, _ := json.Marshal(map[string]any{"type": "node", "id": 1, "tags": tags})
	return string(b)
}

func TestOSMCollectSkipsWithoutCoordinates(t *testing.T) {
	src := NewOSM(OSMConfig{})
	sigs, err := src.Collect(context.Background(), &model.Establishment{ID: "est-1"})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if sigs != nil {
		t.Errorf("signals = %v, want nil", sigs)
	}
	if src.Health().LastRunAt != nil {
		t.Error("a skipped collect must not be recorded as a run")
	}
}

func TestOSMCollectTagSignals(t *testing.T) {
	cases := []struct {
		name    string
		tags    map[string]string
		wantRaw string
		wantVal float64
		wantFam model.SignalFamily
	}{
		{"disused amenity", map[string]string{"disused:amenity": "restaurant"}, "disused", 0.6, model.FamilyDirectStatus},
		{"disused tourism", map[string]string{"disused:tourism": "hotel"}, "disused", 0.6, model.FamilyDirectStatus},
		{"explicit closed", map[string]string{"closed": "yes"}, "closed", 0.6, model.FamilyDirectStatus},
		{"vacant shop", map[string]string{"shop": "vacant"}, "vacant", 0.5, model.FamilyDirectStatus},
		{"active with hours", map[string]string{"name": "Hotel Sol y Playa", "opening_hours": "Mo-Su 09:00-18:00"}, "found_active", 1.0, model.FamilyActivity},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := overpassServer(t, "["+el(tc.tags)+"]")
			defer srv.Close()

			src := NewOSM(OSMConfig{OverpassURL: srv.URL})
			sigs, err := src.Collect(context.Background(), osmEst("est-1", "Sol y Playa"))
			if err != nil {
				t.Fatalf("Collect: %v", err)
			}
			if len(sigs) != 1 {
				t.Fatalf("signals = %d, want 1: %+v", len(sigs), sigs)
			}
			sig := sigs[0]
			if sig.Kind != model.KindOSMTag {
				t.Errorf("Kind = %v, want %v", sig.Kind, model.KindOSMTag)
			}
			if sig.RawValue != tc.wantRaw {
				t.Errorf("RawValue = %q, want %q", sig.RawValue, tc.wantRaw)
			}
			if sig.Value != tc.wantVal {
				t.Errorf("Value = %v, want %v", sig.Value, tc.wantVal)
			}
			if sig.Family != tc.wantFam {
				t.Errorf("Family = %v, want %v", sig.Family, tc.wantFam)
			}
			if sig.TTLDays != 14 {
				t.Errorf("TTLDays = %d, want the configured 14", sig.TTLDays)
			}
			if sig.DedupeKey == "" {
				t.Error("DedupeKey must be set")
			}
		})
	}
}

func TestOSMCollectActiveNameMatchIsCaseInsensitive(t *testing.T) {
	srv := overpassServer(t, "["+el(map[string]string{"name": "HOTEL SOL Y PLAYA", "opening_hours": "24/7"})+"]")
	defer srv.Close()

	src := NewOSM(OSMConfig{OverpassURL: srv.URL})
	sigs, _ := src.Collect(context.Background(), osmEst("est-1", "Sol y Playa"))
	if len(sigs) != 1 || sigs[0].RawValue != "found_active" {
		t.Errorf("signals = %+v, want a single found_active", sigs)
	}
}

func TestOSMCollectActiveNameMismatchIsIgnored(t *testing.T) {
	srv := overpassServer(t, "["+el(map[string]string{"name": "Bar La Esquina", "opening_hours": "24/7"})+"]")
	defer srv.Close()

	src := NewOSM(OSMConfig{OverpassURL: srv.URL})
	sigs, _ := src.Collect(context.Background(), osmEst("est-1", "Sol y Playa"))
	if len(sigs) != 0 {
		t.Errorf("signals = %+v, want none (elements exist but none match)", sigs)
	}
}

func TestOSMCollectMultipleElementsEmitMultipleSignals(t *testing.T) {
	elements := "[" +
		el(map[string]string{"disused:amenity": "restaurant"}) + "," +
		el(map[string]string{"closed": "yes"}) + "]"
	srv := overpassServer(t, elements)
	defer srv.Close()

	src := NewOSM(OSMConfig{OverpassURL: srv.URL})
	sigs, _ := src.Collect(context.Background(), osmEst("est-1", "Sol y Playa"))
	if len(sigs) != 2 {
		t.Fatalf("signals = %d, want 2", len(sigs))
	}
	if sigs[0].DedupeKey == sigs[1].DedupeKey {
		t.Error("disused and closed signals must have different dedupe keys")
	}
}

func TestOSMCollectEmptyResponseIsNotFound(t *testing.T) {
	srv := overpassServer(t, "[]")
	defer srv.Close()

	src := NewOSM(OSMConfig{OverpassURL: srv.URL})
	sigs, err := src.Collect(context.Background(), osmEst("est-1", "Sol y Playa"))
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(sigs) != 1 {
		t.Fatalf("signals = %d, want 1", len(sigs))
	}
	sig := sigs[0]
	if sig.RawValue != "not_found" || sig.Value != 0.0 {
		t.Errorf("signal = %q/%v, want not_found/0", sig.RawValue, sig.Value)
	}
	if sig.Confidence != 0.40 || sig.Weight != 0.20 {
		t.Errorf("confidence/weight = %v/%v, want 0.40/0.20", sig.Confidence, sig.Weight)
	}
}

func TestOSMCollectInvalidJSONIsUnhealthy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html>overpass busy</html>"))
	}))
	defer srv.Close()

	src := NewOSM(OSMConfig{OverpassURL: srv.URL})
	sigs, err := src.Collect(context.Background(), osmEst("est-1", "Sol y Playa"))
	if err != nil {
		t.Fatalf("Collect must not fail for a malformed response, got %v", err)
	}
	if sigs != nil {
		t.Errorf("signals = %v, want nil", sigs)
	}
	health := src.Health()
	if health.Healthy {
		t.Error("health must be unhealthy after a parse failure")
	}
	if health.LastError == "" {
		t.Error("LastError must record the parse failure")
	}
}

func TestOSMCollectDeadEndpointIsUnhealthy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	url := srv.URL
	srv.Close()

	src := NewOSM(OSMConfig{OverpassURL: url})
	sigs, err := src.Collect(context.Background(), osmEst("est-1", "Sol y Playa"))
	if err != nil {
		t.Fatalf("Collect must not fail for a dead endpoint, got %v", err)
	}
	if sigs != nil {
		t.Errorf("signals = %v, want nil", sigs)
	}
	health := src.Health()
	if health.Healthy {
		t.Error("health must be unhealthy after a transport failure")
	}
	if health.LastError == "" {
		t.Error("LastError must record the transport failure")
	}
}
