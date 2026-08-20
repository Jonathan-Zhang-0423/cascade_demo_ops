package media

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
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
	defaultOpenSpeechTTSPlanURL     = "https://openspeech.bytedance.com/api/v3/plan/tts/unidirectional"
	openSpeechTTSSubmitPath         = "/api/v3/tts/submit?Action=SubmitAsyncTtsTask&Version=2022-01-01"
	openSpeechTTSQueryPath          = "/api/v3/tts/query?Action=QueryAsyncTtsResult&Version=2022-01-01"
	openSpeechTTSSuccessCode        = 20000000
	defaultOpenSpeechTTSMaxDownload = int64(64 << 20)
)

type openSpeechTTSConfig struct {
	BaseURL         string
	AgentPlanURL    string
	AppID           string
	AccessKey       string
	AgentPlanAPIKey string
	ResourceID      string
	DefaultSpeaker  string
	OutputDir       string
	FFprobePath     string
	PollAttempts    int
	PollInterval    time.Duration
	MaxDownloadSize int64
}

type openSpeechTTSRequest struct {
	User      openSpeechTTSUser              `json:"user"`
	UniqueID  string                         `json:"unique_id"`
	ReqParams openSpeechTTSRequestParameters `json:"req_params"`
}

type openSpeechTTSUser struct {
	UID string `json:"uid"`
}

type openSpeechTTSRequestParameters struct {
	Text        string                   `json:"text"`
	Speaker     string                   `json:"speaker"`
	AudioParams openSpeechTTSAudioParams `json:"audio_params"`
	// OpenSpeech TTS v3 accepts additions as a JSON-encoded string. Sending a
	// JSON object yields provider code 55000000 (map-to-string conversion).
	Additions string `json:"additions,omitempty"`
}

type openSpeechTTSAudioParams struct {
	Format          string `json:"format"`
	SampleRate      int    `json:"sample_rate"`
	SpeechRate      int    `json:"speech_rate"`
	LoudnessRate    int    `json:"loudness_rate"`
	EnableTimestamp bool   `json:"enable_timestamp"`
	EnableSubtitle  bool   `json:"enable_subtitle"`
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
	BeginTime float64 `json:"begin_time,omitempty"`
	EndTime   float64 `json:"end_time,omitempty"`
	StartTime float64 `json:"startTime,omitempty"`
	LegacyEnd float64 `json:"endTime,omitempty"`
}

type audioProbeResult struct {
	DurationMS   int
	SampleRateHZ int
	Channels     int
	MimeType     string
}

func openSpeechTTSConfigFromEnv(runtime config.AppRuntimeConfig) openSpeechTTSConfig {
	return openSpeechTTSConfig{
		BaseURL:      envDefault("VOLC_TTS_BASE_URL", defaultOpenSpeechTTSBaseURL),
		AgentPlanURL: envDefault("VOLC_TTS_AGENT_PLAN_BASE_URL", defaultOpenSpeechTTSPlanURL),
		AppID:        strings.TrimSpace(os.Getenv("VOLC_TTS_APP_ID")),
		AccessKey:    strings.TrimSpace(os.Getenv("VOLC_TTS_ACCESS_KEY")),
		// The existing local setup uses VOLC_TTS_ACCESS_KEY for the Agent Plan
		// key. Prefer the explicit name, but accept that alias without ever
		// sending legacy X-Api-Access-Key headers.
		AgentPlanAPIKey: firstNonEmpty(os.Getenv("VOLC_TTS_AGENT_PLAN_API_KEY"), os.Getenv("VOLC_TTS_ACCESS_KEY")),
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
	return c.processDoubaoAgentPlanTTS(ctx, request, trace)
}

// processDoubaoAgentPlanTTS is the only real TTS route. The older task API
// remains below only to preserve historical fixtures; it is never a billing
// fallback because Agent Plan credentials and endpoints are distinct.
func (c *AudioClient) processDoubaoAgentPlanTTS(ctx context.Context, request AudioModelInput, trace ArkMediaCallTrace) (*AudioModelProviderResponse, ArkMediaCallTrace, error) {
	requestID, err := randomOpaqueID("tts")
	if err != nil {
		trace.ErrorClass = "request_id_failed"
		return nil, trace, err
	}
	additions, err := json.Marshal(map[string]int{"silence_duration": boundedOptionInt(request.Options, "silence_duration", 0, 0, 3000)})
	if err != nil {
		trace.ErrorClass = "request_marshal_failed"
		return nil, trace, err
	}
	body := openSpeechTTSRequest{
		User:     openSpeechTTSUser{UID: "server-" + requestID},
		UniqueID: requestID,
		ReqParams: openSpeechTTSRequestParameters{
			Text:    request.Text,
			Speaker: firstNonEmpty(request.VoiceID, c.tts.DefaultSpeaker),
			AudioParams: openSpeechTTSAudioParams{
				Format:         firstNonEmpty(request.OutputFormat, "mp3"),
				SampleRate:     positiveOrDefault(request.SampleRateHZ, 24000),
				SpeechRate:     speechRateForTTS(request.Speed),
				LoudnessRate:   boundedOptionInt(request.Options, "loudness_rate", 0, -50, 100),
				EnableSubtitle: true,
			},
			Additions: string(additions),
		},
	}
	trace = newTrace(c.mode, config.ModelProviderDoubao, c.tts.ResourceID, http.MethodPost, c.tts.AgentPlanURL)
	output, transcript, trace, err := c.doOpenSpeechAgentPlanChunked(ctx, requestID, body, trace)
	if err != nil {
		return nil, trace, err
	}
	output.Transcript = transcript
	return &AudioModelProviderResponse{Status: "succeeded", Output: output}, trace, nil
}

type openSpeechPlanChunk struct {
	Code     int    `json:"code"`
	Message  string `json:"message,omitempty"`
	Data     string `json:"data"`
	Sentence struct {
		Text  string `json:"text"`
		Words []struct {
			Word      string  `json:"word"`
			StartTime float64 `json:"startTime"`
			EndTime   float64 `json:"endTime"`
		} `json:"words"`
	} `json:"sentence"`
}

func (c *AudioClient) doOpenSpeechAgentPlanChunked(ctx context.Context, requestID string, payload openSpeechTTSRequest, trace ArkMediaCallTrace) (*AudioModelOutput, []AudioModelTranscriptSegment, ArkMediaCallTrace, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		trace.ErrorClass = "request_marshal_failed"
		return nil, nil, trace, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tts.AgentPlanURL, bytes.NewReader(encoded))
	if err != nil {
		trace.ErrorClass = "request_build_failed"
		return nil, nil, trace, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", c.tts.AgentPlanAPIKey)
	req.Header.Set("X-Api-Resource-Id", c.tts.ResourceID)
	req.Header.Set("X-Api-Request-Id", requestID)
	req.Header.Set("X-Control-Require-Usage-Tokens-Return", "text_words")
	started := time.Now()
	resp, err := c.http.Do(req)
	trace.LatencyMS = int(time.Since(started).Milliseconds())
	if err != nil {
		trace.ErrorClass = "http_error"
		return nil, nil, trace, redactStrings(err, c.tts.AgentPlanAPIKey)
	}
	defer resp.Body.Close()
	trace.HTTPStatus = resp.StatusCode
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		trace.ErrorClass = fmt.Sprintf("http_%d", resp.StatusCode)
		return nil, nil, trace, openSpeechTTSHTTPError(resp.StatusCode, data, c.tts.AgentPlanAPIKey)
	}
	var audio bytes.Buffer
	transcript := []AudioModelTranscriptSegment{}
	finished := false
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 4096), 2<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "data:") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		}
		if line == "" || strings.HasPrefix(line, "event:") {
			continue
		}
		var chunk openSpeechPlanChunk
		if err := json.Unmarshal([]byte(line), &chunk); err != nil {
			trace.ErrorClass = "response_parse_failed"
			return nil, nil, trace, errors.New("OpenSpeech Agent Plan TTS returned invalid stream JSON")
		}
		if chunk.Code != 0 && chunk.Code != openSpeechTTSSuccessCode {
			trace.ErrorClass = "provider_stream_failed"
			return nil, nil, trace, fmt.Errorf("OpenSpeech Agent Plan TTS provider code %d: %s", chunk.Code, sanitizedProviderMessage(chunk.Message))
		}
		if chunk.Data != "" {
			decoded, err := base64.StdEncoding.DecodeString(chunk.Data)
			if err != nil {
				trace.ErrorClass = "audio_decode_failed"
				return nil, nil, trace, errors.New("OpenSpeech Agent Plan TTS returned invalid base64 audio")
			}
			if int64(audio.Len()+len(decoded)) > c.tts.MaxDownloadSize {
				trace.ErrorClass = "candidate_too_large"
				return nil, nil, trace, errors.New("OpenSpeech Agent Plan TTS candidate exceeds size limit")
			}
			audio.Write(decoded)
		}
		if len(chunk.Sentence.Words) > 0 {
			start, end := subtitleBounds(chunk.Sentence.Words)
			if end > start {
				transcript = append(transcript, AudioModelTranscriptSegment{StartMS: start, EndMS: end, Text: chunk.Sentence.Text})
			}
		}
		if chunk.Code == openSpeechTTSSuccessCode {
			finished = true
		}
	}
	if err := scanner.Err(); err != nil {
		trace.ErrorClass = "response_read_failed"
		return nil, nil, trace, errors.New("OpenSpeech Agent Plan TTS stream could not be read")
	}
	if !finished || audio.Len() == 0 {
		trace.ErrorClass = "provider_response_incomplete"
		return nil, nil, trace, errors.New("OpenSpeech Agent Plan TTS stream completed without usable audio")
	}
	output, err := c.writeAndProbeTTSCandidate(ctx, audio.Bytes(), payload.ReqParams.AudioParams.Format, requestID)
	if err != nil {
		trace.ErrorClass = "candidate_probe_failed"
		return nil, nil, trace, err
	}
	return output, transcript, trace, nil
}

func subtitleBounds(words []struct {
	Word      string  `json:"word"`
	StartTime float64 `json:"startTime"`
	EndTime   float64 `json:"endTime"`
}) (int, int) {
	start := int(words[0].StartTime*1000 + 0.5)
	end := int(words[len(words)-1].EndTime*1000 + 0.5)
	return start, end
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
		return trace, openSpeechTTSHTTPError(resp.StatusCode, data, c.tts.AppID, c.tts.AccessKey)
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

func (c *AudioClient) writeAndProbeTTSCandidate(ctx context.Context, audio []byte, format, requestID string) (*AudioModelOutput, error) {
	if len(audio) == 0 || int64(len(audio)) > c.tts.MaxDownloadSize {
		return nil, errors.New("OpenSpeech Agent Plan TTS candidate is empty or oversized")
	}
	if err := os.MkdirAll(c.tts.OutputDir, 0o700); err != nil {
		return nil, err
	}
	ext := normalizedAudioExtension(format)
	temporaryPath := filepath.Join(c.tts.OutputDir, "."+requestID+ext+".partial")
	finalPath := filepath.Join(c.tts.OutputDir, requestID+ext)
	if err := os.WriteFile(temporaryPath, audio, 0o600); err != nil {
		return nil, err
	}
	probe, err := c.probe(ctx, c.tts.FFprobePath, temporaryPath)
	if err != nil {
		_ = os.Remove(temporaryPath)
		return nil, fmt.Errorf("OpenSpeech Agent Plan TTS candidate media validation failed: %w", err)
	}
	if err := os.Rename(temporaryPath, finalPath); err != nil {
		_ = os.Remove(temporaryPath)
		return nil, err
	}
	digest := sha256.Sum256(audio)
	return &AudioModelOutput{
		AssetRef:     "candidate://tts/" + hex.EncodeToString(digest[:]),
		LocalPath:    finalPath,
		MimeType:     probe.MimeType,
		DurationMS:   probe.DurationMS,
		SampleRateHZ: probe.SampleRateHZ,
		Channels:     probe.Channels,
		SHA256:       hex.EncodeToString(digest[:]),
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
	if strings.TrimSpace(value.AgentPlanAPIKey) == "" {
		return errors.New("VOLC_TTS_AGENT_PLAN_API_KEY is missing")
	}
	if strings.TrimSpace(value.ResourceID) == "" {
		return errors.New("VOLC_TTS_RESOURCE_ID is missing")
	}
	if firstNonEmpty(request.VoiceID, value.DefaultSpeaker) == "" {
		return errors.New("VOLC_TTS_SPEAKER or request voice_id is missing")
	}
	if parsed, err := url.Parse(value.AgentPlanURL); err != nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.Host == "" || parsed.Path != "/api/v3/plan/tts/unidirectional" {
		return errors.New("VOLC_TTS_AGENT_PLAN_BASE_URL must be the Agent Plan HTTPS unidirectional endpoint")
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
		startValue := value.BeginTime
		if startValue == 0 {
			startValue = value.StartTime
		}
		endValue := value.EndTime
		if endValue == 0 {
			endValue = value.LegacyEnd
		}
		start := int(startValue*1000 + 0.5)
		end := int(endValue*1000 + 0.5)
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
	if strings.Contains(message, "AGENT_PLAN") {
		return "tts_agent_plan_key_missing"
	}
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

// openSpeechTTSHTTPError preserves a short provider diagnostic for a blocked
// candidate run while removing locally configured credentials. It never
// records request payloads, generated IDs, task IDs, or download URLs.
func openSpeechTTSHTTPError(status int, body []byte, sensitive ...string) error {
	message := sanitizedProviderMessage(string(body))
	for _, value := range sensitive {
		if value != "" {
			message = strings.ReplaceAll(message, value, "[redacted]")
		}
	}
	if message == "" {
		return fmt.Errorf("OpenSpeech TTS HTTP %d", status)
	}
	return fmt.Errorf("OpenSpeech TTS HTTP %d: %s", status, message)
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
