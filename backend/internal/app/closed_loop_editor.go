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
	Chapter      string `json:"chapter"`
	SourceNodeID string `json:"source_node_id,omitempty"`
	StartMS      int    `json:"start_ms"`
	EndMS        int    `json:"end_ms"`
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
		start, end, speed := closedLoopChapterSourceWindow(chapter, span, imports[batchIndex].spans, artifact.DurationMS)
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
		operations := []model.EditOperation{{Type: model.EditOperationTrim}}
		if speed > 1 {
			operations = append(operations, model.EditOperation{Type: model.EditOperationSpeed, Speed: &speed})
		}
		latest.EditPlan.Shots = append(latest.EditPlan.Shots, model.DemoEditShot{
			ID: "shot_" + stepID, SourceArtifactID: artifact.ID, SourceStepID: stepID, SourceTimeRangeMS: &sourceRange,
			Purpose: chapter, Operations: operations, Overlays: []model.EditOverlay{},
		})
		cursor += duration
	}
	latest.AssetCatalog.Timeline.DurationMS = cursor
	latest.AssetCatalog.Timeline.RecordingArtifactID = imports[0].artifact.ID
	latest.AssetCatalog.Source.RecordingResultPackageID = batches[len(batches)-1].Result.ResultID
	latest.AssetCatalog.Source.GeneratedAt = time.Now().UTC()
	latest.AssetCatalog.RunID = request.RunID
	latest.EditPlan.TargetDurationMS = closedLoopOutputDuration(latest.EditPlan)
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
	// A closed-loop fact track is an edited review baseline, not a lossless copy
	// of a potentially thirty-minute build recording. Older sessions merged
	// repeated chapter spans into multi-minute overlapping ranges; never reuse
	// one of those sessions for a new FinalFilm revision.
	duration := closedLoopOutputDuration(session.EditPlan)
	return duration >= 30_000 && duration <= 120_000
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
	candidates := map[string][]closedLoopChapterSpan{}
	for _, span := range manifest.ChapterSpans {
		chapter := normalizedClosedLoopChapter(span)
		if span.EndMS <= span.StartMS || chapter == "" {
			continue
		}
		span.Chapter = chapter
		candidates[chapter] = append(candidates[chapter], span)
	}
	result := map[string]closedLoopChapterSpan{}
	for chapter, spans := range candidates {
		if selected, ok := selectClosedLoopManifestSpan(chapter, spans); ok {
			result[chapter] = selected
		}
	}
	return result, nil
}

func normalizedClosedLoopChapter(span closedLoopChapterSpan) string {
	chapter := strings.TrimSpace(span.Chapter)
	node := strings.ToLower(strings.TrimSpace(span.SourceNodeID))
	// The worker may initially classify a long-running action by words found in
	// its expected outcome. Prefer the observed stage's semantic role when it is
	// available. These roles are workflow concepts and are not tied to a host,
	// route, selector, product name, or visual style.
	switch {
	case strings.Contains(node, "session_setup") || strings.Contains(node, "auth") || strings.Contains(node, "login"):
		return "login"
	case strings.Contains(node, "new_project_entry") || strings.Contains(node, "creation_open"):
		return "creation"
	case strings.Contains(node, "prompt_input") || strings.Contains(node, "project_name_input") || strings.Contains(node, "product_repair_input"):
		return "prompt_input"
	case strings.Contains(node, "start_agent_build") || strings.Contains(node, "submit_product_repair") || strings.Contains(node, "continue_prepared_execution"):
		return "submission"
	case strings.Contains(node, "observe_agent_progress") || strings.Contains(node, "final_observe"):
		return "build_wait"
	case strings.Contains(node, "interaction_surface_ready"):
		return "result_reveal"
	case strings.Contains(node, "interaction_"):
		return "interaction"
	default:
		return chapter
	}
}

func selectClosedLoopManifestSpan(chapter string, spans []closedLoopChapterSpan) (closedLoopChapterSpan, bool) {
	if len(spans) == 0 {
		return closedLoopChapterSpan{}, false
	}
	sort.SliceStable(spans, func(i, j int) bool { return spans[i].StartMS < spans[j].StartMS })
	if chapter == "interaction" {
		selected := spans[0]
		for _, span := range spans[1:] {
			selected.StartMS = minInt(selected.StartMS, span.StartMS)
			selected.EndMS = maxInt(selected.EndMS, span.EndMS)
		}
		return selected, true
	}
	if chapter == "result_reveal" {
		selected := spans[0]
		for _, span := range spans[1:] {
			if span.EndMS-span.StartMS > selected.EndMS-selected.StartMS {
				selected = span
			}
		}
		return selected, true
	}
	if chapter == "submission" {
		for _, span := range spans {
			node := strings.ToLower(span.SourceNodeID)
			if strings.Contains(node, "start_agent_build") || strings.Contains(node, "submit_product_repair") {
				return span, true
			}
		}
	}
	return spans[0], true
}

func closedLoopChapterSourceWindow(chapter string, span closedLoopChapterSpan, all map[string]closedLoopChapterSpan, sourceDurationMS int) (int, int, float64) {
	start, end := maxInt(0, span.StartMS), minInt(sourceDurationMS, span.EndMS)
	clipTail := func(maxDurationMS int) {
		if end-start > maxDurationMS {
			start = end - maxDurationMS
		}
	}
	speed := 1.0
	switch chapter {
	case "login", "creation":
		clipTail(8_000)
	case "prompt_input":
		// The tail preserves the fully entered one-sentence request rather than
		// spending the film on every keystroke.
		clipTail(12_000)
	case "submission", "result_reveal":
		clipTail(8_000)
	case "interaction":
		// Keep a single continuous proof session while bounding the final film.
		clipTail(65_000)
	case "build_wait":
		// A preview-readiness action often owns the real asynchronous wait and is
		// only labelled result_reveal when it finally returns. Borrow the window
		// immediately before its stable reveal, then compress it deterministically.
		if reveal, ok := all["result_reveal"]; ok && reveal.EndMS-reveal.StartMS > 24_000 {
			end = minInt(sourceDurationMS, reveal.EndMS-8_000)
			start = maxInt(reveal.StartMS, end-96_000)
		}
		if end-start > 12_000 {
			speed = 12
		} else {
			clipTail(12_000)
		}
	}
	return start, end, speed
}

func closedLoopOutputDuration(plan model.DemoEditPlan) int {
	total := 0
	for _, shot := range plan.Shots {
		if shot.SourceTimeRangeMS == nil {
			continue
		}
		speed := 1.0
		for index := len(shot.Operations) - 1; index >= 0; index-- {
			operation := shot.Operations[index]
			if operation.Type == model.EditOperationSpeed && operation.Speed != nil && *operation.Speed > 0 {
				speed = *operation.Speed
				break
			}
		}
		total += int(float64((*shot.SourceTimeRangeMS)[1]-(*shot.SourceTimeRangeMS)[0]) / speed)
	}
	return total
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
