package app

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"cascade-demoops/backend/internal/model"
)

type BridgeErrorInfo struct {
	Code          string                    `json:"code"`
	Message       string                    `json:"message"`
	Details       []exchangeHTTPErrorDetail `json:"details,omitempty"`
	CorrelationID string                    `json:"correlation_id"`
	Retryable     bool                      `json:"retryable"`
}

type packagePreflightError struct {
	Message  string
	Findings []model.AgentFinding
}

func (e *packagePreflightError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	if len(e.Findings) > 0 {
		return e.Findings[0].Summary
	}
	return "client execution package failed local preflight"
}

func bridgeErrorInfo(err error) BridgeErrorInfo {
	message := ""
	if err != nil {
		message = redactBridgeError(err.Error())
	}
	info := BridgeErrorInfo{
		Code:          bridgeErrorCode(err),
		Message:       message,
		CorrelationID: bridgeCorrelationID(),
		Retryable:     bridgeErrorRetryable(err),
	}
	if details := bridgeErrorDetails(err); len(details) > 0 {
		info.Details = details
	}
	return info
}

func bridgeErrorCode(err error) string {
	if err == nil {
		return ""
	}
	var protocolErr *exchangeProtocolError
	if errors.As(err, &protocolErr) && protocolErr.code != "" {
		return protocolErr.code
	}
	var preflightErr *packagePreflightError
	if errors.As(err, &preflightErr) {
		return "preflight_failed"
	}
	lower := strings.ToLower(err.Error())
	switch {
	case isLLMJSONError(lower):
		return "llm_output_invalid"
	case isMissingEvidenceError(lower):
		return "missing_evidence"
	case strings.Contains(lower, "cloud") || strings.Contains(lower, "returned "):
		return "cloud_exchange_error"
	case strings.Contains(lower, "required") || strings.Contains(lower, "missing"):
		return "bad_request"
	case strings.Contains(lower, "mismatch") || strings.Contains(lower, "must") || strings.Contains(lower, "unsupported"):
		return "validation_failed"
	default:
		return "bridge_error"
	}
}

func bridgeErrorDetails(err error) []exchangeHTTPErrorDetail {
	if err == nil {
		return nil
	}
	if details := exchangeErrorDetails(err); len(details) > 0 {
		return details
	}
	lower := strings.ToLower(err.Error())
	if isMissingEvidenceError(lower) {
		return []exchangeHTTPErrorDetail{{
			Field:   "project_intelligence.verified_interaction_plan",
			Reason:  "blocking",
			Message: redactBridgeError(err.Error()),
			Hint:    "页面预扫描没有确认目标业务动作。请确认登录后进入工作台、目标功能控件可见，或补充截图标注 / data-testid / role/name 后重试。",
		}}
	}
	var preflightErr *packagePreflightError
	if errors.As(err, &preflightErr) {
		details := make([]exchangeHTTPErrorDetail, 0, len(preflightErr.Findings))
		for _, finding := range preflightErr.Findings {
			details = append(details, exchangeHTTPErrorDetail{
				Field:   preflightFieldFromFinding(finding),
				Reason:  string(finding.Severity),
				Message: redactBridgeError(firstNonEmptyString(finding.Summary, finding.Title, preflightErr.Error())),
				Hint:    redactBridgeError(finding.SuggestedAction),
			})
		}
		return details
	}
	message := err.Error()
	detail := validationDetailFromMessage(message)
	detail.Message = redactBridgeError(detail.Message)
	detail.Hint = redactBridgeError(detail.Hint)
	return []exchangeHTTPErrorDetail{detail}
}

func bridgeErrorRetryable(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	return isLLMJSONError(lower) || strings.Contains(lower, "timeout") || strings.Contains(lower, "temporarily")
}

func isLLMJSONError(lower string) bool {
	return strings.Contains(lower, "llm json parse failed") ||
		strings.Contains(lower, "cannot unmarshal") ||
		strings.Contains(lower, "invalid character") ||
		strings.Contains(lower, "unexpected non-whitespace character after json")
}

func isMissingEvidenceError(lower string) bool {
	return strings.Contains(lower, "missing verified interaction evidence") ||
		strings.Contains(lower, "verified interaction plan is missing") ||
		strings.Contains(lower, "no verified business action")
}

func preflightFieldFromFinding(finding model.AgentFinding) string {
	if strings.Contains(finding.ID, "hash") {
		return "payload.reproducibility"
	}
	if strings.Contains(finding.ID, "selector") {
		return "payload.executable_script_bundle.plan_json.steps.action.target"
	}
	if strings.Contains(finding.ID, "domain") {
		return "payload.recording_run_spec.allowed_domains"
	}
	if strings.Contains(finding.ID, "secret") || strings.Contains(finding.ID, "leakage") {
		return "payload"
	}
	if strings.Contains(finding.ID, "duration") {
		return "payload.recording_run_spec.timeline"
	}
	if strings.Contains(finding.ID, "business_action") {
		return "payload.executable_script_bundle.plan_json.steps"
	}
	return "payload"
}

func bridgeCorrelationID() string {
	return fmt.Sprintf("bridge_%d", time.Now().UTC().UnixNano())
}
