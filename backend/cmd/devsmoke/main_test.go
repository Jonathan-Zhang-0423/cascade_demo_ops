package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cascade-demoops/backend/internal/model"
)

func TestCascadeLoginDashboardNodesAreHumanPaced(t *testing.T) {
	nodes := cascadeLoginDashboardNodes("https://cascadeai.cn", "demo@example.com", "secret")
	if len(nodes) != 14 {
		t.Fatalf("expected 14 nodes, got %d", len(nodes))
	}

	wantDurations := map[string]int{
		"node_hold_home":          humanStageMinDurationMS,
		"node_hold_login":         humanStageMinDurationMS,
		"node_hold_choose_email":  humanStageMinDurationMS,
		"node_hold_fill_email":    humanStageMinDurationMS,
		"node_hold_fill_password": humanStageMinDurationMS,
		"node_hold_submit_login":  humanTransitionStageDurationMS,
		"node_hold_workspace":     humanFinalStageDurationMS,
	}

	gotWaits := 0
	for _, node := range nodes {
		if node.Action == "wait" {
			gotWaits++
			want, ok := wantDurations[node.ID]
			if !ok {
				t.Fatalf("unexpected wait node id %q", node.ID)
			}
			if node.DurationMS != want {
				t.Fatalf("node %s duration = %d, want %d", node.ID, node.DurationMS, want)
			}
			if captureScreenshotForSpec(node) {
				t.Fatalf("wait node %s should not create an extra screenshot", node.ID)
			}
			continue
		}
		if !captureScreenshotForSpec(node) {
			t.Fatalf("action node %s should capture a screenshot", node.ID)
		}
	}
	if gotWaits != len(wantDurations) {
		t.Fatalf("wait node count = %d, want %d", gotWaits, len(wantDurations))
	}
	if total := totalDurationMS(nodes); total < 87000 {
		t.Fatalf("total duration too small: %d", total)
	}
	if waits := waitDurationMS(nodes); waits != 77000 {
		t.Fatalf("explicit wait duration = %d, want 77000", waits)
	}
}

func TestScriptSourceFromNodesUsesHumanStageWaits(t *testing.T) {
	nodes := cascadeLoginDashboardNodes("https://cascadeai.cn", "demo@example.com", "secret")
	source := scriptSourceFromNodes(nodes)
	for _, token := range []string{
		`ctx.log("node_hold_home")`,
		"await ctx.page.waitForTimeout(10000)",
		"await ctx.page.waitForTimeout(12000)",
		"await ctx.page.waitForTimeout(15000)",
	} {
		if !strings.Contains(source, token) {
			t.Fatalf("generated script missing %q:\n%s", token, source)
		}
	}
	if strings.Contains(source, "waitForStage") {
		t.Fatalf("generated script should use explicit wait nodes, not timing hints:\n%s", source)
	}
}

func TestLoadReplayPackageFileAcceptsDirectPayload(t *testing.T) {
	payload, _, err := buildClientExecutionPackage("replay_direct", "org_replay", "project_replay", "http://127.0.0.1:3000", "success", false, defaultRecordDuration, flowDefault, "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	path := writeReplayJSON(t, payload)

	replay, err := loadReplayPackageFile(path, "smoke_replay_direct")
	if err != nil {
		t.Fatal(err)
	}
	if !replay.PayloadAvailable || replay.UploadBody.Payload.PackageID != payload.PackageID {
		t.Fatalf("direct payload should be available in replay upload body: %+v", replay)
	}
	if replay.UploadBody.Envelope.EnvelopeID == "" || replay.UploadBody.Envelope.OrgID != payload.OrgID || replay.UploadBody.Envelope.ProjectID != payload.ProjectID {
		t.Fatalf("direct payload should receive a generated envelope: %+v", replay.UploadBody.Envelope)
	}
	if replay.UploadBody.PayloadRef.Kind == "" || replay.UploadBody.PayloadRef != replay.UploadBody.Envelope.PayloadRef {
		t.Fatalf("direct payload should receive payload_ref from generated envelope: payload_ref=%+v envelope_ref=%+v", replay.UploadBody.PayloadRef, replay.UploadBody.Envelope.PayloadRef)
	}
	if replay.ProductURL != payload.ProjectContextSummary.ProductURL {
		t.Fatalf("replay product URL mismatch: %q", replay.ProductURL)
	}
}

func TestLoadReplayPackageFileAcceptsUploadBody(t *testing.T) {
	payload, envelope, err := buildClientExecutionPackage("replay_body", "org_replay", "project_replay", "http://127.0.0.1:3000", "success", false, defaultRecordDuration, flowDefault, "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	body := replayUploadBody{
		UploadID:   "should_be_replaced",
		Envelope:   envelope,
		PayloadRef: envelope.PayloadRef,
		Payload:    payload,
	}
	path := writeReplayJSON(t, body)

	replay, err := loadReplayPackageFile(path, "smoke_replay_body")
	if err != nil {
		t.Fatal(err)
	}
	if replay.UploadBody.Envelope.EnvelopeID != envelope.EnvelopeID {
		t.Fatalf("upload body envelope should be preserved: %+v", replay.UploadBody.Envelope)
	}
	if replay.UploadBody.PayloadRef != envelope.PayloadRef {
		t.Fatalf("upload body payload_ref should be preserved: %+v", replay.UploadBody.PayloadRef)
	}
	if replay.OrgID != envelope.OrgID || replay.ProjectID != envelope.ProjectID {
		t.Fatalf("upload body identity mismatch: %+v", replay)
	}
}

func TestWriteGeneratedReplayPackageFixtureProducesLoadablePackageFile(t *testing.T) {
	outputDir := t.TempDir()
	path, err := writeGeneratedReplayPackageFixture(context.Background(), smokeOptions{
		ProductURL:     "http://127.0.0.1:4317/v1/devsmoke/product",
		Flow:           flowDefault,
		Mode:           "success",
		RecordDuration: defaultRecordDuration,
	}, "fixture_replay", "org_replay", "project_replay", outputDir, false)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "execution-package.fixture.json" {
		t.Fatalf("fixture path should use stable file name, got %q", path)
	}
	replay, err := loadReplayPackageFile(path, "smoke_fixture_replay")
	if err != nil {
		t.Fatal(err)
	}
	if !replay.PayloadAvailable {
		t.Fatal("generated fixture should contain plaintext payload for replay")
	}
	if replay.OrgID != "org_replay" || replay.ProjectID != "project_replay" {
		t.Fatalf("fixture identity mismatch: %+v", replay)
	}
	if replay.UploadBody.Payload.ProjectContextSummary.ProductURL != "http://127.0.0.1:4317/v1/devsmoke/product" {
		t.Fatalf("fixture product URL mismatch: %q", replay.UploadBody.Payload.ProjectContextSummary.ProductURL)
	}
}

func TestLoadReplayPackageFileRejectsEmptyJSON(t *testing.T) {
	path := writeReplayJSON(t, map[string]any{"payload": map[string]any{}})
	if _, err := loadReplayPackageFile(path, "smoke_empty"); err == nil {
		t.Fatal("expected empty replay package to be rejected")
	}
}

func TestAssertReplayResultAcceptsTerminalOutcomes(t *testing.T) {
	if err := assertReplayResult(model.ExecutionPackageStatusResponse{Status: model.ExchangePackageStatusFailed, Error: &model.AgentError{Code: "selector_timeout"}}, nil); err != nil {
		t.Fatal(err)
	}
	if err := assertReplayResult(model.ExecutionPackageStatusResponse{Status: model.ExchangePackageStatusCompleted, ResultPackageID: "result_1", ResultSummary: &model.ExecutionResultSummary{}}, &model.RecordingResultPackage{}); err != nil {
		t.Fatal(err)
	}
	if err := assertReplayResult(model.ExecutionPackageStatusResponse{Status: model.ExchangePackageStatusAccepted}, nil); err == nil {
		t.Fatal("expected non-terminal replay result to be rejected")
	}
}

func writeReplayJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "package.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
