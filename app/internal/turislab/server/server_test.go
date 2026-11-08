package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/datadope/turislab-doctor/internal/logx"
	"github.com/datadope/turislab-doctor/internal/turislab/model"
	"github.com/datadope/turislab-doctor/internal/turislab/scan"
)

// decodeJSON unmarshals a handler response; a body that does not parse is a
// broken endpoint, so any test hitting it may fail here.
func decodeJSON[T any](t *testing.T, body []byte) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("bad JSON %q: %v", body, err)
	}
	return out
}

func newTestServer(t *testing.T, opts ...Option) *Server {
	t.Helper()
	log := logx.New(&strings.Builder{})
	return New(fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html>ui</html>")}}, log, opts...)
}

func get(t *testing.T, srv *Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestHealth(t *testing.T) {
	srv := newTestServer(t)
	rec := get(t, srv, "/api/health")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var out struct {
		Status  string `json:"status"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("bad JSON: %v", err)
	}
	if out.Status != "ok" {
		t.Errorf("status = %q, want ok", out.Status)
	}
}

func TestReportNotReadyUntilSet(t *testing.T) {
	srv := newTestServer(t)

	if rec := get(t, srv, "/api/report"); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("/api/report status = %d, want 503 before the first report", rec.Code)
	}
	if rec := get(t, srv, "/api/queue"); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("/api/queue status = %d, want 503 before the first report", rec.Code)
	}
	if rec := get(t, srv, "/api/summary"); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("/api/summary status = %d, want 503 before the first report", rec.Code)
	}
}

func TestReportAndQueueAfterSet(t *testing.T) {
	srv := newTestServer(t)

	score := model.NewScore("rest-002")
	score.Priority = 92
	score.Confidence = 0.8
	score.AssignTier(model.DefaultTierConfig())

	srv.SetReport(&model.Report{
		GeneratedAt:        time.Now(),
		EstablishmentCount: 1,
		Queue: []model.QueueItem{{
			Rank:          1,
			Establishment: &model.Establishment{ID: "rest-002", Name: "La Tasca Cerrada"},
			Score:         score,
		}},
		Summary: model.ReportSummary{Total: 1, P0: 1},
	})

	rec := get(t, srv, "/api/report")
	if rec.Code != http.StatusOK {
		t.Fatalf("/api/report status = %d, want 200", rec.Code)
	}
	rep := decodeJSON[model.Report](t, rec.Body.Bytes())
	if len(rep.Queue) != 1 || rep.Queue[0].Establishment.ID != "rest-002" {
		t.Errorf("queue = %+v, want one item for rest-002", rep.Queue)
	}

	// The export endpoint serves the same payload as an attachment.
	rec = get(t, srv, "/api/report/export")
	if rec.Code != http.StatusOK {
		t.Fatalf("/api/report/export status = %d, want 200", rec.Code)
	}
	if disp := rec.Header().Get("Content-Disposition"); !strings.Contains(disp, "attachment") {
		t.Errorf("content-disposition = %q, want attachment", disp)
	}
}

func TestQueueFilters(t *testing.T) {
	srv := newTestServer(t)

	makeItem := func(rank int, id, name, tier string) model.QueueItem {
		sc := model.NewScore(id)
		sc.Tier = model.TierLabel(tier)
		return model.QueueItem{
			Rank:          rank,
			Establishment: &model.Establishment{ID: id, Name: name},
			Score:         sc,
		}
	}

	srv.SetReport(&model.Report{
		Queue: []model.QueueItem{
			makeItem(1, "rest-002", "La Tasca Cerrada", "P0"),
			makeItem(2, "hotel-002", "Hostal El Pinar", "P1"),
			makeItem(3, "rest-001", "Restaurante El Ancla", "P3"),
		},
	})

	// Filter by tier.
	rec := get(t, srv, "/api/queue?tier=P0")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	items := decodeJSON[[]model.QueueItem](t, rec.Body.Bytes())
	if len(items) != 1 || items[0].Establishment.ID != "rest-002" {
		t.Errorf("tier filter returned %+v, want only rest-002", items)
	}

	// Text search on the name.
	rec = get(t, srv, "/api/queue?q=tasca")
	items = decodeJSON[[]model.QueueItem](t, rec.Body.Bytes())
	if len(items) != 1 || items[0].Establishment.ID != "rest-002" {
		t.Errorf("text filter returned %+v, want only rest-002", items)
	}

	// Pagination.
	rec = get(t, srv, "/api/queue?limit=1&offset=1")
	items = decodeJSON[[]model.QueueItem](t, rec.Body.Bytes())
	if len(items) != 1 || items[0].Establishment.ID != "hotel-002" {
		t.Errorf("pagination returned %+v, want the second item", items)
	}
}

func TestScanStatusWithoutScanner(t *testing.T) {
	srv := newTestServer(t)

	if rec := get(t, srv, "/api/scan/status"); rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 without a scanner", rec.Code)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/establishments/e1/refresh", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("refresh status = %d, want 404 without a scanner", rec.Code)
	}
}

type fakeScanner struct {
	scanned []string
}

func (f *fakeScanner) ScanOne(_ context.Context, id string) error {
	f.scanned = append(f.scanned, id)
	return nil
}

func (f *fakeScanner) Status() scan.ScanStatus {
	return scan.ScanStatus{Running: true}
}

func TestRefreshTriggersScanner(t *testing.T) {
	fake := &fakeScanner{}
	srv := newTestServer(t, WithScanner(fake))

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/establishments/rest-002/refresh", nil))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", rec.Code)
	}

	rec = get(t, srv, "/api/scan/status")
	st := decodeJSON[scan.ScanStatus](t, rec.Body.Bytes())
	if !st.Running {
		t.Errorf("running = false, want true from the fake scanner")
	}
}

func TestPipelinePhases(t *testing.T) {
	srv := newTestServer(t)

	rec := get(t, srv, "/api/pipeline")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var out struct {
		Phases []struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		} `json:"phases"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("bad JSON: %v", err)
	}
	if len(out.Phases) != 6 {
		t.Errorf("phases = %d, want 6", len(out.Phases))
	}
}

func TestSPAFallback(t *testing.T) {
	srv := newTestServer(t)

	rec := get(t, srv, "/some/client/route")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 served from index.html", rec.Code)
	}
	if body := rec.Body.String(); body != "<html>ui</html>" {
		t.Errorf("body = %q, want the index.html contents", body)
	}
}

// uiBuildFixture mirrors the real adapter-static output: prerendered routes
// as <route>.html plus hashed assets under _app/immutable.
func uiBuildFixture() fstest.MapFS {
	return fstest.MapFS{
		"index.html":                        &fstest.MapFile{Data: []byte("root shell")},
		"turislab.html":                     &fstest.MapFile{Data: []byte("turislab shell")},
		"how-it-works.html":                 &fstest.MapFile{Data: []byte("how it works shell")},
		"placeholder.html":                  &fstest.MapFile{Data: []byte("placeholder page")},
		"robots.txt":                        &fstest.MapFile{Data: []byte("User-agent: *")},
		"_app/version.json":                 &fstest.MapFile{Data: []byte(`{"version":"1"}`)},
		"_app/immutable/entry/start.ABC.js": &fstest.MapFile{Data: []byte("console.log(1)")},
	}
}

func newUIServer(t *testing.T) *Server {
	t.Helper()
	return New(uiBuildFixture(), logx.New(&strings.Builder{}))
}

// TestPrerenderedRoute serves the page the client-side route expects instead
// of the root shell: with relative asset paths, /turislab/ must answer
// turislab.html or the browser fetches its assets from /turislab/_app/... .
func TestPrerenderedRoute(t *testing.T) {
	srv := newUIServer(t)

	for _, tc := range []struct{ path, want string }{
		{"/turislab", "turislab shell"},
		{"/turislab/", "turislab shell"},
		{"/how-it-works", "how it works shell"},
		{"/", "root shell"},
	} {
		rec := get(t, srv, tc.path)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s: status = %d, want 200", tc.path, rec.Code)
			continue
		}
		if body := rec.Body.String(); body != tc.want {
			t.Errorf("GET %s: body = %q, want %q", tc.path, body, tc.want)
		}
	}
}

// TestMissingAssetIs404 guards against the HTML fallback answering for JS and
// CSS, which the browser reports as corrupted content.
func TestMissingAssetIs404(t *testing.T) {
	srv := newUIServer(t)

	for _, p := range []string{
		"/_app/immutable/entry/missing.js",
		"/_app/immutable/assets/missing.css",
		"/turislab/_app/immutable/entry/start.ABC.js",
	} {
		if rec := get(t, srv, p); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s: status = %d, want 404", p, rec.Code)
		}
	}
}

func TestUICacheHeaders(t *testing.T) {
	srv := newUIServer(t)

	rec := get(t, srv, "/turislab/")
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("prerendered page cache-control = %q, want no-cache", cc)
	}

	rec = get(t, srv, "/_app/immutable/entry/start.ABC.js")
	if rec.Code != http.StatusOK {
		t.Fatalf("asset status = %d, want 200", rec.Code)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "public, max-age=31536000, immutable" {
		t.Errorf("immutable asset cache-control = %q", cc)
	}

	rec = get(t, srv, "/_app/version.json")
	if cc := rec.Header().Get("Cache-Control"); strings.Contains(cc, "immutable") {
		t.Errorf("version.json cache-control = %q, must not be immutable", cc)
	}
}

// TestUnknownAPIIsJSON makes sure the reserved /api namespace never answers
// the SPA's HTML.
func TestUnknownAPIIsJSON(t *testing.T) {
	srv := newUIServer(t)

	rec := get(t, srv, "/api/does-not-exist")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("content-type = %q, want application/json", ct)
	}
}
