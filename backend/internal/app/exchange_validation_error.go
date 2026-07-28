package app

import (
	"errors"
	"strings"
)

type exchangeHTTPErrorDetail struct {
	Field   string `json:"field,omitempty"`
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
}

type exchangeProtocolError struct {
	code    string
	message string
	details []exchangeHTTPErrorDetail
}

func (e *exchangeProtocolError) Error() string {
	if e == nil {
		return ""
	}
	return e.message
}

func newExchangeProtocolError(code string, field string, reason string, message string, hint string) error {
	return &exchangeProtocolError{
		code:    code,
		message: message,
		details: []exchangeHTTPErrorDetail{{
			Field:   field,
			Reason:  reason,
			Message: message,
			Hint:    hint,
		}},
	}
}

func wrapExchangeValidationError(code string, err error) error {
	if err == nil {
		return nil
	}
	message := err.Error()
	return &exchangeProtocolError{
		code:    code,
		message: message,
		details: []exchangeHTTPErrorDetail{validationDetailFromMessage(message)},
	}
}

func exchangeErrorCode(err error, fallback string) string {
	var protocolErr *exchangeProtocolError
	if errors.As(err, &protocolErr) && protocolErr.code != "" {
		return protocolErr.code
	}
	return fallback
}

func exchangeErrorDetails(err error) []exchangeHTTPErrorDetail {
	var protocolErr *exchangeProtocolError
	if !errors.As(err, &protocolErr) || len(protocolErr.details) == 0 {
		return nil
	}
	details := make([]exchangeHTTPErrorDetail, 0, len(protocolErr.details))
	for _, detail := range protocolErr.details {
		detail.Message = redactBridgeError(detail.Message)
		detail.Hint = redactBridgeError(detail.Hint)
		details = append(details, detail)
	}
	return details
}

func validationDetailFromMessage(message string) exchangeHTTPErrorDetail {
	return exchangeHTTPErrorDetail{
		Field:   inferValidationField(message),
		Reason:  inferValidationReason(message),
		Message: message,
		Hint:    inferValidationHint(message),
	}
}

func inferValidationField(message string) string {
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "upload_id"):
		return "upload_id"
	case strings.Contains(lower, "payload_ref"):
		return "payload_ref"
	case strings.Contains(lower, "envelope identity"):
		return "envelope.identity"
	case strings.Contains(lower, "exchange envelope"):
		return "envelope"
	case strings.Contains(lower, "payload_schema_version"):
		return "envelope.payload_schema_version"
	case strings.Contains(lower, "package_kind"):
		return "envelope.package_kind"
	case strings.Contains(lower, "client execution package missing required identity"):
		return "payload.identity"
	case strings.Contains(lower, "client execution package schema_version"):
		return "payload.schema_version"
	case strings.Contains(lower, "safety_report.allowed_to_upload"):
		return "payload.safety_report.allowed_to_upload"
	case strings.Contains(lower, "workflow_graph"):
		return "payload.workflow_graph"
	case strings.Contains(lower, "project_context_summary"):
		return "payload.project_context_summary"
	case strings.Contains(lower, "recording_run_spec."):
		return "payload." + fieldAfter(lower, "recording_run_spec.")
	case strings.Contains(lower, "stage_approval_plan"):
		return "payload.executable_script_bundle.stage_approval_plan"
	case strings.Contains(lower, "script_outline"):
		return "payload.executable_script_bundle.script_outline"
	case strings.Contains(lower, "agent_prompt_policy"):
		return "payload.executable_script_bundle.agent_prompt_policy"
	case strings.Contains(lower, "browser_agent_contract"):
		return "payload.executable_script_bundle.browser_agent_contract"
	case strings.Contains(lower, "node_id"):
		return "payload.executable_script_bundle.plan_json.steps[].node_id"
	case strings.Contains(lower, "executable script bundle") || strings.Contains(lower, "playwright_script") || strings.Contains(lower, "approval_markdown"):
		return "payload.executable_script_bundle"
	case strings.Contains(lower, "sandbox"):
		return "payload.recording_run_spec.sandbox_policy"
	case strings.Contains(lower, "credential grant"):
		return "payload.credential_grants"
	case strings.Contains(lower, "graph hash"):
		return "payload.reproducibility.graph_hash_sha256"
	case strings.Contains(lower, "human approval"):
		return "payload.safety_report.human_approval"
	default:
		return ""
	}
}

func inferValidationReason(message string) string {
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "expired"):
		return "expired"
	case strings.Contains(lower, "not found"):
		return "not_found"
	case strings.Contains(lower, "missing") || strings.Contains(lower, "required"):
		return "required"
	case strings.Contains(lower, "mismatch") || strings.Contains(lower, "does not match"):
		return "mismatch"
	case strings.Contains(lower, "unsupported"):
		return "unsupported"
	case strings.Contains(lower, "exceed"):
		return "out_of_bounds"
	default:
		return "invalid"
	}
}

func inferValidationHint(message string) string {
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "allowed_domains"):
		return "Include the product domain in recording_run_spec.allowed_domains and keep script domains within that list."
	case strings.Contains(lower, "base_url"):
		return "Set recording_run_spec.base_url to the product entry URL that the cloud-side recorder should open."
	case strings.Contains(lower, "raw_recording") || strings.Contains(lower, "trace"):
		return "Request both raw_recording and trace so the result package can include replay/debug materials."
	case strings.Contains(lower, "sha256") || strings.Contains(lower, "hash"):
		return "Recompute canonical hashes after changing graph, plan, markdown, or Playwright script content."
	case strings.Contains(lower, "human approval"):
		return "When envelope.policy.human_approval_required is true, include approved_at and human approval metadata."
	case strings.Contains(lower, "sandbox"):
		return "Use the agreed sandbox policy for cloud execution; do not request host mounts, docker socket, or unrestricted network access."
	case strings.Contains(lower, "signature"):
		return "Use the agreed exchange signature metadata. Dev plaintext upload can keep the existing test verifier path."
	default:
		return "Compare the request with the client execution package protocol and resend a corrected package."
	}
}

func fieldAfter(message string, prefix string) string {
	index := strings.Index(message, prefix)
	if index < 0 {
		return strings.TrimSuffix(prefix, ".")
	}
	start := index + len(prefix)
	end := start
	for end < len(message) {
		ch := message[end]
		if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '_' || ch == '.' {
			end++
			continue
		}
		break
	}
	if end == start {
		return strings.TrimSuffix(prefix, ".")
	}
	return strings.TrimSuffix(prefix, ".") + "." + message[start:end]
}
