package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeConfig(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestLoadResolvesExplicitRelativePathsAgainstConfigDir(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, `
database:
  driver: sqlite
  dsn: ./data/turislab.db
input:
  path: ../base.json
  ground_truth_path: ../ground-truth.json
sources:
  google_maps_scraper:
    enabled: true
    binary: ./bin/scraper
legal:
  scraping_ack: true
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	wantDSN := filepath.Join(dir, "data/turislab.db")
	if cfg.Database.DSN != wantDSN {
		t.Errorf("dsn = %q, want %q", cfg.Database.DSN, wantDSN)
	}
	if want := filepath.Join(dir, "../base.json"); cfg.Input.Path != want {
		t.Errorf("input path = %q, want %q", cfg.Input.Path, want)
	}
	if want := filepath.Join(dir, "../ground-truth.json"); cfg.Input.GroundTruthPath != want {
		t.Errorf("ground truth = %q, want %q", cfg.Input.GroundTruthPath, want)
	}
	if want := filepath.Join(dir, "bin/scraper"); cfg.Sources.GoogleMapsScraper.Binary != want {
		t.Errorf("binary = %q, want %q", cfg.Sources.GoogleMapsScraper.Binary, want)
	}
}

func TestLoadLeavesBareBinaryNameForPathLookup(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, `
sources:
  google_maps_scraper:
    enabled: true
    binary: google-maps-scraper
legal:
  scraping_ack: true
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Sources.GoogleMapsScraper.Binary != "google-maps-scraper" {
		t.Errorf("binary = %q, want the bare name unchanged", cfg.Sources.GoogleMapsScraper.Binary)
	}
}

func TestLoadKeepsInMemoryDSN(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, `
database:
  driver: sqlite
  dsn: ":memory:"
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Database.DSN != ":memory:" {
		t.Errorf("dsn = %q, want :memory:", cfg.Database.DSN)
	}
}

func TestLoadRejectsScrapingWithoutAck(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, `
sources:
  google_maps_scraper:
    enabled: true
legal:
  scraping_ack: false
`)

	if _, err := Load(path); err == nil {
		t.Error("expected an error when scraping is enabled without legal.scraping_ack")
	}
}
