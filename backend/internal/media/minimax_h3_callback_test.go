package media

import (
	"testing"
)

func TestParseMiniMaxH3CallbackChallengeWithoutTaskSideEffects(t *testing.T) {
	event, err := ParseMiniMaxH3CallbackPayload([]byte(`{"challenge":"verify-123"}`), "")
	if err != nil {
		t.Fatal(err)
	}
	if event.Challenge != "verify-123" || event.HasTaskEvent || event.RequiresQuery || !event.NotificationOnly {
		t.Fatalf("challenge event = %+v", event)
	}
	response, err := MiniMaxH3ChallengeResponse(event.Challenge)
	if err != nil || string(response) != "verify-123" {
		t.Fatalf("challenge response = %q err=%v", response, err)
	}
}

func TestParseMiniMaxH3CallbackTaskRequiresQueryConfirmation(t *testing.T) {
	event, err := ParseMiniMaxH3CallbackPayload([]byte(`{"task":{"id":"task_1","status":"succeeded","content":{"url":"https://cdn.example.test/video.mp4"}}}`), "task_1")
	if err != nil {
		t.Fatal(err)
	}
	if event.TaskID != "task_1" || event.Status != MiniMaxH3TaskSucceeded || event.VideoURL != "https://cdn.example.test/video.mp4" || !event.RequiresQuery || !event.NotificationOnly {
		t.Fatalf("task event = %+v", event)
	}
}

func TestParseMiniMaxH3CallbackDoesNotClaimSuccessFromCallbackAlone(t *testing.T) {
	event, err := ParseMiniMaxH3CallbackPayload([]byte(`{"id":"task_1","status":"succeeded"}`), "task_1")
	if err != nil {
		t.Fatal(err)
	}
	if event.Status != MiniMaxH3TaskSucceeded || event.VideoURL != "" || !event.RequiresQuery {
		t.Fatalf("callback must require query and not invent output URL: %+v", event)
	}
}

func TestParseMiniMaxH3CallbackRejectsTaskMismatchUnknownStatusAndNonHTTPSURL(t *testing.T) {
	if _, err := ParseMiniMaxH3CallbackPayload([]byte(`{"id":"task_other","status":"running"}`), "task_1"); err == nil {
		t.Fatal("expected task mismatch rejection")
	}
	if _, err := ParseMiniMaxH3CallbackPayload([]byte(`{"id":"task_1","status":"done"}`), "task_1"); err == nil {
		t.Fatal("expected unknown status rejection")
	}
	event, err := ParseMiniMaxH3CallbackPayload([]byte(`{"id":"task_1","status":"succeeded","content":{"url":"http://cdn.example.test/video.mp4"}}`), "task_1")
	if err != nil {
		t.Fatal(err)
	}
	if event.VideoURL != "" {
		t.Fatalf("non-HTTPS callback URL must not be accepted: %+v", event)
	}
}

func TestMiniMaxH3ChallengeResponseRejectsEmptyChallenge(t *testing.T) {
	if _, err := MiniMaxH3ChallengeResponse(" "); err == nil {
		t.Fatal("expected empty challenge rejection")
	}
}
