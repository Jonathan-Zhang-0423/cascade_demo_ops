package app

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/credentialstore"
	"cascade-demoops/backend/internal/directtransport"
	"cascade-demoops/backend/internal/model"
	"cascade-demoops/backend/internal/orchestrator"
	"cascade-demoops/backend/internal/store"
)

func TestAppDirectTransportApprovesUploadsAndDownloadsThroughDedicatedPort(t *testing.T) {
	root := t.TempDir()
	port := reserveDirectAppTestPort(t)
	gateway, err := directtransport.NewGateway(directtransport.Config{
		ControlAddr: "127.0.0.1:0", WorkerAddr: "127.0.0.1:0", DataBindHost: "127.0.0.1", AdvertisedHost: "127.0.0.1",
		DataPortStart: port, DataPortEnd: port, AllowInsecureLoopback: true,
		BootstrapToken: directAppBootstrapToken, WorkerToken: directAppWorkerToken,
		SpoolRoot: filepath.Join(root, "gateway"), LeaseTTL: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	control := httptest.NewServer(gateway.ControlHandler())
	t.Cleanup(func() { control.Close(); gateway.Close(context.Background()) })

	states := store.NewMemoryStateStore()
	service, err := NewService(config.AppRuntimeConfig{Profile: config.ProfileDev, Environment: "test", Mode: model.AppModeDesktop, DatabaseDialect: config.DatabaseSQLite, SQLitePath: filepath.Join(root, "app.db"), DataRoot: filepath.Join(root, "app"), ArtifactRoot: filepath.Join(root, "artifacts"), CacheRoot: filepath.Join(root, "cache"), LogRoot: filepath.Join(root, "logs"), LLMMode: config.LLMModeDeterministic}, states)
	if err != nil {
		t.Fatal(err)
	}
	vault := newDirectMemoryVault()
	service.storeDirectToken = vault.storeToken
	service.readDirectToken = vault.readToken
	service.deleteDirectToken = vault.deleteToken
	service.storeDirectIdentity = vault.storeIdentity
	service.readDirectIdentity = vault.readIdentity
	service.storeDirectLease = vault.storeLease
	service.readDirectLease = vault.readLease
	service.deleteDirectLease = vault.deleteLease
	view, err := service.SaveDirectTransportSettings(t.Context(), DirectTransportSettingsRequest{ControlURL: control.URL, AccessToken: directAppBootstrapToken})
	if err != nil {
		t.Fatal(err)
	}
	if !view.Configured || !view.Reachable || !view.TokenConfigured || view.ProtocolVersion != model.DirectTransportProtocolVersion {
		t.Fatalf("direct status is not ready: %+v", view)
	}

	state, err := service.CreateProject(t.Context(), orchestrator.UserInput{ProjectID: "direct-tetris", Mode: model.AppModeDesktop, ProductURL: "https://cascadeai.cn/app", ProductDescription: "进入新建项目，填写项目名俄罗斯方块，启动 Agent 构建并观察实际进度。", TargetAudience: "普通用户", MustShow: []string{"进入新建项目", "填写俄罗斯方块", "启动 Agent 构建", "观察构建进度"}, MustNotShow: []string{"密码", "令牌"}, ForbiddenPages: []string{"/billing"}, ForbiddenData: []string{"密码", "令牌"}, AllowedDomains: []string{"cascadeai.cn"}})
	if err != nil {
		t.Fatal(err)
	}
	build, err := service.BuildClientExecutionPackage(t.Context(), state.ProjectID, defaultDesktopOrgID)
	if err != nil {
		t.Fatal(err)
	}
	if build.Package.ConfidenceSummary == nil || build.Package.ConfidenceSummary.Readiness == model.PackageReadinessBlocked {
		t.Fatalf("draft is blocked: %+v", build.Package.ConfidenceSummary)
	}
	upload, err := service.UploadDirectExecutionPackage(t.Context(), state.ProjectID, DirectTransportUploadRequest{OrgID: defaultDesktopOrgID, PackageDigestSHA256: build.PackageDigestSHA256, ApprovalSubjectDigestSHA256: build.ApprovalSubjectDigestSHA256, ConfidenceAssessmentHash: build.Package.ConfidenceSummary.AssessmentHash, RiskConfirmed: true, IdempotencyKey: "direct-tetris-approval"})
	if err != nil {
		t.Fatal(err)
	}
	if upload.Build.BuildStatus != "approved" || upload.Receipt.JobID == "" || upload.Lease.DataPort != port {
		t.Fatalf("unexpected direct upload: %+v", upload)
	}

	claimed := claimDirectWorkerJob(t, gateway)
	if claimed.JobID != upload.Receipt.JobID || claimed.Package.PackageID != upload.Build.Package.PackageID {
		t.Fatalf("worker claimed wrong package: %+v", claimed)
	}
	artifactBytes := bytes.Repeat([]byte("Browser Agent real worker bytes;"), model.DirectArtifactChunkBytes/16+1)
	artifacts := uploadDirectAppFormalArtifacts(t, gateway, claimed, artifactBytes)
	result := directAppTestResult(t, claimed, artifacts)
	putDirectWorkerJSON(t, gateway.WorkerHandler(), http.MethodPut, "/v1/worker/jobs/"+claimed.JobID+"/result", result, http.StatusOK)

	status, err := service.GetDirectExecutionStatus(t.Context(), state.ProjectID, claimed.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != "completed" || status.ResultPackageID != result.ResultID || len(status.Artifacts) != len(artifacts) {
		t.Fatalf("unexpected decrypted status: %+v", status)
	}
	returned, err := service.GetDirectResult(t.Context(), state.ProjectID, claimed.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if returned.ResultID != result.ResultID || returned.CloudJobID != claimed.JobID {
		t.Fatalf("unexpected decrypted result: %+v", returned)
	}
	for _, artifact := range status.Artifacts {
		download, err := service.DownloadDirectArtifact(t.Context(), state.ProjectID, DirectArtifactDownloadRequest{JobID: claimed.JobID, Artifact: artifact})
		if err != nil {
			t.Fatal(err)
		}
		if !download.ChecksumVerified || download.SHA256 != artifact.SHA256 {
			t.Fatalf("artifact download was not verified: %+v", download)
		}
	}
	ack, err := service.AcknowledgeDirectResult(t.Context(), state.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if !ack.VerifiedChecksums || ack.JobID != claimed.JobID || ack.ResultPackageID != result.ResultID || len(ack.ReceivedArtifactIDs) != len(artifacts) {
		t.Fatalf("direct result ACK was not bound to every verified artifact: %+v", ack)
	}
	review, err := service.ReviewDirectResult(t.Context(), state.ProjectID, DirectResultReviewRequest{
		JobID: claimed.JobID, ResultPackageID: result.ResultID,
		Review: model.ResultReviewRequest{IdempotencyKey: "review-direct-tetris", Decision: model.ResultReviewApproved, Summary: "verified Browser Agent output approved"},
	})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := service.readExistingDirectInstallationIdentity()
	if err != nil {
		t.Fatal(err)
	}
	publicKey, err := base64.StdEncoding.DecodeString(identity.PublicKeyBase64)
	if err != nil {
		t.Fatal(err)
	}
	wantReviewerID := model.DirectInstallationID(ed25519.PublicKey(publicKey))
	if review.ResultPackageID != result.ResultID || review.Decision != model.ResultReviewApproved || review.ReviewerInstallID != wantReviewerID {
		t.Fatalf("direct review was not bound to the App installation and result: %+v", review)
	}
	repeated, err := service.ReviewDirectResult(t.Context(), state.ProjectID, DirectResultReviewRequest{JobID: claimed.JobID, ResultPackageID: result.ResultID, Review: model.ResultReviewRequest{IdempotencyKey: "review-direct-tetris", Decision: model.ResultReviewApproved, Summary: "verified Browser Agent output approved"}})
	if err != nil || repeated.ReviewID != review.ReviewID || !repeated.ReviewedAt.Equal(review.ReviewedAt) {
		t.Fatalf("direct review idempotency did not preserve the original record: first=%+v repeated=%+v err=%v", review, repeated, err)
	}
	released, err := service.ReleaseDirectTransportLease(t.Context(), state.ProjectID)
	if err != nil || !released.Released {
		t.Fatalf("direct lease was not released after verified terminal result: %+v err=%v", released, err)
	}

	persisted, err := states.Load(t.Context(), state.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	serialized, _ := json.Marshal(persisted)
	serializedText := string(serialized)
	if strings.Contains(serializedText, directAppBootstrapToken) {
		t.Fatal("project state leaked direct transport token")
	}
	if persisted.DesktopCloudRun == nil || persisted.DesktopCloudRun.Transport != directTransportStateName || persisted.DesktopCloudRun.DataPort != 0 || persisted.DesktopCloudRun.LeaseID != "" || persisted.DesktopCloudRun.LeaseExpiresAt != nil {
		t.Fatalf("direct state was not persisted safely: %+v", persisted.DesktopCloudRun)
	}
	if !persisted.DesktopCloudRun.ResultDownloaded || persisted.DesktopCloudRun.AckedAt == nil || persisted.DesktopCloudRun.ResultReview == nil || persisted.DesktopCloudRun.ResultReview.Decision != string(model.ResultReviewApproved) {
		t.Fatalf("verified direct download and local review were not persisted: %+v", persisted.DesktopCloudRun)
	}
}

func TestAppDirectTransportRejectsStaleApprovalBeforeLeaseAllocation(t *testing.T) {
	service, err := NewService(config.AppRuntimeConfig{DataRoot: t.TempDir(), ArtifactRoot: t.TempDir(), LLMMode: config.LLMModeDeterministic}, store.NewMemoryStateStore())
	if err != nil {
		t.Fatal(err)
	}
	called := false
	service.readDirectToken = func() (string, error) { called = true; return directAppBootstrapToken, nil }
	_, err = service.UploadDirectExecutionPackage(t.Context(), "missing", DirectTransportUploadRequest{ApprovalSubjectDigestSHA256: "stale", ConfidenceAssessmentHash: "stale", RiskConfirmed: true, IdempotencyKey: "stale"})
	if err == nil {
		t.Fatal("stale/missing package was accepted")
	}
	if called {
		t.Fatal("transport token was read before authoritative package approval")
	}
}

func TestDirectReviewAndEditorHandoffRequirePersistedServerACK(t *testing.T) {
	states := store.NewMemoryStateStore()
	service, err := NewService(config.AppRuntimeConfig{DataRoot: t.TempDir(), ArtifactRoot: t.TempDir(), LLMMode: config.LLMModeDeterministic}, states)
	if err != nil {
		t.Fatal(err)
	}
	projectID := "direct-ack-gate"
	if err := states.Save(t.Context(), &orchestrator.CascadeState{
		ProjectID: projectID,
		DesktopCloudRun: &orchestrator.DesktopCloudRunState{
			SchemaVersion: desktopCloudRunSchemaVersion, Transport: directTransportStateName,
			CloudJobID: "job_ack_gate", ResultPackageID: "result_ack_gate", Status: "completed", ResultDownloaded: true,
			ResultPackage: &model.RecordingResultPackage{ResultID: "result_ack_gate"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	_, err = service.ReviewDirectResult(t.Context(), projectID, DirectResultReviewRequest{
		JobID: "job_ack_gate", ResultPackageID: "result_ack_gate",
		Review: model.ResultReviewRequest{IdempotencyKey: "review-before-ack", Decision: model.ResultReviewApproved},
	})
	if err == nil || !strings.Contains(err.Error(), "server ACK") {
		t.Fatalf("review bypassed the ACK gate: %v", err)
	}
	materialization, err := service.GetDirectEditorMaterialization(t.Context(), projectID)
	if err != nil || materialization.Ready || !strings.Contains(materialization.Message, "ACK") {
		t.Fatalf("editor handoff bypassed the ACK gate: %+v err=%v", materialization, err)
	}
}

func TestAppDirectTransportRejectsMissingWorkerAndVerifierVersionNegotiation(t *testing.T) {
	health := model.DirectHealthResponse{
		ProtocolVersion:                model.DirectTransportProtocolVersion,
		CryptoSuite:                    model.DirectTransportCryptoSuite,
		SupportedProtocolVersions:      []string{model.DirectTransportProtocolVersion},
		SupportedPackageSchemaVersions: []string{model.ClientExecutionPackageSchemaVersion},
		SupportedRuntimes:              []string{model.ExecutableScriptRuntimeBrowserAgentOutlineV1},
	}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/direct/health" {
			http.NotFound(response, request)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(response).Encode(health)
	}))
	t.Cleanup(server.Close)
	service, err := NewService(config.AppRuntimeConfig{DataRoot: t.TempDir(), ArtifactRoot: t.TempDir(), LLMMode: config.LLMModeDeterministic}, store.NewMemoryStateStore())
	if err != nil {
		t.Fatal(err)
	}
	service.directTransportURL = server.URL
	service.readDirectToken = func() (string, error) { return directAppBootstrapToken, nil }

	view, err := service.DirectTransportStatus(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if view.Reachable || view.ErrorClass != "protocol_mismatch" {
		t.Fatalf("missing worker/verifier negotiation must fail closed: %+v", view)
	}
}

func TestPersistDirectStatusRetainsStableGatewayGuidance(t *testing.T) {
	states := store.NewMemoryStateStore()
	service, err := NewService(config.AppRuntimeConfig{DataRoot: t.TempDir(), ArtifactRoot: t.TempDir(), LLMMode: config.LLMModeDeterministic}, states)
	if err != nil {
		t.Fatal(err)
	}
	if err := states.Save(t.Context(), &orchestrator.CascadeState{ProjectID: "direct_guidance"}); err != nil {
		t.Fatal(err)
	}
	status := model.DirectJobStatus{
		JobID: "job_guidance", PackageID: "pkg_guidance", Status: "awaiting_manual_login", Stage: "login_checkpoint",
		WaitingReason: "manual_login_required", BlockingErrorCode: "login_checkpoint_pending", NextAction: "complete_manual_login", RequiresReapproval: true,
	}
	if err := service.persistDirectStatus(t.Context(), "direct_guidance", status); err != nil {
		t.Fatal(err)
	}
	persisted, err := states.Load(t.Context(), "direct_guidance")
	if err != nil {
		t.Fatal(err)
	}
	run := persisted.DesktopCloudRun
	if run == nil || run.WaitingReason != status.WaitingReason || run.BlockingErrorCode != status.BlockingErrorCode || run.NextAction != status.NextAction || !run.RequiresReapproval {
		t.Fatalf("stable Gateway guidance was lost during persistence: %+v", run)
	}
}

func TestPersistDirectReunderstandingStatusInvalidatesApprovalAndRotatesPackageIdentity(t *testing.T) {
	states := store.NewMemoryStateStore()
	service, err := NewService(config.AppRuntimeConfig{DataRoot: t.TempDir(), ArtifactRoot: t.TempDir(), LLMMode: config.LLMModeDeterministic}, states)
	if err != nil {
		t.Fatal(err)
	}
	state, err := service.CreateProject(t.Context(), orchestrator.UserInput{
		ProjectID: "direct-reunderstanding", Mode: model.AppModeDesktop,
		ProductURL: "https://cascadeai.cn/app", ProductDescription: "进入新建项目，填写俄罗斯方块并启动 Agent 构建。",
		TargetAudience: "普通用户", MustShow: []string{"新建俄罗斯方块", "启动 Agent 构建"},
		AllowedDomains: []string{"cascadeai.cn"},
	})
	if err != nil {
		t.Fatal(err)
	}
	original, err := service.BuildClientExecutionPackage(t.Context(), state.ProjectID, defaultDesktopOrgID)
	if err != nil {
		t.Fatal(err)
	}
	service.approvedBuilds[state.ProjectID+"|old-approval"] = approvedBuildCacheEntry{Build: original}
	status := model.DirectJobStatus{
		JobID: "job_old", PackageID: original.Package.PackageID, Status: "failed", Stage: "failed",
		ResultPackageID:   "result_old",
		BlockingErrorCode: "reunderstanding_required", NextAction: "regenerate_package_from_structured_issues", RequiresReapproval: true,
		ReunderstandingIssues: []model.DirectReunderstandingIssue{{Code: "STAGE_VALIDATION_FAILURE_THRESHOLD", StageID: "stage_build", Severity: model.FindingSeverityBlocking, Required: true}},
	}
	if err := service.persistDirectStatus(t.Context(), state.ProjectID, status); err != nil {
		t.Fatal(err)
	}
	persisted, err := states.Load(t.Context(), state.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Approved || persisted.CurrentNode != orchestrator.NodeHumanApprove || persisted.Status != orchestrator.FlowStatusAwaitingHuman || persisted.ExecutionPackageGeneration != 1 {
		t.Fatalf("reunderstanding did not invalidate the App approval lifecycle: %+v", persisted)
	}
	if persisted.DesktopCloudRun == nil || len(persisted.DesktopCloudRun.ReunderstandingIssues) != 1 {
		t.Fatalf("structured reunderstanding issues were not restart-safe: %+v", persisted.DesktopCloudRun)
	}
	if len(service.approvedBuilds) != 0 {
		t.Fatalf("old digest-bound approval cache survived reunderstanding: %+v", service.approvedBuilds)
	}
	regenerated, err := service.BuildClientExecutionPackage(t.Context(), state.ProjectID, defaultDesktopOrgID)
	if err != nil {
		t.Fatal(err)
	}
	if regenerated.Package.PackageID == original.Package.PackageID || regenerated.PackageDigestSHA256 == original.PackageDigestSHA256 {
		t.Fatalf("reunderstanding reused invalidated package identity: old=%s/%s new=%s/%s", original.Package.PackageID, original.PackageDigestSHA256, regenerated.Package.PackageID, regenerated.PackageDigestSHA256)
	}
	if err := service.persistDirectStatus(t.Context(), state.ProjectID, status); err != nil {
		t.Fatal(err)
	}
	repeated, err := states.Load(t.Context(), state.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if repeated.ExecutionPackageGeneration != 1 {
		t.Fatalf("idempotent status polling rotated package identity repeatedly: generation=%d", repeated.ExecutionPackageGeneration)
	}
	if err := service.persistDirectUpload(t.Context(), state.ProjectID, regenerated,
		model.DirectPortLease{LeaseID: "lease_new", DataPort: 24001, ExpiresAt: time.Now().UTC().Add(time.Hour)},
		model.DirectPackageReceipt{PackageID: regenerated.Package.PackageID, JobID: "job_new", Status: "queued", Stage: "server_intake"}); err != nil {
		t.Fatal(err)
	}
	newRunState, err := states.Load(t.Context(), state.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	newRun := newRunState.DesktopCloudRun
	if newRun == nil || newRun.BlockingErrorCode != "" || newRun.RequiresReapproval || len(newRun.ReunderstandingIssues) != 0 || newRun.ResultPackageID != "" {
		t.Fatalf("new package upload retained terminal state from the invalidated job: %+v", newRun)
	}
	if newRun.PackageDigestSHA256 != regenerated.PackageDigestSHA256 || newRun.GraphDigestSHA256 != regenerated.Package.Reproducibility.GraphHashSHA256 || newRun.BundleHashSHA256 == "" || newRun.PlanHashSHA256 == "" {
		t.Fatalf("direct upload did not persist authoritative package lineage: %+v", newRun)
	}
}

func TestApproveClientExecutionPackageRebindsConfidenceAfterCredentialGrantExpiry(t *testing.T) {
	product := newAuthenticatedWorkspaceTestServer(t)
	root := t.TempDir()
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(config.AppRuntimeConfig{
		Profile: config.ProfileDev, Environment: "test", Mode: model.AppModeDesktop,
		DatabaseDialect: config.DatabaseSQLite, SQLitePath: filepath.Join(root, "app.db"),
		DataRoot: root, ArtifactRoot: filepath.Join(root, "artifacts"),
		CacheRoot: filepath.Join(root, "cache"), LogRoot: filepath.Join(root, "logs"),
		LLMMode: config.LLMModeDeterministic, DevRepoRoot: repoRoot,
	}, store.NewMemoryStateStore())
	if err != nil {
		t.Fatal(err)
	}
	service.readDemoCredential = func(ref string) (credentialstore.DemoCredential, error) {
		if ref != "direct-confidence" {
			return credentialstore.DemoCredential{}, errors.New("credential not found")
		}
		return credentialstore.DemoCredential{Username: "vault-user@example.test", Password: "vault-password"}, nil
	}
	state, err := service.CreateProject(t.Context(), orchestrator.UserInput{
		ProjectID: "direct-credential-confidence", Mode: model.AppModeDesktop,
		ProductURL: product.URL + "/login", ProductDescription: "登录后进入新建项目，填写俄罗斯方块并启动 Agent 构建。",
		TargetAudience: "普通用户", MustShow: []string{"登录", "新建俄罗斯方块", "启动 Agent 构建"},
		AllowedDomains: []string{"127.0.0.1"},
		DemoUsername:   "vault-user@example.test", DemoPassword: "vault-password", DemoCredentialRef: "credential://demo/direct-confidence",
	})
	if err != nil {
		t.Fatal(err)
	}
	draft, err := service.BuildClientExecutionPackage(t.Context(), state.ProjectID, defaultDesktopOrgID)
	if err != nil {
		t.Fatal(err)
	}
	if len(draft.Package.CredentialGrants) != 1 || draft.Package.ConfidenceSummary == nil {
		t.Fatalf("credential-bound draft is incomplete: grants=%d confidence=%+v", len(draft.Package.CredentialGrants), draft.Package.ConfidenceSummary)
	}
	approved, err := service.ApproveClientExecutionPackage(t.Context(), state.ProjectID, defaultDesktopOrgID, CloudUploadInitRequest{
		PackageDigestSHA256: draft.PackageDigestSHA256, ApprovalSubjectDigestSHA256: draft.ApprovalSubjectDigestSHA256,
		ConfidenceAssessmentHash: draft.Package.ConfidenceSummary.AssessmentHash, RiskConfirmed: true,
		IdempotencyKey: "direct-credential-confidence-approval",
	})
	if err != nil {
		t.Fatal(err)
	}
	if approved.Package.CredentialGrants[0].ExpiresAt.IsZero() {
		t.Fatal("approved credential grant did not receive a short-lived expiry")
	}
	if err := model.ValidatePackageConfidenceSummary(&approved.Package); err != nil {
		t.Fatalf("approved package retained a stale draft confidence assessment: %v", err)
	}
	actualApprovalDigest, err := model.ComputePackageApprovalSubjectDigest(approved.Package)
	if err != nil {
		t.Fatal(err)
	}
	if approved.ApprovalSubjectDigestSHA256 != actualApprovalDigest || approved.Package.SafetyReport.HumanApproval.ApprovalSubjectDigestSHA256 != actualApprovalDigest {
		t.Fatalf("approved package did not freeze its normalized upload subject: build=%s record=%s actual=%s", approved.ApprovalSubjectDigestSHA256, approved.Package.SafetyReport.HumanApproval.ApprovalSubjectDigestSHA256, actualApprovalDigest)
	}
	repeated, err := service.ApproveClientExecutionPackage(t.Context(), state.ProjectID, defaultDesktopOrgID, CloudUploadInitRequest{
		PackageDigestSHA256: draft.PackageDigestSHA256, ApprovalSubjectDigestSHA256: draft.ApprovalSubjectDigestSHA256,
		ConfidenceAssessmentHash: draft.Package.ConfidenceSummary.AssessmentHash, RiskConfirmed: true,
		IdempotencyKey: "direct-credential-confidence-approval",
	})
	if err != nil || repeated.PackageDigestSHA256 != approved.PackageDigestSHA256 || repeated.ApprovalSubjectDigestSHA256 != approved.ApprovalSubjectDigestSHA256 {
		t.Fatalf("idempotent approval did not return the same normalized package: repeated=%+v err=%v", repeated, err)
	}
}

func TestBrowserAgentOutlineAllowsEvidenceBoundInteractionRoute(t *testing.T) {
	product := newAuthenticatedWorkspaceTestServer(t)
	root := t.TempDir()
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(config.AppRuntimeConfig{
		Profile: config.ProfileDev, Environment: "test", Mode: model.AppModeDesktop,
		DatabaseDialect: config.DatabaseSQLite, SQLitePath: filepath.Join(root, "app.db"),
		DataRoot: root, ArtifactRoot: filepath.Join(root, "artifacts"), CacheRoot: filepath.Join(root, "cache"),
		LogRoot: filepath.Join(root, "logs"), LLMMode: config.LLMModeDeterministic, DevRepoRoot: repoRoot,
	}, store.NewMemoryStateStore())
	if err != nil {
		t.Fatal(err)
	}
	service.readDemoCredential = func(ref string) (credentialstore.DemoCredential, error) {
		if ref != "direct-root-route" {
			return credentialstore.DemoCredential{}, errors.New("credential not found")
		}
		return credentialstore.DemoCredential{Username: "vault-user@example.test", Password: "vault-password"}, nil
	}
	state, err := service.CreateProject(t.Context(), orchestrator.UserInput{
		ProjectID: "direct-login-root-route", Mode: model.AppModeDesktop,
		ProductURL: product.URL, ProductDescription: "进入新建项目，填写俄罗斯方块并启动 Agent 构建。",
		TargetAudience: "普通用户", MustShow: []string{"新建俄罗斯方块", "启动 Agent 构建"},
		AllowedDomains: []string{"127.0.0.1"},
		DemoUsername:   "vault-user@example.test", DemoPassword: "vault-password", DemoCredentialRef: "credential://demo/direct-root-route",
	})
	if err != nil {
		t.Fatal(err)
	}
	build, err := service.BuildClientExecutionPackage(t.Context(), state.ProjectID, defaultDesktopOrgID)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := newBrowserAgentStageOrchestrator(contractBrowserAgentPolicyGuard{}).Prepare(&build.Package)
	if err != nil {
		t.Fatalf("evidence-bound root interaction was rejected by its own exploration scope: %v", err)
	}
	found := false
	for _, route := range plan.ExplorationScope.AllowedRoutes {
		if normalizeBrowserAgentRoute(route) == "/" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("evidence-bound interaction route was omitted: %+v", plan.ExplorationScope.AllowedRoutes)
	}
}

func TestBrowserAgentOutlineAllowsInitialBaseURLRoute(t *testing.T) {
	root := t.TempDir()
	service, err := NewService(config.AppRuntimeConfig{Profile: config.ProfileDev, Environment: "test", Mode: model.AppModeDesktop, DataRoot: root, ArtifactRoot: filepath.Join(root, "artifacts"), CacheRoot: filepath.Join(root, "cache"), LogRoot: filepath.Join(root, "logs"), LLMMode: config.LLMModeDeterministic}, store.NewMemoryStateStore())
	if err != nil {
		t.Fatal(err)
	}
	state, err := service.CreateProject(t.Context(), orchestrator.UserInput{ProjectID: "direct-base-route", Mode: model.AppModeDesktop, ProductURL: "https://cascadeai.cn/app", ProductDescription: "进入新建项目并启动 Agent 构建。", TargetAudience: "普通用户", MustShow: []string{"新建项目", "启动 Agent 构建"}, AllowedDomains: []string{"cascadeai.cn"}})
	if err != nil {
		t.Fatal(err)
	}
	build, err := service.BuildClientExecutionPackage(t.Context(), state.ProjectID, defaultDesktopOrgID)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := newBrowserAgentStageOrchestrator(contractBrowserAgentPolicyGuard{}).Prepare(&build.Package)
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range plan.ExplorationScope.AllowedRoutes {
		if normalizeBrowserAgentRoute(route) == "/" {
			return
		}
	}
	t.Fatalf("package base URL route was omitted: %+v", plan.ExplorationScope.AllowedRoutes)
}

func TestAppDirectTransportReleasesLeaseWhenPackageUploadFails(t *testing.T) {
	root := t.TempDir()
	port := reserveDirectAppTestPort(t)
	unreachablePort := reserveDirectAppTestPort(t)
	gateway, err := directtransport.NewGateway(directtransport.Config{
		ControlAddr: "127.0.0.1:0", WorkerAddr: "127.0.0.1:0", DataBindHost: "127.0.0.1", AdvertisedHost: "127.0.0.1",
		DataPortStart: port, DataPortEnd: port, AllowInsecureLoopback: true,
		BootstrapToken: directAppBootstrapToken, WorkerToken: directAppWorkerToken,
		SpoolRoot: filepath.Join(root, "gateway"), LeaseTTL: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	var releaseCount int
	var releaseMu sync.Mutex
	controlHandler := gateway.ControlHandler()
	control := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/v1/direct/leases" {
			proxied := httptest.NewRecorder()
			controlHandler.ServeHTTP(proxied, request)
			if proxied.Code == http.StatusCreated {
				var lease model.DirectPortLease
				if err := json.Unmarshal(proxied.Body.Bytes(), &lease); err != nil {
					t.Fatalf("decode lease response: %v", err)
				}
				lease.DataPort = unreachablePort
				lease.DataURL = "http://127.0.0.1:" + strconv.Itoa(unreachablePort)
				response.Header().Set("Content-Type", "application/json")
				response.WriteHeader(http.StatusCreated)
				_, _ = response.Write(mustMarshalDirectTest(t, lease))
				return
			}
		}
		if request.URL.Path == "/v1/direct/leases/release" {
			releaseMu.Lock()
			releaseCount++
			releaseMu.Unlock()
		}
		controlHandler.ServeHTTP(response, request)
	}))
	t.Cleanup(func() { control.Close(); gateway.Close(context.Background()) })

	states := store.NewMemoryStateStore()
	service, err := NewService(config.AppRuntimeConfig{Profile: config.ProfileDev, Environment: "test", Mode: model.AppModeDesktop, DatabaseDialect: config.DatabaseSQLite, SQLitePath: filepath.Join(root, "app.db"), DataRoot: filepath.Join(root, "app"), ArtifactRoot: filepath.Join(root, "artifacts"), CacheRoot: filepath.Join(root, "cache"), LogRoot: filepath.Join(root, "logs"), LLMMode: config.LLMModeDeterministic}, states)
	if err != nil {
		t.Fatal(err)
	}
	vault := newDirectMemoryVault()
	service.storeDirectToken = vault.storeToken
	service.readDirectToken = vault.readToken
	service.deleteDirectToken = vault.deleteToken
	service.storeDirectIdentity = vault.storeIdentity
	service.readDirectIdentity = vault.readIdentity
	service.storeDirectLease = vault.storeLease
	service.readDirectLease = vault.readLease
	service.deleteDirectLease = vault.deleteLease
	if _, err := service.SaveDirectTransportSettings(t.Context(), DirectTransportSettingsRequest{ControlURL: control.URL, AccessToken: directAppBootstrapToken}); err != nil {
		t.Fatal(err)
	}
	state, err := service.CreateProject(t.Context(), orchestrator.UserInput{ProjectID: "direct-upload-failure", Mode: model.AppModeDesktop, ProductURL: "https://cascadeai.cn/app", ProductDescription: "进入新建项目，填写项目名俄罗斯方块，启动 Agent 构建并观察实际进度。", TargetAudience: "普通用户", MustShow: []string{"进入新建项目", "填写俄罗斯方块", "启动 Agent 构建", "观察构建进度"}, MustNotShow: []string{"密码", "令牌"}, ForbiddenPages: []string{"/billing"}, ForbiddenData: []string{"密码", "令牌"}, AllowedDomains: []string{"cascadeai.cn"}})
	if err != nil {
		t.Fatal(err)
	}
	build, err := service.BuildClientExecutionPackage(t.Context(), state.ProjectID, defaultDesktopOrgID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.UploadDirectExecutionPackage(t.Context(), state.ProjectID, DirectTransportUploadRequest{OrgID: defaultDesktopOrgID, PackageDigestSHA256: build.PackageDigestSHA256, ApprovalSubjectDigestSHA256: build.ApprovalSubjectDigestSHA256, ConfidenceAssessmentHash: build.Package.ConfidenceSummary.AssessmentHash, RiskConfirmed: true, IdempotencyKey: "direct-upload-failure"})
	if err == nil {
		t.Fatal("package upload unexpectedly reached the unavailable advertised data host")
	}
	releaseMu.Lock()
	releases := releaseCount
	releaseMu.Unlock()
	if releases != 1 {
		t.Fatalf("failed upload released the remote lease %d times, want 1", releases)
	}
	if _, err := vault.readLease(state.ProjectID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed upload left a local lease in the vault: %v", err)
	}
}

func TestAppDirectTransportDoesNotReleaseSharedInstallationLeaseForActiveProject(t *testing.T) {
	root := t.TempDir()
	states := store.NewMemoryStateStore()
	service, err := NewService(config.AppRuntimeConfig{Profile: config.ProfileDev, Environment: "test", Mode: model.AppModeDesktop, DatabaseDialect: config.DatabaseSQLite, SQLitePath: filepath.Join(root, "app.db"), DataRoot: filepath.Join(root, "app"), ArtifactRoot: filepath.Join(root, "artifacts"), CacheRoot: filepath.Join(root, "cache"), LogRoot: filepath.Join(root, "logs"), LLMMode: config.LLMModeDeterministic}, states)
	if err != nil {
		t.Fatal(err)
	}
	lease := model.DirectPortLease{ProtocolVersion: model.DirectTransportProtocolVersion, LeaseID: "lease_shared", InstallationID: "install_shared", DataPort: 24000, LeaseToken: "shared-token", ExpiresAt: time.Now().Add(time.Hour), CryptoSuite: model.DirectTransportCryptoSuite, DataURL: "http://127.0.0.1:24000"}
	vault := newDirectMemoryVault()
	service.storeDirectLease = vault.storeLease
	service.readDirectLease = vault.readLease
	service.deleteDirectLease = vault.deleteLease
	data, _ := json.Marshal(lease)
	if err := vault.storeLease("project_done", data); err != nil {
		t.Fatal(err)
	}
	completed := &orchestrator.CascadeState{ProjectID: "project_done", DesktopCloudRun: &orchestrator.DesktopCloudRunState{Transport: directTransportStateName, LeaseID: lease.LeaseID, CloudJobID: "job_done", Status: "completed", ResultDownloaded: true}}
	active := &orchestrator.CascadeState{ProjectID: "project_active", DesktopCloudRun: &orchestrator.DesktopCloudRunState{Transport: directTransportStateName, LeaseID: lease.LeaseID, CloudJobID: "job_active", Status: "running"}}
	if err := states.Save(t.Context(), completed); err != nil {
		t.Fatal(err)
	}
	if err := states.Save(t.Context(), active); err != nil {
		t.Fatal(err)
	}
	_, err = service.ReleaseDirectTransportLease(t.Context(), "project_done")
	if err == nil || !strings.Contains(err.Error(), "shared by another active project") {
		t.Fatalf("shared active project did not protect the installation lease: %v", err)
	}
}

func TestAppDirectTransportDoesNotReacquireExplicitlyReleasedLease(t *testing.T) {
	root := t.TempDir()
	states := store.NewMemoryStateStore()
	service, err := NewService(config.AppRuntimeConfig{Profile: config.ProfileDev, Environment: "test", Mode: model.AppModeDesktop, DatabaseDialect: config.DatabaseSQLite, SQLitePath: filepath.Join(root, "app.db"), DataRoot: root, ArtifactRoot: filepath.Join(root, "artifacts"), CacheRoot: filepath.Join(root, "cache"), LogRoot: filepath.Join(root, "logs"), LLMMode: config.LLMModeDeterministic}, states)
	if err != nil {
		t.Fatal(err)
	}
	lease := model.DirectPortLease{ProtocolVersion: model.DirectTransportProtocolVersion, LeaseID: "lease_released", InstallationID: "install_released", DataPort: 24000, LeaseToken: "released-token", ExpiresAt: time.Now().Add(time.Hour), CryptoSuite: model.DirectTransportCryptoSuite, DataURL: "http://127.0.0.1:24000"}
	vault := newDirectMemoryVault()
	service.readDirectLease = vault.readLease
	service.storeDirectLease = vault.storeLease
	data, _ := json.Marshal(lease)
	if err := vault.storeLease("project_released", data); err != nil {
		t.Fatal(err)
	}
	if err := states.Save(t.Context(), &orchestrator.CascadeState{ProjectID: "project_released", DesktopCloudRun: &orchestrator.DesktopCloudRunState{Transport: directTransportStateName, LeaseID: lease.LeaseID, CloudJobID: "job_released", Status: "completed", Stage: "lease_released", ResultDownloaded: true}}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.directLeaseForRequest(t.Context(), "project_released"); err == nil || !strings.Contains(err.Error(), "lease was released") {
		t.Fatalf("released direct lease was unexpectedly eligible for reacquisition: %v", err)
	}
}

func mustMarshalDirectTest(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

const directAppBootstrapToken = "app-bootstrap-token-with-at-least-thirty-two-characters"
const directAppWorkerToken = "app-worker-token-with-at-least-thirty-two-characters"

type directMemoryVault struct {
	mu       sync.Mutex
	token    string
	identity []byte
	leases   map[string][]byte
}

func newDirectMemoryVault() *directMemoryVault {
	return &directMemoryVault{leases: map[string][]byte{}}
}
func (v *directMemoryVault) storeToken(value string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.token = value
	return nil
}
func (v *directMemoryVault) readToken() (string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.token == "" {
		return "", os.ErrNotExist
	}
	return v.token, nil
}
func (v *directMemoryVault) deleteToken() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.token = ""
	return nil
}
func (v *directMemoryVault) storeIdentity(data []byte) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.identity = append([]byte(nil), data...)
	return nil
}
func (v *directMemoryVault) readIdentity() ([]byte, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if len(v.identity) == 0 {
		return nil, os.ErrNotExist
	}
	return append([]byte(nil), v.identity...), nil
}
func (v *directMemoryVault) storeLease(id string, data []byte) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.leases[id] = append([]byte(nil), data...)
	return nil
}
func (v *directMemoryVault) readLease(id string) ([]byte, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	data, ok := v.leases[id]
	if !ok {
		return nil, os.ErrNotExist
	}
	return append([]byte(nil), data...), nil
}
func (v *directMemoryVault) deleteLease(id string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	delete(v.leases, id)
	return nil
}
func (v *directMemoryVault) leaseToken() string {
	v.mu.Lock()
	defer v.mu.Unlock()
	for _, data := range v.leases {
		var lease model.DirectPortLease
		_ = json.Unmarshal(data, &lease)
		return lease.LeaseToken
	}
	return ""
}

func claimDirectWorkerJob(t *testing.T, gateway *directtransport.Gateway) directtransport.WorkerJob {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/v1/worker/jobs/claim", nil)
	request.Header.Set("Authorization", "Bearer "+directAppWorkerToken)
	response := httptest.NewRecorder()
	gateway.WorkerHandler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("claim status %d: %s", response.Code, response.Body.String())
	}
	var job directtransport.WorkerJob
	if err := json.Unmarshal(response.Body.Bytes(), &job); err != nil {
		t.Fatal(err)
	}
	return job
}
func uploadDirectWorkerArtifact(t *testing.T, gateway *directtransport.Gateway, jobID, artifactID, fileName, mimeType, role, kind string, data []byte) model.DirectArtifact {
	t.Helper()
	request := httptest.NewRequest(http.MethodPut, "/v1/worker/jobs/"+jobID+"/artifacts/"+artifactID+"?file_name="+fileName, bytes.NewReader(data))
	request.Header.Set("Authorization", "Bearer "+directAppWorkerToken)
	request.Header.Set("Content-Type", mimeType)
	request.Header.Set("X-Artifact-SHA256", model.SHA256Hex(data))
	request.Header.Set("X-Artifact-Role", role)
	request.Header.Set("X-Artifact-Kind", kind)
	response := httptest.NewRecorder()
	gateway.WorkerHandler().ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("artifact status %d: %s", response.Code, response.Body.String())
	}
	var artifact model.DirectArtifact
	if err := json.Unmarshal(response.Body.Bytes(), &artifact); err != nil {
		t.Fatal(err)
	}
	return artifact
}
func putDirectWorkerJSON(t *testing.T, handler http.Handler, method, path string, value any, want int) {
	t.Helper()
	body, _ := json.Marshal(value)
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+directAppWorkerToken)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != want {
		t.Fatalf("worker request %s status %d: %s", path, response.Code, response.Body.String())
	}
}
func directAppTestResult(t *testing.T, job directtransport.WorkerJob, artifacts []model.DirectArtifact) model.RecordingResultPackage {
	t.Helper()
	now := time.Now().UTC()
	steps := make([]model.StepResult, 0, len(job.Package.ExecutableScriptBundle.PlanJSON.Steps))
	reports := make([]model.ValidationReport, 0, len(job.Package.ExecutableScriptBundle.PlanJSON.Steps))
	stageByNode := map[string]string{}
	for _, stage := range job.Package.ExecutableScriptBundle.StageApprovalPlan.Stages {
		stageByNode[stage.NodeID] = stage.ID
	}
	bundleHash := job.Package.ExecutableScriptBundle.Reproducibility.BundleHashSHA256
	policyHash := job.Package.ExecutableScriptBundle.Reproducibility.BrowserAgentContractHashSHA256
	for index, planStep := range job.Package.ExecutableScriptBundle.PlanJSON.Steps {
		step := model.StepResult{NodeID: planStep.NodeID, Status: "passed", DurationMS: 100 + index}
		steps = append(steps, step)
		reports = append(reports, model.ValidationReport{
			SchemaVersion: model.ValidationReportSchemaVersion, ReportID: "report_" + job.JobID + "_" + strconv.Itoa(index+1), RunID: job.Package.RecordingRunSpec.RunID,
			SourcePackageID: job.Package.PackageID, SourceBundleHashSHA256: bundleHash, PolicyHashSHA256: policyHash,
			Phase: model.ValidationPhaseRuntimeStage, NodeID: planStep.NodeID, StageID: stageByNode[planStep.NodeID], Decision: model.ValidationDecisionContinue,
			PassRate: 1, OverallConfidence: 1, EvidenceQuality: model.RuntimeObservationActualBrowser,
			EvidenceRefs: []model.EvidenceRef{{ID: "evidence_result_" + strconv.Itoa(index+1), Kind: model.EvidenceKindWebScreenshot, ArtifactID: "artifact_screenshot"}}, CreatedAt: now,
		})
	}
	refs := make([]model.ArtifactRef, 0, len(artifacts))
	descriptors := make([]model.PackageArtifactDescriptor, 0, len(artifacts))
	var stageLog *model.ArtifactRef
	for _, artifact := range artifacts {
		ref := model.ArtifactRef{ID: artifact.ArtifactID, Kind: artifact.Kind, URI: directWorkerArtifactURI(job.JobID, artifact.ArtifactID), MimeType: artifact.MimeType, SHA256: artifact.SHA256, SizeBytes: artifact.SizeBytes}
		refs = append(refs, ref)
		descriptors = append(descriptors, model.PackageArtifactDescriptor{ID: artifact.ArtifactID, Role: artifact.Role, Kind: artifact.Kind, URI: ref.URI, MimeType: artifact.MimeType, SHA256: artifact.SHA256, SizeBytes: artifact.SizeBytes, Encrypted: true, Sensitive: true, RecipientKeyID: "direct-lease"})
		if artifact.Kind == "browser_agent_stage_event_log" {
			copyRef := ref
			stageLog = &copyRef
		}
	}
	result := model.RecordingResultPackage{
		ResultID: "result_" + job.JobID, SourcePackageID: job.Package.PackageID, CloudJobID: job.JobID,
		SchemaVersion: model.RecordingResultPackageSchemaVersion, Status: model.RecordingResultStatusGenerated,
		ExecutionTrace: &model.ExecutionTrace{ID: "trace_" + job.JobID, WorkflowGraphID: job.Package.WorkflowGraph.ID, GraphVersion: job.Package.WorkflowGraph.Version, PassRate: 1, StepResults: steps, Artifacts: refs},
		StepResults:    steps, GeneratedAssets: refs, ValidationReports: reports, StageEventLogRef: stageLog,
		VerificationReport: model.VerificationReport{PassRate: 1, ReproducibilityMatch: true}, AuditTrail: model.CloudExecutionAuditTrail{CompletedAt: now},
		Delivery: model.ResultDelivery{
			ResultPackageRef: model.PackageArtifactDescriptor{ID: "result_package", Role: "recording_result", Kind: model.ArtifactKindRecordingResultPackage, URI: "direct://jobs/" + url.PathEscape(job.JobID) + "/result", SHA256: strings.Repeat("a", 64), Encrypted: true, Sensitive: true, RecipientKeyID: "direct-lease"},
			AssetRefs:        descriptors, RecipientKind: model.ResultRecipientAppInstallation, RecipientKeyID: "direct-lease", EncryptionAlg: model.DirectTransportCryptoSuite, AckRequired: true, ExpiresAt: now.Add(time.Hour),
		}, CreatedAt: now,
	}
	manifest := directAppReplayManifest(job, result)
	validationRunReport, err := BuildValidationRunReport(result, job.Package, manifest)
	if err != nil {
		t.Fatalf("direct App test fixture validation run report: %v", err)
	}
	result.ValidationRunReport = &validationRunReport
	return result
}

func uploadDirectAppFormalArtifacts(t *testing.T, gateway *directtransport.Gateway, job directtransport.WorkerJob, mediaBytes []byte) []model.DirectArtifact {
	t.Helper()
	artifacts := []model.DirectArtifact{
		uploadDirectWorkerArtifact(t, gateway, job.JobID, "artifact_video", "final.mp4", "video/mp4", "final_demo", "demo_video", mediaBytes),
		uploadDirectWorkerArtifact(t, gateway, job.JobID, "artifact_master_2k", "final_master_2k.mp4", "video/mp4", "final_master_2k", "final_video_final_master_2k", mediaBytes),
		uploadDirectWorkerArtifact(t, gateway, job.JobID, "artifact_delivery_1080p", "final_delivery_1080p.mp4", "video/mp4", "final_delivery_1080p", "final_video_final_delivery_1080p", mediaBytes),
		uploadDirectWorkerArtifact(t, gateway, job.JobID, "artifact_deliverables_manifest", "deliverables_manifest.json", "application/json", "deliverables_manifest", "deliverables_manifest", []byte("{}\n")),
		uploadDirectWorkerArtifact(t, gateway, job.JobID, "artifact_raw_recording", "recording.webm", "video/webm", "raw_recording", "raw_recording", mediaBytes),
		uploadDirectWorkerArtifact(t, gateway, job.JobID, "artifact_trace", "trace.zip", "application/zip", "browser_trace", "browser_trace", mediaBytes),
		uploadDirectWorkerArtifact(t, gateway, job.JobID, "artifact_screenshot", "stage.png", "image/png", "stage_evidence", "screenshot", mediaBytes),
		uploadDirectWorkerArtifact(t, gateway, job.JobID, "artifact_timeline_catalog", "asset_timeline_catalog.json", "application/json", "render_metadata", "asset_timeline_catalog", []byte("{}\n")),
		uploadDirectWorkerArtifact(t, gateway, job.JobID, "artifact_edit_plan", "demo_edit_plan.json", "application/json", "render_plan", "demo_edit_plan", []byte("{}\n")),
	}
	eventBytes := directAppStageEventLogBytes(t, job)
	artifacts = append(artifacts, uploadDirectWorkerArtifact(t, gateway, job.JobID, "artifact_events", "events.jsonl", "application/x-ndjson", "stage_event_log", "browser_agent_stage_event_log", eventBytes))
	provisional := directAppTestResult(t, job, artifacts)
	manifestBytes := directAppReplayManifestBytes(t, job, provisional)
	artifacts = append(artifacts, uploadDirectWorkerArtifact(t, gateway, job.JobID, "artifact_replay_manifest", "replay-manifest.json", "application/json", "replay_manifest", "replay_manifest", manifestBytes))
	return artifacts
}

func directAppStageEventLogBytes(t *testing.T, job directtransport.WorkerJob) []byte {
	t.Helper()
	bundle := job.Package.ExecutableScriptBundle
	stageByNode := map[string]string{}
	for _, stage := range bundle.StageApprovalPlan.Stages {
		stageByNode[stage.NodeID] = stage.ID
	}
	sequence := int64(0)
	lines := make([]byte, 0, 4096)
	now := time.Now().UTC()
	for index, step := range bundle.PlanJSON.Steps {
		for _, eventType := range []model.StageExecutionEventType{model.StageExecutionEventStageStarted, model.StageExecutionEventOutcomeObserved, model.StageExecutionEventStageCompleted} {
			sequence++
			event := model.StageExecutionEvent{
				SchemaVersion: model.StageExecutionEventSchemaVersion, EventID: "event_" + strconv.FormatInt(sequence, 10), RunID: job.Package.RecordingRunSpec.RunID,
				SourcePackageID: job.Package.PackageID, SourceBundleHashSHA256: bundle.Reproducibility.BundleHashSHA256, PolicyHashSHA256: bundle.Reproducibility.BrowserAgentContractHashSHA256,
				NodeID: step.NodeID, StageID: stageByNode[step.NodeID], Attempt: 1, Sequence: sequence, EventType: eventType, OccurredAt: now.Add(time.Duration(sequence) * time.Millisecond),
			}
			if eventType == model.StageExecutionEventOutcomeObserved {
				event.Observation = &model.RuntimeObservation{Source: model.RuntimeObservationActualBrowser, URL: "https://cascadeai.cn/project/test", Title: "Stage completed"}
				event.EvidenceRefs = []model.EvidenceRef{{ID: "runtime_evidence_" + strconv.Itoa(index+1), Kind: model.EvidenceKindWebScreenshot, ArtifactID: "artifact_screenshot"}}
			}
			encoded, err := json.Marshal(event)
			if err != nil {
				t.Fatal(err)
			}
			lines = append(lines, encoded...)
			lines = append(lines, '\n')
		}
	}
	return lines
}

func directAppReplayManifestBytes(t *testing.T, job directtransport.WorkerJob, result model.RecordingResultPackage) []byte {
	t.Helper()
	manifest := directAppReplayManifest(job, result)
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(encoded, '\n')
}

func directAppReplayManifest(job directtransport.WorkerJob, result model.RecordingResultPackage) model.ReplayManifest {
	bundle := job.Package.ExecutableScriptBundle
	stageByNode := map[string]string{}
	for _, stage := range bundle.StageApprovalPlan.Stages {
		stageByNode[stage.NodeID] = stage.ID
	}
	manifest := model.ReplayManifest{
		SchemaVersion: model.ReplayManifestSchemaVersion, ManifestID: "manifest_" + job.JobID, CreatedAt: time.Now().UTC(),
		RunID: job.Package.RecordingRunSpec.RunID, PackageID: job.Package.PackageID,
		BundleHashSHA256: bundle.Reproducibility.BundleHashSHA256, PolicyHashSHA256: bundle.Reproducibility.BrowserAgentContractHashSHA256,
		Status: "success", FinalDecision: model.ValidationDecisionContinue,
		MP4URI: directWorkerArtifactURI(job.JobID, "artifact_video"), RawRecordingURI: directWorkerArtifactURI(job.JobID, "artifact_raw_recording"),
		BrowserTraceURI: directWorkerArtifactURI(job.JobID, "artifact_trace"), StageEventLogURI: directWorkerArtifactURI(job.JobID, "artifact_events"),
		ManifestURI: directWorkerArtifactURI(job.JobID, "artifact_replay_manifest"), ProtocolRuntime: model.DirectTransportProtocolVersion,
		ExecutionBundleRuntime: model.ExecutableScriptRuntimeBrowserAgentOutlineV1,
	}
	for index, step := range result.StepResults {
		firstEvent := index*3 + 1
		manifest.Stages = append(manifest.Stages, model.ReplayManifestStage{
			NodeID: step.NodeID, StageID: stageByNode[step.NodeID], Order: index + 1, Status: step.Status, ValidationDecision: model.ValidationDecisionContinue,
			EvidenceArtifactIDs:         []string{"artifact_screenshot"},
			ActionDefinitionEvidenceIDs: []string{"approved_action_" + step.NodeID},
			BeforeScreenshotArtifactIDs: []string{"artifact_screenshot"}, AfterScreenshotArtifactIDs: []string{"artifact_screenshot"},
			StageEventIDs:    []string{"event_" + strconv.Itoa(firstEvent), "event_" + strconv.Itoa(firstEvent+1), "event_" + strconv.Itoa(firstEvent+2)},
			TraceArtifactIDs: []string{"artifact_trace"},
		})
	}
	for _, report := range result.ValidationReports {
		manifest.ValidationReports = append(manifest.ValidationReports, model.ReplayManifestValidationRef{ReportID: report.ReportID, Phase: report.Phase, Decision: report.Decision, CheckCount: len(report.Checks)})
	}
	return manifest
}
func reserveDirectAppTestPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	return port
}
