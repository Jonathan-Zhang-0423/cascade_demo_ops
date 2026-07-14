package media

import (
	"context"
	"strings"
	"time"

	"cascade-demoops/backend/internal/model"
)

type AssetPublisher interface {
	PublishArkAssets(ctx context.Context, plan model.ArkAssetPublicationPlan, planRef model.DirectorMaterialRef) (model.ArkAssetPublicationResult, error)
}

type DryRunAssetPublisher struct {
	now func() time.Time
}

func NewDryRunAssetPublisher(now func() time.Time) DryRunAssetPublisher {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return DryRunAssetPublisher{now: now}
}

func (p DryRunAssetPublisher) PublishArkAssets(ctx context.Context, plan model.ArkAssetPublicationPlan, planRef model.DirectorMaterialRef) (model.ArkAssetPublicationResult, error) {
	if err := ctx.Err(); err != nil {
		return model.ArkAssetPublicationResult{}, err
	}
	items := make([]model.ArkAssetPublicationResultItem, 0, len(plan.Items))
	blockers := append([]model.ArkMediaReadinessFinding{}, plan.Blockers...)
	warnings := append([]model.ArkMediaReadinessFinding{}, plan.Warnings...)
	containsDryRunRefs := false
	allRequiredReady := true
	for _, item := range plan.Items {
		resultItem := dryRunPublicationResultItem(item)
		items = append(items, resultItem)
		if resultItem.DryRun {
			containsDryRunRefs = true
		}
		if resultItem.Required && !resultItem.CanUseForRealCall {
			allRequiredReady = false
			if resultItem.Status != "dry_run_publication_required" {
				blockers = append(blockers, model.ArkMediaReadinessFinding{
					Code:    resultItem.Status,
					Message: firstNonEmpty(resultItem.ActionRequired, "required source asset is not publishable"),
					RefID:   resultItem.SourceRef.ID,
					TaskID:  strings.Join(resultItem.TaskIDs, ","),
				})
			}
		}
	}
	status := "ready"
	switch {
	case len(blockers) > 0:
		status = "blocked"
	case containsDryRunRefs:
		status = "dry_run_publication_required"
	}
	return model.ArkAssetPublicationResult{
		SchemaVersion:      model.ArkAssetPublicationResultSchemaVersion,
		ResultID:           "ark_asset_publication_result_" + safeResultID(plan.SourcePackageID),
		CreatedAt:          p.now().UTC(),
		Mode:               "dry_run",
		Publisher:          "dry_run_asset_publisher",
		SourcePackageID:    plan.SourcePackageID,
		PublicationPlanRef: planRef,
		Status:             status,
		CanUseForRealCall:  allRequiredReady && !containsDryRunRefs && len(blockers) == 0,
		ContainsDryRunRefs: containsDryRunRefs,
		Items:              items,
		Blockers:           blockers,
		Warnings:           warnings,
		Notes: []string{
			"Dry-run publisher did not upload bytes or mint real public URLs.",
			"Replace this publisher with object storage, CDN, or provider asset upload before real Ark media calls need local captured assets.",
		},
	}, nil
}

func dryRunPublicationResultItem(item model.ArkAssetPublicationItem) model.ArkAssetPublicationResultItem {
	result := model.ArkAssetPublicationResultItem{
		SourceRef:         item.Ref,
		TaskIDs:           append([]string{}, item.TaskIDs...),
		Usage:             item.Usage,
		Required:          item.Required,
		Status:            item.Status,
		Published:         false,
		DryRun:            false,
		CanUseForRealCall: item.Status == "ready" && item.CurrentURIIsPublic,
		ActionRequired:    item.ActionRequired,
	}
	if item.Status == "ready" && item.CurrentURIIsPublic {
		ref := item.Ref
		result.ProposedPublicRef = &ref
		result.Status = "ready_existing_public_ref"
		return result
	}
	if item.Status == "ready_after_publication" {
		ref := item.Ref
		ref.URI = item.ExpectedPublicURI
		result.ProposedPublicRef = &ref
		result.Status = "dry_run_publication_required"
		result.DryRun = true
		result.ActionRequired = "real publisher must upload or expose this asset before real Ark calls"
		return result
	}
	return result
}

func safeResultID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var builder strings.Builder
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			builder.WriteRune(r)
			continue
		}
		builder.WriteByte('_')
	}
	normalized := strings.Trim(builder.String(), "_")
	if normalized == "" {
		return "unknown"
	}
	return normalized
}
