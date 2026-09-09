package benchmark

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const runColumns = `id,instance_id,instance_slug_snapshot,instance_name_snapshot,instance_config_snapshot,model_id,model_slug_snapshot,model_name_snapshot,artifact_snapshot,workload_profile,resolved_argv,mapping_differences,status,created_at,started_at,completed_at,llamarack_version,llamarack_commit,llama_cpp_release,llama_cpp_build,llama_bench_version,llama_bench_fingerprint,runtime_variant,benchmark_schema_version,parser_schema_version,hardware_snapshot,failure,diagnostic_output`

type SQLStore struct {
	db *sql.DB
}

func NewSQLStore(db *sql.DB) *SQLStore { return &SQLStore{db: db} }

func (s *SQLStore) CreateRun(ctx context.Context, run Run) error {
	if s == nil || s.db == nil {
		return errors.New("benchmark store is not configured")
	}
	if strings.TrimSpace(run.ID) == "" {
		return errors.New("benchmark run id is required")
	}
	if !run.Status.Valid() {
		return errors.New("benchmark run status is invalid")
	}
	if run.CreatedAt.IsZero() {
		run.CreatedAt = time.Now().UTC()
	}
	instanceConfig, err := marshalJSON(run.InstanceConfig)
	if err != nil {
		return err
	}
	artifact, err := marshalJSON(run.Artifact)
	if err != nil {
		return err
	}
	workload, err := marshalJSON(run.Workload)
	if err != nil {
		return err
	}
	argv, err := marshalJSON(run.ResolvedArgv)
	if err != nil {
		return err
	}
	differences, err := marshalJSON(run.MappingDifferences)
	if err != nil {
		return err
	}
	hardwareSnapshot, err := marshalJSON(run.Hardware)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO benchmark_runs(
		id,instance_id,instance_slug_snapshot,instance_name_snapshot,instance_config_snapshot,
		model_id,model_slug_snapshot,model_name_snapshot,artifact_snapshot,workload_profile,
		resolved_argv,mapping_differences,status,created_at,started_at,completed_at,
		llamarack_version,llamarack_commit,llama_cpp_release,llama_cpp_build,llama_bench_version,
		llama_bench_fingerprint,runtime_variant,benchmark_schema_version,parser_schema_version,
		hardware_snapshot,failure,diagnostic_output
	) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		run.ID, run.InstanceID, run.InstanceSlugSnapshot, run.InstanceNameSnapshot, instanceConfig,
		run.ModelID, run.ModelSlugSnapshot, run.ModelNameSnapshot, artifact, workload,
		argv, differences, string(run.Status), run.CreatedAt.Unix(), nullableTime(run.StartedAt), nullableTime(run.CompletedAt),
		run.Build.LlamaRackVersion, nullIfEmpty(run.Build.LlamaRackCommit), nullIfEmpty(run.Build.LlamaCppRelease), nullIfEmpty(run.Build.LlamaCppBuild), nullIfEmpty(run.Build.LlamaBenchVersion),
		nullIfEmpty(run.Build.LlamaBenchFingerprint), nullIfEmpty(run.Build.RuntimeVariant), run.BenchmarkSchemaVersion, run.ParserSchemaVersion,
		hardwareSnapshot, nullIfEmpty(run.Failure), run.DiagnosticOutput,
	)
	return err
}

func (s *SQLStore) GetRun(ctx context.Context, id string) (Run, error) {
	if s == nil || s.db == nil {
		return Run{}, errors.New("benchmark store is not configured")
	}
	run, err := scanRun(s.db.QueryRowContext(ctx, `SELECT `+runColumns+` FROM benchmark_runs WHERE id=?`, strings.TrimSpace(id)))
	if errors.Is(err, sql.ErrNoRows) {
		return Run{}, ErrNotFound
	}
	if err != nil {
		return Run{}, err
	}
	grouped, err := loadResultsByRunIDs(ctx, s.db, []string{run.ID})
	if err != nil {
		return Run{}, err
	}
	run.Results = grouped[run.ID]
	return run, nil
}

func (s *SQLStore) ListRuns(ctx context.Context, filter Filter) (Page, error) {
	if s == nil || s.db == nil {
		return Page{}, errors.New("benchmark store is not configured")
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}
	where, args := benchmarkWhere(filter)
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM benchmark_runs`+where, args...).Scan(&total); err != nil {
		return Page{}, err
	}
	queryArgs := append(append([]any(nil), args...), limit, offset)
	rows, err := s.db.QueryContext(ctx, `SELECT `+runColumns+` FROM benchmark_runs`+where+` ORDER BY created_at DESC,id DESC LIMIT ? OFFSET ?`, queryArgs...)
	if err != nil {
		return Page{}, err
	}
	defer rows.Close()
	items := make([]Run, 0, limit)
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return Page{}, err
		}
		items = append(items, run)
	}
	if err := rows.Err(); err != nil {
		return Page{}, err
	}
	// Release the list cursor before querying results: SQLite may use one
	// connection, and nested queries while holding rows would then deadlock.
	if err := rows.Close(); err != nil {
		return Page{}, err
	}
	ids := make([]string, len(items))
	for index := range items {
		ids[index] = items[index].ID
	}
	grouped, err := loadResultsByRunIDs(ctx, s.db, ids)
	if err != nil {
		return Page{}, err
	}
	for index := range items {
		items[index].Results = grouped[items[index].ID]
	}
	return Page{Items: items, Total: total, Limit: limit, Offset: offset}, nil
}

func (s *SQLStore) TransitionRun(ctx context.Context, id string, from, to Status, update TransitionUpdate) (Run, error) {
	if s == nil || s.db == nil {
		return Run{}, errors.New("benchmark store is not configured")
	}
	if !from.Valid() || !to.Valid() {
		return Run{}, errors.New("invalid benchmark transition status")
	}
	if !CanTransition(from, to) {
		return Run{}, ErrTransitionConflict
	}
	result, err := s.db.ExecContext(ctx, `UPDATE benchmark_runs
		SET status=?,started_at=COALESCE(?,started_at),completed_at=COALESCE(?,completed_at),failure=?,diagnostic_output=?
		WHERE id=? AND status=?`, string(to), nullableTime(update.StartedAt), nullableTime(update.CompletedAt), nullIfEmpty(update.Failure), update.DiagnosticOutput, strings.TrimSpace(id), string(from))
	if err != nil {
		return Run{}, err
	}
	if err := transitionResult(ctx, s.db, id, result); err != nil {
		return Run{}, err
	}
	return s.GetRun(ctx, id)
}

func (s *SQLStore) CompleteRun(ctx context.Context, id string, completion Completion, results []Result) (Run, error) {
	if s == nil || s.db == nil {
		return Run{}, errors.New("benchmark store is not configured")
	}
	if completion.CompletedAt.IsZero() {
		completion.CompletedAt = time.Now().UTC()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Run{}, err
	}
	defer tx.Rollback()
	updated, err := tx.ExecContext(ctx, `UPDATE benchmark_runs SET status=?,completed_at=?,failure=NULL,diagnostic_output=? WHERE id=? AND status=?`,
		string(StatusCompleted), completion.CompletedAt.Unix(), completion.DiagnosticOutput, strings.TrimSpace(id), string(StatusRunning))
	if err != nil {
		return Run{}, err
	}
	if err := transitionResult(ctx, tx, id, updated); err != nil {
		return Run{}, err
	}
	for index, result := range results {
		result.CaseIndex = index
		raw := result.RawFields
		if len(raw) == 0 {
			raw = json.RawMessage(`{}`)
		}
		if !json.Valid(raw) {
			return Run{}, fmt.Errorf("benchmark result %d has invalid raw JSON", index)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO benchmark_results(
			run_id,case_index,case_id,prompt_tokens,generation_tokens,repetitions,avg_ns,stddev_ns,avg_ts,stddev_ts,raw_fields
		) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, strings.TrimSpace(id), index, result.CaseID, result.PromptTokens, result.GenerationTokens, result.Repetitions,
			nullInt64(result.AverageNS), nullInt64(result.StdDevNS), nullFloat64(result.AverageTokensPS), nullFloat64(result.StdDevTokensPS), string(raw)); err != nil {
			return Run{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Run{}, err
	}
	return s.GetRun(ctx, id)
}

func (s *SQLStore) DeleteRun(ctx context.Context, id string) error {
	if s == nil || s.db == nil {
		return errors.New("benchmark store is not configured")
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM benchmark_runs WHERE id=?`, strings.TrimSpace(id))
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrNotFound
	}
	return nil
}

func benchmarkWhere(filter Filter) (string, []any) {
	clauses := make([]string, 0, 3)
	args := make([]any, 0, 3)
	if value := strings.TrimSpace(filter.InstanceID); value != "" {
		clauses = append(clauses, "instance_id=?")
		args = append(args, value)
	}
	if value := strings.TrimSpace(filter.ModelID); value != "" {
		clauses = append(clauses, "model_id=?")
		args = append(args, value)
	}
	if filter.Status != "" {
		clauses = append(clauses, "status=?")
		args = append(args, string(filter.Status))
	}
	if len(clauses) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanRun(row rowScanner) (Run, error) {
	var run Run
	var instanceConfig, artifact, workload, argv, differences, hardwareSnapshot string
	var status string
	var created int64
	var started, completed sql.NullInt64
	var commit, llamaRelease, llamaBuild, benchVersion, benchFingerprint, variant sql.NullString
	var failure, diagnostics sql.NullString
	if err := row.Scan(
		&run.ID, &run.InstanceID, &run.InstanceSlugSnapshot, &run.InstanceNameSnapshot, &instanceConfig,
		&run.ModelID, &run.ModelSlugSnapshot, &run.ModelNameSnapshot, &artifact, &workload,
		&argv, &differences, &status, &created, &started, &completed,
		&run.Build.LlamaRackVersion, &commit, &llamaRelease, &llamaBuild, &benchVersion,
		&benchFingerprint, &variant, &run.BenchmarkSchemaVersion, &run.ParserSchemaVersion,
		&hardwareSnapshot, &failure, &diagnostics,
	); err != nil {
		return Run{}, err
	}
	run.Status = Status(status)
	if !run.Status.Valid() {
		return Run{}, fmt.Errorf("invalid persisted benchmark status %q", status)
	}
	run.CreatedAt = time.Unix(created, 0).UTC()
	run.StartedAt = timeFromNull(started)
	run.CompletedAt = timeFromNull(completed)
	run.Build.LlamaRackCommit = commit.String
	run.Build.LlamaCppRelease = llamaRelease.String
	run.Build.LlamaCppBuild = llamaBuild.String
	run.Build.LlamaBenchVersion = benchVersion.String
	run.Build.LlamaBenchFingerprint = benchFingerprint.String
	run.Build.RuntimeVariant = variant.String
	run.Failure = failure.String
	run.DiagnosticOutput = diagnostics.String
	for _, item := range []struct {
		name string
		data string
		out  any
	}{
		{"instance_config_snapshot", instanceConfig, &run.InstanceConfig},
		{"artifact_snapshot", artifact, &run.Artifact},
		{"workload_profile", workload, &run.Workload},
		{"resolved_argv", argv, &run.ResolvedArgv},
		{"mapping_differences", differences, &run.MappingDifferences},
		{"hardware_snapshot", hardwareSnapshot, &run.Hardware},
	} {
		if err := json.Unmarshal([]byte(item.data), item.out); err != nil {
			return Run{}, fmt.Errorf("decode %s: %w", item.name, err)
		}
	}
	return run, nil
}

func loadResultsByRunIDs(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, ids []string) (map[string][]Result, error) {
	out := make(map[string][]Result, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for index, id := range ids {
		placeholders[index] = "?"
		args[index] = id
	}
	rows, err := q.QueryContext(ctx, `SELECT run_id,case_index,case_id,prompt_tokens,generation_tokens,repetitions,avg_ns,stddev_ns,avg_ts,stddev_ts,raw_fields FROM benchmark_results WHERE run_id IN (`+strings.Join(placeholders, ",")+`) ORDER BY run_id, case_index`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var runID string
		var result Result
		var avgNS, stddevNS sql.NullInt64
		var avgTS, stddevTS sql.NullFloat64
		var raw string
		if err := rows.Scan(&runID, &result.CaseIndex, &result.CaseID, &result.PromptTokens, &result.GenerationTokens, &result.Repetitions, &avgNS, &stddevNS, &avgTS, &stddevTS, &raw); err != nil {
			return nil, err
		}
		if avgNS.Valid {
			result.AverageNS = avgNS.Int64
		}
		if stddevNS.Valid {
			result.StdDevNS = stddevNS.Int64
		}
		if avgTS.Valid {
			result.AverageTokensPS = avgTS.Float64
		}
		if stddevTS.Valid {
			result.StdDevTokensPS = stddevTS.Float64
		}
		result.RawFields = json.RawMessage(raw)
		out[runID] = append(out[runID], result)
	}
	return out, rows.Err()
}

func transitionResult(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id string, result sql.Result) error {
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 0 {
		return nil
	}
	var exists int
	err = q.QueryRowContext(ctx, `SELECT 1 FROM benchmark_runs WHERE id=?`, strings.TrimSpace(id)).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	return ErrTransitionConflict
}

func marshalJSON(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func nullableTime(value *time.Time) any {
	if value == nil || value.IsZero() {
		return nil
	}
	return value.UTC().Unix()
}

func timeFromNull(value sql.NullInt64) *time.Time {
	if !value.Valid {
		return nil
	}
	resolved := time.Unix(value.Int64, 0).UTC()
	return &resolved
}

func nullIfEmpty(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func nullInt64(value int64) any {
	if value == 0 {
		return nil
	}
	return value
}

func nullFloat64(value float64) any {
	if value == 0 {
		return nil
	}
	return value
}
