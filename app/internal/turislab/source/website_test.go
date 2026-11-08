package source

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/datadope/turislab-doctor/internal/turislab/model"
)

func TestNewWebsiteDefaults(t *testing.T) {
	src := NewWebsite(WebsiteConfig{})
	if src.cfg.TimeoutSeconds != 15 {
		t.Errorf("TimeoutSeconds = %d, want 15", src.cfg.TimeoutSeconds)
	}
	if src.cfg.UserAgent != "TurisLab-Doctor/1.0" {
		t.Errorf("UserAgent = %q, want TurisLab-Doctor/1.0", src.cfg.UserAgent)
	}
	if src.cfg.CacheTTLDays != 7 {
		t.Errorf("CacheTTLDays = %d, want 7", src.cfg.CacheTTLDays)
	}
	if !src.healthy {
		t.Error("a fresh source must report healthy")
	}
	if src.Name() != "website" || !src.Enabled() {
		t.Errorf("Name() = %q, Enabled() = %v", src.Name(), src.Enabled())
	}
	if caps := src.Capabilities(); len(caps) != 2 {
		t.Errorf("Capabilities() = %v, want 2 kinds", caps)
	}
}

func TestWebsiteCollectSkipsWithoutWebsite(t *testing.T) {
	src := NewWebsite(WebsiteConfig{})
	sigs, err := src.Collect(context.Background(), &model.Establishment{ID: "est-1"})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if sigs != nil {
		t.Errorf("signals = %v, want nil", sigs)
	}
}

func TestWebsiteCollectOKWithKeyword(t *testing.T) {
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>Aviso: el hotel permanece CERRADO por temporada</html>"))
	}))
	defer srv.Close()

	src := NewWebsite(WebsiteConfig{
		UserAgent: "test-agent",
		Keywords:  []string{"cerrado"},
	})
	sigs, err := src.Collect(context.Background(), &model.Establishment{ID: "est-1", Website: srv.URL})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if gotUA != "test-agent" {
		t.Errorf("User-Agent sent = %q, want test-agent", gotUA)
	}
	if len(sigs) != 2 {
		t.Fatalf("signals = %d, want 2 (status + keyword)", len(sigs))
	}

	status := sigs[0]
	if status.Kind != model.KindWebStatus || status.Family != model.FamilyActivity {
		t.Errorf("status signal kind/family = %v/%v", status.Kind, status.Family)
	}
	if status.RawValue != "ok" || status.Value != 1.0 {
		t.Errorf("status raw/value = %q/%v, want ok/1", status.RawValue, status.Value)
	}
	if status.DedupeKey == "" {
		t.Error("status signal must carry a dedupe key")
	}

	keyword := sigs[1]
	if keyword.Kind != model.KindWebKeyword || keyword.Family != model.FamilyDirectStatus {
		t.Errorf("keyword signal kind/family = %v/%v", keyword.Kind, keyword.Family)
	}
	if keyword.RawValue != "cerrado" || keyword.Value != 0.5 {
		t.Errorf("keyword raw/value = %q/%v, want cerrado/0.5", keyword.RawValue, keyword.Value)
	}
	if strings.Contains(keyword.DedupeKey, "CERRADO") {
		t.Error("dedupe key must use the configured keyword, not the page casing")
	}

	health := src.Health()
	if !health.Healthy || health.LastError != "" {
		t.Errorf("health after success: %+v", health)
	}
	if health.SignalsCollected != 2 {
		t.Errorf("SignalsCollected = %d, want 2", health.SignalsCollected)
	}
	if health.LastRunAt == nil {
		t.Error("LastRunAt must be set after a run")
	}
}

func TestWebsiteCollectKeywordIsCaseInsensitive(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html>CERRADO POR OBRAS</html>"))
	}))
	defer srv.Close()

	src := NewWebsite(WebsiteConfig{Keywords: []string{"Cerrado"}})
	sigs, _ := src.Collect(context.Background(), &model.Establishment{ID: "est-1", Website: srv.URL})
	if len(sigs) != 2 {
		t.Fatalf("signals = %d, want 2", len(sigs))
	}
	if sigs[1].RawValue != "Cerrado" {
		t.Errorf("keyword raw value = %q, want the configured form", sigs[1].RawValue)
	}
}

func TestWebsiteCollectNoKeywordMatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html>Bienvenidos</html>"))
	}))
	defer srv.Close()

	src := NewWebsite(WebsiteConfig{Keywords: []string{"cerrado"}})
	sigs, _ := src.Collect(context.Background(), &model.Establishment{ID: "est-1", Website: srv.URL})
	if len(sigs) != 1 {
		t.Fatalf("signals = %d, want 1 (status only)", len(sigs))
	}
	if sigs[0].RawValue != "ok" {
		t.Errorf("status = %q, want ok", sigs[0].RawValue)
	}
}

func TestWebsiteCollectStatusCodes(t *testing.T) {
	cases := []struct {
		name       string
		code       int
		wantStatus string
		wantValue  float64
	}{
		{"200 ok", 200, "ok", 1.0},
		{"204 ok", 204, "ok", 1.0},
		{"404 gone", 404, "404", 0.0},
		{"500 error", 500, "error", 0.0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.code)
			}))
			defer srv.Close()

			src := NewWebsite(WebsiteConfig{})
			sigs, err := src.Collect(context.Background(), &model.Establishment{ID: "est-1", Website: srv.URL})
			if err != nil {
				t.Fatalf("Collect: %v", err)
			}
			if len(sigs) != 1 {
				t.Fatalf("signals = %d, want 1", len(sigs))
			}
			if sigs[0].RawValue != tc.wantStatus {
				t.Errorf("status = %q, want %q", sigs[0].RawValue, tc.wantStatus)
			}
			if sigs[0].Value != tc.wantValue {
				t.Errorf("value = %v, want %v", sigs[0].Value, tc.wantValue)
			}
		})
	}
}

func TestWebsiteCollectRefusedConnectionIsDown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	url := srv.URL
	srv.Close() // nothing listens anymore

	src := NewWebsite(WebsiteConfig{})
	sigs, err := src.Collect(context.Background(), &model.Establishment{ID: "est-1", Website: url})
	if err != nil {
		t.Fatalf("Collect must not fail for a dead website, got %v", err)
	}
	if len(sigs) != 1 {
		t.Fatalf("signals = %d, want 1", len(sigs))
	}
	if sigs[0].RawValue != "down" || sigs[0].Value != 0.0 {
		t.Errorf("signal = %q/%v, want down/0", sigs[0].RawValue, sigs[0].Value)
	}
	health := src.Health()
	if health.Healthy {
		t.Error("health must turn unhealthy after a request failure")
	}
	if health.LastError == "" {
		t.Error("LastError must be recorded on failure")
	}
	if health.SignalsCollected != 1 {
		t.Errorf("SignalsCollected = %d, want 1 (the down status signal)", health.SignalsCollected)
	}
}

func TestWebsiteCollectBadURL(t *testing.T) {
	src := NewWebsite(WebsiteConfig{})
	sigs, err := src.Collect(context.Background(), &model.Establishment{ID: "est-1", Website: "http://[::1]:namedport"})
	if err != nil {
		t.Fatalf("a bad URL must not fail the collector, got %v", err)
	}
	if sigs != nil {
		t.Errorf("signals = %v, want nil", sigs)
	}
	health := src.Health()
	if health.Healthy {
		t.Error("health must turn unhealthy after a bad URL")
	}
	if health.LastError == "" {
		t.Error("LastError must record the URL error")
	}
}

func TestWebsiteCollectRedirectCap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, r.URL.Path+"x", http.StatusFound)
	}))
	defer srv.Close()

	src := NewWebsite(WebsiteConfig{})
	sigs, _ := src.Collect(context.Background(), &model.Establishment{ID: "est-1", Website: srv.URL + "/go"})
	if len(sigs) != 1 {
		t.Fatalf("signals = %d, want 1", len(sigs))
	}
	if sigs[0].RawValue != "error" {
		t.Errorf("status = %q, want error (redirect loop must not be reported as ok)", sigs[0].RawValue)
	}
}

func TestWebsiteCollectRespectsContextCancellation(t *testing.T) {
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-done
	}))
	defer func() { close(done); srv.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	src := NewWebsite(WebsiteConfig{TimeoutSeconds: 5})
	sigs, err := src.Collect(ctx, &model.Establishment{ID: "est-1", Website: srv.URL})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(sigs) != 1 || sigs[0].RawValue != "error" {
		t.Errorf("canceled request must report an error status, got %+v", sigs)
	}
	if src.Health().Healthy {
		t.Error("health must be unhealthy after a canceled request")
	}
}
