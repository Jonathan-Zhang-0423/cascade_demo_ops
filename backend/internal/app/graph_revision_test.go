package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cascade-demoops/backend/internal/model"
	"cascade-demoops/backend/internal/orchestrator"
	"cascade-demoops/backend/internal/store"
)

func TestReviseWorkflowGraphPersistsAndInvalidatesStalePreview(t *testing.T) {
	server := newTestDevHTTPServer(t)
	state := createGraphRevisionFixture(t, server, "graph-revision-persist")
	originalGraphDigest, err := model.DigestCanonicalJSON(state.WorkflowGraph)
	if err != nil {
		t.Fatal(err)
	}
	originalBuild, err := server.service.BuildClientExecutionPackage(t.Context(), state.ProjectID, defaultDesktopOrgID)
	if err != nil {
		t.Fatal(err)
	}
	node := state.WorkflowGraph.Nodes[0]
	request := GraphRevisionRequest{
		BaseGraphDigestSHA256: originalGraphDigest,
		Patches:               []GraphNodeRevision{{NodeID: node.ID, IsScreenshot: boolPointer(!node.IsScreenshot), HasZoom: boolPointer(!node.HasZoom)}},
		IdempotencyKey:        "revision-persist-1",
	}

	result, err := server.service.ReviseWorkflowGraph(t.Context(), state.ProjectID, request)
	if err != nil {
		t.Fatal(err)
	}
	if result.State.WorkflowGraph.Version != state.WorkflowGraph.Version+1 {
		t.Fatalf("graph version = %d, want %d", result.State.WorkflowGraph.Version, state.WorkflowGraph.Version+1)
	}
	if result.GraphDigestSHA256 == originalGraphDigest || result.Build.PackageDigestSHA256 == originalBuild.PackageDigestSHA256 || result.ApprovalSubjectDigest == originalBuild.ApprovalSubjectDigestSHA256 {
		t.Fatalf("revision did not invalidate all preview digests: result=%+v", result)
	}
	if result.ConfidenceAssessmentHash == "" || result.Build.Package.ConfidenceSummary == nil || result.ConfidenceAssessmentHash != result.Build.Package.ConfidenceSummary.AssessmentHash {
		t.Fatalf("revision response lost confidence assessment hash: %+v", result)
	}

	reloaded, err := server.service.LoadProject(t.Context(), state.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	revisedNode := graphNodeByID(reloaded.WorkflowGraph, node.ID)
	if revisedNode == nil || revisedNode.IsScreenshot == node.IsScreenshot || revisedNode.HasZoom == node.HasZoom {
		t.Fatalf("persisted graph lost allowed edits: original=%+v revised=%+v", node, revisedNode)
	}
	if revisedNode.Capture != nil && (revisedNode.Capture.Screenshot != revisedNode.IsScreenshot || revisedNode.Capture.Zoom != revisedNode.HasZoom) {
		t.Fatalf("capture fields diverged from graph flags: %+v", revisedNode)
	}
	if reloaded.Approved || reloaded.DesktopCloudRun != nil || reloaded.Artifacts != nil || reloaded.Status != orchestrator.FlowStatusAwaitingHuman {
		t.Fatalf("revision retained stale approval or execution state: %+v", reloaded)
	}

	again, err := server.service.ReviseWorkflowGraph(t.Context(), state.ProjectID, request)
	if err != nil {
		t.Fatal(err)
	}
	if again.GraphDigestSHA256 != result.GraphDigestSHA256 || again.Build.PackageDigestSHA256 != result.Build.PackageDigestSHA256 {
		t.Fatalf("idempotent revision returned a different result: first=%+v second=%+v", result, again)
	}
	request.Patches[0].HasZoom = boolPointer(node.HasZoom)
	if _, err := server.service.ReviseWorkflowGraph(t.Context(), state.ProjectID, request); err == nil || !strings.Contains(err.Error(), "idempotency key") {
		t.Fatalf("idempotency key reuse with different input must fail, got %v", err)
	}
	if _, err := server.service.ReviseWorkflowGraph(t.Context(), state.ProjectID, GraphRevisionRequest{
		BaseGraphDigestSHA256: originalGraphDigest,
		Patches:               []GraphNodeRevision{{NodeID: node.ID, HasZoom: boolPointer(node.HasZoom)}},
		IdempotencyKey:        "revision-stale",
	}); bridgeErrorCode(err) != "package_preview_stale" {
		t.Fatalf("old graph digest must return package_preview_stale, got code=%q err=%v", bridgeErrorCode(err), err)
	}
	if _, _, err := server.service.ApproveCloudClientExecutionPackage(t.Context(), state.ProjectID, defaultDesktopOrgID, CloudUploadInitRequest{
		ApprovalSubjectDigestSHA256: originalBuild.ApprovalSubjectDigestSHA256,
		ConfidenceAssessmentHash:    originalBuild.Package.ConfidenceSummary.AssessmentHash,
		RiskConfirmed:               true,
		IdempotencyKey:              "old-preview-approval",
	}); bridgeErrorCode(err) != "package_preview_stale" {
		t.Fatalf("old package approval must be stale after revision, got code=%q err=%v", bridgeErrorCode(err), err)
	}
}

func TestReviseWorkflowGraphRejectsInvalidPatchesAndUnknownHTTPFields(t *testing.T) {
	server := newTestDevHTTPServer(t)
	state := createGraphRevisionFixture(t, server, "graph-revision-invalid")
	digest, err := model.DigestCanonicalJSON(state.WorkflowGraph)
	if err != nil {
		t.Fatal(err)
	}
	nodeID := state.WorkflowGraph.Nodes[0].ID
	for name, patches := range map[string][]GraphNodeRevision{
		"unknown node":   {{NodeID: "missing", HasZoom: boolPointer(true)}},
		"duplicate node": {{NodeID: nodeID, HasZoom: boolPointer(true)}, {NodeID: nodeID, IsScreenshot: boolPointer(true)}},
		"empty patch":    {{NodeID: nodeID}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := server.service.ReviseWorkflowGraph(t.Context(), state.ProjectID, GraphRevisionRequest{
				BaseGraphDigestSHA256: digest, Patches: patches, IdempotencyKey: "invalid-" + strings.ReplaceAll(name, " ", "-"),
			})
			if err == nil {
				t.Fatal("invalid graph revision was accepted")
			}
		})
	}

	body := []byte(`{"base_graph_digest_sha256":"` + digest + `","patches":[{"node_id":"` + nodeID + `","is_screenshot":true,"action":"delete"}],"idempotency_key":"unknown-field"}`)
	httpRequest := httptest.NewRequest(http.MethodPost, "/v1/desktop/projects/"+state.ProjectID+"/workflow-graph/revisions", bytes.NewReader(body))
	httpRequest.Header.Set("Content-Type", "application/json")
	httpResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(httpResponse, httpRequest)
	if httpResponse.Code == http.StatusOK || !strings.Contains(httpResponse.Body.String(), "unknown field") {
		t.Fatalf("HTTP bridge accepted a field outside the revision allowlist: status=%d body=%s", httpResponse.Code, httpResponse.Body.String())
	}
}

func TestReviseWorkflowGraphSaveFailureLeavesAuthoritativeStateUntouched(t *testing.T) {
	server := newTestDevHTTPServer(t)
	state := createGraphRevisionFixture(t, server, "graph-revision-atomic")
	digest, err := model.DigestCanonicalJSON(state.WorkflowGraph)
	if err != nil {
		t.Fatal(err)
	}
	failing := &failNextSaveStateStore{StateStore: server.service.states, fail: true}
	server.service.states = failing
	node := state.WorkflowGraph.Nodes[0]
	_, err = server.service.ReviseWorkflowGraph(t.Context(), state.ProjectID, GraphRevisionRequest{
		BaseGraphDigestSHA256: digest,
		Patches:               []GraphNodeRevision{{NodeID: node.ID, IsScreenshot: boolPointer(!node.IsScreenshot)}},
		IdempotencyKey:        "revision-save-failure",
	})
	if err == nil || !strings.Contains(err.Error(), "injected save failure") {
		t.Fatalf("expected injected save failure, got %v", err)
	}
	reloaded, err := server.service.LoadProject(t.Context(), state.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	reloadedDigest, err := model.DigestCanonicalJSON(reloaded.WorkflowGraph)
	if err != nil {
		t.Fatal(err)
	}
	if reloadedDigest != digest || graphNodeByID(reloaded.WorkflowGraph, node.ID).IsScreenshot != node.IsScreenshot {
		t.Fatalf("failed revision mutated authoritative state: before=%s after=%s", digest, reloadedDigest)
	}
}

func TestWorkflowGraphRevisionHTTPAndWailsUseSameService(t *testing.T) {
	server := newTestDevHTTPServer(t)
	state := createGraphRevisionFixture(t, server, "graph-revision-bridges")
	digest, err := model.DigestCanonicalJSON(state.WorkflowGraph)
	if err != nil {
		t.Fatal(err)
	}
	node := state.WorkflowGraph.Nodes[0]
	request := GraphRevisionRequest{
		BaseGraphDigestSHA256: digest,
		Patches:               []GraphNodeRevision{{NodeID: node.ID, HasZoom: boolPointer(!node.HasZoom)}},
		IdempotencyKey:        "revision-shared-service",
	}
	body, _ := json.Marshal(request)
	httpRequest := httptest.NewRequest(http.MethodPost, "/v1/desktop/projects/"+state.ProjectID+"/workflow-graph/revisions", bytes.NewReader(body))
	httpRequest.Header.Set("Content-Type", "application/json")
	httpResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(httpResponse, httpRequest)
	if httpResponse.Code != http.StatusOK {
		t.Fatalf("HTTP revision failed: status=%d body=%s", httpResponse.Code, httpResponse.Body.String())
	}
	bridge := &DesktopBridge{service: server.service}
	wailsResponse := bridge.ReviseWorkflowGraph(state.ProjectID, request)
	if !wailsResponse.OK {
		t.Fatalf("Wails revision did not share the service idempotency result: %+v", wailsResponse)
	}
	var wailsResult GraphRevisionResult
	if err := json.Unmarshal(wailsResponse.Data, &wailsResult); err != nil {
		t.Fatal(err)
	}
	var httpEnvelope BridgeResponse
	if err := json.Unmarshal(httpResponse.Body.Bytes(), &httpEnvelope); err != nil {
		t.Fatal(err)
	}
	var httpResult GraphRevisionResult
	if err := json.Unmarshal(httpEnvelope.Data, &httpResult); err != nil {
		t.Fatal(err)
	}
	if httpResult.GraphDigestSHA256 != wailsResult.GraphDigestSHA256 || httpResult.Build.PackageDigestSHA256 != wailsResult.Build.PackageDigestSHA256 {
		t.Fatalf("HTTP and Wails returned different persisted revisions: http=%+v wails=%+v", httpResult, wailsResult)
	}
}

func createGraphRevisionFixture(t *testing.T, server *DevHTTPServer, projectID string) orchestrator.CascadeState {
	t.Helper()
	body, err := json.Marshal(ExecutionPackageRequest{UserInput: &orchestrator.UserInput{
		ProjectID: projectID, Mode: model.AppModeDesktop, ProductURL: "https://app.example.com",
		ProductDescription: "展示新建项目并启动 Agent 构建", TargetAudience: "普通用户",
		MustShow:           []string{"进入新建项目", "启动 Agent 构建"},
		WebpageScreenshots: []model.WebpageScreenshotInput{verifiedActionScreenshotInput()},
	}})
	if err != nil {
		t.Fatal(err)
	}
	state := postExecutionPackage(t, server, body)
	if state.WorkflowGraph == nil || len(state.WorkflowGraph.Nodes) == 0 {
		t.Fatal("graph revision fixture has no workflow nodes")
	}
	if _, err := server.service.BuildClientExecutionPackage(t.Context(), state.ProjectID, defaultDesktopOrgID); err != nil {
		t.Fatalf("graph revision fixture is not formally buildable: %v", err)
	}
	return state
}

func graphNodeByID(graph *model.DemoWorkflowGraph, nodeID string) *model.GraphNode {
	if graph == nil {
		return nil
	}
	for _, node := range graph.Nodes {
		if node != nil && node.ID == nodeID {
			return node
		}
	}
	return nil
}

func boolPointer(value bool) *bool { return &value }

type failNextSaveStateStore struct {
	store.StateStore
	fail bool
}

func (s *failNextSaveStateStore) Save(ctx context.Context, state *orchestrator.CascadeState) error {
	if s.fail {
		s.fail = false
		return errors.New("injected save failure")
	}
	return s.StateStore.Save(ctx, state)
}
