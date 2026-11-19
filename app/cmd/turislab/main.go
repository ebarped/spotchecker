// Command turislab scores tourism establishments for closure risk and
// serves the inspector priority queue over HTTP.
//
// Usage:
//
//	turislab --config config.turislab.yaml
//	turislab --config config.turislab.yaml --once
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"syscall"
	"time"

	"github.com/datadope/turislab-doctor/internal/logx"
	"github.com/datadope/turislab-doctor/internal/turislab/agent"
	"github.com/datadope/turislab-doctor/internal/turislab/capture"
	"github.com/datadope/turislab-doctor/internal/turislab/config"
	"github.com/datadope/turislab-doctor/internal/turislab/judge"
	"github.com/datadope/turislab-doctor/internal/turislab/model"
	"github.com/datadope/turislab-doctor/internal/turislab/scan"
	"github.com/datadope/turislab-doctor/internal/turislab/scoring"
	"github.com/datadope/turislab-doctor/internal/turislab/seed"
	"github.com/datadope/turislab-doctor/internal/turislab/server"
	"github.com/datadope/turislab-doctor/internal/turislab/source"
	"github.com/datadope/turislab-doctor/internal/turislab/store"
	"github.com/datadope/turislab-doctor/internal/turislab/validate"
	"github.com/datadope/turislab-doctor/ui"
)

func main() {
	cfgPath := flag.String("config", "config.turislab.yaml", "path to the YAML configuration file")
	inputPath := flag.String("input", "", "path to the input JSON/NDJSON (overrides input.path)")
	addr := flag.String("addr", "", "listen address (overrides config)")
	once := flag.Bool("once", false, "scan every establishment once and exit")
	establishment := flag.String("establishment", "", "scan one establishment by id and exit")
	validateOnly := flag.Bool("validate", false, "compare the latest scores with the ground truth and exit")
	validateTop := flag.Int("validate-top", 3, "top-N cut-off used by --validate")
	validateStrict := flag.Bool("validate-strict", false, "fail --validate when an active establishment is flagged as closure_confirmed")
	staticDir := flag.String("static-dir", "", "serve the web UI from this directory instead of the embedded bundle (e.g. app/ui/build)")
	flag.Parse()

	log := logx.Default()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Error("", "config: %v", err)
		os.Exit(1)
	}
	if *inputPath != "" {
		// An explicit input file selects the single-file loader instead of
		// the CSV datasets directory.
		cfg.Input.Path = *inputPath
		cfg.Input.DatasetsDir = ""
	}
	log.Info("config", "loaded %s (sources: %v)", *cfgPath, cfg.EnabledSources())

	db, err := store.Open(cfg.Database.DSN)
	if err != nil {
		log.Error("", "store: %v", err)
		os.Exit(1)
	}
	defer db.Close()
	log.Info("store", "opened %s", cfg.Database.DSN)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	res, err := seed.Load(ctx, db, seed.Options{
		Path:             cfg.Input.Path,
		DatasetsDir:      cfg.Input.DatasetsDir,
		Format:           cfg.Input.Format,
		StrictValidation: cfg.Input.StrictValidation,
		DefaultCountry:   cfg.Input.DefaultCountry,
	})
	if err != nil {
		log.Error("", "seed: %v", err)
		os.Exit(1)
	}
	if cfg.Input.DatasetsDir != "" {
		log.Info("seed", "datasets dir: %s (%d csv files)", cfg.Input.DatasetsDir, len(res.Files))
		for _, file := range res.Files {
			log.Info("seed", "  %s: %d/%d registros (%d omitidos)", filepath.Base(file.Path), file.Loaded, file.Total, file.Skipped)
		}
	}
	log.Info("seed", "loaded %d/%d establishments (%d skipped)", res.Loaded, res.Total, res.Skipped)

	if *validateOnly {
		gt, err := validate.Load(cfg.Input.GroundTruthPath)
		if err != nil {
			log.Error("validate", "%v", err)
			os.Exit(1)
		}
		scores, err := db.ListScores(ctx)
		if err != nil {
			log.Error("validate", "list scores: %v", err)
			os.Exit(1)
		}
		result := validate.Run(gt, scores, validate.Options{
			TopN:                *validateTop,
			FailOnFalsePositive: *validateStrict,
		})
		fmt.Print(result.String())
		if !result.Passed {
			os.Exit(1)
		}
		return
	}

	srcs := buildSources(cfg)
	eng := scoring.NewEngine(scoringConfig(cfg))

	// The LLM judge reviews the evidence after the engine scores it and
	// issues the final verdict. Optional: without it the engine's verdict
	// is the final one.
	judgeSvc, err := buildJudge(cfg, log)
	if err != nil {
		log.Error("judge", "%v", err)
		os.Exit(1)
	}
	var scanOpts []scan.Option
	if judgeSvc != nil {
		scanOpts = append(scanOpts, scan.WithJudge(judgeSvc))
	}
	if cfg.Screenshots.Enabled {
		scanOpts = append(scanOpts, scan.WithScreenshoter(capture.New(capture.Config{
			MCPURL:       cfg.Screenshots.MCPURL,
			ArtifactsDir: cfg.ArtifactsDir,
			Timeout:      time.Duration(cfg.Screenshots.TimeoutSeconds) * time.Second,
			MinInterval:  cfg.Screenshots.MinInterval.Duration,
		})))
		log.Info("capture", "capturas automáticas en cada scan → %s (MCP %s)",
			cfg.ArtifactsDir, cfg.Screenshots.MCPURL)
	} else {
		log.Info("capture", "capturas automáticas desactivadas")
	}

	scanCfg := scan.ScanConfig{
		TriggerInterval:      cfg.Scan.TriggerInterval.Duration,
		TargetsPerTick:       cfg.Scan.TargetsPerTick,
		Jitter:               cfg.Scan.Jitter,
		MaxConcurrentSources: cfg.Scan.MaxConcurrentSources,
		BackoffOn429:         cfg.Scan.BackoffOn429.Duration,
		MaxAttempts:          cfg.Scan.MaxAttempts,
		StaleAfterDays:       cfg.Scan.StaleAfterDays,
		PauseBetween:         cfg.Scan.TriggerInterval.Duration,
	}
	scanner := scan.New(scanCfg, db, asScanSources(srcs), eng, log, scanOpts...)

	if *establishment != "" {
		if err := scanner.ScanOne(ctx, *establishment); err != nil {
			log.Error("", "scan %s: %v", *establishment, err)
			os.Exit(1)
		}
		return
	}

	if *once {
		if err := scanner.RunOnce(ctx); err != nil {
			log.Error("", "scan: %v", err)
			os.Exit(1)
		}
		return
	}

	// repeat keeps the ticker looping forever; the default is a single
	// pass over the whole list at startup, after which the scanner rests
	// and only explicit re-scans (the UI's refresh endpoint) wake it up.
	if cfg.Scan.Repeat {
		go scanner.Start(ctx)
	} else {
		go func() { _ = scanner.RunOnce(ctx) }()
	}

	// The web UI ships inside the binary. --static-dir (or server.static_dir)
	// serves a build from disk instead, so UI changes can be tried without
	// recompiling.
	uiFS, err := fs.Sub(ui.Build, "build")
	if err != nil {
		log.Error("server", "embedded UI: %v", err)
		os.Exit(1)
	}
	dir := *staticDir
	if dir == "" {
		dir = cfg.Server.StaticDir
	}
	if dir != "" {
		uiFS = os.DirFS(dir)
		log.Info("server", "serving the web UI from %s", dir)
	} else {
		log.Info("server", "serving the embedded web UI")
	}
	// The inspection assistant is mounted only when the configuration
	// enables it; without it the UI hides the agent tab.
	var agentSvc *agent.Service
	opts := []server.Option{server.WithScanner(scanner), server.WithArtifactsDir(cfg.ArtifactsDir)}
	if cfg.Agent.Enabled {
		agentSvc, err = agent.New(agentConfig(cfg.Agent, cfg.ArtifactsDir), log)
		if err != nil {
			log.Error("agent", "%v", err)
			os.Exit(1)
		}
		defer func() { _ = agentSvc.Close() }()
		opts = append(opts, server.WithAgent(agentSvc))
	}
	srv := server.New(uiFS, log, opts...)
	go refreshLoop(ctx, db, srcs, scanner, srv, agentSvc, cfg.Scan.StaleAfterDays, log)

	lnAddr := cfg.Server.Addr
	if *addr != "" {
		lnAddr = *addr
	}
	ln, err := net.Listen("tcp", lnAddr)
	if err != nil {
		log.Error("", "listen: %v", err)
		os.Exit(1)
	}

	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()

	if cfg.Agent.Enabled {
		log.Info("server", "agent mounted on %s/api/agent/chat", ln.Addr())
	}
	if err := srv.Serve(ln); err != nil {
		log.Error("", "server: %v", err)
		os.Exit(1)
	}
}

// buildSources instantiates the source adapters enabled in the configuration.
func buildSources(cfg config.Config) []source.SignalSource {
	var list []source.SignalSource

	if cfg.Sources.Website.Enabled {
		list = append(list, source.NewWebsite(source.WebsiteConfig{
			TimeoutSeconds: cfg.Sources.Website.TimeoutSeconds,
			UserAgent:      cfg.Sources.Website.UserAgent,
			MaxPages:       cfg.Sources.Website.MaxPages,
			CacheTTLDays:   cfg.Sources.Website.CacheTTLDays,
			CheckSSL:       cfg.Sources.Website.CheckSSL,
			Keywords:       cfg.Sources.Website.Keywords,
		}))
	}

	if cfg.Sources.OSM.Enabled {
		list = append(list, source.NewOSM(source.OSMConfig{
			OverpassURL:    cfg.Sources.OSM.OverpassURL,
			TimeoutSeconds: cfg.Sources.OSM.TimeoutSeconds,
			CacheTTLDays:   cfg.Sources.OSM.CacheTTLDays,
			MaxElements:    cfg.Sources.OSM.MaxElements,
			UserAgent:      cfg.Sources.OSM.UserAgent,
		}))
	}

	if cfg.Sources.GoogleMapsScraper.Enabled {
		proxyURL := ""
		if cfg.Sources.GoogleMapsScraper.ProxyURLEnv != "" {
			proxyURL = os.Getenv(cfg.Sources.GoogleMapsScraper.ProxyURLEnv)
		}
		list = append(list, source.NewGMapsScraper(source.GMapsScraperConfig{
			Provider:         cfg.Sources.GoogleMapsScraper.Provider,
			Binary:           cfg.Sources.GoogleMapsScraper.Binary,
			TimeoutSeconds:   cfg.Sources.GoogleMapsScraper.TimeoutSeconds,
			MaxPlaces:        cfg.Sources.GoogleMapsScraper.MaxPlaces,
			Depth:            cfg.Sources.GoogleMapsScraper.Depth,
			Language:         cfg.Sources.GoogleMapsScraper.Language,
			CacheTTLDays:     cfg.Sources.GoogleMapsScraper.CacheTTLDays,
			IncludeReviews:   cfg.Sources.GoogleMapsScraper.IncludeReviews,
			ProxyURL:         proxyURL,
			FallbackProvider: cfg.Sources.GoogleMapsScraper.FallbackProvider,
			Headless:         cfg.Sources.GoogleMapsScraper.Headless,
		}))
	}

	return list
}

// asScanSources adapts SignalSource values to the smaller interface the
// scanner consumes.
func asScanSources(srcs []source.SignalSource) []scan.Source {
	var list []scan.Source
	for _, s := range srcs {
		list = append(list, s)
	}
	return list
}

// scoringConfig maps the YAML scoring section onto the engine config.
func scoringConfig(cfg config.Config) scoring.ScoringConfig {
	sc := scoring.DefaultScoringConfig()
	w := cfg.Scoring.PriorityWeights
	if w.ClosureRisk > 0 {
		sc.WClosureRisk = w.ClosureRisk
		sc.WQualityRisk = w.QualityRisk
		sc.WQualityDelta = w.QualityDelta
		sc.WStaleness = w.Staleness
		sc.WImpact = w.Impact
		sc.WComplaints = w.Complaints
	}
	c := cfg.Scoring.ClosureFamilyWeights
	if c.DirectStatus > 0 {
		sc.WDirectStatus = c.DirectStatus
		sc.WActivity = c.Activity
		sc.WContext = c.Context
		sc.WOfficial = c.Official
	}
	q := cfg.Scoring.QualityFamilyWeights
	if q.Reviews > 0 {
		sc.WReviews = q.Reviews
		sc.WOfficialQ = q.Official
		sc.WProduct = q.Product
		sc.WNews = q.News
		sc.WFreshness = q.Freshness
	}
	if cfg.Scoring.ConfidenceFactor > 0 {
		sc.ConfidenceFactor = cfg.Scoring.ConfidenceFactor
	}
	if cfg.Scoring.HysteresisWeight > 0 {
		sc.HysteresisWeight = cfg.Scoring.HysteresisWeight
	}
	if cfg.Scoring.VolatileDelta > 0 {
		sc.VolatileDelta = cfg.Scoring.VolatileDelta
	}
	t := cfg.Tiers
	if t.P0Min > 0 {
		sc.TierConfig = model.TierConfig{
			P0Min:                 t.P0Min,
			P1Min:                 t.P1Min,
			P2Min:                 t.P2Min,
			MinConfidenceP0:       t.MinConfidence,
			CollectDataConfidence: t.CollectDataConfidence,
		}
	}
	return sc
}

// buildJudge builds the LLM judge when the configuration enables it. The
// judge model defaults to the agent's, so one endpoint serves both; a nil
// Judge means the feature stays off.
func buildJudge(cfg config.Config, log *logx.Logger) (*judge.Judge, error) {
	if !cfg.Judge.Enabled {
		return nil, nil
	}
	mc := cfg.Judge.Model
	if mc.Name == "" {
		mc = cfg.Agent.Model
	}
	if mc.Name == "" {
		return nil, errors.New("judge: no model configured (set judge.model or agent.model)")
	}
	am := agent.ModelConfig{
		Provider:  mc.Provider,
		Name:      mc.Name,
		BaseURL:   mc.BaseURL,
		APIKeyEnv: mc.APIKeyEnv,
	}
	// Fail fast on a missing key, like the agent does: a credential gap
	// should stop the startup, not the first scan.
	if agent.ResolveAPIKey(am) == "" {
		return nil, fmt.Errorf(
			"judge model: set judge.model.api_key or the environment variable %s (judge.model.api_key_env)",
			mc.APIKeyEnv)
	}
	factory, err := agent.ModelFactory(am)
	if err != nil {
		return nil, err
	}
	timeout := time.Duration(cfg.Judge.TimeoutSeconds) * time.Second
	j := judge.New(judge.Config{
		ModelName:  mc.Name,
		Timeout:    timeout,
		MaxSignals: cfg.Judge.MaxSignals,
	}, factory, log)
	log.Info("judge", "montado sobre %s — revisa la evidencia y emite el veredicto final", mc.Name)
	return j, nil
}

// refreshLoop rebuilds the in-memory report every 10 seconds from the store.
// agentConfig maps the YAML agent section onto the agent package's own
// configuration type, so the package stays decoupled from config.
func agentConfig(cfg config.AgentConfig, artifactsDir string) agent.Config {
	mcp := make([]agent.MCPServer, 0, len(cfg.MCP))
	for _, server := range cfg.MCP {
		mcp = append(mcp, agent.MCPServer{
			Name:    server.Name,
			URL:     server.URL,
			Headers: server.Headers,
			Tools:   server.Tools,
		})
	}
	return agent.Config{
		MaxTurns: cfg.MaxTurns,
		Model: agent.ModelConfig{
			Provider:  cfg.Model.Provider,
			Name:      cfg.Model.Name,
			BaseURL:   cfg.Model.BaseURL,
			APIKeyEnv: cfg.Model.APIKeyEnv,
		},
		MCP:          mcp,
		ArtifactsDir: artifactsDir,
		Suggestions:  cfg.Suggestions,
	}
}

func refreshLoop(ctx context.Context, db *store.SQLiteStore, srcs []source.SignalSource, scanner *scan.Scanner, srv *server.Server, agentSvc *agent.Service, staleAfterDays int, log *logx.Logger) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	build := func() {
		rep := buildReport(ctx, db, srcs, scanner, staleAfterDays)
		srv.SetReport(rep)
		// The assistant answers from the same snapshot the UI shows.
		if agentSvc != nil {
			agentSvc.SetReport(rep)
		}
	}
	build()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			build()
		}
	}
}

// buildReport assembles the full report snapshot: queue ordered by priority,
// summary counts and source statuses.
// waitingOrder orders the unscored establishments by their upcoming scan
// position. The scanner walks the list (ordered by name) starting right
// after the cursor — the id of the last scanned establishment — so rotating
// the list from cursor+1 and keeping the unscored ones gives the queue the
// order in which they will be processed. An empty cursor (nothing scanned
// yet) keeps the natural order.
func waitingOrder(ests []*model.Establishment, byID map[string]*model.Score, cursor string) []*model.Establishment {
	if cursor == "" || len(ests) == 0 {
		var waiting []*model.Establishment
		for _, e := range ests {
			if byID[e.ID] == nil {
				waiting = append(waiting, e)
			}
		}
		return waiting
	}
	start := -1
	for i, e := range ests {
		if e.ID == cursor {
			start = (i + 1) % len(ests)
			break
		}
	}
	rotated := ests
	if start > 0 {
		rotated = make([]*model.Establishment, 0, len(ests))
		rotated = append(rotated, ests[start:]...)
		rotated = append(rotated, ests[:start]...)
	}
	var waiting []*model.Establishment
	for _, e := range rotated {
		if byID[e.ID] == nil {
			waiting = append(waiting, e)
		}
	}
	return waiting
}

// capturedWebsite is the URL the scanner uses for the screenshot: the
// registry's own website, or the newest one Google Maps discovered. Keeping it
// in the report lets the detail screen link the photo it is actually showing.
func capturedWebsite(est *model.Establishment, signals []*model.Signal) string {
	if est == nil {
		return ""
	}
	if est.Website != "" {
		return est.Website
	}
	site := ""
	var newest time.Time
	for _, sig := range signals {
		if sig.EstablishmentID == est.ID && sig.Kind == model.KindWebsiteFound &&
			sig.RawValue != "" && sig.CollectedAt.After(newest) {
			site, newest = sig.RawValue, sig.CollectedAt
		}
	}
	return site
}

func buildReport(ctx context.Context, db *store.SQLiteStore, srcs []source.SignalSource, scanner *scan.Scanner, staleAfterDays int) *model.Report {
	ests, err := db.ListEstablishments(ctx)
	if err != nil {
		ests = nil
	}
	scores, err := db.ListScores(ctx)
	if err != nil {
		scores = nil
	}

	// The evidence table needs the signals behind each score. One query for
	// the whole report beats one per establishment, and the window is the
	// same the scanner scores with.
	if staleAfterDays <= 0 {
		staleAfterDays = 7
	}
	since := time.Now().AddDate(0, 0, -staleAfterDays)
	signals, err := db.ListSignalsSince(ctx, since)
	if err != nil {
		signals = nil
	}
	evidenceByID := reportEvidence(signals, since, scoring.DefaultScoringConfig().DecayHalfLifeDays)
	websiteByID := make(map[string]string, len(ests))
	for _, e := range ests {
		if site := capturedWebsite(e, signals); site != "" {
			websiteByID[e.ID] = site
		}
	}

	byID := make(map[string]*model.Score, len(scores))
	for _, sc := range scores {
		byID[sc.EstablishmentID] = sc
	}

	// Scored establishments lead the queue, worst priority first. Unscored
	// ones ride along at the end with a nil score — the UI shows them as
	// waiting — and their order is the upcoming scan order: the cursor
	// marks the last scanned id, so rotating the list right after it puts
	// the next target first.
	cursor, _ := db.GetCursor(ctx)
	type pair struct {
		est *model.Establishment
		sc  *model.Score
	}
	var scored []pair
	for _, e := range ests {
		if sc := byID[e.ID]; sc != nil {
			scored = append(scored, pair{e, sc})
		}
	}
	sort.SliceStable(scored, func(i, j int) bool {
		return scored[i].sc.Priority > scored[j].sc.Priority
	})

	now := time.Now()
	var queue []model.QueueItem
	summary := model.ReportSummary{Total: len(ests)}
	covered := 0

	rank := 0
	for _, p := range scored {
		rank++
		item := model.QueueItem{
			Rank:           rank,
			Establishment:  p.est,
			WebsiteURL:     websiteByID[p.est.ID],
			Score:          p.sc,
			PrimarySignals: evidenceByID[p.est.ID],
			ScanAge:        now.Sub(p.sc.ComputedAt),
		}
		switch p.sc.Tier {
		case model.TierP0:
			summary.P0++
		case model.TierP1:
			summary.P1++
		case model.TierP2:
			summary.P2++
		case model.TierP3:
			summary.P3++
		case model.TierCollectData:
			summary.CollectData++
		}
		if p.sc.Status == model.StatusClosureConfirmed {
			summary.ClosureConfirmed++
		}
		if p.sc.Confidence < 0.35 {
			summary.LowConfidence++
		}
		if p.sc.EvidenceCount > 0 {
			covered++
		}
		queue = append(queue, item)
	}
	for _, e := range waitingOrder(ests, byID, cursor) {
		rank++
		queue = append(queue, model.QueueItem{
			Rank:          rank,
			Establishment: e,
			WebsiteURL:    websiteByID[e.ID],
		})
	}
	if summary.Total > 0 {
		summary.Coverage = float64(covered) / float64(summary.Total) * 100
	}

	status := scanner.Status()

	var sources []model.SourceStatus
	for _, s := range srcs {
		sources = append(sources, model.SourceStatus{
			Name:    s.Name(),
			Enabled: s.Enabled(),
			Healthy: true,
		})
	}

	return &model.Report{
		GeneratedAt:        now,
		EstablishmentCount: len(ests),
		Queue:              queue,
		Summary:            summary,
		ScanCursor:         cursor,
		ScanRunning:        status.Running,
		ScanError:          status.LastErr,
		Sources:            sources,
	}
}
