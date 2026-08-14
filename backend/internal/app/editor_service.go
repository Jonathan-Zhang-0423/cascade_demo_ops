package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/executor"
	"cascade-demoops/backend/internal/model"
)

const MaxEditorUploadBytes int64 = 8 << 30

var editorUploadExtensions = map[string]bool{
	".mp4": true, ".webm": true, ".mov": true, ".m4v": true,
	".wav": true, ".mp3": true, ".m4a": true, ".aac": true, ".ogg": true, ".flac": true,
}

func (s *Service) CreateEditorSession(ctx context.Context, request model.EditorCreateSessionRequest) (model.EditorSession, error) {
	now := time.Now().UTC()
	sessionID, err := newEditorID("edit")
	if err != nil {
		return model.EditorSession{}, err
	}
	name := strings.TrimSpace(request.Name)
	if name == "" {
		name = "未命名演示"
	}
	session := model.EditorSession{
		SchemaVersion: model.EditorSessionSchemaVersion,
		SessionID:     sessionID,
		Name:          name,
		Mode:          model.EditorSessionModeDemoSafe,
		Status:        model.EditorSessionStatusEditing,
		Revision:      1,
		CreatedAt:     now,
		UpdatedAt:     now,
		AssetCatalog: model.AssetTimelineCatalog{
			SchemaVersion:   model.AssetTimelineCatalogSchemaVersion,
			CatalogID:       "catalog_" + sessionID,
			WorkflowGraphID: "editor_" + sessionID,
			GraphVersion:    1,
			RunID:           sessionID,
			Source:          model.AssetTimelineSource{GeneratedAt: now},
			Constraints: model.AssetTimelineConstraints{
				SourceMaterialOnly:                true,
				ProhibitNewImageOrVideoGeneration: false,
				ScriptIsPrimaryStoryline:          true,
				AllowedEditOperations:             append([]model.EditOperationType{}, model.DemoEditAllowedOperations...),
				ProhibitedPlanKeys:                append([]string{}, model.DemoEditProhibitedPlanKeys...),
			},
			Steps:     []model.TimelineStep{},
			Artifacts: []model.TimelineArtifact{},
		},
		EditPlan: model.DemoEditPlan{
			SchemaVersion:        model.DemoEditPlanSchemaVersion,
			PlanID:               "plan_" + sessionID,
			CatalogID:            "catalog_" + sessionID,
			Objective:            name,
			SourceAuthority:      model.DemoEditSourceAuthorityServerLocalEditor,
			ModelRole:            model.DemoEditModelRolePresentationOptimizerOnly,
			SourceMaterialPolicy: model.DemoEditSourceMaterialPolicyExistingAssetsOnly,
			ScriptOrderPolicy:    model.DemoEditScriptOrderPolicyPreserveRequiredStepOrder,
			LockedFields:         append([]string{}, model.DemoEditRequiredLockedFields...),
			ModelEditableFields:  append([]string{}, model.DemoEditAllowedModelEditableFields...),
			Shots:                []model.DemoEditShot{},
			Audio:                &model.DemoEditAudioPolicy{Mode: "source", VolumePercent: 100},
		},
		PreviewProfile:           model.EditorRenderProfile{Mode: "preview", Width: 1280, Height: 720, FPS: 30, Format: "mp4", Preset: "ultrafast", CRF: 28},
		FinalProfile:             model.EditorRenderProfile{Mode: "final", Width: 2560, Height: 1440, FPS: 30, Format: "mp4", Preset: "medium", CRF: 18},
		Preview:                  model.EditorRenderState{Status: model.EditorRenderStatusNotStarted},
		FinalRender:              model.EditorRenderState{Status: model.EditorRenderStatusNotStarted},
		PresentationCapabilities: s.editorPresentationCapabilities(),
		ProviderCapabilities:     s.editorProviderCapabilities(),
	}
	if err := s.saveEditorSession(session); err != nil {
		return model.EditorSession{}, err
	}
	if strings.TrimSpace(request.SourcePath) != "" {
		return s.ImportEditorAsset(ctx, sessionID, model.EditorImportAssetRequest{Path: request.SourcePath})
	}
	return session, nil
}

func (s *Service) CreateEditorSessionFromResultPackage(ctx context.Context, request model.EditorCreateFromResultPackageRequest) (model.EditorSession, error) {
	result, err := loadEditorResultPackage(request)
	if err != nil {
		return model.EditorSession{}, err
	}
	if err := result.ValidateStatusContract(); err != nil {
		return model.EditorSession{}, fmt.Errorf("invalid recording result package: %w", err)
	}
	if result.Status == model.RecordingResultStatusFailed {
		return model.EditorSession{}, errors.New("failed recording result package cannot create an editor session")
	}
	if strings.TrimSpace(result.ResultID) == "" {
		return model.EditorSession{}, errors.New("recording result package result_id is required")
	}

	artifact, sourcePath, err := resolveResultRecordingArtifact(result, request)
	if err != nil {
		return model.EditorSession{}, err
	}
	name := strings.TrimSpace(request.Name)
	if name == "" {
		name = "结果包 " + result.ResultID
	}
	session, err := s.CreateEditorSession(ctx, model.EditorCreateSessionRequest{Name: name})
	if err != nil {
		return model.EditorSession{}, err
	}
	imported, err := s.ImportEditorAsset(ctx, session.SessionID, model.EditorImportAssetRequest{Path: sourcePath, Label: firstNonEmptyString(artifact.Label, artifact.ID)})
	if err != nil {
		s.removeEditorSession(session.SessionID)
		return model.EditorSession{}, fmt.Errorf("import raw recording %s: %w", artifact.ID, err)
	}
	if checksum := strings.ToLower(strings.TrimSpace(artifact.SHA256)); !resultArtifactEncrypted(result, artifact.ID) && isFullSHA256(checksum) && checksum != strings.ToLower(imported.AssetCatalog.Artifacts[0].SHA256) {
		s.removeEditorSession(session.SessionID)
		return model.EditorSession{}, fmt.Errorf("raw recording checksum mismatch for artifact %s", artifact.ID)
	}

	s.editorMu.Lock()
	defer s.editorMu.Unlock()
	latest, err := s.loadEditorSession(session.SessionID)
	if err != nil {
		return model.EditorSession{}, err
	}
	applyResultPackageToEditorSession(&latest, result, artifact, request)
	if validation, validateErr := s.editorWorker.ValidateEditPlan(ctx, executor.EditPlanValidationRequest{Catalog: latest.AssetCatalog, EditPlan: latest.EditPlan}); validateErr == nil {
		latest.Validation = &validation
	}
	latest.UpdatedAt = time.Now().UTC()
	if err := s.saveEditorSessionUnlocked(latest); err != nil {
		return model.EditorSession{}, err
	}
	return latest, nil
}

// EnsureEditorSessionFromResultPackage creates the local editing handoff once
// per recording result. Repeated delivery notifications reuse the existing
// session instead of creating duplicate entries in the editor inbox.
func (s *Service) EnsureEditorSessionFromResultPackage(ctx context.Context, request model.EditorCreateFromResultPackageRequest) (model.EditorSession, bool, error) {
	result, err := loadEditorResultPackage(request)
	if err != nil {
		return model.EditorSession{}, false, err
	}
	resultID := strings.TrimSpace(result.ResultID)
	if resultID == "" {
		return model.EditorSession{}, false, errors.New("recording result package result_id is required")
	}
	sessions, err := s.ListEditorSessions(ctx)
	if err != nil {
		return model.EditorSession{}, false, err
	}
	for _, session := range sessions {
		if session.AssetCatalog.Source.RecordingResultPackageID == resultID {
			return session, false, nil
		}
	}
	normalized := request
	normalized.ResultPackage = result
	normalized.ResultPackagePath = ""
	created, err := s.CreateEditorSessionFromResultPackage(ctx, normalized)
	return created, err == nil, err
}

func (s *Service) ListEditorSessions(_ context.Context) ([]model.EditorSession, error) {
	s.editorMu.Lock()
	defer s.editorMu.Unlock()
	root := s.editorSessionsRoot()
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return []model.EditorSession{}, nil
	}
	if err != nil {
		return nil, err
	}
	result := make([]model.EditorSession, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		session, loadErr := s.loadEditorSession(entry.Name())
		if loadErr == nil {
			result = append(result, session)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].UpdatedAt.After(result[j].UpdatedAt) })
	return result, nil
}

func (s *Service) GetEditorSession(_ context.Context, sessionID string) (model.EditorSession, error) {
	s.editorMu.Lock()
	defer s.editorMu.Unlock()
	return s.loadEditorSession(sessionID)
}

func (s *Service) ImportEditorAsset(ctx context.Context, sessionID string, request model.EditorImportAssetRequest) (model.EditorSession, error) {
	s.editorMu.Lock()
	defer s.editorMu.Unlock()
	if s.editorWorker == nil {
		return model.EditorSession{}, errors.New("editor worker is unavailable")
	}
	session, err := s.loadEditorSession(sessionID)
	if err != nil {
		return model.EditorSession{}, err
	}
	probe, err := s.editorWorker.ProbeMedia(ctx, executor.MediaProbeRequest{Path: request.Path})
	if err != nil {
		return model.EditorSession{}, err
	}
	for _, existing := range session.AssetCatalog.Artifacts {
		if existing.SHA256 == probe.SHA256 {
			return model.EditorSession{}, errors.New("the same media file is already imported")
		}
	}
	assetID, err := newEditorID("asset")
	if err != nil {
		return model.EditorSession{}, err
	}
	label := strings.TrimSpace(request.Label)
	if label == "" {
		label = probe.FileName
	}
	isNarrationAudio := strings.HasPrefix(strings.ToLower(probe.MimeType), "audio/")
	artifact := model.TimelineArtifact{
		ID:            assetID,
		Kind:          "raw_recording",
		URI:           localFileURI(probe.Path),
		MimeType:      probe.MimeType,
		Label:         label,
		SHA256:        probe.SHA256,
		SizeBytes:     probe.SizeBytes,
		IncludeInDemo: true,
		AssetRole:     "editor_source_video",
		DurationMS:    probe.DurationMS,
		LocalPath:     probe.Path,
		Metadata: map[string]any{
			"source":            "local_import",
			"duration_ms":       probe.DurationMS,
			"width":             probe.Width,
			"height":            probe.Height,
			"fps":               probe.FPS,
			"video_codec":       probe.VideoCodec,
			"audio_codec":       probe.AudioCodec,
			"ffprobe_available": probe.FFProbeAvailable,
		},
	}
	if isNarrationAudio {
		artifact.Kind = "narration_audio"
		artifact.AssetRole = "narration_audio"
		artifact.Metadata["presentation_only"] = true
	}
	session.AssetCatalog.Artifacts = append(session.AssetCatalog.Artifacts, artifact)
	if isNarrationAudio {
		session.Revision++
		session.UpdatedAt = time.Now().UTC()
		session.Preview = model.EditorRenderState{Status: model.EditorRenderStatusNotStarted}
		session.FinalRender = model.EditorRenderState{Status: model.EditorRenderStatusNotStarted}
		validation, validateErr := s.editorWorker.ValidateEditPlan(ctx, executor.EditPlanValidationRequest{Catalog: session.AssetCatalog, EditPlan: session.EditPlan})
		if validateErr == nil {
			session.Validation = &validation
		}
		if err := s.saveEditorSessionUnlocked(session); err != nil {
			return model.EditorSession{}, err
		}
		return session, nil
	}
	if session.AssetCatalog.Timeline.RecordingArtifactID == "" {
		session.AssetCatalog.Timeline.RecordingArtifactID = assetID
	}
	session.AssetCatalog.Timeline.DurationMS += probe.DurationMS
	shotID, err := newEditorID("shot")
	if err != nil {
		return model.EditorSession{}, err
	}
	session.EditPlan.Shots = append(session.EditPlan.Shots, model.DemoEditShot{
		ID:                shotID,
		SourceArtifactID:  assetID,
		SourceTimeRangeMS: &model.MillisecondRange{0, probe.DurationMS},
		Purpose:           label,
		Operations:        []model.EditOperation{{Type: model.EditOperationTrim}},
		Overlays:          []model.EditOverlay{},
	})
	session.EditPlan.TargetDurationMS = editorPlanDuration(session.EditPlan)
	session.Revision++
	session.UpdatedAt = time.Now().UTC()
	session.Preview = model.EditorRenderState{Status: model.EditorRenderStatusNotStarted}
	session.FinalRender = model.EditorRenderState{Status: model.EditorRenderStatusNotStarted}
	validation, validateErr := s.editorWorker.ValidateEditPlan(ctx, executor.EditPlanValidationRequest{Catalog: session.AssetCatalog, EditPlan: session.EditPlan})
	if validateErr == nil {
		session.Validation = &validation
	}
	if err := s.saveEditorSessionUnlocked(session); err != nil {
		return model.EditorSession{}, err
	}
	return session, nil
}

func (s *Service) ImportEditorUpload(ctx context.Context, sessionID, fileName string, input io.Reader) (model.EditorSession, error) {
	return s.importEditorUpload(ctx, sessionID, fileName, input, MaxEditorUploadBytes)
}

func (s *Service) importEditorUpload(ctx context.Context, sessionID, fileName string, input io.Reader, maxBytes int64) (model.EditorSession, error) {
	finalPath, label, err := s.saveEditorUpload(sessionID, fileName, input, maxBytes)
	if err != nil {
		return model.EditorSession{}, err
	}
	session, err := s.ImportEditorAsset(ctx, sessionID, model.EditorImportAssetRequest{Path: finalPath, Label: label})
	if err != nil {
		_ = os.Remove(finalPath)
		return model.EditorSession{}, err
	}
	return session, nil
}

// ImportEditorStyleReference registers a video used only to derive a future
// style profile. It deliberately never adds a timeline shot or final-render
// input, so a reference cannot accidentally become customer-facing footage.
func (s *Service) ImportEditorStyleReference(ctx context.Context, sessionID string, request model.EditorImportAssetRequest) (model.EditorSession, error) {
	s.editorMu.Lock()
	defer s.editorMu.Unlock()
	if s.editorWorker == nil {
		return model.EditorSession{}, errors.New("editor worker is unavailable")
	}
	session, err := s.loadEditorSession(sessionID)
	if err != nil {
		return model.EditorSession{}, err
	}
	probe, err := s.editorWorker.ProbeMedia(ctx, executor.MediaProbeRequest{Path: request.Path})
	if err != nil {
		return model.EditorSession{}, err
	}
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(probe.MimeType)), "video/") {
		return model.EditorSession{}, errors.New("style reference must be a video asset")
	}
	for _, existing := range session.AssetCatalog.Artifacts {
		if existing.SHA256 == probe.SHA256 {
			return model.EditorSession{}, errors.New("the same media file is already imported")
		}
	}
	assetID, err := newEditorID("style_reference")
	if err != nil {
		return model.EditorSession{}, err
	}
	label := strings.TrimSpace(request.Label)
	if label == "" {
		label = probe.FileName
	}
	session.AssetCatalog.Artifacts = append(session.AssetCatalog.Artifacts, model.TimelineArtifact{
		ID:            assetID,
		Kind:          "style_reference_video",
		URI:           localFileURI(probe.Path),
		MimeType:      probe.MimeType,
		Label:         label,
		SHA256:        probe.SHA256,
		SizeBytes:     probe.SizeBytes,
		IncludeInDemo: false,
		AssetRole:     "style_reference_only",
		DurationMS:    probe.DurationMS,
		LocalPath:     probe.Path,
		Metadata: map[string]any{
			"source":                       "style_reference_import",
			"style_reference":              true,
			"presentation_only":            true,
			"rights_confirmation_required": true,
			"duration_ms":                  probe.DurationMS,
			"width":                        probe.Width,
			"height":                       probe.Height,
			"fps":                          probe.FPS,
			"video_codec":                  probe.VideoCodec,
			"audio_codec":                  probe.AudioCodec,
			"ffprobe_available":            probe.FFProbeAvailable,
		},
	})
	session.Revision++
	session.UpdatedAt = time.Now().UTC()
	// Keep previews invalidated even though this asset is intentionally absent
	// from the edit plan; it makes the session revision unambiguous to clients.
	session.Preview = model.EditorRenderState{Status: model.EditorRenderStatusNotStarted}
	session.FinalRender = model.EditorRenderState{Status: model.EditorRenderStatusNotStarted}
	if validation, validateErr := s.editorWorker.ValidateEditPlan(ctx, executor.EditPlanValidationRequest{Catalog: session.AssetCatalog, EditPlan: session.EditPlan}); validateErr == nil {
		session.Validation = &validation
	}
	if err := s.saveEditorSessionUnlocked(session); err != nil {
		return model.EditorSession{}, err
	}
	return session, nil
}

func (s *Service) ImportEditorStyleReferenceUpload(ctx context.Context, sessionID, fileName string, input io.Reader) (model.EditorSession, error) {
	finalPath, label, err := s.saveEditorUpload(sessionID, fileName, input, MaxEditorUploadBytes)
	if err != nil {
		return model.EditorSession{}, err
	}
	session, err := s.ImportEditorStyleReference(ctx, sessionID, model.EditorImportAssetRequest{Path: finalPath, Label: label})
	if err != nil {
		_ = os.Remove(finalPath)
		return model.EditorSession{}, err
	}
	return session, nil
}

func (s *Service) saveEditorUpload(sessionID, fileName string, input io.Reader, maxBytes int64) (string, string, error) {
	if input == nil {
		return "", "", errors.New("editor upload file is required")
	}
	if _, err := s.loadEditorSession(sessionID); err != nil {
		return "", "", err
	}
	label := filepath.Base(strings.TrimSpace(fileName))
	extension := strings.ToLower(filepath.Ext(label))
	if label == "." || label == "" || !editorUploadExtensions[extension] {
		return "", "", errors.New("editor upload must be mp4, webm, mov, m4v, wav, mp3, m4a, aac, ogg, or flac")
	}
	uploadID, err := newEditorID("upload")
	if err != nil {
		return "", "", err
	}
	sessionPath, err := s.editorSessionPath(sessionID)
	if err != nil {
		return "", "", err
	}
	uploadRoot := filepath.Join(filepath.Dir(sessionPath), "uploads")
	if err := os.MkdirAll(uploadRoot, 0o700); err != nil {
		return "", "", err
	}
	finalPath := filepath.Join(uploadRoot, uploadID+extension)
	tempPath := finalPath + ".tmp"
	output, err := os.OpenFile(tempPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", "", err
	}
	written, copyErr := io.Copy(output, io.LimitReader(input, maxBytes+1))
	closeErr := output.Close()
	if copyErr != nil || closeErr != nil || written > maxBytes {
		_ = os.Remove(tempPath)
		if written > maxBytes {
			return "", "", fmt.Errorf("editor upload exceeds %d bytes", maxBytes)
		}
		return "", "", errors.Join(copyErr, closeErr)
	}
	if err := os.Rename(tempPath, finalPath); err != nil {
		_ = os.Remove(tempPath)
		return "", "", err
	}
	return finalPath, label, nil
}

func (s *Service) SaveEditorPlan(ctx context.Context, sessionID string, request model.EditorSavePlanRequest) (model.EditorSession, error) {
	s.editorMu.Lock()
	defer s.editorMu.Unlock()
	session, err := s.loadEditorSession(sessionID)
	if err != nil {
		return model.EditorSession{}, err
	}
	if request.ExpectedRevision != session.Revision {
		return model.EditorSession{}, fmt.Errorf("editor revision conflict: expected %d, current %d", request.ExpectedRevision, session.Revision)
	}
	request.EditPlan.SchemaVersion = model.DemoEditPlanSchemaVersion
	request.EditPlan.CatalogID = session.AssetCatalog.CatalogID
	request.EditPlan.SourceAuthority = model.DemoEditSourceAuthorityServerLocalEditor
	request.EditPlan.ModelRole = model.DemoEditModelRolePresentationOptimizerOnly
	request.EditPlan.SourceMaterialPolicy = model.DemoEditSourceMaterialPolicyExistingAssetsOnly
	request.EditPlan.ScriptOrderPolicy = model.DemoEditScriptOrderPolicyPreserveRequiredStepOrder
	request.EditPlan.LockedFields = append([]string{}, model.DemoEditRequiredLockedFields...)
	request.EditPlan.ModelEditableFields = append([]string{}, model.DemoEditAllowedModelEditableFields...)
	if request.EditPlan.Audio == nil {
		request.EditPlan.Audio = &model.DemoEditAudioPolicy{Mode: "source", VolumePercent: 100}
	}
	request.EditPlan.TargetDurationMS = editorPlanDuration(request.EditPlan)
	if err := validateEditorCandidateReferences(session.AssetCatalog, request.EditPlan); err != nil {
		return model.EditorSession{}, err
	}
	validation, err := s.editorWorker.ValidateEditPlan(ctx, executor.EditPlanValidationRequest{Catalog: session.AssetCatalog, EditPlan: request.EditPlan})
	if err != nil {
		return model.EditorSession{}, err
	}
	session.EditPlan = request.EditPlan
	session.Validation = &validation
	session.Revision++
	session.Status = model.EditorSessionStatusEditing
	session.UpdatedAt = time.Now().UTC()
	session.Preview = model.EditorRenderState{Status: model.EditorRenderStatusNotStarted}
	session.FinalRender = model.EditorRenderState{Status: model.EditorRenderStatusNotStarted}
	if err := s.saveEditorSessionUnlocked(session); err != nil {
		return model.EditorSession{}, err
	}
	return session, nil
}

func (s *Service) ValidateEditorPlan(ctx context.Context, sessionID string) (model.DemoEditPlanValidationReport, error) {
	session, err := s.loadEditorSession(sessionID)
	if err != nil {
		return model.DemoEditPlanValidationReport{}, err
	}
	return s.editorWorker.ValidateEditPlan(ctx, executor.EditPlanValidationRequest{Catalog: session.AssetCatalog, EditPlan: session.EditPlan})
}

func (s *Service) RenderEditorSession(ctx context.Context, sessionID string, preview bool) (model.EditorSession, error) {
	return s.renderEditorSession(ctx, sessionID, preview, "")
}

func (s *Service) StartEditorRender(sessionID string, preview bool) (model.EditorSession, error) {
	jobID, err := newEditorID("render")
	if err != nil {
		return model.EditorSession{}, err
	}
	s.editorJobsMu.Lock()
	defer s.editorJobsMu.Unlock()
	if task, exists := s.editorJobs[sessionID]; exists {
		return model.EditorSession{}, fmt.Errorf("editor render job %s is already running", task.JobID)
	}

	s.editorMu.Lock()
	session, err := s.loadEditorSession(sessionID)
	if err != nil {
		s.editorMu.Unlock()
		return model.EditorSession{}, err
	}
	now := time.Now().UTC()
	state := model.EditorRenderState{
		Status: model.EditorRenderStatusRunning, JobID: jobID, Phase: "queued", Progress: 0,
		Revision: session.Revision, StartedAt: now,
	}
	kind := "final"
	if preview {
		kind = "preview"
		session.Preview = state
	} else {
		session.FinalRender = state
	}
	session.Status = model.EditorSessionStatusRendering
	session.UpdatedAt = now
	if err := s.saveEditorSessionUnlocked(session); err != nil {
		s.editorMu.Unlock()
		return model.EditorSession{}, err
	}
	s.editorMu.Unlock()

	jobCtx, cancel := context.WithCancel(context.Background())
	s.editorJobs[sessionID] = editorRenderTask{JobID: jobID, Kind: kind, Cancel: cancel}
	go s.runEditorRenderJob(jobCtx, sessionID, preview, jobID)
	return session, nil
}

func (s *Service) CancelEditorRender(sessionID, kind string) (model.EditorSession, error) {
	s.editorJobsMu.Lock()
	task, ok := s.editorJobs[sessionID]
	if !ok {
		s.editorJobsMu.Unlock()
		return model.EditorSession{}, errors.New("editor render job is not running")
	}
	if task.Kind != kind {
		s.editorJobsMu.Unlock()
		return model.EditorSession{}, fmt.Errorf("active editor render job is %s, not %s", task.Kind, kind)
	}
	task.Cancel()
	s.editorJobsMu.Unlock()

	s.editorMu.Lock()
	defer s.editorMu.Unlock()
	session, err := s.loadEditorSession(sessionID)
	if err != nil {
		return model.EditorSession{}, err
	}
	state := editorRenderState(session, kind == "preview")
	if state.JobID == task.JobID && state.Status == model.EditorRenderStatusRunning {
		state.CancelRequested = true
		state.Phase = "cancelling"
		setEditorRenderState(&session, kind == "preview", state)
		session.UpdatedAt = time.Now().UTC()
		if err := s.saveEditorSessionUnlocked(session); err != nil {
			return model.EditorSession{}, err
		}
	}
	return session, nil
}

func (s *Service) runEditorRenderJob(ctx context.Context, sessionID string, preview bool, jobID string) {
	defer func() {
		s.editorJobsMu.Lock()
		if task, ok := s.editorJobs[sessionID]; ok && task.JobID == jobID {
			delete(s.editorJobs, sessionID)
		}
		s.editorJobsMu.Unlock()
	}()
	s.updateEditorRenderProgress(sessionID, preview, jobID, "validating", 10)
	if _, err := s.renderEditorSession(ctx, sessionID, preview, jobID); err != nil {
		s.finishEditorRenderError(sessionID, preview, jobID, err, ctx.Err() != nil)
	}
}

func (s *Service) updateEditorRenderProgress(sessionID string, preview bool, jobID, phase string, progress int) {
	s.editorMu.Lock()
	defer s.editorMu.Unlock()
	session, err := s.loadEditorSession(sessionID)
	if err != nil {
		return
	}
	state := editorRenderState(session, preview)
	if state.JobID != jobID || state.Status != model.EditorRenderStatusRunning {
		return
	}
	state.Phase = phase
	state.Progress = progress
	setEditorRenderState(&session, preview, state)
	session.UpdatedAt = time.Now().UTC()
	_ = s.saveEditorSessionUnlocked(session)
}

func (s *Service) finishEditorRenderError(sessionID string, preview bool, jobID string, renderErr error, cancelled bool) {
	s.editorMu.Lock()
	defer s.editorMu.Unlock()
	session, err := s.loadEditorSession(sessionID)
	if err != nil {
		return
	}
	state := editorRenderState(session, preview)
	if state.JobID != jobID || state.Status != model.EditorRenderStatusRunning {
		return
	}
	state.CompletedAt = time.Now().UTC()
	state.Error = renderErr.Error()
	if cancelled {
		state.Status = model.EditorRenderStatusCancelled
		state.Phase = "cancelled"
		session.Status = model.EditorSessionStatusEditing
	} else {
		state.Status = model.EditorRenderStatusFailed
		state.Phase = "failed"
		session.Status = model.EditorSessionStatusFailed
	}
	setEditorRenderState(&session, preview, state)
	session.UpdatedAt = state.CompletedAt
	_ = s.saveEditorSessionUnlocked(session)
}

func (s *Service) renderEditorSession(ctx context.Context, sessionID string, preview bool, jobID string) (model.EditorSession, error) {
	s.editorMu.Lock()
	session, err := s.loadEditorSession(sessionID)
	if err != nil {
		s.editorMu.Unlock()
		return model.EditorSession{}, err
	}
	if jobID != "" {
		current := editorRenderState(session, preview)
		if current.JobID != jobID || current.Status != model.EditorRenderStatusRunning {
			s.editorMu.Unlock()
			return model.EditorSession{}, errors.New("editor render job state changed")
		}
	}
	validation, err := s.editorWorker.ValidateEditPlan(ctx, executor.EditPlanValidationRequest{Catalog: session.AssetCatalog, EditPlan: session.EditPlan})
	if err != nil {
		s.editorMu.Unlock()
		return model.EditorSession{}, err
	}
	session.Validation = &validation
	if !validation.Valid {
		_ = s.saveEditorSessionUnlocked(session)
		s.editorMu.Unlock()
		return model.EditorSession{}, errors.New("edit plan validation failed")
	}
	now := time.Now().UTC()
	startedAt := now
	if current := editorRenderState(session, preview); jobID != "" && current.JobID == jobID && !current.StartedAt.IsZero() {
		startedAt = current.StartedAt
	}
	renderState := model.EditorRenderState{
		Status: model.EditorRenderStatusRunning, JobID: jobID, Phase: "rendering", Progress: 20,
		Revision: session.Revision, StartedAt: startedAt,
	}
	profile := session.FinalProfile
	kind := "final"
	if preview {
		profile = session.PreviewProfile
		kind = "preview"
		session.Preview = renderState
	} else {
		session.FinalRender = renderState
	}
	session.Status = model.EditorSessionStatusRendering
	session.UpdatedAt = now
	if err := s.saveEditorSessionUnlocked(session); err != nil {
		s.editorMu.Unlock()
		return model.EditorSession{}, err
	}
	s.editorMu.Unlock()

	outputDir := filepath.Join(s.runtime.ArtifactRoot, "editor", session.SessionID, fmt.Sprintf("%s-r%d", kind, session.Revision))
	assets := make([]model.ArtifactRef, 0, len(session.AssetCatalog.Artifacts))
	for _, artifact := range session.AssetCatalog.Artifacts {
		assets = append(assets, model.ArtifactRef{
			ID: artifact.ID, Kind: artifact.Kind, URI: artifact.URI, MimeType: artifact.MimeType, Label: artifact.Label,
			SHA256: artifact.SHA256, SizeBytes: artifact.SizeBytes, Sensitive: artifact.Sensitive,
			Metadata: map[string]any{"include_in_demo": artifact.IncludeInDemo, "asset_role": artifact.AssetRole, "duration_ms": artifact.DurationMS},
		})
	}
	result, renderErr := s.editorWorker.Render(ctx, executor.RenderRequest{
		OutputDir:            outputDir,
		DurationSec:          maxInt(1, (session.EditPlan.TargetDurationMS+999)/1000),
		GeneratedAssets:      assets,
		AssetTimelineCatalog: &session.AssetCatalog,
		EditPlan:             &session.EditPlan,
		RenderProfile:        &profile,
	})

	s.editorMu.Lock()
	defer s.editorMu.Unlock()
	latest, loadErr := s.loadEditorSession(sessionID)
	if loadErr != nil {
		return model.EditorSession{}, loadErr
	}
	if jobID != "" && editorRenderState(latest, preview).JobID != jobID {
		return latest, renderErr
	}
	completed := time.Now().UTC()
	state := model.EditorRenderState{JobID: jobID, Revision: session.Revision, StartedAt: startedAt, CompletedAt: completed}
	if ctx.Err() != nil {
		state.Status = model.EditorRenderStatusCancelled
		state.Phase = "cancelled"
		state.Error = "render cancelled"
		latest.Status = model.EditorSessionStatusEditing
		renderErr = ctx.Err()
	} else if renderErr != nil {
		state.Status = model.EditorRenderStatusFailed
		state.Phase = "failed"
		state.Error = renderErr.Error()
		latest.Status = model.EditorSessionStatusFailed
	} else {
		state.Status = model.EditorRenderStatusReady
		state.Phase = "completed"
		state.Progress = 100
		state.VideoPath = result.VideoPath
		state.RenderManifestPath = result.RenderManifestPath
		latest.Status = model.EditorSessionStatusReady
	}
	setEditorRenderState(&latest, preview, state)
	latest.UpdatedAt = completed
	if err := s.saveEditorSessionUnlocked(latest); err != nil {
		return model.EditorSession{}, err
	}
	return latest, renderErr
}

func editorRenderState(session model.EditorSession, preview bool) model.EditorRenderState {
	if preview {
		return session.Preview
	}
	return session.FinalRender
}

func setEditorRenderState(session *model.EditorSession, preview bool, state model.EditorRenderState) {
	if preview {
		session.Preview = state
	} else {
		session.FinalRender = state
	}
}

func (s *Service) EditorMediaPath(sessionID, kind, assetID string) (string, string, error) {
	session, err := s.loadEditorSession(sessionID)
	if err != nil {
		return "", "", err
	}
	if kind == "preview" || kind == "final" {
		state := session.FinalRender
		if kind == "preview" {
			state = session.Preview
		}
		if state.Status != model.EditorRenderStatusReady || state.VideoPath == "" {
			return "", "", errors.New("rendered media is not ready")
		}
		path, err := filepath.Abs(filepath.Clean(state.VideoPath))
		if err != nil || !pathWithinRoot(path, s.runtime.ArtifactRoot) {
			return "", "", errors.New("rendered media path is outside artifact root")
		}
		return path, "video/mp4", nil
	}
	for _, artifact := range session.AssetCatalog.Artifacts {
		if artifact.ID == assetID {
			return artifact.LocalPath, artifact.MimeType, nil
		}
	}
	return "", "", errors.New("editor asset not found")
}

func (s *Service) editorPresentationCapabilities() []model.EditorPresentationCapabilityProfile {
	return []model.EditorPresentationCapabilityProfile{{
		Capability:     model.PresentationVideoCandidateCapability,
		ProfileVersion: "server-presentation-video-v1",
		Available:      s.runtime.ArkMediaMode != "disabled",
		Limits: model.EditorPresentationCapabilityLimits{
			MaxReferenceAssets: 4, MaxReferenceImages: 4, MaxReferenceVideos: 3, MaxReferenceAudios: 0,
			ReferenceVideoMinSec: 2, ReferenceVideoMaxSec: 15, ReferenceVideoTotalMaxSec: 15,
			CandidateDurationMinSec: 4, CandidateDurationMaxSec: 15, RecommendedCandidateDurationSec: 5,
			AcceptedImageFormats: []string{"png", "jpeg"}, AcceptedVideoFormats: []string{"mp4", "mov"},
			MaxRequestBodyBytes: 64 << 20, GeneratedAudioEnabled: false,
		},
		Policies: model.EditorPresentationCapabilityPolicies{
			PresentationOnly: true, RequiresExplicitReview: true, MayReplaceCapturedUI: false,
			FailureBlocksRecordingDelivery: false,
		},
	}}
}

func (s *Service) editorProviderCapabilities() []model.EditorProviderCapability {
	credential := s.runtime.ModelProviders[config.ModelProviderSeedance]
	return []model.EditorProviderCapability{{
		Provider: "seedance", Task: "generated_video_candidate", Mode: string(s.runtime.ArkMediaMode), Configured: credential.Enabled,
		Model: credential.DefaultModel, OutputKind: "generated_video_candidate", AutoInclude: false, RequiresReview: true,
		PresentationOnly: true, CanRepresentBusiness: false,
	}}
}

func (s *Service) editorSessionsRoot() string {
	return filepath.Join(s.runtime.DataRoot, "editor_sessions")
}

func (s *Service) editorSessionPath(sessionID string) (string, error) {
	clean := strings.TrimSpace(sessionID)
	if clean == "" || strings.ContainsAny(clean, `/\\`) || strings.Contains(clean, "..") {
		return "", errors.New("invalid editor session id")
	}
	return filepath.Join(s.editorSessionsRoot(), clean, "session.json"), nil
}

func (s *Service) loadEditorSession(sessionID string) (model.EditorSession, error) {
	path, err := s.editorSessionPath(sessionID)
	if err != nil {
		return model.EditorSession{}, err
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return model.EditorSession{}, err
	}
	var session model.EditorSession
	if err := json.Unmarshal(payload, &session); err != nil {
		return model.EditorSession{}, err
	}
	// Migrate legacy provider-specific session metadata in memory. Saving the
	// session removes the obsolete App-facing provider/model fields.
	if len(session.PresentationCapabilities) == 0 {
		session.PresentationCapabilities = s.editorPresentationCapabilities()
	}
	session.ProviderCapabilities = nil
	return session, nil
}

func (s *Service) ReviewEditorPresentationCandidate(ctx context.Context, sessionID string, request model.EditorReviewPresentationCandidateRequest) (model.EditorSession, error) {
	if err := ctx.Err(); err != nil {
		return model.EditorSession{}, err
	}
	s.editorMu.Lock()
	defer s.editorMu.Unlock()
	session, err := s.loadEditorSession(sessionID)
	if err != nil {
		return model.EditorSession{}, err
	}
	if request.ExpectedRevision != session.Revision {
		return model.EditorSession{}, fmt.Errorf("editor revision conflict: expected %d, current %d", request.ExpectedRevision, session.Revision)
	}
	artifactID := strings.TrimSpace(request.ArtifactID)
	for index := range session.AssetCatalog.Artifacts {
		artifact := &session.AssetCatalog.Artifacts[index]
		if artifact.ID != artifactID {
			continue
		}
		if artifact.Kind != "generated_video_candidate" || artifact.SourceStepID != "" || !editorMetadataBool(artifact.Metadata, "non_authoritative") || !editorMetadataBool(artifact.Metadata, "presentation_only") || artifact.Metadata["source_material_policy"] != "non_authoritative_generated_candidate" {
			return model.EditorSession{}, errors.New("presentation candidate violates the non-authoritative safety boundary")
		}
		if request.Approved && !editorMetadataBool(artifact.Metadata, "media_eligible") {
			return model.EditorSession{}, errors.New("presentation candidate has not passed media validation")
		}
		if artifact.Metadata == nil {
			artifact.Metadata = map[string]any{}
		}
		artifact.Metadata["approved_for_demo"] = request.Approved
		artifact.Metadata["approval_mode"] = "explicit_user_review"
		artifact.Metadata["explicit_review_required"] = true
		artifact.Metadata["include_in_demo"] = false
		artifact.IncludeInDemo = false
		session.Revision++
		session.Status = model.EditorSessionStatusEditing
		session.UpdatedAt = time.Now().UTC()
		session.Preview = model.EditorRenderState{Status: model.EditorRenderStatusNotStarted}
		session.FinalRender = model.EditorRenderState{Status: model.EditorRenderStatusNotStarted}
		if err := s.saveEditorSessionUnlocked(session); err != nil {
			return model.EditorSession{}, err
		}
		return session, nil
	}
	return model.EditorSession{}, errors.New("presentation candidate not found")
}

func validateEditorCandidateReferences(catalog model.AssetTimelineCatalog, plan model.DemoEditPlan) error {
	artifacts := map[string]model.TimelineArtifact{}
	for _, artifact := range catalog.Artifacts {
		artifacts[artifact.ID] = artifact
	}
	for _, shot := range plan.Shots {
		artifact, ok := artifacts[shot.SourceArtifactID]
		if !ok || artifact.Kind != "generated_video_candidate" {
			continue
		}
		if shot.SourceStepID != "" || !editorMetadataBool(artifact.Metadata, "approved_for_demo") || artifact.Metadata["approval_mode"] != "explicit_user_review" || !editorMetadataBool(artifact.Metadata, "presentation_only") || !editorMetadataBool(artifact.Metadata, "non_authoritative") {
			return model.ErrPresentationCandidateExplicitReviewRequired
		}
	}
	return nil
}

func editorMetadataBool(metadata map[string]any, key string) bool {
	value, _ := metadata[key].(bool)
	return value
}

func (s *Service) saveEditorSession(session model.EditorSession) error {
	s.editorMu.Lock()
	defer s.editorMu.Unlock()
	return s.saveEditorSessionUnlocked(session)
}

func (s *Service) saveEditorSessionUnlocked(session model.EditorSession) error {
	path, err := s.editorSessionPath(session.SessionID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return err
	}
	temp := path + ".tmp"
	if err := os.WriteFile(temp, append(payload, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(temp, path)
}

func (s *Service) removeEditorSession(sessionID string) {
	path, err := s.editorSessionPath(sessionID)
	if err == nil {
		_ = os.RemoveAll(filepath.Dir(path))
	}
}

func loadEditorResultPackage(request model.EditorCreateFromResultPackageRequest) (*model.RecordingResultPackage, error) {
	hasPath := strings.TrimSpace(request.ResultPackagePath) != ""
	hasInline := request.ResultPackage != nil
	if hasPath == hasInline {
		return nil, errors.New("provide exactly one of result_package_path or result_package")
	}
	if hasInline {
		return request.ResultPackage, nil
	}
	packagePath, err := localPathFromURI(request.ResultPackagePath)
	if err != nil {
		return nil, fmt.Errorf("result package path: %w", err)
	}
	payload, err := os.ReadFile(packagePath)
	if err != nil {
		return nil, fmt.Errorf("read result package: %w", err)
	}
	var result model.RecordingResultPackage
	if err := json.Unmarshal(payload, &result); err != nil {
		return nil, fmt.Errorf("decode result package: %w", err)
	}
	return &result, nil
}

func resolveResultRecordingArtifact(result *model.RecordingResultPackage, request model.EditorCreateFromResultPackageRequest) (model.ArtifactRef, string, error) {
	candidates := make([]model.ArtifactRef, 0)
	seen := map[string]bool{}
	add := func(artifact model.ArtifactRef) {
		if seen[artifact.ID] || !isRawRecordingArtifact(artifact) {
			return
		}
		seen[artifact.ID] = true
		candidates = append(candidates, artifact)
	}
	for _, artifact := range result.GeneratedAssets {
		add(artifact)
	}
	if result.ExecutionTrace != nil {
		for _, artifact := range result.ExecutionTrace.Artifacts {
			add(artifact)
		}
	}
	wantedID := strings.TrimSpace(request.RecordingArtifactID)
	if wantedID != "" {
		filtered := candidates[:0]
		for _, artifact := range candidates {
			if artifact.ID == wantedID {
				filtered = append(filtered, artifact)
			}
		}
		candidates = filtered
	}
	if len(candidates) == 0 {
		return model.ArtifactRef{}, "", errors.New("recording result package has no matching raw_recording artifact")
	}
	if len(candidates) > 1 {
		return model.ArtifactRef{}, "", errors.New("recording result package has multiple raw recordings; recording_artifact_id is required")
	}
	artifact := candidates[0]
	if override := strings.TrimSpace(request.RecordingPath); override != "" {
		path, err := localPathFromURI(override)
		return artifact, path, err
	}
	if override := strings.TrimSpace(request.ArtifactPaths[artifact.ID]); override != "" {
		path, err := localPathFromURI(override)
		return artifact, path, err
	}
	if resultArtifactEncrypted(result, artifact.ID) {
		return model.ArtifactRef{}, "", fmt.Errorf("raw recording %s is encrypted; provide its downloaded and decrypted local path in artifact_paths", artifact.ID)
	}
	if localPath, ok := artifact.Metadata["local_path"].(string); ok && strings.TrimSpace(localPath) != "" {
		path, err := localPathFromURI(localPath)
		return artifact, path, err
	}
	path, err := localPathFromURI(artifact.URI)
	if err != nil {
		return model.ArtifactRef{}, "", fmt.Errorf("raw recording %s is not locally readable: %w", artifact.ID, err)
	}
	return artifact, path, nil
}

func isRawRecordingArtifact(artifact model.ArtifactRef) bool {
	if strings.EqualFold(strings.TrimSpace(artifact.Kind), "raw_recording") {
		return true
	}
	role, _ := artifact.Metadata["asset_role"].(string)
	return strings.EqualFold(strings.TrimSpace(role), "raw_recording")
}

func resultArtifactEncrypted(result *model.RecordingResultPackage, artifactID string) bool {
	for _, descriptor := range result.Delivery.AssetRefs {
		if descriptor.ID == artifactID {
			return descriptor.Encrypted
		}
	}
	return false
}

func localPathFromURI(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("local path is empty")
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return "", err
	}
	if parsed.Scheme == "" || (runtime.GOOS == "windows" && len(parsed.Scheme) == 1) {
		return filepath.Abs(filepath.Clean(value))
	}
	if !strings.EqualFold(parsed.Scheme, "file") {
		return "", fmt.Errorf("unsupported URI scheme %q; download the artifact first", parsed.Scheme)
	}
	decoded, err := url.PathUnescape(parsed.Path)
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "windows" && len(parsed.Host) == 2 && parsed.Host[1] == ':' {
		decoded = parsed.Host + decoded
	} else if parsed.Host != "" && !strings.EqualFold(parsed.Host, "localhost") {
		decoded = "//" + parsed.Host + decoded
	}
	if runtime.GOOS == "windows" && len(decoded) >= 3 && decoded[0] == '/' && decoded[2] == ':' {
		decoded = decoded[1:]
	}
	return filepath.Abs(filepath.Clean(filepath.FromSlash(decoded)))
}

func applyResultPackageToEditorSession(session *model.EditorSession, result *model.RecordingResultPackage, source model.ArtifactRef, request model.EditorCreateFromResultPackageRequest) {
	traceID := ""
	workflowGraphID := session.AssetCatalog.WorkflowGraphID
	traceStartedAt := time.Time{}
	steps := result.StepResults
	if result.ExecutionTrace != nil {
		traceID = result.ExecutionTrace.ID
		traceStartedAt = result.ExecutionTrace.StartedAt
		if result.ExecutionTrace.WorkflowGraphID != "" {
			workflowGraphID = result.ExecutionTrace.WorkflowGraphID
		}
		if len(steps) == 0 {
			steps = result.ExecutionTrace.StepResults
		}
	}
	session.AssetCatalog.Source.RecordingResultPackageID = result.ResultID
	session.AssetCatalog.Source.ExecutionTraceID = traceID
	session.AssetCatalog.WorkflowGraphID = workflowGraphID
	session.AssetCatalog.RunID = firstNonEmptyString(traceID, result.CloudJobID, session.SessionID)
	session.Automation = editorAutomationSummary(result)
	asset := &session.AssetCatalog.Artifacts[0]
	if asset.Metadata == nil {
		asset.Metadata = map[string]any{}
	}
	asset.Metadata["source"] = "recording_result_package"
	asset.Metadata["recording_result_package_id"] = result.ResultID
	asset.Metadata["source_artifact_id"] = source.ID
	asset.Metadata["source_artifact_uri"] = source.URI
	asset.Metadata["declared_sha256"] = source.SHA256
	appendResultScreenshotAssets(session, result, request, source.ID)
	appendResultPresentationCandidateAssets(session, result, request, source.ID)

	timelineSteps := make([]model.TimelineStep, 0, len(steps))
	shots := make([]model.DemoEditShot, 0, len(steps))
	cursor := 0
	for index, step := range steps {
		startMS, endMS := editorStepRange(step, traceStartedAt, cursor, asset.DurationMS)
		cursor = maxInt(cursor, endMS)
		artifactIDs := make([]string, 0, len(step.Artifacts))
		for _, artifact := range step.Artifacts {
			artifactIDs = append(artifactIDs, artifact.ID)
		}
		timelineSteps = append(timelineSteps, model.TimelineStep{
			StepID: step.NodeID, Order: index + 1, Action: step.NodeID, Status: step.Status, Required: true,
			StartMS: startMS, EndMS: endMS, DurationMS: maxInt(0, endMS-startMS), ObservedState: step.ObservedState, Artifacts: artifactIDs,
		})
		if endMS <= startMS {
			continue
		}
		purpose := firstNonEmptyString(step.ObservedState, step.NodeID)
		shot := model.DemoEditShot{
			ID: fmt.Sprintf("shot_%s_%03d", session.SessionID, index+1), SourceArtifactID: asset.ID, SourceStepID: step.NodeID,
			SourceTimeRangeMS: &model.MillisecondRange{startMS, endMS}, Purpose: purpose,
			Operations: []model.EditOperation{{Type: model.EditOperationTrim}}, Overlays: []model.EditOverlay{},
		}
		if strings.TrimSpace(step.ObservedState) != "" {
			captionStart, captionEnd := 0, minInt(3000, endMS-startMS)
			shot.Overlays = append(shot.Overlays, model.EditOverlay{Type: model.EditOverlayCaption, Text: step.ObservedState, SourceStepID: step.NodeID, StartMS: &captionStart, EndMS: &captionEnd})
		}
		shots = append(shots, shot)
	}
	session.AssetCatalog.Steps = timelineSteps
	if len(shots) > 0 {
		session.EditPlan.Shots = shots
	}
	session.EditPlan.TargetDurationMS = editorPlanDuration(session.EditPlan)
}

func editorAutomationSummary(result *model.RecordingResultPackage) *model.EditorAutomationSummary {
	if result == nil {
		return nil
	}
	summary := &model.EditorAutomationSummary{
		SourcePackageID:          result.SourcePackageID,
		ExecutionRuntime:         result.ExecutionRuntime,
		ValidationState:          "result_only",
		ValidationReportCount:    len(result.ValidationReports),
		PatchCount:               len(result.PatchLedger),
		StageEventAuditAvailable: result.StageEventLogRef != nil,
	}
	if len(result.ValidationReports) > 0 {
		summary.ValidationState = "verified"
	}
	for _, report := range result.ValidationReports {
		summary.LatestDecision = report.Decision
		if realEditorValidationEvidence(report) {
			summary.EvidenceBackedReportCount++
		}
		switch report.Decision {
		case model.ValidationDecisionStopAndReport, model.ValidationDecisionReunderstandingRequired:
			summary.ValidationState = "blocked"
		case model.ValidationDecisionRepairAllowed:
			if summary.ValidationState != "blocked" {
				summary.ValidationState = "repaired_or_review"
			}
		}
	}
	for _, entry := range result.PatchLedger {
		if entry.Applied {
			summary.AppliedPatchCount++
		}
		if entry.RolledBack {
			summary.RolledBackPatchCount++
		}
	}
	if summary.ExecutionRuntime == "" && summary.ValidationReportCount == 0 && summary.PatchCount == 0 {
		summary.ValidationState = "legacy_result"
	}
	return summary
}

func realEditorValidationEvidence(report model.ValidationReport) bool {
	if len(report.EvidenceRefs) == 0 {
		return false
	}
	switch report.EvidenceQuality {
	case model.RuntimeObservationActualBrowser, model.RuntimeObservationAssertion, model.RuntimeObservationArtifact:
		return true
	default:
		return false
	}
}

func appendResultScreenshotAssets(session *model.EditorSession, result *model.RecordingResultPackage, request model.EditorCreateFromResultPackageRequest, recordingArtifactID string) {
	type candidate struct {
		artifact model.ArtifactRef
		stepID   string
	}
	seen := map[string]bool{}
	candidates := make([]candidate, 0)
	add := func(artifact model.ArtifactRef, stepID string) {
		if artifact.ID == "" || artifact.ID == recordingArtifactID || seen[artifact.ID] || !isEditorScreenshotArtifact(artifact) || artifact.Sensitive || isDiagnosticScreenshotArtifact(artifact) {
			return
		}
		seen[artifact.ID] = true
		candidates = append(candidates, candidate{artifact: artifact, stepID: firstNonEmptyString(stepID, artifact.SourceNodeID)})
	}
	for _, artifact := range result.GeneratedAssets {
		add(artifact, "")
	}
	if result.ExecutionTrace != nil {
		for _, artifact := range result.ExecutionTrace.Artifacts {
			add(artifact, "")
		}
	}
	for _, step := range result.StepResults {
		for _, artifact := range step.Artifacts {
			add(artifact, step.NodeID)
		}
	}
	for _, item := range candidates {
		path, err := resolveResultReferenceArtifact(result, request, item.artifact)
		if err != nil {
			continue
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		metadata := map[string]any{
			"source":              "recording_result_package",
			"source_artifact_id":  item.artifact.ID,
			"source_artifact_uri": item.artifact.URI,
			"presentation_only":   true,
		}
		for key, value := range item.artifact.Metadata {
			metadata[key] = value
		}
		session.AssetCatalog.Artifacts = append(session.AssetCatalog.Artifacts, model.TimelineArtifact{
			ID: item.artifact.ID, Kind: "step_screenshot", URI: localFileURI(path), MimeType: item.artifact.MimeType,
			Label: firstNonEmptyString(item.artifact.Label, item.artifact.ID), SHA256: item.artifact.SHA256, SizeBytes: info.Size(),
			SourceStepID: item.stepID, AssetRole: "presentation_reference", IncludeInDemo: editorArtifactIncludeInDemo(item.artifact),
			Metadata: metadata, LocalPath: path,
		})
	}
}

func appendResultPresentationCandidateAssets(session *model.EditorSession, result *model.RecordingResultPackage, request model.EditorCreateFromResultPackageRequest, recordingArtifactID string) {
	seen := map[string]bool{}
	add := func(artifact model.ArtifactRef) {
		if artifact.ID == "" || artifact.ID == recordingArtifactID || seen[artifact.ID] || artifact.Kind != "generated_video_candidate" || artifact.Sensitive || artifact.SourceNodeID != "" {
			return
		}
		seen[artifact.ID] = true
		path, err := resolveResultReferenceArtifact(result, request, artifact)
		if err != nil {
			return
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			return
		}
		metadata := presentationCandidateMetadataForEditor(artifact.Metadata)
		session.AssetCatalog.Artifacts = append(session.AssetCatalog.Artifacts, model.TimelineArtifact{
			ID: artifact.ID, Kind: artifact.Kind, URI: localFileURI(path), MimeType: artifact.MimeType,
			Label: firstNonEmptyString(artifact.Label, artifact.ID), SHA256: artifact.SHA256, SizeBytes: info.Size(),
			AssetRole: "presentation_candidate", IncludeInDemo: false, Metadata: metadata,
			DurationMS: editorMetadataPositiveInt(metadata, "duration_ms", "target_duration_ms"), LocalPath: path,
		})
	}
	for _, artifact := range result.GeneratedAssets {
		add(artifact)
	}
	if result.ExecutionTrace != nil {
		for _, artifact := range result.ExecutionTrace.Artifacts {
			add(artifact)
		}
	}
}

func presentationCandidateMetadataForEditor(source map[string]any) map[string]any {
	metadata := map[string]any{
		"approved_for_demo": false, "include_in_demo": false, "non_authoritative": true,
		"presentation_only": true, "source_material_policy": "non_authoritative_generated_candidate",
		"explicit_review_required": true,
	}
	for _, key := range []string{"media_eligible", "duration_ms", "target_duration_ms", "width", "height", "fps", "video_codec", "audio_codec", "download_status"} {
		if value, ok := source[key]; ok {
			metadata[key] = value
		}
	}
	return metadata
}

func editorMetadataPositiveInt(metadata map[string]any, keys ...string) int {
	for _, key := range keys {
		switch value := metadata[key].(type) {
		case int:
			if value > 0 {
				return value
			}
		case float64:
			if value > 0 {
				return int(value)
			}
		}
	}
	return 0
}

func resolveResultReferenceArtifact(result *model.RecordingResultPackage, request model.EditorCreateFromResultPackageRequest, artifact model.ArtifactRef) (string, error) {
	if override := strings.TrimSpace(request.ArtifactPaths[artifact.ID]); override != "" {
		return localPathFromURI(override)
	}
	if resultArtifactEncrypted(result, artifact.ID) {
		return "", fmt.Errorf("artifact %s is encrypted", artifact.ID)
	}
	if localPath, ok := artifact.Metadata["local_path"].(string); ok && strings.TrimSpace(localPath) != "" {
		return localPathFromURI(localPath)
	}
	return localPathFromURI(artifact.URI)
}

func isEditorScreenshotArtifact(artifact model.ArtifactRef) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(artifact.MimeType)), "image/") || strings.Contains(strings.ToLower(artifact.Kind), "screenshot")
}

func isDiagnosticScreenshotArtifact(artifact model.ArtifactRef) bool {
	role, _ := artifact.Metadata["asset_role"].(string)
	return strings.Contains(strings.ToLower(artifact.Kind+" "+role), "failure") || strings.Contains(strings.ToLower(role), "diagnostic")
}

func editorArtifactIncludeInDemo(artifact model.ArtifactRef) bool {
	if value, ok := artifact.Metadata["include_in_demo"].(bool); ok {
		return value
	}
	return true
}

func editorStepRange(step model.StepResult, traceStartedAt time.Time, cursor, recordingDuration int) (int, int) {
	startMS := cursor
	if !traceStartedAt.IsZero() && !step.StartedAt.IsZero() {
		startMS = maxInt(0, int(step.StartedAt.Sub(traceStartedAt)/time.Millisecond))
	}
	endMS := 0
	if !traceStartedAt.IsZero() && !step.CompletedAt.IsZero() {
		endMS = maxInt(startMS, int(step.CompletedAt.Sub(traceStartedAt)/time.Millisecond))
	}
	durationMS := step.DurationMS
	if durationMS <= 0 && !step.StartedAt.IsZero() && !step.CompletedAt.IsZero() {
		durationMS = maxInt(0, int(step.CompletedAt.Sub(step.StartedAt)/time.Millisecond))
	}
	if endMS <= startMS && durationMS > 0 {
		endMS = startMS + durationMS
	}
	if recordingDuration > 0 {
		startMS = minInt(startMS, recordingDuration)
		endMS = minInt(maxInt(startMS, endMS), recordingDuration)
	}
	return startMS, endMS
}

func isFullSHA256(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}

func newEditorID(prefix string) (string, error) {
	value := make([]byte, 8)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return prefix + "_" + hex.EncodeToString(value), nil
}

func localFileURI(filePath string) string {
	path := filepath.ToSlash(filepath.Clean(filePath))
	// A file URI has an empty authority. On Windows, url.URL would otherwise
	// serialize C:/... as file://C:/..., treating the drive letter as a host;
	// prefixing the absolute slash preserves file:///C:/... and allows strict
	// local-artifact parsing to reject actual remote file authorities.
	if filepath.IsAbs(filePath) && !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return (&url.URL{Scheme: "file", Path: path}).String()
}

func editorPlanDuration(plan model.DemoEditPlan) int {
	total := 0
	for _, shot := range plan.Shots {
		if shot.SourceTimeRangeMS != nil {
			total += maxInt(0, (*shot.SourceTimeRangeMS)[1]-(*shot.SourceTimeRangeMS)[0])
		}
	}
	return total
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}
