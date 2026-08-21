package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/openai/openai-go"
	"github.com/sasuke39/open-warp/internal/llm"
	"github.com/sasuke39/open-warp/internal/memory"
)

const sessionExtractorPrompt = `You update a durable session memory file for a coding agent.
Return only Markdown.
Preserve every required heading exactly once: Session Title, Current State, Task Specification, Files And Functions, Workflow, Errors And Corrections, Tool Results Worth Keeping, Decisions, Key Results, Worklog.
Use only facts present in the previous notes and new conversation delta.
Merge duplicates, remove abandoned implementations, and keep the result concise but useful.
Never include secrets or credentials.
In Files And Functions, write real file/function paths.
In Errors And Corrections, write the failure cause and the fix.
In Tool Results Worth Keeping, keep only command results that will be useful later.
If A was replaced by A1, keep only A1 and a brief migration note.`

const projectExtractorPrompt = "You extract durable project knowledge from a conversation delta.\n" +
	"Return ONLY a JSON object with this exact structure, no other text:\n" +
	`{"updates":[{"path":"workflows.md","mode":"append_or_replace_section","section":"Build","content":"- go build ./... to build"},{"path":"known_issues.md","mode":"append_bullet","section":"Tool Pairing","content":"tool_call/tool_result must stay paired during compaction"}]}` + "\n\n" +
	"Rules:\n" +
	"- path must be one of: project_context.md, user_preferences.md, workflows.md, known_issues.md\n" +
	"- mode must be: append_or_replace_section, append_bullet, or replace_file\n" +
	"- section is the heading name (without #)\n" +
	"- content is the text to add\n" +
	"- Only extract facts that are durable across sessions, not temporary progress.\n" +
	"- Never include secrets, credentials, or API keys.\n" +
	`- If nothing worth remembering, return: {"updates":[]}`

type queuedMemoryPayload struct {
	Delta string `json:"delta"`
}

func (s *Server) startMemoryWorker() {
	if s.memoryQueue == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.memoryCancel = cancel
	s.memoryDone = make(chan struct{})
	if n, err := s.memoryQueue.RecoverRunningOnStartup(ctx); err != nil {
		log.Printf("[MEMORY] Failed to recover startup jobs: %v", err)
	} else if n > 0 {
		log.Printf("[MEMORY] Recovered %d interrupted jobs", n)
	}
	if n, err := s.memoryQueue.PruneCompleted(ctx, time.Now().AddDate(0, 0, -7)); err != nil {
		log.Printf("[MEMORY] Failed to prune completed jobs: %v", err)
	} else if n > 0 {
		log.Printf("[MEMORY] Pruned %d completed jobs", n)
	}
	go s.memoryWorkerLoop(ctx)
}

func (s *Server) memoryWorkerLoop(ctx context.Context) {
	defer close(s.memoryDone)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if err := s.drainOneMemoryJob(ctx); err != nil && ctx.Err() == nil {
			log.Printf("[MEMORY] Worker error: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-s.memoryWake:
		case <-ticker.C:
		}
	}
}

func (s *Server) drainOneMemoryJob(ctx context.Context) error {
	job, err := s.memoryQueue.Claim(ctx, 2*time.Minute)
	if err != nil || job == nil {
		return err
	}
	log.Printf("[MEMORY] Worker claimed job=%s type=%s conv=%s project=%s attempt=%d", job.ID, job.Type, job.ConversationID, job.ProjectKey, job.Attempt)
	if err := s.processMemoryJob(ctx, *job); err != nil {
		if failErr := s.memoryQueue.Fail(context.Background(), *job, err); failErr != nil {
			return fmt.Errorf("process job: %v; persist failure: %w", err, failErr)
		}
		log.Printf("[MEMORY] Job failed job=%s attempt=%d: %v", job.ID, job.Attempt, err)
		return nil
	}
	if err := s.memoryQueue.Complete(context.Background(), *job); err != nil {
		return err
	}
	log.Printf("[MEMORY] Job completed job=%s type=%s", job.ID, job.Type)
	select {
	case s.memoryWake <- struct{}{}:
	default:
	}
	return nil
}

func (s *Server) processMemoryJob(ctx context.Context, job memory.MemoryJob) error {
	var payload queuedMemoryPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return fmt.Errorf("decode job payload: %w", err)
	}
	switch job.Type {
	case memory.JobSession:
		return s.processSessionMemoryJob(ctx, job, payload)
	case memory.JobProject:
		return s.processProjectMemoryJob(ctx, job, payload)
	default:
		return fmt.Errorf("unknown job type %q", job.Type)
	}
}

func (s *Server) processSessionMemoryJob(ctx context.Context, job memory.MemoryJob, payload queuedMemoryPayload) error {
	result := string(job.PreparedResult)
	if result == "" {
		notes, err := s.memoryStore.ReadSessionNotes(job.ConversationID)
		if err != nil {
			notes = memory.DefaultSessionNotes("Session " + memory.ShortID(job.ConversationID))
		}
		request := []openai.ChatCompletionMessageParamUnion{llm.MakeUserMessage(fmt.Sprintf(
			"Previous notes.md:\n\n%s\n\nNew conversation delta:\n\n%s\n\nUpdate the notes.md with the new information. Return the complete updated notes.md.",
			notes, payload.Delta))}
		callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		result, err = s.memoryLLMClient(job.ConversationID).CompleteText(callCtx, sessionExtractorPrompt, request)
		if err != nil {
			return fmt.Errorf("session extraction: %w", err)
		}
		if err := memory.ValidateSessionNotes(result); err != nil {
			return fmt.Errorf("invalid session extraction: %w", err)
		}
		if memory.ContainsSecret(result) {
			return fmt.Errorf("session extraction contains a secret")
		}
		if err := s.memoryQueue.SavePreparedResult(ctx, job.ID, []byte(result)); err != nil {
			return err
		}
	}
	s.memoryMutationMu.Lock()
	defer s.memoryMutationMu.Unlock()
	active, err := s.memoryQueue.IsRunning(ctx, job.ID)
	if err != nil {
		return err
	}
	if !active {
		return nil
	}
	if err := s.memoryStore.WriteSessionNotes(job.ConversationID, result); err != nil {
		return fmt.Errorf("write session notes: %w", err)
	}
	meta := &memory.SessionMeta{
		ConversationID: job.ConversationID, ProjectKey: job.ProjectKey,
		LastMessageIndex: job.ToMessageIndex, LastHistoryChars: job.HistoryChars,
		LastToolCallCount: job.ToolCallCount, UpdatedAt: time.Now().UTC(),
	}
	if err := s.memoryStore.WriteSessionMeta(meta); err != nil {
		return fmt.Errorf("write session cursor: %w", err)
	}
	_ = s.memoryStore.AppendEvent(memory.Event{Type: "session_memory_updated", ConversationID: job.ConversationID, ProjectKey: job.ProjectKey, Path: memory.SessionNotesRelPath(job.ConversationID)})
	return nil
}

func (s *Server) processProjectMemoryJob(ctx context.Context, job memory.MemoryJob, payload queuedMemoryPayload) error {
	var writes []memory.PreparedProjectWrite
	var patch memory.MemoryPatch
	if len(job.PreparedResult) == 0 {
		request := []openai.ChatCompletionMessageParamUnion{llm.MakeUserMessage(
			"Conversation delta:\n\n" + payload.Delta + "\n\nExtract durable project knowledge as a JSON patch.")}
		callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		result, err := s.memoryLLMClient(job.ConversationID).CompleteText(callCtx, projectExtractorPrompt, request)
		if err != nil {
			return fmt.Errorf("project extraction: %w", err)
		}
		patch, err = memory.ParseMemoryPatch([]byte(result))
		if err != nil {
			return err
		}
	} else if err := json.Unmarshal(job.PreparedResult, &writes); err != nil {
		return fmt.Errorf("decode prepared project writes: %w", err)
	}
	s.memoryMutationMu.Lock()
	defer s.memoryMutationMu.Unlock()
	active, err := s.memoryQueue.IsRunning(ctx, job.ID)
	if err != nil {
		return err
	}
	if !active {
		return nil
	}
	if err := s.memoryStore.InitProjectMemory(job.ProjectKey); err != nil {
		return fmt.Errorf("initialize project memory: %w", err)
	}
	if len(job.PreparedResult) == 0 {
		writes, err = s.memoryStore.PrepareProjectWrites(job.ProjectKey, patch)
		if err != nil {
			return err
		}
		prepared, err := json.Marshal(writes)
		if err != nil {
			return err
		}
		if err := s.memoryQueue.SavePreparedResult(ctx, job.ID, prepared); err != nil {
			return err
		}
	}
	if err := s.memoryStore.WritePreparedProject(job.ProjectKey, writes); err != nil {
		return err
	}
	_ = s.memoryStore.AppendEvent(memory.Event{Type: "project_memory_updated", ConversationID: job.ConversationID, ProjectKey: job.ProjectKey})
	return nil
}

func (s *Server) memoryLLMClient(conversationID string) *llm.Client {
	s.mu.RLock()
	conv := s.conversations[conversationID]
	s.mu.RUnlock()
	if conv != nil && conv.client != nil {
		return conv.client
	}
	return llm.NewClient(s.cfg)
}

func (s *Server) enqueueMemoryUpdates(conv *Conversation, convID string) {
	if s.memoryQueue == nil || s.memoryStore == nil {
		return
	}
	stats := memory.SessionStats{
		MessageCount: len(conv.history), HistoryChars: estimateHistoryChars(conv.history),
		ToolCallCount: countToolCalls(conv.history), LastAssistantHasToolCall: lastAssistantHasToolCall(conv.history),
	}
	projectKey := conv.ProjectKey
	if projectKey == "" {
		projectKey = s.resolveProjectKey(convID)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	if s.cfg.Memory.IsSessionEnabled() {
		s.enqueueMemoryKind(ctx, conv, convID, projectKey, memory.JobSession, stats)
	}
	if s.cfg.Memory.IsAutoEnabled() {
		s.enqueueMemoryKind(ctx, conv, convID, projectKey, memory.JobProject, stats)
	}
}

func (s *Server) enqueueMemoryKind(ctx context.Context, conv *Conversation, convID, projectKey string, kind memory.JobType, stats memory.SessionStats) {
	covered, err := s.memoryQueue.LatestCovered(ctx, kind, convID, projectKey)
	if err != nil {
		log.Printf("[MEMORY] Cannot read queue cursor type=%s: %v", kind, err)
		return
	}
	if kind == memory.JobSession {
		if meta, err := s.memoryStore.ReadSessionMeta(convID); err == nil && meta.ProjectKey == projectKey && meta.LastMessageIndex > covered.MessageIndex {
			covered = memory.QueueCursor{MessageIndex: meta.LastMessageIndex, HistoryChars: meta.LastHistoryChars, ToolCalls: meta.LastToolCallCount}
		}
		var meta *memory.SessionMeta
		if covered.MessageIndex > 0 {
			meta = &memory.SessionMeta{LastMessageIndex: covered.MessageIndex, LastHistoryChars: covered.HistoryChars, LastToolCallCount: covered.ToolCalls}
		}
		if !memory.ShouldUpdateSessionMemory(meta, stats) {
			return
		}
	} else if !shouldUpdateProjectMemory(covered, stats) {
		return
	}
	start := covered.MessageIndex
	if start < 0 || start >= len(conv.history) {
		start = 0
	}
	delta := memory.RedactSecrets(s.summarizeDelta(conv.history[start:]))
	if delta == "" {
		return
	}
	payload, err := json.Marshal(queuedMemoryPayload{Delta: delta})
	if err != nil {
		return
	}
	job := memory.EnqueueJob{
		ID: uuid.NewString(), Type: kind, ConversationID: convID, ProjectKey: projectKey,
		ToMessageIndex: stats.MessageCount, HistoryChars: stats.HistoryChars,
		ToolCallCount: stats.ToolCallCount, LastAssistantHasTool: stats.LastAssistantHasToolCall,
		Payload: payload,
	}
	inserted, err := s.memoryQueue.Enqueue(ctx, job)
	if err != nil {
		log.Printf("[MEMORY] Failed durable enqueue type=%s conv=%s project=%s: %v", kind, convID, projectKey, err)
		return
	}
	if inserted {
		log.Printf("[MEMORY] Durable enqueue job=%s type=%s conv=%s project=%s through=%d", job.ID, kind, convID, projectKey, stats.MessageCount)
		select {
		case s.memoryWake <- struct{}{}:
		default:
		}
	}
}

func shouldUpdateProjectMemory(cursor memory.QueueCursor, stats memory.SessionStats) bool {
	if cursor.MessageIndex == 0 {
		return stats.MessageCount >= 6 && stats.HistoryChars >= 8000
	}
	if stats.HistoryChars-cursor.HistoryChars < 12000 {
		return false
	}
	return stats.ToolCallCount-cursor.ToolCalls >= 3 || !stats.LastAssistantHasToolCall
}

func (s *Server) requestConversationSave() {
	select {
	case s.persistWake <- struct{}{}:
	default:
	}
}

func (s *Server) persistenceLoop() {
	defer close(s.persistDone)
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
		case <-s.persistWake:
		}
		if err := s.saveConversations(); err != nil {
			log.Printf("Failed to persist conversations (background): %v", err)
		}
	}
}

func (s *Server) closeBackground(ctx context.Context) error {
	s.runtimeMu.Lock()
	runtimeDriver := s.runtimeDriver
	s.runtimeDriver = nil
	s.runtimeMu.Unlock()
	if runtimeDriver != nil {
		if err := runtimeDriver.Close(ctx); err != nil {
			return err
		}
	}
	s.stopOnce.Do(func() { close(s.stopCh) })
	if s.memoryCancel != nil {
		s.memoryCancel()
	}
	if s.memoryDone != nil {
		select {
		case <-s.memoryDone:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	select {
	case <-s.persistDone:
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := s.saveConversations(); err != nil {
		return err
	}
	if s.memoryQueue != nil {
		return s.memoryQueue.Close()
	}
	return nil
}
