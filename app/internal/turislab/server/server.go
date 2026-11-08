// Package server exposes the TurisLab Doctor API and UI over HTTP.
package server

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/datadope/turislab-doctor/internal/logx"
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
}

type Option func(*Server)

func WithScanner(s scannerIface) Option {
	return func(srv *Server) {
		srv.scanner = s
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

		r.Post("/establishments/{id}/refresh", func(w http.ResponseWriter, req *http.Request) {
			if s.scanner == nil {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "scanner not enabled"})
				return
			}
			id := chi.URLParam(req, "id")
			go func() {
				_ = s.scanner.ScanOne(context.Background(), id)
			}()
			w.WriteHeader(http.StatusAccepted)
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
					{"id": 1, "name": "Ingesta y normalización", "what": "Carga base-de-datos.json y normaliza coordenadas y nombres.", "artifact": "Establishment"},
					{"id": 2, "name": "Recogida de señales", "what": "Scraping de Google Maps (gosom), OSM, y verificación de web.", "artifact": "Signal"},
					{"id": 3, "name": "Procesamiento de evidencia", "what": "Aplicación de TTL, confianza y decaimiento temporal.", "artifact": "Evidence"},
					{"id": 4, "name": "Motor de puntuación", "what": "Cálculo de closure_risk, quality_risk, quality_delta y prioridad.", "artifact": "Score"},
					{"id": 5, "name": "Gestión de colas y tiers", "what": "Asignación de P0-P3 según prioridad y confianza con histéresis.", "artifact": "QueueItem"},
					{"id": 6, "name": "Exposición y supervisión", "what": "API REST y panel SvelteKit para inspectores.", "artifact": "Report"},
				},
			})
		})
	})

	if s.metrics != nil {
		r.Handle("/metrics", s.metrics)
	}

	if s.ui != nil {
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

// filterQueue applies the /api/queue query params: tier, category,
// municipality, q (case-insensitive substring on the name) and the
// limit/offset pagination window. The queue arrives ordered by rank.
func filterQueue(queue []model.QueueItem, params url.Values) []model.QueueItem {
	tier := params.Get("tier")
	category := params.Get("category")
	municipality := params.Get("municipality")
	q := strings.ToLower(params.Get("q"))

	var filtered []model.QueueItem
	for _, item := range queue {
		if tier != "" && string(item.Score.Tier) != tier {
			continue
		}
		if category != "" && string(item.Establishment.Category) != category {
			continue
		}
		if municipality != "" && !strings.EqualFold(item.Establishment.Municipality, municipality) {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(item.Establishment.Name), q) {
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

func spaHandler(fsys fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := path.Clean(r.URL.Path)
		if p == "/" {
			p = "/index.html"
		}
		p = strings.TrimPrefix(p, "/")

		f, err := fsys.Open(p)
		if err != nil {
			f, err = fsys.Open("index.html")
			if err != nil {
				http.Error(w, "SPA index not found", http.StatusNotFound)
				return
			}
		}
		defer f.Close()

		stat, err := f.Stat()
		if err != nil || stat.IsDir() {
			f2, err2 := fsys.Open("index.html")
			if err2 != nil {
				http.Error(w, "SPA index not found", http.StatusNotFound)
				return
			}
			defer f2.Close()
			http.ServeContent(w, r, "index.html", time.Time{}, f2.(io.ReadSeeker))
			return
		}

		http.ServeContent(w, r, stat.Name(), stat.ModTime(), f.(io.ReadSeeker))
	})
}
