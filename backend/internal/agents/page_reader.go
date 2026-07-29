package agents

import (
	"context"
	"fmt"
	"strings"
	"time"

	"cascade-demoops/backend/internal/model"
)

type PageReaderAgent struct{}

func NewPageReaderAgent() *PageReaderAgent { return &PageReaderAgent{} }

func (a *PageReaderAgent) ReadPages(ctx context.Context, project *model.ProjectContext, brief *model.RequirementBrief) ([]model.PageUnderstandingSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	pages := []model.PageUnderstandingSnapshot{}
	if project == nil {
		return pages, nil
	}
	now := time.Now().UTC()
	if strings.TrimSpace(project.ProductURL) != "" {
		page := model.PageUnderstandingSnapshot{
			ID:            "page_product_url",
			ProjectID:     project.ID,
			SchemaVersion: model.MultimodalUnderstandingReportSchemaVersion,
			URL:           project.ProductURL,
			Title:         "产品入口",
			PageRole:      "entry",
			VisionSummary: "基于产品 URL 生成的页面入口理解；等待后续 Playwright 深度扫描补充 DOM 和可访问性树。",
			States:        []string{"待登录或已登录入口", "可作为脚本第一步"},
			StableSelectors: []model.SelectorCandidate{
				{Kind: "css", Value: "body", Confidence: 0.6, StabilityScore: 0.5, Source: "url_input"},
				{Kind: "css", Value: "main", Confidence: 0.52, StabilityScore: 0.48, Source: "url_input"},
			},
			Actions: []model.PageActionInsight{{
				ID:           "action_open_product",
				Label:        "打开产品入口",
				Kind:         "navigate",
				TargetURL:    project.ProductURL,
				Confidence:   0.72,
				EvidenceRefs: []model.EvidenceRef{{ID: "ev_product_url", Kind: model.EvidenceKindBrowserScan, Summary: "产品 URL 输入", Confidence: 0.72}},
			}},
			EvidenceRefs: []model.EvidenceRef{{ID: "ev_product_url", Kind: model.EvidenceKindBrowserScan, Summary: "产品 URL 输入", Confidence: 0.72}},
			Confidence:   0.7,
			CreatedAt:    now,
		}
		page.ProductIdentitySignals = productIdentitySignalsForPage(page.URL, page.Title, page.EvidenceRefs)
		pages = append(pages, page)
	}
	if project.Inputs != nil {
		for i, screenshot := range project.Inputs.WebpageScreenshots {
			page := model.PageUnderstandingSnapshot{
				ID:            firstNonEmpty(screenshot.ID, fmt.Sprintf("page_screenshot_%d", i+1)),
				ProjectID:     project.ID,
				SchemaVersion: model.MultimodalUnderstandingReportSchemaVersion,
				URL:           screenshot.URL,
				Title:         firstNonEmpty(screenshot.Title, "页面截图"),
				PageRole:      firstNonEmpty(screenshot.PageRole, "evidence"),
				ScreenshotRef: &screenshot.Artifact,
				OCRText:       screenshot.OCRText,
				VisionSummary: firstNonEmpty(screenshot.VisionSummary, "截图已进入多模态理解输入。"),
				States:        []string{"截图可见状态", "可用于人工审批预览"},
				Confidence:    0.82,
				CapturedAt:    screenshot.CapturedAt,
				CreatedAt:     now,
			}
			if page.CapturedAt.IsZero() {
				page.CapturedAt = now
			}
			page.EvidenceRefs = append(page.EvidenceRefs, model.EvidenceRef{
				ID:         firstNonEmpty(screenshot.ID, fmt.Sprintf("ev_screenshot_%d", i+1)),
				Kind:       model.EvidenceKindWebScreenshot,
				Summary:    page.VisionSummary,
				ArtifactID: screenshot.Artifact.ID,
				Confidence: 0.82,
			})
			page.ProductIdentitySignals = productIdentitySignalsForPageInput(screenshot, page.EvidenceRefs)
			for _, annotation := range screenshot.Annotations {
				if annotation.SelectorHint != "" {
					page.StableSelectors = append(page.StableSelectors, model.SelectorCandidate{
						Kind:           "css",
						Value:          annotation.SelectorHint,
						Confidence:     0.74,
						StabilityScore: 0.68,
						Source:         "screenshot_annotation",
						EvidenceRefs:   page.EvidenceRefs,
					})
				}
				page.Actions = append(page.Actions, model.PageActionInsight{
					ID:           firstNonEmpty(annotation.ID, "action_"+shortHash(annotation.Label+annotation.SelectorHint)),
					Label:        firstNonEmpty(annotation.Label, annotation.Description, "页面操作"),
					Kind:         firstNonEmpty(annotation.Kind, "inspect"),
					SelectorHint: annotation.SelectorHint,
					FeatureRef:   annotation.FeatureRef,
					EvidenceRefs: page.EvidenceRefs,
					Confidence:   0.7,
				})
			}
			page.RiskFindings = pageRiskFindings(project, page)
			pages = append(pages, page)
		}
	}
	return pages, nil
}

func pageRiskFindings(project *model.ProjectContext, page model.PageUnderstandingSnapshot) []model.AgentFinding {
	findings := []model.AgentFinding{}
	normalizedURL := strings.ToLower(page.URL)
	for _, forbidden := range project.ForbiddenPages {
		if forbidden != "" && strings.Contains(normalizedURL, strings.ToLower(forbidden)) {
			findings = append(findings, model.AgentFinding{
				ID:           "finding_forbidden_page_" + shortHash(forbidden),
				Kind:         "forbidden_page",
				Severity:     model.FindingSeverityBlocking,
				Summary:      "页面命中禁止访问路径，需要从脚本中排除。",
				EvidenceRefs: page.EvidenceRefs,
				Confidence:   0.9,
			})
		}
	}
	lowerText := strings.ToLower(page.OCRText + " " + page.VisionSummary)
	for _, forbidden := range project.ForbiddenData {
		if forbidden != "" && strings.Contains(lowerText, strings.ToLower(forbidden)) {
			findings = append(findings, model.AgentFinding{
				ID:           "finding_forbidden_data_" + shortHash(forbidden),
				Kind:         "forbidden_data",
				Severity:     model.FindingSeverityWarning,
				Summary:      "页面证据中出现禁止展示的数据提示，需要打码或避开。",
				EvidenceRefs: page.EvidenceRefs,
				Confidence:   0.78,
			})
		}
	}
	return findings
}
