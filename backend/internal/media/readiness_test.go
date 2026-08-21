package media

import (
	"testing"
)

func TestMediaReadinessDoesNotCallNetworkAndRedactsValues(t *testing.T) {
	values := map[string]string{
		"CASCADE_ARK_MEDIA_MODE": "real", "CASCADE_VIDEO_PROVIDER": "seedance", "CASCADE_VIDEO_MODEL": "doubao-seedance-test",
		"CASCADE_FFMPEG_PATH": "C:\\secret\\ffmpeg.exe", "CASCADE_FFPROBE_PATH": "C:\\secret\\ffprobe.exe",
		"SEEDANCE_API_KEY": "secret-seedance", "SEEDANCE_BASE_URL": "https://ark.example/api/v3", "SEEDANCE_MODEL": "doubao-seedance-test",
		"VOLC_TOS_ACCESS_KEY": "secret-ak", "VOLC_TOS_SECRET_KEY": "secret-sk", "VOLC_TOS_ENDPOINT": "tos-cn-beijing.volces.com", "VOLC_TOS_REGION": "cn-beijing", "VOLC_TOS_BUCKET": "bucket",
		"VOLC_TTS_ACCESS_KEY": "secret-tts", "VOLC_TTS_RESOURCE_ID": "seed-tts-2.0", "VOLC_TTS_SPEAKER": "voice",
	}
	checks := 0
	r := MediaReadinessFromEnv(func(name string) string { return values[name] }, func(name string) bool { checks++; return true })
	if !r.Ready || r.NetworkCalled || checks != 2 {
		t.Fatalf("unexpected readiness: %+v checks=%d", r, checks)
	}
	for _, check := range r.Checks {
		if check.Detail == "secret-ak" || check.Detail == "secret-sk" || check.Detail == "secret-seedance" || check.Detail == "secret-tts" {
			t.Fatalf("readiness leaked a credential: %+v", check)
		}
	}
}

func TestMediaReadinessReportsMissingTTSAndRealMode(t *testing.T) {
	r := MediaReadinessFromEnv(func(name string) string {
		return map[string]string{
			"CASCADE_FFMPEG_PATH": "ffmpeg", "CASCADE_FFPROBE_PATH": "ffprobe", "CASCADE_ARK_MEDIA_MODE": "dry_run",
			"SEEDANCE_API_KEY": "key", "SEEDANCE_BASE_URL": "https://ark.example/api/v3", "SEEDANCE_MODEL": "model",
		}[name]
	}, func(string) bool { return true })
	if r.Ready || r.VideoReady || r.TTSReady {
		t.Fatal("non-real media mode must block video readiness and missing TTS must remain visible")
	}
	for _, name := range []string{"ark_media_mode_real", "tts_agent_plan_api_key_configured", "tts_speaker_configured"} {
		found := false
		for _, check := range r.Checks {
			if check.Name == name {
				found = true
				if check.Passed {
					t.Fatalf("%s unexpectedly passed", name)
				}
			}
		}
		if !found {
			t.Fatalf("missing check %s", name)
		}
	}
	for _, check := range r.Checks {
		if check.Name == "tts_resource_id_configured" && !check.Passed {
			t.Fatal("the runtime default TTS resource ID should count as configured")
		}
	}
}
