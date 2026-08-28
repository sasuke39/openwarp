package main

import (
	"fmt"
	"sync"
	"time"
)

type steerStatus string

const (
	steerSubmitting steerStatus = "submitting"
	steerAccepted   steerStatus = "accepted"
	steerApplied    steerStatus = "applied"
	steerFailed     steerStatus = "failed"
	steerCancelled  steerStatus = "cancelled"
)

type steerRecord struct {
	ID        string      `json:"steer_id"`
	TaskID    string      `json:"-"`
	Prompt    string      `json:"-"`
	Status    steerStatus `json:"status"`
	Error     string      `json:"error,omitempty"`
	UpdatedAt time.Time   `json:"-"`
}

type steerRegistry struct {
	mu      sync.Mutex
	records map[string]steerRecord
	order   []string
}

func (r *steerRegistry) reserve(id, taskID, prompt string) (steerRecord, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.records[id]; ok {
		if existing.TaskID != taskID || existing.Prompt != prompt {
			return steerRecord{}, false, fmt.Errorf("steer_id already belongs to another request")
		}
		return existing, false, nil
	}
	if r.records == nil {
		r.records = make(map[string]steerRecord)
	}
	record := steerRecord{ID: id, TaskID: taskID, Prompt: prompt, Status: steerSubmitting, UpdatedAt: time.Now().UTC()}
	r.records[id] = record
	r.order = append(r.order, id)
	for len(r.order) > 512 {
		delete(r.records, r.order[0])
		r.order = r.order[1:]
	}
	return record, true, nil
}

func (r *steerRegistry) update(id string, status steerStatus, message string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	record, ok := r.records[id]
	if !ok {
		return
	}
	record.Status, record.Error, record.UpdatedAt = status, message, time.Now().UTC()
	r.records[id] = record
}

func (r *steerRegistry) load(taskID, id string) (steerRecord, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	record, ok := r.records[id]
	return record, ok && record.TaskID == taskID
}

func (r *steerRegistry) cancelTask(taskID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, record := range r.records {
		if record.TaskID == taskID && (record.Status == steerSubmitting || record.Status == steerAccepted) {
			record.Status, record.UpdatedAt = steerCancelled, time.Now().UTC()
			r.records[id] = record
		}
	}
}
