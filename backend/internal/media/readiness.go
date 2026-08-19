package media

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"cascade-demoops/backend/internal/config"
)

// MediaReadinessCheck is a non-network configuration check. Detail is safe to
// expose in diagnostics and must never contain credential values.
type MediaReadinessCheck struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Detail string `json:"detail,omitempty"`
}

type MediaReadinessReport struct {
	SchemaVersion string                `json:"schema_version"`
	Mode          string                `json:"ark_media_mode"`
	Provider      string                `json:"video_provider"`
	Model         string                `json:"video_model"`
	Checks        []MediaReadinessCheck `json:"checks"`
	VideoReady    bool                  `json:"video_ready"`
	TTSReady      bool                  `json:"tts_ready"`
	Ready         bool                  `json:"ready"` // video path readiness; TTS is optional narration
	NetworkCalled bool                  `json:"network_called"`
}

// MediaReadinessFromEnv reports whether the local Server has the prerequisites
// for a real media run. It intentionally does not contact any provider.
func MediaReadinessFromEnv(getenv func(string) string, commandReady func(string) bool) MediaReadinessReport {
	if getenv == nil {
		getenv = os.Getenv
	}
	if commandReady == nil {
		commandReady = localCommandReady
	}
	r := MediaReadinessReport{
		SchemaVersion: "cascade.media_readiness.v1",
		Mode:          strings.TrimSpace(getenv("CASCADE_ARK_MEDIA_MODE")),
		Provider:      strings.TrimSpace(getenv("CASCADE_VIDEO_PROVIDER")),
		Model:         strings.TrimSpace(getenv("CASCADE_VIDEO_MODEL")),
		Checks:        []MediaReadinessCheck{},
		NetworkCalled: false,
	}
	r.add("ffmpeg_ready", commandReady(firstNonEmpty(getenv("CASCADE_FFMPEG_PATH"), "ffmpeg")), "executable path is not recorded")
	r.add("ffprobe_ready", commandReady(firstNonEmpty(getenv("CASCADE_FFPROBE_PATH"), "ffprobe")), "executable path is not recorded")
	r.add("ark_media_mode_real", r.Mode == string(config.ArkMediaModeReal), "CASCADE_ARK_MEDIA_MODE must be real before provider calls")
	r.add("seedance_key_configured", anyConfigured(getenv, "SEEDANCE_API_KEY", "DOUBAO_API_KEY", "ARK_API_KEY"), "credential value is not recorded")
	r.add("seedance_base_url_configured", strings.TrimSpace(getenv("SEEDANCE_BASE_URL")) != "", "base URL is not recorded")
	r.add("seedance_model_configured", strings.TrimSpace(firstNonEmpty(getenv("CASCADE_VIDEO_MODEL"), getenv("SEEDANCE_MODEL"))) != "", "model name is not recorded")
	tos := []string{"VOLC_TOS_ACCESS_KEY", "VOLC_TOS_SECRET_KEY", "VOLC_TOS_ENDPOINT", "VOLC_TOS_REGION", "VOLC_TOS_BUCKET"}
	for _, name := range tos {
		r.add("tos_"+strings.ToLower(strings.TrimPrefix(name, "VOLC_TOS_"))+"_configured", strings.TrimSpace(getenv(name)) != "", "value is not recorded")
	}
	for _, name := range []string{"VOLC_TTS_APP_ID", "VOLC_TTS_ACCESS_KEY", "VOLC_TTS_RESOURCE_ID", "VOLC_TTS_SPEAKER"} {
		value := strings.TrimSpace(getenv(name))
		if name == "VOLC_TTS_RESOURCE_ID" {
			value = firstNonEmpty(value, "seed-tts-2.0")
		}
		r.add("tts_"+strings.ToLower(strings.TrimPrefix(name, "VOLC_TTS_"))+"_configured", value != "", "value is not recorded")
	}
	r.VideoReady = checksPassedExcept(r.Checks, "tts_")
	r.TTSReady = checksPassedWithPrefix(r.Checks, "tts_")
	r.Ready = r.VideoReady
	return r
}

func checksPassedExcept(checks []MediaReadinessCheck, excludedPrefix string) bool {
	for _, check := range checks {
		if excludedPrefix != "" && strings.HasPrefix(check.Name, excludedPrefix) {
			continue
		}
		if !check.Passed {
			return false
		}
	}
	return true
}

func checksPassedWithPrefix(checks []MediaReadinessCheck, prefix string) bool {
	found := false
	for _, check := range checks {
		if !strings.HasPrefix(check.Name, prefix) {
			continue
		}
		found = true
		if !check.Passed {
			return false
		}
	}
	return found
}

func (r *MediaReadinessReport) add(name string, passed bool, detail string) {
	r.Checks = append(r.Checks, MediaReadinessCheck{Name: name, Passed: passed, Detail: detail})
}

func anyConfigured(getenv func(string) string, names ...string) bool {
	for _, name := range names {
		if strings.TrimSpace(getenv(name)) != "" {
			return true
		}
	}
	return false
}

func localCommandReady(name string) bool {
	if filepath.IsAbs(name) || strings.ContainsAny(name, `/\\`) {
		info, err := os.Stat(name)
		return err == nil && info.Mode().IsRegular()
	}
	_, err := exec.LookPath(name)
	return err == nil
}
