package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"cascade-demoops/backend/internal/executor"
	"cascade-demoops/backend/internal/experiment"
	"cascade-demoops/backend/internal/model"
)

type closedLoopCaptureBatch struct {
	Result    model.RecordingResultPackage
	Downloads []CloudDeliverableDownloadResult
}

type closedLoopChapterSpan struct {
	Chapter string `json:"chapter"`
	StartMS int    `json:"start_ms"`
	EndMS   int    `json:"end_ms"`
}

type closedLoopEditorMaterialization struct {
	Session   model.EditorSession
	Downloads []CloudDeliverableDownloadResult
}

type closedLoopImportedBatch struct {
	artifact model.TimelineArtifact
	spans    map[string]closedLoopChapterSpan
}

func (a *appExperimentExecutionAdapter) buildClosedLoopEditorSession(ctx context.Context, request experiment.LegExecutionRequest, batches []closedLoopCaptureBatch) (closedLoopEditorMaterialization, error) {
	if len(batches) == 0 {
		return closedLoopEditorMaterialization{}, errors.New("closed-loop editor requires at least one captured run")
	}
	allDownloads := []CloudDeliverableDownloadResult{}
	for _, batch := range batches {
		allDownloads = append(allDownloads, batch.Downloads...)
	}
	if sessions, listErr := a.service.ListEditorSessions(ctx); listErr == nil {
		for _, existing := range sessions {
			if existing.AssetCatalog.RunID == request.RunID && closedLoopEditorHasSevenChapters(existing) {
				return closedLoopEditorMaterialization{Session: existing, Downloads: allDownloads}, nil
			}
		}
	}
	session, err := a.service.CreateEditorSession(ctx, model.EditorCreateSessionRequest{Name: request.ProjectName + " 闭环事实轨"})
	if err != nil {
		return closedLoopEditorMaterialization{}, err
	}
	completed := false
	defer func() {
		if !completed {
			a.service.removeEditorSession(session.SessionID)
		}
	}()
	imports := make([]closedLoopImportedBatch, 0, len(batches))
	for _, batch := range batches {
		raw, ok := largestClosedLoopDownload(batch.Downloads, "raw_recording")
		if !ok {
			return closedLoopEditorMaterialization{}, errors.New("closed-loop captured run has no complete raw recording")
		}
		spans, err := readClosedLoopChapterSpans(batch.Downloads)
		if err != nil {
			return closedLoopEditorMaterialization{}, err
		}
		updated, err := a.service.ImportEditorAsset(ctx, session.SessionID, model.EditorImportAssetRequest{Path: raw.LocalPath, Label: "事实录制 " + batch.Result.ResultID})
		if err != nil {
			return closedLoopEditorMaterialization{}, err
		}
		artifact, ok := editorArtifactForLocalPath(updated.AssetCatalog.Artifacts, raw.LocalPath)
		if !ok {
			return closedLoopEditorMaterialization{}, errors.New("imported closed-loop recording was not found in the editor catalog")
		}
		imports = append(imports, closedLoopImportedBatch{artifact: artifact, spans: spans})
		session = updated
	}

	a.service.editorMu.Lock()
	defer a.service.editorMu.Unlock()
	latest, err := a.service.loadEditorSession(session.SessionID)
	if err != nil {
		return closedLoopEditorMaterialization{}, err
	}
	latest.AssetCatalog.Steps = nil
	latest.EditPlan.Shots = nil
	cursor := 0
	chapters := model.RequiredDemoChapters()
	for index, chapter := range chapters {
		batchIndex, span, found := selectClosedLoopChapter(imports, chapter)
		if !found {
			return closedLoopEditorMaterialization{}, fmt.Errorf("required closed-loop chapter %s is missing", chapter)
		}
		artifact := imports[batchIndex].artifact
		start, end := maxInt(0, span.StartMS), minInt(artifact.DurationMS, span.EndMS)
		if end <= start {
			return closedLoopEditorMaterialization{}, fmt.Errorf("required closed-loop chapter %s has an invalid source range", chapter)
		}
		duration := end - start
		stepID := "closed_loop_" + chapter
		latest.AssetCatalog.Steps = append(latest.AssetCatalog.Steps, model.TimelineStep{
			StepID: stepID, Order: index + 1, Action: chapter, Status: "passed", Required: true,
			StartMS: cursor, EndMS: cursor + duration, DurationMS: duration, Artifacts: []string{artifact.ID},
		})
		sourceRange := model.MillisecondRange{start, end}
		latest.EditPlan.Shots = append(latest.EditPlan.Shots, model.DemoEditShot{
			ID: "shot_" + stepID, SourceArtifactID: artifact.ID, SourceStepID: stepID, SourceTimeRangeMS: &sourceRange,
			Purpose: chapter, Operations: []model.EditOperation{{Type: model.EditOperationTrim}}, Overlays: []model.EditOverlay{},
		})
		cursor += duration
	}
	latest.AssetCatalog.Timeline.DurationMS = cursor
	latest.AssetCatalog.Timeline.RecordingArtifactID = imports[0].artifact.ID
	latest.AssetCatalog.Source.RecordingResultPackageID = batches[len(batches)-1].Result.ResultID
	latest.AssetCatalog.Source.GeneratedAt = time.Now().UTC()
	latest.AssetCatalog.RunID = request.RunID
	latest.EditPlan.TargetDurationMS = editorPlanDuration(latest.EditPlan)
	latest.EditPlan.Objective = request.BuildPrompt
	latest.Automation = editorAutomationSummary(&batches[len(batches)-1].Result)
	latest.Revision++
	latest.UpdatedAt = time.Now().UTC()
	validation, validateErr := a.service.editorWorker.ValidateEditPlan(ctx, executor.EditPlanValidationRequest{Catalog: latest.AssetCatalog, EditPlan: latest.EditPlan})
	if validateErr != nil {
		return closedLoopEditorMaterialization{}, validateErr
	}
	latest.Validation = &validation
	if !validation.Valid {
		return closedLoopEditorMaterialization{}, errors.New("closed-loop chapter edit plan did not pass validation")
	}
	if err := a.service.saveEditorSessionUnlocked(latest); err != nil {
		return closedLoopEditorMaterialization{}, err
	}
	completed = true
	return closedLoopEditorMaterialization{Session: latest, Downloads: allDownloads}, nil
}

func closedLoopEditorHasSevenChapters(session model.EditorSession) bool {
	chapters := model.RequiredDemoChapters()
	if len(session.AssetCatalog.Steps) != len(chapters) || len(session.EditPlan.Shots) != len(chapters) {
		return false
	}
	for index, chapter := range chapters {
		if session.AssetCatalog.Steps[index].StepID != "closed_loop_"+chapter || !session.AssetCatalog.Steps[index].Required {
			return false
		}
	}
	return true
}

func readClosedLoopChapterSpans(downloads []CloudDeliverableDownloadResult) (map[string]closedLoopChapterSpan, error) {
	manifests := []CloudDeliverableDownloadResult{}
	for _, download := range downloads {
		if download.Kind == "recording_segment_manifest" {
			manifests = append(manifests, download)
		}
	}
	if len(manifests) != 1 {
		return nil, errors.New("closed-loop captured run requires exactly one recording segment manifest")
	}
	raw, err := os.ReadFile(manifests[0].LocalPath)
	if err != nil {
		return nil, err
	}
	var manifest struct {
		SchemaVersion string                  `json:"schema_version"`
		RolloverMS    int                     `json:"rollover_ms"`
		ChapterSpans  []closedLoopChapterSpan `json:"chapter_spans"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil || manifest.SchemaVersion != "demoops.browser_recording_segments.v1" || manifest.RolloverMS != 120_000 {
		return nil, errors.New("closed-loop recording segment manifest is invalid")
	}
	result := map[string]closedLoopChapterSpan{}
	for _, span := range manifest.ChapterSpans {
		chapter := strings.TrimSpace(span.Chapter)
		if span.EndMS <= span.StartMS || chapter == "" {
			continue
		}
		current, ok := result[chapter]
		if !ok {
			result[chapter] = span
			continue
		}
		current.StartMS = minInt(current.StartMS, span.StartMS)
		current.EndMS = maxInt(current.EndMS, span.EndMS)
		result[chapter] = current
	}
	return result, nil
}

func selectClosedLoopChapter(imports []closedLoopImportedBatch, chapter string) (int, closedLoopChapterSpan, bool) {
	if len(imports) == 0 {
		return 0, closedLoopChapterSpan{}, false
	}
	// Login through initial submission are once-only evidence and must always
	// come from the first continuous capture. Re-recordable result chapters use
	// the newest successful repair capture that actually contains the chapter.
	if chapter == "login" || chapter == "creation" || chapter == "prompt_input" || chapter == "submission" {
		span, ok := imports[0].spans[chapter]
		return 0, span, ok
	}
	for index := len(imports) - 1; index >= 0; index-- {
		if span, ok := imports[index].spans[chapter]; ok {
			return index, span, true
		}
	}
	return 0, closedLoopChapterSpan{}, false
}

func largestClosedLoopDownload(downloads []CloudDeliverableDownloadResult, kind string) (CloudDeliverableDownloadResult, bool) {
	var candidates []CloudDeliverableDownloadResult
	for _, download := range downloads {
		if download.Kind == kind && strings.TrimSpace(download.LocalPath) != "" {
			candidates = append(candidates, download)
		}
	}
	if len(candidates) == 0 {
		return CloudDeliverableDownloadResult{}, false
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].SizeBytes > candidates[j].SizeBytes })
	return candidates[0], true
}

func editorArtifactForLocalPath(artifacts []model.TimelineArtifact, pathValue string) (model.TimelineArtifact, bool) {
	want, _ := filepath.Abs(filepath.Clean(pathValue))
	for _, artifact := range artifacts {
		got, _ := filepath.Abs(filepath.Clean(artifact.LocalPath))
		if strings.EqualFold(want, got) {
			return artifact, true
		}
	}
	return model.TimelineArtifact{}, false
}
