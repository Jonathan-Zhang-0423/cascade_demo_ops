package app

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/finalfilm"
	"cascade-demoops/backend/internal/llm"
	"cascade-demoops/backend/internal/model"
)

type finalFilmVisualQualityReviewer struct{ client llm.Client }

type finalFilmVisualCandidateDraft struct {
	Reviews []struct {
		IntentID    string   `json:"intent_id"`
		Attempt     int      `json:"attempt"`
		TextPass    bool     `json:"text_pass"`
		ContentPass bool     `json:"content_pass"`
		Score       float64  `json:"score"`
		Findings    []string `json:"findings"`
	} `json:"reviews"`
}

func newFinalFilmVisualQualityReviewer(client llm.Client) finalfilm.VisualQualityReviewer {
	return &finalFilmVisualQualityReviewer{client: client}
}

func (r *finalFilmVisualQualityReviewer) ReviewCandidates(ctx context.Context, inputs []finalfilm.VisualQualityReviewInput) ([]finalfilm.VisualQualityReviewResult, error) {
	if r == nil || r.client == nil || len(inputs) == 0 {
		return nil, errors.New("candidate visual reviewer requires inputs and a model")
	}
	images := make([]llm.ImageInput, 0, len(inputs))
	descriptions := make([]string, 0, len(inputs))
	for _, input := range inputs {
		image, err := localQualityImage(input.ContactSheetPath, input.IntentID)
		if err != nil {
			return nil, err
		}
		images = append(images, image)
		descriptions = append(descriptions, fmt.Sprintf("intent_id=%s attempt=%d purpose=%s image_label=%s", input.IntentID, input.Attempt, input.Purpose, input.IntentID))
	}
	var draft finalFilmVisualCandidateDraft
	_, err := r.client.GenerateMultimodal(ctx, config.ModelTaskMultimodalUnderstanding, llm.MultimodalRequest{
		System: "Review the supplied multi-frame contact sheets for decorative video shots. Reject any readable text, letters, numbers, HTML, JSON, UI controls, fake application UI, logos, garbled glyphs, full-screen flicker, abrupt flashes, or visible frame-to-frame shake. content_pass also requires a coherent decorative shot matching its supplied purpose. Return one result for every exact intent_id and attempt. Do not infer business facts.",
		User:   strings.Join(descriptions, "\n"), Images: images, SchemaName: "demoops.generated_candidate_visual_review.v1", MaxTokens: 2200, Temperature: 0,
	}, &draft)
	if err != nil {
		return nil, err
	}
	results := make([]finalfilm.VisualQualityReviewResult, 0, len(draft.Reviews))
	seen := map[string]bool{}
	expected := make(map[string]bool, len(inputs))
	for _, input := range inputs {
		expected[fmt.Sprintf("%s\x00%d", strings.TrimSpace(input.IntentID), input.Attempt)] = true
	}
	for _, review := range draft.Reviews {
		key := fmt.Sprintf("%s\x00%d", strings.TrimSpace(review.IntentID), review.Attempt)
		if seen[key] || !expected[key] || review.IntentID == "" || review.Attempt < 1 || review.Score < 0 || review.Score > 1 {
			return nil, errors.New("candidate visual review returned invalid or duplicate identity")
		}
		seen[key] = true
		results = append(results, finalfilm.VisualQualityReviewResult{IntentID: review.IntentID, Attempt: review.Attempt, TextPass: review.TextPass, ContentPass: review.ContentPass, Score: review.Score, Findings: append([]string{}, review.Findings...)})
	}
	if len(results) != len(inputs) {
		return nil, errors.New("candidate visual review omitted an input")
	}
	return results, nil
}

func (r *finalFilmVisualQualityReviewer) ReviewFinal(ctx context.Context, input finalfilm.FinalVisualReviewInput) (model.FinalVisualQualityReport, error) {
	if r == nil || r.client == nil {
		return model.FinalVisualQualityReport{}, errors.New("final visual reviewer is unavailable")
	}
	image, err := localQualityImage(input.ContactSheetPath, "final_sequence")
	if err != nil {
		return model.FinalVisualQualityReport{}, err
	}
	var draft struct {
		TemporalPass bool     `json:"temporal_pass"`
		TextPass     bool     `json:"text_pass"`
		ContentPass  bool     `json:"content_pass"`
		Findings     []string `json:"findings"`
	}
	_, err = r.client.GenerateMultimodal(ctx, config.ModelTaskMultimodalUnderstanding, llm.MultimodalRequest{
		System: "Review this ordered final-film contact sheet. temporal_pass requires stable factual UI with no editor-introduced shake or 1-3-frame full-screen flashes. text_pass rejects HTML tags, JSON/schema keys, selectors, execution logs, internal request fields, and garbled generated text. content_pass requires the factual sequence to cover opening the platform, login, new project creation, original one-sentence request, submission/waiting, result reveal, and real product interaction in that order; decorative shots must not imitate application UI. Return only the three booleans and concise findings.",
		User:   "Required chapters in order: " + strings.Join(input.RequiredChapters, ", ") + ". Approved captions only: " + strings.Join(input.ApprovedCaptions, " | "),
		Images: []llm.ImageInput{image}, SchemaName: "demoops.final_visual_quality_review.v1", MaxTokens: 1400, Temperature: 0,
	}, &draft)
	if err != nil {
		return model.FinalVisualQualityReport{}, err
	}
	return model.FinalVisualQualityReport{SchemaVersion: "demoops.final_visual_quality_report.v1", TemporalPass: draft.TemporalPass, TextPass: draft.TextPass, ContentPass: draft.ContentPass, Findings: append([]string{}, draft.Findings...), ContactSheetPath: input.ContactSheetPath, CheckedAt: time.Now().UTC()}, nil
}

func localQualityImage(pathValue, label string) (llm.ImageInput, error) {
	pathValue = filepath.Clean(strings.TrimSpace(pathValue))
	raw, err := os.ReadFile(pathValue)
	if err != nil {
		return llm.ImageInput{}, fmt.Errorf("read quality contact sheet: %w", err)
	}
	mime := "image/jpeg"
	if strings.EqualFold(filepath.Ext(pathValue), ".png") {
		mime = "image/png"
	}
	return llm.ImageInput{MimeType: mime, DataURI: "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(raw), Label: label}, nil
}
