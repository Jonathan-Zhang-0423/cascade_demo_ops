package media

import (
	"testing"
)

func TestParseSeedance25CallbackRequiresQueryAndOnlyAcceptsOfficialStatuses(t *testing.T) {
	payload := []byte(`{
		"id":"task_25","model":"doubao-seedance-2-5-260628","status":"succeeded","updated_at":1766000001,
		"content":{"video_url":"https://ark-content-generation-cn-beijing.tos-cn-beijing.volces.com/object.mp4?redacted"},
		"usage":{"completion_tokens":1}
	}`)
	event, err := ParseSeedance25CallbackPayload(payload, "task_25")
	if err != nil {
		t.Fatal(err)
	}
	if !event.RequiresQuery || !event.NotificationOnly || event.TaskID != "task_25" || event.UpdatedAtUnixSec != 1766000001 || event.VideoURL == "" {
		t.Fatalf("callback event = %+v", event)
	}
	for _, status := range []string{"cancelled", "unknown"} {
		if _, err := ParseSeedance25CallbackPayload([]byte(`{"id":"task_25","model":"doubao-seedance-2-5-260628","status":"`+status+`","updated_at":1766000001}`), "task_25"); err == nil {
			t.Fatalf("status %q must not be treated as a guaranteed callback", status)
		}
	}
}

func TestParseSeedance25CallbackRejectsMismatchAndUntrustedOutputURL(t *testing.T) {
	if _, err := ParseSeedance25CallbackPayload([]byte(`{"id":"other","model":"doubao-seedance-2-5-260628","status":"running","updated_at":1766000001}`), "task_25"); err == nil {
		t.Fatal("expected task mismatch")
	}
	event, err := ParseSeedance25CallbackPayload([]byte(`{"id":"task_25","model":"doubao-seedance-2-5-260628","status":"succeeded","updated_at":1766000001,"content":{"video_url":"https://untrusted.example/video.mp4"}}`), "task_25")
	if err != nil {
		t.Fatal(err)
	}
	if event.VideoURL != "" || !event.RequiresQuery {
		t.Fatalf("untrusted callback output must not become download authority: %+v", event)
	}
}
