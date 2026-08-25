package creative

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"cascade-demoops/backend/internal/model"
)

func BuildCreativeDirectorPackage(bundle model.CreativeSourceBundle, shots []model.CreativeShotIntent, packageID string, now time.Time) (model.CreativeDirectorPackage, error) {
	if err := model.ValidateCreativeSourceBundle(bundle); err != nil {
		return model.CreativeDirectorPackage{}, fmt.Errorf("validate creative source bundle: %w", err)
	}
	for index, shot := range shots {
		if err := model.ValidateCreativeShotIntent(shot); err != nil {
			return model.CreativeDirectorPackage{}, fmt.Errorf("creative_shot_intents[%d]: %w", index, err)
		}
	}
	pkg := model.CreativeDirectorPackage{
		SchemaVersion: model.CreativeDirectorPackageSchemaVersion,
		PackageID:     packageID,
		BundleID:      bundle.BundleID,
		BundleDigest:  bundle.SourceDigest,
		GeneratedAt:   now.UTC(),
		ShotIntents:   append([]model.CreativeShotIntent{}, shots...),
		StyleProfile:  model.CreativeStyleProfile{Name: "enterprise_technology", ColorGrade: "enterprise_technology", Pacing: "confident_dynamic", TransitionStyle: "clean_hero", AudioStrategy: "source_ducking"},
	}
	if err := model.ValidateCreativeDirectorPackage(pkg); err != nil {
		return model.CreativeDirectorPackage{}, err
	}
	return pkg, nil
}

func AdaptToPresentationIntents(bundle model.CreativeSourceBundle, shots []model.CreativeShotIntent) ([]model.PresentationGenerationIntent, error) {
	if err := model.ValidateCreativeSourceBundle(bundle); err != nil {
		return nil, fmt.Errorf("validate creative source bundle: %w", err)
	}
	artifactByID := make(map[string]model.CreativeEvidenceArtifact, len(bundle.EvidenceArtifacts))
	for _, artifact := range bundle.EvidenceArtifacts {
		artifactByID[artifact.ID] = artifact
	}
	result := make([]model.PresentationGenerationIntent, 0, len(shots))
	for index, shot := range shots {
		if err := model.ValidateCreativeShotIntent(shot); err != nil {
			return nil, fmt.Errorf("creative_shot_intents[%d]: %w", index, err)
		}
		refs := make([]string, 0, len(shot.ReferenceArtifactIDs))
		for _, artifactID := range shot.ReferenceArtifactIDs {
			artifact, ok := artifactByID[artifactID]
			if !ok {
				return nil, fmt.Errorf("shot %s references unknown artifact %s", shot.IntentID, artifactID)
			}
			if artifact.Sensitive || !artifact.Immutable {
				return nil, fmt.Errorf("shot %s references mutable or sensitive artifact %s", shot.IntentID, artifactID)
			}
			refs = append(refs, artifactID)
		}
		intent := model.PresentationGenerationIntent{
			IntentID: shot.IntentID, Capability: model.PresentationVideoCandidateCapability,
			Purpose: creativePurpose(shot.Role, shot.Purpose), Required: false,
			ReferenceAssetRefs: refs,
			RequestedSlot:      model.PresentationGenerationRequestedSlot{PreferredDurationSec: shot.DurationSec, AspectRatio: shot.AspectRatio},
			ContentPolicy:      model.PresentationGenerationContentPolicy{PresentationOnly: true, RequiresExplicitReview: true},
			FailurePolicy:      model.PresentationGenerationFailureContinue,
		}
		if err := model.ValidatePresentationGenerationIntents([]model.PresentationGenerationIntent{intent}); err != nil {
			return nil, fmt.Errorf("adapt shot %s: %w", shot.IntentID, err)
		}
		result = append(result, intent)
	}
	return result, nil
}

type EditShotInput struct {
	ID                     string
	SourceArtifactID       string
	SourceStepID           string
	SourceTimeRangeMS      *model.MillisecondRange
	Purpose                string
	Role                   model.CreativeShotRole
	DurationMS             int
	TargetGeometryVerified bool
	GeometryArtifactID     string
}

func CompileDemoEditPlan(bundle model.CreativeSourceBundle, inputs []EditShotInput) (model.DemoEditPlan, error) {
	if err := model.ValidateCreativeSourceBundle(bundle); err != nil {
		return model.DemoEditPlan{}, fmt.Errorf("validate creative source bundle: %w", err)
	}
	if len(inputs) == 0 {
		return model.DemoEditPlan{}, errors.New("at least one edit shot is required")
	}
	artifactByID := make(map[string]model.CreativeEvidenceArtifact, len(bundle.EvidenceArtifacts))
	for _, artifact := range bundle.EvidenceArtifacts {
		artifactByID[artifact.ID] = artifact
	}
	shots := make([]model.DemoEditShot, 0, len(inputs))
	seen := map[string]struct{}{}
	for index, input := range inputs {
		if strings.TrimSpace(input.ID) == "" || strings.TrimSpace(input.SourceArtifactID) == "" {
			return model.DemoEditPlan{}, fmt.Errorf("edit_shots[%d] requires id and source artifact", index)
		}
		if _, exists := seen[input.ID]; exists {
			return model.DemoEditPlan{}, fmt.Errorf("edit_shots[%d].id is duplicated", index)
		}
		seen[input.ID] = struct{}{}
		artifact, ok := artifactByID[input.SourceArtifactID]
		if !ok || artifact.Sensitive || !artifact.Immutable {
			return model.DemoEditPlan{}, fmt.Errorf("edit shot %s has invalid source artifact", input.ID)
		}
		if input.TargetGeometryVerified {
			geometry, exists := artifactByID[input.GeometryArtifactID]
			if !exists || geometry.Sensitive || !geometry.Immutable || strings.TrimSpace(geometry.Role) != "target_geometry" {
				return model.DemoEditPlan{}, fmt.Errorf("edit shot %s claims verified geometry without an immutable target_geometry artifact", input.ID)
			}
		}
		shot := model.DemoEditShot{ID: input.ID, SourceArtifactID: input.SourceArtifactID, SourceStepID: input.SourceStepID, SourceTimeRangeMS: input.SourceTimeRangeMS, Purpose: input.Purpose, PresentationKind: "video"}
		if input.DurationMS > 0 {
			shot.OutputDurationMS = input.DurationMS
		}
		shot.Operations = operationsForRole(input.Role, input.TargetGeometryVerified)
		shots = append(shots, shot)
	}
	return model.DemoEditPlan{
		SchemaVersion: model.DemoEditPlanSchemaVersion, PlanID: "creative_edit_" + bundle.BundleID,
		Objective: "enterprise_film_presentation", SourceAuthority: model.DemoEditSourceAuthorityCustomerSideAgent,
		ModelRole: model.DemoEditModelRolePresentationOptimizerOnly, SourceMaterialPolicy: model.DemoEditSourceMaterialPolicyExistingAssetsOnly,
		ScriptOrderPolicy: model.DemoEditScriptOrderPolicyPreserveRequiredStepOrder, LockedFields: append([]string{}, model.DemoEditRequiredLockedFields...),
		ModelEditableFields: append([]string{}, model.DemoEditAllowedModelEditableFields...), Shots: shots,
		GlobalStyle: &model.DemoEditGlobalStyle{ColorGrade: "enterprise_technology", Pacing: "confident_dynamic", TransitionStyle: "clean_hero"},
	}, nil
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

// PurposeForRole exposes the stable role-to-purpose mapping to the FinalFilm
// adapter without exposing Provider or state-machine internals.
func PurposeForRole(role model.CreativeShotRole, fallback string) string {
	return creativePurpose(role, fallback)
}

func operationsForRole(role model.CreativeShotRole, geometryVerified bool) []model.EditOperation {
	operations := make([]model.EditOperation, 0, 2)
	if role == model.CreativeShotRoleHeroTransition || role == model.CreativeShotRoleEstablishing {
		operations = append(operations, model.EditOperation{Type: model.EditOperationTransition, Style: "clean_hero"})
	}
	if role == model.CreativeShotRoleActionFocus {
		speed := 1.15
		operations = append(operations, model.EditOperation{Type: model.EditOperationSpeed, Speed: &speed})
	}
	if geometryVerified {
		zoom := 1.06
		operations = append(operations, model.EditOperation{Type: model.EditOperationZoomPan, Zoom: &zoom, Style: "target_geometry_verified"})
	}
	return operations
}
