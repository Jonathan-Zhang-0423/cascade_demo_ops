package finalfilm

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"cascade-demoops/backend/internal/model"
)

func guidedDemoPresentationIntents(catalog model.AssetTimelineCatalog) []model.PresentationGenerationIntent {
	purposes := []string{"intro"}
	required := 0
	for _, step := range catalog.Steps {
		if step.Required {
			required++
		}
	}
	if required >= 2 {
		purposes = append(purposes, "section_divider")
	}
	if required >= 6 {
		purposes = append(purposes, "section_divider")
	}
	purposes = append(purposes, "outro")
	result := make([]model.PresentationGenerationIntent, 0, len(purposes))
	divider := 0
	for _, purpose := range purposes {
		id := "guided_" + purpose
		if purpose == "section_divider" {
			divider++
			id = fmt.Sprintf("guided_section_divider_%02d", divider)
		}
		intent, _ := model.PresentationGenerationIntentDefaults(id, purpose, nil)
		intent.RequestedSlot.PreferredDurationSec = 4
		result = append(result, intent)
	}
	return result
}

func buildDirectorEvidenceDigest(job model.FinalFilmJob, sourceAudioPresent bool, now time.Time) (model.DirectorEvidenceDigest, error) {
	steps := make([]model.TimelineStep, 0, len(job.Catalog.Steps))
	for _, step := range job.Catalog.Steps {
		if step.Required {
			steps = append(steps, step)
		}
	}
	sort.SliceStable(steps, func(i, j int) bool { return steps[i].Order < steps[j].Order })
	digest := model.DirectorEvidenceDigest{
		SchemaVersion: model.DirectorEvidenceDigestSchemaVersion,
		Objective:     strings.TrimSpace(job.BaselinePlan.Objective), SourceDurationMS: job.Catalog.Timeline.DurationMS,
		VisualStyle: model.DirectorVisualStyleEvidence{DominantColors: dominantColorsFromCatalog(job.Catalog), Tone: "observed_product", MotionLevel: "derived_from_fact_track"},
		Audio:       model.DirectorAudioEvidence{SourceAudioPresent: sourceAudioPresent}, CreatedAt: now.UTC(),
	}
	if digest.Objective == "" {
		digest.Objective = "Present the verified product workflow and its observed result."
	}
	activeMS := 0
	previousEnd := 0
	for index, step := range steps {
		start, end := maxInt(0, step.StartMS), maxInt(step.StartMS+1, step.EndMS)
		if start > previousEnd {
			digest.WaitRanges = append(digest.WaitRanges, model.MillisecondRange{previousEnd, start})
		}
		previousEnd = maxInt(previousEnd, end)
		activeMS += end - start
		artifacts := append([]string{}, step.Artifacts...)
		if len(artifacts) == 0 {
			for _, artifact := range job.Catalog.Artifacts {
				if artifact.SourceStepID == step.StepID || artifact.ID == job.Catalog.Timeline.RecordingArtifactID {
					artifacts = append(artifacts, artifact.ID)
				}
			}
		}
		digest.RequiredSteps = append(digest.RequiredSteps, model.DirectorEvidenceStep{
			StepID: step.StepID, Order: index + 1, Action: step.Action, ExpectedOutcome: step.ExpectedOutcome,
			ObservedState: step.ObservedState, SourceRangeMS: model.MillisecondRange{start, end}, ArtifactIDs: uniqueStrings(artifacts),
		})
	}
	if previousEnd < digest.SourceDurationMS {
		digest.WaitRanges = append(digest.WaitRanges, model.MillisecondRange{previousEnd, digest.SourceDurationMS})
	}
	if activeMS > 0 {
		digest.InteractionDensity = float64(len(digest.RequiredSteps)) / (float64(activeMS) / 1000)
	}
	seed := job.JobID + "\x00" + job.Constraints.ConstraintSetID + "\x00" + fmt.Sprint(digest.RequiredSteps)
	sum := sha256.Sum256([]byte(seed))
	digest.DigestID = "evidence_digest_" + hex.EncodeToString(sum[:10])
	if err := model.ValidateDirectorEvidenceDigest(digest); err != nil {
		return model.DirectorEvidenceDigest{}, err
	}
	return digest, nil
}

func buildDirectorStoryPlan(job model.FinalFilmJob, digest model.DirectorEvidenceDigest) (model.DirectorStoryPlan, error) {
	policy := model.DefaultFinalFilmAutomationPolicy()
	plan := model.DirectorStoryPlan{
		SchemaVersion: model.DirectorStoryPlanSchemaVersion,
		AudioPlan:     model.DirectorAudioPlan{TargetLUFS: -16, TruePeakDB: -1, PreserveSource: true, BackgroundMode: "source_or_silent"},
		SkillVersions: copyStringMap(policy.SkillVersions),
		DecisionLog:   []string{"Required steps remain in factual order.", "Wait-like observations use bounded 4-12x compression.", "Generated shots package chapters only and never replace verified UI."},
	}
	stepByID := map[string]model.DirectorEvidenceStep{}
	for _, step := range digest.RequiredSteps {
		stepByID[step.StepID] = step
	}
	generatedDuration := len(job.PresentationIntents) * 4000
	factDuration := 0
	for index, shot := range job.BaselinePlan.Shots {
		rangeMS := shot.SourceTimeRangeMS
		if rangeMS == nil {
			continue
		}
		speed := existingShotSpeed(shot)
		step := stepByID[shot.SourceStepID]
		// ObservedState contains generic provenance such as "url_observed" on
		// every successful browser step. Classifying on that field compressed the
		// entire factual interaction proof as if it were a wait. Only the action's
		// business meaning may opt a segment into wait compression.
		if waitLikeText(step.Action) && rangeMS[1]-rangeMS[0] >= 4000 {
			speed = maxFloat(speed, 8)
		}
		output := maxInt(1, int(float64(rangeMS[1]-rangeMS[0])/speed))
		factDuration += output
		plan.Timeline = append(plan.Timeline, model.DirectorTimelineSegment{
			SegmentID: fmt.Sprintf("fact_%03d", index+1), Kind: "fact", SourceArtifactID: shot.SourceArtifactID,
			SourceStepID: shot.SourceStepID, SourceRangeMS: rangeMS, Placement: "fact_order", Speed: speed,
			OutputDurationMS: output, Caption: strings.TrimSpace(step.ExpectedOutcome),
		})
	}
	anchorIndex := 0
	for index, intent := range job.PresentationIntents {
		placement := "between_sections"
		anchor := ""
		if intent.Purpose == "intro" {
			placement = "before_first_required_step"
		}
		if intent.Purpose == "outro" {
			placement = "after_last_required_step"
		}
		if intent.Purpose == "section_divider" && len(digest.RequiredSteps) > 0 {
			anchorIndex++
			position := anchorIndex * len(digest.RequiredSteps) / (countPurpose(job.PresentationIntents, "section_divider") + 1)
			if position >= len(digest.RequiredSteps) {
				position = len(digest.RequiredSteps) - 1
			}
			anchor = digest.RequiredSteps[position].StepID
		}
		plan.Timeline = append(plan.Timeline, model.DirectorTimelineSegment{
			SegmentID: fmt.Sprintf("generated_%02d", index+1), Kind: "generated_presentation", IntentID: intent.IntentID,
			Placement: placement, AnchorAfterStepID: anchor, Speed: 1, OutputDurationMS: intent.RequestedSlot.PreferredDurationSec * 1000,
		})
	}
	total := factDuration + generatedDuration
	// guided-demo-v1 aims at the center of the requested 100-110 second
	// product-demo window. The deterministic EDL compiler later reconciles the
	// exact accepted candidate set to this target using source-derived pacing.
	target := 105_000
	if total > target {
		target = total
	}
	total = target
	if total > policy.TargetDuration.MaxMS {
		total = policy.TargetDuration.MaxMS
	}
	plan.TargetDurationMS = total
	for _, step := range digest.RequiredSteps {
		plan.Beats = append(plan.Beats, model.DirectorStoryBeat{BeatID: "beat_" + step.StepID, Purpose: "verified_step", StepIDs: []string{step.StepID}, Caption: step.ExpectedOutcome, DurationMS: step.SourceRangeMS[1] - step.SourceRangeMS[0]})
	}
	if err := model.ValidateDirectorStoryPlan(plan, policy.TargetDuration); err != nil {
		return model.DirectorStoryPlan{}, err
	}
	return plan, nil
}

func dominantColorsFromCatalog(catalog model.AssetTimelineCatalog) []string {
	counts := map[uint32]int{}
	imagesRead := 0
	for _, artifact := range catalog.Artifacts {
		if imagesRead >= 8 || (!strings.HasPrefix(strings.ToLower(artifact.MimeType), "image/") && !strings.Contains(strings.ToLower(artifact.Kind), "screenshot")) {
			continue
		}
		imagePath := strings.TrimSpace(artifact.LocalPath)
		if imagePath == "" {
			imagePath = filePathFromLocalURI(artifact.URI)
		}
		file, err := os.Open(filepath.Clean(imagePath))
		if err != nil {
			continue
		}
		decoded, _, decodeErr := image.Decode(file)
		_ = file.Close()
		if decodeErr != nil {
			continue
		}
		imagesRead++
		bounds := decoded.Bounds()
		stepX, stepY := maxInt(1, bounds.Dx()/64), maxInt(1, bounds.Dy()/64)
		for y := bounds.Min.Y; y < bounds.Max.Y; y += stepY {
			for x := bounds.Min.X; x < bounds.Max.X; x += stepX {
				r, g, b, a := decoded.At(x, y).RGBA()
				if a < 0x8000 {
					continue
				}
				bucket := uint32((r>>12)<<8 | (g>>12)<<4 | (b >> 12))
				counts[bucket]++
			}
		}
	}
	type rankedColor struct {
		bucket uint32
		count  int
	}
	ranked := make([]rankedColor, 0, len(counts))
	for bucket, count := range counts {
		ranked = append(ranked, rankedColor{bucket: bucket, count: count})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].count != ranked[j].count {
			return ranked[i].count > ranked[j].count
		}
		return ranked[i].bucket < ranked[j].bucket
	})
	result := []string{}
	for _, color := range ranked {
		red, green, blue := ((color.bucket>>8)&0xF)*17, ((color.bucket>>4)&0xF)*17, (color.bucket&0xF)*17
		hexColor := fmt.Sprintf("#%02x%02x%02x", red, green, blue)
		result = append(result, hexColor)
		if len(result) == 3 {
			break
		}
	}
	return result
}
func waitLikeText(value string) bool {
	value = strings.ToLower(value)
	for _, token := range []string{"wait", "observe", "progress", "loading", "等待", "观察", "进度"} {
		if strings.Contains(value, token) {
			return true
		}
	}
	return false
}
func existingShotSpeed(shot model.DemoEditShot) float64 {
	for i := len(shot.Operations) - 1; i >= 0; i-- {
		if shot.Operations[i].Type == model.EditOperationSpeed && shot.Operations[i].Speed != nil {
			return maxFloat(0.5, minFloat(12, *shot.Operations[i].Speed))
		}
	}
	return 1
}
func countPurpose(intents []model.PresentationGenerationIntent, purpose string) int {
	n := 0
	for _, intent := range intents {
		if intent.Purpose == purpose {
			n++
		}
	}
	return n
}
func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	result := []string{}
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v != "" && !seen[v] {
			seen[v] = true
			result = append(result, v)
		}
	}
	return result
}
func copyStringMap(source map[string]string) map[string]string {
	result := map[string]string{}
	for k, v := range source {
		result[k] = v
	}
	return result
}
func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
