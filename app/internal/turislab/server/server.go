// Package server exposes the TurisLab Doctor API and UI over HTTP.
package server

import (
	"context"
	"encoding/json"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/datadope/turislab-doctor/internal/logx"
	"github.com/datadope/turislab-doctor/internal/turislab/agent"
	"github.com/datadope/turislab-doctor/internal/turislab/artifacts"
	"github.com/datadope/turislab-doctor/internal/turislab/model"
	"github.com/datadope/turislab-doctor/internal/turislab/scan"
)

type scannerIface interface {
	ScanOne(ctx context.Context, id string) error
	Status() scan.ScanStatus
}

type Server struct {
	report  atomic.Pointer[model.Report]
	ui      fs.FS
	log     *logx.Logger
	http    atomic.Pointer[http.Server]
	scanner scannerIface
	metrics http.Handler
	// artifactsDir holds website captures written by the browser MCP. Empty
	// disables the screenshot endpoint.
	artifactsDir string
	// agent, when set, exposes /api/agent/info, /api/agent/tools and
	// /api/agent/chat.
	agent *agent.Service
}

type Option func(*Server)

func WithScanner(s scannerIface) Option {
	return func(srv *Server) {
		srv.scanner = s
	}
}

// WithAgent mounts the inspection assistant endpoints.
func WithAgent(a *agent.Service) Option {
	return func(srv *Server) {
		srv.agent = a
	}
}

// WithArtifactsDir enables GET/HEAD /api/establishments/{id}/screenshot,
// serving <dir>/<id>.png (the files the browser MCP writes from chat).
func WithArtifactsDir(dir string) Option {
	return func(srv *Server) {
		srv.artifactsDir = dir
	}
}

func WithMetrics(h http.Handler) Option {
	return func(srv *Server) {
		srv.metrics = h
	}
}

func New(ui fs.FS, log *logx.Logger, opts ...Option) *Server {
	s := &Server{
		ui:  ui,
		log: log,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *Server) SetReport(r *model.Report) {
	s.report.Store(r)
}

func (s *Server) Serve(ln net.Listener) error {
	hs := &http.Server{
		Handler:      s.Handler(),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	s.http.Store(hs)

	s.log.Info("server", "Listening on %s", ln.Addr().String())
	return hs.Serve(ln)
}

// Handler builds the HTTP routing tree. Exposed for tests.
func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)

	r.Route("/api", func(r chi.Router) {
		r.Get("/health", func(w http.ResponseWriter, req *http.Request) {
			writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": "0.1"})
		})

		r.Get("/report", func(w http.ResponseWriter, req *http.Request) {
			rep := s.report.Load()
			if rep == nil {
				writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "report not ready"})
				return
			}
			writeJSON(w, http.StatusOK, rep)
		})

		r.Get("/report/export", func(w http.ResponseWriter, req *http.Request) {
			rep := s.report.Load()
			if rep == nil {
				writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "report not ready"})
				return
			}
			w.Header().Set("Content-Disposition", "attachment; filename=turislab-report.json")
			writeJSON(w, http.StatusOK, rep)
		})

		r.Get("/summary", func(w http.ResponseWriter, req *http.Request) {
			rep := s.report.Load()
			if rep == nil {
				writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "report not ready"})
				return
			}
			writeJSON(w, http.StatusOK, rep.Summary)
		})

		r.Get("/queue", func(w http.ResponseWriter, req *http.Request) {
			rep := s.report.Load()
			if rep == nil {
				writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "report not ready"})
				return
			}
			writeJSON(w, http.StatusOK, filterQueue(rep.Queue, req.URL.Query()))
		})

		r.Get("/establishments", func(w http.ResponseWriter, req *http.Request) {
			rep := s.report.Load()
			if rep == nil {
				writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "report not ready"})
				return
			}
			writeJSON(w, http.StatusOK, rep.Queue)
		})

		// One queue entry in full: registry fields, score breakdown (judge
		// verdict included) and the primary evidence behind it. The detail
		// screen of the UI reads this; unknown ids are a 404 so the screen
		// can tell "not in the dataset" apart from "report not ready".
		r.Get("/establishments/{id}", func(w http.ResponseWriter, req *http.Request) {
			rep := s.report.Load()
			if rep == nil {
				writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "report not ready"})
				return
			}
			item, ok := findQueueItem(rep.Queue, chi.URLParam(req, "id"))
			if !ok {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "establishment not found"})
				return
			}
			writeJSON(w, http.StatusOK, item)
		})

		// Screenshots are runtime files, not part of the embedded UI build.
		// HEAD lets the detail screen check for a capture cheaply and pick
		// up its Last-Modified date before deciding to show the image.
		r.Get("/establishments/{id}/screenshot", s.serveScreenshot)
		r.Head("/establishments/{id}/screenshot", s.serveScreenshot)

		r.Post("/establishments/{id}/refresh", func(w http.ResponseWriter, req *http.Request) {
			if s.scanner == nil {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "scanner not enabled"})
				return
			}
			id := chi.URLParam(req, "id")
			go func() {
				_ = s.scanner.ScanOne(context.Background(), id)
			}()
			writeJSON(w, http.StatusAccepted, map[string]string{"status": "refresh triggered"})
		})

		r.Get("/scan/status", func(w http.ResponseWriter, req *http.Request) {
			if s.scanner == nil {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "scanner not enabled"})
				return
			}
			writeJSON(w, http.StatusOK, s.scanner.Status())
		})

		r.Get("/pipeline", func(w http.ResponseWriter, req *http.Request) {
			writeJSON(w, http.StatusOK, map[string]any{
				"phases": []map[string]any{
					{"id": 1, "name": "Ingesta y normalización", "what": "Carga los CSV de datasets/ y normaliza coordenadas y nombres.", "artifact": "Establishment"},
					{"id": 2, "name": "Recogida de señales", "what": "Scraping de Google Maps (gosom), OSM, y verificación de web.", "artifact": "Signal"},
					{"id": 3, "name": "Procesamiento de evidencia", "what": "Aplicación de TTL, confianza y decaimiento temporal.", "artifact": "Evidence"},
					{"id": 4, "name": "Motor de puntuación", "what": "Cálculo de closure_risk, quality_risk, quality_delta y prioridad.", "artifact": "Score"},
					{"id": 5, "name": "Veredicto del juez (LLM)", "what": "Un LLM revisa el expediente de evidencia y emite el estado final.", "artifact": "Verdict"},
					{"id": 6, "name": "Gestión de colas y tiers", "what": "Asignación de P0-P3 según prioridad y confianza con histéresis.", "artifact": "QueueItem"},
					{"id": 7, "name": "Exposición y supervisión", "what": "API REST y panel SvelteKit para inspectores.", "artifact": "Report"},
				},
			})
		})

		if s.agent != nil {
			r.Get("/agent/info", s.agent.HandleInfo)
			r.Get("/agent/tools", s.agent.HandleTools)
			// A chat turn runs several model rounds and tool calls: it gets
			// its own, much larger deadline than the rest of the API.
			r.With(middleware.Timeout(agent.ChatTimeout+30*time.Second)).
				Post("/agent/chat", s.agent.HandleChat)
		}

		// Unknown /api endpoints answer JSON 404 instead of falling through
		// to the single page app, which would answer HTML.
		r.NotFound(func(w http.ResponseWriter, req *http.Request) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		})
	})

	if s.metrics != nil {
		r.Handle("/metrics", s.metrics)
	}

	if s.ui != nil {
		files := http.FileServer(http.FS(s.ui))

		// _app/immutable/** holds content-hashed assets: cache them
		// forever. Everything else under _app/ (e.g. version.json) changes
		// with each build.
		r.Handle("/_app/immutable/*", cacheImmutable(files))
		r.Handle("/_app/*", files)
		r.Handle("/*", spaHandler(s.ui))
	}

	return r
}

func (s *Server) Close() error {
	if hs := s.http.Load(); hs != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return hs.Shutdown(ctx)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// serveScreenshot returns the PNG the browser MCP saved for an establishment.
// The id is validated before touching the filesystem: it is the only part of
// the path that comes from the request.
func (s *Server) serveScreenshot(w http.ResponseWriter, req *http.Request) {
	id := chi.URLParam(req, "id")
	if !artifacts.ValidID(id) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid establishment id"})
		return
	}
	if s.artifactsDir == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "screenshots not enabled"})
		return
	}

	file, err := artifacts.ScreenshotPath(s.artifactsDir, id)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid establishment id"})
		return
	}
	st, err := os.Stat(file)
	if err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "screenshot not found"})
		} else {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "cannot read screenshot"})
		}
		return
	}
	if !st.Mode().IsRegular() {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "screenshot not found"})
		return
	}

	// A recapture overwrites the same file; keep proxies and the browser from
	// showing the old photo. ServeFile handles HEAD and Last-Modified.
	w.Header().Set("Cache-Control", "no-store")
	http.ServeFile(w, req, file)
}

// filterQueue applies the /api/queue query params: tier, category,
// municipality, q (case-insensitive substring on the name or the id) and the
// limit/offset pagination window. The queue arrives ordered by rank.
func filterQueue(queue []model.QueueItem, params url.Values) []model.QueueItem {
	tier := params.Get("tier")
	category := params.Get("category")
	municipality := params.Get("municipality")
	q := strings.ToLower(params.Get("q"))

	var filtered = make([]model.QueueItem, 0, len(queue))
	for _, item := range queue {
		if tier != "" && (item.Score == nil || string(item.Score.Tier) != tier) {
			continue
		}
		if category != "" && string(item.Establishment.Category) != category {
			continue
		}
		if municipality != "" && !strings.EqualFold(item.Establishment.Municipality, municipality) {
			continue
		}
		if q != "" &&
			!strings.Contains(strings.ToLower(item.Establishment.Name), q) &&
			!strings.Contains(strings.ToLower(item.Establishment.ID), q) {
			continue
		}
		filtered = append(filtered, item)
	}

	offset, _ := strconv.Atoi(params.Get("offset"))
	limit, _ := strconv.Atoi(params.Get("limit"))
	if limit <= 0 {
		limit = 50
	}
	if offset > len(filtered) {
		offset = len(filtered)
	}
	end := min(offset+limit, len(filtered))
	return filtered[offset:end]
}

// findQueueItem returns the queue entry whose establishment carries id. The
// queue holds one entry per establishment of the dataset, so a linear scan
// costs nothing next to encoding the response.
func findQueueItem(queue []model.QueueItem, id string) (model.QueueItem, bool) {
	for _, item := range queue {
		if item.Establishment != nil && item.Establishment.ID == id {
			return item, true
		}
	}
	return model.QueueItem{}, false
}

// spaHandler serves the static files of the UI bundle and falls back to
// index.html for client-side routes, so the single page app keeps working.
//
// adapter-static writes prerendered routes as <route>.html (e.g.
// turislab.html, how-it-works.html), so an extensionless path is resolved
// against that file first: requesting /turislab/ must not answer the root
// shell, because the prerendered HTML references its assets relative to the
// page and would otherwise fetch them from /turislab/_app/... .
//
// A missing path with an extension is a missing asset: it must answer 404,
// never the HTML fallback. Serving index.html for a .js module is what the
// browser reports as corrupted content.
func spaHandler(fsys fs.FS) http.Handler {
	files := http.FileServer(http.FS(fsys))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" || name == "." {
			name = "index.html"
		}
		served, ok := resolveUIPath(fsys, name)
		if !ok {
			http.NotFound(w, r)
			return
		}
		if served != name {
			r = r.Clone(r.Context())
			r.URL.Path = "/" + served
			// http.FileServer redirects any path ending in /index.html to
			// its directory, which would bounce the SPA fallback; serve the
			// directory path instead (root for the shell).
			if strings.HasSuffix(r.URL.Path, "/index.html") {
				r.URL.Path = strings.TrimSuffix(r.URL.Path, "index.html")
			}
		}
		if strings.HasSuffix(served, ".html") {
			// The entry point must not be cached: it references the hashed
			// assets of the build it was produced with.
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	})
}

// resolveUIPath maps a request path to a file of the bundle. Routes without
// an extension fall back to the SPA shell (index.html, or the tracked
// placeholder when the real UI has not been built).
func resolveUIPath(fsys fs.FS, name string) (string, bool) {
	if isUIFile(fsys, name) {
		return name, true
	}
	if isUIFile(fsys, name+".html") {
		return name + ".html", true
	}
	if index := path.Join(name, "index.html"); isUIFile(fsys, index) {
		return index, true
	}
	if path.Ext(name) == "" {
		if isUIFile(fsys, "index.html") {
			return "index.html", true
		}
		if isUIFile(fsys, "placeholder.html") {
			return "placeholder.html", true
		}
	}
	return "", false
}

func isUIFile(fsys fs.FS, name string) bool {
	stat, err := fs.Stat(fsys, name)
	return err == nil && !stat.IsDir()
}

func cacheImmutable(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		next.ServeHTTP(w, r)
	})
}
