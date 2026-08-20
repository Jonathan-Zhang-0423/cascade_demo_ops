package media

import "testing"

func TestSeedance25ReadinessRequiresPrivateTOSAndNeverCallsNetwork(t *testing.T) {
	values := map[string]string{
		"CASCADE_ARK_MEDIA_MODE": "real", "SEEDANCE_MODEL": Seedance25ServerModel, "SEEDANCE_API_KEY": "secret-key", "SEEDANCE_BASE_URL": "https://ark.cn-beijing.volces.com/api/v3",
		"CASCADE_FFMPEG_PATH": "D:\\ffmpeg.exe", "CASCADE_FFPROBE_PATH": "D:\\ffprobe.exe",
		"VOLC_TOS_ACCESS_KEY": "secret-ak", "VOLC_TOS_SECRET_KEY": "secret-sk", "VOLC_TOS_ENDPOINT": "private-bucket.tos-cn-beijing.ivolces.com", "VOLC_TOS_REGION": "cn-beijing", "VOLC_TOS_BUCKET": "bucket",
		"VOLC_TOS_SIGNED_URL_TTL_SEC": "3600",
	}
	commands := 0
	report := Seedance25ReadinessFromEnv(func(name string) string { return values[name] }, func(string) bool { commands++; return true })
	if !report.Ready || report.NetworkCalled || commands != 2 {
		t.Fatalf("readiness = %+v commands=%d", report, commands)
	}
	for _, check := range report.Checks {
		if check.Detail == "secret-key" || check.Detail == "secret-ak" || check.Detail == "secret-sk" {
			t.Fatalf("readiness leaked a secret: %+v", check)
		}
	}
	if !readinessCheckPassed(report, "seedance_2_5_admission_policy") {
		t.Fatalf("default Server-owned admission policy should be ready: %+v", report)
	}
}

func TestSeedance25ReadinessRejectsInvalidAdmissionPolicy(t *testing.T) {
	values := map[string]string{
		"CASCADE_ARK_MEDIA_MODE": "real", "SEEDANCE_MODEL": Seedance25ServerModel, "SEEDANCE_API_KEY": "secret-key", "SEEDANCE_BASE_URL": "https://ark.cn-beijing.volces.com/api/v3",
		"CASCADE_FFMPEG_PATH": "D:\\ffmpeg.exe", "CASCADE_FFPROBE_PATH": "D:\\ffprobe.exe",
		"VOLC_TOS_ACCESS_KEY": "secret-ak", "VOLC_TOS_SECRET_KEY": "secret-sk", "VOLC_TOS_ENDPOINT": "private-bucket.tos-cn-beijing.ivolces.com", "VOLC_TOS_REGION": "cn-beijing", "VOLC_TOS_BUCKET": "bucket",
		Seedance25MaxConcurrentEnv: "not-a-number",
	}
	report := Seedance25ReadinessFromEnv(func(name string) string { return values[name] }, func(string) bool { return true })
	if report.Ready || readinessCheckPassed(report, "seedance_2_5_admission_policy") {
		t.Fatalf("invalid admission policy must block readiness: %+v", report)
	}
}

func readinessCheckPassed(report Seedance25ReadinessReport, name string) bool {
	for _, check := range report.Checks {
		if check.Name == name {
			return check.Passed
		}
	}
	return false
}

func TestSeedance25ReadinessBlocksPublicTOSAndWrongModel(t *testing.T) {
	values := map[string]string{
		"CASCADE_ARK_MEDIA_MODE": "real", "SEEDANCE_MODEL": "doubao-seedance-2-0-260128", "SEEDANCE_API_KEY": "key", "SEEDANCE_BASE_URL": "https://ark.example/api/v3",
		"VOLC_TOS_ACCESS_KEY": "ak", "VOLC_TOS_SECRET_KEY": "sk", "VOLC_TOS_ENDPOINT": "bucket.tos-cn-beijing.volces.com", "VOLC_TOS_REGION": "cn-beijing", "VOLC_TOS_BUCKET": "bucket",
		"VOLC_TOS_SIGNED_URL_TTL_SEC": "60",
	}
	report := Seedance25ReadinessFromEnv(func(name string) string { return values[name] }, func(string) bool { return true })
	if report.Ready {
		t.Fatal("Seedance 2.5 readiness must fail closed for wrong model and public TOS endpoint")
	}
	failed := map[string]bool{}
	for _, check := range report.Checks {
		if !check.Passed {
			failed[check.Name] = true
		}
	}
	for _, name := range []string{"seedance_2_5_model_selected", "tos_endpoint_ark_private", "tos_signed_url_ttl_30m_to_24h"} {
		if !failed[name] {
			t.Fatalf("expected failed check %q: %+v", name, report)
		}
	}
}
