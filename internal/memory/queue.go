package memory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type JobType string

const (
	JobSession JobType = "session"
	JobProject JobType = "project"
)

type EnqueueJob struct {
	ID                   string
	Type                 JobType
	ConversationID       string
	ProjectKey           string
	ToMessageIndex       int
	HistoryChars         int
	ToolCallCount        int
	LastAssistantHasTool bool
	Payload              []byte
}

type MemoryJob struct {
	EnqueueJob
	Attempt        int
	PreparedResult []byte
}

type QueueCursor struct {
	MessageIndex int
	HistoryChars int
	ToolCalls    int
}

type QueueStats struct {
	Pending int `json:"pending"`
	Running int `json:"running"`
	Failed  int `json:"failed"`
}

type DurableQueue struct {
	db *sql.DB
}

func OpenDurableQueue(path string) (*DurableQueue, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("memory queue: create directory: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("memory queue: open: %w", err)
	}
	db.SetMaxOpenConns(1)
	q := &DurableQueue{db: db}
	if err := q.initialize(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return q, nil
}

func (q *DurableQueue) initialize(ctx context.Context) error {
	for _, statement := range []string{
		`PRAGMA journal_mode=WAL`,
		`PRAGMA synchronous=FULL`,
		`PRAGMA busy_timeout=5000`,
		`PRAGMA foreign_keys=ON`,
		`CREATE TABLE IF NOT EXISTS memory_jobs (
			id TEXT PRIMARY KEY,
			kind TEXT NOT NULL CHECK (kind IN ('session','project')),
			conversation_id TEXT NOT NULL,
			project_key TEXT NOT NULL,
			to_message_index INTEGER NOT NULL,
			history_chars INTEGER NOT NULL DEFAULT 0,
			tool_call_count INTEGER NOT NULL DEFAULT 0,
			last_assistant_has_tool INTEGER NOT NULL DEFAULT 0,
			payload BLOB NOT NULL,
			prepared_result BLOB,
			status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','running','completed','failed')),
			attempt INTEGER NOT NULL DEFAULT 0,
			next_attempt_at INTEGER NOT NULL,
			lease_until INTEGER,
			last_error TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			UNIQUE(kind, conversation_id, project_key, to_message_index)
		)`,
		`CREATE INDEX IF NOT EXISTS memory_jobs_claim_idx ON memory_jobs(status, next_attempt_at, created_at)`,
		`CREATE TABLE IF NOT EXISTS memory_cursors (
			kind TEXT NOT NULL,
			conversation_id TEXT NOT NULL,
			project_key TEXT NOT NULL,
			message_index INTEGER NOT NULL,
			history_chars INTEGER NOT NULL,
			tool_call_count INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			PRIMARY KEY(kind, conversation_id, project_key)
		)`,
	} {
		if _, err := q.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("memory queue: initialize: %w", err)
		}
	}
	return nil
}

func (q *DurableQueue) Close() error { return q.db.Close() }

func (q *DurableQueue) JournalMode(ctx context.Context) (string, error) {
	var mode string
	err := q.db.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&mode)
	return strings.ToLower(mode), err
}

func (q *DurableQueue) Enqueue(ctx context.Context, job EnqueueJob) (bool, error) {
	if job.ID == "" || job.ConversationID == "" || job.ProjectKey == "" || len(job.Payload) == 0 {
		return false, errors.New("memory queue: incomplete job")
	}
	if job.Type != JobSession && job.Type != JobProject {
		return false, fmt.Errorf("memory queue: invalid job type %q", job.Type)
	}
	now := time.Now().UTC().UnixMilli()
	result, err := q.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO memory_jobs (
			id, kind, conversation_id, project_key, to_message_index,
			history_chars, tool_call_count, last_assistant_has_tool, payload,
			next_attempt_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		job.ID, job.Type, job.ConversationID, job.ProjectKey, job.ToMessageIndex,
		job.HistoryChars, job.ToolCallCount, boolInt(job.LastAssistantHasTool), job.Payload,
		now, now, now,
	)
	if err != nil {
		return false, fmt.Errorf("memory queue: enqueue: %w", err)
	}
	n, err := result.RowsAffected()
	return n > 0, err
}

func (q *DurableQueue) Claim(ctx context.Context, lease time.Duration) (*MemoryJob, error) {
	tx, err := q.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("memory queue: begin claim: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	now := time.Now().UTC()
	row := tx.QueryRowContext(ctx, `
		SELECT id, kind, conversation_id, project_key, to_message_index,
		       history_chars, tool_call_count, last_assistant_has_tool, payload,
		       attempt, COALESCE(prepared_result, X'')
		FROM memory_jobs
		WHERE status = 'pending' AND next_attempt_at <= ?
		ORDER BY created_at, id LIMIT 1`, now.UnixMilli())
	var job MemoryJob
	var hasTool int
	if err := row.Scan(&job.ID, &job.Type, &job.ConversationID, &job.ProjectKey,
		&job.ToMessageIndex, &job.HistoryChars, &job.ToolCallCount, &hasTool,
		&job.Payload, &job.Attempt, &job.PreparedResult); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("memory queue: select claim: %w", err)
	}
	job.LastAssistantHasTool = hasTool != 0
	result, err := tx.ExecContext(ctx, `
		UPDATE memory_jobs SET status='running', attempt=attempt+1, lease_until=?, updated_at=?
		WHERE id=? AND status='pending'`, now.Add(lease).UnixMilli(), now.UnixMilli(), job.ID)
	if err != nil {
		return nil, fmt.Errorf("memory queue: claim: %w", err)
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return nil, nil
	}
	job.Attempt++
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("memory queue: commit claim: %w", err)
	}
	return &job, nil
}

func (q *DurableQueue) SavePreparedResult(ctx context.Context, id string, result []byte) error {
	res, err := q.db.ExecContext(ctx, `UPDATE memory_jobs SET prepared_result=?, updated_at=? WHERE id=? AND status='running'`, result, time.Now().UTC().UnixMilli(), id)
	if err != nil {
		return fmt.Errorf("memory queue: save prepared result: %w", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("memory queue: running job %s not found", id)
	}
	return nil
}

func (q *DurableQueue) IsRunning(ctx context.Context, id string) (bool, error) {
	var count int
	err := q.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM memory_jobs WHERE id=? AND status='running'`, id).Scan(&count)
	return count == 1, err
}

func (q *DurableQueue) Complete(ctx context.Context, job MemoryJob) error {
	tx, err := q.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	now := time.Now().UTC().UnixMilli()
	result, err := tx.ExecContext(ctx, `UPDATE memory_jobs SET status='completed', lease_until=NULL, last_error='', updated_at=? WHERE id=? AND status='running'`, now, job.ID)
	if err != nil {
		return fmt.Errorf("memory queue: complete: %w", err)
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return tx.Commit()
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO memory_cursors(kind, conversation_id, project_key, message_index, history_chars, tool_call_count, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(kind, conversation_id, project_key) DO UPDATE SET
			message_index=MAX(message_index, excluded.message_index),
			history_chars=MAX(history_chars, excluded.history_chars),
			tool_call_count=MAX(tool_call_count, excluded.tool_call_count),
			updated_at=excluded.updated_at`,
		job.Type, job.ConversationID, job.ProjectKey, job.ToMessageIndex, job.HistoryChars, job.ToolCallCount, now); err != nil {
		return fmt.Errorf("memory queue: update cursor: %w", err)
	}
	return tx.Commit()
}

func (q *DurableQueue) Fail(ctx context.Context, job MemoryJob, cause error) error {
	delay := retryDelay(job.Attempt)
	status := "pending"
	if job.Attempt >= 5 {
		status = "failed"
	}
	message := cause.Error()
	if len(message) > 2000 {
		message = message[:2000]
	}
	_, err := q.db.ExecContext(ctx, `
		UPDATE memory_jobs SET status=?, next_attempt_at=?, lease_until=NULL, last_error=?, updated_at=? WHERE id=?`,
		status, time.Now().UTC().Add(delay).UnixMilli(), message, time.Now().UTC().UnixMilli(), job.ID)
	if err != nil {
		return fmt.Errorf("memory queue: fail job: %w", err)
	}
	return nil
}

func (q *DurableQueue) RecoverExpired(ctx context.Context) (int64, error) {
	now := time.Now().UTC().UnixMilli()
	res, err := q.db.ExecContext(ctx, `
		UPDATE memory_jobs SET status='pending', next_attempt_at=?, lease_until=NULL, updated_at=?
		WHERE status='running' AND lease_until <= ?`, now, now, now)
	if err != nil {
		return 0, fmt.Errorf("memory queue: recover leases: %w", err)
	}
	return res.RowsAffected()
}

// RecoverRunningOnStartup makes every job left running by a previous process
// immediately eligible. There cannot be a live owner before this queue is
// opened by the new server process.
func (q *DurableQueue) RecoverRunningOnStartup(ctx context.Context) (int64, error) {
	now := time.Now().UTC().UnixMilli()
	res, err := q.db.ExecContext(ctx, `
		UPDATE memory_jobs SET status='pending', next_attempt_at=?, lease_until=NULL, updated_at=?
		WHERE status='running'`, now, now)
	if err != nil {
		return 0, fmt.Errorf("memory queue: recover startup jobs: %w", err)
	}
	return res.RowsAffected()
}

func (q *DurableQueue) PruneCompleted(ctx context.Context, before time.Time) (int64, error) {
	result, err := q.db.ExecContext(ctx, `DELETE FROM memory_jobs WHERE status='completed' AND updated_at < ?`, before.UnixMilli())
	if err != nil {
		return 0, fmt.Errorf("memory queue: prune completed jobs: %w", err)
	}
	return result.RowsAffected()
}

func (q *DurableQueue) Cursor(ctx context.Context, kind JobType, conversationID, projectKey string) (QueueCursor, error) {
	var cursor QueueCursor
	err := q.db.QueryRowContext(ctx, `
		SELECT message_index, history_chars, tool_call_count FROM memory_cursors
		WHERE kind=? AND conversation_id=? AND project_key=?`, kind, conversationID, projectKey).
		Scan(&cursor.MessageIndex, &cursor.HistoryChars, &cursor.ToolCalls)
	if errors.Is(err, sql.ErrNoRows) {
		return QueueCursor{}, nil
	}
	return cursor, err
}

func (q *DurableQueue) LatestCovered(ctx context.Context, kind JobType, conversationID, projectKey string) (QueueCursor, error) {
	cursor, err := q.Cursor(ctx, kind, conversationID, projectKey)
	if err != nil {
		return QueueCursor{}, err
	}
	var pending QueueCursor
	err = q.db.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(to_message_index),0), COALESCE(MAX(history_chars),0), COALESCE(MAX(tool_call_count),0)
		FROM memory_jobs WHERE kind=? AND conversation_id=? AND project_key=? AND status IN ('pending','running')`,
		kind, conversationID, projectKey).Scan(&pending.MessageIndex, &pending.HistoryChars, &pending.ToolCalls)
	if err != nil {
		return QueueCursor{}, err
	}
	return QueueCursor{
		MessageIndex: max(cursor.MessageIndex, pending.MessageIndex),
		HistoryChars: max(cursor.HistoryChars, pending.HistoryChars),
		ToolCalls:    max(cursor.ToolCalls, pending.ToolCalls),
	}, nil
}

func (q *DurableQueue) Stats(ctx context.Context) (QueueStats, error) {
	var stats QueueStats
	rows, err := q.db.QueryContext(ctx, `SELECT status, COUNT(*) FROM memory_jobs WHERE status IN ('pending','running','failed') GROUP BY status`)
	if err != nil {
		return stats, err
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return stats, err
		}
		switch status {
		case "pending":
			stats.Pending = count
		case "running":
			stats.Running = count
		case "failed":
			stats.Failed = count
		}
	}
	return stats, rows.Err()
}

func (q *DurableQueue) ClearSession(ctx context.Context, conversationID string) error {
	return q.clearTarget(ctx, `conversation_id=?`, conversationID)
}

func (q *DurableQueue) ClearProject(ctx context.Context, projectKey string) error {
	return q.clearTarget(ctx, `project_key=?`, projectKey)
}

func (q *DurableQueue) clearTarget(ctx context.Context, predicate string, value string) error {
	tx, err := q.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM memory_jobs WHERE `+predicate, value); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM memory_cursors WHERE `+predicate, value); err != nil {
		return err
	}
	return tx.Commit()
}

func retryDelay(attempt int) time.Duration {
	delays := []time.Duration{5 * time.Second, 30 * time.Second, 2 * time.Minute, 10 * time.Minute}
	if attempt <= 0 {
		return delays[0]
	}
	if attempt > len(delays) {
		return delays[len(delays)-1]
	}
	return delays[attempt-1]
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
