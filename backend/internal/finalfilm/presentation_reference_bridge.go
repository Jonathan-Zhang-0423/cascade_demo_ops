package finalfilm

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"cascade-demoops/backend/internal/media"
	"cascade-demoops/backend/internal/model"
)

const presentationReferencePublicationSchemaVersion = "demoops.presentation_reference_publication.v1"

// prepareAutomatedPresentationReferences resolves only Server-created,
// text-free palette boards. The provider receives a short-lived published URL;
// the URL is deliberately never persisted in the job or event stream. Both
// automated providers support text-only generation, so an installation without
// a publisher drops only these decorative palette references instead of
// blocking the mandatory presentation slots. Factual or caller-supplied
// references are never removed by this fallback.
func (s *Service) prepareAutomatedPresentationReferences(ctx context.Context, job model.FinalFilmJob, intent media.GeneratedShotIntent, operationID string) (media.GeneratedShotIntent, error) {
	local := map[string]model.TimelineArtifact{}
	for _, artifact := range job.Catalog.Artifacts {
		if strings.EqualFold(strings.TrimSpace(artifact.Kind), "generated_palette_reference") {
			local[artifact.ID] = artifact
		}
	}
	prepared := intent
	prepared.References = make([]media.GeneratedShotReference, 0, len(intent.References))
	for _, reference := range intent.References {
		if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(reference.URI)), "asset://") {
			prepared.References = append(prepared.References, reference)
			continue
		}
		artifact, ok := local[reference.ArtifactID]
		if !ok || strings.TrimSpace(artifact.LocalPath) == "" || !strings.EqualFold(strings.TrimSpace(artifact.MimeType), "image/png") {
			return intent, errors.New("opaque automated reference is not a text-free palette board")
		}
		if s.assetPublisher == nil {
			continue
		}
		retention := s.referenceRetention
		if strings.TrimSpace(retention.Mode) == "" {
			retention = model.DefaultMediaDeliveryPreferences().TOSRetention
		}
		if job.RunAuthorization == nil || strings.TrimSpace(job.RunAuthorization.AuthorizationRef) == "" {
			return intent, errors.New("text-free palette publication requires the run's explicit provider authorization")
		}
		// The guided run authorization covers the model-input-only palette upload
		// for this job. It does not authorize publishing recordings, credentials,
		// execution packages, or any other catalog artifact.
		retention.ClientDisclosureAcknowledged = true
		material := model.DirectorMaterialRef{ID: artifact.ID, Kind: artifact.Kind, URI: artifact.LocalPath, MimeType: artifact.MimeType, SizeBytes: artifact.SizeBytes, AssetRole: artifact.AssetRole, IncludeInDemo: false, Sensitive: false, Metadata: map[string]any{"text_free": true, "ui_free": true}}
		plan := model.ArkAssetPublicationPlan{
			SchemaVersion: presentationReferencePublicationSchemaVersion,
			PlanID:        "presentation_reference_" + safePathComponent(operationID+"_"+artifact.ID), CreatedAt: s.now().UTC(),
			Mode: "generated_presentation_reference", SourcePackageID: job.SourcePackageID, Status: "ready_after_publication",
			PublicationStrategy: "private_tos_presigned_url", URLTTLHours: 1, TOSRetention: retention,
			Items: []model.ArkAssetPublicationItem{{Ref: material, TaskIDs: []string{operationID}, Usage: "text_free_palette_reference", Required: true, AcceptedMimeTypes: []string{"image/png"}, NeedsPublication: true, RecommendedFileName: safePathComponent(artifact.ID) + filepath.Ext(artifact.LocalPath), Status: "ready_after_publication"}},
		}
		published, err := s.assetPublisher.PublishArkAssets(ctx, plan, material)
		if err != nil {
			return intent, fmt.Errorf("publish text-free palette reference: %w", err)
		}
		if !published.CanUseForRealCall || len(published.Items) != 1 || published.Items[0].ProposedPublicRef == nil || !strings.HasPrefix(strings.ToLower(strings.TrimSpace(published.Items[0].ProposedPublicRef.URI)), "https://") {
			return intent, errors.New("text-free palette publication did not return one provider-readable HTTPS reference")
		}
		preparedReference := reference
		preparedReference.URI = published.Items[0].ProposedPublicRef.URI
		prepared.References = append(prepared.References, preparedReference)
	}
	return prepared, nil
}
