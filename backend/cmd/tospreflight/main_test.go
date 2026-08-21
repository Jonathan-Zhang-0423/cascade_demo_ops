package main

import (
	"testing"

	"cascade-demoops/backend/internal/media"
)

func TestTOSPreflightUsesAPIEndpointForUploadWhenEndpointsDiffer(t *testing.T) {
	config := media.TOSAssetPublisherConfig{Endpoint: "tos-cn-beijing.ivolces.com", APIEndpoint: "tos-cn-beijing.volces.com"}
	if got, want := tosPreflightAPIEndpoint(config), "tos-cn-beijing.volces.com"; got != want {
		t.Fatalf("upload endpoint=%q want=%q", got, want)
	}
}

func TestTOSPreflightFallsBackToSigningEndpointWhenAPIEndpointIsUnset(t *testing.T) {
	config := media.TOSAssetPublisherConfig{Endpoint: "tos-cn-beijing.ivolces.com"}
	if got, want := tosPreflightAPIEndpoint(config), "tos-cn-beijing.ivolces.com"; got != want {
		t.Fatalf("upload endpoint=%q want=%q", got, want)
	}
}
