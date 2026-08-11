package app

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"cascade-demoops/backend/internal/model"
	"cascade-demoops/backend/internal/orchestrator"
)

const desktopCloudRunSchemaVersion = "demoops.desktop_cloud_run.v1"

func (s *Service) updateDesktopCloudRun(ctx context.Context, projectID string, update func(*orchestrator.DesktopCloudRunState)) error {
	return s.updateDesktopCloudState(ctx, projectID, func(state *orchestrator.CascadeState) {
		if state.DesktopCloudRun == nil {
			state.DesktopCloudRun = &orchestrator.DesktopCloudRunState{SchemaVersion: desktopCloudRunSchemaVersion}
		}
		update(state.DesktopCloudRun)
	})
}

func (s *Service) updateDesktopCloudState(ctx context.Context, projectID string, update func(*orchestrator.CascadeState)) error {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return errors.New("project_id is required")
	}
	s.cloudStateMu.Lock()
	defer s.cloudStateMu.Unlock()
	state, err := s.states.Load(ctx, projectID)
	if err != nil {
		return err
	}
	update(state)
	if state.DesktopCloudRun != nil {
		state.DesktopCloudRun.SchemaVersion = desktopCloudRunSchemaVersion
		state.DesktopCloudRun.UpdatedAt = time.Now().UTC()
	}
	return s.states.Save(ctx, state)
}

func (s *Service) persistCloudInit(ctx context.Context, projectID, orgID string, response model.ExecutionPackageInitResponse) error {
	return s.updateDesktopCloudRun(ctx, projectID, func(run *orchestrator.DesktopCloudRunState) {
		run.OrgID = firstNonEmptyString(orgID, defaultDesktopOrgID)
		run.UploadID = response.UploadID
		run.Status = "queued"
		run.Stage = "upload_initialized"
		run.Message = "服务器上传会话已初始化。"
		run.ProgressPercent = 10
	})
}

func (s *Service) persistCloudUpload(ctx context.Context, projectID, orgID, uploadID string, response model.ExecutionPackageUploadResponse) error {
	return s.updateDesktopCloudRun(ctx, projectID, func(run *orchestrator.DesktopCloudRunState) {
		run.OrgID = firstNonEmptyString(orgID, run.OrgID, defaultDesktopOrgID)
		run.UploadID = firstNonEmptyString(uploadID, run.UploadID)
		run.ExchangePackageID = response.ExchangePackageID
		run.CloudJobID = response.CloudJobID
		run.Status = string(response.Status)
		run.Stage = "accepted"
		run.Message = "执行包已上传服务器，等待校验和隔离执行。"
		if run.ProgressPercent < 20 {
			run.ProgressPercent = 20
		}
	})
}

func (s *Service) persistCloudStatus(ctx context.Context, projectID, orgID string, status model.ExecutionPackageStatusResponse) error {
	return s.updateDesktopCloudRun(ctx, projectID, func(run *orchestrator.DesktopCloudRunState) {
		run.OrgID = firstNonEmptyString(orgID, run.OrgID, defaultDesktopOrgID)
		run.ExchangePackageID = firstNonEmptyString(status.ExchangePackageID, run.ExchangePackageID)
		run.CloudJobID = firstNonEmptyString(status.CloudJobID, run.CloudJobID)
		run.Status = string(status.Status)
		run.Stage = status.Stage
		run.Message = status.Message
		run.ProgressPercent = status.ProgressPercent
		run.StageHistory = append([]model.ExecutionStageEvent(nil), status.StageHistory...)
		run.FailureSummary = status.FailureSummary
		run.Error = status.Error
		run.ResultPackageID = firstNonEmptyString(status.ResultPackageID, run.ResultPackageID)
	})
}

func (s *Service) persistCloudResult(ctx context.Context, projectID, orgID string, result model.RecordingResultPackage) error {
	return s.updateDesktopCloudRun(ctx, projectID, func(run *orchestrator.DesktopCloudRunState) {
		run.OrgID = firstNonEmptyString(orgID, run.OrgID, defaultDesktopOrgID)
		run.ResultPackageID = firstNonEmptyString(result.ResultID, run.ResultPackageID)
		run.CloudJobID = firstNonEmptyString(result.CloudJobID, run.CloudJobID)
		run.ResultPackage = &result
		if result.Status == model.RecordingResultStatusFailed {
			run.Status = "failed"
			run.Stage = "failed"
			run.Message = "服务器返回失败诊断。"
		} else {
			run.Status = "completed"
			run.Stage = "completed"
			run.Message = "服务器结果包已返回。"
		}
		run.ProgressPercent = 100
	})
}

func (s *Service) persistCloudDownload(ctx context.Context, projectID, resultPackageID string, download CloudDeliverableDownloadResult) error {
	if !download.ChecksumVerified || strings.TrimSpace(download.LocalPath) == "" {
		return nil
	}
	return s.updateDesktopCloudRun(ctx, projectID, func(run *orchestrator.DesktopCloudRunState) {
		run.ResultPackageID = firstNonEmptyString(resultPackageID, run.ResultPackageID)
		asset := orchestrator.DesktopDownloadedAssetState{
			ArtifactID: download.ArtifactID,
			Kind:       download.Kind,
			Role:       download.Role,
			FileName:   filepath.Base(download.LocalPath),
			SHA256:     download.SHA256,
			MimeType:   download.MimeType,
			SizeBytes:  download.SizeBytes,
			Verified:   true,
		}
		replaced := false
		for index := range run.DownloadedAssets {
			if run.DownloadedAssets[index].ArtifactID == asset.ArtifactID {
				run.DownloadedAssets[index] = asset
				replaced = true
				break
			}
		}
		if !replaced {
			run.DownloadedAssets = append(run.DownloadedAssets, asset)
		}
		if run.Transport == directTransportStateName && len(run.DirectArtifacts) > 0 {
			verified := make(map[string]bool, len(run.DownloadedAssets))
			for _, downloaded := range run.DownloadedAssets {
				verified[downloaded.ArtifactID] = downloaded.Verified
			}
			allVerified := true
			for _, expected := range run.DirectArtifacts {
				if !verified[expected.ArtifactID] {
					allVerified = false
					break
				}
			}
			run.ResultDownloaded = allVerified
		}
	})
}

func (s *Service) persistCloudAck(ctx context.Context, projectID string, ack model.ResultPackageAckResponse) error {
	return s.updateDesktopCloudRun(ctx, projectID, func(run *orchestrator.DesktopCloudRunState) {
		run.ResultPackageID = firstNonEmptyString(ack.ResultPackageID, run.ResultPackageID)
		run.ResultDownloaded = ack.VerifiedChecksums
		if !ack.AckedAt.IsZero() {
			ackedAt := ack.AckedAt
			run.AckedAt = &ackedAt
		}
		if ack.VerifiedChecksums {
			run.Message = "App 已下载全部成品、校验 SHA-256 并完成服务器 ACK。"
		}
	})
}

func (s *Service) persistCloudReview(ctx context.Context, projectID string, review model.ResultReviewRecord) error {
	return s.updateDesktopCloudRun(ctx, projectID, func(run *orchestrator.DesktopCloudRunState) {
		run.ResultPackageID = firstNonEmptyString(review.ResultPackageID, run.ResultPackageID)
		run.ResultReview = &orchestrator.DesktopResultReviewState{
			Decision: string(review.Decision), ReviewID: review.ReviewID,
			IdempotencyKey: review.IdempotencyKey, ReviewerInstallID: review.ReviewerInstallID,
			Summary: review.Summary, UpdatedAt: review.ReviewedAt,
		}
	})
}

func (s *Service) persistCloudRevision(ctx context.Context, projectID string, revision model.ResultRevisionRecord) error {
	return s.updateDesktopCloudRun(ctx, projectID, func(run *orchestrator.DesktopCloudRunState) {
		if run.ResultReview == nil {
			run.ResultReview = &orchestrator.DesktopResultReviewState{UpdatedAt: revision.RequestedAt}
		}
		run.ResultPackageID = firstNonEmptyString(revision.ResultPackageID, run.ResultPackageID)
		run.ResultReview.RevisionID = revision.RevisionID
		run.ResultReview.RevisionAction = string(revision.ResolvedAction)
		run.ResultReview.RevisionStatus = revision.Status
		run.ResultReview.UpdatedAt = revision.RequestedAt
		if revision.ResolvedAction == model.ResultRevisionRerecord {
			run.Message = "已提交重新录制请求。"
		} else {
			run.Message = "已提交重新剪辑请求。"
		}
	})
}

func (s *Service) persistCloudEventCursor(ctx context.Context, projectID, packageID, eventID string) error {
	eventID = strings.TrimSpace(eventID)
	if eventID == "" || !strings.HasPrefix(eventID, strings.TrimSpace(packageID)+":") {
		return nil
	}
	return s.updateDesktopCloudRun(ctx, projectID, func(run *orchestrator.DesktopCloudRunState) {
		if run.ExchangePackageID == "" || run.ExchangePackageID == packageID {
			run.ExchangePackageID = packageID
			run.LastEventID = eventID
		}
	})
}

func (s *Service) cloudEventCursor(ctx context.Context, projectID, packageID string) string {
	state, err := s.states.Load(ctx, projectID)
	if err != nil || state.DesktopCloudRun == nil || state.DesktopCloudRun.ExchangePackageID != packageID {
		return ""
	}
	return state.DesktopCloudRun.LastEventID
}
