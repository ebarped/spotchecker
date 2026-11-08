// Package scan implements the sequential establishment scanner.
package scan

import (
	"context"
	"math/rand"
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

	for t := 0; t < targetsCount && t < len(ests); t++ {
		est := ests[(idx+t)%len(ests)]
		if err := s.ScanOne(ctx, est.ID); err != nil {
			s.log.Warn("scan", "Failed to scan establishment %s: %v", est.ID, err)
		}
	}

	_ = s.store.SetCursor(ctx, ests[(idx+targetsCount-1)%len(ests)].ID)
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

	var wg sync.WaitGroup
	sem := make(chan struct{}, s.cfg.MaxConcurrentSources)
	var mu sync.Mutex
	var allSignals []*model.Signal

	now := time.Now()

	s.mu.Lock()
	for _, src := range s.sources {
		if !src.Enabled() {
			continue
		}
		if until, ok := s.backoffs[src.Name()]; ok && now.Before(until) {
			continue
		}

		wg.Add(1)
		go func(src Source) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			sigs, err := src.Collect(ctx, target)
			if err != nil {
				s.log.Warn("source", "%s error: %v", src.Name(), err)
				return
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
	_ = s.store.SaveScore(ctx, score)
	return nil
}

func (s *Scanner) RunOnce(ctx context.Context) error {
	ests, err := s.store.ListEstablishments(ctx)
	if err != nil {
		return err
	}
	for i, e := range ests {
		if i > 0 && s.cfg.PauseBetween > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(s.cfg.PauseBetween):
			}
		}
		if err := s.ScanOne(ctx, e.ID); err != nil {
			s.log.Warn("scan", "RunOnce error on %s: %v", e.ID, err)
		}
	}
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
