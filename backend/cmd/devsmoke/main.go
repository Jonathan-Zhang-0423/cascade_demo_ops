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
	"path/filepath"
	"strings"
	"time"

	"cascade-demoops/backend/internal/agents"
	"cascade-demoops/backend/internal/app"
	"cascade-demoops/backend/internal/model"
)

const defaultTimeout = 6 * time.Minute
const defaultRecordDuration = 12 * time.Second

const (
	flowDefault               = "default"
	flowCascadeLoginDashboard = "cascade-login-dashboard"
)

const (
	humanStageMinDurationMS        = 10000
	humanTransitionStageDurationMS = 12000
	humanFinalStageDurationMS      = 15000
)

func main() {
	baseURL := flag.String("base-url", "http://127.0.0.1:4317", "devserver base URL")
	token := flag.String("token", os.Getenv("CASCADE_DEV_EXCHANGE_TOKEN"), "CASCADE_DEV_EXCHANGE_TOKEN bearer token")
	productURL := flag.String("product-url", "", "externally reachable product URL to record; defaults to a local synthetic page")
	flow := flag.String("flow", flowDefault, "execution flow: default or cascade-login-dashboard")
	repoPath := flag.String("repo-path", "", "optional local project root for read-only code summary")
	loginEmail := flag.String("login-email", os.Getenv("CASCADE_SMOKE_LOGIN_EMAIL"), "dev-only login email for cascade-login-dashboard flow")
	loginPassword := flag.String("login-password", os.Getenv("CASCADE_SMOKE_LOGIN_PASSWORD"), "dev-only login password for cascade-login-dashboard flow")
	mode := flag.String("mode", "success", "smoke mode: success, failure, or any")
	autoRun := flag.Bool("auto-run", false, "server auto-runs plaintext uploads; skip the explicit dev /run call")
	recordDuration := flag.Duration("record-duration", defaultRecordDuration, "target raw recording duration for external product URL smoke packages")
	sampleOutputDir := flag.String("sample-output-dir", "", "optional local directory for sanitized package samples and downloaded deliverables")
	reusePackageID := flag.String("reuse-package-id", "", "skip upload/run and verify an existing exchange package id")
	packageFile := flag.String("package-file", "", "replay a client execution package JSON file instead of generating a synthetic smoke package")
	packageFileFixture := flag.Bool("package-file-fixture", false, "generate a temporary client execution package JSON file and replay it through --package-file")
	orgIDOverride := flag.String("org-id", "", "override org_id for package-file or reuse-package-id")
	projectIDOverride := flag.String("project-id", "", "override project_id for package-file uploads")
	timeout := flag.Duration("timeout", defaultTimeout, "end-to-end smoke timeout")
	if err := flag.CommandLine.Parse(cleanShellInjectedArgs(os.Args[1:])); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	modeWasSet := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "mode" {
			modeWasSet = true
		}
	})
	if strings.TrimSpace(*packageFile) != "" && !modeWasSet {
		*mode = "any"
	}

	options := smokeOptions{
		BaseURL:         *baseURL,
		Token:           *token,
		ProductURL:      *productURL,
		Flow:            *flow,
		RepoPath:        *repoPath,
		LoginEmail:      *loginEmail,
		LoginPassword:   *loginPassword,
		Mode:            *mode,
		AutoRun:         *autoRun,
		RecordDuration:  *recordDuration,
		SampleOutputDir: *sampleOutputDir,
		ReusePackageID:  *reusePackageID,
		PackageFile:     *packageFile,
		PackageFixture:  *packageFileFixture,
		OrgID:           *orgIDOverride,
		ProjectID:       *projectIDOverride,
		Timeout:         *timeout,
	}
	if err := run(options); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func cleanShellInjectedArgs(args []string) []string {
	cleaned := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := strings.ToLower(strings.TrimSpace(args[i]))
		switch arg {
		case "-encodedcommand", "-command", "-file", "-inputformat", "-outputformat", "-executionpolicy", "-windowstyle":
			i++
			continue
		case "-noprofile", "-noninteractive", "-mta", "-sta", "-nologo":
			continue
		}
		cleaned = append(cleaned, args[i])
	}
	return cleaned
}

type smokeOptions struct {
	BaseURL         string
	Token           string
	ProductURL      string
	Flow            string
	RepoPath        string
	LoginEmail      string
	LoginPassword   string
	Mode            string
	AutoRun         bool
	RecordDuration  time.Duration
	SampleOutputDir string
	ReusePackageID  string
	PackageFile     string
	PackageFixture  bool
	OrgID           string
	ProjectID       string
	Timeout         time.Duration
}

func run(options smokeOptions) error {
	options.BaseURL = strings.TrimRight(strings.TrimSpace(options.BaseURL), "/")
	options.Token = strings.TrimSpace(options.Token)
	options.ProductURL = strings.TrimSpace(options.ProductURL)
	options.Flow = strings.TrimSpace(strings.ToLower(options.Flow))
	options.RepoPath = strings.TrimSpace(options.RepoPath)
	options.LoginEmail = strings.TrimSpace(options.LoginEmail)
	options.Mode = strings.TrimSpace(strings.ToLower(options.Mode))
	options.ReusePackageID = strings.TrimSpace(options.ReusePackageID)
	options.PackageFile = strings.TrimSpace(options.PackageFile)
	options.OrgID = strings.TrimSpace(options.OrgID)
	options.ProjectID = strings.TrimSpace(options.ProjectID)
	if options.Flow == "" {
		options.Flow = flowDefault
	}
	if options.BaseURL == "" {
		return errors.New("--base-url is required")
	}
	if options.Token == "" {
		return errors.New("--token is required")
	}
	if options.Mode != "success" && options.Mode != "failure" && options.Mode != "any" {
		return fmt.Errorf("unsupported --mode %q", options.Mode)
	}
	if options.PackageFixture && options.PackageFile != "" {
		return errors.New("--package-file-fixture cannot be combined with --package-file")
	}
	if options.PackageFixture && options.ReusePackageID != "" {
		return errors.New("--package-file-fixture cannot be combined with --reuse-package-id")
	}
	if options.Mode == "any" && options.PackageFile == "" && options.ReusePackageID == "" && !options.PackageFixture {
		return errors.New("--mode any requires --package-file, --reuse-package-id, or --package-file-fixture")
	}
	if options.Flow != flowDefault && options.Flow != flowCascadeLoginDashboard {
		return fmt.Errorf("unsupported --flow %q", options.Flow)
	}
	if options.Flow == flowCascadeLoginDashboard && (options.LoginEmail == "" || options.LoginPassword == "") {
		return errors.New("--login-email and --login-password are required for cascade-login-dashboard flow")
	}
	if options.Timeout <= 0 {
		options.Timeout = defaultTimeout
	}
	if options.RecordDuration <= 0 {
		options.RecordDuration = defaultRecordDuration
	}

	ctx, cancel := context.WithTimeout(context.Background(), options.Timeout)
	defer cancel()

	externalProduct := options.ProductURL != ""
	if options.ProductURL == "" && options.PackageFile == "" {
		productServer := httptest.NewServer(http.HandlerFunc(handleSmokeProduct))
		defer productServer.Close()
		options.ProductURL = productServer.URL
	}

	orgID := options.OrgID
	projectID := options.ProjectID
	smokeID := fmt.Sprintf("%d", time.Now().UTC().UnixNano())
	client := &http.Client{Timeout: 60 * time.Second}
	sampleDir := strings.TrimSpace(options.SampleOutputDir)
	if sampleDir != "" {
		sampleDir = strings.ReplaceAll(sampleDir, "{id}", smokeID)
	}
	source := "synthetic"
	waitForTerminal := true
	packageFilePath := options.PackageFile

	if options.PackageFixture {
		if orgID == "" {
			orgID = "org_devsmoke"
		}
		if projectID == "" {
			projectID = "project_devsmoke"
		}
		generatedPath, err := writeGeneratedReplayPackageFixture(ctx, options, smokeID, orgID, projectID, sampleDir, externalProduct)
		if err != nil {
			return err
		}
		options.PackageFile = generatedPath
		packageFilePath = generatedPath
	}

	exchangePackageID := options.ReusePackageID
	if exchangePackageID == "" {
		uploadBody := replayUploadBody{}
		payloadAvailable := true
		producer := model.ExchangeProducer{AppVersion: "devsmoke", InstallID: "devsmoke-local", RuntimeProfile: "cloud-side-smoke"}
		packageKind := model.ExchangePackageKindClientExecution

		if options.PackageFile != "" {
			replay, err := loadReplayPackageFile(options.PackageFile, smokeID)
			if err != nil {
				return err
			}
			if options.PackageFixture {
				source = "package_file_fixture"
			} else {
				source = "package_file"
			}
			uploadBody = replay.UploadBody
			payloadAvailable = replay.PayloadAvailable
			if options.ProductURL == "" {
				options.ProductURL = replay.ProductURL
			}
			if orgID == "" {
				orgID = replay.OrgID
			}
			if projectID == "" {
				projectID = replay.ProjectID
			}
			if uploadBody.Envelope.Producer.AppVersion != "" || uploadBody.Envelope.Producer.InstallID != "" {
				producer = uploadBody.Envelope.Producer
			}
			if uploadBody.Envelope.PackageKind != "" {
				packageKind = uploadBody.Envelope.PackageKind
			}
		} else {
			if orgID == "" {
				orgID = "org_devsmoke"
			}
			if projectID == "" {
				projectID = "project_devsmoke"
			}
			codeSnapshot, err := readCodeSnapshot(ctx, options.RepoPath, projectID)
			if err != nil {
				return fmt.Errorf("read local repo summary: %w", err)
			}
			payload, envelope, err := buildClientExecutionPackage(smokeID, orgID, projectID, options.ProductURL, options.Mode, externalProduct, options.RecordDuration, options.Flow, options.LoginEmail, options.LoginPassword, codeSnapshot)
			if err != nil {
				return err
			}
			if sampleDir != "" {
				if err := writeSanitizedPackageSample(sampleDir, payload, envelope, codeSnapshot, options.LoginEmail, options.LoginPassword); err != nil {
					return fmt.Errorf("write sanitized package sample: %w", err)
				}
			}
			uploadBody = replayUploadBody{
				Envelope:   envelope,
				PayloadRef: envelope.PayloadRef,
				Payload:    payload,
			}
		}
		if orgID == "" || projectID == "" {
			return errors.New("org_id and project_id are required; provide them in --package-file or via --org-id/--project-id")
		}

		initResponse, err := postJSON[model.ExecutionPackageInitResponse](ctx, client, options.BaseURL+"/v1/execution-packages/init", options.Token, "", model.ExecutionPackageInitRequest{
			OrgID:       orgID,
			ProjectID:   projectID,
			PackageKind: packageKind,
			Producer:    producer,
		})
		if err != nil {
			return fmt.Errorf("init execution package: %w", err)
		}

		uploadBody.UploadID = initResponse.UploadID
		if uploadBody.PayloadRef.Kind == "" {
			uploadBody.PayloadRef = uploadBody.Envelope.PayloadRef
		}
		uploadResponse, err := postJSON[model.ExecutionPackageUploadResponse](ctx, client, options.BaseURL+"/v1/execution-packages", options.Token, "", uploadBody)
		if err != nil {
			return fmt.Errorf("upload execution package: %w", err)
		}
		if uploadResponse.ExchangePackageID == "" {
			return errors.New("upload response missing exchange_package_id")
		}
		exchangePackageID = uploadResponse.ExchangePackageID

		if !payloadAvailable {
			waitForTerminal = false
		} else if !options.AutoRun {
			if _, err := postJSON[model.ExecutionPackageStatusResponse](ctx, client, options.BaseURL+"/v1/dev/execution-packages/"+url.PathEscape(exchangePackageID)+"/run", options.Token, orgID, map[string]any{}); err != nil {
				return fmt.Errorf("run uploaded execution package: %w", err)
			}
		}
	} else if orgID == "" {
		orgID = "org_devsmoke"
	}

	var status model.ExecutionPackageStatusResponse
	var err error
	if waitForTerminal {
		status, err = waitForTerminalStatus(ctx, client, options.BaseURL, options.Token, orgID, exchangePackageID)
	} else {
		status, err = getJSON[model.ExecutionPackageStatusResponse](ctx, client, options.BaseURL+"/v1/execution-packages/"+url.PathEscape(exchangePackageID)+"/status", options.Token, orgID)
	}
	if err != nil {
		return fmt.Errorf("get execution package status: %w", err)
	}
	debug, err := getJSON[app.ExecutionPackageDebugView](ctx, client, options.BaseURL+"/v1/dev/execution-packages/"+url.PathEscape(exchangePackageID)+"/debug", options.Token, orgID)
	if err != nil {
		return fmt.Errorf("get execution package debug view: %w", err)
	}

	var result *model.RecordingResultPackage
	if status.ResultPackageID != "" {
		got, resultErr := getJSON[model.RecordingResultPackage](ctx, client, options.BaseURL+"/v1/result-packages/"+url.PathEscape(status.ResultPackageID), options.Token, orgID)
		if resultErr != nil {
			return fmt.Errorf("get result package: %w", resultErr)
		}
		result = &got
	}
	if waitForTerminal {
		if err := assertSmokeResult(options.Mode, status, debug, result); err != nil {
			return err
		}
	} else if options.Mode != "any" {
		return fmt.Errorf("package did not start because plaintext payload is unavailable; status=%q stage=%q", status.Status, status.Stage)
	}
	executionList, err := getJSON[model.ExecutionPackageListResponse](ctx, client, options.BaseURL+"/v1/dev/execution-packages", options.Token, orgID)
	if err != nil {
		return fmt.Errorf("list execution packages: %w", err)
	}
	resultList, err := getJSON[model.ResultPackageListResponse](ctx, client, options.BaseURL+"/v1/dev/result-packages", options.Token, orgID)
	if err != nil {
		return fmt.Errorf("list result packages: %w", err)
	}
	listCheck, err := verifyListEndpoints(exchangePackageID, status, executionList, resultList)
	if err != nil {
		return err
	}
	if sampleDir != "" && result != nil {
		if err := downloadDeliverables(ctx, client, options.BaseURL, options.Token, orgID, sampleDir, status); err != nil {
			return fmt.Errorf("download deliverables: %w", err)
		}
	}
	downloadMode := options.Mode
	if options.Mode == "any" && status.Status == model.ExchangePackageStatusCompleted {
		downloadMode = "success"
	}
	downloadCheck, err := verifyDownloadableDeliverable(ctx, client, options.BaseURL, options.Token, orgID, downloadMode, status)
	if err != nil {
		return err
	}
	ackCheck, ackedStatus, err := verifyResultAck(ctx, client, options.BaseURL, options.Token, orgID, downloadMode, status, downloadCheck)
	if err != nil {
		return err
	}
	if ackedStatus.ExchangePackageID != "" {
		status = ackedStatus
	}

	summary := smokeSummary{
		OK:                true,
		Mode:              options.Mode,
		Source:            source,
		PackageFile:       packageFilePath,
		BaseURL:           options.BaseURL,
		ProductURL:        options.ProductURL,
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
		AckCheck:      ackCheck,
		ListCheck:     listCheck,
	}
	if status.ResultSummary != nil {
		var ackedAt *time.Time
		if !status.ResultSummary.AckedAt.IsZero() {
			ackedAt = &status.ResultSummary.AckedAt
		}
		summary.Result = &smokeResultSummary{
			ResultID:            status.ResultSummary.ResultID,
			ResultStatus:        string(status.ResultSummary.ResultStatus),
			DeliveryStatus:      string(status.ResultSummary.DeliveryStatus),
			PassRate:            status.ResultSummary.PassRate,
			StepCount:           status.ResultSummary.StepCount,
			DemoVideoCount:      status.ResultSummary.DemoVideoCount,
			ScreenshotCount:     status.ResultSummary.ScreenshotCount,
			RawRecordingCount:   status.ResultSummary.RawRecordingCount,
			TraceCount:          status.ResultSummary.TraceCount,
			PrimaryDemoVideoURI: status.ResultSummary.PrimaryDemoVideoURI,
			RawRecordingURI:     status.ResultSummary.RawRecordingURI,
			AckedAt:             ackedAt,
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
	Source            string               `json:"source,omitempty"`
	PackageFile       string               `json:"package_file,omitempty"`
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
	AckCheck          *smokeAckCheck       `json:"ack_check,omitempty"`
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
	DeliveryStatus      string             `json:"delivery_status,omitempty"`
	PassRate            float64            `json:"pass_rate"`
	StepCount           int                `json:"step_count"`
	DemoVideoCount      int                `json:"demo_video_count"`
	ScreenshotCount     int                `json:"screenshot_count"`
	RawRecordingCount   int                `json:"raw_recording_count"`
	TraceCount          int                `json:"trace_count"`
	PrimaryDemoVideoURI string             `json:"primary_demo_video_uri,omitempty"`
	RawRecordingURI     string             `json:"raw_recording_uri,omitempty"`
	AckedAt             *time.Time         `json:"acked_at,omitempty"`
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
	SHA256        string `json:"sha256,omitempty"`
	SizeBytes     int64  `json:"size_bytes,omitempty"`
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

type smokeAckCheck struct {
	ResultPackageID   string   `json:"result_package_id,omitempty"`
	Status            string   `json:"status,omitempty"`
	DeliveryStatus    string   `json:"delivery_status,omitempty"`
	ReceivedAssetIDs  []string `json:"received_asset_ids,omitempty"`
	VerifiedChecksums bool     `json:"verified_checksums,omitempty"`
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
			SHA256:        deliverable.SHA256,
			SizeBytes:     deliverable.SizeBytes,
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
	if mode != "any" && (debug.Package.WorkflowNodeCount < 1 || debug.Package.ScriptStepCount < 1) {
		return fmt.Errorf("debug package summary looks incomplete: %+v", debug.Package)
	}
	if mode == "any" {
		return assertReplayResult(status, result)
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
	if deliverable := findExecutionDeliverable(status.ResultSummary.Deliverables, "demo_video"); deliverable == nil || deliverable.SHA256 == "" || deliverable.SizeBytes <= 0 {
		return fmt.Errorf("success smoke result_summary missing demo checksum metadata: %+v", status.ResultSummary.Deliverables)
	}
	return nil
}

func assertReplayResult(status model.ExecutionPackageStatusResponse, result *model.RecordingResultPackage) error {
	switch status.Status {
	case model.ExchangePackageStatusCompleted:
		if status.ResultPackageID == "" || status.ResultSummary == nil || result == nil {
			return errors.New("replay completed but result package or result_summary is missing")
		}
	case model.ExchangePackageStatusFailed:
		if status.Error == nil && status.FailureSummary == nil {
			return errors.New("replay failed without error or failure_summary")
		}
	case model.ExchangePackageStatusCanceled, model.ExchangePackageStatusExpired:
		return nil
	default:
		return fmt.Errorf("replay expected a terminal status, got %q", status.Status)
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

func writeGeneratedReplayPackageFixture(ctx context.Context, options smokeOptions, smokeID string, orgID string, projectID string, sampleDir string, externalProduct bool) (string, error) {
	codeSnapshot, err := readCodeSnapshot(ctx, options.RepoPath, projectID)
	if err != nil {
		return "", fmt.Errorf("read local repo summary: %w", err)
	}
	payload, envelope, err := buildClientExecutionPackage(smokeID, orgID, projectID, options.ProductURL, options.Mode, externalProduct, options.RecordDuration, options.Flow, options.LoginEmail, options.LoginPassword, codeSnapshot)
	if err != nil {
		return "", err
	}
	outputDir := sampleDir
	if outputDir == "" {
		tmpRoot := strings.TrimSpace(os.Getenv("GOTMPDIR"))
		if tmpRoot == "" {
			tmpRoot = os.TempDir()
		}
		outputDir, err = os.MkdirTemp(tmpRoot, "cascade-devsmoke-package-replay-*")
		if err != nil {
			return "", err
		}
	} else if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(outputDir, "execution-package.fixture.json")
	if err := writeIndentedJSONFile(path, payload); err != nil {
		return "", err
	}
	if sampleDir != "" {
		if err := writeSanitizedPackageSample(sampleDir, payload, envelope, codeSnapshot, options.LoginEmail, options.LoginPassword); err != nil {
			return "", fmt.Errorf("write sanitized package sample: %w", err)
		}
	}
	return path, nil
}

func writeIndentedJSONFile(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}

func writeSanitizedPackageSample(outputDir string, payload model.ClientExecutionPackage, envelope model.ExchangeEnvelope, snapshot *model.CodeUnderstandingSnapshot, secretValues ...string) error {
	if strings.TrimSpace(outputDir) == "" {
		return nil
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return err
	}
	writeJSON := func(name string, value any) error {
		data, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			return err
		}
		data = []byte(redactString(string(data), secretValues...))
		return os.WriteFile(filepath.Join(outputDir, name), data, 0o600)
	}
	if err := writeJSON("exchange_envelope.json", envelope); err != nil {
		return err
	}
	if err := writeJSON("client_execution_package.sanitized.json", payload); err != nil {
		return err
	}
	if payload.ExecutableScriptBundle != nil {
		if payload.ExecutableScriptBundle.PlanJSON != nil {
			if err := writeJSON("execution_plan.sanitized.json", payload.ExecutableScriptBundle.PlanJSON); err != nil {
				return err
			}
		}
		script := redactString(payload.ExecutableScriptBundle.PlaywrightScript.InlineSource, secretValues...)
		if err := os.WriteFile(filepath.Join(outputDir, "recording_script.sanitized.ts"), []byte(script), 0o600); err != nil {
			return err
		}
		markdown := redactString(payload.ExecutableScriptBundle.ApprovalMarkdown.InlineMarkdown, secretValues...)
		if err := os.WriteFile(filepath.Join(outputDir, "approval.md"), []byte(markdown), 0o600); err != nil {
			return err
		}
	}
	if snapshot != nil {
		if err := writeJSON("code_summary.json", snapshot); err != nil {
			return err
		}
	}
	return nil
}

func downloadDeliverables(ctx context.Context, client *http.Client, baseURL string, token string, orgID string, outputDir string, status model.ExecutionPackageStatusResponse) error {
	if status.ResultSummary == nil || strings.TrimSpace(outputDir) == "" {
		return nil
	}
	deliverableDir := filepath.Join(outputDir, "deliverables")
	if err := os.MkdirAll(deliverableDir, 0o755); err != nil {
		return err
	}
	for index, deliverable := range status.ResultSummary.Deliverables {
		if deliverable.DownloadURL == "" {
			continue
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, absoluteURL(baseURL, deliverable.DownloadURL), nil)
		if err != nil {
			return err
		}
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("X-Cascade-Org-ID", orgID)
		response, err := client.Do(request)
		if err != nil {
			return err
		}
		if response.StatusCode != http.StatusOK {
			data, _ := io.ReadAll(response.Body)
			response.Body.Close()
			return fmt.Errorf("download %s returned %d: %s", deliverable.Kind, response.StatusCode, strings.TrimSpace(string(data)))
		}
		name := fmt.Sprintf("%02d_%s_%s%s", index+1, safeFileName(deliverable.Kind), safeFileName(firstNonEmpty(deliverable.Role, deliverable.ID)), extensionForDeliverable(deliverable, response.Header.Get("Content-Type")))
		file, err := os.Create(filepath.Join(deliverableDir, name))
		if err != nil {
			response.Body.Close()
			return err
		}
		_, copyErr := io.Copy(file, response.Body)
		closeErr := file.Close()
		response.Body.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func redactString(value string, secrets ...string) string {
	result := value
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		result = strings.ReplaceAll(result, secret, "***redacted***")
	}
	return result
}

func safeFileName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "artifact"
	}
	var builder strings.Builder
	for _, r := range strings.ToLower(value) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			builder.WriteRune(r)
		} else {
			builder.WriteByte('_')
		}
	}
	return strings.Trim(builder.String(), "_")
}

func extensionForDeliverable(deliverable model.ExecutionDeliverable, contentType string) string {
	kind := strings.ToLower(deliverable.Kind)
	uri := strings.ToLower(deliverable.URI)
	switch {
	case strings.Contains(kind, "video") || strings.Contains(uri, ".webm") || strings.Contains(contentType, "video/webm"):
		return ".webm"
	case strings.Contains(kind, "screenshot") || strings.Contains(uri, ".png") || strings.Contains(contentType, "image/png"):
		return ".png"
	case strings.Contains(kind, "docs") || strings.Contains(uri, ".md") || strings.Contains(contentType, "markdown"):
		return ".md"
	case strings.Contains(uri, ".zip") || strings.Contains(kind, "trace"):
		return ".zip"
	default:
		return ".json"
	}
}

func verifyResultAck(ctx context.Context, client *http.Client, baseURL string, token string, orgID string, mode string, status model.ExecutionPackageStatusResponse, downloadCheck *smokeDownloadCheck) (*smokeAckCheck, model.ExecutionPackageStatusResponse, error) {
	if mode != "success" || status.ResultPackageID == "" || downloadCheck == nil || downloadCheck.ArtifactID == "" {
		return nil, model.ExecutionPackageStatusResponse{}, nil
	}
	receivedAssetIDs := []string{downloadCheck.ArtifactID}
	ack, err := postJSON[model.ResultPackageAckResponse](ctx, client, baseURL+"/v1/result-packages/"+url.PathEscape(status.ResultPackageID)+"/ack", token, orgID, model.ResultPackageAckRequest{
		ResultPackageID:   status.ResultPackageID,
		AckedByInstallID:  "devsmoke-local",
		ReceivedAssetIDs:  receivedAssetIDs,
		VerifiedChecksums: true,
	})
	if err != nil {
		return nil, model.ExecutionPackageStatusResponse{}, fmt.Errorf("ack result package: %w", err)
	}
	if ack.Status != model.RecordingResultStatusAcked || ack.DeliveryStatus != model.ResultDeliveryStatusAcked || !ack.VerifiedChecksums {
		return nil, model.ExecutionPackageStatusResponse{}, fmt.Errorf("unexpected ack response: %+v", ack)
	}
	ackedStatus, err := getJSON[model.ExecutionPackageStatusResponse](ctx, client, baseURL+"/v1/execution-packages/"+url.PathEscape(status.ExchangePackageID)+"/status", token, orgID)
	if err != nil {
		return nil, model.ExecutionPackageStatusResponse{}, fmt.Errorf("get acked status: %w", err)
	}
	if ackedStatus.ResultSummary == nil || ackedStatus.ResultSummary.ResultStatus != model.RecordingResultStatusAcked || ackedStatus.ResultSummary.DeliveryStatus != model.ResultDeliveryStatusAcked || ackedStatus.ResultSummary.AckedAt.IsZero() {
		return nil, model.ExecutionPackageStatusResponse{}, fmt.Errorf("acked status missing ack summary: %+v", ackedStatus.ResultSummary)
	}
	return &smokeAckCheck{
		ResultPackageID:   ack.ResultPackageID,
		Status:            string(ack.Status),
		DeliveryStatus:    string(ack.DeliveryStatus),
		ReceivedAssetIDs:  append([]string{}, ack.ReceivedAssetIDs...),
		VerifiedChecksums: ack.VerifiedChecksums,
	}, ackedStatus, nil
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

func readCodeSnapshot(ctx context.Context, repoPath string, projectID string) (*model.CodeUnderstandingSnapshot, error) {
	if strings.TrimSpace(repoPath) == "" {
		return nil, nil
	}
	project := &model.ProjectContext{
		ID:             projectID,
		Mode:           model.AppModeWeb,
		ProductURL:     "https://cascadeai.cn/",
		TargetAudience: "internal integration tester",
		Inputs: &model.ProjectInputBundle{
			Code: []model.CodeInput{{
				ID:           "code_real_cascade",
				Kind:         "local_repo",
				URI:          "local://redacted/cascade",
				LocalPath:    repoPath,
				RepositoryID: "repo_real_cascade",
				Language:     "typescript",
				Framework:    "react",
				Entrypoints:  []string{"frontend/web/src/App.tsx", "frontend/web/src/pages/auth.tsx", "frontend/web/src/pages/dashboard.tsx"},
			}},
		},
	}
	brief := &model.RequirementBrief{
		ID:             "brief_real_cascade",
		ProjectID:      projectID,
		SchemaVersion:  model.MultimodalUnderstandingReportSchemaVersion,
		Scenario:       "product_demo",
		TargetAudience: "internal integration tester",
		Objective:      "生成 Cascade 真实网页登录、工作台浏览、多步骤截图和录制素材。",
		MustShow:       []string{"登录入口", "邮箱密码登录", "项目工作台", "本地代码摘要驱动的执行计划"},
		MustNotShow:    []string{"raw password", ".env 内容", "完整源码"},
		RequiredAssets: []model.AssetKind{model.AssetKindDemoVideo, model.AssetKindStepByStepDocs, model.AssetKindScreenshotPack},
	}
	snapshots, err := agents.NewCodeReaderAgent().ReadCode(ctx, project, brief)
	if err != nil {
		return nil, err
	}
	if len(snapshots) == 0 {
		return nil, nil
	}
	return &snapshots[0], nil
}

func cascadeLoginDashboardNodes(baseURL string, loginEmail string, loginPassword string) []nodeSpec {
	loginURL := absoluteURL(baseURL, "/login")
	appURL := absoluteURL(baseURL, "/app")
	return []nodeSpec{
		{ID: "node_open_home", Title: "打开官网首页", Action: model.GraphActionNavigate, Target: model.ActionTarget{URL: baseURL}, Expected: "Cascade 官网首页加载完成", DurationMS: 1600},
		{ID: "node_hold_home", Title: "停留首页观察", Action: model.GraphActionWait, Expected: "首页保持可见，稳定首屏", DurationMS: humanStageMinDurationMS, CaptureScreenshot: false},
		{ID: "node_open_login", Title: "进入登录页", Action: model.GraphActionNavigate, Target: model.ActionTarget{URL: loginURL}, Expected: "登录方式选择页展示", DurationMS: 1600},
		{ID: "node_hold_login", Title: "停留登录页观察", Action: model.GraphActionWait, Expected: "登录方式选择页保持可见", DurationMS: humanStageMinDurationMS, CaptureScreenshot: false},
		{ID: "node_choose_email", Title: "选择邮箱登录", Action: model.GraphActionClick, Target: model.ActionTarget{Text: "邮箱登录"}, Expected: "邮箱密码登录表单展示", DurationMS: 1200},
		{ID: "node_hold_choose_email", Title: "停留表单观察", Action: model.GraphActionWait, Expected: "邮箱密码登录表单保持可见", DurationMS: humanStageMinDurationMS, CaptureScreenshot: false},
		{ID: "node_fill_email", Title: "填写测试邮箱", Action: model.GraphActionFill, Target: model.ActionTarget{Selector: "input[type=\"email\"]"}, Value: loginEmail, Expected: "测试邮箱已填入", DurationMS: 900},
		{ID: "node_hold_fill_email", Title: "停留邮箱输入观察", Action: model.GraphActionWait, Expected: "邮箱输入完成后保持页面稳定", DurationMS: humanStageMinDurationMS, CaptureScreenshot: false},
		{ID: "node_fill_password", Title: "填写测试密码", Action: model.GraphActionFill, Target: model.ActionTarget{Selector: "input[type=\"password\"]"}, Value: loginPassword, Expected: "测试密码已填入并被打码策略覆盖", DurationMS: 900},
		{ID: "node_hold_fill_password", Title: "停留密码输入观察", Action: model.GraphActionWait, Expected: "密码输入完成后保持页面稳定", DurationMS: humanStageMinDurationMS, CaptureScreenshot: false},
		{ID: "node_submit_login", Title: "提交登录", Action: model.GraphActionClick, Target: model.ActionTarget{Selector: "form button[type=\"submit\"]"}, Expected: "登录请求提交", DurationMS: 2500, TimeoutMS: 15000},
		{ID: "node_hold_submit_login", Title: "等待登录结果", Action: model.GraphActionWait, Expected: "登录完成后停留观察结果", DurationMS: humanTransitionStageDurationMS, CaptureScreenshot: false},
		{ID: "node_open_workspace", Title: "打开项目工作台", Action: model.GraphActionNavigate, Target: model.ActionTarget{URL: appURL}, Expected: "进入项目工作台或展示账号状态页", DurationMS: 2200, TimeoutMS: 15000},
		{ID: "node_hold_workspace", Title: "停留工作台采集素材", Action: model.GraphActionWait, Expected: "工作台界面保持可见，采集最终素材", DurationMS: humanFinalStageDurationMS, CaptureScreenshot: false},
	}
}

func buildClientExecutionPackage(
	smokeID string,
	orgID string,
	projectID string,
	baseURL string,
	mode string,
	externalProduct bool,
	recordDuration time.Duration,
	flow string,
	loginEmail string,
	loginPassword string,
	codeSnapshot *model.CodeUnderstandingSnapshot,
) (model.ClientExecutionPackage, model.ExchangeEnvelope, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return model.ClientExecutionPackage{}, model.ExchangeEnvelope{}, err
	}
	host := parsed.Host
	now := time.Now().UTC().Truncate(time.Microsecond)
	packageID := "pkg_devsmoke_" + smokeID
	graphID := "graph_devsmoke_" + smokeID
	runID := "run_devsmoke_" + smokeID
	openDurationMS := 900
	if externalProduct {
		openDurationMS = 1000
	}
	nodes := []nodeSpec{
		{ID: "node_open", Title: "Open product", Action: model.GraphActionNavigate, Target: model.ActionTarget{URL: baseURL}, Value: "", Expected: "Product page loads", DurationMS: openDurationMS},
	}
	if flow == flowCascadeLoginDashboard {
		nodes = cascadeLoginDashboardNodes(baseURL, loginEmail, loginPassword)
	} else if externalProduct {
		recordDurationMS := int(recordDuration / time.Millisecond)
		if recordDurationMS < openDurationMS {
			recordDurationMS = openDurationMS
		}
		holdDurationMS := recordDurationMS - openDurationMS
		if holdDurationMS > 0 {
			nodes = append(nodes, nodeSpec{ID: "node_hold", Title: "Hold product page", Action: model.GraphActionWait, Expected: "Product page remains visible for recording", DurationMS: holdDurationMS})
		}
	} else {
		nodes = append(nodes, nodeSpec{ID: "node_fill", Title: "Enter demo email", Action: model.GraphActionFill, Target: model.ActionTarget{Selector: "#email"}, Value: "demo@example.com", Expected: "Email is entered", DurationMS: 700})
		if mode == "failure" {
			nodes = append(nodes, nodeSpec{ID: "node_missing_selector", Title: "Trigger selector diagnostic", Action: model.GraphActionClick, Target: model.ActionTarget{Selector: "#does-not-exist"}, Expected: "Failure diagnostic is produced", DurationMS: 500, TimeoutMS: 500})
		}
		nodes = append(nodes,
			nodeSpec{ID: "node_click", Title: "Start demo", Action: model.GraphActionClick, Target: model.ActionTarget{Selector: "#start"}, Expected: "Primary action is clicked", DurationMS: 700},
			nodeSpec{ID: "node_assert", Title: "Confirm status", Action: model.GraphActionAssert, Target: model.ActionTarget{Selector: "#status.done"}, Expected: "Confirmation is visible", DurationMS: 900},
		)
	}

	graph := model.NewDemoWorkflowGraph(graphID, projectID, baseURL)
	graph.Status = model.GraphStatusApproved
	graph.Name = "Dev smoke execution graph"
	graph.Summary = "Cloud-side HTTP exchange smoke graph generated by cmd/devsmoke."
	if externalProduct {
		graph.Summary = "Cloud-side HTTP exchange smoke graph for an externally reachable product URL."
	}
	if flow == flowCascadeLoginDashboard {
		graph.Name = "Cascade real app login and dashboard flow"
		graph.Summary = "Real website multi-step flow generated from local code summary and demo account inputs."
	}
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
		Summary:          "Open a product page, perform scripted UI interactions, and capture assets.",
		Language:         "typescript",
		WorkflowGraph:    graph,
		RecordingRunSpec: runSpec,
		Steps:            scriptStepsFromSpecs(nodes, baseURL),
		SafetyPolicy: model.ScriptSafetyPolicy{
			AllowedDomains: []string{host},
			Redactions:     model.RedactionPolicy{MaskSelectors: maskSelectorsForNodes(nodes)},
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

	source := scriptSource(baseURL, mode, externalProduct, waitDurationMS(nodes), nodes)
	markdown := "# Dev smoke approval\n\n1. Open product page\n2. Enter demo email\n3. Click the primary action\n4. Confirm success or capture failure diagnostics"
	if externalProduct {
		markdown = fmt.Sprintf("# Dev smoke approval\n\n1. Open the externally reachable product page\n2. Keep the page visible for about %d seconds total\n3. Capture a screenshot and raw recording\n4. Return recording artifacts and result package", totalDurationMS(nodes)/1000)
	}
	if flow == flowCascadeLoginDashboard {
		markdown = cascadeLoginApprovalMarkdown(baseURL, codeSnapshot, nodes)
	}
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
			Redactions:           model.RedactionPolicy{MaskSelectors: maskSelectorsForNodes(nodes)},
			AllowedContextAPIs:   []string{"ctx.page", "ctx.log"},
			AllowedPageMethods:   []string{"goto", "fill", "click", "waitForTimeout", "locator", "waitForLoadState"},
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
			Name:              projectNameForFlow(flow),
			ProductURL:        baseURL,
			TargetAudience:    "internal integration tester",
			InputFingerprints: inputFingerprints(smokeID, codeSnapshot),
			Metadata:          projectMetadataForFlow(flow, codeSnapshot),
		},
		ProductMapSummary:      productMapSummaryForFlow(flow, smokeID, baseURL, codeSnapshot),
		WorkflowGraph:          graph,
		RecordingRunSpec:       runSpec,
		ExecutableScriptBundle: bundle,
		CredentialGrants:       credentialGrantsForFlow(flow, host, now),
		EvidenceBundle:         evidenceBundleForFlow(flow, codeSnapshot),
		Reproducibility: model.ReproducibilitySpec{
			GraphHashSHA256:       graphDigest,
			ScriptHashSHA256:      planHash,
			InputFingerprints:     inputFingerprints(smokeID, codeSnapshot),
			SourceSnapshotDigest:  sourceDigestForSnapshot(codeSnapshot),
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
		Metadata: packageMetadataForFlow(flow, mode, codeSnapshot),
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
	ID                string
	Title             string
	Action            model.GraphActionType
	Target            model.ActionTarget
	Value             string
	Expected          string
	DurationMS        int
	TimeoutMS         int
	CaptureScreenshot bool
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
			Capture:         model.CaptureSpec{Screenshot: captureScreenshotForSpec(spec), Video: true, Zoom: spec.Action == model.GraphActionClick || spec.Action == model.GraphActionFill, FocusSelector: spec.Target.Selector, AssetRole: "primary"},
			Timing:          model.NodeTimingHint{NodeID: spec.ID, DurationMS: spec.DurationMS},
			Narrative:       model.NarrativeCue{Title: spec.Title, Caption: spec.Expected},
			Blocking:        true,
		})
	}
	return steps
}

func totalDurationMS(nodes []nodeSpec) int {
	total := 0
	for _, spec := range nodes {
		duration := spec.DurationMS
		if duration <= 0 {
			duration = 700
		}
		total += duration
	}
	return total
}

func waitDurationMS(nodes []nodeSpec) int {
	total := 0
	for _, spec := range nodes {
		if spec.Action == model.GraphActionWait && spec.DurationMS > 0 {
			total += spec.DurationMS
		}
	}
	return total
}

func captureScreenshotForSpec(spec nodeSpec) bool {
	if spec.Action == model.GraphActionWait {
		return spec.CaptureScreenshot
	}
	return true
}

func maskSelectorsForNodes(nodes []nodeSpec) []string {
	mask := []string{
		"[data-sensitive]",
		"[data-private]",
		"[data-testid*=\"user\" i]",
		"[data-testid*=\"account\" i]",
		"[data-testid*=\"profile\" i]",
		"[aria-label*=\"用户\" i]",
		"[aria-label*=\"账号\" i]",
		"[aria-label*=\"账户\" i]",
		"[aria-label*=\"个人\" i]",
		"[class*=\"user\" i]",
		"[class*=\"account\" i]",
		"[class*=\"profile\" i]",
		"[class*=\"avatar\" i]",
	}
	for _, node := range nodes {
		if node.Action != model.GraphActionFill {
			continue
		}
		lowerSelector := strings.ToLower(node.Target.Selector)
		if strings.Contains(lowerSelector, "password") || strings.Contains(lowerSelector, "email") {
			mask = append(mask, node.Target.Selector)
		}
	}
	return uniqueStrings(mask)
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		normalized := strings.TrimSpace(value)
		if normalized == "" || seen[normalized] {
			continue
		}
		seen[normalized] = true
		result = append(result, normalized)
	}
	return result
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func shortID(value string) string {
	hash := model.SHA256Hex([]byte(value))
	if len(hash) > 12 {
		return hash[:12]
	}
	return hash
}

func limitRouteSummaries(values []model.RouteInsight, limit int) []model.RouteInsight {
	if limit <= 0 || len(values) <= limit {
		return values
	}
	return values[:limit]
}

func limitComponentSummaries(values []model.ComponentInsight, limit int) []model.ComponentInsight {
	if limit <= 0 || len(values) <= limit {
		return values
	}
	return values[:limit]
}

func projectNameForFlow(flow string) string {
	if flow == flowCascadeLoginDashboard {
		return "Cascade real website"
	}
	return "Dev smoke product"
}

func inputFingerprints(smokeID string, snapshot *model.CodeUnderstandingSnapshot) map[string]string {
	fingerprints := map[string]string{"devsmoke": smokeID}
	if snapshot != nil && snapshot.SourceDigestSHA256 != "" {
		fingerprints["source_tree"] = snapshot.SourceDigestSHA256
	}
	return fingerprints
}

func sourceDigestForSnapshot(snapshot *model.CodeUnderstandingSnapshot) string {
	if snapshot == nil {
		return ""
	}
	return snapshot.SourceDigestSHA256
}

func projectMetadataForFlow(flow string, snapshot *model.CodeUnderstandingSnapshot) map[string]string {
	metadata := map[string]string{"flow": flow}
	if snapshot != nil {
		metadata["code_reader"] = "CodeReaderAgent"
		metadata["source_summary_only"] = "true"
		metadata["code_file_count"] = fmt.Sprint(snapshot.FileCount)
	}
	return metadata
}

func packageMetadataForFlow(flow string, mode string, snapshot *model.CodeUnderstandingSnapshot) map[string]any {
	metadata := map[string]any{"devsmoke_mode": mode, "flow": flow}
	if snapshot != nil {
		metadata["code_summary"] = map[string]any{
			"file_count":            snapshot.FileCount,
			"frameworks":            snapshot.Frameworks,
			"languages":             snapshot.Languages,
			"route_count":           len(snapshot.Routes),
			"component_count":       len(snapshot.Components),
			"selector_count":        len(snapshot.Selectors),
			"api_endpoint_count":    len(snapshot.APIEndpoints),
			"data_model_count":      len(snapshot.DataModels),
			"source_digest_sha256":  snapshot.SourceDigestSHA256,
			"source_summary_only":   true,
			"full_source_uploaded":  false,
			"raw_local_path_stored": false,
		}
	}
	return metadata
}

func productMapSummaryForFlow(flow string, smokeID string, baseURL string, snapshot *model.CodeUnderstandingSnapshot) model.ProductMapSummary {
	if flow != flowCascadeLoginDashboard {
		return model.ProductMapSummary{
			ProductMapID: "map_devsmoke_" + smokeID,
			Version:      1,
			Summary:      "Product page used for cloud-side exchange smoke validation.",
			Pages:        []*model.ProductPage{{URL: baseURL, Title: "Dev Smoke Product", Purpose: "Verify product page recording."}},
			Features:     []*model.Feature{{Name: "Primary action", UserValue: "Confirms scripted UI execution and asset capture."}},
		}
	}
	summary := model.ProductMapSummary{
		ProductMapID: "map_devsmoke_" + smokeID,
		Version:      1,
		Summary:      "Cascade 真实网页登录与项目工作台路径。产品地图由本地代码结构摘要和真实网址共同生成，不包含完整源码。",
		Pages: []*model.ProductPage{
			{URL: baseURL, Title: "Cascade 官网首页", Purpose: "展示产品入口与品牌首屏。"},
			{URL: absoluteURL(baseURL, "/login"), Title: "登录页", Purpose: "演示邮箱密码登录路径。"},
			{URL: absoluteURL(baseURL, "/app"), Title: "项目工作台", Purpose: "展示登录后项目列表和新建项目入口。"},
		},
		Features: []*model.Feature{
			{Name: "邮箱密码登录", UserValue: "客户使用已有账号进入工作台。"},
			{Name: "项目工作台", UserValue: "客户查看项目、新建项目并进入 DemoOps 后续链路。"},
		},
	}
	if snapshot != nil {
		for _, route := range limitRouteSummaries(snapshot.Routes, 12) {
			summary.Routes = append(summary.Routes, &model.RouteMapNode{
				ID:           "route_" + shortID(route.Path),
				Path:         route.Path,
				Name:         firstNonEmpty(route.Name, route.Path),
				AuthRequired: route.AuthRequired,
			})
		}
		for _, component := range limitComponentSummaries(snapshot.Components, 12) {
			summary.Components = append(summary.Components, model.ComponentSummary{
				ID:                 component.ID,
				Name:               component.Name,
				FilePathHashSHA256: component.FilePathHashSHA256,
			})
		}
	}
	return summary
}

func evidenceBundleForFlow(flow string, snapshot *model.CodeUnderstandingSnapshot) model.EvidenceBundle {
	bundle := model.EvidenceBundle{EvidenceRefs: []model.EvidenceRef{{
		ID:         "ev_devsmoke_requirements",
		Kind:       model.EvidenceKindRequirementDoc,
		Summary:    "Synthetic smoke package generated by cloud-side devsmoke.",
		Confidence: 0.8,
	}}}
	if flow == flowCascadeLoginDashboard {
		bundle.EvidenceSummaries = append(bundle.EvidenceSummaries, model.EvidenceSummary{
			ID:         "ev_summary_real_flow",
			Kind:       model.EvidenceKindRequirementDoc,
			Summary:    "真实 Cascade 网站登录到工作台的多步骤录制仿真；最终 AI 剪辑阶段不在本次实现范围。",
			Confidence: 0.86,
		})
	}
	if snapshot != nil {
		bundle.EvidenceRefs = append(bundle.EvidenceRefs, snapshot.EvidenceRefs...)
		bundle.EvidenceSummaries = append(bundle.EvidenceSummaries, model.EvidenceSummary{
			ID:         "ev_summary_code_reader",
			Kind:       model.EvidenceKindSourceCode,
			Summary:    fmt.Sprintf("CodeReaderAgent 只读扫描 %d 个文件；框架=%s；路由=%d；组件=%d；selector=%d；source_digest=%s。", snapshot.FileCount, strings.Join(snapshot.Frameworks, ","), len(snapshot.Routes), len(snapshot.Components), len(snapshot.Selectors), snapshot.SourceDigestSHA256),
			Confidence: 0.78,
			Sensitive:  false,
		})
		bundle.SourceTrees = append(bundle.SourceTrees, model.SourceTreeDigest{
			RepositoryID:     firstNonEmpty(snapshot.RepositoryID, "repo_real_cascade"),
			Branch:           snapshot.Branch,
			CommitSHA:        snapshot.CommitSHA,
			RootDigestSHA256: snapshot.SourceDigestSHA256,
			FileCount:        snapshot.FileCount,
			PathDigests:      snapshot.PathDigests,
		})
	}
	return bundle
}

func credentialGrantsForFlow(flow string, host string, now time.Time) []model.CredentialGrant {
	if flow != flowCascadeLoginDashboard {
		return nil
	}
	return []model.CredentialGrant{
		{
			GrantID:                  "grant_demo_email",
			Kind:                     "username",
			Purpose:                  "用于真实 Cascade 网站联调登录。",
			Scope:                    "login:" + host,
			ExpiresAt:                now.Add(2 * time.Hour),
			CloudSecretRef:           "vault://devsmoke/cascade-demo/email",
			AllowedDomains:           []string{host},
			AllowedOperations:        []string{"fill"},
			RotationRequiredAfterRun: false,
			DeleteAfterRun:           true,
		},
		{
			GrantID:                  "grant_demo_password",
			Kind:                     "password",
			Purpose:                  "用于真实 Cascade 网站联调登录；dev 明文通道临时注入，落盘样例脱敏。",
			Scope:                    "login:" + host,
			ExpiresAt:                now.Add(2 * time.Hour),
			CloudSecretRef:           "vault://devsmoke/cascade-demo/password",
			AllowedDomains:           []string{host},
			AllowedOperations:        []string{"fill"},
			RotationRequiredAfterRun: true,
			DeleteAfterRun:           true,
		},
	}
}

func cascadeLoginApprovalMarkdown(baseURL string, snapshot *model.CodeUnderstandingSnapshot, nodes []nodeSpec) string {
	codeLine := "- 本地代码摘要：未提供项目根目录。"
	if snapshot != nil {
		codeLine = fmt.Sprintf("- 本地代码摘要：CodeReaderAgent 只读扫描 %d 个文件，识别框架 %s、路由 %d 个、组件 %d 个、selector %d 个，source digest %s。", snapshot.FileCount, strings.Join(snapshot.Frameworks, ", "), len(snapshot.Routes), len(snapshot.Components), len(snapshot.Selectors), snapshot.SourceDigestSHA256)
	}
	return fmt.Sprintf(`# Cascade 真实网站录制审批预览

## 演示目标

使用真实网站 %s 完成从官网入口到邮箱登录，再进入项目工作台的多步骤录制仿真。

## 生成依据

- 真实网址：%s
%s
- 本次不上传完整源码，不读取或上传 .env 内容。
- 录制节奏：每个 stage 至少停留 %d 秒，登录提交/工作台过渡会延长到 %d 秒，最终工作台停留 %d 秒；预计总时长约 %d 秒。

## 执行路径

1. 打开官网首页。
2. 进入登录页。
3. 选择邮箱登录。
4. 填写测试邮箱。
5. 填写测试密码，截图/录屏按密码输入框打码。
6. 提交登录。
7. 进入项目工作台。
8. 停留工作台采集截图、原始录屏和步骤文档。

## 安全策略

- 允许域名：cascadeai.cn。
- 禁止上传完整源码：是。
- 凭据范围：仅用于本次登录录制。
- 打码选择器：input[type="password"], input[type="email"], [data-sensitive]，以及账号/用户标识/头像/个人菜单类选择器。
- 最终 AI 剪辑：本次不实现，只返回原始录屏、截图、步骤文档和非 AI 渲染产物。
`, baseURL, baseURL, codeLine, humanStageMinDurationMS/1000, humanTransitionStageDurationMS/1000, humanFinalStageDurationMS/1000, totalDurationMS(nodes)/1000)
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
	targetDurationSec := (startMS + 999) / 1000
	if targetDurationSec <= 0 {
		targetDurationSec = 1
	}
	maxDurationSec := 30
	if targetDurationSec+10 > maxDurationSec {
		maxDurationSec = targetDurationSec + 10
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
		Timeline: model.RecordingTimeline{TargetDurationSec: targetDurationSec, MaxDurationSec: maxDurationSec, CaptureWindows: windows, NodeTimingHints: hints},
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
		Redactions:    model.RedactionPolicy{MaskSelectors: maskSelectorsForNodes(nodes)},
		FailurePolicy: model.RecordingFailurePolicy{RetryAttempts: 1, SelectorRepairAllowed: true, MaxRepairAttempts: 1},
		Environment:   map[string]string{"locale": "en-US", "timezone": "UTC"},
	}
}

func scriptSource(baseURL string, mode string, externalProduct bool, holdDurationMS int, nodes []nodeSpec) string {
	if externalProduct && len(nodes) > 2 {
		return scriptSourceFromNodes(nodes)
	}
	if externalProduct {
		holdBlock := ""
		if holdDurationMS > 0 {
			holdBlock = fmt.Sprintf(`  await ctx.page.waitForTimeout(%d);
  ctx.log("node_hold");
`, holdDurationMS)
		}
		return fmt.Sprintf(`type CascadeRecordingContext = { page: any; log: any };
type CascadeRecordingResult = { ok: boolean };
export async function runCascadeRecording(ctx: CascadeRecordingContext): Promise<CascadeRecordingResult> {
  await ctx.page.goto("%s", { waitUntil: "domcontentloaded", timeout: 10000 });
  ctx.log("node_open");
%s
  return { ok: true };
}`, baseURL, holdBlock)
	}
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

func scriptSourceFromNodes(nodes []nodeSpec) string {
	var builder strings.Builder
	builder.WriteString("type CascadeRecordingContext = { page: any; log: any };\n")
	builder.WriteString("type CascadeRecordingResult = { ok: boolean };\n")
	builder.WriteString("export async function runCascadeRecording(ctx: CascadeRecordingContext): Promise<CascadeRecordingResult> {\n")
	for _, node := range nodes {
		builder.WriteString("  ctx.log(" + jsString(node.ID) + ");\n")
		timeoutMS := node.TimeoutMS
		if timeoutMS <= 0 {
			timeoutMS = 10000
		}
		switch node.Action {
		case model.GraphActionNavigate:
			builder.WriteString("  await ctx.page.goto(" + jsString(node.Target.URL) + ", { waitUntil: " + jsString(waitUntilForAction(node.Action)) + ", timeout: " + fmt.Sprint(timeoutMS) + " });\n")
		case model.GraphActionClick:
			builder.WriteString("  await ctx.page.locator(" + jsString(selectorExpression(node.Target)) + ").first().click({ timeout: " + fmt.Sprint(timeoutMS) + " });\n")
		case model.GraphActionFill:
			builder.WriteString("  await ctx.page.locator(" + jsString(selectorExpression(node.Target)) + ").first().fill(" + jsString(node.Value) + ", { timeout: " + fmt.Sprint(timeoutMS) + " });\n")
		case model.GraphActionSelect:
			builder.WriteString("  await ctx.page.locator(" + jsString(selectorExpression(node.Target)) + ").first().selectOption(" + jsString(node.Value) + ", { timeout: " + fmt.Sprint(timeoutMS) + " });\n")
		case model.GraphActionWait:
			if node.Target.Selector != "" || node.Target.Text != "" {
				builder.WriteString("  await ctx.page.locator(" + jsString(selectorExpression(node.Target)) + ").first().waitFor({ timeout: " + fmt.Sprint(timeoutMS) + " });\n")
			} else {
				builder.WriteString("  await ctx.page.waitForTimeout(" + fmt.Sprint(node.DurationMS) + ");\n")
			}
		case model.GraphActionAssert, model.GraphActionInspect:
			builder.WriteString("  await ctx.page.waitForTimeout(" + fmt.Sprint(maxInt(node.DurationMS, 500)) + ");\n")
		}
	}
	builder.WriteString("  return { ok: true };\n")
	builder.WriteString("}\n")
	return builder.String()
}

func selectorExpression(target model.ActionTarget) string {
	if target.Selector != "" {
		return target.Selector
	}
	if target.TestID != "" {
		return `[data-testid="` + strings.ReplaceAll(target.TestID, `"`, `\"`) + `"]`
	}
	if target.Text != "" {
		return "text=" + target.Text
	}
	if target.Label != "" {
		return "text=" + target.Label
	}
	return "body"
}

func jsString(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func maxInt(a int, b int) int {
	if a > b {
		return a
	}
	return b
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

type replayUploadBody struct {
	UploadID   string                       `json:"upload_id,omitempty"`
	Envelope   model.ExchangeEnvelope       `json:"envelope,omitempty"`
	PayloadRef model.EncryptedPayloadRef    `json:"payload_ref,omitempty"`
	Payload    model.ClientExecutionPackage `json:"payload,omitempty"`
}

type replayPackage struct {
	UploadBody       replayUploadBody
	OrgID            string
	ProjectID        string
	ProductURL       string
	PayloadAvailable bool
}

func loadReplayPackageFile(path string, smokeID string) (replayPackage, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return replayPackage{}, fmt.Errorf("read --package-file: %w", err)
	}
	var body replayUploadBody
	if err := json.Unmarshal(data, &body); err != nil {
		return replayPackage{}, fmt.Errorf("decode --package-file: %w", err)
	}
	if body.Payload.PackageID == "" && body.Envelope.EnvelopeID == "" {
		var payload model.ClientExecutionPackage
		if err := json.Unmarshal(data, &payload); err != nil {
			return replayPackage{}, fmt.Errorf("decode --package-file as client execution package: %w", err)
		}
		if payload.PackageID == "" {
			return replayPackage{}, errors.New("--package-file must be either an upload body with envelope/payload or a ClientExecutionPackage payload")
		}
		body.Payload = payload
	}
	if body.Payload.PackageID != "" && body.Envelope.EnvelopeID == "" {
		envelope, err := envelopeForPayload(smokeID, body.Payload, time.Now().UTC().Truncate(time.Microsecond))
		if err != nil {
			return replayPackage{}, fmt.Errorf("build replay envelope: %w", err)
		}
		body.Envelope = envelope
	}
	if body.Envelope.EnvelopeID == "" {
		return replayPackage{}, errors.New("--package-file envelope is required when plaintext payload is not present")
	}
	if body.PayloadRef.Kind == "" {
		body.PayloadRef = body.Envelope.PayloadRef
	}
	orgID := body.Envelope.OrgID
	projectID := body.Envelope.ProjectID
	productURL := ""
	if body.Payload.PackageID != "" {
		if orgID == "" {
			orgID = body.Payload.OrgID
		}
		if projectID == "" {
			projectID = body.Payload.ProjectID
		}
		productURL = body.Payload.ProjectContextSummary.ProductURL
	}
	if productURL == "" {
		productURL = body.PayloadRef.URI
	}
	return replayPackage{
		UploadBody:       body,
		OrgID:            orgID,
		ProjectID:        projectID,
		ProductURL:       productURL,
		PayloadAvailable: body.Payload.PackageID != "",
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
				Details []struct {
					Field   string `json:"field,omitempty"`
					Reason  string `json:"reason,omitempty"`
					Message string `json:"message"`
					Hint    string `json:"hint,omitempty"`
				} `json:"details,omitempty"`
			} `json:"error"`
		}
		if json.Unmarshal(data, &exchangeErr) == nil && exchangeErr.Error.Code != "" {
			if len(exchangeErr.Error.Details) > 0 {
				details, _ := json.Marshal(exchangeErr.Error.Details)
				return zero, fmt.Errorf("%s %s returned %d: %s: %s details=%s", req.Method, req.URL, resp.StatusCode, exchangeErr.Error.Code, exchangeErr.Error.Message, details)
			}
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
