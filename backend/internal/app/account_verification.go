package app

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/model"
)

const (
	verificationLifetime         = 10 * time.Minute
	verificationCooldown         = 60 * time.Second
	verificationMaxAttempts      = 5
	verificationHourlySendLimit  = 5
	verificationPurposeContact   = "contact"
	verificationPurposeReset     = "password_reset"
	approvedVerificationSender   = "Cascade AI <noreply@cascadeai.co>"
	approvedVerificationSubject  = "Cascade AI Verification"
	approvedPasswordResetSubject = "Cascade AI Password Reset"
)

type accountVerificationError struct {
	code      string
	message   string
	retryable bool
}

func (e *accountVerificationError) Error() string { return e.message }

func verificationError(code, message string, retryable bool) error {
	return &accountVerificationError{code: code, message: message, retryable: retryable}
}

func (s *accountService) startVerification(ctx context.Context, channel, destination string) (model.AccountVerificationStart, error) {
	state, err := s.authenticatedState(ctx)
	if err != nil {
		return model.AccountVerificationStart{}, err
	}
	channel = strings.ToLower(strings.TrimSpace(channel))
	destination = normalizeVerificationDestination(channel, destination)
	if err := validateVerificationDestination(channel, destination); err != nil {
		return model.AccountVerificationStart{}, verificationError("verification_destination_invalid", err.Error(), false)
	}
	return s.createVerificationChallenge(ctx, state, verificationPurposeContact, channel, destination, false)
}

func (s *accountService) confirmVerification(ctx context.Context, channel, code string) (model.AccountProfile, error) {
	state, err := s.authenticatedState(ctx)
	if err != nil {
		return model.AccountProfile{}, err
	}
	channel = strings.ToLower(strings.TrimSpace(channel))
	code = strings.TrimSpace(code)
	challenge, err := s.verifyChallenge(ctx, state, verificationPurposeContact, channel, code)
	if err != nil {
		return model.AccountProfile{}, err
	}
	state.Profile.Email = challenge.Destination
	setVerificationStatus(&state.Profile, channel, model.AccountVerificationVerified)
	state.Profile.UpdatedAt = s.now()
	if err := s.store.Save(ctx, state); err != nil {
		return model.AccountProfile{}, err
	}
	return s.publicProfile(state), nil
}

func verificationChallengeKey(purpose, channel string) string {
	return purpose + ":" + channel
}

func (s *accountService) verificationCodeHash(userID, purpose, channel, destination, code string) string {
	secret := strings.TrimSpace(s.runtime.AccountVerificationSecret)
	if secret == "" {
		secret = "cascade-local-development-verification"
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(userID + "\x00" + purpose + "\x00" + channel + "\x00" + destination + "\x00" + code))
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *accountService) verificationSendKey(purpose, channel, destination string) string {
	return s.verificationCodeHash("delivery-limit", purpose, channel, strings.ToLower(destination), "")
}

func (s *accountService) createVerificationChallenge(ctx context.Context, state *model.AccountState, purpose, channel, destination string, suppressDeliveryErrors bool) (model.AccountVerificationStart, error) {
	now := s.now()
	key := verificationChallengeKey(purpose, channel)
	if current, ok := state.VerificationChallenges[key]; ok && now.Before(current.ResendAt) && strings.EqualFold(current.Destination, destination) {
		if suppressDeliveryErrors {
			return genericVerificationStart(channel, destination, now), nil
		}
		return model.AccountVerificationStart{}, verificationError("verification_rate_limited", fmt.Sprintf("wait %d seconds before requesting another code", max(1, int(current.ResendAt.Sub(now).Seconds()))), true)
	}
	sendKey := s.verificationSendKey(purpose, channel, destination)
	recent := state.VerificationSends[sendKey][:0]
	for _, sentAt := range state.VerificationSends[sendKey] {
		if now.Sub(sentAt) < time.Hour {
			recent = append(recent, sentAt)
		}
	}
	state.VerificationSends[sendKey] = recent
	if len(recent) >= verificationHourlySendLimit {
		if suppressDeliveryErrors {
			return genericVerificationStart(channel, destination, now), nil
		}
		return model.AccountVerificationStart{}, verificationError("verification_hourly_limit", "too many verification messages were requested; try again later", true)
	}
	code, err := s.generateVerificationCode()
	if err != nil {
		return model.AccountVerificationStart{}, verificationError("verification_generation_failed", "verification code could not be generated", true)
	}
	challenge := model.AccountVerificationChallenge{
		UserID: state.Profile.ID, Purpose: purpose, Channel: channel, Destination: destination,
		CodeHash:  s.verificationCodeHash(state.Profile.ID, purpose, channel, destination, code),
		CreatedAt: now, ExpiresAt: now.Add(verificationLifetime), ResendAt: now.Add(verificationCooldown),
	}
	state.VerificationChallenges[key] = challenge
	state.VerificationSends[sendKey] = append(recent, now)
	if err := s.store.Save(ctx, state); err != nil {
		return model.AccountVerificationStart{}, err
	}
	if err := s.deliverVerification(ctx, purpose, channel, destination, code); err != nil {
		delete(state.VerificationChallenges, key)
		state.VerificationSends[sendKey] = recent
		_ = s.store.Save(ctx, state)
		if suppressDeliveryErrors {
			return genericVerificationStart(channel, destination, now), nil
		}
		return model.AccountVerificationStart{}, err
	}
	result := model.AccountVerificationStart{Channel: channel, MaskedDestination: maskVerificationDestination(channel, destination), Status: "pending", ExpiresAt: challenge.ExpiresAt, ResendAt: challenge.ResendAt}
	if s.isDevelopmentRuntime() && !s.verificationProviderConfiguredForPurpose(channel, purpose) {
		result.DevelopmentCode = code
	}
	return result, nil
}

func genericVerificationStart(channel, destination string, now time.Time) model.AccountVerificationStart {
	return model.AccountVerificationStart{Channel: channel, MaskedDestination: maskVerificationDestination(channel, destination), Status: "pending", ExpiresAt: now.Add(verificationLifetime), ResendAt: now.Add(verificationCooldown)}
}

func (s *accountService) verifyChallenge(ctx context.Context, state *model.AccountState, purpose, channel, code string) (model.AccountVerificationChallenge, error) {
	key := verificationChallengeKey(purpose, channel)
	challenge, ok := state.VerificationChallenges[key]
	if !ok || challenge.UserID != state.Profile.ID || challenge.Purpose != purpose || !s.now().Before(challenge.ExpiresAt) {
		delete(state.VerificationChallenges, key)
		_ = s.store.Save(ctx, state)
		return model.AccountVerificationChallenge{}, verificationError("verification_expired", "verification code is missing or expired", false)
	}
	if challenge.Attempts >= verificationMaxAttempts {
		return model.AccountVerificationChallenge{}, verificationError("verification_attempts_exhausted", "too many incorrect verification attempts; request a new code", false)
	}
	if len(code) != 6 {
		return model.AccountVerificationChallenge{}, verificationError("verification_invalid", "enter the six-digit verification code", false)
	}
	expected, decodeErr := hex.DecodeString(challenge.CodeHash)
	actual, actualErr := hex.DecodeString(s.verificationCodeHash(state.Profile.ID, purpose, channel, challenge.Destination, strings.TrimSpace(code)))
	if decodeErr != nil || actualErr != nil || len(expected) != len(actual) || subtle.ConstantTimeCompare(expected, actual) != 1 {
		challenge.Attempts++
		state.VerificationChallenges[key] = challenge
		if challenge.Attempts >= verificationMaxAttempts {
			delete(state.VerificationChallenges, key)
		}
		_ = s.store.Save(ctx, state)
		if challenge.Attempts >= verificationMaxAttempts {
			return model.AccountVerificationChallenge{}, verificationError("verification_attempts_exhausted", "too many incorrect verification attempts; request a new code", false)
		}
		return model.AccountVerificationChallenge{}, verificationError("verification_invalid", "verification code is incorrect", false)
	}
	delete(state.VerificationChallenges, key)
	return challenge, nil
}

func (s *accountService) deliverVerificationCode(ctx context.Context, purpose, channel, destination, code string) error {
	if channel != "email" {
		return verificationError("verification_channel_unsupported", "only email verification is supported", false)
	}
	configured := strings.TrimSpace(s.runtime.ResendAPIKey) != "" || strings.TrimSpace(s.runtime.VerificationEmailFrom) != ""
	if !configured && s.isDevelopmentRuntime() {
		return nil
	}
	if strings.TrimSpace(s.runtime.ResendAPIKey) == "" {
		return verificationError("verification_not_configured", "Resend API key is not configured", false)
	}
	if strings.TrimSpace(s.runtime.VerificationEmailFrom) != approvedVerificationSender {
		return verificationError("verification_sender_invalid", "verification sender must be Cascade AI <noreply@cascadeai.co>", false)
	}
	if len(strings.TrimSpace(s.runtime.AccountVerificationSecret)) < 32 {
		return verificationError("verification_not_configured", "account verification secret must contain at least 32 characters", false)
	}
	return s.sendPurposeVerificationEmail(ctx, purpose, destination, code)
}

func (s *accountService) sendVerificationEmail(ctx context.Context, destination, code string) error {
	return s.sendPurposeVerificationEmail(ctx, verificationPurposeContact, destination, code)
}

func (s *accountService) sendPurposeVerificationEmail(ctx context.Context, purpose, destination, code string) error {
	subject := approvedVerificationSubject
	heading := "Verify your email"
	body := "Use the code below to verify your email address and continue in Cascade."
	tag := "account_verification"
	if purpose == verificationPurposeReset {
		subject = approvedPasswordResetSubject
		heading = "Reset your password"
		body = "Use the code below to reset your Cascade password."
		tag = "password_reset"
	}
	plainText := fmt.Sprintf("Cascade AI\n\n%s\n\n%s\n\n%s\n\nThis code expires in 10 minutes.\n\nIf you didn’t request this, you can safely ignore this email.\n\n— The Cascade AI team", heading, body, code)
	html := fmt.Sprintf(`<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"></head><body style="margin:0;background:#f4f4f2;color:#171719;font-family:Arial,sans-serif"><div style="display:none;max-height:0;overflow:hidden;opacity:0">Your Cascade verification code expires in 10 minutes.</div><div style="max-width:520px;margin:40px auto;padding:32px;background:#fff;border:1px solid #e5e5e1;border-radius:16px"><p style="margin:0 0 20px;font-size:13px;color:#626268">Cascade AI</p><h1 style="margin:0 0 12px;font-size:24px">%s</h1><p style="margin:0 0 24px;color:#5d5d62;line-height:1.6">%s</p><div style="padding:18px;text-align:center;border-radius:12px;background:#f0f7f5;font-family:monospace;font-size:30px;letter-spacing:8px">%s</div><p style="margin:24px 0 0;color:#5d5d62;font-size:13px;line-height:1.5">This code expires in 10 minutes.</p><p style="margin:14px 0 0;color:#85858b;font-size:12px;line-height:1.5">If you didn’t request this, you can safely ignore this email.</p><p style="margin:20px 0 0;color:#626268;font-size:12px">— The Cascade AI team</p></div></body></html>`, heading, body, code)
	payload, _ := json.Marshal(map[string]any{
		"from":    approvedVerificationSender,
		"to":      []string{destination},
		"subject": subject,
		"text":    plainText,
		"html":    html,
		"tags":    []map[string]string{{"name": "category", "value": tag}},
	})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.resend.com/emails", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+s.runtime.ResendAPIKey)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "cascade-demoops/1.0")
	hash := s.verificationCodeHash("delivery", purpose, "email", destination, code)
	prefix := "cascade-account-verification/"
	if purpose == verificationPurposeReset {
		prefix = "cascade-password-reset/"
	}
	request.Header.Set("Idempotency-Key", prefix+hash[:32])
	return s.performResendRequest(request)
}

func (s *accountService) performResendRequest(request *http.Request) error {
	for attempt := 0; attempt < 2; attempt++ {
		currentRequest := request
		if attempt > 0 {
			currentRequest = request.Clone(request.Context())
			if request.GetBody != nil {
				body, err := request.GetBody()
				if err != nil {
					return verificationError("verification_delivery_failed", "email verification request could not be retried", true)
				}
				currentRequest.Body = body
			}
		}
		response, err := s.client.Do(currentRequest)
		if err != nil {
			if attempt == 0 {
				continue
			}
			return verificationError("verification_delivery_failed", "email verification could not be sent; try again", true)
		}
		body, _ := io.ReadAll(io.LimitReader(response.Body, 16*1024))
		_ = response.Body.Close()
		if response.StatusCode >= 500 && attempt == 0 {
			continue
		}
		if response.StatusCode >= 200 && response.StatusCode < 300 {
			var sent struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(body, &sent); err != nil || strings.TrimSpace(sent.ID) == "" {
				return verificationError("verification_delivery_failed", "Resend accepted the request but returned an unreadable email ID", true)
			}
			return nil
		}
		var providerError struct {
			Name    string `json:"name"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(body, &providerError)
		detail := strings.TrimSpace(providerError.Message)
		if detail == "" {
			detail = "Resend rejected the email request"
		}
		if response.StatusCode == http.StatusTooManyRequests {
			if retryAfter := strings.TrimSpace(response.Header.Get("Retry-After")); retryAfter != "" {
				detail += "; try again in " + retryAfter + " seconds"
			}
			return verificationError("verification_rate_limited", detail, true)
		}
		if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusUnprocessableEntity {
			return verificationError("verification_delivery_config", detail, false)
		}
		return verificationError("verification_delivery_failed", detail, response.StatusCode >= 500)
	}
	return verificationError("verification_delivery_failed", "email verification could not be sent", true)
}

func (s *accountService) verificationProviderConfiguredForPurpose(channel, purpose string) bool {
	return channel == "email" && s.runtime.ResendAPIKey != "" && s.runtime.VerificationEmailFrom != ""
}

func (s *accountService) verificationProviderConfigured(channel string) bool {
	return s.verificationProviderConfiguredForPurpose(channel, verificationPurposeContact)
}

func (s *accountService) isDevelopmentRuntime() bool {
	return s.runtime.Profile == config.ProfileDev || s.runtime.Environment == "development"
}

func validateVerificationDestination(channel, destination string) error {
	if channel != "email" {
		return errors.New("only email verification is supported")
	}
	parsed, err := mail.ParseAddress(destination)
	if err != nil || !strings.EqualFold(parsed.Address, destination) {
		return errors.New("email address has an invalid format")
	}
	return nil
}

func normalizeVerificationDestination(channel, destination string) string {
	destination = strings.TrimSpace(destination)
	if channel == "email" {
		return strings.ToLower(destination)
	}
	return destination
}

func randomVerificationCode() (string, error) {
	value, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", value.Int64()), nil
}

func (s *accountService) startPasswordReset(ctx context.Context, channel, destination string) (model.AccountVerificationStart, error) {
	state, err := s.ensureState(ctx)
	if err != nil {
		return model.AccountVerificationStart{}, err
	}
	channel = strings.ToLower(strings.TrimSpace(channel))
	destination = normalizeVerificationDestination(channel, destination)
	if err := validateVerificationDestination(channel, destination); err != nil {
		return model.AccountVerificationStart{}, verificationError("verification_destination_invalid", err.Error(), false)
	}
	if !s.verificationProviderConfiguredForPurpose(channel, verificationPurposeReset) && !s.isDevelopmentRuntime() {
		return model.AccountVerificationStart{}, verificationError("verification_not_configured", "email verification provider is not configured", false)
	}
	now := s.now()
	matches := channel == "email" && state.Profile.EmailVerification == model.AccountVerificationVerified && strings.EqualFold(state.Profile.Email, destination)
	if !matches {
		return genericVerificationStart(channel, destination, now), nil
	}
	return s.createVerificationChallenge(ctx, state, verificationPurposeReset, channel, destination, true)
}

func (s *accountService) confirmPasswordReset(ctx context.Context, channel, destination, code, newPassword string) (model.AccountSession, error) {
	if err := validateAccountPassword(newPassword); err != nil {
		return model.AccountSession{}, err
	}
	state, err := s.ensureState(ctx)
	if err != nil {
		return model.AccountSession{}, err
	}
	channel = strings.ToLower(strings.TrimSpace(channel))
	destination = normalizeVerificationDestination(channel, destination)
	if err := validateVerificationDestination(channel, destination); err != nil {
		return model.AccountSession{}, verificationError("verification_destination_invalid", err.Error(), false)
	}
	challenge, err := s.verifyChallenge(ctx, state, verificationPurposeReset, channel, code)
	if err != nil || !strings.EqualFold(challenge.Destination, destination) {
		if err != nil {
			return s.sessionView(state), err
		}
		return s.sessionView(state), verificationError("verification_invalid", "verification code is incorrect", false)
	}
	hash, err := s.hashPassword(newPassword)
	if err != nil {
		return model.AccountSession{}, verificationError("account_password_hash_failed", "password could not be secured", true)
	}
	state.PasswordHash = hash
	state.Profile.HasPassword = true
	state.Profile.UpdatedAt = s.now()
	state.Authenticated = false
	state.LoginFailures = model.AccountLoginFailures{}
	state.VerificationChallenges = map[string]model.AccountVerificationChallenge{}
	if err := s.store.Save(ctx, state); err != nil {
		return model.AccountSession{}, err
	}
	return s.sessionView(state), nil
}

func (s *accountService) verificationState(ctx context.Context, channel string) (model.AccountVerificationState, error) {
	state, err := s.authenticatedState(ctx)
	if err != nil {
		return model.AccountVerificationState{}, err
	}
	channel = strings.ToLower(strings.TrimSpace(channel))
	if channel != "email" {
		return model.AccountVerificationState{}, verificationError("verification_channel_unsupported", "only email verification is supported", false)
	}
	status := state.Profile.EmailVerification
	result := model.AccountVerificationState{Channel: channel, Status: status}
	key := verificationChallengeKey(verificationPurposeContact, channel)
	challenge, ok := state.VerificationChallenges[key]
	if !ok || challenge.UserID != state.Profile.ID {
		return result, nil
	}
	if !s.now().Before(challenge.ExpiresAt) {
		delete(state.VerificationChallenges, key)
		_ = s.store.Save(ctx, state)
		return result, nil
	}
	result.Status = model.AccountVerificationPending
	result.MaskedDestination = maskVerificationDestination(channel, challenge.Destination)
	result.ExpiresAt = &challenge.ExpiresAt
	result.ResendAt = &challenge.ResendAt
	return result, nil
}

func setVerificationStatus(profile *model.AccountProfile, channel string, status model.AccountVerificationStatus) {
	if channel == "email" {
		profile.EmailVerification = status
	}
}

func maskVerificationDestination(channel, destination string) string {
	if channel == "email" {
		parts := strings.SplitN(destination, "@", 2)
		if len(parts) == 2 {
			return string([]rune(parts[0])[:1]) + "•••@" + parts[1]
		}
	}
	return "•••"
}
