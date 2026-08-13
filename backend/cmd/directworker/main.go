package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const workerProtocolVersion = "cascade.browser_agent_worker.v1"

func main() {
	baseURL := flag.String("gateway-url", "http://127.0.0.1:18444", "loopback Worker API URL")
	poll := flag.Duration("poll-interval", 500*time.Millisecond, "queue poll interval")
	flag.Parse()
	token := strings.TrimSpace(os.Getenv("CASCADE_DIRECT_WORKER_TOKEN"))
	if token == "" {
		fatal("CASCADE_DIRECT_WORKER_TOKEN is required")
	}
	if err := validateLoopbackWorkerURL(*baseURL); err != nil {
		fatal(err.Error())
	}
	client := &http.Client{Timeout: 2 * time.Minute}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ticker := time.NewTicker(*poll)
	defer ticker.Stop()
	for {
		if err := dispatchQueued(ctx, client, strings.TrimRight(*baseURL, "/"), token); err != nil {
			fmt.Fprintln(os.Stderr, "direct worker poll:", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func dispatchQueued(ctx context.Context, client *http.Client, baseURL, token string) error {
	if err := heartbeat(ctx, client, baseURL, token); err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/v1/worker/jobs/queued", nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("queue request failed (%d): %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	var payload struct {
		JobIDs []string `json:"job_ids"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return err
	}
	for _, jobID := range payload.JobIDs {
		if strings.TrimSpace(jobID) == "" {
			continue
		}
		if err := claimJob(ctx, client, baseURL, token, jobID); err != nil {
			return err
		}
		if err := runJob(ctx, client, baseURL, token, jobID); err != nil {
			return err
		}
	}
	return nil
}

func heartbeat(ctx context.Context, client *http.Client, baseURL, token string) error {
	body, err := json.Marshal(map[string]string{
		"mode":             "external",
		"protocol_version": workerProtocolVersion,
	})
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/v1/worker/heartbeat", bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("worker heartbeat failed (%d): %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func claimJob(ctx context.Context, client *http.Client, baseURL, token, jobID string) error {
	body, err := json.Marshal(map[string]string{
		"protocol_version": workerProtocolVersion,
		"job_id":           jobID,
	})
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/v1/worker/jobs/claim", bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("claim %s failed (%d): %s", jobID, response.StatusCode, strings.TrimSpace(string(body)))
	}
	var payload struct {
		ProtocolVersion string          `json:"protocol_version"`
		JobID           string          `json:"job_id"`
		PackageJSON     json.RawMessage `json:"package_json"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return fmt.Errorf("decode claim %s response: %w", jobID, err)
	}
	if payload.ProtocolVersion != workerProtocolVersion {
		return fmt.Errorf("claim %s returned unsupported worker protocol %q", jobID, payload.ProtocolVersion)
	}
	if payload.JobID != jobID || len(payload.PackageJSON) == 0 {
		return fmt.Errorf("claim %s returned invalid job binding", jobID)
	}
	return nil
}

func runJob(ctx context.Context, client *http.Client, baseURL, token, jobID string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/v1/worker/jobs/"+url.PathEscape(jobID)+"/run", nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("run %s failed (%d): %s", jobID, response.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func validateLoopbackWorkerURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "http" {
		return fmt.Errorf("worker gateway URL must be plain HTTP loopback")
	}
	host := parsed.Hostname()
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("worker gateway URL must resolve to loopback")
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil || port != 18444 {
		return fmt.Errorf("worker gateway URL must use loopback port 18444")
	}
	return nil
}

func fatal(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}
