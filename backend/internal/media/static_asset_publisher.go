package media

import (
	"context"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"cascade-demoops/backend/internal/model"
)

type StaticAssetPublisher struct {
	publicDir     string
	publicBaseURL string
	now           func() time.Time
}

func NewStaticAssetPublisher(publicDir string, publicBaseURL string, now func() time.Time) StaticAssetPublisher {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return StaticAssetPublisher{
		publicDir:     strings.TrimSpace(publicDir),
		publicBaseURL: strings.TrimRight(strings.TrimSpace(publicBaseURL), "/"),
		now:           now,
	}
}

func (p StaticAssetPublisher) PublishArkAssets(ctx context.Context, plan model.ArkAssetPublicationPlan, planRef model.DirectorMaterialRef) (model.ArkAssetPublicationResult, error) {
	if err := ctx.Err(); err != nil {
		return model.ArkAssetPublicationResult{}, err
	}
	items := make([]model.ArkAssetPublicationResultItem, 0, len(plan.Items))
	blockers := append([]model.ArkMediaReadinessFinding{}, plan.Blockers...)
	warnings := append([]model.ArkMediaReadinessFinding{}, plan.Warnings...)
	allRequiredReady := true
	for _, item := range plan.Items {
		resultItem := p.staticPublicationResultItem(item)
		items = append(items, resultItem)
		if resultItem.Required && !resultItem.CanUseForRealCall {
			allRequiredReady = false
			blockers = append(blockers, model.ArkMediaReadinessFinding{
				Code:    resultItem.Status,
				Message: firstNonEmpty(resultItem.ActionRequired, "required source asset could not be published"),
				RefID:   resultItem.SourceRef.ID,
				TaskID:  strings.Join(resultItem.TaskIDs, ","),
			})
		}
		if !resultItem.Required && !resultItem.CanUseForRealCall && resultItem.Status != "ready_existing_public_ref" {
			warnings = append(warnings, model.ArkMediaReadinessFinding{
				Code:    resultItem.Status,
				Message: firstNonEmpty(resultItem.ActionRequired, "optional source asset could not be published"),
				RefID:   resultItem.SourceRef.ID,
				TaskID:  strings.Join(resultItem.TaskIDs, ","),
			})
		}
	}
	status := "ready"
	if len(blockers) > 0 {
		status = "blocked"
	}
	return model.ArkAssetPublicationResult{
		SchemaVersion:      model.ArkAssetPublicationResultSchemaVersion,
		ResultID:           "ark_asset_publication_result_" + safeResultID(plan.SourcePackageID),
		CreatedAt:          p.now().UTC(),
		Mode:               "static_public_dir",
		Publisher:          "static_asset_publisher",
		SourcePackageID:    plan.SourcePackageID,
		PublicationPlanRef: planRef,
		Status:             status,
		CanUseForRealCall:  allRequiredReady && len(blockers) == 0,
		ContainsDryRunRefs: false,
		Items:              items,
		Blockers:           blockers,
		Warnings:           warnings,
		Notes: []string{
			"Published source assets are copied to CASCADE_ARK_ASSET_PUBLIC_DIR and referenced through CASCADE_ARK_ASSET_PUBLIC_BASE_URL.",
			"Only captured source assets are published; generated media still cannot replace product UI evidence.",
		},
	}, nil
}

func (p StaticAssetPublisher) staticPublicationResultItem(item model.ArkAssetPublicationItem) model.ArkAssetPublicationResultItem {
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
	if item.Status != "ready_after_publication" {
		return result
	}
	if strings.TrimSpace(p.publicDir) == "" {
		result.Status = "publisher_public_dir_missing"
		result.ActionRequired = "set CASCADE_ARK_ASSET_PUBLIC_DIR to a directory served over HTTPS"
		return result
	}
	if !isHTTPSURL(p.publicBaseURL) {
		result.Status = "publisher_public_base_url_invalid"
		result.ActionRequired = "set CASCADE_ARK_ASSET_PUBLIC_BASE_URL to an HTTPS URL reachable by Ark"
		return result
	}
	sourcePath, ok := localPathFromURI(item.Ref.URI)
	if !ok || strings.TrimSpace(sourcePath) == "" {
		result.Status = "source_uri_not_local"
		result.ActionRequired = "provide a local captured source file before static publication"
		return result
	}
	fileName := firstNonEmpty(item.RecommendedFileName, filepath.Base(sourcePath))
	if fileName == "." || fileName == string(filepath.Separator) {
		fileName = safeResultID(firstNonEmpty(item.Ref.ID, item.Ref.Kind, "asset"))
	}
	destinationPath := filepath.Join(p.publicDir, filepath.Base(fileName))
	if err := copyFileIfNeeded(sourcePath, destinationPath); err != nil {
		result.Status = "publication_failed"
		result.ActionRequired = "copy captured source asset to public asset directory: " + err.Error()
		return result
	}
	ref := item.Ref
	ref.URI = joinPublicAssetURL(p.publicBaseURL, filepath.Base(fileName))
	result.ProposedPublicRef = &ref
	result.Status = "published"
	result.Published = true
	result.CanUseForRealCall = true
	return result
}

func copyFileIfNeeded(source string, destination string) error {
	source = filepath.Clean(source)
	destination = filepath.Clean(destination)
	sourceAbs, sourceErr := filepath.Abs(source)
	destinationAbs, destinationErr := filepath.Abs(destination)
	if sourceErr == nil && destinationErr == nil && strings.EqualFold(sourceAbs, destinationAbs) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(destination)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

func localPathFromURI(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}
	lower := strings.ToLower(value)
	if strings.HasPrefix(lower, "file://") {
		parsed, err := url.Parse(value)
		if err != nil {
			return "", false
		}
		path := parsed.Path
		if runtime.GOOS == "windows" && len(path) >= 3 && path[0] == '/' && path[2] == ':' {
			path = path[1:]
		}
		return filepath.FromSlash(path), true
	}
	if strings.Contains(lower, "://") {
		return "", false
	}
	return value, true
}

func joinPublicAssetURL(baseURL string, fileName string) string {
	return strings.TrimRight(baseURL, "/") + "/" + url.PathEscape(fileName)
}

func isHTTPSURL(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	return err == nil && strings.EqualFold(parsed.Scheme, "https") && parsed.Host != ""
}
