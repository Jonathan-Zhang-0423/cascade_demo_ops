package media

import (
	"testing"
	"time"
)

func TestMiniMaxH3CallbackDeduperOnlyWakesFirstNotification(t *testing.T) {
	payload := []byte(`{"task":{"id":"task_1","status":"succeeded"}}`)
	event, err := ParseMiniMaxH3CallbackPayload(payload, "task_1")
	if err != nil {
		t.Fatal(err)
	}
	deduper := NewMiniMaxH3CallbackDeduper(4)
	when := time.Date(2026, 8, 6, 1, 2, 3, 0, time.UTC)
	first, err := deduper.Accept(event, payload, when)
	if err != nil {
		t.Fatal(err)
	}
	if first.Duplicate || !first.QueryRequired || !first.NotificationOnly || !first.ReceivedAt.Equal(when) {
		t.Fatalf("first record = %+v", first)
	}
	second, err := deduper.Accept(event, payload, when.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if !second.Duplicate || second.QueryRequired || second.DedupKey != first.DedupKey {
		t.Fatalf("duplicate record = %+v", second)
	}
	if deduper.Len() != 1 {
		t.Fatalf("deduper len = %d", deduper.Len())
	}
}

func TestMiniMaxH3CallbackDeduperSeparatesPayloadAndBoundsMemory(t *testing.T) {
	deduper := NewMiniMaxH3CallbackDeduper(1)
	for i, payload := range [][]byte{
		[]byte(`{"id":"task_1","status":"running"}`),
		[]byte(`{"id":"task_1","status":"succeeded"}`),
	} {
		event, err := ParseMiniMaxH3CallbackPayload(payload, "task_1")
		if err != nil {
			t.Fatal(err)
		}
		record, err := deduper.Accept(event, payload, time.Time{})
		if err != nil || record.Duplicate {
			t.Fatalf("record %d = %+v err=%v", i, record, err)
		}
	}
	if deduper.Len() != 1 {
		t.Fatalf("bounded len = %d", deduper.Len())
	}
}

func TestMiniMaxH3CallbackDeduperRejectsChallengeAndNil(t *testing.T) {
	challenge, err := ParseMiniMaxH3CallbackPayload([]byte(`{"challenge":"x"}`), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewMiniMaxH3CallbackDeduper(1).Accept(challenge, []byte(`{"challenge":"x"}`), time.Time{}); err == nil {
		t.Fatal("expected challenge rejection")
	}
	var nilDeduper *MiniMaxH3CallbackDeduper
	if _, err := nilDeduper.Accept(MiniMaxH3CallbackEvent{}, nil, time.Time{}); err == nil {
		t.Fatal("expected nil deduper rejection")
	}
}
