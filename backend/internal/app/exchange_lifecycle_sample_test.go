package app

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"cascade-demoops/backend/internal/model"
)

func TestSampleLifecycleAppPackageToServerIntake(t *testing.T) {
	t.Setenv(devExchangeHTTPEnv, "1")
	t.Setenv(devExchangeTokenEnv, "test-token")
	server := newTestDevHTTPServer(t)
	now := time.Date(2026, 7, 10, 10, 0, 0, 0, time.UTC)
	server.service.exchange.now = fixedClock(now)
	pkg := sampleClientExecutionPackageForAppTest(t)

	initPayload := exchangeHTTPDo[model.ExecutionPackageInitResponse](t, server, http.MethodPost, "/v1/execution-packages/init", model.ExecutionPackageInitRequest{
		OrgID:       pkg.OrgID,
		ProjectID:   pkg.ProjectID,
		PackageKind: model.ExchangePackageKindClientExecution,
		Producer:    model.ExchangeProducer{AppVersion: "desktop-sample", RuntimeProfile: "dev"},
	})
	if initPayload.UploadID == "" || initPayload.ServerPublicKeyID == "" || len(initPayload.SupportedCryptoSuites) == 0 {
		t.Fatalf("init should return upload and crypto metadata: %+v", initPayload)
	}

	envelope := sampleEnvelopeForAppTest(t, pkg, now)
	envelope.EnvelopeID = "env_sample_lifecycle"
	envelope.IdempotencyKey = "idem_sample_lifecycle"
	envelope.Crypto.Nonce = "nonce_sample_lifecycle"
	uploadPayload := exchangeHTTPDo[model.ExecutionPackageUploadResponse](t, server, http.MethodPost, "/v1/execution-packages", exchangeUploadHTTPBody{
		UploadID:   initPayload.UploadID,
		Envelope:   envelope,
		PayloadRef: envelope.PayloadRef,
		Payload:    pkg,
	})
	if uploadPayload.Status != model.ExchangePackageStatusAccepted || uploadPayload.ExchangePackageID == "" || uploadPayload.CloudJobID == "" {
		t.Fatalf("upload should create accepted exchange package and cloud job: %+v", uploadPayload)
	}

	status := exchangeHTTPDo[model.ExecutionPackageStatusResponse](t, server, http.MethodGet, "/v1/execution-packages/"+uploadPayload.ExchangePackageID+"/status?org_id="+pkg.OrgID, nil)
	if status.Status != model.ExchangePackageStatusAccepted || status.Stage != "accepted" || status.ProgressPercent != 10 {
		t.Fatalf("status should stay accepted before cloud run starts: %+v", status)
	}
	if len(status.StageHistory) != 1 || status.StageHistory[0].Stage != "accepted" {
		t.Fatalf("status should expose stage history for App polling: %+v", status.StageHistory)
	}

	debug := exchangeHTTPDo[ExecutionPackageDebugView](t, server, http.MethodGet, "/v1/dev/execution-packages/"+uploadPayload.ExchangePackageID+"/debug?org_id="+pkg.OrgID, nil)
	if debug.Package.PackageID != pkg.PackageID || debug.Package.ScriptStepCount != 1 || !debug.Package.ScriptHashConfigured {
		t.Fatalf("server debug view should expose package summary without raw script content: %+v", debug.Package)
	}
	if debug.Package.AllowedDomains[0] != "app.example.com" || !debug.Package.RawRecordingRequested || !debug.Package.TraceRequested {
		t.Fatalf("server should retain recording policy summary: %+v", debug.Package)
	}
}

func TestSampleLifecycleRepairScriptBundleUpload(t *testing.T) {
	service := NewExchangeIntakeService(nil)
	now := time.Date(2026, 7, 10, 10, 30, 0, 0, time.UTC)
	service.now = fixedClock(now)
	ctx := t.Context()

	original := sampleClientExecutionPackageForAppTest(t)
	originalEnvelope := sampleEnvelopeForAppTest(t, original, now)
	originalEnvelope.EnvelopeID = "env_sample_original"
	originalEnvelope.IdempotencyKey = "idem_sample_original"
	originalEnvelope.Crypto.Nonce = "nonce_sample_original"

	originalUpload := uploadSamplePackageToIntake(t, service, original, originalEnvelope)
	failed := sampleFailedRecordingResultForAppTest(original)
	failed.SourcePackageID = original.PackageID
	failed.CloudJobID = originalUpload.CloudJobID
	failed.FailureDiagnostic.SourcePackageID = original.PackageID
	failed.FailureDiagnostic.CloudJobID = originalUpload.CloudJobID
	failed.RepairRequest.SourcePackageID = original.PackageID
	failed.RepairRequest.CloudJobID = originalUpload.CloudJobID

	failedStatus, err := service.CompleteWithRecordingResult(ctx, original.OrgID, originalUpload.ExchangePackageID, failed)
	if err != nil {
		t.Fatal(err)
	}
	if failedStatus.Status != model.ExchangePackageStatusFailed || failedStatus.ResultPackageID == "" || failedStatus.FailureSummary == nil {
		t.Fatalf("expected failed result with repair diagnostic, got %+v", failedStatus)
	}

	failedResult, err := service.GetResultPackage(ctx, original.OrgID, failedStatus.ResultPackageID)
	if err != nil {
		t.Fatal(err)
	}
	repaired := sampleRepairedClientExecutionPackageForAppTest(t, original, failedResult, now.Add(15*time.Minute))
	if repaired.ExecutableScriptBundle.RepairLineage == nil || repaired.RepairContext == nil {
		t.Fatalf("repair package must carry lineage and repair_context: bundle=%+v context=%+v", repaired.ExecutableScriptBundle.RepairLineage, repaired.RepairContext)
	}
	if repaired.ExecutableScriptBundle.Reproducibility.BundleHashSHA256 == original.ExecutableScriptBundle.Reproducibility.BundleHashSHA256 {
		t.Fatal("repair package should produce a new bundle hash after script/plan changes")
	}
	if !strings.Contains(repaired.ExecutableScriptBundle.ApprovalMarkdown.InlineMarkdown, "本次修复说明") {
		t.Fatalf("repair approval markdown should explain the script change: %s", repaired.ExecutableScriptBundle.ApprovalMarkdown.InlineMarkdown)
	}

	repairAt := now.Add(15 * time.Minute)
	service.now = fixedClock(repairAt)
	repairEnvelope := sampleEnvelopeForAppTest(t, repaired, repairAt)
	repairEnvelope.EnvelopeID = "env_sample_repair"
	repairEnvelope.IdempotencyKey = repaired.RepairContext.IdempotencyKey
	repairEnvelope.Crypto.Nonce = "nonce_sample_repair"
	repairUpload := uploadSamplePackageToIntake(t, service, repaired, repairEnvelope)
	if repairUpload.Status != model.ExchangePackageStatusAccepted || repairUpload.CloudJobID == originalUpload.CloudJobID {
		t.Fatalf("repair upload should create a new accepted cloud job: original=%+v repair=%+v", originalUpload, repairUpload)
	}

	repairStatus, err := service.Status(ctx, repaired.OrgID, repairUpload.ExchangePackageID)
	if err != nil {
		t.Fatal(err)
	}
	if repairStatus.Status != model.ExchangePackageStatusAccepted || repairStatus.Stage != "accepted" {
		t.Fatalf("repair package should wait for human-approved cloud run: %+v", repairStatus)
	}
}

func uploadSamplePackageToIntake(t *testing.T, service *ExchangeIntakeService, pkg model.ClientExecutionPackage, envelope model.ExchangeEnvelope) model.ExecutionPackageUploadResponse {
	t.Helper()
	initResp, err := service.Init(t.Context(), model.ExecutionPackageInitRequest{
		OrgID:       pkg.OrgID,
		ProjectID:   pkg.ProjectID,
		PackageKind: model.ExchangePackageKindClientExecution,
		Producer:    envelope.Producer,
	})
	if err != nil {
		t.Fatal(err)
	}
	uploadResp, err := service.Upload(t.Context(), model.ExecutionPackageUploadRequest{UploadID: initResp.UploadID, Envelope: envelope, PayloadRef: envelope.PayloadRef}, pkg)
	if err != nil {
		t.Fatal(err)
	}
	return uploadResp
}

func sampleRepairedClientExecutionPackageForAppTest(t *testing.T, original model.ClientExecutionPackage, failed model.RecordingResultPackage, repairedAt time.Time) model.ClientExecutionPackage {
	t.Helper()
	repaired := original
	repaired.PackageID = original.PackageID + "_repair_1"
	repaired.CreatedAt = repairedAt
	repaired.ApprovedAt = repairedAt
	repaired.SafetyReport.HumanApproval = model.UserApprovalRecord{
		ApprovalID:       "approval_repair_1",
		ApprovedByUserID: "user_1",
		ApprovedAt:       repairedAt,
		PlanDigestSHA256: original.Reproducibility.GraphHashSHA256,
		ReviewedNodeIDs:  []string{"node_open_dashboard"},
		Notes:            []string{"修复 selector 等待时间，未新增域名、凭据或禁用页面。"},
	}

	baseBundle := original.ExecutableScriptBundle
	basePlanHash := baseBundle.Reproducibility.PlanHashSHA256
	baseBundleHash := baseBundle.Reproducibility.BundleHashSHA256
	bundleCopy := *baseBundle
	planCopy := *baseBundle.PlanJSON
	planCopy.ID = "script_repair_1"
	planCopy.UpdatedAt = repairedAt
	planCopy.Steps = append([]model.ScriptStep{}, baseBundle.PlanJSON.Steps...)
	planCopy.Steps[0].Action.TimeoutMS = 15000
	planCopy.Steps[0].Timing.DurationMS = 1800
	planCopy.Steps[0].Narrative.Title = "重新打开仪表盘"
	planHash, err := planCopy.ComputeScriptHash()
	if err != nil {
		t.Fatal(err)
	}
	planCopy.Reproducibility.ScriptHashSHA256 = planHash

	source := `type CascadeRecordingContext = { page: any; secrets: any; capture: any; assert: any; log: any };
type CascadeRecordingResult = { ok: boolean };
export async function runCascadeRecording(ctx: CascadeRecordingContext): Promise<CascadeRecordingResult> {
  await ctx.log.step("node_open_dashboard", "Repair: open dashboard with longer wait");
  await ctx.page.goto("https://app.example.com/dashboard");
  await ctx.page.waitForLoadState("networkidle");
  return { ok: true };
}`
	markdown := "# Approval\n\n## 本次修复说明\n\n- 失败节点：node_open_dashboard\n- 修改内容：增加 networkidle 等待，并把步骤时长调整为 1800ms。\n- 未新增域名、凭据或禁用页面。"
	scriptHash := model.SHA256Hex([]byte(source))
	markdownHash := model.SHA256Hex([]byte(markdown))

	bundleCopy.ID = "bundle_repair_1"
	bundleCopy.Status = model.ExecutableScriptBundleStatusValidated
	bundleCopy.PlanJSON = &planCopy
	bundleCopy.PlaywrightScript = model.ExecutableScriptSource{InlineSource: source, MimeType: "text/typescript", SHA256: scriptHash, SizeBytes: int64(len(source))}
	bundleCopy.ApprovalMarkdown = model.ApprovalMarkdownDocument{InlineMarkdown: markdown, MimeType: "text/markdown", SHA256: markdownHash, SizeBytes: int64(len(markdown))}
	bundleCopy.SecurityPolicy.AllowedPageMethods = []string{"goto", "waitForLoadState"}
	bundleCopy.Reproducibility.PlanHashSHA256 = planHash
	bundleCopy.Reproducibility.ScriptHashSHA256 = scriptHash
	bundleCopy.Reproducibility.MarkdownHashSHA256 = markdownHash
	bundleCopy.Reproducibility.BundleHashSHA256 = ""
	bundleCopy.RepairLineage = &model.ScriptRepairLineage{
		BaseBundleID:         baseBundle.ID,
		BaseBundleHashSHA256: baseBundleHash,
		SourceResultID:       failed.ResultID,
		SourceCloudJobID:     failed.CloudJobID,
		RepairAttempt:        1,
		ChangeSummary:        "增加页面稳定等待并调整节点时长。",
		DiagnosticRefs:       []model.EvidenceRef{{ID: failed.FailureDiagnostic.ID, Kind: model.EvidenceKindBrowserTrace}},
		CreatedAt:            repairedAt,
	}
	bundleHash, err := bundleCopy.ComputeBundleHash()
	if err != nil {
		t.Fatal(err)
	}
	bundleCopy.Reproducibility.BundleHashSHA256 = bundleHash
	repaired.ExecutableScriptBundle = &bundleCopy
	repaired.RecordingRunSpec = planCopy.RecordingRunSpec
	repaired.Reproducibility.ScriptHashSHA256 = planHash
	repaired.RepairContext = &model.ScriptRepairContext{
		SourceResultID:       failed.ResultID,
		SourcePackageID:      failed.SourcePackageID,
		SourceCloudJobID:     failed.CloudJobID,
		RepairAttempt:        1,
		BaseBundleID:         baseBundle.ID,
		BaseBundleHashSHA256: baseBundleHash,
		BasePlanHashSHA256:   basePlanHash,
		DiagnosticRefs:       []model.EvidenceRef{{ID: failed.FailureDiagnostic.ID, Kind: model.EvidenceKindBrowserTrace}},
		FailureDiagnostic:    failed.FailureDiagnostic,
		UserApproval:         &repaired.SafetyReport.HumanApproval,
		IdempotencyKey:       failed.SourcePackageID + ":repair:1:" + bundleHash,
	}
	return repaired
}
