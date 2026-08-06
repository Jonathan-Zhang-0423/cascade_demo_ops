package media

import "testing"

func TestGeneratedShotPreflightDefaultsEveryProviderToDisabled(t *testing.T) {
	report := PreflightGeneratedShotIntent(validGeneratedShotIntent(), []GeneratedShotProviderCompiler{
		Seedance20GeneratedShotCompiler{}, MiniMaxH3GeneratedShotCompiler{},
	}, nil)
	if report.SchemaVersion != GeneratedShotPreflightSchemaVersion || report.Executable {
		t.Fatalf("report safety envelope = %+v", report)
	}
	if len(report.Providers) != 2 {
		t.Fatalf("providers = %+v", report.Providers)
	}
	for _, provider := range report.Providers {
		if !provider.CapabilityCompatible || provider.Enabled || provider.Selectable {
			t.Fatalf("provider must remain compatible but disabled: %+v", provider)
		}
		if provider.FailureCode != "provider_disabled" || provider.CompiledRequest == nil || !provider.CompiledRequest.DryRunOnly {
			t.Fatalf("provider diagnostic = %+v", provider)
		}
	}
}

func TestGeneratedShotPreflightKeepsProviderBoundariesIndependent(t *testing.T) {
	intent := validGeneratedShotIntent()
	intent.References = []GeneratedShotReference{{
		ArtifactID: "first", URI: "https://assets.example.test/first.png", MimeType: "image/png", Usage: GeneratedShotReferenceFirstFrame,
	}}
	report := PreflightGeneratedShotIntent(intent, []GeneratedShotProviderCompiler{
		Seedance20GeneratedShotCompiler{}, MiniMaxH3GeneratedShotCompiler{},
	}, []GeneratedShotProviderAvailability{
		{Provider: GeneratedShotProviderSeedance20, Enabled: true},
		{Provider: GeneratedShotProviderMiniMaxH3, Enabled: false, Reason: "H3 sidecar remains disabled during acceptance"},
	})
	seedance := preflightProvider(t, report, GeneratedShotProviderSeedance20)
	h3 := preflightProvider(t, report, GeneratedShotProviderMiniMaxH3)
	if seedance.CapabilityCompatible || seedance.Selectable || seedance.FailureCode != "provider_first_frame_not_enabled" {
		t.Fatalf("seedance preflight = %+v", seedance)
	}
	if !h3.CapabilityCompatible || h3.Enabled || h3.Selectable || h3.FailureCode != "provider_disabled" {
		t.Fatalf("h3 preflight = %+v", h3)
	}
}

func TestGeneratedShotPreflightCanSelectOnlyExplicitlyEnabledCompatibleProvider(t *testing.T) {
	report := PreflightGeneratedShotIntent(validGeneratedShotIntent(), []GeneratedShotProviderCompiler{
		Seedance20GeneratedShotCompiler{}, MiniMaxH3GeneratedShotCompiler{},
	}, []GeneratedShotProviderAvailability{
		{Provider: GeneratedShotProviderSeedance20, Enabled: true},
	})
	seedance := preflightProvider(t, report, GeneratedShotProviderSeedance20)
	h3 := preflightProvider(t, report, GeneratedShotProviderMiniMaxH3)
	if !seedance.Selectable || !seedance.CapabilityCompatible || !seedance.Enabled {
		t.Fatalf("seedance preflight = %+v", seedance)
	}
	if h3.Selectable || h3.Enabled {
		t.Fatalf("H3 must default disabled: %+v", h3)
	}
	if report.Executable {
		t.Fatal("preflight report must never become an execution authorization")
	}
}

func TestGeneratedShotPreflightRejectsLastFrameOnlyForEveryProvider(t *testing.T) {
	intent := validGeneratedShotIntent()
	intent.References = []GeneratedShotReference{{
		ArtifactID: "last", URI: "https://assets.example.test/last.png", MimeType: "image/png", Usage: GeneratedShotReferenceLastFrame,
	}}
	report := PreflightGeneratedShotIntent(intent, []GeneratedShotProviderCompiler{
		Seedance20GeneratedShotCompiler{}, MiniMaxH3GeneratedShotCompiler{},
	}, []GeneratedShotProviderAvailability{
		{Provider: GeneratedShotProviderSeedance20, Enabled: true},
		{Provider: GeneratedShotProviderMiniMaxH3, Enabled: true},
	})
	for _, provider := range report.Providers {
		if provider.CapabilityCompatible || provider.Selectable || provider.CompiledRequest != nil {
			t.Fatalf("last-frame-only must be rejected before compilation: %+v", provider)
		}
		if provider.FailureCode != "provider_last_frame_only_not_enabled" {
			t.Fatalf("unexpected failure: %+v", provider)
		}
	}
}

func preflightProvider(t *testing.T, report GeneratedShotPreflightReport, provider string) GeneratedShotProviderPreflight {
	t.Helper()
	for _, item := range report.Providers {
		if item.Provider == provider {
			return item
		}
	}
	t.Fatalf("provider %q not found in %+v", provider, report.Providers)
	return GeneratedShotProviderPreflight{}
}
