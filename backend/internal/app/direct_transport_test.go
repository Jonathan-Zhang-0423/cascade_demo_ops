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
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"cascade-demoops/backend/internal/config"
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

	state, err := service.CreateProject(t.Context(), orchestrator.UserInput{ProjectID: "direct-tetris", Mode: model.AppModeDesktop, ProductURL: "https://cascadeai.cn/app", ProductDescription: "进入新建项目，填写项目名俄罗斯方块，启动 Agent 构建并观察实际进度。", TargetAudience: "普通用户", MustShow: []string{"进入新建项目", "填写俄罗斯方块", "启动 Agent 构建", "观察构建进度"}, MustNotShow: []string{"密码", "令牌"}, ForbiddenPages: []string{"/billing"}, ForbiddenData: []string{"密码", "令牌"}, AllowedDomains: []string{"cascadeai.cn"}, WebpageScreenshots: formalAppScreenshotInputs("https://cascadeai.cn")})
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
	artifact := uploadDirectWorkerArtifact(t, gateway, claimed.JobID, "artifact_video", artifactBytes)
	result := directAppTestResult(claimed, artifact)
	putDirectWorkerJSON(t, gateway.WorkerHandler(), http.MethodPut, "/v1/worker/jobs/"+claimed.JobID+"/result", result, http.StatusOK)

	status, err := service.GetDirectExecutionStatus(t.Context(), state.ProjectID, claimed.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != "completed" || status.ResultPackageID != result.ResultID || len(status.Artifacts) != 1 {
		t.Fatalf("unexpected decrypted status: %+v", status)
	}
	returned, err := service.GetDirectResult(t.Context(), state.ProjectID, claimed.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if returned.ResultID != result.ResultID || returned.CloudJobID != claimed.JobID {
		t.Fatalf("unexpected decrypted result: %+v", returned)
	}
	download, err := service.DownloadDirectArtifact(t.Context(), state.ProjectID, DirectArtifactDownloadRequest{JobID: claimed.JobID, Artifact: status.Artifacts[0]})
	if err != nil {
		t.Fatal(err)
	}
	if !download.ChecksumVerified || download.SHA256 != model.SHA256Hex(artifactBytes) {
		t.Fatalf("artifact download was not verified: %+v", download)
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
	if !persisted.DesktopCloudRun.ResultDownloaded || persisted.DesktopCloudRun.ResultReview == nil || persisted.DesktopCloudRun.ResultReview.Decision != string(model.ResultReviewApproved) {
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

func TestApproveClientExecutionPackageRebindsConfidenceAfterCredentialGrantExpiry(t *testing.T) {
	root := t.TempDir()
	service, err := NewService(config.AppRuntimeConfig{
		Profile: config.ProfileDev, Environment: "test", Mode: model.AppModeDesktop,
		DatabaseDialect: config.DatabaseSQLite, SQLitePath: filepath.Join(root, "app.db"),
		DataRoot: root, ArtifactRoot: filepath.Join(root, "artifacts"),
		CacheRoot: filepath.Join(root, "cache"), LogRoot: filepath.Join(root, "logs"),
		LLMMode: config.LLMModeDeterministic,
	}, store.NewMemoryStateStore())
	if err != nil {
		t.Fatal(err)
	}
	state, err := service.CreateProject(t.Context(), orchestrator.UserInput{
		ProjectID: "direct-credential-confidence", Mode: model.AppModeDesktop,
		ProductURL: "https://cascadeai.cn/app", ProductDescription: "登录后进入新建项目，填写俄罗斯方块并启动 Agent 构建。",
		TargetAudience: "普通用户", MustShow: []string{"登录", "新建俄罗斯方块", "启动 Agent 构建"},
		AllowedDomains: []string{"cascadeai.cn"}, WebpageScreenshots: formalAppScreenshotInputs("https://cascadeai.cn"),
		DemoUsername: "vault-user", DemoPassword: "vault-password", DemoCredentialRef: "credential://demo/direct-confidence",
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
}

func TestBrowserAgentOutlineAllowsEvidenceBoundInteractionRoute(t *testing.T) {
	root := t.TempDir()
	service, err := NewService(config.AppRuntimeConfig{
		Profile: config.ProfileDev, Environment: "test", Mode: model.AppModeDesktop,
		DatabaseDialect: config.DatabaseSQLite, SQLitePath: filepath.Join(root, "app.db"),
		DataRoot: root, ArtifactRoot: filepath.Join(root, "artifacts"), CacheRoot: filepath.Join(root, "cache"),
		LogRoot: filepath.Join(root, "logs"), LLMMode: config.LLMModeDeterministic,
	}, store.NewMemoryStateStore())
	if err != nil {
		t.Fatal(err)
	}
	screenshots := formalAppScreenshotInputs("https://cascadeai.cn")
	screenshots[0].URL = "https://cascadeai.cn/"
	state, err := service.CreateProject(t.Context(), orchestrator.UserInput{
		ProjectID: "direct-login-root-route", Mode: model.AppModeDesktop,
		ProductURL: "https://cascadeai.cn", ProductDescription: "进入新建项目，填写俄罗斯方块并启动 Agent 构建。",
		TargetAudience: "普通用户", MustShow: []string{"新建俄罗斯方块", "启动 Agent 构建"},
		AllowedDomains: []string{"cascadeai.cn"}, WebpageScreenshots: screenshots,
		DemoUsername: "vault-user", DemoPassword: "vault-password", DemoCredentialRef: "credential://demo/direct-root-route",
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
	state, err := service.CreateProject(t.Context(), orchestrator.UserInput{ProjectID: "direct-base-route", Mode: model.AppModeDesktop, ProductURL: "https://cascadeai.cn/app", ProductDescription: "进入新建项目并启动 Agent 构建。", TargetAudience: "普通用户", MustShow: []string{"新建项目", "启动 Agent 构建"}, AllowedDomains: []string{"cascadeai.cn"}, WebpageScreenshots: formalAppScreenshotInputs("https://cascadeai.cn")})
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
	state, err := service.CreateProject(t.Context(), orchestrator.UserInput{ProjectID: "direct-upload-failure", Mode: model.AppModeDesktop, ProductURL: "https://cascadeai.cn/app", ProductDescription: "进入新建项目，填写项目名俄罗斯方块，启动 Agent 构建并观察实际进度。", TargetAudience: "普通用户", MustShow: []string{"进入新建项目", "填写俄罗斯方块", "启动 Agent 构建", "观察构建进度"}, MustNotShow: []string{"密码", "令牌"}, ForbiddenPages: []string{"/billing"}, ForbiddenData: []string{"密码", "令牌"}, AllowedDomains: []string{"cascadeai.cn"}, WebpageScreenshots: formalAppScreenshotInputs("https://cascadeai.cn")})
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
func uploadDirectWorkerArtifact(t *testing.T, gateway *directtransport.Gateway, jobID, artifactID string, data []byte) model.DirectArtifact {
	t.Helper()
	request := httptest.NewRequest(http.MethodPut, "/v1/worker/jobs/"+jobID+"/artifacts/"+artifactID+"?file_name=recording.webm", bytes.NewReader(data))
	request.Header.Set("Authorization", "Bearer "+directAppWorkerToken)
	request.Header.Set("Content-Type", "video/webm")
	request.Header.Set("X-Artifact-SHA256", model.SHA256Hex(data))
	request.Header.Set("X-Artifact-Role", "final_demo_video")
	request.Header.Set("X-Artifact-Kind", "video")
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
func directAppTestResult(job directtransport.WorkerJob, artifact model.DirectArtifact) model.RecordingResultPackage {
	now := time.Now().UTC()
	step := model.StepResult{NodeID: job.Package.WorkflowGraph.Nodes[0].ID, Status: "passed", DurationMS: 100}
	asset := model.ArtifactRef{ID: artifact.ArtifactID, Kind: artifact.Kind, URI: "direct://artifacts/" + artifact.ArtifactID, MimeType: artifact.MimeType, SHA256: artifact.SHA256, SizeBytes: artifact.SizeBytes}
	descriptor := model.PackageArtifactDescriptor{ID: artifact.ArtifactID, Role: artifact.Role, Kind: artifact.Kind, URI: "direct://artifacts/" + artifact.ArtifactID, MimeType: artifact.MimeType, SHA256: artifact.SHA256, SizeBytes: artifact.SizeBytes, Encrypted: true, Sensitive: true}
	return model.RecordingResultPackage{ResultID: "result_" + job.JobID, SourcePackageID: job.Package.PackageID, CloudJobID: job.JobID, SchemaVersion: model.RecordingResultPackageSchemaVersion, Status: model.RecordingResultStatusGenerated, ExecutionTrace: &model.ExecutionTrace{ID: "trace_" + job.JobID, WorkflowGraphID: job.Package.WorkflowGraph.ID, GraphVersion: job.Package.WorkflowGraph.Version, PassRate: 1, StepResults: []model.StepResult{step}, Artifacts: []model.ArtifactRef{asset}}, StepResults: []model.StepResult{step}, GeneratedAssets: []model.ArtifactRef{asset}, VerificationReport: model.VerificationReport{PassRate: 1, ReproducibilityMatch: true}, AuditTrail: model.CloudExecutionAuditTrail{CompletedAt: now}, Delivery: model.ResultDelivery{ResultPackageRef: model.PackageArtifactDescriptor{ID: "result_package", Role: "recording_result", Kind: model.ArtifactKindRecordingResultPackage, URI: "direct://results/result.json", SHA256: strings.Repeat("a", 64), Encrypted: true, Sensitive: true}, AssetRefs: []model.PackageArtifactDescriptor{descriptor}, RecipientKind: model.ResultRecipientAppInstallation, RecipientKeyID: "direct-lease", EncryptionAlg: model.DirectTransportCryptoSuite, AckRequired: true, ExpiresAt: now.Add(time.Hour)}, CreatedAt: now}
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
