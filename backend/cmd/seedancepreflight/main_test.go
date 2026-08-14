package main

import "testing"

func TestCandidateExtensionPrefersResponseContentType(t *testing.T) {
	for _, test := range []struct {
		contentType string
		url         string
		want        string
	}{
		{"video/mp4", "https://asset.example/no-extension", ".mp4"},
		{"image/jpeg; charset=binary", "https://asset.example/generated.mp4", ".jpg"},
		{"", "https://asset.example/generated.webp?token=redacted", ".webp"},
		{"application/octet-stream", "https://asset.example/no-extension", ".bin"},
	} {
		if got := candidateExtension(test.contentType, test.url); got != test.want {
			t.Fatalf("candidateExtension(%q, %q)=%q want %q", test.contentType, test.url, got, test.want)
		}
	}
}
