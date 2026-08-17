package media

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/volcengine/ve-tos-golang-sdk/v2/tos"
	"github.com/volcengine/ve-tos-golang-sdk/v2/tos/enum"

	"cascade-demoops/backend/internal/model"
)

const (
	defaultTOSSignedURLTTL = time.Hour
	maxTOSSignedURLTTL     = 7 * 24 * time.Hour
)

// TOSAssetPublisherConfig contains only runtime-supplied storage settings.
// Credentials come from env or a secret manager, never an execution package.
type TOSAssetPublisherConfig struct {
	AccessKey    string
	SecretKey    string
	Endpoint     string
	Region       string
	Bucket       string
	Prefix       string
	SignedURLTTL time.Duration
}

type tosObjectClient interface {
	PutObjectV2(context.Context, *tos.PutObjectV2Input) (*tos.PutObjectV2Output, error)
	PreSignedURL(*tos.PreSignedURLInput) (*tos.PreSignedURLOutput, error)
}

type TOSAssetPublisher struct {
	config TOSAssetPublisherConfig
	client tosObjectClient
	now    func() time.Time
}

func NewTOSAssetPublisher(config TOSAssetPublisherConfig, now func() time.Time) (*TOSAssetPublisher, error) {
	if err := validateTOSAssetPublisherConfig(config); err != nil {
		return nil, err
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	config = normalizeTOSAssetPublisherConfig(config)
	client, err := tos.NewClientV2(config.Endpoint, tos.WithRegion(config.Region), tos.WithCredentials(tos.NewStaticCredentials(config.AccessKey, config.SecretKey)))
	if err != nil {
		return nil, fmt.Errorf("create TOS client: %w", err)
	}
	return newTOSAssetPublisherForClient(config, client, now), nil
}

func newTOSAssetPublisherForClient(config TOSAssetPublisherConfig, client tosObjectClient, now func() time.Time) *TOSAssetPublisher {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &TOSAssetPublisher{config: normalizeTOSAssetPublisherConfig(config), client: client, now: now}
}

func (p *TOSAssetPublisher) PublishArkAssets(ctx context.Context, plan model.ArkAssetPublicationPlan, planRef model.DirectorMaterialRef) (model.ArkAssetPublicationResult, error) {
	if err := ctx.Err(); err != nil {
		return model.ArkAssetPublicationResult{}, err
	}
	if p == nil || p.client == nil {
		return model.ArkAssetPublicationResult{}, errors.New("TOS asset publisher is not initialized")
	}
	items := make([]model.ArkAssetPublicationResultItem, 0, len(plan.Items))
	blockers := append([]model.ArkMediaReadinessFinding{}, plan.Blockers...)
	warnings := append([]model.ArkMediaReadinessFinding{}, plan.Warnings...)
	allRequiredReady := true
	for index, item := range plan.Items {
		resultItem := p.publishItem(ctx, plan.SourcePackageID, index, item)
		items = append(items, resultItem)
		if item.Required && !resultItem.CanUseForRealCall {
			allRequiredReady = false
			blockers = append(blockers, model.ArkMediaReadinessFinding{Code: firstNonEmpty(resultItem.Status, "tos_publication_failed"), Message: firstNonEmpty(resultItem.ActionRequired, "required source asset could not be published to private TOS"), RefID: resultItem.SourceRef.ID, TaskID: strings.Join(resultItem.TaskIDs, ",")})
		}
	}
	status := "ready"
	if len(blockers) > 0 {
		status = "blocked"
	}
	return model.ArkAssetPublicationResult{
		SchemaVersion: model.ArkAssetPublicationResultSchemaVersion, ResultID: "ark_asset_publication_result_" + safeResultID(plan.SourcePackageID), CreatedAt: p.now().UTC(),
		Mode: "private_tos_presigned_url", Publisher: "volcengine_tos_asset_publisher", SourcePackageID: plan.SourcePackageID, PublicationPlanRef: planRef,
		Status: status, CanUseForRealCall: allRequiredReady && len(blockers) == 0, ContainsDryRunRefs: false, Items: items, Blockers: blockers, Warnings: warnings,
		Notes: []string{
			"Selected captured assets were uploaded to a private TOS bucket and exposed only through short-lived signed GET URLs.",
			"Source code, credentials, browser cookies, and execution packages are not uploaded by this publisher.",
		},
	}, nil
}

func (p *TOSAssetPublisher) publishItem(ctx context.Context, sourcePackageID string, index int, item model.ArkAssetPublicationItem) model.ArkAssetPublicationResultItem {
	result := model.ArkAssetPublicationResultItem{SourceRef: item.Ref, TaskIDs: append([]string{}, item.TaskIDs...), Usage: item.Usage, Required: item.Required, Status: item.Status, CanUseForRealCall: item.Status == "ready" && item.CurrentURIIsPublic, ActionRequired: item.ActionRequired}
	if item.Status == "ready" && item.CurrentURIIsPublic {
		ref := item.Ref
		result.ProposedPublicRef = &ref
		result.Status = "ready_existing_public_ref"
		return result
	}
	if item.Status != "ready_after_publication" {
		return result
	}
	sourcePath, ok := localPathFromURI(item.Ref.URI)
	if !ok || strings.TrimSpace(sourcePath) == "" {
		result.Status, result.ActionRequired = "tos_source_uri_not_local", "provide a selected local captured source file before TOS publication"
		return result
	}
	info, err := os.Stat(sourcePath)
	if err != nil || info.IsDir() {
		result.Status, result.ActionRequired = "tos_source_unavailable", "selected captured source file is not available for TOS publication"
		return result
	}
	file, err := os.Open(sourcePath)
	if err != nil {
		result.Status, result.ActionRequired = "tos_source_open_failed", "selected captured source file cannot be opened for TOS publication"
		return result
	}
	defer file.Close()
	key := p.objectKey(sourcePackageID, index, item)
	contentType := firstNonEmpty(item.Ref.MimeType, mime.TypeByExtension(filepath.Ext(sourcePath)), "application/octet-stream")
	_, err = p.client.PutObjectV2(ctx, &tos.PutObjectV2Input{PutObjectBasicInput: tos.PutObjectBasicInput{Bucket: p.config.Bucket, Key: key, ContentLength: info.Size(), ContentType: contentType}, Content: file})
	if err != nil {
		result.Status, result.ActionRequired = "tos_upload_failed", "verify TOS bucket, region, endpoint, object-prefix policy, and uploader credentials"
		result.FailureStage, result.ErrorClass = "put_object", classifyTOSPublishError(err)
		return result
	}
	signed, err := p.client.PreSignedURL(&tos.PreSignedURLInput{HTTPMethod: enum.HttpMethodGet, Bucket: p.config.Bucket, Key: key, Expires: int64(p.config.SignedURLTTL.Seconds())})
	if err != nil || signed == nil || strings.TrimSpace(signed.SignedUrl) == "" {
		result.Status, result.ActionRequired = "tos_presign_failed", "verify the uploader can generate a signed GET URL for the selected TOS object"
		result.FailureStage = "presign_get"
		if err != nil {
			result.ErrorClass = classifyTOSPublishError(err)
		} else {
			result.ErrorClass = "empty_presigned_url"
		}
		return result
	}
	ref := item.Ref
	ref.URI, ref.MimeType = signed.SignedUrl, contentType
	result.ProposedPublicRef = &ref
	result.Status, result.Published, result.CanUseForRealCall, result.ActionRequired = "published_presigned", true, true, ""
	return result
}

func classifyTOSPublishError(err error) string {
	if err == nil {
		return ""
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "accessdenied"), strings.Contains(message, "access denied"), strings.Contains(message, "forbidden"), strings.Contains(message, "statuscode=403"), strings.Contains(message, "http 403"):
		return "access_denied"
	case strings.Contains(message, "nosuchbucket"), strings.Contains(message, "no such bucket"), strings.Contains(message, "statuscode=404"), strings.Contains(message, "http 404"):
		return "bucket_not_found"
	case strings.Contains(message, "invalidaccesskey"), strings.Contains(message, "invalid access key"), strings.Contains(message, "signature"):
		return "credential_or_signature_rejected"
	case strings.Contains(message, "timeout"), strings.Contains(message, "deadline exceeded"):
		return "timeout"
	case strings.Contains(message, "connection reset"), strings.Contains(message, "connectex"), strings.Contains(message, "no such host"), strings.Contains(message, "dial tcp"):
		return "network_error"
	default:
		return "provider_error"
	}
}

func (p *TOSAssetPublisher) objectKey(sourcePackageID string, index int, item model.ArkAssetPublicationItem) string {
	name := filepath.Base(firstNonEmpty(item.RecommendedFileName, item.Ref.URI, item.Ref.ID, "asset"))
	name = strings.ReplaceAll(name, "\\", "_")
	return strings.Trim(p.config.Prefix, "/") + "/" + safeResultID(sourcePackageID) + "/" + fmt.Sprintf("%03d_", index+1) + name
}

func normalizeTOSAssetPublisherConfig(config TOSAssetPublisherConfig) TOSAssetPublisherConfig {
	config.AccessKey, config.SecretKey = strings.TrimSpace(config.AccessKey), strings.TrimSpace(config.SecretKey)
	config.Endpoint, config.Region, config.Bucket = strings.TrimSpace(config.Endpoint), strings.TrimSpace(config.Region), strings.TrimSpace(config.Bucket)
	config.Prefix = strings.Trim(strings.TrimSpace(config.Prefix), "/")
	if config.Prefix == "" {
		config.Prefix = "ark-media"
	}
	if config.SignedURLTTL <= 0 {
		config.SignedURLTTL = defaultTOSSignedURLTTL
	}
	return config
}

func validateTOSAssetPublisherConfig(config TOSAssetPublisherConfig) error {
	config = normalizeTOSAssetPublisherConfig(config)
	if config.AccessKey == "" || config.SecretKey == "" || config.Endpoint == "" || config.Region == "" || config.Bucket == "" {
		return errors.New("TOS access key, secret key, endpoint, region, and bucket are required")
	}
	if config.SignedURLTTL > maxTOSSignedURLTTL {
		return fmt.Errorf("TOS signed URL TTL must not exceed %s", maxTOSSignedURLTTL)
	}
	return nil
}

func TOSAssetPublisherConfigFromEnv(getenv func(string) string) (TOSAssetPublisherConfig, bool) {
	if getenv == nil {
		getenv = os.Getenv
	}
	config := TOSAssetPublisherConfig{AccessKey: getenv("VOLC_TOS_ACCESS_KEY"), SecretKey: getenv("VOLC_TOS_SECRET_KEY"), Endpoint: getenv("VOLC_TOS_ENDPOINT"), Region: getenv("VOLC_TOS_REGION"), Bucket: getenv("VOLC_TOS_BUCKET"), Prefix: getenv("VOLC_TOS_PREFIX")}
	if raw := strings.TrimSpace(getenv("VOLC_TOS_SIGNED_URL_TTL_SEC")); raw != "" {
		if ttl, err := time.ParseDuration(raw + "s"); err == nil {
			config.SignedURLTTL = ttl
		}
	}
	configured := config.AccessKey != "" || config.SecretKey != "" || config.Endpoint != "" || config.Region != "" || config.Bucket != ""
	return normalizeTOSAssetPublisherConfig(config), configured
}
