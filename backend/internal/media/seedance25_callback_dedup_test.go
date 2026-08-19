package media

import (
	"testing"
	"time"
)

func TestSeedance25CallbackDeduperUsesTaskStatusAndUpdatedAt(t *testing.T) {
	event, err := ParseSeedance25CallbackPayload([]byte(`{"id":"task_25","model":"doubao-seedance-2-5-260628","status":"running","updated_at":1766000001}`), "task_25")
	if err != nil {
		t.Fatal(err)
	}
	deduper := NewSeedance25CallbackDeduper(2)
	when := time.Date(2026, 8, 19, 2, 3, 4, 0, time.UTC)
	first, err := deduper.Accept(event, when)
	if err != nil {
		t.Fatal(err)
	}
	if first.Duplicate || !first.QueryRequired || first.ReceivedAt != when {
		t.Fatalf("first wake record = %+v", first)
	}
	second, err := deduper.Accept(event, when.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if !second.Duplicate || second.QueryRequired || second.DedupKey != first.DedupKey {
		t.Fatalf("duplicate wake record = %+v", second)
	}
	event.UpdatedAtUnixSec++
	third, err := deduper.Accept(event, when.Add(2*time.Second))
	if err != nil || third.Duplicate || !third.QueryRequired {
		t.Fatalf("new state version must wake query: %+v err=%v", third, err)
	}
}

func TestSeedance25CallbackDeduperRejectsUnauditableEvent(t *testing.T) {
	if _, err := NewSeedance25CallbackDeduper(1).Accept(Seedance25CallbackEvent{TaskID: "task_25", Status: "cancelled"}, time.Time{}); err == nil {
		t.Fatal("expected callback event validation failure")
	}
}
