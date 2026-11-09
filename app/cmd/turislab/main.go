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
	"flag"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/signal"
	"sort"
	"syscall"
	"time"

	"github.com/datadope/turislab-doctor/internal/logx"
	"github.com/datadope/turislab-doctor/internal/turislab/agent"
	"github.com/datadope/turislab-doctor/internal/turislab/config"
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
		Format:           cfg.Input.Format,
		StrictValidation: cfg.Input.StrictValidation,
		DefaultCountry:   cfg.Input.DefaultCountry,
	})
	if err != nil {
		log.Error("", "seed: %v", err)
		os.Exit(1)
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
	scanner := scan.New(scanCfg, db, asScanSources(srcs), eng, log)

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

	go scanner.Start(ctx)

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
	opts := []server.Option{server.WithScanner(scanner)}
	if cfg.Agent.Enabled {
		agentSvc, err = agent.New(agentConfig(cfg.Agent), log)
		if err != nil {
			log.Error("agent", "%v", err)
			os.Exit(1)
		}
		defer func() { _ = agentSvc.Close() }()
		opts = append(opts, server.WithAgent(agentSvc))
	}
	srv := server.New(uiFS, log, opts...)
	go refreshLoop(ctx, db, srcs, scanner, srv, agentSvc, log)

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

// refreshLoop rebuilds the in-memory report every 10 seconds from the store.
// agentConfig maps the YAML agent section onto the agent package's own
// configuration type, so the package stays decoupled from config.
func agentConfig(cfg config.AgentConfig) agent.Config {
	mcp := make([]agent.MCPServer, 0, len(cfg.MCP))
	for _, server := range cfg.MCP {
		mcp = append(mcp, agent.MCPServer{Name: server.Name, URL: server.URL, Headers: server.Headers})
	}
	return agent.Config{
		MaxTurns: cfg.MaxTurns,
		Model: agent.ModelConfig{
			Provider:  cfg.Model.Provider,
			Name:      cfg.Model.Name,
			BaseURL:   cfg.Model.BaseURL,
			APIKeyEnv: cfg.Model.APIKeyEnv,
		},
		MCP:         mcp,
		Suggestions: cfg.Suggestions,
	}
}

func refreshLoop(ctx context.Context, db *store.SQLiteStore, srcs []source.SignalSource, scanner *scan.Scanner, srv *server.Server, agentSvc *agent.Service, log *logx.Logger) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	build := func() {
		rep := buildReport(ctx, db, srcs, scanner)
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
func buildReport(ctx context.Context, db *store.SQLiteStore, srcs []source.SignalSource, scanner *scan.Scanner) *model.Report {
	ests, err := db.ListEstablishments(ctx)
	if err != nil {
		ests = nil
	}
	scores, err := db.ListScores(ctx)
	if err != nil {
		scores = nil
	}

	byID := make(map[string]*model.Score, len(scores))
	for _, sc := range scores {
		byID[sc.EstablishmentID] = sc
	}

	// Only include establishments that have been scored at least once; a
	// fresh database shows an empty queue until the scanner runs.
	type pair struct {
		est *model.Establishment
		sc  *model.Score
	}
	var pairs []pair
	for _, e := range ests {
		if sc, ok := byID[e.ID]; ok {
			pairs = append(pairs, pair{e, sc})
		}
	}
	sort.SliceStable(pairs, func(i, j int) bool {
		return pairs[i].sc.Priority > pairs[j].sc.Priority
	})

	now := time.Now()
	var queue []model.QueueItem
	summary := model.ReportSummary{Total: len(pairs)}
	covered := 0

	for i, p := range pairs {
		item := model.QueueItem{
			Rank:          i + 1,
			Establishment: p.est,
			Score:         p.sc,
			ScanAge:       now.Sub(p.sc.ComputedAt),
		}
		queue = append(queue, item)

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
	}
	if summary.Total > 0 {
		summary.Coverage = float64(covered) / float64(summary.Total) * 100
	}

	cursor, _ := db.GetCursor(ctx)
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
