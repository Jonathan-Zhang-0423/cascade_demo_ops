package media

import (
	"testing"
	"time"
)

func TestSeedance25AdmissionEnforcesConcurrentAndRPMLimits(t *testing.T) {
	now := time.Unix(1_760_000_000, 0).UTC()
	gate, err := NewSeedance25AdmissionGate(Seedance25AdmissionPolicy{MaxConcurrent: 1, Window: time.Minute, MaxRequestsPerWindow: 1, IdempotencyTTL: time.Hour}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	request := seedance25AdmissionRequest()
	first, err := gate.Acquire("finalfilm:job_1", "key_1", request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gate.Acquire("finalfilm:job_2", "key_2", request); admissionCode(err) != Seedance25AdmissionConcurrentLimit {
		t.Fatalf("expected concurrent limit, got %v", err)
	}
	first.BindProviderTask("task_1")
	first.Finish(true)
	if _, err := gate.Acquire("finalfilm:job_2", "key_2", request); admissionCode(err) != Seedance25AdmissionRequestQuota {
		t.Fatalf("expected RPM limit, got %v", err)
	}
	now = now.Add(time.Minute)
	if _, err := gate.Acquire("finalfilm:job_2", "key_2", request); err != nil {
		t.Fatalf("new window should accept task creation: %v", err)
	}
}

func TestSeedance25AdmissionResumesUnknownRemoteTaskWithoutDuplicateCreate(t *testing.T) {
	now := time.Unix(1_760_000_000, 0).UTC()
	gate, err := NewSeedance25AdmissionGate(Seedance25AdmissionPolicy{MaxConcurrent: 1, Window: time.Minute, MaxRequestsPerWindow: 1, IdempotencyTTL: time.Hour}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	request := seedance25AdmissionRequest()
	first, err := gate.Acquire("finalfilm:job_1", "key_1", request)
	if err != nil {
		t.Fatal(err)
	}
	first.BindProviderTask("task_remote_1")
	first.Finish(false)
	if snapshot := gate.Snapshot(); snapshot.Concurrent != 1 || snapshot.RequestsUsed != 1 {
		t.Fatalf("unknown remote task must retain quota: %+v", snapshot)
	}
	resumed, err := gate.Acquire("finalfilm:job_1", "key_1", request)
	if err != nil || !resumed.Replayed || resumed.ResumeProviderTaskID != "task_remote_1" {
		t.Fatalf("same key must resume existing task: lease=%+v err=%v", resumed, err)
	}
	if _, err := gate.Acquire("finalfilm:job_2", "key_2", request); admissionCode(err) != Seedance25AdmissionConcurrentLimit {
		t.Fatalf("second task must remain blocked while remote task is unresolved: %v", err)
	}
	resumed.Finish(true)
	if snapshot := gate.Snapshot(); snapshot.Concurrent != 0 || snapshot.RequestsUsed != 1 {
		t.Fatalf("terminal task must release only concurrency: %+v", snapshot)
	}
}

func TestSeedance25AdmissionPolicyUsesConservativeDefaultsAndRejectsInvalidValues(t *testing.T) {
	policy, err := Seedance25AdmissionPolicyFromEnv(func(string) string { return "" })
	if err != nil || policy.MaxConcurrent != 3 || policy.MaxRequestsPerWindow != 180 || policy.Window != time.Minute {
		t.Fatalf("unexpected conservative default policy: %+v err=%v", policy, err)
	}
	if _, err := Seedance25AdmissionPolicyFromEnv(func(name string) string {
		if name == Seedance25MaxConcurrentEnv {
			return "zero"
		}
		return ""
	}); admissionCode(err) != Seedance25AdmissionInvalidPolicy {
		t.Fatalf("invalid environment policy was accepted: %v", err)
	}
}

func seedance25AdmissionRequest() ContentGenerationTaskRequest {
	return ContentGenerationTaskRequest{Model: Seedance25ServerModel, Content: []ContentPart{{Type: "text", Role: "user", Text: "presentation intro"}}, Duration: 5, Resolution: "1080p", OutputFormat: "mp4"}
}

func admissionCode(err error) string {
	if typed, ok := err.(*Seedance25AdmissionError); ok && typed != nil {
		return typed.Code
	}
	return ""
}
