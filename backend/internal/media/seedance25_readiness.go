package media

import (
	"os"
	"strconv"
	"strings"

	"cascade-demoops/backend/internal/config"
)

const Seedance25ReadinessSchemaVersion = "demoops.seedance_2_5_readiness.v1"

// Seedance25ReadinessReport is a non-network, no-secret preflight report for
// a future real candidate call. It is intentionally separate from the generic
// media readiness report so configuring another provider cannot accidentally
// represent Seedance 2.5 as callable.
type Seedance25ReadinessReport struct {
	SchemaVersion string                `json:"schema_version"`
	Model         string                `json:"model"`
	Checks        []MediaReadinessCheck `json:"checks"`
	Ready         bool                  `json:"ready"`
	NetworkCalled bool                  `json:"network_called"`
}

func Seedance25ReadinessFromEnv(getenv func(string) string, commandReady func(string) bool) Seedance25ReadinessReport {
	if getenv == nil {
		getenv = os.Getenv
	}
	if commandReady == nil {
		commandReady = localCommandReady
	}
	modelName := strings.TrimSpace(firstNonEmpty(getenv("SEEDANCE_MODEL"), getenv("CASCADE_VIDEO_MODEL")))
	report := Seedance25ReadinessReport{
		SchemaVersion: Seedance25ReadinessSchemaVersion, Model: modelName, NetworkCalled: false,
	}
	report.add("ark_media_mode_real", strings.TrimSpace(getenv("CASCADE_ARK_MEDIA_MODE")) == string(config.ArkMediaModeReal), "CASCADE_ARK_MEDIA_MODE must be real before any provider call")
	report.add("seedance_2_5_model_selected", modelName == Seedance25ServerModel, "SEEDANCE_MODEL must select Seedance 2.5")
	report.add("seedance_key_configured", anyConfigured(getenv, "SEEDANCE_API_KEY", "DOUBAO_API_KEY", "ARK_API_KEY"), "credential value is not recorded")
	report.add("seedance_base_url_configured", strings.TrimSpace(getenv("SEEDANCE_BASE_URL")) != "", "base URL is not recorded")
	report.add("ffmpeg_ready", commandReady(firstNonEmpty(getenv("CASCADE_FFMPEG_PATH"), "ffmpeg")), "executable path is not recorded")
	report.add("ffprobe_ready", commandReady(firstNonEmpty(getenv("CASCADE_FFPROBE_PATH"), "ffprobe")), "executable path is not recorded")

	for _, name := range []string{"VOLC_TOS_ACCESS_KEY", "VOLC_TOS_SECRET_KEY", "VOLC_TOS_ENDPOINT", "VOLC_TOS_REGION", "VOLC_TOS_BUCKET"} {
		report.add("tos_"+strings.ToLower(strings.TrimPrefix(name, "VOLC_TOS_"))+"_configured", strings.TrimSpace(getenv(name)) != "", "value is not recorded")
	}
	report.add("tos_region_cn_beijing", strings.EqualFold(strings.TrimSpace(getenv("VOLC_TOS_REGION")), "cn-beijing"), "Seedance 2.5 private-input route requires cn-beijing")
	report.add("tos_endpoint_ark_private", arkPrivateTOSPresignedURLHostConfigured(getenv("VOLC_TOS_ENDPOINT")), "endpoint must generate an ivolces.com private-TOS presigned URL")
	ttl, validTTL := seedance25TOSPresignTTL(getenv("VOLC_TOS_SIGNED_URL_TTL_SEC"))
	report.add("tos_signed_url_ttl_30m_to_24h", validTTL, "signed URL TTL is checked without recording its configured value")
	_ = ttl // retained for future structured diagnostics without exposing runtime configuration.
	policy, policyErr := Seedance25AdmissionPolicyFromEnv(getenv)
	report.add("seedance_2_5_admission_policy", policyErr == nil, seedance25AdmissionReadinessDetail(policy, policyErr))
	report.Ready = checksPassedExcept(report.Checks, "")
	return report
}

func seedance25AdmissionReadinessDetail(policy Seedance25AdmissionPolicy, err error) string {
	if err != nil {
		return "Server-owned Seedance 2.5 concurrency/RPM policy is invalid; no credential or request data is recorded"
	}
	return "Server-owned admission active: max_concurrent=" + strconv.Itoa(policy.MaxConcurrent) + ", create_task_rpm=" + strconv.Itoa(policy.MaxRequestsPerWindow) + ", idempotency_ttl_sec=" + strconv.FormatInt(int64(policy.IdempotencyTTL.Seconds()), 10)
}

func (r *Seedance25ReadinessReport) add(name string, passed bool, detail string) {
	r.Checks = append(r.Checks, MediaReadinessCheck{Name: name, Passed: passed, Detail: detail})
}

func arkPrivateTOSPresignedURLHostConfigured(endpoint string) bool {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return false
	}
	if !strings.Contains(endpoint, "://") {
		endpoint = "https://" + endpoint
	}
	return arkPrivateTOSPresignedURLAllowed(endpoint)
}

func seedance25TOSPresignTTL(raw string) (int64, bool) {
	if strings.TrimSpace(raw) == "" {
		return int64(defaultTOSSignedURLTTL.Seconds()), true
	}
	seconds, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return 0, false
	}
	return seconds, seconds >= 30*60 && seconds <= 24*60*60
}
