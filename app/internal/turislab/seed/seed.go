// Package seed loads establishment records into the store.
//
// On startup the seeder either reads one JSON/NDJSON file (Options.Path) or,
// when Options.DatasetsDir is set, every *.csv file in that directory
// (Options.DatasetsDir). Each record is normalised (deterministic ID when the
// source lacks one, default country, category cleanup) and upserted: existing
// records are updated, new records are inserted.
package seed

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/datadope/turislab-doctor/internal/turislab/model"
)

// Store is the subset of store.Store used by the seeder.
type Store interface {
	SaveEstablishment(ctx context.Context, e *model.Establishment) error
	ListEstablishments(ctx context.Context) ([]*model.Establishment, error)
}

// Options configures the seed operation.
type Options struct {
	// Path is the path to a single JSON/NDJSON input file. Ignored when
	// DatasetsDir is set.
	Path string

	// DatasetsDir, when set, makes Load list every *.csv file in that
	// directory (sorted by name) and upsert the records from all of them.
	// It takes precedence over Path and Format.
	DatasetsDir string

	// Format is the file format ("json", "ndjson"). Defaults to "json".
	Format string

	// StrictValidation causes Load to return an error for invalid records
	// instead of skipping them.
	StrictValidation bool

	// DefaultCountry is used when an establishment has no Country field (ISO
	// 3166-1 alpha-2, e.g. "ES").
	DefaultCountry string
}

// FileResult summarises the records loaded from one dataset file.
type FileResult struct {
	Path    string
	Total   int
	Loaded  int
	Skipped int
	Errors  []string
}

// Result summarises a completed seed operation.
type Result struct {
	// Total is the total number of records across all input files.
	Total int

	// Loaded is the number of records successfully upserted.
	Loaded int

	// Skipped is the number of records skipped due to validation errors
	// (only non-zero when StrictValidation is false).
	Skipped int

	// Errors contains validation error messages for skipped records.
	Errors []string

	// Files holds one entry per file processed, in processing order.
	Files []FileResult
}

// Load reads the configured input and upserts all valid establishments into
// the store. With DatasetsDir it loads every CSV in the directory; otherwise
// it loads the single file at Path. It returns a Result describing what
// happened.
func Load(ctx context.Context, s Store, opts Options) (Result, error) {
	if opts.DatasetsDir != "" {
		return loadDatasets(ctx, s, opts)
	}
	return loadFile(ctx, s, opts)
}

// loadFile reads the single JSON/NDJSON input file.
func loadFile(ctx context.Context, s Store, opts Options) (Result, error) {
	if opts.Format == "" {
		opts.Format = "json"
	}

	f, err := os.Open(opts.Path)
	if err != nil {
		return Result{}, fmt.Errorf("seed: open %q: %w", opts.Path, err)
	}
	defer f.Close()

	var raws []json.RawMessage

	switch opts.Format {
	case "ndjson":
		dec := json.NewDecoder(f)
		for dec.More() {
			var raw json.RawMessage
			if err := dec.Decode(&raw); err != nil {
				return Result{}, fmt.Errorf("seed: decode ndjson: %w", err)
			}
			raws = append(raws, raw)
		}
	default: // "json"
		if err := json.NewDecoder(f).Decode(&raws); err != nil {
			return Result{}, fmt.Errorf("seed: decode json: %w", err)
		}
	}

	var res Result
	res.Total = len(raws)

	for i, raw := range raws {
		est, err := parseEstablishment(raw, opts.DefaultCountry)
		if err != nil {
			msg := fmt.Sprintf("record %d: %v", i, err)
			if opts.StrictValidation {
				return res, fmt.Errorf("seed: %s", msg)
			}
			res.Skipped++
			res.Errors = append(res.Errors, msg)
			continue
		}

		if err := s.SaveEstablishment(ctx, est); err != nil {
			return res, fmt.Errorf("seed: save establishment %q: %w", est.ID, err)
		}
		res.Loaded++
	}

	return res, nil
}

// parseEstablishment deserialises one raw JSON message and normalises it.
func parseEstablishment(raw json.RawMessage, defaultCountry string) (*model.Establishment, error) {
	var e model.Establishment
	if err := json.Unmarshal(raw, &e); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}
	if err := normalise(&e, defaultCountry); err != nil {
		return nil, err
	}
	return &e, nil
}

// normalise applies the defaults shared by the JSON and CSV loaders: trims
// the name, fills in the country, generates a deterministic ID when needed
// and normalises the category.
func normalise(e *model.Establishment, defaultCountry string) error {
	e.Name = strings.TrimSpace(e.Name)
	if e.Name == "" {
		return fmt.Errorf("name is empty")
	}

	if e.Country == "" && defaultCountry != "" {
		e.Country = defaultCountry
	}

	if strings.TrimSpace(e.ID) == "" {
		e.ID = deterministicID(*e)
		e.IDGenerated = true
	}

	switch e.Category {
	case model.CategoryRestaurant, model.CategoryHotel, model.CategoryBar, model.CategoryOther:
		// ok
	case "":
		e.Category = model.CategoryOther
	default:
		// Accept unknown categories but normalise to Other.
		e.Category = model.CategoryOther
	}

	return nil
}

// deterministicID returns a short SHA-256-based ID for an establishment that
// has no ID in the source data.  The input is name+address+municipality, which
// is stable across re-imports of the same record.
func deterministicID(e model.Establishment) string {
	key := strings.ToLower(e.Name) + "|" + strings.ToLower(e.Address) + "|" + strings.ToLower(e.Municipality)
	h := sha256.Sum256([]byte(key))
	return fmt.Sprintf("gen-%x", h[:8])
}
