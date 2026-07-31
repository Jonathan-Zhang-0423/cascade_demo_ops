package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestDownloadCloudDeliverableToPartialResumesWithRange(t *testing.T) {
	payload := []byte(strings.Repeat("demo-video-frame-", 64))
	partialSize := len(payload) / 3
	var rangeHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rangeHeader = r.Header.Get("Range")
		start := partialSize
		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, len(payload)-1, len(payload)))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(payload[start:])
	}))
	defer server.Close()

	partialPath := filepath.Join(t.TempDir(), "demo.mp4.part")
	if err := os.WriteFile(partialPath, payload[:partialSize], 0o600); err != nil {
		t.Fatal(err)
	}
	size, sum, mimeType, resumed, err := downloadCloudDeliverableToPartial(context.Background(), server.Client(), server.URL, "", "org_test", partialPath)
	if err != nil {
		t.Fatal(err)
	}
	wantHash := sha256.Sum256(payload)
	if rangeHeader != "bytes="+strconv.Itoa(partialSize)+"-" || !resumed {
		t.Fatalf("range=%q resumed=%v", rangeHeader, resumed)
	}
	if size != int64(len(payload)) || sum != hex.EncodeToString(wantHash[:]) || mimeType != "video/mp4" {
		t.Fatalf("size=%d sum=%q mime=%q", size, sum, mimeType)
	}
	got, err := os.ReadFile(partialPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(payload) {
		t.Fatal("resumed file does not match complete payload")
	}
}

func TestDownloadCloudDeliverableRestartsWhenServerIgnoresRange(t *testing.T) {
	payload := []byte("complete-payload")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
	}))
	defer server.Close()

	partialPath := filepath.Join(t.TempDir(), "demo.mp4.part")
	if err := os.WriteFile(partialPath, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	size, _, _, resumed, err := downloadCloudDeliverableToPartial(context.Background(), server.Client(), server.URL, "", "", partialPath)
	if err != nil {
		t.Fatal(err)
	}
	if resumed || size != int64(len(payload)) {
		t.Fatalf("resumed=%v size=%d", resumed, size)
	}
	got, _ := os.ReadFile(partialPath)
	if string(got) != string(payload) {
		t.Fatalf("partial file was not restarted: %q", got)
	}
}
