package creative

import (
	"fmt"
	"strings"

	"cascade-demoops/backend/internal/media"
	"cascade-demoops/backend/internal/model"
)

// AdaptToGeneratedShotIntents is used by the Server-side FinalFilm adapter
// when the creative director has already declared a compatible Provider Pool.
// It preserves the provider-neutral prompt/content boundary while carrying
// the allowlist as policy for the Provider registry to enforce.
func AdaptToGeneratedShotIntents(bundle model.CreativeSourceBundle, shots []model.CreativeShotIntent) ([]media.GeneratedShotIntent, error) {
	if err := model.ValidateCreativeSourceBundle(bundle); err != nil {
		return nil, fmt.Errorf("validate creative source bundle: %w", err)
	}
	artifactByID := make(map[string]model.CreativeEvidenceArtifact, len(bundle.EvidenceArtifacts))
	for _, artifact := range bundle.EvidenceArtifacts {
		artifactByID[artifact.ID] = artifact
	}
	result := make([]media.GeneratedShotIntent, 0, len(shots))
	for _, shot := range shots {
		if err := model.ValidateCreativeShotIntent(shot); err != nil {
			return nil, err
		}
		intent := media.GeneratedShotIntent{IntentID: shot.IntentID, Purpose: creativePurpose(shot.Role, shot.Purpose), Prompt: strings.TrimSpace(shot.Prompt), DurationSec: shot.DurationSec, AspectRatio: shot.AspectRatio, AllowedProviders: append([]string{}, shot.AllowedProviders...), ContentPolicy: media.GeneratedShotContentPolicy{PresentationOnly: true, RequiresExplicitReview: true}, FailurePolicy: media.GeneratedShotFailureContinue}
		for _, artifactID := range shot.ReferenceArtifactIDs {
			artifact, ok := artifactByID[artifactID]
			if !ok || artifact.Sensitive || !artifact.Immutable {
				return nil, fmt.Errorf("shot %s references invalid artifact %s", shot.IntentID, artifactID)
			}
			if strings.TrimSpace(artifact.ProviderReference) == "" {
				return nil, fmt.Errorf("shot %s artifact %s has no provider-safe reference", shot.IntentID, artifact.ID)
			}
			intent.References = append(intent.References, media.GeneratedShotReference{ArtifactID: artifact.ID, URI: artifact.ProviderReference, MimeType: artifact.MimeType, Usage: media.GeneratedShotReferenceGeneral})
		}
		if err := media.ValidateGeneratedShotIntent(intent); err != nil {
			return nil, err
		}
		result = append(result, intent)
	}
	return result, nil
}
