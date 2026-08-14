package app

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cascade-demoops/backend/internal/model"
)

func TestRealAppAcceptanceRejectsUnverifiedPackageAndExplainsOwnership(t *testing.T) {
	service := newTestDevHTTPServer(t).service
	pkg, upload := uploadAcceptanceTestPackage(t, service, false)

	item, err := service.GetRealAppExecutionAcceptanceItem(t.Context(), pkg.OrgID, upload.ExchangePackageID)
	if err != nil {
		t.Fatal(err)
	}
	if item.Source.Origin != "unverified_client_upload" || item.Source.AppGenerated || item.Eligibility.CanRun {
		t.Fatalf("unverified package must be visible but blocked: %+v", item)
	}
	if !hasAcceptanceIssue(item.Eligibility.Issues, "app_origin_unverified", "app") {
		t.Fatalf("missing App-owned origin diagnostic: %+v", item.Eligibility.Issues)
	}
	if _, err := service.RunRealAppExecutionAcceptance(t.Context(), pkg.OrgID, upload.ExchangePackageID); err == nil || !strings.Contains(err.Error(), "app_origin_unverified") {
		t.Fatalf("unverified package reached execution: %v", err)
	}
}

func TestRealAppAcceptanceRecognizesTransportAuthenticatedAppPackage(t *testing.T) {
	service := newTestDevHTTPServer(t).service
	pkg, upload := uploadAcceptanceTestPackage(t, service, true)

	item, err := service.GetRealAppExecutionAcceptanceItem(t.Context(), pkg.OrgID, upload.ExchangePackageID)
	if err != nil {
		t.Fatal(err)
	}
	if item.Source.Origin != "app_formal_exchange" || !item.Source.AppGenerated || !item.Source.TransportAuthenticated {
		t.Fatalf("formal App source was not retained: %+v", item.Source)
	}
	if !item.Eligibility.CanRun || item.Eligibility.Verdict != "ready" {
		t.Fatalf("valid formal App package should be runnable: %+v", item.Eligibility)
	}
}

func TestRealAppAcceptanceListExcludesServerControlledFixtures(t *testing.T) {
	service := newTestDevHTTPServer(t).service
	pkg, upload := uploadAcceptanceTestPackage(t, service, false)
	state, err := service.exchange.packageState(pkg.OrgID, upload.ExchangePackageID)
	if err != nil {
		t.Fatal(err)
	}
	state.Envelope.Producer.RuntimeProfile = "server_controlled_acceptance"

	view, err := service.GetRealAppExecutionAcceptance(t.Context(), pkg.OrgID)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Items) != 0 {
		t.Fatalf("Server fixtures must stay in the fixed acceptance tab: %+v", view.Items)
	}
}

func TestServerAcceptanceRejectsCrossOriginBrowserRequests(t *testing.T) {
	server := newTestDevHTTPServerWithEnvironment(t, "development")
	request := httptest.NewRequest(http.MethodGet, "/v1/desktop/app-execution-acceptance?org_id=org_1", nil)
	request.RemoteAddr = "127.0.0.1:54321"
	request.Host = "127.0.0.1:4317"
	request.Header.Set("Origin", "https://malicious.example")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code == http.StatusOK {
		t.Fatalf("cross-origin browser request reached local acceptance API: code=%d body=%s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/v1/desktop/app-execution-acceptance?org_id=org_1", nil)
	request.RemoteAddr = "127.0.0.1:54321"
	request.Host = "127.0.0.1:4317"
	request.Header.Set("Origin", "http://127.0.0.1:4317")
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("same-origin local page was rejected: code=%d body=%s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/v1/desktop/app-execution-acceptance?org_id=org_1", nil)
	request.RemoteAddr = "127.0.0.1:54321"
	request.Host = "localhost:4317"
	request.Header.Set("Origin", "http://localhost:4317")
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("same-origin localhost page was rejected: code=%d body=%s", response.Code, response.Body.String())
	}
}

func uploadAcceptanceTestPackage(t *testing.T, service *Service, authenticated bool) (model.ClientExecutionPackage, model.ExecutionPackageUploadResponse) {
	t.Helper()
	service.runtime.DevRepoRoot = filepath.Join("..", "..", "..")
	pkg, err := protocolAcceptancePackage(service.browserAgentAcceptanceFixturePath(), "http://127.0.0.1:54321")
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Metadata == nil {
		pkg.Metadata = map[string]any{}
	}
	pkg.Metadata["producer"] = "app_generated_test"
	if err := normalizeClientExecutionPackageForUpload(&pkg); err != nil {
		t.Fatal(err)
	}
	refreshTestPackageApprovalDigests(t, &pkg)
	now := time.Date(2026, 8, 3, 2, 0, 0, 0, time.UTC)
	service.exchange.now = fixedClock(now)
	producer := model.ExchangeProducer{InstallID: "install_acceptance_test", RuntimeProfile: "desktop-product-run"}
	init, err := service.exchange.Init(t.Context(), model.ExecutionPackageInitRequest{OrgID: pkg.OrgID, ProjectID: pkg.ProjectID, PackageKind: model.ExchangePackageKindClientExecution, Producer: producer})
	if err != nil {
		t.Fatal(err)
	}
	envelope := sampleEnvelopeForAppTest(t, pkg, now)
	envelope.Producer = producer
	request := model.ExecutionPackageUploadRequest{UploadID: init.UploadID, Envelope: envelope, PayloadRef: envelope.PayloadRef}
	if authenticated {
		service.exchange.verifier = acceptanceSignatureVerifier(true)
		upload, uploadErr := service.exchange.UploadFromInstallation(t.Context(), request, pkg, producer.InstallID)
		if uploadErr != nil {
			t.Fatal(uploadErr)
		}
		return pkg, upload
	}
	upload, err := service.exchange.Upload(t.Context(), request, pkg)
	if err != nil {
		t.Fatal(err)
	}
	return pkg, upload
}

func hasAcceptanceIssue(issues []RealAppAcceptanceIssue, code, owner string) bool {
	for _, issue := range issues {
		if issue.Code == code && issue.Owner == owner {
			return true
		}
	}
	return false
}

type acceptanceSignatureVerifier bool

func (v acceptanceSignatureVerifier) VerifyExchangeSignature(*model.ExchangeEnvelope, []byte) bool {
	return bool(v)
}
