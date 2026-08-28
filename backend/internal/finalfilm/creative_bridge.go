package finalfilm

import (
	"fmt"
	"strings"

	"cascade-demoops/backend/internal/model"
)

// CompileCreativeDirectorPlan is an adapter-only bridge. It prepares the
// existing FinalFilm Director plan from the new creative contract without
// changing FinalFilm state, Store, revisions, or approval semantics.
func CompileCreativeDirectorPlan(pkg model.CreativeDirectorPackage, job model.FinalFilmJob) (model.FinalFilmDirectorPlan, error) {
	if err := model.ValidateCreativeDirectorPackage(pkg); err != nil {
		return model.FinalFilmDirectorPlan{}, err
	}
	allowed := make(map[string]struct{}, len(job.Constraints.AllowedArtifactIDs))
	for _, artifactID := range job.Constraints.AllowedArtifactIDs {
		allowed[artifactID] = struct{}{}
	}
	plan := model.FinalFilmDirectorPlan{SchemaVersion: model.FinalFilmDirectorPlanSchemaVersion, PlanID: "creative_director_" + pkg.PackageID, JobID: job.JobID, ConstraintSetID: job.Constraints.ConstraintSetID, DirectorRunID: "creative_run_" + pkg.PackageID, GeneratedAt: pkg.GeneratedAt, Specs: make([]model.FinalFilmGeneratedSpec, 0, len(pkg.ShotIntents))}
	for _, shot := range pkg.ShotIntents {
		refs := append([]string{}, shot.ReferenceArtifactIDs...)
		for _, artifactID := range refs {
			if _, ok := allowed[artifactID]; !ok {
				return model.FinalFilmDirectorPlan{}, fmt.Errorf("creative shot %s references artifact outside FinalFilm constraint set: %s", shot.IntentID, artifactID)
			}
		}
		prompt := strings.TrimSpace(shot.Prompt)
		plan.Specs = append(plan.Specs, model.FinalFilmGeneratedSpec{
			SpecID: "spec_" + shot.IntentID, IntentID: shot.IntentID, Purpose: creativePurpose(shot.Role, shot.Purpose), Prompt: prompt, PromptSHA256: model.FinalFilmPromptSHA256(prompt), DurationSec: shot.DurationSec, AspectRatio: shot.AspectRatio, ReferenceArtifactIDs: refs,
			ContentPolicy: model.FinalFilmGeneratedSpecContentPolicy{PresentationOnly: true, NoCapturedUIRecreation: true, NoBusinessFactClaims: true, NoUnverifiedText: true, RequiresExplicitReview: true}, FailurePolicy: model.PresentationGenerationFailureContinue,
		})
	}
	return plan, nil
}

func creativePurpose(role model.CreativeShotRole, fallback string) string {
	if strings.TrimSpace(fallback) != "" {
		return fallback
	}
	switch role {
	case model.CreativeShotRoleEstablishing, model.CreativeShotRoleChapterOpen:
		return "intro"
	case model.CreativeShotRoleBrandOutro:
		return "outro"
	case model.CreativeShotRoleHeroTransition:
		return "section_divider"
	default:
		return "abstract_broll"
	}
}
