package driver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"cascade-demoops/backend/internal/model"
)

func TestBrowserAgentWorkerRealSessionSmoke(t *testing.T) {
	if os.Getenv("CASCADE_BROWSER_AGENT_SMOKE") != "1" {
		t.Skip("set CASCADE_BROWSER_AGENT_SMOKE=1 to run the real Playwright worker smoke")
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = writer.Write([]byte(`<!doctype html><html><head><title>Go Browser Agent Smoke</title></head><body><main aria-label="Dashboard"><button data-testid="invite-member" onclick="document.querySelector('#status').textContent='Invite flow starts'">Invite teammate</button><p id="status">Waiting</p></main></body></html>`))
	}))
	defer server.Close()

	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve repository root")
	}
	workerPath := filepath.Join(filepath.Dir(sourceFile), "..", "..", "..", "video-worker", "dist", "index.js")
	if _, err := os.Stat(workerPath); err != nil {
		t.Fatalf("build video-worker before running smoke: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	worker := NewBrowserAgentWorker("node", workerPath)
	session, opened, err := worker.Open(ctx, BrowserAgentWorkerOpenRequest{
		SessionID: "go_driver_smoke", OutputDir: t.TempDir(),
		Browser:        BrowserAgentWorkerBrowser{Engine: "chromium", Headless: true, Viewport: BrowserAgentWorkerViewport{Width: 960, Height: 640}},
		AllowedDomains: []string{"127.0.0.1"}, AllowedOrigins: []string{server.URL}, AllowedRoutes: []string{"/"},
		ForbiddenPathPrefixes: []string{"/v1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Abort()
	if opened.SessionID != "go_driver_smoke" {
		t.Fatalf("unexpected session id: %+v", opened)
	}

	navigate := BrowserAgentWorkerStage{
		ID: "stage_open", Order: 1, NodeID: "node_open", URL: server.URL,
		TargetContract: model.BrowserAgentTargetContract{SemanticID: "target_dashboard", Destructive: false},
		Interactions:   []model.BrowserAgentInteraction{{Kind: model.GraphActionNavigate, Target: model.ActionTarget{URL: server.URL}, NonDestructive: true}},
		Validations:    []model.ValidationSpec{{ID: "validation_url", Kind: "url_matches", Target: model.ActionTarget{URL: server.URL}, Required: true}},
	}
	if _, err := session.Observe(ctx, navigate); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Execute(ctx, navigate); err != nil {
		t.Fatal(err)
	}
	click := BrowserAgentWorkerStage{
		ID: "stage_click", Order: 2, NodeID: "node_click",
		TargetContract: model.BrowserAgentTargetContract{SemanticID: "target_invite", AllowedRoles: []string{"button"}, AllowedNames: []string{"Invite teammate"}, Destructive: false},
		Interactions:   []model.BrowserAgentInteraction{{Kind: model.GraphActionClick, Target: model.ActionTarget{TestID: "invite-member"}, NonDestructive: true}},
		Validations:    []model.ValidationSpec{{ID: "validation_invite", Kind: "element_visible", Target: model.ActionTarget{TestID: "invite-member"}, Required: true}},
	}
	observed, err := session.Observe(ctx, click)
	if err != nil {
		t.Fatal(err)
	}
	if !observed.TargetResolved || len(observed.EvidenceRefs) == 0 {
		t.Fatalf("semantic target observation is incomplete: %+v", observed)
	}
	executed, err := session.Execute(ctx, click)
	if err != nil {
		t.Fatal(err)
	}
	if len(executed.Observation.Assertions) == 0 || !executed.Observation.Assertions[0].Passed {
		t.Fatalf("real click result was not asserted: %+v", executed)
	}
	closed, err := session.Close(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if closed.TracePath == "" || len(closed.Artifacts) == 0 {
		t.Fatalf("real session did not produce trace evidence: %+v", closed)
	}
}
