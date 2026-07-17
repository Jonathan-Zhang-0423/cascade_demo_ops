package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/executor"
	"cascade-demoops/backend/internal/model"
)

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
		},
		PreviewProfile:       model.EditorRenderProfile{Mode: "preview", Width: 1280, Height: 720, FPS: 30, Format: "mp4", Preset: "ultrafast", CRF: 28},
		FinalProfile:         model.EditorRenderProfile{Mode: "final", Width: 1920, Height: 1080, FPS: 30, Format: "mp4", Preset: "medium", CRF: 21},
		Preview:              model.EditorRenderState{Status: model.EditorRenderStatusNotStarted},
		FinalRender:          model.EditorRenderState{Status: model.EditorRenderStatusNotStarted},
		ProviderCapabilities: s.editorProviderCapabilities(),
	}
	if err := s.saveEditorSession(session); err != nil {
		return model.EditorSession{}, err
	}
	if strings.TrimSpace(request.SourcePath) != "" {
		return s.ImportEditorAsset(ctx, sessionID, model.EditorImportAssetRequest{Path: request.SourcePath})
	}
	return session, nil
}

func (s *Service) ListEditorSessions(_ context.Context) ([]model.EditorSession, error) {
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
	session.AssetCatalog.Artifacts = append(session.AssetCatalog.Artifacts, artifact)
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
	request.EditPlan.TargetDurationMS = editorPlanDuration(request.EditPlan)
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
	s.editorMu.Lock()
	session, err := s.loadEditorSession(sessionID)
	if err != nil {
		s.editorMu.Unlock()
		return model.EditorSession{}, err
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
	renderState := model.EditorRenderState{Status: model.EditorRenderStatusRunning, Revision: session.Revision, StartedAt: now}
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
		OutputDir:       outputDir,
		DurationSec:     maxInt(1, (session.EditPlan.TargetDurationMS+999)/1000),
		GeneratedAssets: assets,
		EditPlan:        &session.EditPlan,
		RenderProfile:   &profile,
	})

	s.editorMu.Lock()
	defer s.editorMu.Unlock()
	latest, loadErr := s.loadEditorSession(sessionID)
	if loadErr != nil {
		return model.EditorSession{}, loadErr
	}
	completed := time.Now().UTC()
	state := model.EditorRenderState{Revision: session.Revision, StartedAt: now, CompletedAt: completed}
	if renderErr != nil {
		state.Status = model.EditorRenderStatusFailed
		state.Error = renderErr.Error()
		latest.Status = model.EditorSessionStatusFailed
	} else {
		state.Status = model.EditorRenderStatusReady
		state.VideoPath = result.VideoPath
		state.RenderManifestPath = result.RenderManifestPath
		latest.Status = model.EditorSessionStatusReady
	}
	if preview {
		latest.Preview = state
	} else {
		latest.FinalRender = state
	}
	latest.UpdatedAt = completed
	if err := s.saveEditorSessionUnlocked(latest); err != nil {
		return model.EditorSession{}, err
	}
	return latest, renderErr
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
	return session, nil
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

func newEditorID(prefix string) (string, error) {
	value := make([]byte, 8)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return prefix + "_" + hex.EncodeToString(value), nil
}

func localFileURI(filePath string) string {
	path := filepath.ToSlash(filepath.Clean(filePath))
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
