package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"cascade-demoops/backend/internal/direct"
	"cascade-demoops/backend/internal/model"
)

type smokeClient struct {
	httpClient *http.Client
	controlURL string
	bootstrap  string
	publicKey  ed25519.PublicKey
	privateKey ed25519.PrivateKey
	lease      direct.DirectPortLease
}

const recoverySmokeStateSchema = "cascade.browser_agent_direct_recovery_smoke.v1"

type recoverySmokeState struct {
	SchemaVersion    string                 `json:"schema_version"`
	ControlURL       string                 `json:"control_url"`
	InsecureDevTLS   bool                   `json:"insecure_dev_tls"`
	PublicKeyBase64  string                 `json:"public_key_base64"`
	PrivateKeyBase64 string                 `json:"private_key_base64"`
	Lease            direct.DirectPortLease `json:"lease"`
	JobID            string                 `json:"job_id"`
	PackageID        string                 `json:"package_id"`
}

func main() {
	controlURL := flag.String("control-url", "https://127.0.0.1:18443", "Direct TLS control URL")
	packagePath := flag.String("package", "", "path to an App-approved browser-agent-outline-v1 package JSON")
	outputDir := flag.String("output", "artifacts/direct-smoke/latest", "verified result output directory")
	timeout := flag.Duration("timeout", 15*time.Minute, "end-to-end timeout")
	insecureDevTLS := flag.Bool("insecure-dev-tls", false, "allow a local self-signed certificate; never use in production")
	serverFixture := flag.Bool("server-controlled-fixture", false, "normalize the checked-in fixture for Server-only recovery testing; never formal App evidence")
	prepareRecovery := flag.String("prepare-recovery-state", "", "upload a Server fixture, persist protected client state, and exit without running Chromium")
	verifyRecovery := flag.String("verify-recovery-state", "", "load protected client state after Gateway restart and verify query + Worker reclaim/release")
	workerURL := flag.String("worker-url", "http://127.0.0.1:18444", "loopback Worker API URL used only by recovery verification")
	flag.Parse()
	bootstrap := strings.TrimSpace(os.Getenv("CASCADE_DIRECT_BOOTSTRAP_TOKEN"))
	if strings.TrimSpace(*verifyRecovery) != "" {
		if strings.TrimSpace(*prepareRecovery) != "" || strings.TrimSpace(*packagePath) != "" || *serverFixture {
			fatal(errors.New("-verify-recovery-state cannot be combined with package or prepare flags"))
		}
		workerToken := strings.TrimSpace(os.Getenv("CASCADE_DIRECT_WORKER_TOKEN"))
		if bootstrap == "" || workerToken == "" {
			fatal(errors.New("CASCADE_DIRECT_BOOTSTRAP_TOKEN and CASCADE_DIRECT_WORKER_TOKEN are required for recovery verification"))
		}
		must(verifyGatewayRecovery(*verifyRecovery, *workerURL, bootstrap, workerToken))
		return
	}
	if strings.TrimSpace(*packagePath) == "" || bootstrap == "" {
		fatal(errors.New("-package and CASCADE_DIRECT_BOOTSTRAP_TOKEN are required"))
	}
	if strings.TrimSpace(*prepareRecovery) != "" && !*serverFixture {
		fatal(errors.New("-prepare-recovery-state requires -server-controlled-fixture"))
	}
	if *serverFixture && strings.TrimSpace(*prepareRecovery) == "" {
		fatal(errors.New("-server-controlled-fixture is restricted to recovery preparation and cannot run end-to-end smoke"))
	}
	packageJSON, err := os.ReadFile(*packagePath)
	must(err)
	var pkg model.ClientExecutionPackage
	must(json.Unmarshal(packageJSON, &pkg))
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	must(err)
	if *serverFixture {
		packageJSON, pkg, err = normalizeServerControlledFixture(packageJSON, pkg, direct.InstallationID(publicKey))
		must(err)
	}
	if pkg.ExecutableScriptBundle == nil || pkg.ExecutableScriptBundle.ScriptManifest.Runtime != model.ExecutableScriptRuntimeBrowserAgentOutlineV1 {
		fatal(errors.New("package runtime must be browser-agent-outline-v1"))
	}
	must(model.ValidateClientExecutionPackageForDirectExecution(&pkg))
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: *insecureDevTLS} //nolint:gosec -- explicit local-only flag
	client := &smokeClient{
		httpClient: &http.Client{Transport: transport, Timeout: 2 * time.Minute}, controlURL: strings.TrimRight(*controlURL, "/"),
		bootstrap: bootstrap, publicKey: publicKey, privateKey: privateKey,
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	must(client.allocate(ctx))
	released := false
	defer func() {
		if !released {
			_ = client.release(context.Background())
		}
	}()
	receipt, err := client.uploadPackage(ctx, packageJSON)
	must(err)
	fmt.Printf("Direct package accepted: job=%s package=%s\n", receipt.JobID, receipt.PackageID)
	if strings.TrimSpace(*prepareRecovery) != "" {
		must(writeRecoverySmokeState(*prepareRecovery, client, receipt))
		released = true
		fmt.Printf("Server-only recovery state written to %s; app_formal_run=false\n", *prepareRecovery)
		return
	}
	status, err := client.wait(ctx, receipt.JobID)
	must(err)
	if status.Status != "completed" {
		fatal(fmt.Errorf("direct job ended with status=%s stage=%s", status.Status, status.Stage))
	}
	resultJSON, result, err := client.result(ctx, receipt.JobID)
	must(err)
	must(os.MkdirAll(*outputDir, 0o700))
	must(os.WriteFile(filepath.Join(*outputDir, "recording-result.json"), append(resultJSON, '\n'), 0o600))
	must(client.downloadArtifacts(ctx, receipt.JobID, result, *outputDir))
	must(writeVerifiedMarkers(result, *outputDir))
	must(client.release(ctx))
	released = true
	fmt.Printf("Verified Direct result written to %s\n", *outputDir)
}

func normalizeServerControlledFixture(original []byte, pkg model.ClientExecutionPackage, installationID string) ([]byte, model.ClientExecutionPackage, error) {
	if pkg.ExecutableScriptBundle == nil {
		return nil, pkg, errors.New("fixture executable script bundle is required")
	}
	pkg.Reproducibility.PackageHashSHA256 = direct.HashSHA256(original)
	pkg.CredentialGrants = nil
	pkg.ProducerInstallationID = installationID
	if err := populateServerControlledSelectorProvenance(&pkg, time.Now().UTC()); err != nil {
		return nil, pkg, err
	}
	if err := recomputeServerControlledFixtureHashes(&pkg); err != nil {
		return nil, pkg, err
	}
	pkg.SafetyReport.HumanApproval.ApprovedByInstallationID = installationID
	pkg.SafetyReport.HumanApproval.ApprovalSchemaVersion = model.UserApprovalSchemaVersion
	pkg.SafetyReport.HumanApproval.PlanDigestSHA256 = pkg.ExecutableScriptBundle.Reproducibility.PlanHashSHA256
	subjectDigests, err := model.ComputePackageApprovalComponentDigests(pkg)
	if err != nil {
		return nil, pkg, err
	}
	pkg.SafetyReport.HumanApproval.SubjectDigestsSHA256 = subjectDigests
	digest, err := model.ComputePackageApprovalSubjectDigest(pkg)
	if err != nil {
		return nil, pkg, err
	}
	pkg.SafetyReport.HumanApproval.ApprovalSubjectDigestSHA256 = digest
	data, err := json.Marshal(pkg)
	return data, pkg, err
}

func recomputeServerControlledFixtureHashes(pkg *model.ClientExecutionPackage) error {
	if pkg == nil || pkg.WorkflowGraph == nil || pkg.ExecutableScriptBundle == nil {
		return errors.New("fixture graph and executable bundle are required")
	}
	graphHash, err := model.DigestCanonicalJSON(pkg.WorkflowGraph)
	if err != nil {
		return err
	}
	pkg.Reproducibility.GraphHashSHA256 = graphHash
	bundle := pkg.ExecutableScriptBundle
	bundle.Reproducibility.GraphHashSHA256 = graphHash
	if bundle.Reproducibility.PlanHashSHA256, err = bundle.PlanJSON.ComputeScriptHash(); err != nil {
		return err
	}
	if bundle.Reproducibility.StagePlanHashSHA256, err = model.DigestCanonicalJSON(bundle.StageApprovalPlan); err != nil {
		return err
	}
	if bundle.Reproducibility.OutlineHashSHA256, err = model.DigestCanonicalJSON(bundle.ScriptOutline); err != nil {
		return err
	}
	if bundle.Reproducibility.PromptPolicyHashSHA256, err = model.DigestCanonicalJSON(bundle.AgentPromptPolicy); err != nil {
		return err
	}
	if bundle.Reproducibility.BrowserAgentContractHashSHA256, err = model.DigestCanonicalJSON(bundle.BrowserAgentContract); err != nil {
		return err
	}
	bundle.Reproducibility.BundleHashSHA256, err = bundle.ComputeBundleHash()
	return err
}

func populateServerControlledSelectorProvenance(pkg *model.ClientExecutionPackage, observedAt time.Time) error {
	data, err := json.Marshal(pkg)
	if err != nil {
		return err
	}
	var root any
	if err := json.Unmarshal(data, &root); err != nil {
		return err
	}
	index := 0
	var walk func(any)
	walk = func(value any) {
		switch typed := value.(type) {
		case map[string]any:
			for key, child := range typed {
				if key == "selector_alternatives" || key == "dom_hints" {
					if candidates, ok := child.([]any); ok {
						for _, raw := range candidates {
							candidate, ok := raw.(map[string]any)
							if !ok {
								continue
							}
							index++
							evidenceID := fmt.Sprintf("ev_server_fixture_selector_%d", index)
							candidate["evidence_id"] = evidenceID
							candidate["source_kind"] = "page_scan"
							candidate["source_digest"] = model.SHA256Hex([]byte(fmt.Sprintf("%v:%v:%d", candidate["kind"], candidate["value"], index)))
							candidate["observed_role"] = "button"
							candidate["observed_accessible_name"] = fmt.Sprintf("Server fixture target %d", index)
							candidate["observed_url"] = "https://app.example.com/dashboard"
							candidate["observed_route_template"] = "/dashboard"
							candidate["evidence_digest_sha256"] = candidate["source_digest"]
							candidate["observed_at"] = observedAt.Format(time.RFC3339Nano)
							candidate["evidence_refs"] = []any{map[string]any{"id": evidenceID, "kind": "browser_scan"}}
						}
					}
					continue
				}
				walk(child)
			}
		case []any:
			for _, child := range typed {
				walk(child)
			}
		}
	}
	walk(root)
	data, err = json.Marshal(root)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, pkg)
}

func writeRecoverySmokeState(path string, client *smokeClient, receipt direct.DirectPackageReceipt) error {
	if client == nil || receipt.JobID == "" || receipt.PackageID == "" {
		return errors.New("recovery smoke state is incomplete")
	}
	state := recoverySmokeState{
		SchemaVersion: recoverySmokeStateSchema, ControlURL: client.controlURL,
		InsecureDevTLS:  client.httpClient.Transport.(*http.Transport).TLSClientConfig.InsecureSkipVerify, //nolint:gosec -- recorded local test setting
		PublicKeyBase64: base64.StdEncoding.EncodeToString(client.publicKey), PrivateKeyBase64: base64.StdEncoding.EncodeToString(client.privateKey),
		Lease: client.lease, JobID: receipt.JobID, PackageID: receipt.PackageID,
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}

func readRecoverySmokeState(path, bootstrap string) (*smokeClient, recoverySmokeState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, recoverySmokeState{}, err
	}
	var state recoverySmokeState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, state, err
	}
	publicKey, err := base64.StdEncoding.DecodeString(state.PublicKeyBase64)
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		return nil, state, errors.New("invalid recovery public key")
	}
	privateKey, err := base64.StdEncoding.DecodeString(state.PrivateKeyBase64)
	if err != nil || len(privateKey) != ed25519.PrivateKeySize {
		return nil, state, errors.New("invalid recovery private key")
	}
	if state.SchemaVersion != recoverySmokeStateSchema || state.JobID == "" || state.PackageID == "" || state.Lease.LeaseID == "" || state.Lease.LeaseToken == "" || state.Lease.InstallationID != direct.InstallationID(ed25519.PublicKey(publicKey)) {
		return nil, state, errors.New("invalid recovery smoke state")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: state.InsecureDevTLS} //nolint:gosec -- explicit saved local-only setting
	client := &smokeClient{
		httpClient: &http.Client{Transport: transport, Timeout: 2 * time.Minute}, controlURL: strings.TrimRight(state.ControlURL, "/"), bootstrap: bootstrap,
		publicKey: ed25519.PublicKey(publicKey), privateKey: ed25519.PrivateKey(privateKey), lease: state.Lease,
	}
	return client, state, nil
}

func verifyGatewayRecovery(statePath, workerURL, bootstrap, workerToken string) error {
	client, state, err := readRecoverySmokeState(statePath, bootstrap)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	status, err := client.status(ctx, state.JobID)
	if err != nil {
		return fmt.Errorf("query restored job: %w", err)
	}
	if status.JobID != state.JobID || status.PackageID != state.PackageID || status.Status != "queued" {
		return fmt.Errorf("restored job mismatch: %+v", status)
	}
	if err := workerClaimAndRelease(ctx, workerURL, workerToken, state.JobID); err != nil {
		return err
	}
	status, err = client.status(ctx, state.JobID)
	if err != nil {
		return fmt.Errorf("query job after Worker release: %w", err)
	}
	if status.Status != "queued" || status.JobID != state.JobID {
		return fmt.Errorf("Worker reclaim/release did not restore queued status: %+v", status)
	}
	fmt.Printf("Gateway recovery verified: job=%s status=%s stage=%s app_formal_run=false chromium_started=false\n", status.JobID, status.Status, status.Stage)
	return nil
}

func workerClaimAndRelease(ctx context.Context, workerURL, token, jobID string) error {
	baseURL := strings.TrimRight(strings.TrimSpace(workerURL), "/")
	if baseURL != "http://127.0.0.1:18444" {
		return errors.New("recovery verification Worker URL must remain http://127.0.0.1:18444")
	}
	client := &http.Client{Timeout: 30 * time.Second}
	body, _ := json.Marshal(map[string]string{"protocol_version": direct.WorkerProtocolVersion, "job_id": jobID})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/v1/worker/jobs/claim", bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	if err := doWorkerRequest(client, request); err != nil {
		return fmt.Errorf("claim restored job: %w", err)
	}
	request, err = http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/v1/worker/jobs/"+url.PathEscape(jobID)+"/release", nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	if err := doWorkerRequest(client, request); err != nil {
		return fmt.Errorf("release restored Worker claim: %w", err)
	}
	return nil
}

func doWorkerRequest(client *http.Client, request *http.Request) error {
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1024*1024))
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("%s %s failed (%d): %s", request.Method, request.URL.Path, response.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func writeVerifiedMarkers(result model.RecordingResultPackage, outputDir string) error {
	for _, ref := range result.GeneratedAssets {
		if ref.ID == "" || ref.SHA256 == "" {
			continue
		}
		name := safeName(ref.ID)
		if extension := filepath.Ext(strings.TrimSpace(ref.URI)); extension != "" && len(extension) <= 8 {
			name += extension
		}
		if err := os.WriteFile(filepath.Join(outputDir, name+".verified.sha256"), []byte(ref.SHA256+"  "+name+"\n"), 0o600); err != nil {
			return err
		}
	}
	return nil
}

func (c *smokeClient) allocate(ctx context.Context) error {
	request := c.signedLeaseRequest()
	var lease direct.DirectPortLease
	if err := c.controlJSON(ctx, http.MethodPost, "/v1/direct/leases", request, &lease); err != nil {
		return err
	}
	if lease.LeaseID == "" || lease.InstallationID == "" || lease.DataPort < direct.DataPortMin || lease.DataPort > direct.DataPortMax || lease.LeaseToken == "" || lease.CryptoSuite != direct.CryptoSuite {
		return errors.New("invalid direct lease response")
	}
	c.lease = lease
	return nil
}

func (c *smokeClient) release(ctx context.Context) error {
	if c.lease.LeaseID == "" {
		return nil
	}
	return c.controlJSON(ctx, http.MethodPost, "/v1/direct/leases/release", c.signedLeaseRequest(), nil)
}

func (c *smokeClient) signedLeaseRequest() direct.DirectLeaseRequest {
	request := direct.DirectLeaseRequest{
		ProtocolVersion: direct.ProtocolVersion, InstallationID: direct.InstallationID(c.publicKey), ClientVersion: "directsmoke-v1",
		TimestampUnixMS: time.Now().UnixMilli(), RequestNonce: randomValue(18), SigningPublicKeyBase64: base64.StdEncoding.EncodeToString(c.publicKey),
	}
	request.SignatureBase64 = base64.StdEncoding.EncodeToString(ed25519.Sign(c.privateKey, direct.LeaseRequestSigningBytes(request)))
	return request
}

func (c *smokeClient) controlJSON(ctx context.Context, method, path string, input, output any) error {
	var body []byte
	var err error
	if input != nil {
		body, err = json.Marshal(input)
		if err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.controlURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.bootstrap)
	req.Header.Set("Content-Type", "application/json")
	return c.doJSON(req, output)
}

func (c *smokeClient) uploadPackage(ctx context.Context, packageJSON []byte) (direct.DirectPackageReceipt, error) {
	message, err := direct.EncryptMessage(c.lease.LeaseToken, c.lease.InstallationID, c.lease.LeaseID, c.lease.DataPort, "package_"+randomValue(12), "client_execution_package", "app_to_browser_agent", packageJSON, time.Now())
	if err != nil {
		return direct.DirectPackageReceipt{}, err
	}
	response, err := c.dataMessage(ctx, http.MethodPost, "/v1/direct/packages", message)
	if err != nil {
		return direct.DirectPackageReceipt{}, err
	}
	plain, err := response.Decrypt(c.lease.LeaseToken, c.lease.InstallationID, c.lease.DataPort)
	if err != nil {
		return direct.DirectPackageReceipt{}, err
	}
	var receipt direct.DirectPackageReceipt
	err = json.Unmarshal(plain, &receipt)
	return receipt, err
}

func (c *smokeClient) wait(ctx context.Context, jobID string) (direct.DirectJobStatus, error) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		status, err := c.status(ctx, jobID)
		if err != nil {
			return direct.DirectJobStatus{}, err
		}
		fmt.Printf("status=%s stage=%s progress=%d%%\n", status.Status, status.Stage, status.Progress)
		if isTerminal(status.Status) {
			return status, nil
		}
		select {
		case <-ctx.Done():
			return status, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (c *smokeClient) status(ctx context.Context, jobID string) (direct.DirectJobStatus, error) {
	var encrypted direct.DirectEncryptedMessage
	if err := c.dataRead(ctx, "/v1/direct/jobs/"+url.PathEscape(jobID), &encrypted); err != nil {
		return direct.DirectJobStatus{}, err
	}
	plain, err := encrypted.Decrypt(c.lease.LeaseToken, c.lease.InstallationID, c.lease.DataPort)
	if err != nil {
		return direct.DirectJobStatus{}, err
	}
	var status direct.DirectJobStatus
	err = json.Unmarshal(plain, &status)
	return status, err
}

func (c *smokeClient) result(ctx context.Context, jobID string) ([]byte, model.RecordingResultPackage, error) {
	var encrypted direct.DirectEncryptedMessage
	if err := c.dataRead(ctx, "/v1/direct/jobs/"+url.PathEscape(jobID)+"/result", &encrypted); err != nil {
		return nil, model.RecordingResultPackage{}, err
	}
	plain, err := encrypted.Decrypt(c.lease.LeaseToken, c.lease.InstallationID, c.lease.DataPort)
	if err != nil {
		return nil, model.RecordingResultPackage{}, err
	}
	var result model.RecordingResultPackage
	if err := json.Unmarshal(plain, &result); err != nil {
		return nil, result, err
	}
	return plain, result, nil
}

func (c *smokeClient) downloadArtifacts(ctx context.Context, jobID string, result model.RecordingResultPackage, outputDir string) error {
	for _, ref := range result.GeneratedAssets {
		path := "/v1/direct/jobs/" + url.PathEscape(jobID) + "/artifacts/" + url.PathEscape(ref.ID) + "/chunks/0"
		first, descriptor, err := c.chunk(ctx, path)
		if err != nil {
			return err
		}
		data := append([]byte{}, first...)
		for index := 1; index < descriptor.ChunkCount; index++ {
			chunkPath := "/v1/direct/jobs/" + url.PathEscape(jobID) + "/artifacts/" + url.PathEscape(ref.ID) + "/chunks/" + strconv.Itoa(index)
			chunk, next, err := c.chunk(ctx, chunkPath)
			if err != nil {
				return err
			}
			if next.SHA256 != descriptor.SHA256 || next.Size != descriptor.Size {
				return errors.New("artifact descriptor changed between chunks")
			}
			data = append(data, chunk...)
		}
		if int64(len(data)) != descriptor.Size || direct.HashSHA256(data) != descriptor.SHA256 || (ref.SHA256 != "" && ref.SHA256 != descriptor.SHA256) {
			return fmt.Errorf("artifact %q checksum or size mismatch", ref.ID)
		}
		name := safeName(ref.ID)
		if extension := filepath.Ext(strings.TrimSpace(ref.URI)); extension != "" && len(extension) <= 8 {
			name += extension
		}
		if err := os.WriteFile(filepath.Join(outputDir, name), data, 0o600); err != nil {
			return err
		}
	}
	return nil
}

func (c *smokeClient) chunk(ctx context.Context, path string) ([]byte, direct.DirectArtifactDescriptor, error) {
	var encrypted direct.DirectEncryptedMessage
	if err := c.dataRead(ctx, path, &encrypted); err != nil {
		return nil, direct.DirectArtifactDescriptor{}, err
	}
	plain, err := encrypted.Decrypt(c.lease.LeaseToken, c.lease.InstallationID, c.lease.DataPort)
	if err != nil {
		return nil, direct.DirectArtifactDescriptor{}, err
	}
	var payload struct {
		Descriptor  direct.DirectArtifactDescriptor `json:"descriptor"`
		BytesBase64 string                          `json:"bytes_base64"`
	}
	if err := json.Unmarshal(plain, &payload); err != nil {
		return nil, payload.Descriptor, err
	}
	data, err := base64.StdEncoding.DecodeString(payload.BytesBase64)
	return data, payload.Descriptor, err
}

func (c *smokeClient) dataMessage(ctx context.Context, method, path string, message direct.DirectEncryptedMessage) (direct.DirectEncryptedMessage, error) {
	body, err := json.Marshal(message)
	if err != nil {
		return direct.DirectEncryptedMessage{}, err
	}
	var response direct.DirectEncryptedMessage
	err = c.signedDataJSON(ctx, method, path, body, &response)
	return response, err
}

func (c *smokeClient) dataRead(ctx context.Context, path string, output any) error {
	return c.signedDataJSON(ctx, http.MethodGet, path, nil, output)
}

func (c *smokeClient) signedDataJSON(ctx context.Context, method, path string, body []byte, output any) error {
	ts := time.Now().UnixMilli()
	nonce := randomValue(18)
	digest := direct.HashSHA256(body)
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.lease.DataURL, "/")+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Cascade-Timestamp", strconv.FormatInt(ts, 10))
	req.Header.Set("X-Cascade-Nonce", nonce)
	req.Header.Set("X-Cascade-Body-SHA256", digest)
	req.Header.Set("X-Cascade-Signature", direct.SignDataRequest(method, path, ts, nonce, digest, c.lease.LeaseToken, c.lease.InstallationID, c.lease.LeaseID, c.lease.DataPort))
	return c.doJSON(req, output)
}

func (c *smokeClient) doJSON(req *http.Request, output any) error {
	response, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, direct.MaxArtifactSize+1024*1024))
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("%s %s failed (%d): %s", req.Method, req.URL.Path, response.StatusCode, strings.TrimSpace(string(body)))
	}
	if output == nil || len(body) == 0 {
		return nil
	}
	return json.Unmarshal(body, output)
}

func randomValue(size int) string {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(value)
}

func safeName(value string) string {
	value = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, value)
	if value == "" {
		return "artifact"
	}
	return value
}

func isTerminal(status string) bool {
	return status == "completed" || status == "failed" || status == "canceled" || status == "expired"
}

func must(err error) {
	if err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
