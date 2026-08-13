package media

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"cascade-demoops/backend/internal/config"
)

const (
	defaultOpenSpeechTTSBaseURL     = "https://openspeech.bytedance.com"
	openSpeechTTSSubmitPath         = "/api/v3/tts/submit?Action=SubmitAsyncTtsTask&Version=2022-01-01"
	openSpeechTTSQueryPath          = "/api/v3/tts/query?Action=QueryAsyncTtsResult&Version=2022-01-01"
	openSpeechTTSSuccessCode        = 20000000
	defaultOpenSpeechTTSMaxDownload = int64(64 << 20)
)

type openSpeechTTSConfig struct {
	BaseURL         string
	AppID           string
	AccessKey       string
	ResourceID      string
	DefaultSpeaker  string
	OutputDir       string
	FFprobePath     string
	PollAttempts    int
	PollInterval    time.Duration
	MaxDownloadSize int64
}

type openSpeechTTSRequest struct {
	User struct {
		UID string `json:"uid"`
	} `json:"user"`
	UniqueID  string                         `json:"unique_id"`
	ReqParams openSpeechTTSRequestParameters `json:"req_params"`
}

type openSpeechTTSRequestParameters struct {
	Text        string                   `json:"text"`
	Speaker     string                   `json:"speaker"`
	AudioParams openSpeechTTSAudioParams `json:"audio_params"`
	Additions   map[string]any           `json:"additions,omitempty"`
}

type openSpeechTTSAudioParams struct {
	Format          string `json:"format"`
	SampleRate      int    `json:"sample_rate"`
	SpeechRate      int    `json:"speech_rate"`
	LoudnessRate    int    `json:"loudness_rate"`
	EnableTimestamp bool   `json:"enable_timestamp"`
}

type openSpeechTTSResponse struct {
	Code    int                     `json:"code"`
	Message string                  `json:"message,omitempty"`
	Data    openSpeechTTSResultData `json:"data"`
}

type openSpeechTTSResultData struct {
	TaskID       string                  `json:"task_id"`
	TaskStatus   int                     `json:"task_status"`
	AudioURL     string                  `json:"audio_url,omitempty"`
	Sentences    []openSpeechTTSSentence `json:"sentences,omitempty"`
	ErrorMessage string                  `json:"error_message,omitempty"`
}

type openSpeechTTSSentence struct {
	Text      string  `json:"text,omitempty"`
	StartTime float64 `json:"startTime,omitempty"`
	EndTime   float64 `json:"endTime,omitempty"`
}

type audioProbeResult struct {
	DurationMS   int
	SampleRateHZ int
	Channels     int
	MimeType     string
}

func openSpeechTTSConfigFromEnv(runtime config.AppRuntimeConfig) openSpeechTTSConfig {
	return openSpeechTTSConfig{
		BaseURL:         envDefault("VOLC_TTS_BASE_URL", defaultOpenSpeechTTSBaseURL),
		AppID:           strings.TrimSpace(os.Getenv("VOLC_TTS_APP_ID")),
		AccessKey:       strings.TrimSpace(os.Getenv("VOLC_TTS_ACCESS_KEY")),
		ResourceID:      envDefault("VOLC_TTS_RESOURCE_ID", "seed-tts-2.0"),
		DefaultSpeaker:  strings.TrimSpace(os.Getenv("VOLC_TTS_SPEAKER")),
		OutputDir:       envDefault("CASCADE_TTS_OUTPUT_DIR", filepath.Join(runtime.ArtifactRoot, "audio-model", "tts-candidates")),
		FFprobePath:     firstNonEmpty(runtime.FFprobePath, os.Getenv("CASCADE_FFPROBE_PATH"), "ffprobe"),
		PollAttempts:    boundedEnvInt("CASCADE_TTS_POLL_ATTEMPTS", 20, 1, 120),
		PollInterval:    time.Duration(boundedEnvInt("CASCADE_TTS_POLL_INTERVAL_MS", 3000, 0, 30000)) * time.Millisecond,
		MaxDownloadSize: int64(boundedEnvInt("CASCADE_TTS_DOWNLOAD_MAX_BYTES", int(defaultOpenSpeechTTSMaxDownload), 1024, int(defaultOpenSpeechTTSMaxDownload))),
	}
}

func (c *AudioClient) processDoubaoTTS(ctx context.Context, request AudioModelInput, trace ArkMediaCallTrace) (*AudioModelProviderResponse, ArkMediaCallTrace, error) {
	if err := validateOpenSpeechTTSConfig(c.tts, request); err != nil {
		trace.ErrorClass = classifyTTSConfigError(err)
		return nil, trace, err
	}
	requestID, err := randomOpaqueID("tts")
	if err != nil {
		trace.ErrorClass = "request_id_failed"
		return nil, trace, err
	}
	body := openSpeechTTSRequest{UniqueID: requestID}
	body.User.UID = "cascade-server"
	body.ReqParams = openSpeechTTSRequestParameters{
		Text:    request.Text,
		Speaker: firstNonEmpty(request.VoiceID, c.tts.DefaultSpeaker),
		AudioParams: openSpeechTTSAudioParams{
			Format:          firstNonEmpty(request.OutputFormat, "mp3"),
			SampleRate:      positiveOrDefault(request.SampleRateHZ, 24000),
			SpeechRate:      speechRateForTTS(request.Speed),
			LoudnessRate:    boundedOptionInt(request.Options, "loudness_rate", 0, -50, 100),
			EnableTimestamp: true,
		},
		Additions: map[string]any{"silence_duration": boundedOptionInt(request.Options, "silence_duration", 300, 0, 3000)},
	}

	submitEndpoint := openSpeechEndpoint(c.tts.BaseURL, openSpeechTTSSubmitPath)
	trace = newTrace(c.mode, config.ModelProviderDoubao, c.tts.ResourceID, http.MethodPost, submitEndpoint)
	var submit openSpeechTTSResponse
	trace, err = c.doOpenSpeechTTSJSON(ctx, submitEndpoint, requestID, body, &submit, trace)
	if err != nil {
		return nil, trace, err
	}
	if err := validateOpenSpeechTTSResponse(submit); err != nil {
		trace.ErrorClass = "provider_submit_failed"
		return nil, trace, err
	}
	if strings.TrimSpace(submit.Data.TaskID) == "" {
		trace.ErrorClass = "provider_response_invalid"
		return nil, trace, errors.New("OpenSpeech TTS submit response is missing task_id")
	}

	providerResponse := &AudioModelProviderResponse{TaskID: submit.Data.TaskID, Status: ttsTaskStatus(submit.Data.TaskStatus)}
	current := submit
	for attempt := 0; attempt < c.tts.PollAttempts && current.Data.TaskStatus == 1; attempt++ {
		if attempt > 0 || c.tts.PollInterval > 0 {
			if err := c.sleep(ctx, c.tts.PollInterval); err != nil {
				trace.ErrorClass = "poll_cancelled"
				return providerResponse, trace, err
			}
		}
		queryEndpoint := openSpeechEndpoint(c.tts.BaseURL, openSpeechTTSQueryPath)
		queryTrace := newTrace(c.mode, config.ModelProviderDoubao, c.tts.ResourceID, http.MethodPost, queryEndpoint)
		var query openSpeechTTSResponse
		queryTrace, err = c.doOpenSpeechTTSJSON(ctx, queryEndpoint, requestID, map[string]string{"task_id": submit.Data.TaskID}, &query, queryTrace)
		trace = queryTrace
		if err != nil {
			return providerResponse, trace, err
		}
		if err := validateOpenSpeechTTSResponse(query); err != nil {
			trace.ErrorClass = "provider_query_failed"
			return providerResponse, trace, err
		}
		current = query
		providerResponse.Status = ttsTaskStatus(current.Data.TaskStatus)
	}
	if current.Data.TaskStatus == 1 {
		trace.ErrorClass = "poll_exhausted"
		return providerResponse, trace, errors.New("OpenSpeech TTS task did not complete within the polling limit")
	}
	if current.Data.TaskStatus != 2 {
		trace.ErrorClass = "provider_task_failed"
		providerResponse.Error = &ProviderError{Code: "tts_task_failed", Message: sanitizedProviderMessage(current.Data.ErrorMessage)}
		return providerResponse, trace, errors.New("OpenSpeech TTS task failed")
	}
	if strings.TrimSpace(current.Data.AudioURL) == "" {
		trace.ErrorClass = "provider_response_invalid"
		return providerResponse, trace, errors.New("OpenSpeech TTS completed response is missing audio_url")
	}

	output, err := c.downloadAndProbeTTSCandidate(ctx, current.Data.AudioURL, body.ReqParams.AudioParams.Format, requestID)
	if err != nil {
		trace.ErrorClass = "candidate_download_or_probe_failed"
		return providerResponse, trace, err
	}
	output.Transcript = transcriptFromTTSSentences(current.Data.Sentences)
	providerResponse.Status = "succeeded"
	providerResponse.Output = output
	return providerResponse, trace, nil
}

func (c *AudioClient) doOpenSpeechTTSJSON(ctx context.Context, endpoint, requestID string, payload, target any, trace ArkMediaCallTrace) (ArkMediaCallTrace, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		trace.ErrorClass = "request_marshal_failed"
		return trace, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		trace.ErrorClass = "request_build_failed"
		return trace, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-App-Id", c.tts.AppID)
	req.Header.Set("X-Api-Access-Key", c.tts.AccessKey)
	req.Header.Set("X-Api-Resource-Id", c.tts.ResourceID)
	req.Header.Set("X-Api-Request-Id", requestID)
	started := time.Now()
	resp, err := c.http.Do(req)
	trace.LatencyMS = int(time.Since(started).Milliseconds())
	if err != nil {
		trace.ErrorClass = "http_error"
		return trace, redactStrings(err, c.tts.AppID, c.tts.AccessKey)
	}
	defer resp.Body.Close()
	trace.HTTPStatus = resp.StatusCode
	data, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		trace.ErrorClass = "response_read_failed"
		return trace, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		trace.ErrorClass = fmt.Sprintf("http_%d", resp.StatusCode)
		return trace, fmt.Errorf("OpenSpeech TTS HTTP %d", resp.StatusCode)
	}
	if err := json.Unmarshal(data, target); err != nil {
		trace.ErrorClass = "response_parse_failed"
		return trace, errors.New("OpenSpeech TTS returned invalid JSON")
	}
	return trace, nil
}

func (c *AudioClient) downloadAndProbeTTSCandidate(ctx context.Context, rawURL, format, requestID string) (*AudioModelOutput, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.Host == "" {
		return nil, errors.New("OpenSpeech TTS audio_url must be an absolute HTTPS URL")
	}
	client := *c.http
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 || !strings.EqualFold(req.URL.Scheme, "https") {
			return errors.New("OpenSpeech TTS download redirect denied")
		}
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, errors.New("OpenSpeech TTS download request could not be created")
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.New("OpenSpeech TTS candidate download failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OpenSpeech TTS candidate download HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > c.tts.MaxDownloadSize {
		return nil, errors.New("OpenSpeech TTS candidate exceeds download size limit")
	}
	if err := os.MkdirAll(c.tts.OutputDir, 0o700); err != nil {
		return nil, err
	}
	ext := normalizedAudioExtension(format)
	temporaryPath := filepath.Join(c.tts.OutputDir, "."+requestID+ext+".partial")
	finalPath := filepath.Join(c.tts.OutputDir, requestID+ext)
	file, err := os.OpenFile(temporaryPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, err
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(resp.Body, c.tts.MaxDownloadSize+1))
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil || written > c.tts.MaxDownloadSize {
		_ = os.Remove(temporaryPath)
		return nil, errors.New("OpenSpeech TTS candidate download was incomplete or oversized")
	}
	probe, err := c.probe(ctx, c.tts.FFprobePath, temporaryPath)
	if err != nil {
		_ = os.Remove(temporaryPath)
		return nil, fmt.Errorf("OpenSpeech TTS candidate media validation failed: %w", err)
	}
	if err := os.Rename(temporaryPath, finalPath); err != nil {
		_ = os.Remove(temporaryPath)
		return nil, err
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	return &AudioModelOutput{
		AssetRef:     "candidate://tts/" + digest,
		LocalPath:    finalPath,
		MimeType:     probe.MimeType,
		DurationMS:   probe.DurationMS,
		SampleRateHZ: probe.SampleRateHZ,
		Channels:     probe.Channels,
		SHA256:       digest,
	}, nil
}

func probeAudioCandidate(ctx context.Context, ffprobePath, candidatePath string) (audioProbeResult, error) {
	command := exec.CommandContext(ctx, ffprobePath, "-v", "error", "-select_streams", "a:0", "-show_entries", "stream=codec_type,sample_rate,channels:format=duration,format_name", "-of", "json", candidatePath)
	data, err := command.Output()
	if err != nil {
		return audioProbeResult{}, errors.New("ffprobe could not validate the candidate")
	}
	var result struct {
		Streams []struct {
			CodecType  string `json:"codec_type"`
			SampleRate string `json:"sample_rate"`
			Channels   int    `json:"channels"`
		} `json:"streams"`
		Format struct {
			Duration   string `json:"duration"`
			FormatName string `json:"format_name"`
		} `json:"format"`
	}
	if err := json.Unmarshal(data, &result); err != nil || len(result.Streams) == 0 || result.Streams[0].CodecType != "audio" {
		return audioProbeResult{}, errors.New("ffprobe found no audio stream")
	}
	duration, _ := strconv.ParseFloat(result.Format.Duration, 64)
	sampleRate, _ := strconv.Atoi(result.Streams[0].SampleRate)
	if duration <= 0 || sampleRate <= 0 || result.Streams[0].Channels <= 0 {
		return audioProbeResult{}, errors.New("ffprobe returned invalid audio metadata")
	}
	return audioProbeResult{DurationMS: int(duration*1000 + 0.5), SampleRateHZ: sampleRate, Channels: result.Streams[0].Channels, MimeType: audioMimeType(result.Format.FormatName)}, nil
}

func validateOpenSpeechTTSConfig(value openSpeechTTSConfig, request AudioModelInput) error {
	if strings.TrimSpace(value.AppID) == "" {
		return errors.New("VOLC_TTS_APP_ID is missing")
	}
	if strings.TrimSpace(value.AccessKey) == "" {
		return errors.New("VOLC_TTS_ACCESS_KEY is missing")
	}
	if strings.TrimSpace(value.ResourceID) == "" {
		return errors.New("VOLC_TTS_RESOURCE_ID is missing")
	}
	if firstNonEmpty(request.VoiceID, value.DefaultSpeaker) == "" {
		return errors.New("VOLC_TTS_SPEAKER or request voice_id is missing")
	}
	if parsed, err := url.Parse(value.BaseURL); err != nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.Host == "" {
		return errors.New("VOLC_TTS_BASE_URL must be absolute HTTPS")
	}
	if request.Pitch != 0 {
		return errors.New("OpenSpeech TTS v1 does not support pitch; omit it")
	}
	return nil
}

func validateOpenSpeechTTSResponse(response openSpeechTTSResponse) error {
	if response.Code != openSpeechTTSSuccessCode {
		return fmt.Errorf("OpenSpeech TTS provider code %d: %s", response.Code, sanitizedProviderMessage(response.Message))
	}
	return nil
}

func transcriptFromTTSSentences(values []openSpeechTTSSentence) []AudioModelTranscriptSegment {
	result := make([]AudioModelTranscriptSegment, 0, len(values))
	for _, value := range values {
		start := int(value.StartTime*1000 + 0.5)
		end := int(value.EndTime*1000 + 0.5)
		if end <= start {
			continue
		}
		result = append(result, AudioModelTranscriptSegment{StartMS: start, EndMS: end, Text: value.Text})
	}
	return result
}

func ttsTaskStatus(value int) string {
	switch value {
	case 1:
		return "queued"
	case 2:
		return "succeeded"
	case 3:
		return "failed"
	default:
		return "unknown"
	}
}

func sleepWithContext(ctx context.Context, duration time.Duration) error {
	if duration <= 0 {
		return nil
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func randomOpaqueID(prefix string) (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return prefix + "_" + hex.EncodeToString(value), nil
}

func openSpeechEndpoint(baseURL, pathAndQuery string) string {
	return strings.TrimRight(baseURL, "/") + pathAndQuery
}
func envDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
func positiveOrDefault(value, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}
func normalizedAudioExtension(format string) string {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "wav":
		return ".wav"
	case "ogg":
		return ".ogg"
	default:
		return ".mp3"
	}
}
func audioMimeType(formatName string) string {
	value := strings.ToLower(formatName)
	if strings.Contains(value, "wav") {
		return "audio/wav"
	}
	if strings.Contains(value, "ogg") {
		return "audio/ogg"
	}
	return "audio/mpeg"
}
func speechRateForTTS(speed float64) int {
	if speed == 0 {
		return 0
	}
	value := int((speed-1)*100 + 0.5)
	if value < -50 {
		return -50
	}
	if value > 100 {
		return 100
	}
	return value
}
func boundedOptionInt(options map[string]any, name string, fallback, minimum, maximum int) int {
	value, ok := options[name]
	if !ok {
		return fallback
	}
	number, ok := value.(float64)
	if !ok {
		return fallback
	}
	result := int(number)
	if result < minimum {
		return minimum
	}
	if result > maximum {
		return maximum
	}
	return result
}
func boundedEnvInt(name string, fallback, minimum, maximum int) int {
	value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name)))
	if err != nil {
		return fallback
	}
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}
func classifyTTSConfigError(err error) string {
	message := err.Error()
	if strings.Contains(message, "APP_ID") {
		return "tts_app_id_missing"
	}
	if strings.Contains(message, "ACCESS_KEY") {
		return "tts_access_key_missing"
	}
	if strings.Contains(message, "RESOURCE_ID") {
		return "tts_resource_id_missing"
	}
	if strings.Contains(message, "SPEAKER") {
		return "tts_speaker_missing"
	}
	return "tts_config_error"
}
func sanitizedProviderMessage(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	if len(value) > 200 {
		value = value[:200]
	}
	return value
}
func redactStrings(err error, values ...string) error {
	if err == nil {
		return nil
	}
	message := err.Error()
	for _, value := range values {
		if value != "" {
			message = strings.ReplaceAll(message, value, "[redacted]")
		}
	}
	return errors.New(message)
}
