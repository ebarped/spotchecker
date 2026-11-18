// Package store persists establishments, signals and scores using SQLite.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/datadope/turislab-doctor/internal/turislab/model"
	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("store: item not found")

// Store defines the persistence contract for TurisLab Doctor.
type Store interface {
	SaveEstablishment(ctx context.Context, e *model.Establishment) error
	GetEstablishment(ctx context.Context, id string) (*model.Establishment, error)
	ListEstablishments(ctx context.Context) ([]*model.Establishment, error)
	SaveSignal(ctx context.Context, s *model.Signal) error
	ListSignals(ctx context.Context, establishmentID string, since time.Time) ([]*model.Signal, error)
	SaveScore(ctx context.Context, sc *model.Score) error
	GetScore(ctx context.Context, establishmentID string) (*model.Score, error)
	ListScores(ctx context.Context) ([]*model.Score, error)
	GetCursor(ctx context.Context) (string, error)
	SetCursor(ctx context.Context, establishmentID string) error
	Close() error
}

type SQLiteStore struct {
	db *sql.DB
}

func Open(dsn string) (*SQLiteStore, error) {
	if !strings.Contains(dsn, "?") {
		dsn += "?_busy_timeout=5000&_journal=WAL"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	s := &SQLiteStore{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate sqlite: %w", err)
	}
	return s, nil
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

func (s *SQLiteStore) migrate() error {
	query := `
	CREATE TABLE IF NOT EXISTS establishments (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		category TEXT NOT NULL,
		stars INTEGER,
		address TEXT,
		municipality TEXT,
		province TEXT,
		postal_code TEXT,
		country TEXT,
		lat REAL,
		lng REAL,
		phone TEXT,
		email TEXT,
		website TEXT,
		registry_id TEXT,
		certifications TEXT,
		last_inspection_at TEXT,
		last_inspection_result TEXT,
		google_place_id TEXT,
		tripadvisor_id TEXT,
		yelp_id TEXT,
		osm_id TEXT,
		notes TEXT,
		id_generated INTEGER,
		created_at TEXT,
		updated_at TEXT
	);

	CREATE TABLE IF NOT EXISTS signals (
		id TEXT PRIMARY KEY,
		establishment_id TEXT NOT NULL,
		source_name TEXT NOT NULL,
		kind TEXT NOT NULL,
		collected_at TEXT NOT NULL,
		value REAL NOT NULL,
		raw_value TEXT,
		confidence REAL NOT NULL,
		weight REAL NOT NULL,
		family TEXT NOT NULL,
		dedupe_key TEXT UNIQUE,
		ttl_days INTEGER,
		error TEXT,
		payload BLOB,
		created_at TEXT
	);

	CREATE TABLE IF NOT EXISTS scores (
		establishment_id TEXT PRIMARY KEY,
		computed_at TEXT NOT NULL,
		closure_risk REAL NOT NULL,
		quality_risk REAL NOT NULL,
		quality_delta REAL NOT NULL,
		priority REAL NOT NULL,
		tier TEXT NOT NULL,
		confidence REAL NOT NULL,
		evidence_count INTEGER NOT NULL,
		status TEXT NOT NULL,
		reason TEXT,
		volatile INTEGER,
		conflict_detected INTEGER,
		heuristic_status TEXT NOT NULL DEFAULT '',
		heuristic_reason TEXT NOT NULL DEFAULT '',
		judge_model TEXT NOT NULL DEFAULT '',
		judge_status TEXT NOT NULL DEFAULT '',
		judge_reason TEXT NOT NULL DEFAULT '',
		judge_confidence REAL NOT NULL DEFAULT 0,
		judge_error TEXT NOT NULL DEFAULT '',
		judge_risk REAL NOT NULL DEFAULT 0,
		judge_risk_known INTEGER NOT NULL DEFAULT 0,
		risk_known INTEGER NOT NULL DEFAULT 0,
		updated_at TEXT
	);

	CREATE TABLE IF NOT EXISTS scan_cursor (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		establishment_id TEXT,
		updated_at TEXT
	);
	`
	if _, err := s.db.Exec(query); err != nil {
		return err
	}
	return s.migrateScores()
}

// scoreColumns added after the first release. Databases created before
// each column existed get it through ALTER TABLE; fresh ones already carry
// them in the CREATE TABLE above.
var scoreColumns = []struct{ name, decl string }{
	{"heuristic_status", "TEXT NOT NULL DEFAULT ''"},
	{"heuristic_reason", "TEXT NOT NULL DEFAULT ''"},
	{"judge_model", "TEXT NOT NULL DEFAULT ''"},
	{"judge_status", "TEXT NOT NULL DEFAULT ''"},
	{"judge_reason", "TEXT NOT NULL DEFAULT ''"},
	{"judge_confidence", "REAL NOT NULL DEFAULT 0"},
	{"judge_error", "TEXT NOT NULL DEFAULT ''"},
	// Rows written before the judge reported its own risk keep 0, but the
	// known flag stays false so the UI renders "—" instead of a fake 0%.
	{"judge_risk", "REAL NOT NULL DEFAULT 0"},
	{"judge_risk_known", "INTEGER NOT NULL DEFAULT 0"},
	// Rows written before the engine could tell "no risk measured" from
	// "zero risk" keep 0: the UI shows them as unknown until the next scan.
	{"risk_known", "INTEGER NOT NULL DEFAULT 0"},
}

// migrateScores adds the score columns an older database misses.
func (s *SQLiteStore) migrateScores() error {
	for _, col := range scoreColumns {
		query := `SELECT 1 FROM pragma_table_info('scores') WHERE name = ?`
		var exists int
		err := s.db.QueryRowContext(context.Background(), query, col.name).Scan(&exists)
		if err == nil {
			continue // already present
		}
		if err != sql.ErrNoRows {
			return err
		}
		alter := fmt.Sprintf("ALTER TABLE scores ADD COLUMN %s %s", col.name, col.decl)
		if _, err := s.db.Exec(alter); err != nil {
			return fmt.Errorf("add scores.%s: %w", col.name, err)
		}
	}
	return nil
}

func (s *SQLiteStore) SaveEstablishment(ctx context.Context, e *model.Establishment) error {
	certs, _ := json.Marshal(e.Certifications)
	now := time.Now().UTC().Format(time.RFC3339)
	var inspAt string
	if e.LastInspectionAt != nil {
		inspAt = e.LastInspectionAt.UTC().Format(time.RFC3339)
	}

	query := `
	INSERT INTO establishments (
		id, name, category, stars, address, municipality, province, postal_code, country,
		lat, lng, phone, email, website, registry_id, certifications,
		last_inspection_at, last_inspection_result, google_place_id, tripadvisor_id,
		yelp_id, osm_id, notes, id_generated, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(id) DO UPDATE SET
		name=excluded.name, category=excluded.category, stars=excluded.stars,
		address=excluded.address, municipality=excluded.municipality, province=excluded.province,
		postal_code=excluded.postal_code, country=excluded.country, lat=excluded.lat, lng=excluded.lng,
		phone=excluded.phone, email=excluded.email, website=excluded.website, registry_id=excluded.registry_id,
		certifications=excluded.certifications, last_inspection_at=excluded.last_inspection_at,
		last_inspection_result=excluded.last_inspection_result, google_place_id=excluded.google_place_id,
		tripadvisor_id=excluded.tripadvisor_id, yelp_id=excluded.yelp_id, osm_id=excluded.osm_id,
		notes=excluded.notes, id_generated=excluded.id_generated, updated_at=excluded.updated_at
	`
	_, err := s.db.ExecContext(ctx, query,
		e.ID, e.Name, string(e.Category), e.Stars, e.Address, e.Municipality, e.Province, e.PostalCode, e.Country,
		e.Lat, e.Lng, e.Phone, e.Email, e.Website, e.RegistryID, string(certs),
		nullableString(inspAt), e.LastInspectionResult, e.GooglePlaceID, e.TripAdvisorID,
		e.YelpID, e.OSMID, e.Notes, boolToInt(e.IDGenerated), now, now,
	)
	return err
}

func (s *SQLiteStore) GetEstablishment(ctx context.Context, id string) (*model.Establishment, error) {
	query := `SELECT id, name, category, stars, address, municipality, province, postal_code, country, lat, lng, phone, email, website, registry_id, certifications, last_inspection_at, last_inspection_result, google_place_id, tripadvisor_id, yelp_id, osm_id, notes, id_generated FROM establishments WHERE id = ?`
	row := s.db.QueryRowContext(ctx, query, id)
	est, err := scanEstablishment(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return est, nil
}

func (s *SQLiteStore) ListEstablishments(ctx context.Context) ([]*model.Establishment, error) {
	query := `SELECT id, name, category, stars, address, municipality, province, postal_code, country, lat, lng, phone, email, website, registry_id, certifications, last_inspection_at, last_inspection_result, google_place_id, tripadvisor_id, yelp_id, osm_id, notes, id_generated FROM establishments ORDER BY name`
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []*model.Establishment
	for rows.Next() {
		est, err := scanEstablishmentRows(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, est)
	}
	return list, rows.Err()
}

func scanEstablishment(row interface{ Scan(dest ...any) error }) (*model.Establishment, error) {
	var e model.Establishment
	var cat string
	var certsJSON string
	var inspAt sql.NullString
	var idGen int
	var stars sql.NullInt64
	var lat, lng sql.NullFloat64

	err := row.Scan(
		&e.ID, &e.Name, &cat, &stars, &e.Address, &e.Municipality, &e.Province, &e.PostalCode, &e.Country,
		&lat, &lng, &e.Phone, &e.Email, &e.Website, &e.RegistryID, &certsJSON,
		&inspAt, &e.LastInspectionResult, &e.GooglePlaceID, &e.TripAdvisorID,
		&e.YelpID, &e.OSMID, &e.Notes, &idGen,
	)
	if err != nil {
		return nil, err
	}
	e.Category = model.Category(cat)
	e.IDGenerated = idGen == 1
	if stars.Valid {
		v := int(stars.Int64)
		e.Stars = &v
	}
	if lat.Valid {
		e.Lat = &lat.Float64
	}
	if lng.Valid {
		e.Lng = &lng.Float64
	}
	if certsJSON != "" {
		_ = json.Unmarshal([]byte(certsJSON), &e.Certifications)
	}
	if inspAt.Valid && inspAt.String != "" {
		if t, err := time.Parse(time.RFC3339, inspAt.String); err == nil {
			e.LastInspectionAt = &t
		}
	}
	return &e, nil
}

func scanEstablishmentRows(rows *sql.Rows) (*model.Establishment, error) {
	return scanEstablishment(rows)
}

func (s *SQLiteStore) SaveSignal(ctx context.Context, sig *model.Signal) error {
	now := time.Now().UTC().Format(time.RFC3339)
	colAt := sig.CollectedAt.UTC().Format(time.RFC3339)

	// A signal without a dedupe key must not be deduplicated: an empty string
	// would make the UNIQUE index collapse every keyless signal into a single
	// row. Falling back to the signal id keeps every insert distinct while
	// the column stays NOT NULL for the readers.
	dedupeKey := sig.DedupeKey
	if dedupeKey == "" {
		dedupeKey = "nokey:" + sig.ID
	}

	// Re-observing the same fact (same dedupe key) must refresh the stored
	// interpretation, not keep the first one forever. Otherwise a change in
	// the rules — a new website status value, a review crossing a recency
	// bucket — never reaches the score of an establishment that was already
	// scanned once. The row keeps its original id; the evidence timestamp and
	// the derived fields move to the newest observation so the signal does
	// not age out while it is still being collected.
	query := `
	INSERT INTO signals (
		id, establishment_id, source_name, kind, collected_at, value, raw_value,
		confidence, weight, family, dedupe_key, ttl_days, error, payload, created_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(dedupe_key) DO UPDATE SET
		collected_at = excluded.collected_at,
		value        = excluded.value,
		raw_value    = excluded.raw_value,
		confidence   = excluded.confidence,
		weight       = excluded.weight,
		family       = excluded.family,
		ttl_days     = excluded.ttl_days,
		error        = excluded.error,
		payload      = excluded.payload
	`
	_, err := s.db.ExecContext(ctx, query,
		sig.ID, sig.EstablishmentID, sig.SourceName, string(sig.Kind), colAt, sig.Value, sig.RawValue,
		sig.Confidence, sig.Weight, string(sig.Family), dedupeKey, sig.TTLDays, sig.Error, sig.Payload, now,
	)
	return err
}

// ListSignalsSince returns every signal collected at or after since, newest
// first. The report uses it to expose the evidence behind each score without
// one query per establishment.
func (s *SQLiteStore) ListSignalsSince(ctx context.Context, since time.Time) ([]*model.Signal, error) {
	sinceStr := since.UTC().Format(time.RFC3339)
	query := `SELECT id, establishment_id, source_name, kind, collected_at, value, raw_value, confidence, weight, family, dedupe_key, ttl_days, error, payload FROM signals WHERE collected_at >= ? ORDER BY collected_at DESC`
	rows, err := s.db.QueryContext(ctx, query, sinceStr)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []*model.Signal
	for rows.Next() {
		var sig model.Signal
		var kind, family string
		var colAt string
		var payload []byte
		err := rows.Scan(
			&sig.ID, &sig.EstablishmentID, &sig.SourceName, &kind, &colAt, &sig.Value, &sig.RawValue,
			&sig.Confidence, &sig.Weight, &family, &sig.DedupeKey, &sig.TTLDays, &sig.Error, &payload,
		)
		if err != nil {
			return nil, err
		}
		sig.Kind = model.SignalKind(kind)
		sig.Family = model.SignalFamily(family)
		sig.Payload = payload
		if t, err := time.Parse(time.RFC3339, colAt); err == nil {
			sig.CollectedAt = t
		}
		list = append(list, &sig)
	}
	return list, rows.Err()
}

func (s *SQLiteStore) ListSignals(ctx context.Context, establishmentID string, since time.Time) ([]*model.Signal, error) {
	sinceStr := since.UTC().Format(time.RFC3339)
	query := `SELECT id, establishment_id, source_name, kind, collected_at, value, raw_value, confidence, weight, family, dedupe_key, ttl_days, error, payload FROM signals WHERE establishment_id = ? AND collected_at >= ? ORDER BY collected_at DESC`
	rows, err := s.db.QueryContext(ctx, query, establishmentID, sinceStr)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []*model.Signal
	for rows.Next() {
		var sig model.Signal
		var kind, family string
		var colAt string
		var payload []byte
		err := rows.Scan(
			&sig.ID, &sig.EstablishmentID, &sig.SourceName, &kind, &colAt, &sig.Value, &sig.RawValue,
			&sig.Confidence, &sig.Weight, &family, &sig.DedupeKey, &sig.TTLDays, &sig.Error, &payload,
		)
		if err != nil {
			return nil, err
		}
		sig.Kind = model.SignalKind(kind)
		sig.Family = model.SignalFamily(family)
		sig.Payload = payload
		if t, err := time.Parse(time.RFC3339, colAt); err == nil {
			sig.CollectedAt = t
		}
		list = append(list, &sig)
	}
	return list, rows.Err()
}

func (s *SQLiteStore) SaveScore(ctx context.Context, sc *model.Score) error {
	now := time.Now().UTC().Format(time.RFC3339)
	compAt := sc.ComputedAt.UTC().Format(time.RFC3339)

	query := `
	INSERT INTO scores (
		establishment_id, computed_at, closure_risk, quality_risk, quality_delta,
		priority, tier, confidence, evidence_count, status, reason, volatile, conflict_detected,
		heuristic_status, heuristic_reason, judge_model, judge_status, judge_reason,
		judge_confidence, judge_error, judge_risk, judge_risk_known, risk_known, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(establishment_id) DO UPDATE SET
		computed_at=excluded.computed_at, closure_risk=excluded.closure_risk, quality_risk=excluded.quality_risk,
		quality_delta=excluded.quality_delta, priority=excluded.priority, tier=excluded.tier,
		confidence=excluded.confidence, evidence_count=excluded.evidence_count, status=excluded.status,
		reason=excluded.reason, volatile=excluded.volatile, conflict_detected=excluded.conflict_detected,
		heuristic_status=excluded.heuristic_status, heuristic_reason=excluded.heuristic_reason,
		judge_model=excluded.judge_model, judge_status=excluded.judge_status, judge_reason=excluded.judge_reason,
		judge_confidence=excluded.judge_confidence, judge_error=excluded.judge_error,
		judge_risk=excluded.judge_risk, judge_risk_known=excluded.judge_risk_known,
		risk_known=excluded.risk_known, updated_at=excluded.updated_at
	`
	_, err := s.db.ExecContext(ctx, query,
		sc.EstablishmentID, compAt, sc.ClosureRisk, sc.QualityRisk, sc.QualityDelta,
		sc.Priority, string(sc.Tier), sc.Confidence, sc.EvidenceCount, string(sc.Status),
		sc.Reason, boolToInt(sc.Volatile), boolToInt(sc.ConflictDetected),
		string(sc.HeuristicStatus), sc.HeuristicReason, sc.JudgeModel, string(sc.JudgeStatus),
		sc.JudgeReason, sc.JudgeConfidence, sc.JudgeError, sc.JudgeRisk, boolToInt(sc.JudgeRiskKnown), boolToInt(sc.RiskKnown), now,
	)
	return err
}

// scoreColumnsQuery lists every score column in scan order.
const scoreColumnsQuery = `establishment_id, computed_at, closure_risk, quality_risk, quality_delta, priority, tier, confidence, evidence_count, status, reason, volatile, conflict_detected, heuristic_status, heuristic_reason, judge_model, judge_status, judge_reason, judge_confidence, judge_error, judge_risk, judge_risk_known, risk_known`

// scanScore reads one score row produced by scoreColumnsQuery.
func scanScore(row interface{ Scan(dest ...any) error }) (*model.Score, error) {
	var sc model.Score
	var compAt string
	var tier, status string
	var vol, confDet, riskKnown, judgeRiskKnown int
	var heurStatus, heurReason, judgeModel, judgeStatus, judgeReason, judgeError string

	err := row.Scan(
		&sc.EstablishmentID, &compAt, &sc.ClosureRisk, &sc.QualityRisk, &sc.QualityDelta,
		&sc.Priority, &tier, &sc.Confidence, &sc.EvidenceCount, &status,
		&sc.Reason, &vol, &confDet,
		&heurStatus, &heurReason, &judgeModel, &judgeStatus, &judgeReason,
		&sc.JudgeConfidence, &judgeError, &sc.JudgeRisk, &judgeRiskKnown, &riskKnown,
	)
	if err != nil {
		return nil, err
	}
	sc.Tier = model.TierLabel(tier)
	sc.Status = model.StatusSuggested(status)
	sc.Volatile = vol == 1
	sc.ConflictDetected = confDet == 1
	sc.RiskKnown = riskKnown == 1
	sc.JudgeRiskKnown = judgeRiskKnown == 1
	sc.HeuristicStatus = model.StatusSuggested(heurStatus)
	sc.HeuristicReason = heurReason
	sc.JudgeModel = judgeModel
	sc.JudgeStatus = model.StatusSuggested(judgeStatus)
	sc.JudgeReason = judgeReason
	sc.JudgeError = judgeError
	// Rows written before the judge existed carry no heuristic verdict:
	// the engine's is the only one there ever was.
	if sc.HeuristicStatus == "" {
		sc.HeuristicStatus = sc.Status
		sc.HeuristicReason = sc.Reason
	}
	if t, err := time.Parse(time.RFC3339, compAt); err == nil {
		sc.ComputedAt = t
	}
	return &sc, nil
}

func (s *SQLiteStore) GetScore(ctx context.Context, establishmentID string) (*model.Score, error) {
	query := `SELECT ` + scoreColumnsQuery + ` FROM scores WHERE establishment_id = ?`
	row := s.db.QueryRowContext(ctx, query, establishmentID)

	sc, err := scanScore(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return sc, nil
}

func (s *SQLiteStore) ListScores(ctx context.Context) ([]*model.Score, error) {
	query := `SELECT ` + scoreColumnsQuery + ` FROM scores`
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []*model.Score
	for rows.Next() {
		sc, err := scanScore(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, sc)
	}
	return list, rows.Err()
}

func (s *SQLiteStore) GetCursor(ctx context.Context) (string, error) {
	query := `SELECT establishment_id FROM scan_cursor WHERE id = 1`
	var estID sql.NullString
	err := s.db.QueryRowContext(ctx, query).Scan(&estID)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", err
	}
	if estID.Valid {
		return estID.String, nil
	}
	return "", nil
}

func (s *SQLiteStore) SetCursor(ctx context.Context, establishmentID string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	query := `
	INSERT INTO scan_cursor (id, establishment_id, updated_at) VALUES (1, ?, ?)
	ON CONFLICT(id) DO UPDATE SET establishment_id=excluded.establishment_id, updated_at=excluded.updated_at
	`
	_, err := s.db.ExecContext(ctx, query, establishmentID, now)
	return err
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
