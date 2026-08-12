package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/model"
)

const browserAgentTestWaiverSchemaVersion = "demoops.browser_agent_test_waiver.v1"

// DevAppPackageWaiverRequest identifies an App-generated draft already held by
// the local Server. It deliberately cannot carry package JSON or actions.
type DevAppPackageWaiverRequest struct {
	ProjectID                    string   `json:"project_id"`
	PackageID                    string   `json:"package_id"`
	ExpectedBundleHashSHA256     string   `json:"expected_bundle_hash_sha256"`
	ExpectedPlanHashSHA256       string   `json:"expected_plan_hash_sha256"`
	ApprovedNodeIDs              []string `json:"approved_node_ids"`
	ApprovedBlockingReasonHashes []string `json:"approved_blocking_reason_hashes,omitempty"`
	DevTestAck                   bool     `json:"dev_test_ack"`
}

// DevAppPackageRawWaiverRequest binds a waiver to an App-produced package
// file already present on the local workstation. The file is read once,
// validated, and retained only in memory; its JSON is never rewritten.
type DevAppPackageRawWaiverRequest struct {
	PackageFile                  string   `json:"package_file"`
	ExpectedPackageID            string   `json:"package_id"`
	ExpectedBundleHashSHA256     string   `json:"expected_bundle_hash_sha256"`
	ExpectedPlanHashSHA256       string   `json:"expected_plan_hash_sha256"`
	ApprovedNodeIDs              []string `json:"approved_node_ids"`
	ApprovedBlockingReasonHashes []string `json:"approved_blocking_reason_hashes,omitempty"`
	DevTestAck                   bool     `json:"dev_test_ack"`
}

type DevAppPackageWaiverRunRequest struct {
	SessionID  string `json:"session_id"`
	DevTestAck bool   `json:"dev_test_ack"`
}

type BrowserAgentTestWaiverNode struct {
	NodeID           string                `json:"node_id"`
	StageID          string                `json:"stage_id"`
	InteractionIndex int                   `json:"interaction_index"`
	ActionType       model.GraphActionType `json:"action_type"`
	SemanticID       string                `json:"semantic_id"`
}

// BrowserAgentTestWaiver is a short-lived, Server-issued in-memory capability.
// It never changes the App package and is never accepted by Exchange.
type BrowserAgentTestWaiver struct {
	SchemaVersion                string                       `json:"schema_version"`
	WaiverID                     string                       `json:"waiver_id"`
	ProjectID                    string                       `json:"project_id"`
	PackageID                    string                       `json:"package_id"`
	BundleHashSHA256             string                       `json:"bundle_hash_sha256"`
	PlanHashSHA256               string                       `json:"plan_hash_sha256"`
	OriginalPackageDigest        string                       `json:"original_package_digest_sha256"`
	RawPackageSHA256             string                       `json:"raw_package_sha256,omitempty"`
	ApprovalSubjectDigest        string                       `json:"approval_subject_digest_sha256"`
	AllowedOrigin                string                       `json:"allowed_origin"`
	AllowedNodes                 []BrowserAgentTestWaiverNode `json:"allowed_nodes"`
	ApprovedBlockingReasonHashes []string                     `json:"approved_blocking_reason_hashes,omitempty"`
	BlockedReasons               []string                     `json:"blocked_reasons,omitempty"`
	DevTestOnly                  bool                         `json:"dev_test_only"`
	NotForExchangeUpload         bool                         `json:"not_for_exchange_upload"`
	TestOnlyWaiver               bool                         `json:"test_only_waiver"`
	FormalExchange               bool                         `json:"formal_exchange"`
	AppGenerated                 bool                         `json:"app_generated"`
	TransportAuthenticated       bool                         `json:"transport_authenticated"`
	IssuedAt                     time.Time                    `json:"issued_at"`
	ExpiresAt                    time.Time                    `json:"expires_at"`
	Status                       string                       `json:"status"`
	AuditLogPath                 string                       `json:"audit_log_path,omitempty"`
}

type devAppPackageTestWaiverRecord struct {
	view         BrowserAgentTestWaiver
	packageValue model.ClientExecutionPackage
	rawFile      string
}

type devAppPackageTestWaiverManager struct {
	service *Service
	mu      sync.Mutex
	records map[string]devAppPackageTestWaiverRecord
}

func newDevAppPackageTestWaiverManager(service *Service) *devAppPackageTestWaiverManager {
	return &devAppPackageTestWaiverManager{service: service, records: map[string]devAppPackageTestWaiverRecord{}}
}

func (m *devAppPackageTestWaiverManager) localOnlyGuard(ack bool) error {
	if m == nil || m.service == nil || m.service.runtime.Profile != config.ProfileDev || strings.EqualFold(m.service.runtime.Environment, "production") {
		return errors.New("app-package test waiver is available only in a local dev/test runtime")
	}
	if !ack {
		return errors.New("dev_test_ack=true is required for the local app-package test waiver")
	}
	return nil
}

func (m *devAppPackageTestWaiverManager) Issue(ctx context.Context, request DevAppPackageWaiverRequest) (BrowserAgentTestWaiver, error) {
	if err := m.localOnlyGuard(request.DevTestAck); err != nil {
		return BrowserAgentTestWaiver{}, err
	}
	request.ProjectID, request.PackageID = strings.TrimSpace(request.ProjectID), strings.TrimSpace(request.PackageID)
	request.ExpectedBundleHashSHA256 = strings.TrimSpace(request.ExpectedBundleHashSHA256)
	request.ExpectedPlanHashSHA256 = strings.TrimSpace(request.ExpectedPlanHashSHA256)
	if request.ProjectID == "" || request.PackageID == "" || request.ExpectedBundleHashSHA256 == "" || request.ExpectedPlanHashSHA256 == "" || len(request.ApprovedNodeIDs) == 0 {
		return BrowserAgentTestWaiver{}, errors.New("project_id, package_id, both expected hashes, and approved_node_ids are required")
	}
	build, err := m.service.BuildClientExecutionPackage(ctx, request.ProjectID, defaultDesktopOrgID)
	if err != nil {
		return BrowserAgentTestWaiver{}, fmt.Errorf("rebuild App draft from local project state: %w", err)
	}
	// Package preflight synchronizes readiness findings into local App state.
	// Rebuild once after that synchronization so the waiver captures the same
	// stable approval subject that a subsequent formal App export will expose.
	stabilized, err := m.service.BuildClientExecutionPackage(ctx, request.ProjectID, defaultDesktopOrgID)
	if err != nil {
		return BrowserAgentTestWaiver{}, fmt.Errorf("stabilize App draft after local preflight: %w", err)
	}
	if !sameAppDraftExecutionIdentity(build.Package, stabilized.Package) {
		return BrowserAgentTestWaiver{}, errors.New("App draft execution identity changed while issuing the test waiver")
	}
	build = stabilized
	pkg := build.Package
	bundle := pkg.ExecutableScriptBundle
	if bundle == nil || bundle.ScriptManifest.Runtime != model.ExecutableScriptRuntimeBrowserAgentOutlineV1 {
		return BrowserAgentTestWaiver{}, errors.New("test waiver requires an App-generated browser-agent-outline-v1 draft")
	}
	if err := verifyAppDraftProtocolHashes(bundle); err != nil {
		return BrowserAgentTestWaiver{}, err
	}
	if strings.TrimSpace(request.ExpectedBundleHashSHA256) == "" || strings.TrimSpace(request.ExpectedPlanHashSHA256) == "" {
		return BrowserAgentTestWaiver{}, errors.New("expected bundle and plan hashes are required")
	}
	if pkg.PackageID != request.PackageID || bundle.Reproducibility.BundleHashSHA256 != request.ExpectedBundleHashSHA256 || bundle.Reproducibility.PlanHashSHA256 != request.ExpectedPlanHashSHA256 {
		return BrowserAgentTestWaiver{}, errors.New("project/package identity or expected bundle/plan hash does not match the Server-rebuilt App draft")
	}
	plan, err := compileBrowserAgentRuntimePlan(&pkg)
	if err != nil {
		return BrowserAgentTestWaiver{}, fmt.Errorf("compile unchanged App draft: %w", err)
	}
	target, err := devVisibleTargetURL(pkg.RecordingRunSpec.BaseURL)
	if err != nil {
		return BrowserAgentTestWaiver{}, fmt.Errorf("App draft is outside the local visible acceptance target: %w", err)
	}
	requested := map[string]bool{}
	for _, nodeID := range request.ApprovedNodeIDs {
		nodeID = strings.TrimSpace(nodeID)
		if nodeID == "" || requested[nodeID] {
			return BrowserAgentTestWaiver{}, errors.New("approved_node_ids must contain unique non-empty node IDs")
		}
		requested[nodeID] = true
	}
	allowed := make([]BrowserAgentTestWaiverNode, 0, len(requested))
	for _, stage := range plan.Stages {
		if !requested[stage.NodeID] {
			continue
		}
		if stage.TargetContract.Destructive {
			return BrowserAgentTestWaiver{}, fmt.Errorf("node %s is explicitly destructive and can never receive a test waiver", stage.NodeID)
		}
		foundUnclassified := false
		for interactionIndex, interaction := range stage.Interactions {
			if interaction.NonDestructive {
				continue
			}
			foundUnclassified = true
			allowed = append(allowed, BrowserAgentTestWaiverNode{NodeID: stage.NodeID, StageID: stage.ID, InteractionIndex: interactionIndex, ActionType: interaction.Kind, SemanticID: stage.TargetContract.SemanticID})
		}
		if !foundUnclassified {
			return BrowserAgentTestWaiver{}, fmt.Errorf("node %s has no missing non-destructive classification to waive", stage.NodeID)
		}
		delete(requested, stage.NodeID)
	}
	if len(requested) != 0 {
		return BrowserAgentTestWaiver{}, fmt.Errorf("approved_node_ids contains nodes not present in the compiled App draft: %v", mapKeys(requested))
	}
	if len(allowed) == 0 {
		return BrowserAgentTestWaiver{}, errors.New("approved_node_ids does not identify an unclassified non-destructive action")
	}
	approvedReasonHashes, err := validateWaiverTargetsAgainstBlockingReport(pkg, allowed, request.ApprovedBlockingReasonHashes)
	if err != nil {
		return BrowserAgentTestWaiver{}, err
	}
	originalDigest, err := model.DigestCanonicalJSON(pkg)
	if err != nil {
		return BrowserAgentTestWaiver{}, err
	}
	now := timeNowUTC()
	waiverID := fmt.Sprintf("test_waiver_%d", now.UnixNano())
	auditDir := filepath.Join(m.service.runtime.ArtifactRoot, "dev-test-only", "app-package-waiver", safePathSegment(waiverID))
	view := BrowserAgentTestWaiver{
		SchemaVersion: browserAgentTestWaiverSchemaVersion, WaiverID: waiverID,
		ProjectID: pkg.ProjectID, PackageID: pkg.PackageID,
		BundleHashSHA256: bundle.Reproducibility.BundleHashSHA256, PlanHashSHA256: bundle.Reproducibility.PlanHashSHA256,
		OriginalPackageDigest: originalDigest, ApprovalSubjectDigest: build.ApprovalSubjectDigestSHA256,
		AllowedOrigin: devVisibleOrigin(target), AllowedNodes: allowed, ApprovedBlockingReasonHashes: approvedReasonHashes,
		BlockedReasons: packageBlockingReasons(pkg), DevTestOnly: true, NotForExchangeUpload: true, TestOnlyWaiver: true,
		FormalExchange: false, AppGenerated: true, TransportAuthenticated: false,
		IssuedAt: now, ExpiresAt: now.Add(15 * time.Minute), Status: "issued",
		AuditLogPath: filepath.Join(auditDir, "test-waiver-audit.jsonl"),
	}
	if err := appendBrowserAgentTestWaiverAudit(view.AuditLogPath, map[string]any{"event": "waiver_issued", "waiver": view, "occurred_at": now}); err != nil {
		return BrowserAgentTestWaiver{}, err
	}
	m.mu.Lock()
	m.records[waiverID] = devAppPackageTestWaiverRecord{view: view, packageValue: pkg}
	m.mu.Unlock()
	return view, nil
}

// IssueFromRawFile is the strict local path for replaying the exact App
// package exported to disk. Unlike Issue, it never rebuilds package state from
// a project. This is deliberately dev/test-only and loopback-only at HTTP.
func (m *devAppPackageTestWaiverManager) IssueFromRawFile(ctx context.Context, request DevAppPackageRawWaiverRequest) (BrowserAgentTestWaiver, error) {
	if err := m.localOnlyGuard(request.DevTestAck); err != nil {
		return BrowserAgentTestWaiver{}, err
	}
	path, err := m.validateRawPackagePath(request.PackageFile)
	if err != nil {
		return BrowserAgentTestWaiver{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return BrowserAgentTestWaiver{}, fmt.Errorf("read App execution package file: %w", err)
	}
	var pkg model.ClientExecutionPackage
	if err := json.Unmarshal(data, &pkg); err != nil {
		return BrowserAgentTestWaiver{}, fmt.Errorf("decode App execution package file: %w", err)
	}
	if err := model.ValidateClientExecutionPackageForLocalTestWaiver(&pkg); err != nil {
		return BrowserAgentTestWaiver{}, fmt.Errorf("raw App package validation failed: %w", err)
	}
	bundle := pkg.ExecutableScriptBundle
	if bundle == nil || bundle.ScriptManifest.Runtime != model.ExecutableScriptRuntimeBrowserAgentOutlineV1 {
		return BrowserAgentTestWaiver{}, errors.New("raw App package must use browser-agent-outline-v1")
	}
	if err := verifyAppDraftProtocolHashes(bundle); err != nil {
		return BrowserAgentTestWaiver{}, err
	}
	if strings.TrimSpace(request.ExpectedPackageID) != "" && pkg.PackageID != strings.TrimSpace(request.ExpectedPackageID) {
		return BrowserAgentTestWaiver{}, errors.New("raw App package_id does not match the requested identity")
	}
	if bundle.Reproducibility.BundleHashSHA256 != strings.TrimSpace(request.ExpectedBundleHashSHA256) || bundle.Reproducibility.PlanHashSHA256 != strings.TrimSpace(request.ExpectedPlanHashSHA256) {
		return BrowserAgentTestWaiver{}, errors.New("raw App package bundle/plan hash does not match the requested identity")
	}
	if len(request.ApprovedNodeIDs) == 0 {
		return BrowserAgentTestWaiver{}, errors.New("approved_node_ids are required")
	}
	plan, err := compileBrowserAgentRuntimePlan(&pkg)
	if err != nil {
		return BrowserAgentTestWaiver{}, fmt.Errorf("compile unchanged raw App package: %w", err)
	}
	target, err := devVisibleTargetURL(pkg.RecordingRunSpec.BaseURL)
	if err != nil {
		return BrowserAgentTestWaiver{}, fmt.Errorf("raw App package is outside the local visible acceptance target: %w", err)
	}
	productURL, err := url.Parse(strings.TrimSpace(pkg.ProjectContextSummary.ProductURL))
	if err != nil || devVisibleOrigin(productURL) != devVisibleOrigin(target) {
		return BrowserAgentTestWaiver{}, errors.New("raw App package product_url does not match recording_run_spec.base_url")
	}
	visibleOrigin := devVisibleOrigin(target)
	if !containsExactString(plan.ExplorationScope.AllowedOrigins, visibleOrigin) {
		return BrowserAgentTestWaiver{}, errors.New("raw App package allowed_origins must include its local visible origin")
	}
	// App planning may preserve an HTTPS candidate for the same loopback
	// host/port even when the actual product is served over HTTP. The raw
	// package remains unchanged; this test-only waiver accepts that narrowly
	// equivalent local alias and the runtime policy below still pins the Worker
	// to visibleOrigin. Any different host, port, path, or scheme is rejected.
	if !localVisibleOriginAliasesOnly(plan.ExplorationScope.AllowedOrigins, target) {
		return BrowserAgentTestWaiver{}, errors.New("raw App package allowed_origins must be limited to the local visible origin or its same loopback scheme alias")
	}
	if !localVisibleHostAllowed(pkg.RecordingRunSpec.AllowedDomains, target) || !localVisibleHostAllowed(bundle.SecurityPolicy.AllowedDomains, target) {
		return BrowserAgentTestWaiver{}, errors.New("raw App package allowed_domains must include the local visible host")
	}
	requested := map[string]bool{}
	for _, nodeID := range request.ApprovedNodeIDs {
		nodeID = strings.TrimSpace(nodeID)
		if nodeID == "" || requested[nodeID] {
			return BrowserAgentTestWaiver{}, errors.New("approved_node_ids must contain unique non-empty node IDs")
		}
		requested[nodeID] = true
	}
	allowed := make([]BrowserAgentTestWaiverNode, 0, len(requested))
	for _, stage := range plan.Stages {
		if !requested[stage.NodeID] {
			continue
		}
		if stage.TargetContract.Destructive {
			return BrowserAgentTestWaiver{}, fmt.Errorf("node %s is explicitly destructive and can never receive a test waiver", stage.NodeID)
		}
		found := false
		for index, interaction := range stage.Interactions {
			if interaction.NonDestructive {
				continue
			}
			allowed = append(allowed, BrowserAgentTestWaiverNode{NodeID: stage.NodeID, StageID: stage.ID, InteractionIndex: index, ActionType: interaction.Kind, SemanticID: stage.TargetContract.SemanticID})
			found = true
		}
		if !found {
			return BrowserAgentTestWaiver{}, fmt.Errorf("node %s has no missing non-destructive classification to waive", stage.NodeID)
		}
		delete(requested, stage.NodeID)
	}
	if len(requested) != 0 {
		return BrowserAgentTestWaiver{}, fmt.Errorf("approved_node_ids contains nodes not present in the raw App package: %v", mapKeys(requested))
	}
	approvedReasonHashes, err := validateWaiverTargetsAgainstBlockingReport(pkg, allowed, request.ApprovedBlockingReasonHashes)
	if err != nil {
		return BrowserAgentTestWaiver{}, err
	}
	digest, err := model.DigestCanonicalJSON(pkg)
	if err != nil {
		return BrowserAgentTestWaiver{}, err
	}
	now := timeNowUTC()
	waiverID := fmt.Sprintf("test_waiver_%d", now.UnixNano())
	auditDir := filepath.Join(m.service.runtime.ArtifactRoot, "dev-test-only", "app-package-waiver", safePathSegment(waiverID))
	rawHash := fmt.Sprintf("%x", sha256.Sum256(data))
	view := BrowserAgentTestWaiver{SchemaVersion: browserAgentTestWaiverSchemaVersion, WaiverID: waiverID, ProjectID: pkg.ProjectID, PackageID: pkg.PackageID, BundleHashSHA256: bundle.Reproducibility.BundleHashSHA256, PlanHashSHA256: bundle.Reproducibility.PlanHashSHA256, OriginalPackageDigest: digest, RawPackageSHA256: rawHash, AllowedOrigin: devVisibleOrigin(target), AllowedNodes: allowed, ApprovedBlockingReasonHashes: approvedReasonHashes, BlockedReasons: packageBlockingReasons(pkg), DevTestOnly: true, NotForExchangeUpload: true, TestOnlyWaiver: true, FormalExchange: false, AppGenerated: true, TransportAuthenticated: false, IssuedAt: now, ExpiresAt: now.Add(15 * time.Minute), Status: "issued", AuditLogPath: filepath.Join(auditDir, "test-waiver-audit.jsonl")}
	if err := appendBrowserAgentTestWaiverAudit(view.AuditLogPath, map[string]any{"event": "waiver_issued_from_raw_app_package", "package_file": path, "waiver": view, "occurred_at": now}); err != nil {
		return BrowserAgentTestWaiver{}, err
	}
	m.mu.Lock()
	m.records[waiverID] = devAppPackageTestWaiverRecord{view: view, packageValue: pkg, rawFile: path}
	m.mu.Unlock()
	_ = ctx
	return view, nil
}

func (m *devAppPackageTestWaiverManager) validateRawPackagePath(value string) (string, error) {
	path := filepath.Clean(strings.TrimSpace(value))
	if path == "." || !filepath.IsAbs(path) {
		return "", errors.New("package_file must be an absolute local path")
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return "", errors.New("package_file must point to an existing JSON file")
	}
	root := filepath.Clean(m.service.runtime.DevRepoRoot)
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("package_file must be inside the local Engine workspace")
	}
	if strings.ToLower(filepath.Ext(path)) != ".json" {
		return "", errors.New("package_file must use the .json extension")
	}
	return path, nil
}

func localVisibleOriginAliasesOnly(origins []string, target *url.URL) bool {
	if target == nil || target.Hostname() == "" || target.Port() == "" {
		return false
	}
	for _, raw := range origins {
		parsed, err := url.Parse(strings.TrimSpace(raw))
		if err != nil || parsed.Scheme == "" || parsed.Hostname() != target.Hostname() || parsed.Port() != target.Port() || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil {
			return false
		}
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return false
		}
		if !isLoopbackHost(parsed.Hostname()) {
			return false
		}
	}
	return true
}

func isLoopbackHost(host string) bool {
	host = strings.Trim(strings.ToLower(strings.TrimSpace(host)), "[]")
	return host == "127.0.0.1" || host == "localhost" || host == "::1"
}

func localVisibleHostAllowed(values []string, target *url.URL) bool {
	if target == nil || !isLoopbackHost(target.Hostname()) {
		return false
	}
	for _, value := range values {
		candidate := strings.TrimSpace(value)
		if strings.EqualFold(candidate, target.Host) || strings.EqualFold(candidate, target.Hostname()) {
			return true
		}
	}
	return false
}

// applyTestOnlyWaiverRuntimeClassifications supplies the one missing safety
// classification to a cloned in-memory runtime plan. It is deliberately more
// restrictive than the policy guard: every waiver tuple must identify one
// exact interaction, and an explicitly destructive target can never be
// reclassified. The App package, its compiled formal plan and both protocol
// hashes remain unchanged.
func applyTestOnlyWaiverRuntimeClassifications(plan BrowserAgentRuntimePlan, waiver BrowserAgentTestWaiver) (BrowserAgentRuntimePlan, []BrowserAgentTestWaiverNode, error) {
	if !waiver.DevTestOnly || !waiver.NotForExchangeUpload || !waiver.TestOnlyWaiver || waiver.FormalExchange || timeNowUTC().After(waiver.ExpiresAt) {
		return BrowserAgentRuntimePlan{}, nil, errors.New("invalid or expired local app-package test waiver")
	}
	if plan.SourcePackageID != waiver.PackageID || plan.SourceBundleHashSHA256 != waiver.BundleHashSHA256 || plan.SourcePlanHashSHA256 != waiver.PlanHashSHA256 {
		return BrowserAgentRuntimePlan{}, nil, errors.New("test waiver is not bound to the runtime package and bundle hash")
	}
	if len(waiver.AllowedNodes) == 0 {
		return BrowserAgentRuntimePlan{}, nil, errors.New("test waiver has no node-scoped classifications")
	}

	runtimeCopy := plan
	runtimeCopy.Stages = append([]BrowserAgentRuntimeStage{}, plan.Stages...)
	for index := range runtimeCopy.Stages {
		runtimeCopy.Stages[index].Interactions = append([]model.BrowserAgentInteraction{}, plan.Stages[index].Interactions...)
	}
	applied := make([]BrowserAgentTestWaiverNode, 0, len(waiver.AllowedNodes))
	seen := map[string]bool{}
	for _, allowed := range waiver.AllowedNodes {
		key := fmt.Sprintf("%s\x00%s\x00%d\x00%s\x00%s", allowed.NodeID, allowed.StageID, allowed.InteractionIndex, allowed.ActionType, allowed.SemanticID)
		if seen[key] {
			return BrowserAgentRuntimePlan{}, nil, errors.New("test waiver contains a duplicate interaction classification")
		}
		seen[key] = true
		stageIndex := -1
		for index := range runtimeCopy.Stages {
			stage := runtimeCopy.Stages[index]
			if stage.NodeID == allowed.NodeID && stage.ID == allowed.StageID {
				if stageIndex != -1 {
					return BrowserAgentRuntimePlan{}, nil, errors.New("runtime plan contains duplicate waiver stage identity")
				}
				stageIndex = index
			}
		}
		if stageIndex < 0 {
			return BrowserAgentRuntimePlan{}, nil, fmt.Errorf("test waiver stage was not found: %s", allowed.NodeID)
		}
		stage := &runtimeCopy.Stages[stageIndex]
		if stage.TargetContract.Destructive {
			return BrowserAgentRuntimePlan{}, nil, fmt.Errorf("test waiver cannot reclassify destructive target: %s", allowed.NodeID)
		}
		if stage.TargetContract.SemanticID != allowed.SemanticID || allowed.InteractionIndex < 0 || allowed.InteractionIndex >= len(stage.Interactions) {
			return BrowserAgentRuntimePlan{}, nil, fmt.Errorf("test waiver interaction identity mismatch: %s", allowed.NodeID)
		}
		interaction := &stage.Interactions[allowed.InteractionIndex]
		if interaction.Kind != allowed.ActionType || interaction.NonDestructive {
			return BrowserAgentRuntimePlan{}, nil, fmt.Errorf("test waiver action identity or missing classification mismatch: %s", allowed.NodeID)
		}
		interaction.NonDestructive = true
		applied = append(applied, allowed)
	}
	return runtimeCopy, applied, nil
}

func (m *devAppPackageTestWaiverManager) Get(waiverID string) (BrowserAgentTestWaiver, error) {
	if err := m.localOnlyGuard(true); err != nil {
		return BrowserAgentTestWaiver{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	record, ok := m.records[strings.TrimSpace(waiverID)]
	if !ok {
		return BrowserAgentTestWaiver{}, errors.New("app-package test waiver was not found")
	}
	if timeNowUTC().After(record.view.ExpiresAt) && record.view.Status == "issued" {
		record.view.Status = "expired"
		m.records[waiverID] = record
	}
	return record.view, nil
}

func (m *devAppPackageTestWaiverManager) Run(ctx context.Context, waiverID string, request DevAppPackageWaiverRunRequest) (DevVisibleBrowserAgentView, error) {
	if err := m.localOnlyGuard(request.DevTestAck); err != nil {
		return DevVisibleBrowserAgentView{}, err
	}
	m.mu.Lock()
	record, ok := m.records[strings.TrimSpace(waiverID)]
	m.mu.Unlock()
	if !ok {
		return DevVisibleBrowserAgentView{}, errors.New("app-package test waiver was not found")
	}
	if record.view.Status != "issued" || timeNowUTC().After(record.view.ExpiresAt) {
		return DevVisibleBrowserAgentView{}, errors.New("app-package test waiver is expired or already consumed")
	}
	if record.rawFile != "" {
		data, readErr := os.ReadFile(record.rawFile)
		if readErr != nil {
			return DevVisibleBrowserAgentView{}, fmt.Errorf("read raw App package before waiver run: %w", readErr)
		}
		if record.view.RawPackageSHA256 != "" && fmt.Sprintf("%x", sha256.Sum256(data)) != record.view.RawPackageSHA256 {
			return DevVisibleBrowserAgentView{}, errors.New("raw App package bytes changed after waiver issuance; issue a new waiver")
		}
		var current model.ClientExecutionPackage
		if decodeErr := json.Unmarshal(data, &current); decodeErr != nil {
			return DevVisibleBrowserAgentView{}, fmt.Errorf("decode raw App package before waiver run: %w", decodeErr)
		}
		currentDigest, digestErr := model.DigestCanonicalJSON(current)
		if digestErr != nil || currentDigest != record.view.OriginalPackageDigest {
			return DevVisibleBrowserAgentView{}, errors.New("raw App package changed after waiver issuance; issue a new waiver")
		}
		if err := verifyAppDraftProtocolHashes(current.ExecutableScriptBundle); err != nil {
			return DevVisibleBrowserAgentView{}, err
		}
	} else {
		// Legacy project-bound local path retained for compatibility with earlier
		// Server tests; it is never used by the raw-package endpoint.
		if record.packageValue.PackageID != record.view.PackageID || record.packageValue.ExecutableScriptBundle == nil {
			return DevVisibleBrowserAgentView{}, errors.New("stored original App draft identity is invalid")
		}
		bundle := record.packageValue.ExecutableScriptBundle
		if bundle.Reproducibility.BundleHashSHA256 != record.view.BundleHashSHA256 || bundle.Reproducibility.PlanHashSHA256 != record.view.PlanHashSHA256 {
			return DevVisibleBrowserAgentView{}, errors.New("stored original App draft hashes changed after waiver issuance")
		}
		if err := verifyAppDraftProtocolHashes(bundle); err != nil {
			return DevVisibleBrowserAgentView{}, err
		}
	}
	digest, err := model.DigestCanonicalJSON(record.packageValue)
	if err != nil || digest != record.view.OriginalPackageDigest {
		return DevVisibleBrowserAgentView{}, errors.New("stored original App draft digest changed after waiver issuance")
	}
	guard := testOnlyWaiverPolicyGuard{waiver: record.view}
	result, runErr := m.service.devVisibleBrowserAgent.ExecuteWaivedPackage(ctx, request.SessionID, record.packageValue, guard, record.view)
	status := "consumed"
	if runErr != nil {
		status = "failed"
	}
	m.mu.Lock()
	record.view.Status = status
	m.records[waiverID] = record
	m.mu.Unlock()
	_ = appendBrowserAgentTestWaiverAudit(record.view.AuditLogPath, map[string]any{
		"event": "waiver_run_finished", "waiver_id": record.view.WaiverID, "package_id": record.view.PackageID,
		"bundle_hash_sha256": record.view.BundleHashSHA256, "plan_hash_sha256": record.view.PlanHashSHA256,
		"allowed_nodes": record.view.AllowedNodes, "status": status, "occurred_at": timeNowUTC(), "error": safeWaiverError(runErr),
	})
	return result, runErr
}

type testOnlyWaiverPolicyGuard struct {
	waiver BrowserAgentTestWaiver
}

func (g testOnlyWaiverPolicyGuard) Authorize(plan BrowserAgentRuntimePlan, intent BrowserAgentActionIntent) BrowserAgentPolicyDecision {
	if target := firstNonEmptyString(intent.URL, intent.Route); target != "" && !testWaiverTargetWithinOrigin(target, g.waiver.AllowedOrigin) {
		return deniedBrowserAgentPolicy("test_waiver_origin_denied", "test waiver target is outside its single local HTTP origin")
	}
	formal := contractBrowserAgentPolicyGuard{}.Authorize(plan, intent)
	if formal.Allowed || formal.Code != "destructive_action_denied" || intent.TargetContract.Destructive {
		return formal
	}
	if !g.waiver.DevTestOnly || !g.waiver.NotForExchangeUpload || !g.waiver.TestOnlyWaiver || g.waiver.FormalExchange || timeNowUTC().After(g.waiver.ExpiresAt) {
		return formal
	}
	if plan.SourcePackageID != g.waiver.PackageID || plan.SourceBundleHashSHA256 != g.waiver.BundleHashSHA256 || plan.SourcePlanHashSHA256 != g.waiver.PlanHashSHA256 {
		return deniedBrowserAgentPolicy("test_waiver_identity_mismatch", "test waiver is not bound to this package, bundle hash and plan hash")
	}
	matched := false
	for _, allowed := range g.waiver.AllowedNodes {
		if allowed.NodeID == intent.NodeID && allowed.StageID == intent.StageID && allowed.InteractionIndex == intent.InteractionIndex && allowed.ActionType == intent.ActionType && allowed.SemanticID == intent.TargetContract.SemanticID {
			matched = true
			break
		}
	}
	if !matched {
		return formal
	}
	// Re-run the formal guard with only the missing classification supplied in
	// memory. Domain/origin/route/forbidden-page checks remain authoritative.
	reclassified := intent
	reclassified.NonDestructive = true
	decision := contractBrowserAgentPolicyGuard{}.Authorize(plan, reclassified)
	if !decision.Allowed {
		return decision
	}
	return BrowserAgentPolicyDecision{Allowed: true, Code: "test_only_waiver", Reason: "Server-local node-scoped test waiver supplied only the missing non-destructive classification"}
}

func sameAppDraftExecutionIdentity(left, right model.ClientExecutionPackage) bool {
	if left.PackageID != right.PackageID || left.ProjectID != right.ProjectID || left.ExecutableScriptBundle == nil || right.ExecutableScriptBundle == nil {
		return false
	}
	return left.ExecutableScriptBundle.Reproducibility.BundleHashSHA256 == right.ExecutableScriptBundle.Reproducibility.BundleHashSHA256 &&
		left.ExecutableScriptBundle.Reproducibility.PlanHashSHA256 == right.ExecutableScriptBundle.Reproducibility.PlanHashSHA256
}

func testWaiverTargetWithinOrigin(value, allowedOrigin string) bool {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "//") || strings.HasPrefix(value, "#") {
		return true
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return false
	}
	return strings.EqualFold(devVisibleOrigin(parsed), strings.TrimSpace(allowedOrigin))
}

func packageBlockingReasons(pkg model.ClientExecutionPackage) []string {
	if pkg.ConfidenceSummary == nil {
		return nil
	}
	return append([]string{}, pkg.ConfidenceSummary.BlockingReasons...)
}

func validateWaiverTargetsAgainstBlockingReport(pkg model.ClientExecutionPackage, allowed []BrowserAgentTestWaiverNode, approvedBlockingReasonHashes []string) ([]string, error) {
	if pkg.ConfidenceSummary == nil || pkg.ConfidenceSummary.Readiness != model.PackageReadinessBlocked || len(pkg.ConfidenceSummary.BlockingReasons) == 0 {
		return nil, errors.New("test waiver requires an App blocking report with explicit blocking reasons")
	}
	approved := make(map[string]bool, len(allowed))
	for _, node := range allowed {
		nodeID := strings.TrimSpace(node.NodeID)
		if nodeID == "" {
			return nil, errors.New("test waiver contains an empty node identity")
		}
		approved[nodeID] = true
	}
	approvedGlobal := make(map[string]bool, len(approvedBlockingReasonHashes))
	for _, value := range approvedBlockingReasonHashes {
		hash := strings.ToLower(strings.TrimSpace(value))
		if len(hash) != 64 || approvedGlobal[hash] {
			return nil, errors.New("approved_blocking_reason_hashes must contain unique SHA-256 values")
		}
		for _, character := range hash {
			if !strings.ContainsRune("0123456789abcdef", character) {
				return nil, errors.New("approved_blocking_reason_hashes must contain unique SHA-256 values")
			}
		}
		approvedGlobal[hash] = true
	}
	matched := make(map[string]bool, len(approved))
	matchedGlobal := make(map[string]bool, len(approvedGlobal))
	for _, reason := range pkg.ConfidenceSummary.BlockingReasons {
		reason = strings.TrimSpace(reason)
		separator := strings.Index(reason, ":")
		if separator <= 0 {
			hash := model.SHA256Hex([]byte(reason))
			if !approvedGlobal[hash] {
				return nil, fmt.Errorf("test waiver does not explicitly approve global App blocking reason sha256=%s", hash)
			}
			matchedGlobal[hash] = true
			continue
		}
		nodeID := strings.TrimSpace(reason[:separator])
		if !approved[nodeID] {
			return nil, fmt.Errorf("test waiver does not approve App blocking node %q", nodeID)
		}
		matched[nodeID] = true
	}
	for nodeID := range approved {
		if !matched[nodeID] {
			return nil, fmt.Errorf("test waiver node %q is not named by the App blocking report", nodeID)
		}
	}
	for hash := range approvedGlobal {
		if !matchedGlobal[hash] {
			return nil, fmt.Errorf("approved global App blocking reason sha256=%s is not present in the package", hash)
		}
	}
	result := make([]string, 0, len(matchedGlobal))
	for hash := range matchedGlobal {
		result = append(result, hash)
	}
	sort.Strings(result)
	return result, nil
}

func verifyAppDraftProtocolHashes(bundle *model.ExecutableRecordingScriptBundle) error {
	if bundle == nil || bundle.PlanJSON == nil {
		return errors.New("App draft executable bundle or plan_json is missing")
	}
	planHash, err := bundle.PlanJSON.ComputeScriptHash()
	if err != nil {
		return fmt.Errorf("recompute App draft plan hash: %w", err)
	}
	if planHash == "" || planHash != bundle.Reproducibility.PlanHashSHA256 {
		return errors.New("App draft plan hash does not match its unchanged plan content")
	}
	bundleHash, err := bundle.ComputeBundleHash()
	if err != nil {
		return fmt.Errorf("recompute App draft bundle hash: %w", err)
	}
	if bundleHash == "" || bundleHash != bundle.Reproducibility.BundleHashSHA256 {
		return errors.New("App draft bundle hash does not match its unchanged bundle content")
	}
	return nil
}

func mapKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}

func appendBrowserAgentTestWaiverAudit(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create test waiver audit directory: %w", err)
	}
	data, err := model.CanonicalJSON(value)
	if err != nil {
		return fmt.Errorf("encode test waiver audit event: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open test waiver audit log: %w", err)
	}
	defer file.Close()
	_, err = file.Write(append(data, '\n'))
	return err
}

func safeWaiverError(err error) string {
	if err == nil {
		return ""
	}
	return redactBridgeError(err.Error())
}

func (s *DevHTTPServer) handleAppPackageTestWaivers(w http.ResponseWriter, r *http.Request) {
	if err := s.requireLocalServerAcceptance(r); err != nil {
		writeBridgeValue(w, nil, err)
		return
	}
	manager := s.service.devAppPackageTestWaivers
	if manager == nil {
		writeBridgeValue(w, nil, errors.New("app-package test waiver manager is not configured"))
		return
	}
	if r.Method == http.MethodPost && r.URL.Path == "/v1/desktop/app-package-test-waivers" {
		var request DevAppPackageWaiverRequest
		if err := decodeJSON(r, &request); err != nil {
			writeBridgeValue(w, nil, err)
			return
		}
		value, err := manager.Issue(r.Context(), request)
		writeBridgeValue(w, value, err)
		return
	}
	const prefix = "/v1/desktop/app-package-test-waivers/"
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, prefix), "/"), "/")
	if r.Method == http.MethodGet && len(parts) == 1 && parts[0] != "" {
		value, err := manager.Get(parts[0])
		writeBridgeValue(w, value, err)
		return
	}
	if r.Method == http.MethodPost && len(parts) == 2 && parts[0] != "" && parts[1] == "run" {
		var request DevAppPackageWaiverRunRequest
		if err := decodeJSON(r, &request); err != nil {
			writeBridgeValue(w, nil, err)
			return
		}
		value, err := manager.Run(r.Context(), parts[0], request)
		writeBridgeValue(w, value, err)
		return
	}
	http.NotFound(w, r)
}

func (s *DevHTTPServer) handleAppPackageRawTestWaiver(w http.ResponseWriter, r *http.Request) {
	if err := s.requireLocalServerAcceptance(r); err != nil {
		writeBridgeValue(w, nil, err)
		return
	}
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	manager := s.service.devAppPackageTestWaivers
	if manager == nil {
		writeBridgeValue(w, nil, errors.New("app-package test waiver manager is not configured"))
		return
	}
	var request DevAppPackageRawWaiverRequest
	if err := decodeJSON(r, &request); err != nil {
		writeBridgeValue(w, nil, err)
		return
	}
	value, err := manager.IssueFromRawFile(r.Context(), request)
	writeBridgeValue(w, value, err)
}
