package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/model"
)

func TestExchangeIntakeServiceLifecycle(t *testing.T) {
	service := NewExchangeIntakeService(nil)
	service.now = fixedClock(time.Date(2026, 7, 9, 16, 0, 0, 0, time.UTC))
	ctx := context.Background()
	pkg := sampleClientExecutionPackageForAppTest(t)

	initResp, err := service.Init(ctx, model.ExecutionPackageInitRequest{
		OrgID:       pkg.OrgID,
		ProjectID:   pkg.ProjectID,
		PackageKind: model.ExchangePackageKindClientExecution,
		Producer:    model.ExchangeProducer{AppVersion: "test", RuntimeProfile: "cloud"},
	})
	if err != nil {
		t.Fatal(err)
	}
	envelope := sampleEnvelopeForAppTest(t, pkg, service.now())
	uploadResp, err := service.Upload(ctx, model.ExecutionPackageUploadRequest{UploadID: initResp.UploadID, Envelope: envelope}, pkg)
	if err != nil {
		t.Fatal(err)
	}
	if uploadResp.Status != model.ExchangePackageStatusAccepted || uploadResp.ExchangePackageID == "" || uploadResp.CloudJobID == "" {
		t.Fatalf("unexpected upload response: %+v", uploadResp)
	}

	status, err := service.Status(ctx, pkg.OrgID, uploadResp.ExchangePackageID)
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != model.ExchangePackageStatusAccepted {
		t.Fatalf("expected accepted status, got %+v", status)
	}
	if status.Stage != "accepted" || status.ProgressPercent != 10 || status.Message == "" {
		t.Fatalf("expected accepted status to include dev-facing progress metadata, got %+v", status)
	}
	if len(status.StageHistory) != 1 || status.StageHistory[0].Stage != "accepted" {
		t.Fatalf("expected accepted stage history, got %+v", status.StageHistory)
	}

	if _, _, err := service.StartExecution(ctx, pkg.OrgID, uploadResp.ExchangePackageID); err != nil {
		t.Fatal(err)
	}
	running, err := service.MarkExecutionStage(ctx, pkg.OrgID, uploadResp.ExchangePackageID, "running_script", "Running test script.", 55)
	if err != nil {
		t.Fatal(err)
	}
	if running.Stage != "running_script" || running.ProgressPercent != 55 || len(running.StageHistory) < 3 {
		t.Fatalf("expected running stage history, got %+v", running)
	}

	result := sampleRecordingResultForAppTest(pkg)
	completed, err := service.CompleteWithRecordingResult(ctx, pkg.OrgID, uploadResp.ExchangePackageID, result)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != model.ExchangePackageStatusCompleted || completed.ResultPackageID == "" {
		t.Fatalf("expected completed package with result id, got %+v", completed)
	}
	if completed.Stage != "completed" || completed.ProgressPercent != 100 || completed.ResultSummary == nil {
		t.Fatalf("expected completed status summary, got %+v", completed)
	}
	if len(completed.StageHistory) < 4 || completed.StageHistory[len(completed.StageHistory)-1].Stage != "completed" {
		t.Fatalf("expected completed stage history, got %+v", completed.StageHistory)
	}
	if completed.ResultSummary.DemoVideoCount != 1 || completed.ResultSummary.RawRecordingCount != 1 || completed.ResultSummary.PrimaryDemoVideoURI == "" {
		t.Fatalf("unexpected result summary: %+v", completed.ResultSummary)
	}
	if completed.ResultSummary.DeliveryStatus != model.ResultDeliveryStatusReady || !completed.ResultSummary.AckRequired || completed.ResultSummary.ExpiresAt.IsZero() {
		t.Fatalf("result summary should expose ready delivery state, got %+v", completed.ResultSummary)
	}
	if got := findDeliverable(completed.ResultSummary.Deliverables, "demo_video"); got == nil || got.Role != "final_demo" || !got.IncludeInDemo || got.URI == "" || got.SHA256 != "sha_demo_video" || got.SizeBytes != 2048 {
		t.Fatalf("result summary should expose final demo deliverable, got %+v", completed.ResultSummary.Deliverables)
	}
	if got := findDeliverable(completed.ResultSummary.Deliverables, "raw_recording"); got == nil || got.URI == "" {
		t.Fatalf("result summary should expose raw recording deliverable, got %+v", completed.ResultSummary.Deliverables)
	}

	gotResult, err := service.GetResultPackage(ctx, pkg.OrgID, completed.ResultPackageID)
	if err != nil {
		t.Fatal(err)
	}
	if gotResult.ResultID != result.ResultID || gotResult.SourcePackageID != pkg.PackageID {
		t.Fatalf("result package mismatch: %+v", gotResult)
	}
	if gotResult.Delivery.DeliveredAt.IsZero() {
		t.Fatalf("result package get should mark delivery, got %+v", gotResult.Delivery)
	}

	ack, err := service.AckResultPackage(ctx, pkg.OrgID, model.ResultPackageAckRequest{ResultPackageID: completed.ResultPackageID, AckedByInstallID: "install_1", ReceivedAssetIDs: []string{"artifact_demo_video"}, VerifiedChecksums: true})
	if err != nil {
		t.Fatal(err)
	}
	if ack.Status != model.RecordingResultStatusAcked || ack.DeliveryStatus != model.ResultDeliveryStatusAcked || ack.ResultPackageID != completed.ResultPackageID || ack.AckedByInstallID != "install_1" || !ack.VerifiedChecksums {
		t.Fatalf("unexpected ack response: %+v", ack)
	}
	if len(ack.ReceivedAssetIDs) != 1 || ack.ReceivedAssetIDs[0] != "artifact_demo_video" {
		t.Fatalf("ack response should echo received asset ids, got %+v", ack)
	}
	ackedStatus, err := service.Status(ctx, pkg.OrgID, completed.ExchangePackageID)
	if err != nil {
		t.Fatal(err)
	}
	if ackedStatus.ResultSummary == nil || ackedStatus.ResultSummary.ResultStatus != model.RecordingResultStatusAcked || ackedStatus.ResultSummary.DeliveryStatus != model.ResultDeliveryStatusAcked || ackedStatus.ResultSummary.AckedAt.IsZero() {
		t.Fatalf("status should expose acked result summary, got %+v", ackedStatus.ResultSummary)
	}
	results, err := service.ListResultPackages(ctx, pkg.OrgID)
	if err != nil {
		t.Fatal(err)
	}
	if len(results.Items) != 1 || results.Items[0].DeliveryStatus != model.ResultDeliveryStatusAcked || results.Items[0].AckedByInstallID != "install_1" || results.Items[0].AckedAt.IsZero() {
		t.Fatalf("result list should expose acked delivery state, got %+v", results.Items)
	}
}

func TestResultReviewAndRevisionAreIdempotentAndRouteByIssue(t *testing.T) {
	service := NewExchangeIntakeService(nil)
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	service.now = fixedClock(now)
	ctx := context.Background()
	pkg := sampleClientExecutionPackageForAppTest(t)
	initResp, err := service.Init(ctx, model.ExecutionPackageInitRequest{OrgID: pkg.OrgID, ProjectID: pkg.ProjectID, PackageKind: model.ExchangePackageKindClientExecution})
	if err != nil {
		t.Fatal(err)
	}
	upload, err := service.Upload(ctx, model.ExecutionPackageUploadRequest{UploadID: initResp.UploadID, Envelope: sampleEnvelopeForAppTest(t, pkg, now)}, pkg)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.StartExecution(ctx, pkg.OrgID, upload.ExchangePackageID); err != nil {
		t.Fatal(err)
	}
	completed, err := service.CompleteWithRecordingResult(ctx, pkg.OrgID, upload.ExchangePackageID, sampleRecordingResultForAppTest(pkg))
	if err != nil {
		t.Fatal(err)
	}

	reviewRequest := model.ResultReviewRequest{IdempotencyKey: "review-1", Decision: model.ResultReviewReeditRequested, Summary: "字幕需要调整"}
	firstReview, err := service.ReviewResultPackage(ctx, pkg.OrgID, completed.ResultPackageID, reviewRequest)
	if err != nil {
		t.Fatal(err)
	}
	secondReview, err := service.ReviewResultPackage(ctx, pkg.OrgID, completed.ResultPackageID, reviewRequest)
	if err != nil {
		t.Fatal(err)
	}
	if firstReview.ReviewID == "" || firstReview.ReviewID != secondReview.ReviewID {
		t.Fatalf("review idempotency failed: first=%+v second=%+v", firstReview, secondReview)
	}

	revisionRequest := model.ResultRevisionRequest{
		IdempotencyKey: "revision-1", RequestedAction: model.ResultRevisionAuto,
		Issues: []model.ResultRevisionIssue{{Kind: "missing_step", Comment: "缺少保存成功页面"}},
	}
	firstRevision, err := service.RequestResultRevision(ctx, pkg.OrgID, completed.ResultPackageID, revisionRequest)
	if err != nil {
		t.Fatal(err)
	}
	secondRevision, err := service.RequestResultRevision(ctx, pkg.OrgID, completed.ResultPackageID, revisionRequest)
	if err != nil {
		t.Fatal(err)
	}
	if firstRevision.RevisionID == "" || firstRevision.RevisionID != secondRevision.RevisionID || firstRevision.ResolvedAction != model.ResultRevisionRerecord {
		t.Fatalf("revision routing/idempotency failed: first=%+v second=%+v", firstRevision, secondRevision)
	}
}

func TestExchangeIntakePreflightDoesNotConsumeNonceOrPersistPackage(t *testing.T) {
	service := NewExchangeIntakeService(nil)
	service.now = fixedClock(time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC))
	ctx := context.Background()
	pkg := sampleClientExecutionPackageForAppTest(t)
	envelope := sampleEnvelopeForAppTest(t, pkg, service.now())
	request := model.ExecutionPackageUploadRequest{Envelope: envelope, PayloadRef: envelope.PayloadRef}

	if err := service.ValidateUpload(ctx, request, pkg); err != nil {
		t.Fatalf("preflight must accept a valid package: %v", err)
	}
	if len(service.packages) != 0 || len(service.seenNonces) != 0 {
		t.Fatalf("preflight must not persist an execution package or consume the nonce: packages=%d nonces=%d", len(service.packages), len(service.seenNonces))
	}
	init, err := service.Init(ctx, model.ExecutionPackageInitRequest{OrgID: pkg.OrgID, ProjectID: pkg.ProjectID, PackageKind: model.ExchangePackageKindClientExecution})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Upload(ctx, model.ExecutionPackageUploadRequest{UploadID: init.UploadID, Envelope: envelope, PayloadRef: envelope.PayloadRef}, pkg); err != nil {
		t.Fatalf("the same package must remain uploadable after preflight: %v", err)
	}
}

func TestExchangeIntakeRejectsInlinePayloadWhenCloudTransportIsRequired(t *testing.T) {
	service := NewExchangeIntakeService(nil)
	service.SetInlinePayloadAllowed(false)
	service.now = fixedClock(time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC))
	pkg := sampleClientExecutionPackageForAppTest(t)
	init, err := service.Init(t.Context(), model.ExecutionPackageInitRequest{OrgID: pkg.OrgID, ProjectID: pkg.ProjectID, PackageKind: model.ExchangePackageKindClientExecution})
	if err != nil {
		t.Fatal(err)
	}
	envelope := sampleEnvelopeForAppTest(t, pkg, service.now())
	_, err = service.Upload(t.Context(), model.ExecutionPackageUploadRequest{UploadID: init.UploadID, Envelope: envelope, PayloadRef: envelope.PayloadRef}, pkg)
	if exchangeErrorCode(err, "") != "production_inline_payload_forbidden" {
		t.Fatalf("cloud transport must reject an inline package, got %v", err)
	}
}

func TestExchangeIntakeAcceptsEncryptedPayloadRefWithoutPersistingPlainPayload(t *testing.T) {
	service := NewExchangeIntakeService(nil)
	service.now = fixedClock(time.Date(2026, 7, 9, 16, 5, 0, 0, time.UTC))
	ctx := context.Background()
	pkg := sampleClientExecutionPackageForAppTest(t)
	initResp, err := service.Init(ctx, model.ExecutionPackageInitRequest{
		OrgID:       pkg.OrgID,
		ProjectID:   pkg.ProjectID,
		PackageKind: model.ExchangePackageKindClientExecution,
	})
	if err != nil {
		t.Fatal(err)
	}
	envelope := sampleEnvelopeForAppTest(t, pkg, service.now())
	envelope.Crypto.Nonce = "nonce_payload_ref_only"
	envelope.PayloadRef.Kind = model.PayloadRefKindArtifact
	envelope.PayloadRef.ArtifactID = "payload_artifact_1"
	envelope.PayloadRef.URI = "s3://cascade-exchange/payload.enc"
	envelope.PayloadRef.InlineCiphertext = ""
	envelope.PayloadRef.SHA256 = "ciphertext_hash_1"
	envelope.PayloadRef.SizeBytes = 2048
	envelope.PayloadRef.Encrypted = true
	envelope.PayloadRef.Sensitive = true
	envelope.Crypto.CiphertextDigestSHA256 = envelope.PayloadRef.SHA256
	uploadResp, err := service.Upload(ctx, model.ExecutionPackageUploadRequest{UploadID: initResp.UploadID, Envelope: envelope, PayloadRef: envelope.PayloadRef}, model.ClientExecutionPackage{})
	if err != nil {
		t.Fatal(err)
	}
	status, err := service.Status(ctx, pkg.OrgID, uploadResp.ExchangePackageID)
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != model.ExchangePackageStatusAccepted || status.Stage != "accepted" {
		t.Fatalf("expected encrypted payload ref upload to be accepted, got %+v", status)
	}
	if _, _, err := service.StartExecution(ctx, pkg.OrgID, uploadResp.ExchangePackageID); err == nil {
		t.Fatal("expected envelope-only package to require worker decryption before execution")
	}
}

func TestExchangeAckRequiresChecksumVerification(t *testing.T) {
	service := NewExchangeIntakeService(nil)
	service.now = fixedClock(time.Date(2026, 7, 9, 16, 8, 0, 0, time.UTC))
	ctx := context.Background()
	pkg := sampleClientExecutionPackageForAppTest(t)
	initResp, err := service.Init(ctx, model.ExecutionPackageInitRequest{OrgID: pkg.OrgID, ProjectID: pkg.ProjectID, PackageKind: model.ExchangePackageKindClientExecution})
	if err != nil {
		t.Fatal(err)
	}
	envelope := sampleEnvelopeForAppTest(t, pkg, service.now())
	envelope.Crypto.Nonce = "nonce_ack_checksum"
	uploadResp, err := service.Upload(ctx, model.ExecutionPackageUploadRequest{UploadID: initResp.UploadID, Envelope: envelope}, pkg)
	if err != nil {
		t.Fatal(err)
	}
	result := sampleRecordingResultForAppTest(pkg)
	completed, err := service.CompleteWithRecordingResult(ctx, pkg.OrgID, uploadResp.ExchangePackageID, result)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.AckResultPackage(ctx, pkg.OrgID, model.ResultPackageAckRequest{ResultPackageID: completed.ResultPackageID, AckedByInstallID: "install_1"}); err == nil {
		t.Fatal("expected ack without verified_checksums to be rejected")
	}
	if _, err := service.AckResultPackage(ctx, pkg.OrgID, model.ResultPackageAckRequest{ResultPackageID: completed.ResultPackageID, AckedByInstallID: "install_1", VerifiedChecksums: true, ChecksumMismatchIDs: []string{"artifact_demo_video"}}); err == nil {
		t.Fatal("expected ack with checksum mismatch ids to be rejected")
	}
}

func TestDesktopBridgeExchangePackageResponsesAreJSONSafe(t *testing.T) {
	bridge, err := NewDesktopBridge(config.AppRuntimeConfig{
		Profile:         config.ProfileDev,
		Environment:     "test",
		Mode:            model.AppModeWeb,
		DatabaseDialect: config.DatabaseSQLite,
		DataRoot:        t.TempDir(),
		ArtifactRoot:    t.TempDir(),
		CacheRoot:       t.TempDir(),
		LogRoot:         t.TempDir(),
		ResourceRoot:    t.TempDir(),
		DevRepoRoot:     t.TempDir(),
		SidecarPaths:    map[string]string{},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	pkg := sampleClientExecutionPackageForAppTest(t)

	initResponse := bridge.InitExecutionPackage(model.ExecutionPackageInitRequest{OrgID: pkg.OrgID, ProjectID: pkg.ProjectID, PackageKind: model.ExchangePackageKindClientExecution})
	if !initResponse.OK {
		t.Fatalf("InitExecutionPackage error: %s", initResponse.Error)
	}
	var initPayload model.ExecutionPackageInitResponse
	if err := json.Unmarshal(initResponse.Data, &initPayload); err != nil {
		t.Fatal(err)
	}
	if initPayload.UploadID == "" {
		t.Fatalf("expected upload id: %+v", initPayload)
	}

	envelope := sampleEnvelopeForAppTest(t, pkg, time.Now().UTC())
	uploadResponse := bridge.UploadExecutionPackage(model.ExecutionPackageUploadRequest{UploadID: initPayload.UploadID, Envelope: envelope}, pkg)
	if !uploadResponse.OK {
		t.Fatalf("UploadExecutionPackage error: %s", uploadResponse.Error)
	}
	var uploadPayload model.ExecutionPackageUploadResponse
	if err := json.Unmarshal(uploadResponse.Data, &uploadPayload); err != nil {
		t.Fatal(err)
	}
	if uploadPayload.Status != model.ExchangePackageStatusAccepted {
		t.Fatalf("expected accepted upload: %+v", uploadPayload)
	}
}

func TestExchangeIntakeServiceCompletesWithFailedRecordingResult(t *testing.T) {
	service := NewExchangeIntakeService(nil)
	service.now = fixedClock(time.Date(2026, 7, 9, 17, 0, 0, 0, time.UTC))
	ctx := context.Background()
	pkg := sampleClientExecutionPackageForAppTest(t)
	initResp, err := service.Init(ctx, model.ExecutionPackageInitRequest{OrgID: pkg.OrgID, ProjectID: pkg.ProjectID, PackageKind: model.ExchangePackageKindClientExecution})
	if err != nil {
		t.Fatal(err)
	}
	envelope := sampleEnvelopeForAppTest(t, pkg, service.now())
	envelope.Crypto.Nonce = "nonce_failed_result"
	uploadResp, err := service.Upload(ctx, model.ExecutionPackageUploadRequest{UploadID: initResp.UploadID, Envelope: envelope}, pkg)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.StartExecution(ctx, pkg.OrgID, uploadResp.ExchangePackageID); err != nil {
		t.Fatal(err)
	}
	failed := sampleFailedRecordingResultForAppTest(pkg)

	status, err := service.CompleteWithRecordingResult(ctx, pkg.OrgID, uploadResp.ExchangePackageID, failed)
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != model.ExchangePackageStatusFailed || status.ResultPackageID == "" || status.FailureSummary == nil {
		t.Fatalf("expected failed status with result and summary, got %+v", status)
	}
	if status.FailureSummary.FailedNodeID != "node_open_dashboard" || status.FailureSummary.CurrentURL == "" {
		t.Fatalf("unexpected failure summary: %+v", status.FailureSummary)
	}
	got, err := service.GetResultPackage(ctx, pkg.OrgID, status.ResultPackageID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != model.RecordingResultStatusFailed || got.FailureDiagnostic == nil {
		t.Fatalf("expected failed result package, got %+v", got)
	}
}

func TestExchangeIntakeServicePersistsCompletedResult(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 7, 10, 9, 0, 0, 0, time.UTC)
	service := newExchangeIntakeService(nil, newFileExchangeSnapshotStore(root))
	service.now = fixedClock(now)
	ctx := context.Background()
	pkg := sampleClientExecutionPackageForAppTest(t)

	initResp, err := service.Init(ctx, model.ExecutionPackageInitRequest{OrgID: pkg.OrgID, ProjectID: pkg.ProjectID, PackageKind: model.ExchangePackageKindClientExecution})
	if err != nil {
		t.Fatal(err)
	}
	envelope := sampleEnvelopeForAppTest(t, pkg, now)
	envelope.Crypto.Nonce = "nonce_persisted_result"
	uploadResp, err := service.Upload(ctx, model.ExecutionPackageUploadRequest{UploadID: initResp.UploadID, Envelope: envelope}, pkg)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.StartExecution(ctx, pkg.OrgID, uploadResp.ExchangePackageID); err != nil {
		t.Fatal(err)
	}
	completed, err := service.CompleteWithRecordingResult(ctx, pkg.OrgID, uploadResp.ExchangePackageID, sampleRecordingResultForAppTest(pkg))
	if err != nil {
		t.Fatal(err)
	}

	reloaded := newExchangeIntakeService(nil, newFileExchangeSnapshotStore(root))
	reloaded.now = fixedClock(now.Add(time.Minute))
	status, err := reloaded.Status(ctx, pkg.OrgID, uploadResp.ExchangePackageID)
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != model.ExchangePackageStatusCompleted || status.ResultPackageID != completed.ResultPackageID || status.ResultSummary == nil {
		t.Fatalf("persisted status mismatch: %+v", status)
	}
	if len(status.StageHistory) < 3 {
		t.Fatalf("expected persisted stage history, got %+v", status.StageHistory)
	}
	result, err := reloaded.GetResultPackage(ctx, pkg.OrgID, completed.ResultPackageID)
	if err != nil {
		t.Fatal(err)
	}
	if result.ResultID != "result_1" || result.SourcePackageID != pkg.PackageID {
		t.Fatalf("persisted result mismatch: %+v", result)
	}
	artifact, err := reloaded.GetResultArtifact(ctx, pkg.OrgID, completed.ResultPackageID, "artifact_demo_video")
	if err != nil {
		t.Fatal(err)
	}
	if artifact.Kind != "demo_video" || artifact.URI == "" {
		t.Fatalf("persisted artifact mismatch: %+v", artifact)
	}
}

func TestExchangeIntakeServiceFailsRunningPackageAfterRestart(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 7, 10, 10, 30, 0, 0, time.UTC)
	service := newExchangeIntakeService(nil, newFileExchangeSnapshotStore(root))
	service.now = fixedClock(now)
	ctx := context.Background()
	pkg := sampleClientExecutionPackageForAppTest(t)

	initResp, err := service.Init(ctx, model.ExecutionPackageInitRequest{OrgID: pkg.OrgID, ProjectID: pkg.ProjectID, PackageKind: model.ExchangePackageKindClientExecution})
	if err != nil {
		t.Fatal(err)
	}
	envelope := sampleEnvelopeForAppTest(t, pkg, now)
	envelope.Crypto.Nonce = "nonce_restart_running"
	uploadResp, err := service.Upload(ctx, model.ExecutionPackageUploadRequest{UploadID: initResp.UploadID, Envelope: envelope}, pkg)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.StartExecution(ctx, pkg.OrgID, uploadResp.ExchangePackageID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.MarkExecutionStage(ctx, pkg.OrgID, uploadResp.ExchangePackageID, "running_script", "Running script when process exits.", 55); err != nil {
		t.Fatal(err)
	}

	reloaded := newExchangeIntakeService(nil, newFileExchangeSnapshotStore(root))
	status, err := reloaded.Status(ctx, pkg.OrgID, uploadResp.ExchangePackageID)
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != model.ExchangePackageStatusFailed || status.Error == nil || status.Error.Code != "interrupted_by_restart" {
		t.Fatalf("expected running package to fail after restart, got %+v", status)
	}
	if status.FailureSummary == nil || status.FailureSummary.FailedStage != "running_script" {
		t.Fatalf("restart failure should preserve failed stage, got %+v", status.FailureSummary)
	}

	list, err := reloaded.ListExecutionPackages(ctx, pkg.OrgID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 1 || list.Items[0].FailureSummary == nil || list.Items[0].FailureSummary.Code != "interrupted_by_restart" {
		t.Fatalf("list should expose restart failure summary, got %+v", list.Items)
	}
}

func TestExchangeIntakeServiceCancelsRunningPackageAndPreservesTerminalStatus(t *testing.T) {
	service := NewExchangeIntakeService(nil)
	service.now = fixedClock(time.Date(2026, 7, 10, 11, 10, 0, 0, time.UTC))
	ctx := context.Background()
	pkg := sampleClientExecutionPackageForAppTest(t)

	initResp, err := service.Init(ctx, model.ExecutionPackageInitRequest{OrgID: pkg.OrgID, ProjectID: pkg.ProjectID, PackageKind: model.ExchangePackageKindClientExecution})
	if err != nil {
		t.Fatal(err)
	}
	envelope := sampleEnvelopeForAppTest(t, pkg, service.now())
	envelope.Crypto.Nonce = "nonce_cancel_running"
	uploadResp, err := service.Upload(ctx, model.ExecutionPackageUploadRequest{UploadID: initResp.UploadID, Envelope: envelope}, pkg)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.StartExecution(ctx, pkg.OrgID, uploadResp.ExchangePackageID); err != nil {
		t.Fatal(err)
	}

	canceled, err := service.CancelExecution(ctx, pkg.OrgID, uploadResp.ExchangePackageID, "test_cancel")
	if err != nil {
		t.Fatal(err)
	}
	if canceled.Status != model.ExchangePackageStatusCanceled || canceled.Stage != "canceled" || canceled.Error == nil || canceled.Error.Code != "test_cancel" {
		t.Fatalf("expected canceled package status, got %+v", canceled)
	}
	if canceled.FailureSummary == nil || canceled.FailureSummary.Code != "test_cancel" {
		t.Fatalf("canceled package should expose cancellation summary, got %+v", canceled.FailureSummary)
	}

	if updated, err := service.MarkExecutionStage(ctx, pkg.OrgID, uploadResp.ExchangePackageID, "running_script", "late progress", 80); err != nil {
		t.Fatal(err)
	} else if updated.Status != model.ExchangePackageStatusCanceled || updated.Stage != "canceled" {
		t.Fatalf("late progress must not overwrite canceled status, got %+v", updated)
	}
	if completed, err := service.CompleteWithRecordingResult(ctx, pkg.OrgID, uploadResp.ExchangePackageID, sampleRecordingResultForAppTest(pkg)); err != nil {
		t.Fatal(err)
	} else if completed.Status != model.ExchangePackageStatusCanceled || completed.ResultPackageID != "" {
		t.Fatalf("late result must not overwrite canceled status, got %+v", completed)
	}
	if failed, err := service.FailExecution(ctx, pkg.OrgID, uploadResp.ExchangePackageID, "late_failure", errors.New("late failure")); err != nil {
		t.Fatal(err)
	} else if failed.Status != model.ExchangePackageStatusCanceled || failed.Error == nil || failed.Error.Code != "test_cancel" {
		t.Fatalf("late failure must not overwrite canceled status, got %+v", failed)
	}
}

func TestExchangeIntakeServiceGeneratesUniqueUploadIDsUnderFixedClock(t *testing.T) {
	service := NewExchangeIntakeService(nil)
	now := time.Date(2026, 7, 10, 11, 30, 0, 0, time.UTC)
	service.now = fixedClock(now)
	ctx := context.Background()
	seen := map[string]bool{}
	for index := 0; index < 20; index++ {
		response, err := service.Init(ctx, model.ExecutionPackageInitRequest{
			OrgID:       "org_1",
			ProjectID:   "project_1",
			PackageKind: model.ExchangePackageKindClientExecution,
		})
		if err != nil {
			t.Fatal(err)
		}
		if response.UploadID == "" {
			t.Fatal("upload id is required")
		}
		if seen[response.UploadID] {
			t.Fatalf("duplicate upload id generated: %s", response.UploadID)
		}
		seen[response.UploadID] = true
	}
}

func TestExchangeIntakeServiceListsPackagesWithoutPayloadDump(t *testing.T) {
	service := NewExchangeIntakeService(nil)
	now := time.Date(2026, 7, 10, 11, 0, 0, 0, time.UTC)
	service.now = fixedClock(now)
	ctx := context.Background()
	pkg := sampleClientExecutionPackageForAppTest(t)

	initResp, err := service.Init(ctx, model.ExecutionPackageInitRequest{OrgID: pkg.OrgID, ProjectID: pkg.ProjectID, PackageKind: model.ExchangePackageKindClientExecution})
	if err != nil {
		t.Fatal(err)
	}
	envelope := sampleEnvelopeForAppTest(t, pkg, now)
	envelope.Crypto.Nonce = "nonce_list_packages"
	uploadResp, err := service.Upload(ctx, model.ExecutionPackageUploadRequest{UploadID: initResp.UploadID, Envelope: envelope}, pkg)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.StartExecution(ctx, pkg.OrgID, uploadResp.ExchangePackageID); err != nil {
		t.Fatal(err)
	}
	completed, err := service.CompleteWithRecordingResult(ctx, pkg.OrgID, uploadResp.ExchangePackageID, sampleRecordingResultForAppTest(pkg))
	if err != nil {
		t.Fatal(err)
	}

	executions, err := service.ListExecutionPackages(ctx, pkg.OrgID)
	if err != nil {
		t.Fatal(err)
	}
	if len(executions.Items) != 1 {
		t.Fatalf("expected one execution package, got %+v", executions.Items)
	}
	execution := executions.Items[0]
	if execution.ExchangePackageID != uploadResp.ExchangePackageID || execution.ResultPackageID != completed.ResultPackageID {
		t.Fatalf("execution list identity mismatch: %+v", execution)
	}
	if execution.Status != model.ExchangePackageStatusCompleted || execution.ResultSummary == nil || execution.ResultSummary.DemoVideoCount != 1 {
		t.Fatalf("execution list should include status summary, got %+v", execution)
	}

	results, err := service.ListResultPackages(ctx, pkg.OrgID)
	if err != nil {
		t.Fatal(err)
	}
	if len(results.Items) != 1 {
		t.Fatalf("expected one result package, got %+v", results.Items)
	}
	result := results.Items[0]
	if result.ResultPackageID != completed.ResultPackageID || result.ExchangePackageID != uploadResp.ExchangePackageID {
		t.Fatalf("result list identity mismatch: %+v", result)
	}
	if result.Status != model.RecordingResultStatusGenerated || result.ResultSummary == nil || result.ResultSummary.DemoVideoCount != 1 {
		t.Fatalf("result list should include result summary, got %+v", result)
	}

	data, err := json.Marshal(struct {
		Executions model.ExecutionPackageListResponse `json:"executions"`
		Results    model.ResultPackageListResponse    `json:"results"`
	}{Executions: executions, Results: results})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "runCascadeRecording") || strings.Contains(string(data), "InlineSource") {
		t.Fatalf("list responses must not dump executable payloads: %s", string(data))
	}
}

func TestExecutionTimeoutUsesPolicyOrDefault(t *testing.T) {
	if got := executionTimeout(2); got != 2*time.Second {
		t.Fatalf("expected policy timeout to win, got %s", got)
	}
	if got := executionTimeout(0); got != time.Duration(defaultExecutionTimeoutSec)*time.Second {
		t.Fatalf("expected default timeout, got %s", got)
	}
}

func TestShouldMarkExecutionTimeoutDoesNotOverrideCompletedStatus(t *testing.T) {
	if shouldMarkExecutionTimeout(model.ExecutionPackageStatusResponse{
		ExchangePackageID: "xpkg_completed",
		Status:            model.ExchangePackageStatusCompleted,
	}) {
		t.Fatal("completed package must not be overwritten by timeout")
	}
	if shouldMarkExecutionTimeout(model.ExecutionPackageStatusResponse{
		ExchangePackageID: "xpkg_timeout",
		Status:            model.ExchangePackageStatusFailed,
		Error:             &model.AgentError{Code: "execution_timeout"},
	}) {
		t.Fatal("existing execution_timeout failure should not be rewritten")
	}
	if !shouldMarkExecutionTimeout(model.ExecutionPackageStatusResponse{
		ExchangePackageID: "xpkg_running",
		Status:            model.ExchangePackageStatusRunning,
	}) {
		t.Fatal("running package should be marked as timeout after deadline")
	}
}

func sampleClientExecutionPackageForAppTest(t *testing.T) model.ClientExecutionPackage {
	t.Helper()
	now := time.Date(2026, 7, 9, 15, 30, 0, 0, time.UTC)
	graph := model.NewDemoWorkflowGraph("graph_1", "project_1", "https://app.example.com/dashboard")
	graph.Status = model.GraphStatusApproved
	graph.Nodes = []*model.GraphNode{{
		ID:              "node_open_dashboard",
		Type:            model.GraphNodeTypeAction,
		ExpectedOutcome: "Dashboard loads",
		ActionSpec:      &model.GraphAction{Type: model.GraphActionNavigate, Target: model.ActionTarget{URL: "https://app.example.com/dashboard"}},
		Capture:         &model.CaptureSpec{Screenshot: true, Video: true},
	}}
	graph.Edges = []*model.GraphEdge{}
	graphDigest, err := model.DigestCanonicalJSON(graph)
	if err != nil {
		t.Fatal(err)
	}

	doc := &model.ExecutionScriptDocument{
		ID:              "script_1",
		ProjectID:       "project_1",
		WorkflowGraphID: graph.ID,
		GraphVersion:    graph.Version,
		SchemaVersion:   model.ExecutionScriptDocumentSchemaVersion,
		Status:          model.ScriptDocumentStatusApproved,
		WorkflowGraph:   graph,
		RecordingRunSpec: model.RecordingRunSpec{
			RunID:          "run_1",
			BaseURL:        "https://app.example.com",
			AllowedDomains: []string{"app.example.com"},
			Browser:        model.BrowserRunSpec{Engine: "chromium", Headless: true},
			Timeline:       model.RecordingTimeline{TargetDurationSec: 30},
			Outputs:        model.RecordingOutputRequest{RawRecording: true, Trace: true, ScreenshotPack: true, StepByStepDocs: true},
			Redactions:     model.RedactionPolicy{},
			FailurePolicy:  model.RecordingFailurePolicy{RetryAttempts: 1, SelectorRepairAllowed: true},
		},
		Steps: []model.ScriptStep{{
			ID:              "step_1",
			Order:           1,
			NodeID:          "node_open_dashboard",
			PageTarget:      model.ScriptPageTarget{URL: "https://app.example.com/dashboard"},
			Action:          model.ScriptActionInstruction{Type: model.GraphActionNavigate, Target: model.ActionTarget{URL: "https://app.example.com/dashboard"}},
			ExpectedOutcome: "Dashboard loads",
			Capture:         model.CaptureSpec{Screenshot: true, Video: true},
			Timing:          model.NodeTimingHint{NodeID: "node_open_dashboard", DurationMS: 1200},
			Narrative:       model.NarrativeCue{Title: "Open dashboard"},
			Blocking:        true,
		}},
		SafetyPolicy:      model.ScriptSafetyPolicy{AllowedDomains: []string{"app.example.com"}, Redactions: model.RedactionPolicy{}},
		Reproducibility:   model.ReproducibilitySpec{GraphHashSHA256: graphDigest},
		ApprovalChecklist: model.ScriptApprovalChecklist{HumanApprovalRequired: true, SourceSummaryOnly: true},
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	docHash, err := doc.ComputeScriptHash()
	if err != nil {
		t.Fatal(err)
	}
	doc.Reproducibility.ScriptHashSHA256 = docHash

	source := `type CascadeRecordingContext = { page: any; secrets: any; capture: any; assert: any; log: any };
type CascadeRecordingResult = { ok: boolean };
export async function runCascadeRecording(ctx: CascadeRecordingContext): Promise<CascadeRecordingResult> {
  await ctx.log.step("node_open_dashboard", "Open dashboard");
  await ctx.page.goto("https://app.example.com/dashboard");
  return { ok: true };
}`
	markdown := "# Approval\n\n1. Open dashboard"
	scriptHash := model.SHA256Hex([]byte(source))
	markdownHash := model.SHA256Hex([]byte(markdown))
	bundle := &model.ExecutableRecordingScriptBundle{
		ID:              "bundle_1",
		ProjectID:       "project_1",
		WorkflowGraphID: graph.ID,
		SchemaVersion:   model.ExecutableRecordingScriptBundleSchemaVersion,
		Status:          model.ExecutableScriptBundleStatusValidated,
		ScriptManifest: model.ExecutableScriptManifest{
			ScriptID:            "recording_graph_1",
			Version:             1,
			Language:            "typescript",
			Runtime:             "playwright-restricted-sandbox",
			EntryFunction:       "runCascadeRecording",
			Generator:           "test",
			GeneratorVersion:    "0.1.0",
			DependencyAllowlist: []string{},
			ContextAPIs:         []string{"ctx.page", "ctx.log"},
			StepNodeIDs:         []string{"node_open_dashboard"},
		},
		PlanJSON:         doc,
		PlaywrightScript: model.ExecutableScriptSource{InlineSource: source, MimeType: "text/typescript", SHA256: scriptHash, SizeBytes: int64(len(source))},
		ApprovalMarkdown: model.ApprovalMarkdownDocument{InlineMarkdown: markdown, MimeType: "text/markdown", SHA256: markdownHash, SizeBytes: int64(len(markdown))},
		SecurityPolicy: model.ExecutableScriptSecurityPolicy{
			AllowedDomains:       []string{"app.example.com"},
			Redactions:           model.RedactionPolicy{},
			AllowedContextAPIs:   []string{"ctx.page", "ctx.log"},
			AllowedPageMethods:   []string{"goto"},
			ForbiddenIdentifiers: []string{"import", "require", "eval", "process", "fetch"},
			NetworkPolicy:        "allowed_domains_only_via_ctx_page",
			FileSystemPolicy:     "no_direct_fs_access",
		},
		Reproducibility: model.ExecutableScriptReproducibility{
			PlanHashSHA256:     docHash,
			ScriptHashSHA256:   scriptHash,
			MarkdownHashSHA256: markdownHash,
			GraphHashSHA256:    graphDigest,
		},
		Validation: &model.ExecutableScriptValidation{Valid: true, ValidatedAt: now},
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	bundleHash, err := bundle.ComputeBundleHash()
	if err != nil {
		t.Fatal(err)
	}
	bundle.Reproducibility.BundleHashSHA256 = bundleHash

	return model.ClientExecutionPackage{
		PackageID:     "pkg_1",
		OrgID:         "org_1",
		ProjectID:     "project_1",
		SchemaVersion: model.ClientExecutionPackageSchemaVersion,
		CreatedAt:     now,
		ApprovedAt:    now,
		ProjectContextSummary: model.ProjectContextSummary{
			ContextID:      "ctx_1",
			SchemaVersion:  model.ProjectContextSchemaVersion,
			Mode:           model.AppModeWeb,
			ProductURL:     "https://app.example.com",
			TargetAudience: "sales",
		},
		ProductMapSummary:      model.ProductMapSummary{ProductMapID: "map_1", Version: 1, Summary: "Dashboard flow"},
		WorkflowGraph:          graph,
		RecordingRunSpec:       doc.RecordingRunSpec,
		ExecutableScriptBundle: bundle,
		CredentialGrants: []model.CredentialGrant{{
			GrantID:                  "grant_1",
			Kind:                     "demo_account",
			Purpose:                  "record_demo",
			CloudSecretRef:           "vault://grant_1",
			AllowedDomains:           []string{"app.example.com"},
			RotationRequiredAfterRun: true,
			DeleteAfterRun:           true,
		}},
		EvidenceBundle:  model.EvidenceBundle{EvidenceRefs: []model.EvidenceRef{{ID: "ev_1", Kind: model.EvidenceKindRequirementDoc}}},
		Reproducibility: model.ReproducibilitySpec{GraphHashSHA256: graphDigest, ScriptHashSHA256: docHash},
		SafetyReport: model.PackageSafetyReport{
			AllowedToUpload: true,
			UploadMode:      "structure_summary_only",
			HumanApproval: model.UserApprovalRecord{
				ApprovalID:       "approval_1",
				ApprovedByUserID: "user_1",
				ApprovedAt:       now,
				PlanDigestSHA256: graphDigest,
				ReviewedNodeIDs:  []string{"node_open_dashboard"},
			},
		},
	}
}

func sampleFailedRecordingResultForAppTest(pkg model.ClientExecutionPackage) model.RecordingResultPackage {
	now := time.Date(2026, 7, 9, 17, 10, 0, 0, time.UTC)
	diagnostic := &model.ScriptFailureDiagnostic{
		ID:              "diag_node_open_dashboard",
		SchemaVersion:   model.ScriptFailureDiagnosticSchemaVersion,
		SourcePackageID: pkg.PackageID,
		CloudJobID:      "job_1",
		FailedNodeID:    "node_open_dashboard",
		FailedStepOrder: 1,
		Error:           model.AgentError{Code: "navigation_failed", Message: "Navigation failed", Retryable: true},
		CurrentURL:      "https://app.example.com/dashboard",
		PageTitle:       "Dashboard",
		ScreenshotRefs: []model.PackageArtifactDescriptor{{
			ID:        "artifact_failure_screenshot_001",
			Role:      "failure_screenshot",
			Kind:      "failure_screenshot",
			URI:       "file:///tmp/failure-step-001.png",
			MimeType:  "image/png",
			SHA256:    "failure_hash",
			Encrypted: false,
			Sensitive: true,
			Metadata:  map[string]any{"dev_local_artifact": true},
		}},
		TraceRefs: []model.PackageArtifactDescriptor{{
			ID:        "artifact_browser_trace",
			Role:      "failure_trace",
			Kind:      "browser_trace",
			URI:       "file:///tmp/trace.zip",
			MimeType:  "application/zip",
			SHA256:    "trace_hash",
			Encrypted: false,
			Sensitive: false,
			Metadata:  map[string]any{"dev_local_artifact": true},
		}},
		RedactionReport: model.DiagnosticRedactionReport{Applied: true, FullHTMLIncluded: false},
		CapturedAt:      now,
	}
	return model.RecordingResultPackage{
		ResultID:        "result_failed_1",
		SourcePackageID: pkg.PackageID,
		CloudJobID:      "job_1",
		SchemaVersion:   model.RecordingResultPackageSchemaVersion,
		Status:          model.RecordingResultStatusFailed,
		ExecutionTrace: &model.ExecutionTrace{
			ID:              "trace_failed_1",
			WorkflowGraphID: pkg.WorkflowGraph.ID,
			GraphVersion:    pkg.WorkflowGraph.Version,
			PassRate:        0,
			StepResults:     []model.StepResult{{NodeID: "node_open_dashboard", Status: "failed", Error: &diagnostic.Error}},
			Artifacts:       []model.ArtifactRef{{ID: "artifact_failure_screenshot_001", Kind: "failure_screenshot", URI: "file:///tmp/failure-step-001.png", SHA256: "failure_hash", Sensitive: true, SourceNodeID: "node_open_dashboard"}},
		},
		StepResults:        []model.StepResult{{NodeID: "node_open_dashboard", Status: "failed", Error: &diagnostic.Error}},
		GeneratedAssets:    []model.ArtifactRef{{ID: "artifact_failure_screenshot_001", Kind: "failure_screenshot", URI: "file:///tmp/failure-step-001.png", SHA256: "failure_hash", Sensitive: true, SourceNodeID: "node_open_dashboard"}},
		VerificationReport: model.VerificationReport{PassRate: 0, FailedNodeIDs: []string{"node_open_dashboard"}, ReproducibilityMatch: true},
		FailureDiagnostic:  diagnostic,
		RepairRequest: &model.ScriptRepairRequest{
			ID:               "repair_failed_1",
			SourceResultID:   "result_failed_1",
			SourcePackageID:  pkg.PackageID,
			CloudJobID:       "job_1",
			RepairAttempt:    1,
			ApprovalRequired: true,
			RequestedAt:      now,
			ExpiresAt:        now.Add(24 * time.Hour),
		},
		AuditTrail: model.CloudExecutionAuditTrail{StartedAt: now.Add(-time.Second), CompletedAt: now},
		Delivery: model.ResultDelivery{
			ResultPackageRef: model.PackageArtifactDescriptor{ID: "result_failed_artifact", Kind: "recording_result_package", URI: "cascade://recording-results/result_failed_1", Encrypted: true},
			AckRequired:      true,
			ExpiresAt:        now.Add(24 * time.Hour),
		},
		CreatedAt: now,
	}
}

func sampleEnvelopeForAppTest(t *testing.T, pkg model.ClientExecutionPackage, now time.Time) model.ExchangeEnvelope {
	t.Helper()
	digest, err := model.DigestCanonicalJSON(pkg)
	if err != nil {
		t.Fatal(err)
	}
	return model.ExchangeEnvelope{
		EnvelopeID:           "env_1",
		OrgID:                pkg.OrgID,
		ProjectID:            pkg.ProjectID,
		PackageKind:          model.ExchangePackageKindClientExecution,
		SchemaVersion:        model.ExchangeEnvelopeSchemaVersion,
		PayloadSchemaVersion: model.ClientExecutionPackageSchemaVersion,
		IdempotencyKey:       "idem_1",
		CreatedAt:            now,
		ExpiresAt:            now.Add(time.Hour),
		Producer:             model.ExchangeProducer{AppVersion: "test", RuntimeProfile: "cloud"},
		Crypto: model.ExchangeCrypto{
			CryptoSuite:            model.CryptoSuiteXChaCha20Poly1305,
			KeyWrappingMode:        model.KeyWrappingModeServerKMS,
			ServerKeyID:            "server_key_1",
			ContentEncryptionAlg:   model.CryptoSuiteXChaCha20Poly1305,
			KeyEncryptionAlg:       "x25519-hkdf-sha256",
			CompressionAlg:         model.CompressionGzip,
			PayloadDigestAlg:       "sha256",
			PayloadDigestSHA256:    digest,
			CiphertextDigestSHA256: "sha_ciphertext",
			SignatureAlg:           "ed25519",
			SignatureKeyID:         "client_key_1",
			Signature:              "signature",
			Nonce:                  "nonce_1",
			EncryptedContentKey:    "encrypted_content_key",
		},
		PayloadRef: model.EncryptedPayloadRef{Kind: "inline", InlineCiphertext: "ciphertext", SHA256: "sha_ciphertext", SizeBytes: 10, Encrypted: true, Sensitive: true, CompressionAlg: model.CompressionGzip},
		Policy: model.ExchangePackagePolicy{
			ReplayProtection:      true,
			DeletePayloadAfterRun: true,
			HumanApprovalRequired: true,
			StructureSummaryOnly:  true,
		},
	}
}

func sampleRecordingResultForAppTest(pkg model.ClientExecutionPackage) model.RecordingResultPackage {
	now := time.Date(2026, 7, 9, 16, 10, 0, 0, time.UTC)
	artifact := model.ArtifactRef{ID: "artifact_raw_recording", Kind: "raw_recording", URI: "file:///tmp/recording.webm", MimeType: "video/webm", SHA256: "sha_raw_recording", SizeBytes: 1024, SourceNodeID: "node_open_dashboard"}
	demoVideo := model.ArtifactRef{ID: "artifact_demo_video", Kind: "demo_video", URI: "file:///tmp/demo.webm", MimeType: "video/webm", SHA256: "sha_demo_video", SizeBytes: 2048, Metadata: map[string]any{"asset_role": "final_demo", "include_in_demo": true}}
	step := model.StepResult{NodeID: "node_open_dashboard", Status: "passed", DurationMS: 1200}
	return model.RecordingResultPackage{
		ResultID:        "result_1",
		SourcePackageID: pkg.PackageID,
		CloudJobID:      "job_1",
		SchemaVersion:   model.RecordingResultPackageSchemaVersion,
		Status:          model.RecordingResultStatusGenerated,
		ExecutionTrace: &model.ExecutionTrace{
			ID:              "trace_1",
			WorkflowGraphID: pkg.WorkflowGraph.ID,
			GraphVersion:    pkg.WorkflowGraph.Version,
			PassRate:        1,
			StepResults:     []model.StepResult{step},
			Artifacts:       []model.ArtifactRef{artifact},
		},
		StepResults:     []model.StepResult{step},
		GeneratedAssets: []model.ArtifactRef{artifact, demoVideo},
		VerificationReport: model.VerificationReport{
			PassRate:             1,
			ReproducibilityMatch: true,
		},
		AuditTrail: model.CloudExecutionAuditTrail{CompletedAt: now},
		Delivery: model.ResultDelivery{
			ResultPackageRef: model.PackageArtifactDescriptor{
				ID:        "result_package_artifact",
				Role:      "recording_result",
				Kind:      "recording_result_package",
				URI:       "cascade://recording-results/result_1.enc",
				SHA256:    "sha_result",
				Encrypted: true,
				Sensitive: true,
			},
			RecipientKind:  model.ResultRecipientAppInstallation,
			RecipientKeyID: "install_result_key_1",
			EncryptionAlg:  model.CryptoSuiteXChaCha20Poly1305,
			AckRequired:    true,
			ExpiresAt:      now.Add(24 * time.Hour),
		},
		CreatedAt: now,
	}
}

func TestSummarizeRecordingResultExposesOutlineValidationEvidenceCompleteness(t *testing.T) {
	result := model.RecordingResultPackage{
		ResultID:         "result_outline_summary",
		Status:           model.RecordingResultStatusGenerated,
		ExecutionRuntime: model.ExecutableScriptRuntimeBrowserAgentOutlineV1,
		StepResults: []model.StepResult{
			{NodeID: "node_1", Status: "passed", ObservedState: "source=browser_assertion; assertion:project_ready=passed"},
			{NodeID: "node_2", Status: "passed", ObservedState: "source=actual_browser_observation; assertion:build_complete=passed"},
		},
		StageEventLogRef: &model.ArtifactRef{ID: "artifact_stage_events", Kind: "stage_event_log", URI: "file:///tmp/events.jsonl"},
		ValidationReports: []model.ValidationReport{
			{Phase: model.ValidationPhasePreExecution, Decision: model.ValidationDecisionContinue},
			{Phase: model.ValidationPhaseRuntimeStage, Decision: model.ValidationDecisionContinue, NodeID: "node_1"},
			{Phase: model.ValidationPhaseRuntimeStage, Decision: model.ValidationDecisionContinue, NodeID: "node_2"},
			{Phase: model.ValidationPhasePostExecution, Decision: model.ValidationDecisionContinue},
		},
	}

	summary := summarizeRecordingResult(result)
	if summary.Validation == nil {
		t.Fatal("outline result must expose a validation summary")
	}
	validation := summary.Validation
	if validation.Status != "complete" || validation.ValidationReportCount != 4 || validation.PreExecutionReportCount != 1 || validation.RuntimeStageReportCount != 2 || validation.PostExecutionReportCount != 1 || validation.RealObservedStepCount != 2 || !validation.StageEventLogAvailable || validation.LatestDecision != model.ValidationDecisionContinue {
		t.Fatalf("unexpected outline validation summary: %+v", validation)
	}

	result.StepResults[1].ObservedState = "source=derived_from_plan; assertion:build_complete=passed"
	if got := summarizeRecordingResult(result).Validation; got.Status != "incomplete" || got.RealObservedStepCount != 1 {
		t.Fatalf("plan-derived state must not be counted as real evidence: %+v", got)
	}
}

func fixedClock(now time.Time) func() time.Time {
	return func() time.Time { return now }
}

func findDeliverable(deliverables []model.ExecutionDeliverable, kind string) *model.ExecutionDeliverable {
	for index := range deliverables {
		if deliverables[index].Kind == kind {
			return &deliverables[index]
		}
	}
	return nil
}
