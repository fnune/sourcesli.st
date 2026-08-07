// Package etl moves Debian data from an upstream snapshot into our own schema.
//
// The three stages are deliberately separate types. Extract owns everything
// that talks to Debian and is the only part with an opinion about upstream
// etiquette. Transform owns the SQL that knows UDD's column names. Load owns
// the swap that publishes a finished batch. A change to one rarely reaches the
// others, which is the point.
package etl

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrUnchanged reports that upstream has published nothing new since the state
// handed to Restore. It is an ordinary outcome, not a failure: the whole design
// depends on a no-op sync being cheap enough to run on a schedule.
var ErrUnchanged = errors.New("etl: upstream unchanged since the last run")

// Clock supplies the current time. Injected rather than called directly so that
// a test can assert on what a run recorded without racing the wall clock.
type Clock interface {
	Now() time.Time
}

// SystemClock reads the wall clock. It is the Clock every binary passes.
var SystemClock Clock = systemClock{}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now().UTC() }

// UpstreamState is what the last successful run consumed. It is the input to a
// conditional request, so an empty value means "we have nothing, send it all".
type UpstreamState struct {
	ETag         string
	LastModified string
	SyncedAt     time.Time
}

// Snapshot identifies the upstream data a Source restored, so the pipeline can
// record which upstream publication produced the rows now being served.
type Snapshot struct {
	ETag         string
	LastModified string
}

// Source restores an upstream snapshot into the udd_raw schema of the target
// database, and reports what it restored.
//
// Declared here because the pipeline is its consumer. Implementations live in
// this package for the real dump and for fixtures, and neither knows about the
// other.
type Source interface {
	// Name identifies the source in logs and in the sync_runs table.
	Name() string

	// Restore returns ErrUnchanged when state already describes the newest
	// upstream publication, having transferred as little as the protocol allows.
	Restore(ctx context.Context, state UpstreamState) (Snapshot, error)
}

// Pipeline runs extract, transform, and load against one database, recording
// every attempt in sync_runs whether or not it succeeded.
type Pipeline struct {
	pool   *pgxpool.Pool
	source Source
	loader *Loader
	clock  Clock
	logger *slog.Logger
}

// NewPipeline wires a pipeline. Every dependency is a parameter because every
// one of them is volatile: a database, a network client, the clock, and a sink
// for logs.
func NewPipeline(pool *pgxpool.Pool, source Source, loader *Loader, clock Clock, logger *slog.Logger) *Pipeline {
	return &Pipeline{
		pool:   pool,
		source: source,
		loader: loader,
		clock:  clock,
		logger: logger,
	}
}

// Outcome describes how a run ended. It is written to sync_runs.outcome.
type Outcome string

// The outcomes a run can record.
const (
	// OutcomeUpdated means new upstream data was published to the live tables.
	OutcomeUpdated Outcome = "updated"
	// OutcomeUnchanged means upstream had nothing new, so nothing was rebuilt.
	OutcomeUnchanged Outcome = "unchanged"
	// OutcomeFailed means the run stopped and the previous data is still live.
	OutcomeFailed Outcome = "failed"
)

// Result reports what a run did, for a caller that wants to log or exit on it.
type Result struct {
	Outcome     Outcome
	RowsWritten int64
	Snapshot    Snapshot
}

// Run performs one sync.
//
// A failure at any stage leaves the previously loaded data live and untouched,
// because nothing is published until Load swaps. That is what makes a failed
// sync a non-event rather than an outage.
func (p *Pipeline) Run(ctx context.Context) (Result, error) {
	startedAt := p.clock.Now()

	state, err := p.readUpstreamState(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("reading upstream state: %w", err)
	}

	state, err = p.discardStateIfDataIsGone(ctx, state)
	if err != nil {
		return Result{}, err
	}

	runID, err := p.beginRun(ctx, startedAt)
	if err != nil {
		return Result{}, fmt.Errorf("recording the start of a sync run: %w", err)
	}

	result, runErr := p.run(ctx, state)
	if runErr != nil {
		if finishErr := p.finishRun(ctx, runID, OutcomeFailed, 0, Snapshot{}, runErr); finishErr != nil {
			return Result{}, errors.Join(runErr, fmt.Errorf("recording the failure: %w", finishErr))
		}
		return Result{Outcome: OutcomeFailed}, runErr
	}

	if err := p.finishRun(ctx, runID, result.Outcome, result.RowsWritten, result.Snapshot, nil); err != nil {
		return result, fmt.Errorf("recording the end of a sync run: %w", err)
	}

	if result.Outcome == OutcomeUpdated {
		if err := p.recordUpstreamState(ctx, result.Snapshot); err != nil {
			return result, fmt.Errorf("recording upstream state: %w", err)
		}
	}

	return result, nil
}

func (p *Pipeline) run(ctx context.Context, state UpstreamState) (Result, error) {
	p.logger.InfoContext(ctx, "sync starting",
		slog.String("source", p.source.Name()),
		slog.String("known_etag", state.ETag))

	snapshot, err := p.source.Restore(ctx, state)
	switch {
	case errors.Is(err, ErrUnchanged):
		p.logger.InfoContext(ctx, "upstream unchanged, nothing to rebuild",
			slog.String("etag", state.ETag))
		return Result{Outcome: OutcomeUnchanged}, nil
	case err != nil:
		return Result{}, fmt.Errorf("restoring the upstream snapshot: %w", err)
	}

	rows, err := p.loader.Load(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("loading the restored snapshot: %w", err)
	}

	p.logger.InfoContext(ctx, "sync published new data",
		slog.String("etag", snapshot.ETag),
		slog.Int64("rows", rows))

	return Result{Outcome: OutcomeUpdated, RowsWritten: rows, Snapshot: snapshot}, nil
}

// discardStateIfDataIsGone forgets what the last run consumed when the rows it
// produced are no longer there. Kept because a conditional request carrying that
// state answers 304, which publishes nothing and reports success.
func (p *Pipeline) discardStateIfDataIsGone(
	ctx context.Context,
	state UpstreamState,
) (UpstreamState, error) {
	if state.ETag == "" && state.LastModified == "" {
		return state, nil
	}

	var published bool
	if err := p.pool.QueryRow(
		ctx, `SELECT EXISTS (SELECT 1 FROM binary_packages)`,
	).Scan(&published); err != nil {
		return UpstreamState{}, fmt.Errorf("checking whether the live tables hold data: %w", err)
	}
	if published {
		return state, nil
	}

	p.logger.WarnContext(ctx, "upstream state describes data the database no longer holds, rebuilding",
		slog.String("discarded_etag", state.ETag))
	return UpstreamState{}, nil
}

func (p *Pipeline) readUpstreamState(ctx context.Context) (UpstreamState, error) {
	var state UpstreamState
	var etag, lastModified *string
	var syncedAt *time.Time

	err := p.pool.QueryRow(
		ctx,
		`SELECT etag, last_modified, synced_at FROM upstream_state WHERE id`,
	).Scan(&etag, &lastModified, &syncedAt)
	if err != nil {
		if errors.Is(err, errNoRows) {
			return UpstreamState{}, nil
		}
		return UpstreamState{}, err
	}

	if etag != nil {
		state.ETag = *etag
	}
	if lastModified != nil {
		state.LastModified = *lastModified
	}
	if syncedAt != nil {
		state.SyncedAt = *syncedAt
	}
	return state, nil
}

func (p *Pipeline) recordUpstreamState(ctx context.Context, snapshot Snapshot) error {
	_, err := p.pool.Exec(ctx,
		`INSERT INTO upstream_state (id, etag, last_modified, synced_at)
		 VALUES (true, $1, $2, $3)
		 ON CONFLICT (id) DO UPDATE SET
		     etag = excluded.etag,
		     last_modified = excluded.last_modified,
		     synced_at = excluded.synced_at`,
		snapshot.ETag, snapshot.LastModified, p.clock.Now())
	return err
}

func (p *Pipeline) beginRun(ctx context.Context, startedAt time.Time) (int64, error) {
	var id int64
	err := p.pool.QueryRow(
		ctx,
		`INSERT INTO sync_runs (source, started_at, outcome) VALUES ($1, $2, 'running') RETURNING id`,
		p.source.Name(), startedAt,
	).Scan(&id)
	return id, err
}

func (p *Pipeline) finishRun(
	ctx context.Context,
	id int64,
	outcome Outcome,
	rows int64,
	snapshot Snapshot,
	runErr error,
) error {
	var message *string
	if runErr != nil {
		text := runErr.Error()
		message = &text
	}

	_, err := p.pool.Exec(ctx,
		`UPDATE sync_runs
		 SET finished_at = $2, outcome = $3, rows_written = $4, etag = $5, last_modified = $6, error = $7
		 WHERE id = $1`,
		id, p.clock.Now(), string(outcome), rows, snapshot.ETag, snapshot.LastModified, message)
	return err
}
