package media

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/volcengine/ve-tos-golang-sdk/v2/tos"

	"cascade-demoops/backend/internal/model"
)

type fakeTOSObjectClient struct {
	putInput     *tos.PutObjectV2Input
	presignInput *tos.PreSignedURLInput
	putErr       error
	presignErr   error
	presignURL   string
}

func TestClassifyTOSPublishErrorRedactsProviderDetails(t *testing.T) {
	for _, test := range []struct{ input, want string }{
		{"TOS statuscode=403 AccessDenied ak=secret", "access_denied"},
		{"NoSuchBucket: bucket missing", "bucket_not_found"},
		{"dial tcp 1.2.3.4:443: connectex", "network_error"},
		{"connectex: access to socket forbidden by its access permissions", "network_error"},
		{"request timeout", "timeout"},
		{"unexpected provider response", "provider_error"},
	} {
		if got := classifyTOSPublishError(errors.New(test.input)); got != test.want {
			t.Fatalf("classifyTOSPublishError(%q)=%q want %q", test.input, got, test.want)
		}
	}
}

func (f *fakeTOSObjectClient) PutObjectV2(_ context.Context, input *tos.PutObjectV2Input) (*tos.PutObjectV2Output, error) {
	f.putInput = input
	return &tos.PutObjectV2Output{}, f.putErr
}

func (f *fakeTOSObjectClient) PreSignedURL(input *tos.PreSignedURLInput) (*tos.PreSignedURLOutput, error) {
	f.presignInput = input
	url := f.presignURL
	if url == "" {
		url = "https://private-bucket.tos-cn-beijing.ivolces.com/signed"
	}
	return &tos.PreSignedURLOutput{SignedUrl: url}, f.presignErr
}

func TestTOSAssetPublisherUploadsSelectedAssetAndReturnsSignedURL(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "source.mp4")
	if err := os.WriteFile(path, []byte("mp4-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 30, 8, 0, 0, 0, time.UTC)
	fake := &fakeTOSObjectClient{}
	publisher := newTOSAssetPublisherForClient(TOSAssetPublisherConfig{
		Bucket: "cascade-ark-media-test", Prefix: "ark-media/", SignedURLTTL: time.Hour,
	}, fake, func() time.Time { return now })
	plan := model.ArkAssetPublicationPlan{SourcePackageID: "pkg_01", Items: []model.ArkAssetPublicationItem{{
		Ref:     model.DirectorMaterialRef{ID: "source", URI: path, MimeType: "video/mp4"},
		TaskIDs: []string{"seedance_reference_director_preview"}, Required: true,
		RecommendedFileName: "source_reference.mp4", Status: "ready_after_publication",
	}}}

	result, err := publisher.PublishArkAssets(t.Context(), plan, model.DirectorMaterialRef{ID: "plan"})
	if err != nil {
		t.Fatal(err)
	}
	if !result.CanUseForRealCall || result.Mode != "private_tos_presigned_url" || len(result.Items) != 1 {
		t.Fatalf("unexpected publication result: %+v", result)
	}
	if fake.putInput == nil || fake.putInput.Bucket != "cascade-ark-media-test" || fake.putInput.ContentLength != int64(len("mp4-bytes")) {
		t.Fatalf("unexpected upload input: %+v", fake.putInput)
	}
	if fake.putInput.Key != "ark-media/pkg_01/001_source_reference.mp4" || fake.presignInput == nil || fake.presignInput.Key != fake.putInput.Key {
		t.Fatalf("unexpected TOS object key: put=%+v presign=%+v", fake.putInput, fake.presignInput)
	}
	if result.Items[0].ProposedPublicRef == nil || result.Items[0].ProposedPublicRef.URI != "https://private-bucket.tos-cn-beijing.ivolces.com/signed" {
		t.Fatalf("expected signed URL only in the local publication result: %+v", result.Items[0])
	}
}

func TestTOSAssetPublisherRejectsNonArkPrivatePresignedURLBeforeProviderUse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source.mp4")
	if err := os.WriteFile(path, []byte("mp4-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	publisher := newTOSAssetPublisherForClient(TOSAssetPublisherConfig{Bucket: "cascade-ark-media-test"}, &fakeTOSObjectClient{presignURL: "https://private-bucket.tos-cn-beijing.volces.com/signed"}, time.Now)
	plan := model.ArkAssetPublicationPlan{SourcePackageID: "pkg_ark_url", Items: []model.ArkAssetPublicationItem{{
		Ref: model.DirectorMaterialRef{ID: "source", URI: path, MimeType: "video/mp4"}, Required: true, Status: "ready_after_publication",
	}}}
	result, err := publisher.PublishArkAssets(t.Context(), plan, model.DirectorMaterialRef{ID: "plan"})
	if err != nil {
		t.Fatal(err)
	}
	if result.CanUseForRealCall || result.Items[0].Status != "tos_presign_ark_input_url_invalid" || result.Items[0].ErrorClass != "ark_private_tos_url_required" {
		t.Fatalf("non-private Ark URL must be blocked: %+v", result)
	}
}

func TestTOSAssetPublisherConfigFromEnvDoesNotRequireStaticPublicURL(t *testing.T) {
	values := map[string]string{
		"VOLC_TOS_ACCESS_KEY": "ak", "VOLC_TOS_SECRET_KEY": "sk", "VOLC_TOS_ENDPOINT": "tos-cn-beijing.volces.com",
		"VOLC_TOS_REGION": "cn-beijing", "VOLC_TOS_BUCKET": "cascade-ark-media-test", "VOLC_TOS_PREFIX": "ark-media/",
	}
	config, configured := TOSAssetPublisherConfigFromEnv(func(key string) string { return values[key] })
	if !configured || config.Prefix != "ark-media" || config.SignedURLTTL != time.Hour {
		t.Fatalf("unexpected TOS config: %+v configured=%t", config, configured)
	}
}

func TestTOSAssetPublisherRequiresRetentionAcknowledgement(t *testing.T) {
	fake := &fakeTOSObjectClient{}
	publisher := newTOSAssetPublisherForClient(TOSAssetPublisherConfig{Bucket: "cascade-ark-media-test"}, fake, func() time.Time {
		return time.Date(2026, 8, 18, 0, 0, 0, 0, time.UTC)
	})
	retention := model.DefaultMediaDeliveryPreferences().TOSRetention
	plan := model.ArkAssetPublicationPlan{SourcePackageID: "pkg_ack", TOSRetention: retention, Items: []model.ArkAssetPublicationItem{{
		Ref: model.DirectorMaterialRef{ID: "source", URI: filepath.Join(t.TempDir(), "source.mp4"), MimeType: "video/mp4"}, Required: true, Status: "ready_after_publication",
	}}}
	result, err := publisher.PublishArkAssets(t.Context(), plan, model.DirectorMaterialRef{ID: "plan"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "blocked" || result.CanUseForRealCall || len(result.Blockers) != 1 || result.Blockers[0].Code != "tos_retention_client_ack_required" {
		t.Fatalf("unexpected retention admission result: %+v", result)
	}
	if fake.putInput != nil {
		t.Fatal("publisher uploaded bytes before retention acknowledgement")
	}
}
