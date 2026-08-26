package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cascade-demoops/backend/internal/app"
	"cascade-demoops/backend/internal/directtransport"
	"cascade-demoops/backend/internal/model"
)

func TestWorkerFinalizationContextOutlivesExpiredExecutionContext(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.Background())
	cancelParent()

	ctx, cancel := workerFinalizationContext(parent)
	defer cancel()
	select {
	case <-ctx.Done():
		t.Fatalf("finalization context inherited the expired execution context: %v", ctx.Err())
	default:
	}
	deadline, ok := ctx.Deadline()
	remaining := time.Until(deadline)
	if !ok || remaining < workerFinalizationTimeout-time.Second || remaining > workerFinalizationTimeout {
		t.Fatalf("finalization context is not independently bounded: deadline=%v ok=%t", deadline, ok)
	}
}

func TestWorkerInterruptionIsDistinctFromExecutionTimeout(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.Background())
	execution, cancelExecution := context.WithCancel(parent)
	cancelExecution()
	if workerWasInterrupted(parent) {
		t.Fatal("an execution-local timeout must not be treated as a service restart")
	}
	cancelParent()
	if !workerWasInterrupted(parent) || execution.Err() == nil {
		t.Fatal("a process-level cancellation must release the job for checkpoint recovery")
	}
}

func TestWorkerClaimRejectsProtocolMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/worker/jobs/claim" || request.Header.Get("Authorization") != "Bearer worker-test-token" {
			http.Error(response, "unexpected request", http.StatusBadRequest)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(response).Encode(directtransport.WorkerJob{ProtocolVersion: "cascade.browser_agent_worker.v0", JobID: "job_mismatch"})
	}))
	t.Cleanup(server.Close)
	w := &worker{client: server.Client(), baseURL: server.URL, token: "worker-test-token"}

	_, ok, err := w.claim(context.Background())
	if err == nil || ok || !strings.Contains(err.Error(), "worker protocol mismatch") {
		t.Fatalf("mismatched worker claim was accepted: ok=%v err=%v", ok, err)
	}
}

func TestRecoverOrphanedStageEventLogUploadsOnce(t *testing.T) {
	root := t.TempDir()
	jobID := "job_interrupted"
	recording := filepath.Join(root, "jobs", jobID, "recording")
	if err := os.MkdirAll(recording, 0o700); err != nil {
		t.Fatal(err)
	}
	data := []byte("{\"event_type\":\"stage_failed\"}\n")
	if err := os.WriteFile(filepath.Join(recording, "browser-agent-stage-events.jsonl"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		calls++
		if request.URL.Path != "/v1/worker/jobs/"+jobID+"/artifacts/stage_event_log_"+jobID || request.Header.Get("X-Artifact-Recovery") != "stage-event-log-v1" || request.Header.Get("X-Artifact-Kind") != "browser_agent_stage_event_log" {
			http.Error(response, "unexpected recovery request", http.StatusBadRequest)
			return
		}
		if request.Header.Get("X-Artifact-SHA256") != fmt.Sprintf("%x", sha256.Sum256(data)) {
			http.Error(response, "bad digest", http.StatusUnprocessableEntity)
			return
		}
		response.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(server.Close)
	w := &worker{client: server.Client(), baseURL: server.URL, token: "worker-test-token", outputRoot: root}
	if err := w.recoverOrphanedStageEventLogs(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := w.recoverOrphanedStageEventLogs(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("recovery upload was not locally checkpointed: calls=%d", calls)
	}
}

func TestPrioritizeRecoveryArtifactsPutsStageEventsFirst(t *testing.T) {
	files := []app.DirectWorkerArtifactFile{
		{Artifact: model.ArtifactRef{ID: "video", Kind: "raw_recording"}},
		{Artifact: model.ArtifactRef{ID: "events", Kind: "browser_agent_stage_event_log"}},
		{Artifact: model.ArtifactRef{ID: "shot", Kind: "screenshot"}},
	}
	prioritizeRecoveryArtifacts(files)
	if files[0].Artifact.ID != "events" {
		t.Fatalf("stage events were not prioritized: %+v", files)
	}
}
