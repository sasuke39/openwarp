package memory

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestDurableQueueUsesWALAndSurvivesReopen(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "memory-queue.db")
	q, err := OpenDurableQueue(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := q.JournalMode(context.Background()); err != nil || got != "wal" {
		t.Fatalf("journal mode = %q, err=%v; want wal", got, err)
	}
	job := EnqueueJob{
		ID: "job-1", Type: JobSession, ConversationID: "conv-1",
		ProjectKey: "project-a", ToMessageIndex: 4, HistoryChars: 12000,
		ToolCallCount: 3, Payload: []byte(`{"history":[]}`),
	}
	if inserted, err := q.Enqueue(context.Background(), job); err != nil || !inserted {
		t.Fatalf("enqueue inserted=%v err=%v", inserted, err)
	}
	job.ID = "same-cursor-different-id"
	if inserted, err := q.Enqueue(context.Background(), job); err != nil || inserted {
		t.Fatalf("duplicate cursor inserted=%v err=%v", inserted, err)
	}
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}

	q, err = OpenDurableQueue(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = q.Close() })
	claimed, err := q.Claim(context.Background(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if claimed == nil || claimed.ID != "job-1" || claimed.ProjectKey != "project-a" {
		t.Fatalf("claimed = %#v", claimed)
	}
}

func TestDurableQueueRecoversExpiredLeaseAndAdvancesCursor(t *testing.T) {
	q, err := OpenDurableQueue(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = q.Close() })
	ctx := context.Background()
	job := EnqueueJob{ID: "job-1", Type: JobProject, ConversationID: "conv-1", ProjectKey: "project-a", ToMessageIndex: 9, HistoryChars: 9000, Payload: []byte("x")}
	if _, err := q.Enqueue(ctx, job); err != nil {
		t.Fatal(err)
	}
	claimed, err := q.Claim(ctx, -time.Second)
	if err != nil || claimed == nil {
		t.Fatalf("claim=%#v err=%v", claimed, err)
	}
	if n, err := q.RecoverExpired(ctx); err != nil || n != 1 {
		t.Fatalf("recovered=%d err=%v", n, err)
	}
	claimed, err = q.Claim(ctx, time.Minute)
	if err != nil || claimed == nil {
		t.Fatalf("reclaim=%#v err=%v", claimed, err)
	}
	if err := q.Complete(ctx, *claimed); err != nil {
		t.Fatal(err)
	}
	cursor, err := q.Cursor(ctx, JobProject, "conv-1", "project-a")
	if err != nil {
		t.Fatal(err)
	}
	if cursor.MessageIndex != 9 || cursor.HistoryChars != 9000 {
		t.Fatalf("cursor=%+v", cursor)
	}
}

func TestDurableQueueKeepsProjectsIsolated(t *testing.T) {
	q, err := OpenDurableQueue(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = q.Close() })
	ctx := context.Background()
	for _, project := range []string{"project-a", "project-b"} {
		_, err := q.Enqueue(ctx, EnqueueJob{ID: "job-" + project, Type: JobProject, ConversationID: "conv-1", ProjectKey: project, ToMessageIndex: 6, Payload: []byte(project)})
		if err != nil {
			t.Fatal(err)
		}
	}
	first, _ := q.Claim(ctx, time.Minute)
	if first == nil || first.ProjectKey != "project-a" {
		t.Fatalf("first=%#v", first)
	}
	if err := q.Complete(ctx, *first); err != nil {
		t.Fatal(err)
	}
	cursorA, _ := q.Cursor(ctx, JobProject, "conv-1", "project-a")
	cursorB, _ := q.Cursor(ctx, JobProject, "conv-1", "project-b")
	if cursorA.MessageIndex != 6 || cursorB.MessageIndex != 0 {
		t.Fatalf("cursorA=%+v cursorB=%+v", cursorA, cursorB)
	}
}

func TestDurableQueuePersistsPreparedResultAndRecoversRunningOnStartup(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "queue.db")
	q, err := OpenDurableQueue(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, _ = q.Enqueue(ctx, EnqueueJob{ID: "job-1", Type: JobSession, ConversationID: "conv-1", ProjectKey: "project-a", ToMessageIndex: 4, Payload: []byte("payload")})
	job, err := q.Claim(ctx, time.Hour)
	if err != nil || job == nil {
		t.Fatalf("claim=%#v err=%v", job, err)
	}
	if err := q.SavePreparedResult(ctx, job.ID, []byte("prepared notes")); err != nil {
		t.Fatal(err)
	}
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}

	q, err = OpenDurableQueue(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = q.Close() })
	if n, err := q.RecoverRunningOnStartup(ctx); err != nil || n != 1 {
		t.Fatalf("recovered=%d err=%v", n, err)
	}
	job, err = q.Claim(ctx, time.Minute)
	if err != nil || job == nil || string(job.PreparedResult) != "prepared notes" {
		t.Fatalf("reclaimed=%#v err=%v", job, err)
	}
}

func TestDurableQueueFailureIsRetainedForRetry(t *testing.T) {
	q, err := OpenDurableQueue(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = q.Close() })
	ctx := context.Background()
	_, _ = q.Enqueue(ctx, EnqueueJob{ID: "job-1", Type: JobSession, ConversationID: "conv-1", ProjectKey: "project-a", Payload: []byte("payload")})
	job, _ := q.Claim(ctx, time.Minute)
	if err := q.Fail(ctx, *job, context.DeadlineExceeded); err != nil {
		t.Fatal(err)
	}
	stats, err := q.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Pending != 1 || stats.Failed != 0 {
		t.Fatalf("stats=%+v", stats)
	}
	if immediate, err := q.Claim(ctx, time.Minute); err != nil || immediate != nil {
		t.Fatalf("retry should honor backoff: job=%#v err=%v", immediate, err)
	}
}

func TestDurableQueueClearProjectRemovesOnlyThatProject(t *testing.T) {
	q, err := OpenDurableQueue(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = q.Close() })
	ctx := context.Background()
	for _, project := range []string{"project-a", "project-b"} {
		_, _ = q.Enqueue(ctx, EnqueueJob{ID: project, Type: JobProject, ConversationID: "conv-1", ProjectKey: project, ToMessageIndex: 6, Payload: []byte(project)})
	}
	if err := q.ClearProject(ctx, "project-a"); err != nil {
		t.Fatal(err)
	}
	job, err := q.Claim(ctx, time.Minute)
	if err != nil || job == nil || job.ProjectKey != "project-b" {
		t.Fatalf("remaining job=%#v err=%v", job, err)
	}
}
