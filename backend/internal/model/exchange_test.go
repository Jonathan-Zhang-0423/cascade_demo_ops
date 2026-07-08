package model

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestClientExecutionPackageJSONRoundTripPreservesGraphAndEvidence(t *testing.T) {
	pkg := sampleClientExecutionPackage(t)
	data, err := json.Marshal(pkg)
	if err != nil {
		t.Fatal(err)
	}
	var got ClientExecutionPackage
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.SchemaVersion != ClientExecutionPackageSchemaVersion {
		t.Fatalf("schema = %q", got.SchemaVersion)
	}
	if got.WorkflowGraph == nil || got.WorkflowGraph.ID != pkg.WorkflowGraph.ID {
		t.Fatalf("workflow graph did not round-trip: %#v", got.WorkflowGraph)
	}
	if len(got.EvidenceBundle.EvidenceRefs) != 1 || got.EvidenceBundle.EvidenceRefs[0].ID != "ev_route_summary" {
		t.Fatalf("evidence refs did not round-trip: %#v", got.EvidenceBundle.EvidenceRefs)
	}
	if got.RecordingRunSpec.Timeline.CaptureWindows[0].NodeID != "node_open_dashboard" {
		t.Fatalf("capture window order was not preserved: %#v", got.RecordingRunSpec.Timeline.CaptureWindows)
	}
	if got.RecordingRunSpec.Redactions.MaskSelectors[0] != "[data-sensitive]" {
		t.Fatalf("redactions did not round-trip: %#v", got.RecordingRunSpec.Redactions)
	}
}

func TestClientExecutionPackageAvoidsRawSecretAndSourceContent(t *testing.T) {
	pkg := sampleClientExecutionPackage(t)
	payload, err := json.Marshal(pkg)
	if err != nil {
		t.Fatal(err)
	}
	text := string(payload)
	forbidden := []string{
		"raw-password-123",
		"BEGIN PRIVATE KEY",
		"function submitPayment",
		"src/components/BillingForm.tsx",
	}
	for _, token := range forbidden {
		if strings.Contains(text, token) {
			t.Fatalf("client execution package leaked forbidden token %q: %s", token, text)
		}
	}
	if !strings.Contains(text, "cloud_secret_ref") {
		t.Fatal("expected package to carry a cloud secret reference")
	}
	if !strings.Contains(text, "file_path_hash_sha256") {
		t.Fatal("expected package to use file path hashes for product map summaries")
	}
}

func TestCanonicalDigestIsStableForEquivalentMaps(t *testing.T) {
	left := map[string]any{"b": 2, "a": map[string]any{"z": true, "m": "value"}}
	right := map[string]any{"a": map[string]any{"m": "value", "z": true}, "b": 2}
	leftDigest, err := DigestCanonicalJSON(left)
	if err != nil {
		t.Fatal(err)
	}
	rightDigest, err := DigestCanonicalJSON(right)
	if err != nil {
		t.Fatal(err)
	}
	if leftDigest != rightDigest {
		t.Fatalf("digest mismatch: %s != %s", leftDigest, rightDigest)
	}
}

func TestExchangeEnvelopeValidationCatchesDigestSignatureExpiryAndReplay(t *testing.T) {
	pkg := sampleClientExecutionPackage(t)
	now := time.Date(2026, 7, 8, 9, 0, 0, 0, time.UTC)
	envelope := sampleEnvelopeForPayload(t, pkg, now)
	seen := map[string]bool{}
	if err := envelope.ValidateForPayload(pkg, now, seen, staticSignatureVerifier(true)); err != nil {
		t.Fatal(err)
	}
	if err := envelope.ValidateForPayload(pkg, now, seen, staticSignatureVerifier(true)); err == nil {
		t.Fatal("expected replayed nonce to be rejected")
	}

	badDigest := envelope
	badDigest.Crypto.Nonce = "nonce_bad_digest"
	badDigest.Crypto.PayloadDigestSHA256 = "not-the-payload-digest"
	if err := badDigest.ValidateForPayload(pkg, now, map[string]bool{}, staticSignatureVerifier(true)); err == nil {
		t.Fatal("expected payload digest mismatch")
	}

	badSignature := envelope
	badSignature.Crypto.Nonce = "nonce_bad_signature"
	if err := badSignature.ValidateForPayload(pkg, now, map[string]bool{}, staticSignatureVerifier(false)); err == nil {
		t.Fatal("expected bad signature to be rejected")
	}

	expired := envelope
	expired.Crypto.Nonce = "nonce_expired"
	expired.ExpiresAt = now.Add(-time.Minute)
	if err := expired.ValidateForPayload(pkg, now, map[string]bool{}, staticSignatureVerifier(true)); err == nil {
		t.Fatal("expected expired envelope to be rejected")
	}
}

func TestRecordingResultPackageJSONRoundTrip(t *testing.T) {
	now := time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC)
	result := RecordingResultPackage{
		ResultID:        "result_1",
		SourcePackageID: "pkg_1",
		CloudJobID:      "job_1",
		SchemaVersion:   RecordingResultPackageSchemaVersion,
		Status:          RecordingResultStatusGenerated,
		ExecutionTrace: &ExecutionTrace{
			ID:              "trace_1",
			WorkflowGraphID: "graph_1",
			GraphVersion:    1,
			PassRate:        1,
		},
		StepResults: []StepResult{{NodeID: "node_open_dashboard", Status: "passed", DurationMS: 1200}},
		GeneratedAssets: []ArtifactRef{{
			ID:        "asset_video",
			Kind:      string(AssetKindDemoVideo),
			URI:       "s3://cascade-results/demo.mp4.enc",
			SHA256:    "sha_video",
			MimeType:  "video/mp4",
			Sensitive: true,
		}},
		VerificationReport: VerificationReport{
			PassRate:             1,
			ReproducibilityMatch: true,
			OutputChecksums:      []ContentDigest{{ID: "asset_video", SHA256: "sha_video"}},
		},
		AuditTrail: CloudExecutionAuditTrail{
			CloudWorkerID:       "worker_1",
			StartedAt:           now,
			CompletedAt:         now.Add(time.Minute),
			RuntimeVersions:     map[string]string{"browser": "chromium-stable"},
			SourcePackageDigest: "sha_pkg",
			GraphDigest:         "sha_graph",
		},
		Delivery: ResultDelivery{
			ResultPackageRef: PackageArtifactDescriptor{
				ID:        "result_package_artifact",
				Kind:      "recording_result_package",
				URI:       "s3://cascade-results/result.json.enc",
				SHA256:    "sha_result",
				Encrypted: true,
			},
			AckRequired: true,
			ExpiresAt:   now.Add(24 * time.Hour),
		},
		CreatedAt: now,
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var got RecordingResultPackage
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.SchemaVersion != RecordingResultPackageSchemaVersion || got.Delivery.ResultPackageRef.URI == "" {
		t.Fatalf("result package did not round-trip: %+v", got)
	}
}

type staticSignatureVerifier bool

func (v staticSignatureVerifier) VerifyExchangeSignature(envelope *ExchangeEnvelope, canonicalPayload []byte) bool {
	return bool(v)
}

func sampleEnvelopeForPayload(t *testing.T, pkg ClientExecutionPackage, now time.Time) ExchangeEnvelope {
	t.Helper()
	digest, err := DigestCanonicalJSON(pkg)
	if err != nil {
		t.Fatal(err)
	}
	return ExchangeEnvelope{
		EnvelopeID:           "env_1",
		OrgID:                pkg.OrgID,
		ProjectID:            pkg.ProjectID,
		PackageKind:          ExchangePackageKindClientExecution,
		SchemaVersion:        ExchangeEnvelopeSchemaVersion,
		PayloadSchemaVersion: ClientExecutionPackageSchemaVersion,
		IdempotencyKey:       "idem_1",
		CreatedAt:            now,
		ExpiresAt:            now.Add(time.Hour),
		Producer:             ExchangeProducer{AppVersion: "0.1.0", InstallID: "install_1", OS: "windows", RuntimeProfile: "desktop"},
		Crypto: ExchangeCrypto{
			ServerKeyID:          "srv_key_1",
			ContentEncryptionAlg: "xchacha20-poly1305",
			KeyEncryptionAlg:     "x25519-hkdf-sha256",
			CompressionAlg:       "zstd",
			PayloadDigestAlg:     "sha256",
			PayloadDigestSHA256:  digest,
			SignatureAlg:         "ed25519",
			SignatureKeyID:       "install_signing_key_1",
			Signature:            "signature_bytes_base64",
			Nonce:                "nonce_1",
		},
		PayloadRef: EncryptedPayloadRef{
			Kind:             "inline",
			InlineCiphertext: "encrypted_payload",
			SHA256:           "encrypted_payload_sha",
			SizeBytes:        512,
		},
		Policy: ExchangePackagePolicy{
			ReplayProtection:       true,
			DeletePayloadAfterRun:  true,
			HumanApprovalRequired:  true,
			StructureSummaryOnly:   true,
			RequiredIPAllowlistAck: true,
			MaxExecutionWindowSec:  900,
		},
	}
}

func sampleClientExecutionPackage(t *testing.T) ClientExecutionPackage {
	t.Helper()
	now := time.Date(2026, 7, 8, 8, 0, 0, 0, time.UTC)
	graph := NewDemoWorkflowGraph("graph_1", "project_1", "https://app.example.com/dashboard")
	graph.Status = GraphStatusApproved
	graph.Nodes = []*GraphNode{
		{
			ID:              "node_open_dashboard",
			Type:            GraphNodeTypeAction,
			Title:           "Open dashboard",
			ExpectedOutcome: "Dashboard loads",
			ActionSpec: &GraphAction{
				Type:   GraphActionNavigate,
				Target: ActionTarget{URL: "https://app.example.com/dashboard"},
			},
			Capture: &CaptureSpec{Screenshot: true, Video: true, MaskSelectors: []string{"[data-sensitive]"}},
		},
		{
			ID:              "node_invite_member",
			Type:            GraphNodeTypeAction,
			Title:           "Invite teammate",
			ExpectedOutcome: "Invite modal appears",
			ActionSpec: &GraphAction{
				Type:   GraphActionClick,
				Target: ActionTarget{TestID: "invite-member"},
			},
			DurationHintMS: 1800,
		},
	}
	graph.Edges = []*GraphEdge{{ID: "edge_1", FromNode: "node_open_dashboard", ToNode: "node_invite_member"}}

	graphDigest, err := DigestCanonicalJSON(graph)
	if err != nil {
		t.Fatal(err)
	}

	return ClientExecutionPackage{
		PackageID:     "pkg_1",
		OrgID:         "org_1",
		ProjectID:     "project_1",
		SchemaVersion: ClientExecutionPackageSchemaVersion,
		CreatedAt:     now,
		ApprovedAt:    now.Add(time.Minute),
		ProjectContextSummary: ProjectContextSummary{
			ContextID:      "ctx_1",
			SchemaVersion:  ProjectContextSchemaVersion,
			Mode:           AppModeDesktop,
			Name:           "Team collaboration launch",
			ProductURL:     "https://app.example.com",
			TargetAudience: "sales",
			UseCases:       []DemoUseCase{DemoUseCaseLaunch, DemoUseCaseSales},
			AccessPolicy: &AccessPolicy{
				CredentialVaultRequired: true,
				SessionIsolation:        true,
				AutoExpireCredentials:   true,
				AllowedDomains:          []string{"app.example.com"},
				AuditLogRequired:        true,
			},
			SecurityPolicy: &SecurityPolicy{
				ForbiddenPages: []string{"/billing"},
				ForbiddenData:  []string{"customer_email", "api_key"},
				PIIHandling:    "mask_in_artifacts",
			},
			InputFingerprints: map[string]string{"requirements": "sha_req", "source_tree": "sha_tree"},
		},
		ProductMapSummary: ProductMapSummary{
			ProductMapID: "map_1",
			Version:      1,
			Summary:      "Team collaboration paths",
			Routes:       []*RouteMapNode{{ID: "route_dashboard", Path: "/dashboard", AuthRequired: true}},
			Components: []ComponentSummary{{
				ID:                 "component_invite",
				Name:               "InviteButton",
				Kind:               "button",
				FilePathHashSHA256: "sha_path_invite",
				SelectorCount:      2,
				ActionCount:        1,
			}},
			DataModels: []DataModelSummary{{
				ID:                   "model_team",
				Name:                 "Team",
				Kind:                 "entity",
				SourcePathHashSHA256: "sha_path_team",
				Fields:               []DataField{{Name: "id", Type: "string", Required: true}},
			}},
		},
		WorkflowGraph: graph,
		RecordingRunSpec: RecordingRunSpec{
			RunID:               "run_1",
			BaseURL:             "https://app.example.com",
			AllowedDomains:      []string{"app.example.com"},
			RequiredIPAllowlist: []string{"203.0.113.10"},
			AuthFlowRef:         "grant_demo_login",
			Timezone:            "UTC",
			Locale:              "en-US",
			Browser: BrowserRunSpec{
				Engine:        "chromium",
				VersionPolicy: "stable-pinned",
				Headless:      true,
				Viewports:     []ViewportSpec{{Name: "desktop", Width: 1440, Height: 900, Device: "desktop"}},
			},
			Timeline: RecordingTimeline{
				TargetDurationSec: 60,
				MaxDurationSec:    90,
				CaptureWindows: []CaptureWindow{{
					ID:         "capture_1",
					NodeID:     "node_open_dashboard",
					StartMS:    0,
					DurationMS: 5000,
					Role:       "opening",
				}},
				NodeTimingHints: []NodeTimingHint{{NodeID: "node_invite_member", DurationMS: 1800}},
			},
			Outputs: RecordingOutputRequest{
				RawRecording:     true,
				FinalVideo:       true,
				ScreenshotPack:   true,
				StepByStepDocs:   true,
				Trace:            true,
				OutputFormats:    []string{"mp4", "markdown", "png"},
				ResolutionWidth:  1920,
				ResolutionHeight: 1080,
			},
			Redactions: RedactionPolicy{
				MaskSelectors:        []string{"[data-sensitive]"},
				TextPatterns:         []string{"[A-Z0-9._%+-]+@[A-Z0-9.-]+"},
				VideoMaskPolicy:      "mask_before_persist",
				ScreenshotMaskPolicy: "mask_before_persist",
			},
			FailurePolicy: RecordingFailurePolicy{
				RetryAttempts:             2,
				SelectorRepairAllowed:     true,
				DataRepairAllowed:         false,
				MaxRepairAttempts:         1,
				HumanEscalationConditions: []string{"auth_failed", "forbidden_page_detected"},
			},
		},
		CredentialGrants: []CredentialGrant{{
			GrantID:                  "grant_demo_login",
			Kind:                     "demo_account",
			Purpose:                  "cloud_recording_login",
			Scope:                    "recording_session",
			CloudSecretRef:           "vault://cloud/grants/grant_demo_login",
			AllowedDomains:           []string{"app.example.com"},
			AllowedOperations:        []string{"login", "record_demo"},
			RotationRequiredAfterRun: true,
			DeleteAfterRun:           true,
			ExpiresAt:                now.Add(time.Hour),
		}},
		EvidenceBundle: EvidenceBundle{
			EvidenceRefs: []EvidenceRef{{ID: "ev_route_summary", Kind: EvidenceKindSourceCode, Summary: "Route and component summary"}},
			SourceTrees: []SourceTreeDigest{{
				RepositoryID:     "repo_1",
				Branch:           "main",
				CommitSHA:        "abc123",
				RootDigestSHA256: "sha_tree",
				FileCount:        128,
				PathDigests:      []PathDigest{{PathHashSHA256: "sha_path_invite", ContentSHA256: "sha_component", Language: "tsx", Entrypoint: true}},
			}},
			RequirementDigests: []ContentDigest{{ID: "req_1", Kind: "markdown", Title: "Launch requirements", SHA256: "sha_req"}},
			ArtifactDescriptors: []PackageArtifactDescriptor{{
				ID:        "screenshot_1",
				Role:      "evidence",
				Kind:      "webpage_screenshot",
				URI:       "file://local/redacted/screenshot.png.enc",
				MimeType:  "image/png",
				SHA256:    "sha_screenshot",
				Encrypted: true,
				Sensitive: true,
			}},
		},
		Reproducibility: ReproducibilitySpec{
			GraphHashSHA256:       graphDigest,
			InputFingerprints:     map[string]string{"source_tree": "sha_tree", "requirements": "sha_req"},
			BrowserRuntimePins:    map[string]string{"chromium": "stable-2026-07"},
			SourceSnapshotDigest:  "sha_tree",
			DeterministicSeed:     "seed_1",
			CreatedWithAppVersion: "0.1.0",
		},
		SafetyReport: PackageSafetyReport{
			AllowedToUpload:    true,
			UploadMode:         "structure_summary_only",
			ForbiddenPages:     []string{"/billing"},
			ForbiddenData:      []string{"customer_email", "api_key"},
			RedactionSelectors: []string{"[data-sensitive]"},
			PIIHandling:        "mask_in_artifacts",
			HumanApproval: UserApprovalRecord{
				ApprovalID:       "approval_1",
				ApprovedByUserID: "user_1",
				ApprovedAt:       now.Add(time.Minute),
				PlanDigestSHA256: graphDigest,
				ReviewedNodeIDs:  []string{"node_open_dashboard", "node_invite_member"},
			},
		},
	}
}
