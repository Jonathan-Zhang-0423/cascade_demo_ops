package media

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

const MiniMaxH3CallbackSchemaVersion = "demoops.minimax_h3_callback.v1"

// MiniMaxH3CallbackEvent is a notification-only interpretation of a callback
// payload. It never claims task success and never registers an artifact.
type MiniMaxH3CallbackEvent struct {
	SchemaVersion    string `json:"schema_version"`
	Challenge        string `json:"challenge,omitempty"`
	TaskID           string `json:"task_id,omitempty"`
	Status           string `json:"status,omitempty"`
	VideoURL         string `json:"video_url,omitempty"`
	HasTaskEvent     bool   `json:"has_task_event"`
	RequiresQuery    bool   `json:"requires_query"`
	NotificationOnly bool   `json:"notification_only"`
}

type MiniMaxH3CallbackValidationError struct {
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *MiniMaxH3CallbackValidationError) Error() string {
	if e == nil {
		return ""
	}
	return e.Code + ": " + e.Field + ": " + e.Message
}

// ParseMiniMaxH3CallbackPayload parses challenge or task notification JSON.
// expectedTaskID is required for task events and prevents one callback from
// waking a different Server task. The parser does not perform network I/O.
func ParseMiniMaxH3CallbackPayload(raw []byte, expectedTaskID string) (MiniMaxH3CallbackEvent, error) {
	fail := func(field string, code string, message string) (MiniMaxH3CallbackEvent, error) {
		return MiniMaxH3CallbackEvent{}, &MiniMaxH3CallbackValidationError{Field: field, Code: code, Message: message}
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return fail("payload", "h3_callback_payload_empty", "callback payload is empty")
	}
	var root map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&root); err != nil {
		return fail("payload", "h3_callback_json_invalid", err.Error())
	}
	if len(root) == 0 {
		return fail("payload", "h3_callback_payload_empty", "callback payload must be a JSON object")
	}
	challenge, _ := stringValue(root["challenge"])
	rootTask := callbackTaskMap(root)
	if challenge != "" && rootTask == nil {
		return MiniMaxH3CallbackEvent{
			SchemaVersion: MiniMaxH3CallbackSchemaVersion, Challenge: challenge,
			NotificationOnly: true,
		}, nil
	}
	if challenge != "" && rootTask != nil {
		return fail("challenge", "h3_callback_challenge_event_invalid", "challenge verification and task notification must be separate payloads")
	}
	if rootTask == nil {
		return fail("task", "h3_callback_task_missing", "callback payload does not contain a task event")
	}
	taskID := firstCallbackString(rootTask, "task_id", "id")
	if taskID == "" {
		return fail("task_id", "h3_callback_task_id_missing", "task event must contain task_id or id")
	}
	if expected := strings.TrimSpace(expectedTaskID); expected != "" && taskID != expected {
		return fail("task_id", "h3_callback_task_mismatch", "callback task does not match the expected Server task")
	}
	statusRaw := firstCallbackString(rootTask, "status")
	status := normalizeMiniMaxH3TaskStatus(statusRaw)
	if status == "" {
		return fail("status", "h3_callback_status_unsupported", fmt.Sprintf("unsupported callback task status %q", statusRaw))
	}
	videoURL := callbackVideoURL(rootTask)
	return MiniMaxH3CallbackEvent{
		SchemaVersion: MiniMaxH3CallbackSchemaVersion, TaskID: taskID, Status: status,
		VideoURL: videoURL, HasTaskEvent: true, RequiresQuery: true, NotificationOnly: true,
	}, nil
}

// MiniMaxH3ChallengeResponse returns exactly the challenge bytes required by
// the provider verification handshake. It intentionally rejects empty input.
func MiniMaxH3ChallengeResponse(challenge string) ([]byte, error) {
	if strings.TrimSpace(challenge) == "" {
		return nil, &MiniMaxH3CallbackValidationError{Field: "challenge", Code: "h3_callback_challenge_missing", Message: "challenge is required"}
	}
	return []byte(challenge), nil
}

func callbackTaskMap(root map[string]any) map[string]any {
	if hasCallbackTaskFields(root) {
		return root
	}
	for _, key := range []string{"task", "data", "payload"} {
		if nested, ok := root[key].(map[string]any); ok {
			if task := callbackTaskMap(nested); task != nil {
				return task
			}
		}
	}
	return nil
}

func hasCallbackTaskFields(value map[string]any) bool {
	for _, key := range []string{"task_id", "id", "status", "content", "output"} {
		if _, ok := value[key]; ok {
			return true
		}
	}
	return false
}

func callbackVideoURL(task map[string]any) string {
	if content, ok := task["content"].(map[string]any); ok {
		if value, _ := stringValue(content["url"]); validMiniMaxH3CallbackURL(value) {
			return value
		}
	}
	if output, ok := task["output"].(map[string]any); ok {
		if value, _ := stringValue(output["video_url"]); validMiniMaxH3CallbackURL(value) {
			return value
		}
	}
	if value, _ := stringValue(task["video_url"]); validMiniMaxH3CallbackURL(value) {
		return value
	}
	return ""
}

func validMiniMaxH3CallbackURL(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	return err == nil && strings.EqualFold(parsed.Scheme, "https") && strings.TrimSpace(parsed.Host) != ""
}

func firstCallbackString(value map[string]any, keys ...string) string {
	for _, key := range keys {
		if result, _ := stringValue(value[key]); strings.TrimSpace(result) != "" {
			return strings.TrimSpace(result)
		}
	}
	return ""
}

func stringValue(value any) (string, bool) {
	result, ok := value.(string)
	return strings.TrimSpace(result), ok
}

func asMiniMaxH3CallbackValidationError(err error) *MiniMaxH3CallbackValidationError {
	var target *MiniMaxH3CallbackValidationError
	if errors.As(err, &target) {
		return target
	}
	return nil
}
