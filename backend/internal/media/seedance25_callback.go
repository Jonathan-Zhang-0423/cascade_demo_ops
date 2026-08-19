package media

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

const Seedance25CallbackSchemaVersion = "demoops.seedance_2_5_callback.v1"

// Seedance25CallbackEvent is a notification-only view of an Ark callback.
// It cannot mark a provider task successful or register an output artifact;
// the normal GET task query remains the source of truth.
type Seedance25CallbackEvent struct {
	SchemaVersion     string `json:"schema_version"`
	TaskID            string `json:"task_id"`
	Model             string `json:"model"`
	Status            string `json:"status"`
	UpdatedAtUnixSec  int64  `json:"updated_at_unix_sec"`
	VideoURL          string `json:"video_url,omitempty"`
	ProviderErrorCode string `json:"provider_error_code,omitempty"`
	RequiresQuery     bool   `json:"requires_query"`
	NotificationOnly  bool   `json:"notification_only"`
}

type Seedance25CallbackValidationError struct {
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Seedance25CallbackValidationError) Error() string {
	if e == nil {
		return ""
	}
	return e.Code + ": " + e.Field + ": " + e.Message
}

// ParseSeedance25CallbackPayload parses only callback statuses that Ark
// documents for callback_url: queued, running, succeeded, failed, expired.
// `cancelled` is intentionally rejected here because Ark does not promise a
// cancellation callback; cancellation state is established by DELETE + GET.
func ParseSeedance25CallbackPayload(raw []byte, expectedTaskID string) (Seedance25CallbackEvent, error) {
	fail := func(field string, code string, message string) (Seedance25CallbackEvent, error) {
		return Seedance25CallbackEvent{}, &Seedance25CallbackValidationError{Field: field, Code: code, Message: message}
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return fail("payload", "seedance_2_5_callback_payload_empty", "callback payload is empty")
	}
	var payload seedance25CallbackPayload
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return fail("payload", "seedance_2_5_callback_json_invalid", err.Error())
	}
	if strings.TrimSpace(payload.ID) == "" {
		return fail("id", "seedance_2_5_callback_task_id_missing", "callback task id is required")
	}
	if expected := strings.TrimSpace(expectedTaskID); expected != "" && payload.ID != expected {
		return fail("id", "seedance_2_5_callback_task_mismatch", "callback task id does not match the Server task")
	}
	if payload.Model != Seedance25ServerModel {
		return fail("model", "seedance_2_5_callback_model_mismatch", "callback model is not the registered Seedance 2.5 model")
	}
	if !seedance25CallbackStatusAllowed(payload.Status) {
		return fail("status", "seedance_2_5_callback_status_unsupported", fmt.Sprintf("callback status %q is not an officially guaranteed Seedance 2.5 callback status", payload.Status))
	}
	updatedAt, err := payload.UpdatedAt.Int64()
	if err != nil || updatedAt <= 0 {
		return fail("updated_at", "seedance_2_5_callback_updated_at_invalid", "callback updated_at must be a positive Unix-second integer")
	}
	event := Seedance25CallbackEvent{
		SchemaVersion: Seedance25CallbackSchemaVersion,
		TaskID:        payload.ID, Model: payload.Model, Status: payload.Status, UpdatedAtUnixSec: updatedAt,
		RequiresQuery: true, NotificationOnly: true,
	}
	if payload.Error != nil {
		event.ProviderErrorCode = strings.TrimSpace(payload.Error.Code)
	}
	if payload.Content != nil && seedance25CallbackOutputURLAllowed(payload.Content.VideoURL) {
		event.VideoURL = strings.TrimSpace(payload.Content.VideoURL)
	}
	return event, nil
}

type seedance25CallbackPayload struct {
	ID        string             `json:"id"`
	Model     string             `json:"model"`
	Status    string             `json:"status"`
	UpdatedAt json.Number        `json:"updated_at"`
	Content   *seedance25Content `json:"content,omitempty"`
	Error     *ProviderError     `json:"error,omitempty"`
}

type seedance25Content struct {
	VideoURL string `json:"video_url,omitempty"`
}

func seedance25CallbackStatusAllowed(status string) bool {
	switch strings.TrimSpace(status) {
	case "queued", "running", "succeeded", "failed", "expired":
		return true
	default:
		return false
	}
}

func seedance25CallbackOutputURLAllowed(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") {
		return false
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	return strings.HasSuffix(host, ".tos-cn-beijing.volces.com")
}

func asSeedance25CallbackValidationError(err error) *Seedance25CallbackValidationError {
	var target *Seedance25CallbackValidationError
	if errors.As(err, &target) {
		return target
	}
	return nil
}
