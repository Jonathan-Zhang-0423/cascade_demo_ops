package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/model"
)

func TestOnlyWaiverPolicyGuardWaivesOnlyExactUnclassifiedNode(t *testing.T) {
	pkg := readBrowserAgentOutlineFixture(t)
	plan, err := compileBrowserAgentRuntimePlan(&pkg)
	if err != nil {
		t.Fatal(err)
	}
	stage := plan.Stages[1]
	intent := BrowserAgentActionIntent{
		NodeID: stage.NodeID, StageID: stage.ID, ActionType: stage.Interactions[0].Kind,
		URL: stage.URL, Route: stage.Route, TargetContract: stage.TargetContract, NonDestructive: false,
	}
	formal := contractBrowserAgentPolicyGuard{}.Authorize(plan, intent)
	if formal.Allowed || formal.Code != "destructive_action_denied" {
		t.Fatalf("formal policy must reject the unclassified action: %+v", formal)
	}
	waiver := BrowserAgentTestWaiver{
		PackageID: pkg.PackageID, BundleHashSHA256: plan.SourceBundleHashSHA256, PlanHashSHA256: plan.SourcePlanHashSHA256,
		AllowedNodes: []BrowserAgentTestWaiverNode{{NodeID: stage.NodeID, StageID: stage.ID, ActionType: intent.ActionType, SemanticID: stage.TargetContract.SemanticID}},
		DevTestOnly:  true, NotForExchangeUpload: true, TestOnlyWaiver: true, ExpiresAt: timeNowUTC().Add(time.Minute),
	}
	decision := (testOnlyWaiverPolicyGuard{waiver: waiver}).Authorize(plan, intent)
	if !decision.Allowed || decision.Code != "test_only_waiver" {
		t.Fatalf("exact node-scoped waiver should supply only the missing classification: %+v", decision)
	}

	other := intent
	other.NodeID = plan.Stages[0].NodeID
	other.StageID = plan.Stages[0].ID
	other.TargetContract = plan.Stages[0].TargetContract
	if decision := (testOnlyWaiverPolicyGuard{waiver: waiver}).Authorize(plan, other); decision.Allowed {
		t.Fatalf("unlisted node must remain denied: %+v", decision)
	}
}

func TestOnlyWaiverPolicyGuardKeepsDestructiveAndNetworkBoundaries(t *testing.T) {
	pkg := readBrowserAgentOutlineFixture(t)
	plan, err := compileBrowserAgentRuntimePlan(&pkg)
	if err != nil {
		t.Fatal(err)
	}
	stage := plan.Stages[0]
	waiver := BrowserAgentTestWaiver{
		PackageID: pkg.PackageID, BundleHashSHA256: plan.SourceBundleHashSHA256, PlanHashSHA256: plan.SourcePlanHashSHA256,
		AllowedOrigin: "https://app.example.com",
		AllowedNodes:  []BrowserAgentTestWaiverNode{{NodeID: stage.NodeID, StageID: stage.ID, ActionType: model.GraphActionNavigate, SemanticID: stage.TargetContract.SemanticID}},
		DevTestOnly:   true, NotForExchangeUpload: true, TestOnlyWaiver: true, ExpiresAt: timeNowUTC().Add(time.Minute),
	}
	guard := testOnlyWaiverPolicyGuard{waiver: waiver}
	base := BrowserAgentActionIntent{NodeID: stage.NodeID, StageID: stage.ID, ActionType: model.GraphActionNavigate, URL: "https://app.example.com/dashboard", TargetContract: stage.TargetContract}

	destructive := base
	destructive.TargetContract.Destructive = true
	if decision := guard.Authorize(plan, destructive); decision.Allowed || decision.Code != "destructive_action_denied" {
		t.Fatalf("explicit destructive target must never be waived: %+v", decision)
	}
	outside := base
	outside.URL = "https://evil.example.net/dashboard"
	if decision := guard.Authorize(plan, outside); decision.Allowed || (decision.Code != "domain_not_allowed" && decision.Code != "test_waiver_origin_denied") {
		t.Fatalf("domain boundary must survive waiver: %+v", decision)
	}
	forbidden := base
	forbidden.URL = "https://app.example.com/v1/execution-packages"
	if decision := guard.Authorize(plan, forbidden); decision.Allowed || decision.Code != "forbidden_page" {
		t.Fatalf("forbidden page must survive waiver: %+v", decision)
	}
	route := base
	route.URL = "https://app.example.com/settings"
	if decision := guard.Authorize(plan, route); decision.Allowed || decision.Code != "route_not_allowed" {
		t.Fatalf("route boundary must survive waiver: %+v", decision)
	}
}

func TestOnlyWaiverPolicyGuardRejectsExpiredOrHashMismatchedWaiver(t *testing.T) {
	pkg := readBrowserAgentOutlineFixture(t)
	plan, err := compileBrowserAgentRuntimePlan(&pkg)
	if err != nil {
		t.Fatal(err)
	}
	stage := plan.Stages[0]
	intent := BrowserAgentActionIntent{NodeID: stage.NodeID, StageID: stage.ID, ActionType: model.GraphActionNavigate, URL: "https://app.example.com/dashboard", TargetContract: stage.TargetContract}
	base := BrowserAgentTestWaiver{
		PackageID: pkg.PackageID, BundleHashSHA256: "wrong-hash", PlanHashSHA256: plan.SourcePlanHashSHA256,
		AllowedOrigin: "https://app.example.com",
		AllowedNodes:  []BrowserAgentTestWaiverNode{{NodeID: stage.NodeID, StageID: stage.ID, ActionType: intent.ActionType, SemanticID: stage.TargetContract.SemanticID}},
		DevTestOnly:   true, NotForExchangeUpload: true, TestOnlyWaiver: true, ExpiresAt: timeNowUTC().Add(time.Minute),
	}
	if decision := (testOnlyWaiverPolicyGuard{waiver: base}).Authorize(plan, intent); decision.Allowed || decision.Code != "test_waiver_identity_mismatch" {
		t.Fatalf("bundle hash mismatch must be rejected: %+v", decision)
	}
	base.BundleHashSHA256 = plan.SourceBundleHashSHA256
	base.ExpiresAt = timeNowUTC().Add(-time.Second)
	if decision := (testOnlyWaiverPolicyGuard{waiver: base}).Authorize(plan, intent); decision.Allowed {
		t.Fatalf("expired waiver must be rejected: %+v", decision)
	}
}

func TestOnlyWaiverGuardDoesNotMutateOriginalPackage(t *testing.T) {
	pkg := readBrowserAgentOutlineFixture(t)
	before, err := model.DigestCanonicalJSON(pkg)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := compileBrowserAgentRuntimePlan(&pkg)
	if err != nil {
		t.Fatal(err)
	}
	stage := plan.Stages[0]
	waiver := BrowserAgentTestWaiver{PackageID: pkg.PackageID, BundleHashSHA256: plan.SourceBundleHashSHA256, PlanHashSHA256: plan.SourcePlanHashSHA256, AllowedOrigin: "https://app.example.com", DevTestOnly: true, NotForExchangeUpload: true, TestOnlyWaiver: true, ExpiresAt: timeNowUTC().Add(time.Minute), AllowedNodes: []BrowserAgentTestWaiverNode{{NodeID: stage.NodeID, StageID: stage.ID, ActionType: model.GraphActionNavigate, SemanticID: stage.TargetContract.SemanticID}}}
	_ = (testOnlyWaiverPolicyGuard{waiver: waiver}).Authorize(plan, BrowserAgentActionIntent{NodeID: stage.NodeID, StageID: stage.ID, ActionType: model.GraphActionNavigate, URL: "https://app.example.com/dashboard", TargetContract: stage.TargetContract})
	after, err := model.DigestCanonicalJSON(pkg)
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("test waiver policy mutated the App package: %s != %s", before, after)
	}
}

func TestApplyTestOnlyWaiverRuntimeClassificationsClonesExactInteraction(t *testing.T) {
	pkg := readBrowserAgentOutlineFixture(t)
	packageDigest, err := model.DigestCanonicalJSON(pkg)
	if err != nil {
		t.Fatal(err)
	}
	formalPlan, err := compileBrowserAgentRuntimePlan(&pkg)
	if err != nil {
		t.Fatal(err)
	}
	stageIndex, interactionIndex := 1, 0
	formalPlan.Stages[stageIndex].Interactions[interactionIndex].NonDestructive = false
	stage := formalPlan.Stages[stageIndex]
	waiver := BrowserAgentTestWaiver{
		PackageID: pkg.PackageID, BundleHashSHA256: formalPlan.SourceBundleHashSHA256, PlanHashSHA256: formalPlan.SourcePlanHashSHA256,
		AllowedNodes: []BrowserAgentTestWaiverNode{{NodeID: stage.NodeID, StageID: stage.ID, InteractionIndex: interactionIndex, ActionType: stage.Interactions[interactionIndex].Kind, SemanticID: stage.TargetContract.SemanticID}},
		DevTestOnly:  true, NotForExchangeUpload: true, TestOnlyWaiver: true, ExpiresAt: timeNowUTC().Add(time.Minute),
	}
	runtimePlan, applied, err := applyTestOnlyWaiverRuntimeClassifications(formalPlan, waiver)
	if err != nil {
		t.Fatal(err)
	}
	if len(applied) != 1 || !runtimePlan.Stages[stageIndex].Interactions[interactionIndex].NonDestructive {
		t.Fatalf("exact waiver classification was not applied to runtime copy: %+v", applied)
	}
	if formalPlan.Stages[stageIndex].Interactions[interactionIndex].NonDestructive {
		t.Fatal("runtime classification mutated the formal compiled plan")
	}
	workerStage := workerStageFromRuntime(runtimePlan.Stages[stageIndex])
	if !workerStage.Interactions[interactionIndex].NonDestructive {
		t.Fatal("runtime classification was not propagated to the Worker request")
	}
	afterDigest, err := model.DigestCanonicalJSON(pkg)
	if err != nil {
		t.Fatal(err)
	}
	if packageDigest != afterDigest {
		t.Fatal("runtime classification mutated the App package or protocol hashes")
	}
}

func TestApplyTestOnlyWaiverRuntimeClassificationsRejectsIdentityAndSafetyMismatch(t *testing.T) {
	pkg := readBrowserAgentOutlineFixture(t)
	plan, err := compileBrowserAgentRuntimePlan(&pkg)
	if err != nil {
		t.Fatal(err)
	}
	stageIndex, interactionIndex := 1, 0
	plan.Stages[stageIndex].Interactions[interactionIndex].NonDestructive = false
	stage := plan.Stages[stageIndex]
	base := BrowserAgentTestWaiver{
		PackageID: pkg.PackageID, BundleHashSHA256: plan.SourceBundleHashSHA256, PlanHashSHA256: plan.SourcePlanHashSHA256,
		AllowedNodes: []BrowserAgentTestWaiverNode{{NodeID: stage.NodeID, StageID: stage.ID, InteractionIndex: interactionIndex, ActionType: stage.Interactions[interactionIndex].Kind, SemanticID: stage.TargetContract.SemanticID}},
		DevTestOnly:  true, NotForExchangeUpload: true, TestOnlyWaiver: true, ExpiresAt: timeNowUTC().Add(time.Minute),
	}
	tests := map[string]func(*BrowserAgentRuntimePlan, *BrowserAgentTestWaiver){
		"plan hash": func(_ *BrowserAgentRuntimePlan, waiver *BrowserAgentTestWaiver) { waiver.PlanHashSHA256 = "wrong" },
		"node": func(_ *BrowserAgentRuntimePlan, waiver *BrowserAgentTestWaiver) {
			waiver.AllowedNodes[0].NodeID = "other"
		},
		"stage": func(_ *BrowserAgentRuntimePlan, waiver *BrowserAgentTestWaiver) {
			waiver.AllowedNodes[0].StageID = "other"
		},
		"semantic": func(_ *BrowserAgentRuntimePlan, waiver *BrowserAgentTestWaiver) {
			waiver.AllowedNodes[0].SemanticID = "other"
		},
		"action": func(_ *BrowserAgentRuntimePlan, waiver *BrowserAgentTestWaiver) {
			waiver.AllowedNodes[0].ActionType = model.GraphActionNavigate
		},
		"index": func(_ *BrowserAgentRuntimePlan, waiver *BrowserAgentTestWaiver) {
			waiver.AllowedNodes[0].InteractionIndex = 99
		},
		"destructive target": func(plan *BrowserAgentRuntimePlan, _ *BrowserAgentTestWaiver) {
			plan.Stages[stageIndex].TargetContract.Destructive = true
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			candidatePlan := plan
			candidatePlan.Stages = append([]BrowserAgentRuntimeStage{}, plan.Stages...)
			candidatePlan.Stages[stageIndex].Interactions = append([]model.BrowserAgentInteraction{}, plan.Stages[stageIndex].Interactions...)
			candidateWaiver := base
			candidateWaiver.AllowedNodes = append([]BrowserAgentTestWaiverNode{}, base.AllowedNodes...)
			mutate(&candidatePlan, &candidateWaiver)
			if _, _, err := applyTestOnlyWaiverRuntimeClassifications(candidatePlan, candidateWaiver); err == nil {
				t.Fatal("mismatched or destructive waiver classification must be rejected")
			}
		})
	}
}

func TestOnlyWaiverPolicyGuardNarrowsAppOriginCandidatesToVisibleHTTPOrigin(t *testing.T) {
	pkg := readBrowserAgentOutlineFixture(t)
	plan, err := compileBrowserAgentRuntimePlan(&pkg)
	if err != nil {
		t.Fatal(err)
	}
	stage := plan.Stages[0]
	waiver := BrowserAgentTestWaiver{
		PackageID: pkg.PackageID, BundleHashSHA256: plan.SourceBundleHashSHA256, PlanHashSHA256: plan.SourcePlanHashSHA256, AllowedOrigin: "https://app.example.com",
		AllowedNodes: []BrowserAgentTestWaiverNode{{NodeID: stage.NodeID, StageID: stage.ID, ActionType: model.GraphActionNavigate, SemanticID: stage.TargetContract.SemanticID}},
		DevTestOnly:  true, NotForExchangeUpload: true, TestOnlyWaiver: true, ExpiresAt: timeNowUTC().Add(time.Minute),
	}
	guard := testOnlyWaiverPolicyGuard{waiver: waiver}
	intent := BrowserAgentActionIntent{NodeID: stage.NodeID, StageID: stage.ID, ActionType: model.GraphActionNavigate, URL: "http://app.example.com/dashboard", TargetContract: stage.TargetContract}
	if decision := guard.Authorize(plan, intent); decision.Allowed || decision.Code != "test_waiver_origin_denied" {
		t.Fatalf("scheme candidate outside the visible origin must remain denied: %+v", decision)
	}
	intent.URL = "/dashboard/projects/42"
	if decision := guard.Authorize(plan, intent); !decision.Allowed {
		t.Fatalf("relative route inside the visible origin should remain eligible: %+v", decision)
	}
}

func TestVerifyAppDraftProtocolHashesRejectsContentTampering(t *testing.T) {
	pkg := readBrowserAgentOutlineFixture(t)
	if err := verifyAppDraftProtocolHashes(pkg.ExecutableScriptBundle); err != nil {
		t.Fatalf("unaltered fixture hashes must verify: %v", err)
	}
	pkg.ExecutableScriptBundle.ScriptOutline.Stages[0].Objective = "tampered after hash"
	if err := verifyAppDraftProtocolHashes(pkg.ExecutableScriptBundle); err == nil || !strings.Contains(err.Error(), "bundle hash") {
		t.Fatalf("content tampering with retained self-reported hash must be rejected: %v", err)
	}
}

func TestAppPackageTestWaiverHTTPRequiresLocalAckAndRejectsPackageInjection(t *testing.T) {
	server := newTestDevHTTPServer(t)
	request := httptest.NewRequest(http.MethodPost, "/v1/desktop/app-package-test-waivers", strings.NewReader(`{"project_id":"p","package_id":"pkg","expected_bundle_hash_sha256":"b","expected_plan_hash_sha256":"h","approved_node_ids":["n"],"dev_test_ack":false}`))
	request.RemoteAddr = "127.0.0.1:45000"
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "dev_test_ack=true") {
		t.Fatalf("missing acknowledgement must be rejected: %d %s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/v1/desktop/app-package-test-waivers", strings.NewReader(`{"project_id":"p","package_id":"pkg","expected_bundle_hash_sha256":"b","expected_plan_hash_sha256":"h","approved_node_ids":["n"],"dev_test_ack":true,"package":{"injected":true}}`))
	request.RemoteAddr = "127.0.0.1:45000"
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "unknown field") {
		t.Fatalf("caller-supplied package JSON must be rejected: %d %s", response.Code, response.Body.String())
	}
}

func TestAppPackageTestWaiverHTTPRejectsProductionAndNonLoopback(t *testing.T) {
	server := newTestDevHTTPServerWithEnvironment(t, "production")
	body := `{"project_id":"p","package_id":"pkg","expected_bundle_hash_sha256":"b","expected_plan_hash_sha256":"h","approved_node_ids":["n"],"dev_test_ack":true}`
	request := httptest.NewRequest(http.MethodPost, "/v1/desktop/app-package-test-waivers", strings.NewReader(body))
	request.RemoteAddr = "127.0.0.1:45000"
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "local dev/test") {
		t.Fatalf("production environment must reject test waiver: %d %s", response.Code, response.Body.String())
	}

	server = newTestDevHTTPServer(t)
	request = httptest.NewRequest(http.MethodPost, "/v1/desktop/app-package-test-waivers", strings.NewReader(body))
	request.RemoteAddr = "203.0.113.9:45000"
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "loopback") {
		t.Fatalf("non-loopback request must reject test waiver: %d %s", response.Code, response.Body.String())
	}
}

func TestBrowserAgentTestWaiverLabelsCannotRepresentFormalExchange(t *testing.T) {
	waiver := BrowserAgentTestWaiver{SchemaVersion: browserAgentTestWaiverSchemaVersion, DevTestOnly: true, NotForExchangeUpload: true, TestOnlyWaiver: true, FormalExchange: false, AppGenerated: true, TransportAuthenticated: false}
	data, err := json.Marshal(waiver)
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{`"dev_test_only":true`, `"not_for_exchange_upload":true`, `"test_only_waiver":true`, `"formal_exchange":false`, `"transport_authenticated":false`} {
		if !strings.Contains(string(data), marker) {
			t.Fatalf("test-only marker %s is missing from waiver: %s", marker, data)
		}
	}
}

func TestAppPackageTestWaiverLocalGuardRejectsCloudProfile(t *testing.T) {
	manager := newDevAppPackageTestWaiverManager(&Service{runtime: config.AppRuntimeConfig{Profile: config.ProfileCloud, Environment: "development"}})
	if err := manager.localOnlyGuard(true); err == nil {
		t.Fatal("cloud profile must never issue a local test waiver")
	}
}
