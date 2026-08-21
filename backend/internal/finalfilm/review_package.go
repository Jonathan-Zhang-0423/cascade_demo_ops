package finalfilm

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"cascade-demoops/backend/internal/model"
)

type reviewPackageManifest struct {
	SchemaVersion     string                             `json:"schema_version"`
	PackageID         string                             `json:"package_id"`
	JobID             string                             `json:"job_id"`
	JobRevision       int                                `json:"job_revision"`
	AutomationProfile string                             `json:"automation_profile"`
	SkillVersions     map[string]string                  `json:"skill_versions"`
	Files             []model.FinalFilmReviewPackageFile `json:"files"`
}

func (s *Service) buildReviewPackage(ctx context.Context, job model.FinalFilmJob, record GeneratedTrackRecord) (model.FinalFilmReviewPackage, error) {
	packageID := deterministicReviewPackageID(job.JobID, job.Revision)
	parent := filepath.Join(s.outputRoot, job.JobID)
	directory := filepath.Join(parent, fmt.Sprintf("review-package-r%d", job.Revision))
	temporary := directory + ".tmp"
	if err := os.RemoveAll(temporary); err != nil {
		return model.FinalFilmReviewPackage{}, err
	}
	if err := os.MkdirAll(temporary, 0o700); err != nil {
		return model.FinalFilmReviewPackage{}, err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(temporary)
		}
	}()
	files := []model.FinalFilmReviewPackageFile{}
	add := func(role, source, relative, storedSHA string, required bool) error {
		entry, copyErr := copyReviewPackageFile(temporary, role, source, relative, storedSHA, required)
		if copyErr != nil {
			return copyErr
		}
		if entry.RelativePath != "" {
			files = append(files, entry)
		}
		return nil
	}
	if job.FinalOutputValidation == nil {
		return model.FinalFilmReviewPackage{}, errors.New("review package requires final output validation")
	}
	if err := add("final_video", job.FinalRender.VideoPath, "video/final.mp4", job.FinalOutputValidation.VideoSHA256, true); err != nil {
		return model.FinalFilmReviewPackage{}, err
	}
	if err := add("fact_track_baseline", job.BaselineRender.VideoPath, "video/fact-track-baseline.mp4", "", true); err != nil {
		return model.FinalFilmReviewPackage{}, err
	}
	raw := recordingArtifact(job.Catalog)
	if raw == nil {
		return model.FinalFilmReviewPackage{}, errors.New("review package requires the raw recording artifact")
	}
	rawPath := strings.TrimSpace(raw.LocalPath)
	if rawPath == "" {
		rawPath = filePathFromLocalURI(raw.URI)
	}
	if err := add("raw_recording", rawPath, "source/raw-recording"+filepath.Ext(rawPath), raw.SHA256, true); err != nil {
		return model.FinalFilmReviewPackage{}, err
	}
	for _, candidate := range record.Candidates {
		base := filepath.Join("providers", safeReviewName(candidate.IntentID), safeReviewName(candidate.Provider), safeReviewName(candidate.CandidateID))
		if err := add("provider_original", candidate.OriginalArtifact.Path, filepath.Join(base, "original"+filepath.Ext(candidate.OriginalArtifact.Path)), candidate.OriginalArtifact.SHA256, false); err != nil {
			return model.FinalFilmReviewPackage{}, err
		}
		if err := add("provider_normalized", candidate.NormalizedArtifact.Path, filepath.Join(base, "normalized.mp4"), candidate.NormalizedArtifact.SHA256, false); err != nil {
			return model.FinalFilmReviewPackage{}, err
		}
	}
	if err := add("render_manifest", job.FinalRender.RenderManifestPath, "reports/render-manifest.json", "", true); err != nil {
		return model.FinalFilmReviewPackage{}, err
	}
	for _, optional := range discoverFinalRenderSidecars(filepath.Dir(job.FinalRender.RenderManifestPath)) {
		if err := add(optional.role, optional.path, filepath.Join("reports", filepath.Base(optional.path)), "", false); err != nil {
			return model.FinalFilmReviewPackage{}, err
		}
	}
	for _, supplement := range job.ReviewSupplements {
		if err := add(supplement.Role, supplement.SourcePath, supplement.RelativePath, "", supplement.Required); err != nil {
			return model.FinalFilmReviewPackage{}, err
		}
	}
	events, err := s.store.ListEvents(ctx, job.JobID)
	if err != nil {
		return model.FinalFilmReviewPackage{}, fmt.Errorf("load final film events for review package: %w", err)
	}
	metadata := []struct {
		role, path string
		value      any
	}{{"director_plan", "plans/director-plan.json", job.DirectorPlan}, {"evidence_digest", "plans/evidence-digest.json", job.EvidenceDigest}, {"edl", "plans/final-edl.json", job.FinalPlan}, {"quality_reports", "reports/candidate-quality.json", job.QualityReports}, {"provider_attempts", "reports/provider-attempts.json", job.ProviderAttempts}, {"event_log", "reports/final-film-events.json", events}}
	for _, item := range metadata {
		payload, marshalErr := json.MarshalIndent(item.value, "", "  ")
		if marshalErr != nil {
			return model.FinalFilmReviewPackage{}, marshalErr
		}
		target := filepath.Join(temporary, filepath.FromSlash(item.path))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return model.FinalFilmReviewPackage{}, err
		}
		if err := os.WriteFile(target, append(payload, '\n'), 0o600); err != nil {
			return model.FinalFilmReviewPackage{}, err
		}
		info, _ := os.Stat(target)
		files = append(files, model.FinalFilmReviewPackageFile{Role: item.role, RelativePath: filepath.ToSlash(item.path), Source: "job_state", Required: true, SizeBytes: info.Size()})
	}
	manifestPath := filepath.Join(temporary, "manifest.json")
	manifest := reviewPackageManifest{SchemaVersion: model.FinalFilmReviewPackageSchemaVersion, PackageID: packageID, JobID: job.JobID, JobRevision: job.Revision, AutomationProfile: job.AutomationProfile, Files: files}
	if job.AutomationPolicy != nil {
		manifest.SkillVersions = copyStringMap(job.AutomationPolicy.SkillVersions)
	}
	payload, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return model.FinalFilmReviewPackage{}, err
	}
	if err := os.WriteFile(manifestPath, append(payload, '\n'), 0o600); err != nil {
		return model.FinalFilmReviewPackage{}, err
	}
	_ = os.RemoveAll(directory)
	if err := os.Rename(temporary, directory); err != nil {
		return model.FinalFilmReviewPackage{}, err
	}
	cleanup = false
	zipPath := directory + ".zip"
	if err := zipReviewPackage(directory, zipPath); err != nil {
		return model.FinalFilmReviewPackage{}, err
	}
	return model.FinalFilmReviewPackage{SchemaVersion: model.FinalFilmReviewPackageSchemaVersion, PackageID: packageID, JobID: job.JobID, JobRevision: job.Revision, DirectoryPath: directory, ZIPPath: zipPath, ManifestPath: filepath.Join(directory, "manifest.json"), Files: files, CreatedAt: s.now().UTC()}, nil
}

func deterministicReviewPackageID(jobID string, revision int) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(jobID) + fmt.Sprintf("\x00%d", revision)))
	return "review_package_" + hex.EncodeToString(sum[:12])
}

func validateReviewSupplements(supplements []model.FinalFilmReviewSupplement) error {
	seenRoles := map[string]bool{}
	seenPaths := map[string]bool{}
	for _, supplement := range supplements {
		role := strings.TrimSpace(supplement.Role)
		source := strings.TrimSpace(supplement.SourcePath)
		relative := filepath.Clean(strings.TrimSpace(supplement.RelativePath))
		if role == "" || source == "" || relative == "." || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return errors.New("final film review supplement identity or relative path is invalid")
		}
		if seenRoles[role] || seenPaths[relative] || strings.EqualFold(filepath.Base(relative), "manifest.json") {
			return errors.New("final film review supplement role or path is duplicated or reserved")
		}
		seenRoles[role], seenPaths[relative] = true, true
	}
	return nil
}

type finalRenderSidecar struct{ role, path string }

func discoverFinalRenderSidecars(directory string) []finalRenderSidecar {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil
	}
	result := []finalRenderSidecar{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		role := ""
		switch ext {
		case ".ass", ".srt", ".vtt":
			role = "subtitles"
		case ".log":
			role = "ffmpeg_log"
		}
		if role != "" {
			result = append(result, finalRenderSidecar{role: role, path: filepath.Join(directory, entry.Name())})
		}
	}
	return result
}

func copyReviewPackageFile(root, role, source, relative, storedSHA string, required bool) (model.FinalFilmReviewPackageFile, error) {
	source = filepath.Clean(strings.TrimSpace(source))
	info, err := os.Stat(source)
	if err != nil {
		if !required && errors.Is(err, os.ErrNotExist) {
			return model.FinalFilmReviewPackageFile{}, nil
		}
		return model.FinalFilmReviewPackageFile{}, fmt.Errorf("review package %s: %w", role, err)
	}
	if !info.Mode().IsRegular() {
		return model.FinalFilmReviewPackageFile{}, fmt.Errorf("review package %s is not a regular file", role)
	}
	target := filepath.Join(root, filepath.FromSlash(relative))
	cleanRoot, cleanTarget := filepath.Clean(root), filepath.Clean(target)
	if cleanTarget == cleanRoot || !strings.HasPrefix(strings.ToLower(cleanTarget), strings.ToLower(cleanRoot+string(filepath.Separator))) {
		return model.FinalFilmReviewPackageFile{}, errors.New("review package path escapes package root")
	}
	if err := os.MkdirAll(filepath.Dir(cleanTarget), 0o700); err != nil {
		return model.FinalFilmReviewPackageFile{}, err
	}
	input, err := os.Open(source)
	if err != nil {
		return model.FinalFilmReviewPackageFile{}, err
	}
	defer input.Close()
	output, err := os.OpenFile(cleanTarget, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return model.FinalFilmReviewPackageFile{}, err
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil {
		return model.FinalFilmReviewPackageFile{}, copyErr
	}
	if closeErr != nil {
		return model.FinalFilmReviewPackageFile{}, closeErr
	}
	return model.FinalFilmReviewPackageFile{Role: role, RelativePath: filepath.ToSlash(relative), Source: source, Required: required, SizeBytes: info.Size(), SHA256: strings.ToLower(strings.TrimSpace(storedSHA))}, nil
}

func zipReviewPackage(directory, zipPath string) error {
	temporary := zipPath + ".tmp"
	file, err := os.Create(temporary)
	if err != nil {
		return err
	}
	writer := zip.NewWriter(file)
	err = filepath.Walk(directory, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || info.IsDir() {
			return walkErr
		}
		relative, relErr := filepath.Rel(directory, path)
		if relErr != nil {
			return relErr
		}
		entry, createErr := writer.Create(filepath.ToSlash(relative))
		if createErr != nil {
			return createErr
		}
		input, openErr := os.Open(path)
		if openErr != nil {
			return openErr
		}
		_, copyErr := io.Copy(entry, input)
		closeErr := input.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	closeZipErr := writer.Close()
	closeFileErr := file.Close()
	if err != nil {
		_ = os.Remove(temporary)
		return err
	}
	if closeZipErr != nil {
		return closeZipErr
	}
	if closeFileErr != nil {
		return closeFileErr
	}
	_ = os.Remove(zipPath)
	return os.Rename(temporary, zipPath)
}

func recordingArtifact(catalog model.AssetTimelineCatalog) *model.TimelineArtifact {
	for index := range catalog.Artifacts {
		if catalog.Artifacts[index].ID == catalog.Timeline.RecordingArtifactID {
			return &catalog.Artifacts[index]
		}
	}
	return nil
}

func filePathFromLocalURI(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(strings.ToLower(value), "file:///") {
		value = strings.TrimPrefix(value, "file:///")
		return filepath.FromSlash(value)
	}
	return value
}

func safeReviewName(value string) string {
	value = strings.TrimSpace(value)
	value = strings.NewReplacer("/", "_", "\\", "_", "..", "_").Replace(value)
	if value == "" {
		return "unknown"
	}
	return value
}

func (s *Service) RecordFinalReview(ctx context.Context, jobID string, expectedRevision int, decision, reason, reviewerRef, packageID string) (model.FinalFilmJob, error) {
	job, err := s.store.GetJob(ctx, jobID)
	if err != nil {
		return model.FinalFilmJob{}, err
	}
	if job.Revision != expectedRevision || job.State != model.FinalFilmJobAwaitingFinalReview || job.ReviewPackage == nil {
		return model.FinalFilmJob{}, errors.New("final review requires the current awaiting_final_review revision")
	}
	decision, reviewerRef, packageID = strings.TrimSpace(decision), strings.TrimSpace(reviewerRef), strings.TrimSpace(packageID)
	if reviewerRef == "" || packageID != job.ReviewPackage.PackageID || job.ReviewPackage.JobRevision != job.Revision {
		return model.FinalFilmJob{}, errors.New("final review must bind the current reviewer, package, and revision")
	}
	if decision != "accept" && decision != "reject" {
		return model.FinalFilmJob{}, errors.New("final review decision must be accept or reject")
	}
	if decision == "reject" && strings.TrimSpace(reason) == "" {
		return model.FinalFilmJob{}, errors.New("final review rejection reason is required")
	}
	next := job
	next.Revision++
	next.UpdatedAt = s.now().UTC()
	next.FinalReview = &model.FinalFilmFinalReview{Decision: decision, Reason: strings.TrimSpace(reason), ReviewerRef: reviewerRef, JobRevision: job.Revision, PackageID: packageID, ReviewedAt: next.UpdatedAt}
	if decision == "accept" {
		next.State, next.Phase = model.FinalFilmJobCompleted, "completed_after_final_review"
	} else {
		next.State, next.Phase = model.FinalFilmJobRevisionRequested, "revision_requested"
		next.RunAuthorization = nil
		next.GenerationAuthorized = false
		next.GenerationAuthorizationRef = ""
	}
	if err := s.store.TransitionJob(ctx, job.JobID, job.Revision, next, s.event(next, next.Phase, "已记录最终成片人工终审", map[string]any{"decision": decision, "package_id": packageID})); err != nil {
		return model.FinalFilmJob{}, err
	}
	return next, nil
}

func (s *Service) FinalMediaPath(ctx context.Context, jobID string) (string, error) {
	job, err := s.store.GetJob(ctx, jobID)
	if err != nil {
		return "", err
	}
	path := filepath.Clean(job.FinalRender.VideoPath)
	if path == "." || path == "" {
		return "", errors.New("final media is not ready")
	}
	return path, nil
}

func (s *Service) ReviewPackagePath(ctx context.Context, jobID string) (string, error) {
	job, err := s.store.GetJob(ctx, jobID)
	if err != nil {
		return "", err
	}
	if job.ReviewPackage == nil {
		return "", errors.New("review package is not ready")
	}
	return filepath.Clean(job.ReviewPackage.ZIPPath), nil
}
