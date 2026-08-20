package app

import (
	"bufio"
	"context"
	"os"
	"testing"
	"time"

	"cascade-demoops/backend/internal/model"
)

func TestStageEventAuditLogIsAppendOnlyOrderedAndIdempotent(t *testing.T) {
	directory := t.TempDir()
	log, err := newStageEventAuditLog(directory, "job_1")
	if err != nil {
		t.Fatal(err)
	}
	first := auditTestEvent("event_1", 1)
	if err := log.Append(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if err := log.Append(context.Background(), first); err != nil {
		t.Fatalf("duplicate event_id must be idempotent: %v", err)
	}
	outOfOrder := auditTestEvent("event_2", 1)
	if err := log.Append(context.Background(), outOfOrder); err == nil {
		t.Fatal("different event with non-increasing sequence must be rejected")
	}
	if err := log.Append(context.Background(), auditTestEvent("event_2", 2)); err != nil {
		t.Fatal(err)
	}
	if log.Count() != 2 {
		t.Fatalf("expected two persisted events, got %d", log.Count())
	}

	file, err := os.Open(log.path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	lines := 0
	for scanner.Scan() {
		lines++
	}
	if err := scanner.Err(); err != nil || lines != 2 {
		t.Fatalf("expected two JSONL records, lines=%d err=%v", lines, err)
	}
	artifact, err := log.ArtifactRef()
	if err != nil {
		t.Fatal(err)
	}
	if artifact.SHA256 == "" || artifact.SizeBytes == 0 || !artifact.Sensitive || artifact.MimeType != "application/x-ndjson" {
		t.Fatalf("unexpected stage event artifact: %+v", artifact)
	}
	reloaded, err := newStageEventAuditLog(directory, "job_1")
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Count() != 2 || len(reloaded.Events()) != 2 {
		t.Fatalf("existing audit history was not restored: count=%d events=%d", reloaded.Count(), len(reloaded.Events()))
	}
	if err := reloaded.Append(context.Background(), auditTestEvent("event_3", 3)); err != nil {
		t.Fatalf("restored sequence did not continue monotonically: %v", err)
	}
}

func auditTestEvent(eventID string, sequence int64) model.StageExecutionEvent {
	return model.StageExecutionEvent{
		SchemaVersion: model.StageExecutionEventSchemaVersion, EventID: eventID, RunID: "run_1",
		SourcePackageID: "pkg_1", SourceBundleHashSHA256: "bundle_hash", PolicyHashSHA256: "policy_hash",
		NodeID: "node_1", StageID: "stage_1", Attempt: 1, Sequence: sequence,
		EventType: model.StageExecutionEventObservationCollected, OccurredAt: time.Date(2026, 7, 22, 12, 0, int(sequence), 0, time.UTC),
		Observation:  &model.RuntimeObservation{Source: model.RuntimeObservationActualBrowser, Title: "项目详情"},
		EvidenceRefs: []model.EvidenceRef{{ID: "artifact_1"}},
	}
}
