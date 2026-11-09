// Package scan implements the sequential establishment scanner.
package scan

import (
	"context"
	"math/rand"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/datadope/turislab-doctor/internal/logx"
	"github.com/datadope/turislab-doctor/internal/turislab/model"
	"github.com/datadope/turislab-doctor/internal/turislab/scoring"
)

type Store interface {
	ListEstablishments(ctx context.Context) ([]*model.Establishment, error)
	SaveSignal(ctx context.Context, s *model.Signal) error
	ListSignals(ctx context.Context, establishmentID string, since time.Time) ([]*model.Signal, error)
	SaveScore(ctx context.Context, s *model.Score) error
	GetScore(ctx context.Context, establishmentID string) (*model.Score, error)
	GetCursor(ctx context.Context) (string, error)
	SetCursor(ctx context.Context, establishmentID string) error
}

type Source interface {
	Name() string
	Enabled() bool
	Collect(ctx context.Context, est *model.Establishment) ([]*model.Signal, error)
}

type ScanConfig struct {
	TriggerInterval      time.Duration
	TargetsPerTick       int
	Jitter               float64
	MaxConcurrentSources int
	BackoffOn429         time.Duration
	MaxAttempts          int
	StaleAfterDays       int

	// PauseBetween is the delay inserted between two establishments in
	// RunOnce. The tick loop already spaces scans by TriggerInterval; RunOnce
	// must do the same or it will hammer a rate-limited source (Google Maps
	// starts failing after one immediate consecutive query). Zero disables
	// the pause, which is what tests want.
	PauseBetween time.Duration
}

type ScanStatus struct {
	Running  bool       `json:"running"`
	LastScan *time.Time `json:"last_scan"`
	LastErr  string     `json:"last_err"`
}

type Scanner struct {
	cfg     ScanConfig
	store   Store
	sources []Source
	scoring *scoring.Engine
	log     *logx.Logger

	running  atomic.Bool
	lastErr  atomic.Pointer[string]
	lastScan atomic.Pointer[time.Time]
	mu       sync.Mutex
	backoffs map[string]time.Time
}

func New(cfg ScanConfig, store Store, sources []Source, eng *scoring.Engine, log *logx.Logger) *Scanner {
	return &Scanner{
		cfg:      cfg,
		store:    store,
		sources:  sources,
		scoring:  eng,
		log:      log,
		backoffs: make(map[string]time.Time),
	}
}

func (s *Scanner) Start(ctx context.Context) {
	if !s.running.CompareAndSwap(false, true) {
		return
	}
	defer s.running.Store(false)

	s.log.Info("scan", "Scanner started (interval=%v)", s.cfg.TriggerInterval)
	ticker := time.NewTicker(s.cfg.TriggerInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.log.Info("scan", "Scanner stopped")
			return
		case <-ticker.C:
			if err := s.tick(ctx); err != nil {
				s.log.Warn("scan", "Tick error: %v", err)
				errStr := err.Error()
				s.lastErr.Store(&errStr)
			}
		}
	}
}

func (s *Scanner) tick(ctx context.Context) error {
	ests, err := s.store.ListEstablishments(ctx)
	if err != nil || len(ests) == 0 {
		return err
	}

	cursor, _ := s.store.GetCursor(ctx)
	idx := 0
	if cursor != "" {
		for i, e := range ests {
			if e.ID == cursor {
				idx = (i + 1) % len(ests)
				break
			}
		}
	}

	targetsCount := s.cfg.TargetsPerTick
	if targetsCount < 1 {
		targetsCount = 1
	}

	now := time.Now()
	s.lastScan.Store(&now)

	started := time.Now()
	where := cursor
	if where == "" {
		where = "el principio"
	}
	s.log.Step("scan", "tick: %d establecimiento(s) a partir de %s", targetsCount, where)

	for t := 0; t < targetsCount && t < len(ests); t++ {
		est := ests[(idx+t)%len(ests)]
		if err := s.ScanOne(ctx, est.ID); err != nil {
			s.log.Warn("scan", "Failed to scan establishment %s: %v", est.ID, err)
		}
	}

	_ = s.store.SetCursor(ctx, ests[(idx+targetsCount-1)%len(ests)].ID)
	s.log.Info("scan", "tick completado en %.1fs", time.Since(started).Seconds())
	return nil
}

func (s *Scanner) ScanOne(ctx context.Context, establishmentID string) error {
	ests, err := s.store.ListEstablishments(ctx)
	if err != nil {
		return err
	}
	var target *model.Establishment
	for _, e := range ests {
		if e.ID == establishmentID {
			target = e
			break
		}
	}
	if target == nil {
		return nil
	}

	now := time.Now()

	// The sources that will actually run: disabled ones and sources in
	// backoff stay out. Naming them up front makes the log read as the
	// plan of the scan.
	var enabled []Source
	for _, src := range s.sources {
		if !src.Enabled() {
			continue
		}
		if until, ok := s.backoffs[src.Name()]; ok && now.Before(until) {
			continue
		}
		enabled = append(enabled, src)
	}
	names := make([]string, len(enabled))
	for i, src := range enabled {
		names[i] = src.Name()
	}
	if len(enabled) == 0 {
		s.log.Warn("scan", "[%s] %s sin fuentes que ejecutar (ninguna habilitada o todas en backoff)",
			target.ID, target.Name)
	} else {
		s.log.Step("scan", "[%s] %s (%s) — escaneando con %s",
			target.ID, target.Name, target.Municipality, strings.Join(names, " + "))
	}

	var wg sync.WaitGroup
	sem := make(chan struct{}, s.cfg.MaxConcurrentSources)
	var mu sync.Mutex
	var allSignals []*model.Signal

	s.mu.Lock()
	for _, src := range enabled {
		wg.Add(1)
		go func(src Source) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			sigs, err := src.Collect(ctx, target)
			if err != nil {
				s.log.Warn("source", "[%s] %s error: %v", target.ID, src.Name(), err)
				return
			}
			if len(sigs) == 0 {
				s.log.Info("scan", "[%s] %s no aportó señales", target.ID, src.Name())
			} else {
				s.log.Info("scan", "[%s] %s → %d señales", target.ID, src.Name(), len(sigs))
			}
			mu.Lock()
			allSignals = append(allSignals, sigs...)
			mu.Unlock()
		}(src)
	}
	s.mu.Unlock()

	wg.Wait()

	for _, sig := range allSignals {
		_ = s.store.SaveSignal(ctx, sig)
	}

	since := now.AddDate(0, 0, -s.cfg.StaleAfterDays)
	historicalSigs, _ := s.store.ListSignals(ctx, target.ID, since)
	prevScore, _ := s.store.GetScore(ctx, target.ID)

	var inspDays float64 = 730.0
	if target.LastInspectionAt != nil {
		inspDays = now.Sub(*target.LastInspectionAt).Hours() / 24.0
	}

	score := s.scoring.Compute(target, historicalSigs, prevScore, inspDays)
	s.log.Info("scan", "[%s] score %s · riesgo de cierre %.0f%% · prioridad %.0f/100 · %d evidencias — %s",
		target.ID, score.Tier, score.ClosureRisk*100, score.Priority, score.EvidenceCount, score.Reason)
	if prevScore != nil && (prevScore.Status != score.Status || prevScore.Tier != score.Tier) {
		s.log.Warn("scan", "[%s] cambio de veredicto: %s (%s) → %s (%s)",
			target.ID, prevScore.Status, prevScore.Tier, score.Status, score.Tier)
	}
	_ = s.store.SaveScore(ctx, score)
	return nil
}

func (s *Scanner) RunOnce(ctx context.Context) error {
	// The one-pass run counts as activity too: /api/scan/status and the
	// UI's poll loop follow the flag to show the pass live.
	s.running.Store(true)
	defer s.running.Store(false)

	ests, err := s.store.ListEstablishments(ctx)
	if err != nil {
		return err
	}
	started := time.Now()
	s.log.Step("scan", "pase único: %d establecimiento(s)", len(ests))
	var failures int
	for i, e := range ests {
		if i > 0 && s.cfg.PauseBetween > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(s.cfg.PauseBetween):
			}
		}
		if err := s.ScanOne(ctx, e.ID); err != nil {
			failures++
			s.log.Warn("scan", "RunOnce error on %s: %v", e.ID, err)
		}
	}
	s.log.Info("scan", "pase completado en %.1fs · %d establecimientos · %d fallos — el scanner queda a la espera de re-escaneos manuales",
		time.Since(started).Seconds(), len(ests), failures)
	return nil
}

func (s *Scanner) Status() ScanStatus {
	var t *time.Time
	if p := s.lastScan.Load(); p != nil {
		t = p
	}
	var errStr string
	if p := s.lastErr.Load(); p != nil {
		errStr = *p
	}
	return ScanStatus{
		Running:  s.running.Load(),
		LastScan: t,
		LastErr:  errStr,
	}
}

func init() {
	rand.Seed(time.Now().UnixNano())
}
