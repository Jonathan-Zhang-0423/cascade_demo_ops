// Command seedancecandidateaudit turns a completed Seedance preflight audit
// into a local, review-only Server candidate review. It never calls a
// provider, approves an asset, or changes a DemoEditPlan.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"cascade-demoops/backend/internal/executor"
	"cascade-demoops/backend/internal/media"
	"cascade-demoops/backend/internal/model"
)

type preflightAudit struct {
	CreatedAt       time.Time       `json:"created_at"`
	SourcePackageID string          `json:"source_package_id"`
	Provider        string          `json:"provider"`
	Model           string          `json:"model"`
	TaskID          string          `json:"task_id"`
	ProviderStatus  string          `json:"provider_status"`
	Status          string          `json:"status"`
	Artifacts       []auditArtifact `json:"candidate_artifacts"`
	Probes          []auditProbe    `json:"normalization_probes"`
}

type auditArtifact struct {
	File       string `json:"file"`
	Path       string `json:"path"`
	SHA256     string `json:"sha256"`
	SizeBytes  int64  `json:"size_bytes"`
	Normalized bool   `json:"normalized"`
}

type auditProbe struct {
	SourceFile      string                    `json:"source_file"`
	NormalizedFile  string                    `json:"normalized_file"`
	OriginalProbe   media.MiniMaxH3MediaProbe `json:"original_probe"`
	NormalizedProbe media.MiniMaxH3MediaProbe `json:"normalized_probe"`
}

func main() {
	auditPath := flag.String("audit", "", "completed seedance_audit.json")
	outputPath := flag.String("output", "", "candidate_asset_review.json output path")
	flag.Parse()
	if strings.TrimSpace(*auditPath) == "" {
		fatal(errors.New("-audit is required"))
	}
	audit, err := readAudit(*auditPath)
	fatalIf(err)
	result, err := generationResultFromAudit(audit)
	fatalIf(err)
	review := executor.ReviewArkMediaCandidateAssets(nil, &result, time.Now().UTC())
	if strings.TrimSpace(*outputPath) == "" {
		*outputPath = filepath.Join(filepath.Dir(*auditPath), "candidate_asset_review.json")
	}
	fatalIf(writeJSON(*outputPath, review))
	fmt.Printf("Seedance candidate audit completed\n")
	fmt.Printf("review_status=%s\n", review.Status)
	fmt.Printf("pending_review_count=%d\n", len(review.PendingReviewArtifacts))
	fmt.Printf("approved_count=%d\n", len(review.ApprovedArtifacts))
	fmt.Printf("rejected_count=%d\n", len(review.RejectedArtifacts))
	fmt.Printf("review_path=%s\n", *outputPath)
	fmt.Println("provider_output_adopted=false")
	fmt.Println("inserted_into_demo_edit_plan=false")
}

func readAudit(path string) (preflightAudit, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return preflightAudit{}, err
	}
	var audit preflightAudit
	if err := json.Unmarshal(data, &audit); err != nil {
		return preflightAudit{}, err
	}
	if audit.Provider != "seedance" && audit.Provider != "seedance-2.0" {
		return preflightAudit{}, fmt.Errorf("audit provider is not Seedance: %q", audit.Provider)
	}
	if audit.Status != "candidate_downloaded_and_normalized" {
		return preflightAudit{}, fmt.Errorf("audit is not a normalized candidate result: %s", audit.Status)
	}
	if audit.ProviderStatus != "succeeded" {
		return preflightAudit{}, fmt.Errorf("audit provider status is not succeeded: %s", audit.ProviderStatus)
	}
	if strings.TrimSpace(audit.TaskID) == "" || strings.TrimSpace(audit.SourcePackageID) == "" {
		return preflightAudit{}, errors.New("audit task_id and source_package_id are required")
	}
	return audit, nil
}

func generationResultFromAudit(audit preflightAudit) (model.ArkMediaGenerationResult, error) {
	probes := map[string]auditProbe{}
	for _, probe := range audit.Probes {
		if probe.NormalizedFile != "" {
			probes[probe.NormalizedFile] = probe
		}
	}
	artifacts := append([]auditArtifact{}, audit.Artifacts...)
	sort.Slice(artifacts, func(left, right int) bool { return artifacts[left].File < artifacts[right].File })
	originals := map[string]model.ArtifactRef{}
	result := model.ArkMediaGenerationResult{
		SchemaVersion:    model.ArkMediaGenerationResultSchemaVersion,
		ResultID:         "seedance_preflight_result_" + safeID(audit.TaskID),
		CreatedAt:        audit.CreatedAt,
		SourcePackageID:  audit.SourcePackageID,
		Status:           "candidate_artifacts_normalized",
		Provider:         audit.Provider,
		Model:            audit.Model,
		TaskID:           audit.TaskID,
		ProviderStatus:   audit.ProviderStatus,
		NonAuthoritative: true,
	}
	for index, item := range artifacts {
		if item.Normalized || !strings.EqualFold(filepath.Ext(item.File), ".mp4") {
			continue
		}
		if err := verifyAuditArtifact(item); err != nil {
			return model.ArkMediaGenerationResult{}, err
		}
		id := fmt.Sprintf("seedance_original_%03d", index+1)
		original := model.ArtifactRef{ID: id, Kind: "generated_video_candidate", URI: item.Path, MimeType: "video/mp4", SHA256: item.SHA256, SizeBytes: item.SizeBytes, CreatedAt: audit.CreatedAt, Metadata: providerMetadata(audit)}
		original.Metadata["download_status"] = "downloaded"
		originals[item.File] = original
		result.DownloadedArtifacts = append(result.DownloadedArtifacts, original)
	}
	for index, item := range artifacts {
		if !item.Normalized || !strings.EqualFold(filepath.Ext(item.File), ".mp4") {
			continue
		}
		if err := verifyAuditArtifact(item); err != nil {
			return model.ArkMediaGenerationResult{}, err
		}
		probe, ok := probes[item.File]
		if !ok {
			return model.ArkMediaGenerationResult{}, fmt.Errorf("normalized candidate %s has no recorded media probe", item.File)
		}
		originalName := strings.TrimSuffix(item.File, ".normalized.mp4") + ".mp4"
		original, ok := originals[originalName]
		if !ok {
			return model.ArkMediaGenerationResult{}, fmt.Errorf("normalized candidate %s has no original candidate", item.File)
		}
		metadata := providerMetadata(audit)
		metadata["provider_original_artifact_id"] = original.ID
		metadata["artifact_variant"] = "normalized"
		metadata["normalization_status"] = "ok"
		metadata["media_probe_status"] = "ok"
		metadata["normalization_profile"] = media.GeneratedShotNormalizationProfile
		metadata["original_media_probe"] = probe.OriginalProbe
		metadata["normalized_media_probe"] = probe.NormalizedProbe
		metadata["duration_ms"] = int(probe.NormalizedProbe.DurationSec*1000 + 0.5)
		metadata["presentation_only"] = true
		result.DownloadedArtifacts = append(result.DownloadedArtifacts, model.ArtifactRef{ID: fmt.Sprintf("seedance_normalized_%03d", index+1), Kind: "generated_video_candidate", URI: item.Path, MimeType: "video/mp4", SHA256: item.SHA256, SizeBytes: item.SizeBytes, CreatedAt: audit.CreatedAt, Metadata: metadata})
	}
	if len(result.DownloadedArtifacts) == 0 {
		return model.ArkMediaGenerationResult{}, errors.New("audit contains no downloadable Seedance MP4 artifacts")
	}
	return result, nil
}

func providerMetadata(audit preflightAudit) map[string]any {
	return map[string]any{
		"provider": audit.Provider, "model": audit.Model, "task_id": audit.TaskID,
		"non_authoritative": true, "include_in_demo": false,
		"source_material_policy": "non_authoritative_generated_candidate",
	}
}

func verifyAuditArtifact(item auditArtifact) error {
	info, err := os.Stat(item.Path)
	if err != nil || info.IsDir() {
		return fmt.Errorf("audit artifact %s is unavailable", item.File)
	}
	if info.Size() != item.SizeBytes || item.SizeBytes <= 0 {
		return fmt.Errorf("audit artifact %s size does not match recorded evidence", item.File)
	}
	data, err := os.ReadFile(item.Path)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(data)
	if !strings.EqualFold(hex.EncodeToString(digest[:]), item.SHA256) {
		return fmt.Errorf("audit artifact %s sha256 does not match recorded evidence", item.File)
	}
	return nil
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}

func safeID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.Map(func(char rune) rune {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') {
			return char
		}
		return '_'
	}, value)
	return strings.Trim(value, "_")
}

func fatalIf(err error) {
	if err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "Seedance candidate audit failed:", err)
	os.Exit(1)
}
