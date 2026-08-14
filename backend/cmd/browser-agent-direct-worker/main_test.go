package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cascade-demoops/backend/internal/directtransport"
)

func TestWorkerClaimRejectsProtocolMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/worker/jobs/claim" || request.Header.Get("Authorization") != "Bearer worker-test-token" {
			http.Error(response, "unexpected request", http.StatusBadRequest)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(response).Encode(directtransport.WorkerJob{ProtocolVersion: "cascade.browser_agent_worker.v0", JobID: "job_mismatch"})
	}))
	t.Cleanup(server.Close)
	w := &worker{client: server.Client(), baseURL: server.URL, token: "worker-test-token"}

	_, ok, err := w.claim(context.Background())
	if err == nil || ok || !strings.Contains(err.Error(), "worker protocol mismatch") {
		t.Fatalf("mismatched worker claim was accepted: ok=%v err=%v", ok, err)
	}
}
