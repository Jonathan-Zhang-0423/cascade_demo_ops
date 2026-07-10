package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"time"

	"cascade-demoops/backend/internal/app"
	"cascade-demoops/backend/internal/model"
)

const defaultTimeout = 180 * time.Second

func main() {
	baseURL := flag.String("base-url", "http://127.0.0.1:4317", "devserver base URL")
	token := flag.String("token", os.Getenv("CASCADE_DEV_EXCHANGE_TOKEN"), "CASCADE_DEV_EXCHANGE_TOKEN bearer token")
	mode := flag.String("mode", "success", "smoke mode: success or failure")
	reusePackageID := flag.String("reuse-package-id", "", "skip upload/run and verify an existing exchange package id")
	timeout := flag.Duration("timeout", defaultTimeout, "end-to-end smoke timeout")
	flag.Parse()

	if err := run(*baseURL, *token, *mode, *reusePackageID, *timeout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(baseURL string, token string, mode string, reusePackageID string, timeout time.Duration) error {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	token = strings.TrimSpace(token)
	mode = strings.TrimSpace(strings.ToLower(mode))
	reusePackageID = strings.TrimSpace(reusePackageID)
	if baseURL == "" {
		return errors.New("--base-url is required")
	}
	if token == "" {
		return errors.New("--token is required")
	}
	if mode != "success" && mode != "failure" {
		return fmt.Errorf("unsupported --mode %q", mode)
	}
	if timeout <= 0 {
		timeout = defaultTimeout
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	productServer := httptest.NewServer(http.HandlerFunc(handleSmokeProduct))
	defer productServer.Close()

	orgID := "org_devsmoke"
	projectID := "project_devsmoke"
	smokeID := fmt.Sprintf("%d", time.Now().UTC().UnixNano())
	client := &http.Client{Timeout: 60 * time.Second}

	exchangePackageID := reusePackageID
	if exchangePackageID == "" {
		initResponse, err := postJSON[model.ExecutionPackageInitResponse](ctx, client, baseURL+"/v1/execution-packages/init", token, "", model.ExecutionPackageInitRequest{
			OrgID:       orgID,
			ProjectID:   projectID,
			PackageKind: model.ExchangePackageKindClientExecution,
			Producer:    model.ExchangeProducer{AppVersion: "devsmoke", InstallID: "devsmoke-local", RuntimeProfile: "cloud-side-smoke"},
		})
		if err != nil {
			return fmt.Errorf("init execution package: %w", err)
		}

		payload, envelope, err := buildClientExecutionPackage(smokeID, orgID, projectID, productServer.URL, mode)
		if err != nil {
			return err
		}
		uploadResponse, err := postJSON[model.ExecutionPackageUploadResponse](ctx, client, baseURL+"/v1/execution-packages", token, "", map[string]any{
			"upload_id": initResponse.UploadID,
			"envelope":  envelope,
			"payload":   payload,
		})
		if err != nil {
			return fmt.Errorf("upload execution package: %w", err)
		}
		if uploadResponse.ExchangePackageID == "" {
			return errors.New("upload response missing exchange_package_id")
		}
		exchangePackageID = uploadResponse.ExchangePackageID

		if _, err := postJSON[model.ExecutionPackageStatusResponse](ctx, client, baseURL+"/v1/dev/execution-packages/"+url.PathEscape(exchangePackageID)+"/run", token, orgID, map[string]any{}); err != nil {
			return fmt.Errorf("run uploaded execution package: %w", err)
		}
	}

	status, err := waitForTerminalStatus(ctx, client, baseURL, token, orgID, exchangePackageID)
	if err != nil {
		return fmt.Errorf("get execution package status: %w", err)
	}
	debug, err := getJSON[app.ExecutionPackageDebugView](ctx, client, baseURL+"/v1/dev/execution-packages/"+url.PathEscape(exchangePackageID)+"/debug", token, orgID)
	if err != nil {
		return fmt.Errorf("get execution package debug view: %w", err)
	}

	var result *model.RecordingResultPackage
	if status.ResultPackageID != "" {
		got, resultErr := getJSON[model.RecordingResultPackage](ctx, client, baseURL+"/v1/result-packages/"+url.PathEscape(status.ResultPackageID), token, orgID)
		if resultErr != nil {
			return fmt.Errorf("get result package: %w", resultErr)
		}
		result = &got
	}
	if err := assertSmokeResult(mode, status, debug, result); err != nil {
		return err
	}
	executionList, err := getJSON[model.ExecutionPackageListResponse](ctx, client, baseURL+"/v1/dev/execution-packages", token, orgID)
	if err != nil {
		return fmt.Errorf("list execution packages: %w", err)
	}
	resultList, err := getJSON[model.ResultPackageListResponse](ctx, client, baseURL+"/v1/dev/result-packages", token, orgID)
	if err != nil {
		return fmt.Errorf("list result packages: %w", err)
	}
	listCheck, err := verifyListEndpoints(exchangePackageID, status, executionList, resultList)
	if err != nil {
		return err
	}
	downloadCheck, err := verifyDownloadableDeliverable(ctx, client, baseURL, token, orgID, mode, status)
	if err != nil {
		return err
	}

	summary := smokeSummary{
		OK:                true,
		Mode:              mode,
		BaseURL:           baseURL,
		ProductURL:        productServer.URL,
		ExchangePackageID: exchangePackageID,
		CloudJobID:        status.CloudJobID,
		Status:            string(status.Status),
		Stage:             status.Stage,
		StageCount:        len(status.StageHistory),
		ResultPackageID:   status.ResultPackageID,
		Runtime: smokeRuntimeSummary{
			VideoWorkerReady: debug.Runtime.VideoWorkerReady,
			NodeReady:        debug.Runtime.NodeReady,
			FFmpegReady:      debug.Runtime.FFmpegReady,
			VideoWorkerPath:  debug.Runtime.VideoWorkerPath,
			ArtifactRoot:     debug.Runtime.ArtifactRoot,
		},
		Package: smokePackageSummary{
			PackageID:       debug.Package.PackageID,
			WorkflowNodeCnt: debug.Package.WorkflowNodeCount,
			ScriptStepCnt:   debug.Package.ScriptStepCount,
			BaseURL:         debug.Package.BaseURL,
		},
		DownloadCheck: downloadCheck,
		ListCheck:     listCheck,
	}
	if status.ResultSummary != nil {
		summary.Result = &smokeResultSummary{
			ResultID:            status.ResultSummary.ResultID,
			ResultStatus:        string(status.ResultSummary.ResultStatus),
			PassRate:            status.ResultSummary.PassRate,
			StepCount:           status.ResultSummary.StepCount,
			DemoVideoCount:      status.ResultSummary.DemoVideoCount,
			ScreenshotCount:     status.ResultSummary.ScreenshotCount,
			RawRecordingCount:   status.ResultSummary.RawRecordingCount,
			TraceCount:          status.ResultSummary.TraceCount,
			PrimaryDemoVideoURI: status.ResultSummary.PrimaryDemoVideoURI,
			RawRecordingURI:     status.ResultSummary.RawRecordingURI,
			Deliverables:        smokeDeliverables(status.ResultSummary.Deliverables),
		}
	}
	if status.FailureSummary != nil {
		summary.Failure = &smokeFailureSummary{
			Code:                 status.FailureSummary.Code,
			FailedStage:          status.FailureSummary.FailedStage,
			FailedNodeID:         status.FailureSummary.FailedNodeID,
			FailureScreenshotURI: status.FailureSummary.FailureScreenshotURI,
			FailureTraceURI:      status.FailureSummary.FailureTraceURI,
		}
	}
	data, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}

func waitForTerminalStatus(ctx context.Context, client *http.Client, baseURL string, token string, orgID string, exchangePackageID string) (model.ExecutionPackageStatusResponse, error) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		status, err := getJSON[model.ExecutionPackageStatusResponse](ctx, client, baseURL+"/v1/execution-packages/"+url.PathEscape(exchangePackageID)+"/status", token, orgID)
		if err != nil {
			return model.ExecutionPackageStatusResponse{}, err
		}
		switch status.Status {
		case model.ExchangePackageStatusCompleted, model.ExchangePackageStatusFailed, model.ExchangePackageStatusCanceled, model.ExchangePackageStatusExpired:
			return status, nil
		}
		select {
		case <-ctx.Done():
			return model.ExecutionPackageStatusResponse{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

type smokeSummary struct {
	OK                bool                 `json:"ok"`
	Mode              string               `json:"mode"`
	BaseURL           string               `json:"base_url"`
	ProductURL        string               `json:"product_url"`
	ExchangePackageID string               `json:"exchange_package_id"`
	CloudJobID        string               `json:"cloud_job_id,omitempty"`
	Status            string               `json:"status"`
	Stage             string               `json:"stage,omitempty"`
	StageCount        int                  `json:"stage_count"`
	ResultPackageID   string               `json:"result_package_id,omitempty"`
	Runtime           smokeRuntimeSummary  `json:"runtime"`
	Package           smokePackageSummary  `json:"package"`
	DownloadCheck     *smokeDownloadCheck  `json:"download_check,omitempty"`
	ListCheck         *smokeListCheck      `json:"list_check,omitempty"`
	Result            *smokeResultSummary  `json:"result,omitempty"`
	Failure           *smokeFailureSummary `json:"failure,omitempty"`
}

type smokeRuntimeSummary struct {
	VideoWorkerReady bool   `json:"video_worker_ready"`
	NodeReady        bool   `json:"node_ready"`
	FFmpegReady      bool   `json:"ffmpeg_ready"`
	VideoWorkerPath  string `json:"video_worker_path,omitempty"`
	ArtifactRoot     string `json:"artifact_root,omitempty"`
}

type smokePackageSummary struct {
	PackageID       string `json:"package_id,omitempty"`
	WorkflowNodeCnt int    `json:"workflow_node_count"`
	ScriptStepCnt   int    `json:"script_step_count"`
	BaseURL         string `json:"base_url,omitempty"`
}

type smokeResultSummary struct {
	ResultID            string             `json:"result_id,omitempty"`
	ResultStatus        string             `json:"result_status,omitempty"`
	PassRate            float64            `json:"pass_rate"`
	StepCount           int                `json:"step_count"`
	DemoVideoCount      int                `json:"demo_video_count"`
	ScreenshotCount     int                `json:"screenshot_count"`
	RawRecordingCount   int                `json:"raw_recording_count"`
	TraceCount          int                `json:"trace_count"`
	PrimaryDemoVideoURI string             `json:"primary_demo_video_uri,omitempty"`
	RawRecordingURI     string             `json:"raw_recording_uri,omitempty"`
	Deliverables        []smokeDeliverable `json:"deliverables,omitempty"`
}

type smokeFailureSummary struct {
	Code                 string `json:"code,omitempty"`
	FailedStage          string `json:"failed_stage,omitempty"`
	FailedNodeID         string `json:"failed_node_id,omitempty"`
	FailureScreenshotURI string `json:"failure_screenshot_uri,omitempty"`
	FailureTraceURI      string `json:"failure_trace_uri,omitempty"`
}

type smokeDeliverable struct {
	Kind          string `json:"kind,omitempty"`
	Role          string `json:"role,omitempty"`
	URI           string `json:"uri,omitempty"`
	DownloadURL   string `json:"download_url,omitempty"`
	SourceNodeID  string `json:"source_node_id,omitempty"`
	IncludeInDemo bool   `json:"include_in_demo,omitempty"`
}

type smokeDownloadCheck struct {
	Kind         string `json:"kind,omitempty"`
	DownloadURL  string `json:"download_url,omitempty"`
	BytesRead    int64  `json:"bytes_read,omitempty"`
	ContentType  string `json:"content_type,omitempty"`
	ArtifactID   string `json:"artifact_id,omitempty"`
	ArtifactKind string `json:"artifact_kind,omitempty"`
}

type smokeListCheck struct {
	ExecutionCount int  `json:"execution_count"`
	ResultCount    int  `json:"result_count"`
	ExecutionFound bool `json:"execution_found"`
	ResultFound    bool `json:"result_found"`
}

func smokeDeliverables(deliverables []model.ExecutionDeliverable) []smokeDeliverable {
	out := make([]smokeDeliverable, 0, len(deliverables))
	for _, deliverable := range deliverables {
		if deliverable.URI == "" {
			continue
		}
		out = append(out, smokeDeliverable{
			Kind:          deliverable.Kind,
			Role:          deliverable.Role,
			URI:           deliverable.URI,
			DownloadURL:   deliverable.DownloadURL,
			SourceNodeID:  deliverable.SourceNodeID,
			IncludeInDemo: deliverable.IncludeInDemo,
		})
	}
	return out
}

func assertSmokeResult(mode string, status model.ExecutionPackageStatusResponse, debug app.ExecutionPackageDebugView, result *model.RecordingResultPackage) error {
	if len(status.StageHistory) == 0 {
		return errors.New("status stage_history is empty")
	}
	if !debug.Runtime.VideoWorkerReady {
		return errors.New("debug runtime reports video_worker_ready=false")
	}
	if !debug.Runtime.NodeReady {
		return errors.New("debug runtime reports node_ready=false")
	}
	if debug.Package.WorkflowNodeCount < 4 || debug.Package.ScriptStepCount < 4 {
		return fmt.Errorf("debug package summary looks incomplete: %+v", debug.Package)
	}
	if mode == "failure" {
		if status.Status != model.ExchangePackageStatusFailed {
			return fmt.Errorf("failure smoke expected failed status, got %q", status.Status)
		}
		if status.ResultPackageID == "" || result == nil {
			return errors.New("failure smoke expected result package with diagnostic")
		}
		if result.Status != model.RecordingResultStatusFailed || result.FailureDiagnostic == nil {
			return fmt.Errorf("failure smoke expected failed recording result with diagnostic, got %+v", result)
		}
		if status.FailureSummary == nil || status.FailureSummary.FailedNodeID == "" || status.FailureSummary.FailureScreenshotURI == "" || status.FailureSummary.FailureTraceURI == "" {
			return fmt.Errorf("failure smoke expected populated failure_summary, got %+v", status.FailureSummary)
		}
		return nil
	}
	if status.Status != model.ExchangePackageStatusCompleted {
		return fmt.Errorf("success smoke expected completed status, got %q stage=%q error=%+v failure=%+v", status.Status, status.Stage, status.Error, status.FailureSummary)
	}
	if status.ResultPackageID == "" || status.ResultSummary == nil || result == nil {
		return errors.New("success smoke expected result package and result_summary")
	}
	if result.Status != model.RecordingResultStatusGenerated {
		return fmt.Errorf("success smoke expected generated recording result, got %q", result.Status)
	}
	if status.ResultSummary.PassRate != 1 || status.ResultSummary.RawRecordingCount < 1 || status.ResultSummary.TraceCount < 1 || status.ResultSummary.DemoVideoCount < 1 || status.ResultSummary.ScreenshotCount < 1 {
		return fmt.Errorf("success smoke result_summary missing expected assets: %+v", status.ResultSummary)
	}
	if !hasDeliverable(status.ResultSummary.Deliverables, "demo_video") || !hasDeliverable(status.ResultSummary.Deliverables, "raw_recording") || !hasDeliverable(status.ResultSummary.Deliverables, "browser_trace") {
		return fmt.Errorf("success smoke result_summary missing deliverables: %+v", status.ResultSummary.Deliverables)
	}
	if deliverable := findExecutionDeliverable(status.ResultSummary.Deliverables, "demo_video"); deliverable == nil || deliverable.DownloadURL == "" {
		return fmt.Errorf("success smoke result_summary missing demo download_url: %+v", status.ResultSummary.Deliverables)
	}
	return nil
}

func hasDeliverable(deliverables []model.ExecutionDeliverable, kind string) bool {
	for _, deliverable := range deliverables {
		if deliverable.Kind == kind && deliverable.URI != "" {
			return true
		}
	}
	return false
}

func verifyDownloadableDeliverable(ctx context.Context, client *http.Client, baseURL string, token string, orgID string, mode string, status model.ExecutionPackageStatusResponse) (*smokeDownloadCheck, error) {
	if mode != "success" || status.ResultSummary == nil {
		return nil, nil
	}
	deliverable := findExecutionDeliverable(status.ResultSummary.Deliverables, "demo_video")
	if deliverable == nil || deliverable.DownloadURL == "" {
		return nil, errors.New("success smoke did not receive a downloadable demo_video deliverable")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, absoluteURL(baseURL, deliverable.DownloadURL), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("X-Cascade-Org-ID", orgID)
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(response.Body)
		return nil, fmt.Errorf("download deliverable returned %d: %s", response.StatusCode, strings.TrimSpace(string(data)))
	}
	n, err := io.Copy(io.Discard, response.Body)
	if err != nil {
		return nil, err
	}
	if n <= 0 {
		return nil, errors.New("downloaded deliverable was empty")
	}
	return &smokeDownloadCheck{
		Kind:         deliverable.Kind,
		DownloadURL:  deliverable.DownloadURL,
		BytesRead:    n,
		ContentType:  response.Header.Get("Content-Type"),
		ArtifactID:   response.Header.Get("X-Cascade-Artifact-ID"),
		ArtifactKind: response.Header.Get("X-Cascade-Artifact-Kind"),
	}, nil
}

func verifyListEndpoints(exchangePackageID string, status model.ExecutionPackageStatusResponse, executionList model.ExecutionPackageListResponse, resultList model.ResultPackageListResponse) (*smokeListCheck, error) {
	execution := findExecutionListItem(executionList.Items, exchangePackageID)
	if execution == nil {
		return nil, fmt.Errorf("execution package %s was not present in list response", exchangePackageID)
	}
	if execution.Status != status.Status || execution.CloudJobID != status.CloudJobID {
		return nil, fmt.Errorf("execution list item does not match status: list=%+v status=%+v", execution, status)
	}
	if status.ResultPackageID != "" && execution.ResultPackageID != status.ResultPackageID {
		return nil, fmt.Errorf("execution list result id mismatch: list=%q status=%q", execution.ResultPackageID, status.ResultPackageID)
	}
	resultFound := false
	if status.ResultPackageID != "" {
		result := findResultListItem(resultList.Items, status.ResultPackageID)
		if result == nil {
			return nil, fmt.Errorf("result package %s was not present in list response", status.ResultPackageID)
		}
		if result.ExchangePackageID != exchangePackageID {
			return nil, fmt.Errorf("result list exchange id mismatch: %+v", result)
		}
		if result.ResultSummary == nil {
			return nil, fmt.Errorf("result list item is missing result_summary: %+v", result)
		}
		resultFound = true
	}
	return &smokeListCheck{
		ExecutionCount: len(executionList.Items),
		ResultCount:    len(resultList.Items),
		ExecutionFound: true,
		ResultFound:    resultFound,
	}, nil
}

func findExecutionListItem(items []model.ExecutionPackageListItem, exchangePackageID string) *model.ExecutionPackageListItem {
	for index := range items {
		if items[index].ExchangePackageID == exchangePackageID {
			return &items[index]
		}
	}
	return nil
}

func findResultListItem(items []model.ResultPackageListItem, resultPackageID string) *model.ResultPackageListItem {
	for index := range items {
		if items[index].ResultPackageID == resultPackageID {
			return &items[index]
		}
	}
	return nil
}

func findExecutionDeliverable(deliverables []model.ExecutionDeliverable, kind string) *model.ExecutionDeliverable {
	for index := range deliverables {
		if deliverables[index].Kind == kind && deliverables[index].URI != "" {
			return &deliverables[index]
		}
	}
	return nil
}

func absoluteURL(baseURL string, value string) string {
	if strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") {
		return value
	}
	return strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(value, "/")
}

func buildClientExecutionPackage(smokeID string, orgID string, projectID string, baseURL string, mode string) (model.ClientExecutionPackage, model.ExchangeEnvelope, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return model.ClientExecutionPackage{}, model.ExchangeEnvelope{}, err
	}
	host := parsed.Host
	now := time.Now().UTC().Truncate(time.Microsecond)
	packageID := "pkg_devsmoke_" + smokeID
	graphID := "graph_devsmoke_" + smokeID
	runID := "run_devsmoke_" + smokeID
	nodes := []nodeSpec{
		{ID: "node_open", Title: "Open product", Action: model.GraphActionNavigate, Target: model.ActionTarget{URL: baseURL}, Value: "", Expected: "Product page loads", DurationMS: 900},
		{ID: "node_fill", Title: "Enter demo email", Action: model.GraphActionFill, Target: model.ActionTarget{Selector: "#email"}, Value: "demo@example.com", Expected: "Email is entered", DurationMS: 700},
	}
	if mode == "failure" {
		nodes = append(nodes, nodeSpec{ID: "node_missing_selector", Title: "Trigger selector diagnostic", Action: model.GraphActionClick, Target: model.ActionTarget{Selector: "#does-not-exist"}, Expected: "Failure diagnostic is produced", DurationMS: 500, TimeoutMS: 500})
	}
	nodes = append(nodes,
		nodeSpec{ID: "node_click", Title: "Start demo", Action: model.GraphActionClick, Target: model.ActionTarget{Selector: "#start"}, Expected: "Primary action is clicked", DurationMS: 700},
		nodeSpec{ID: "node_assert", Title: "Confirm status", Action: model.GraphActionAssert, Target: model.ActionTarget{Selector: "#status.done"}, Expected: "Confirmation is visible", DurationMS: 900},
	)

	graph := model.NewDemoWorkflowGraph(graphID, projectID, baseURL)
	graph.Status = model.GraphStatusApproved
	graph.Name = "Dev smoke execution graph"
	graph.Summary = "Cloud-side HTTP exchange smoke graph generated by cmd/devsmoke."
	graph.Nodes = make([]*model.GraphNode, 0, len(nodes))
	graph.Edges = make([]*model.GraphEdge, 0, len(nodes)-1)
	for index, spec := range nodes {
		graph.Nodes = append(graph.Nodes, graphNodeFromSpec(spec))
		if index > 0 {
			graph.Edges = append(graph.Edges, &model.GraphEdge{ID: "edge_" + nodes[index-1].ID + "_" + spec.ID, FromNode: nodes[index-1].ID, ToNode: spec.ID, Condition: "next"})
		}
	}
	graphDigest, err := model.DigestCanonicalJSON(graph)
	if err != nil {
		return model.ClientExecutionPackage{}, model.ExchangeEnvelope{}, err
	}

	runSpec := recordingRunSpec(runID, baseURL, host, nodes)
	doc := &model.ExecutionScriptDocument{
		ID:               "script_" + graphID,
		ProjectID:        projectID,
		WorkflowGraphID:  graph.ID,
		GraphVersion:     graph.Version,
		SchemaVersion:    model.ExecutionScriptDocumentSchemaVersion,
		Status:           model.ScriptDocumentStatusApproved,
		Title:            "Dev smoke recording script",
		Summary:          "Open a local product page, perform scripted UI interactions, and capture assets.",
		Language:         "typescript",
		WorkflowGraph:    graph,
		RecordingRunSpec: runSpec,
		Steps:            scriptStepsFromSpecs(nodes, baseURL),
		SafetyPolicy: model.ScriptSafetyPolicy{
			AllowedDomains: []string{host},
			Redactions:     model.RedactionPolicy{MaskSelectors: []string{"[data-sensitive]"}},
			PIIHandling:    "synthetic_test_data_only",
		},
		Reproducibility: model.ReproducibilitySpec{
			GraphHashSHA256:   graphDigest,
			DeterministicSeed: "devsmoke_" + smokeID,
		},
		ApprovalChecklist: model.ScriptApprovalChecklist{HumanApprovalRequired: true, SourceSummaryOnly: true, RedactionsReviewRequired: true},
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	planHash, err := doc.ComputeScriptHash()
	if err != nil {
		return model.ClientExecutionPackage{}, model.ExchangeEnvelope{}, err
	}
	doc.Reproducibility.ScriptHashSHA256 = planHash

	source := scriptSource(baseURL, mode)
	markdown := "# Dev smoke approval\n\n1. Open product page\n2. Enter demo email\n3. Click the primary action\n4. Confirm success or capture failure diagnostics"
	scriptHash := model.SHA256Hex([]byte(source))
	markdownHash := model.SHA256Hex([]byte(markdown))
	bundle := &model.ExecutableRecordingScriptBundle{
		ID:              "bundle_" + graphID,
		ProjectID:       projectID,
		WorkflowGraphID: graph.ID,
		SchemaVersion:   model.ExecutableRecordingScriptBundleSchemaVersion,
		Status:          model.ExecutableScriptBundleStatusValidated,
		ScriptManifest: model.ExecutableScriptManifest{
			ScriptID:            "recording_" + graphID,
			Version:             1,
			Language:            "typescript",
			Runtime:             "playwright-restricted-sandbox",
			EntryFunction:       "runCascadeRecording",
			Generator:           "cmd_devsmoke",
			GeneratorVersion:    "0.1.0",
			DependencyAllowlist: []string{},
			ContextAPIs:         []string{"ctx.page", "ctx.log"},
			StepNodeIDs:         stepNodeIDs(nodes),
		},
		PlanJSON:         doc,
		PlaywrightScript: model.ExecutableScriptSource{InlineSource: source, MimeType: "text/typescript", SHA256: scriptHash, SizeBytes: int64(len(source))},
		ApprovalMarkdown: model.ApprovalMarkdownDocument{InlineMarkdown: markdown, MimeType: "text/markdown", SHA256: markdownHash, SizeBytes: int64(len(markdown))},
		SecurityPolicy: model.ExecutableScriptSecurityPolicy{
			AllowedDomains:       []string{host},
			Redactions:           model.RedactionPolicy{MaskSelectors: []string{"[data-sensitive]"}},
			AllowedContextAPIs:   []string{"ctx.page", "ctx.log"},
			AllowedPageMethods:   []string{"goto", "fill", "click"},
			ForbiddenImports:     []string{"fs", "node:fs", "child_process", "node:child_process", "http", "https", "net", "tls"},
			ForbiddenIdentifiers: []string{"import", "require", "eval", "Function", "process", "global", "globalThis", "window", "document", "fetch", "XMLHttpRequest", "WebSocket"},
			NetworkPolicy:        "allowed_domains_only_via_ctx_page",
			FileSystemPolicy:     "no_direct_fs_access",
		},
		Reproducibility: model.ExecutableScriptReproducibility{
			PlanHashSHA256:     planHash,
			ScriptHashSHA256:   scriptHash,
			MarkdownHashSHA256: markdownHash,
			GraphHashSHA256:    graphDigest,
			GeneratorVersion:   "0.1.0",
			DeterministicSeed:  "devsmoke_" + smokeID,
		},
		Validation: &model.ExecutableScriptValidation{Valid: true, ValidatedAt: now},
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	bundleHash, err := bundle.ComputeBundleHash()
	if err != nil {
		return model.ClientExecutionPackage{}, model.ExchangeEnvelope{}, err
	}
	bundle.Reproducibility.BundleHashSHA256 = bundleHash

	pkg := model.ClientExecutionPackage{
		PackageID:     packageID,
		OrgID:         orgID,
		ProjectID:     projectID,
		SchemaVersion: model.ClientExecutionPackageSchemaVersion,
		CreatedAt:     now,
		ApprovedAt:    now,
		ProjectContextSummary: model.ProjectContextSummary{
			ContextID:         "ctx_devsmoke_" + smokeID,
			SchemaVersion:     model.ProjectContextSchemaVersion,
			Mode:              model.AppModeWeb,
			Name:              "Dev smoke product",
			ProductURL:        baseURL,
			TargetAudience:    "internal integration tester",
			InputFingerprints: map[string]string{"devsmoke": smokeID},
		},
		ProductMapSummary: model.ProductMapSummary{
			ProductMapID: "map_devsmoke_" + smokeID,
			Version:      1,
			Summary:      "Synthetic local product page used only for cloud-side exchange smoke validation.",
			Pages:        []*model.ProductPage{{URL: baseURL, Title: "Dev Smoke Product", Purpose: "Verify product page recording."}},
			Features:     []*model.Feature{{Name: "Primary action", UserValue: "Confirms scripted UI execution and asset capture."}},
		},
		WorkflowGraph:          graph,
		RecordingRunSpec:       runSpec,
		ExecutableScriptBundle: bundle,
		EvidenceBundle:         model.EvidenceBundle{EvidenceRefs: []model.EvidenceRef{{ID: "ev_devsmoke_requirements", Kind: model.EvidenceKindRequirementDoc, Summary: "Synthetic smoke package generated by cloud-side devsmoke."}}},
		Reproducibility: model.ReproducibilitySpec{
			GraphHashSHA256:       graphDigest,
			ScriptHashSHA256:      planHash,
			InputFingerprints:     map[string]string{"devsmoke": smokeID},
			DeterministicSeed:     "devsmoke_" + smokeID,
			CreatedWithAppVersion: "cmd_devsmoke",
		},
		SafetyReport: model.PackageSafetyReport{
			AllowedToUpload: true,
			UploadMode:      "structure_summary_only",
			HumanApproval: model.UserApprovalRecord{
				ApprovalID:       "approval_devsmoke_" + smokeID,
				ApprovedByUserID: "devsmoke",
				ApprovedAt:       now,
				PlanDigestSHA256: graphDigest,
				ReviewedNodeIDs:  stepNodeIDs(nodes),
			},
			PIIHandling: "synthetic_test_data_only",
		},
		Metadata: map[string]any{"devsmoke_mode": mode},
	}
	if err := model.ValidateClientExecutionPackageForCloudExecution(&pkg); err != nil {
		return model.ClientExecutionPackage{}, model.ExchangeEnvelope{}, fmt.Errorf("generated smoke package is invalid: %w", err)
	}
	packageHash, err := model.DigestCanonicalJSON(pkg)
	if err != nil {
		return model.ClientExecutionPackage{}, model.ExchangeEnvelope{}, err
	}
	pkg.Reproducibility.PackageHashSHA256 = packageHash
	envelope, err := envelopeForPayload(smokeID, pkg, now)
	if err != nil {
		return model.ClientExecutionPackage{}, model.ExchangeEnvelope{}, err
	}
	return pkg, envelope, nil
}

type nodeSpec struct {
	ID         string
	Title      string
	Action     model.GraphActionType
	Target     model.ActionTarget
	Value      string
	Expected   string
	DurationMS int
	TimeoutMS  int
}

func graphNodeFromSpec(spec nodeSpec) *model.GraphNode {
	timeoutMS := spec.TimeoutMS
	if timeoutMS <= 0 {
		timeoutMS = 10000
	}
	return &model.GraphNode{
		ID:              spec.ID,
		Type:            model.GraphNodeTypeAction,
		Title:           spec.Title,
		Action:          string(spec.Action),
		Selector:        spec.Target.Selector,
		InputData:       spec.Value,
		ExpectedOutcome: spec.Expected,
		IsScreenshot:    true,
		HasZoom:         spec.Action == model.GraphActionClick || spec.Action == model.GraphActionFill,
		RetryPolicy:     1,
		ActionSpec:      &model.GraphAction{Type: spec.Action, Target: spec.Target, Value: spec.Value, TimeoutMS: timeoutMS, WaitUntil: waitUntilForAction(spec.Action)},
		Capture:         &model.CaptureSpec{Screenshot: true, Video: true, Zoom: spec.Action == model.GraphActionClick || spec.Action == model.GraphActionFill, FocusSelector: spec.Target.Selector, AssetRole: "primary"},
		DurationHintMS:  spec.DurationMS,
	}
}

func scriptStepsFromSpecs(nodes []nodeSpec, baseURL string) []model.ScriptStep {
	steps := make([]model.ScriptStep, 0, len(nodes))
	for index, spec := range nodes {
		timeoutMS := spec.TimeoutMS
		if timeoutMS <= 0 {
			timeoutMS = 10000
		}
		pageURL := ""
		if spec.Action == model.GraphActionNavigate {
			pageURL = baseURL
		}
		steps = append(steps, model.ScriptStep{
			ID:              fmt.Sprintf("step_%02d_%s", index+1, spec.ID),
			Order:           index + 1,
			NodeID:          spec.ID,
			Title:           spec.Title,
			PageTarget:      model.ScriptPageTarget{URL: pageURL, Selector: spec.Target.Selector},
			Action:          model.ScriptActionInstruction{Type: spec.Action, Target: spec.Target, Value: spec.Value, TimeoutMS: timeoutMS, WaitUntil: waitUntilForAction(spec.Action)},
			ExpectedOutcome: spec.Expected,
			Capture:         model.CaptureSpec{Screenshot: true, Video: true, Zoom: spec.Action == model.GraphActionClick || spec.Action == model.GraphActionFill, FocusSelector: spec.Target.Selector, AssetRole: "primary"},
			Timing:          model.NodeTimingHint{NodeID: spec.ID, DurationMS: spec.DurationMS},
			Narrative:       model.NarrativeCue{Title: spec.Title, Caption: spec.Expected},
			Blocking:        true,
		})
	}
	return steps
}

func recordingRunSpec(runID string, baseURL string, host string, nodes []nodeSpec) model.RecordingRunSpec {
	windows := make([]model.CaptureWindow, 0, len(nodes))
	hints := make([]model.NodeTimingHint, 0, len(nodes))
	startMS := 0
	for _, spec := range nodes {
		duration := spec.DurationMS
		if duration <= 0 {
			duration = 700
		}
		windows = append(windows, model.CaptureWindow{ID: "capture_" + spec.ID, NodeID: spec.ID, StartMS: startMS, DurationMS: duration, Role: "primary"})
		hints = append(hints, model.NodeTimingHint{NodeID: spec.ID, DurationMS: duration})
		startMS += duration
	}
	return model.RecordingRunSpec{
		RunID:          runID,
		BaseURL:        baseURL,
		AllowedDomains: []string{host},
		Timezone:       "UTC",
		Locale:         "en-US",
		Browser: model.BrowserRunSpec{
			Engine:        "chromium",
			VersionPolicy: "bundled_playwright",
			Headless:      true,
			Viewports:     []model.ViewportSpec{{Name: "desktop", Width: 960, Height: 640, Device: "desktop"}},
		},
		Timeline: model.RecordingTimeline{TargetDurationSec: 12, MaxDurationSec: 30, CaptureWindows: windows, NodeTimingHints: hints},
		Outputs: model.RecordingOutputRequest{
			RawRecording:     true,
			FinalVideo:       true,
			ScreenshotPack:   true,
			StepByStepDocs:   true,
			Trace:            true,
			OutputFormats:    []string{"mp4", "png", "json"},
			ResolutionWidth:  960,
			ResolutionHeight: 640,
		},
		Redactions:    model.RedactionPolicy{MaskSelectors: []string{"[data-sensitive]"}},
		FailurePolicy: model.RecordingFailurePolicy{RetryAttempts: 1, SelectorRepairAllowed: true, MaxRepairAttempts: 1},
		Environment:   map[string]string{"locale": "en-US", "timezone": "UTC"},
	}
}

func scriptSource(baseURL string, mode string) string {
	failureBlock := ""
	if mode == "failure" {
		failureBlock = `  await ctx.page.click("#does-not-exist", { timeout: 500 });
  ctx.log("node_missing_selector");
`
	}
	return fmt.Sprintf(`type CascadeRecordingContext = { page: any; log: any };
type CascadeRecordingResult = { ok: boolean };
export async function runCascadeRecording(ctx: CascadeRecordingContext): Promise<CascadeRecordingResult> {
  await ctx.page.goto("%s");
  ctx.log("node_open");
  await ctx.page.fill("#email", "demo@example.com");
  ctx.log("node_fill");
%s  await ctx.page.click("#start");
  ctx.log("node_click");
  ctx.log("node_assert");
  return { ok: true };
}`, baseURL, failureBlock)
}

func stepNodeIDs(nodes []nodeSpec) []string {
	ids := make([]string, 0, len(nodes))
	for _, node := range nodes {
		ids = append(ids, node.ID)
	}
	return ids
}

func waitUntilForAction(actionType model.GraphActionType) string {
	if actionType == model.GraphActionNavigate {
		return "domcontentloaded"
	}
	return ""
}

func envelopeForPayload(smokeID string, pkg model.ClientExecutionPackage, now time.Time) (model.ExchangeEnvelope, error) {
	digest, err := model.DigestCanonicalJSON(pkg)
	if err != nil {
		return model.ExchangeEnvelope{}, err
	}
	return model.ExchangeEnvelope{
		EnvelopeID:           "env_devsmoke_" + smokeID,
		OrgID:                pkg.OrgID,
		ProjectID:            pkg.ProjectID,
		PackageKind:          model.ExchangePackageKindClientExecution,
		SchemaVersion:        model.ExchangeEnvelopeSchemaVersion,
		PayloadSchemaVersion: model.ClientExecutionPackageSchemaVersion,
		IdempotencyKey:       "idem_devsmoke_" + smokeID,
		CreatedAt:            now,
		ExpiresAt:            now.Add(30 * time.Minute),
		Producer:             model.ExchangeProducer{AppVersion: "devsmoke", InstallID: "devsmoke-local", RuntimeProfile: "cloud-side-smoke"},
		Crypto: model.ExchangeCrypto{
			CryptoSuite:            model.CryptoSuiteAES256GCM,
			ServerKeyID:            "local-dev/server-public-key",
			KeyWrappingMode:        model.KeyWrappingModeServerPublicKey,
			KeyEncryptionAlg:       "aes-256-gcm-dev-inline",
			ContentEncryptionAlg:   model.CryptoSuiteAES256GCM,
			CompressionAlg:         model.CompressionNone,
			PayloadDigestAlg:       "sha256",
			PayloadDigestSHA256:    digest,
			CiphertextDigestSHA256: digest,
			SignatureAlg:           "dev-static",
			SignatureKeyID:         "devsmoke",
			Signature:              "devsmoke-signature",
			Nonce:                  "nonce_devsmoke_" + smokeID,
			EncryptedContentKey:    "devsmoke-inline-content-key",
		},
		PayloadRef: model.EncryptedPayloadRef{
			Kind:             model.PayloadRefKindInline,
			InlineCiphertext: "devsmoke-inline-ciphertext",
			MimeType:         "application/json",
			SHA256:           digest,
			SizeBytes:        int64(len(digest)),
			Encrypted:        true,
			Sensitive:        true,
			CompressionAlg:   model.CompressionNone,
		},
		Policy: model.ExchangePackagePolicy{
			ReplayProtection:       true,
			MaxExecutionWindowSec:  900,
			DeletePayloadAfterRun:  true,
			HumanApprovalRequired:  true,
			StructureSummaryOnly:   true,
			RequiredIPAllowlistAck: false,
		},
	}, nil
}

func postJSON[T any](ctx context.Context, client *http.Client, endpoint string, token string, orgID string, body any) (T, error) {
	var zero T
	payload, err := json.Marshal(body)
	if err != nil {
		return zero, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return zero, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	if orgID != "" {
		req.Header.Set("X-Cascade-Org-ID", orgID)
	}
	return doJSON[T](client, req)
}

func getJSON[T any](ctx context.Context, client *http.Client, endpoint string, token string, orgID string) (T, error) {
	var zero T
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return zero, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if orgID != "" {
		req.Header.Set("X-Cascade-Org-ID", orgID)
	}
	return doJSON[T](client, req)
}

func doJSON[T any](client *http.Client, req *http.Request) (T, error) {
	var zero T
	resp, err := client.Do(req)
	if err != nil {
		return zero, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return zero, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var exchangeErr struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(data, &exchangeErr) == nil && exchangeErr.Error.Code != "" {
			return zero, fmt.Errorf("%s %s returned %d: %s: %s", req.Method, req.URL, resp.StatusCode, exchangeErr.Error.Code, exchangeErr.Error.Message)
		}
		return zero, fmt.Errorf("%s %s returned %d: %s", req.Method, req.URL, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	var out T
	if err := json.Unmarshal(data, &out); err != nil {
		return zero, fmt.Errorf("decode %s %s response: %w; body=%s", req.Method, req.URL, err, strings.TrimSpace(string(data)))
	}
	return out, nil
}

func handleSmokeProduct(w http.ResponseWriter, r *http.Request) {
	if host, _, err := net.SplitHostPort(r.Host); err == nil && host != "127.0.0.1" && host != "::1" && host != "localhost" {
		http.Error(w, "devsmoke product server is loopback-only", http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(smokeProductHTML))
}

const smokeProductHTML = `<!doctype html>
<html>
  <head>
    <meta charset="utf-8" />
    <title>Cascade Dev Smoke Product</title>
    <style>
      body { margin: 0; font-family: Arial, sans-serif; background: #f5f7fb; color: #172033; }
      main { width: 760px; margin: 56px auto; background: white; border: 1px solid #dde3ee; border-radius: 8px; padding: 28px; }
      label, input, button { display: block; font-size: 16px; }
      input { width: 320px; padding: 10px; margin: 8px 0 16px; border: 1px solid #aab4c4; border-radius: 6px; }
      button { padding: 10px 14px; border: 0; border-radius: 6px; color: white; background: #1769e0; cursor: pointer; }
      #status { margin-top: 18px; padding: 12px; border-radius: 6px; background: #eef2f7; }
      #status.done { color: #0f6b3a; background: #e7f7ed; }
    </style>
  </head>
  <body>
    <main>
      <h1>Product demo workspace</h1>
      <p>Run one deterministic product interaction for AIGC recording.</p>
      <label for="email">Demo email</label>
      <input id="email" autocomplete="off" />
      <button id="start" type="button">Start demo</button>
      <div id="status">Waiting for input</div>
    </main>
    <script>
      const button = document.getElementById("start");
      const email = document.getElementById("email");
      const status = document.getElementById("status");
      button.addEventListener("click", () => {
        status.className = "done";
        status.textContent = "Demo ready for " + (email.value || "visitor");
      });
    </script>
  </body>
</html>`
