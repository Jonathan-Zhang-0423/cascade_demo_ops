package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/model"
	"cascade-demoops/backend/internal/store"
)

func TestAccountServicePersistsProfileSessionAndPlan(t *testing.T) {
	ctx := context.Background()
	accountStore := store.NewMemoryAccountStore()
	service := newAccountService(config.AppRuntimeConfig{Profile: config.ProfileDev, Environment: "development"}, accountStore)
	session, err := service.session(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !session.Authenticated || session.User == nil || session.User.DisplayName == "" {
		t.Fatalf("unexpected default session: %#v", session)
	}
	profile, err := service.updateProfile(ctx, accountProfileUpdate{DisplayName: "Jonathan Zhang"})
	if err != nil {
		t.Fatal(err)
	}
	if profile.Initials != "JZ" || profile.Email != "" {
		t.Fatalf("unexpected profile: %#v", profile)
	}
	plan, err := service.plan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if plan.CreditsRemaining != plan.CreditsIncluded-plan.CreditsUsed || len(plan.Usage) != 3 {
		t.Fatalf("unexpected plan: %#v", plan)
	}
	loggedOut, err := service.logout(ctx)
	if err != nil || loggedOut.Authenticated {
		t.Fatalf("logout failed: %#v %v", loggedOut, err)
	}
	if _, err := service.profile(ctx); err == nil {
		t.Fatal("profile should require an authenticated session")
	}
	loggedIn, err := service.devLogin(ctx)
	if err != nil || !loggedIn.Authenticated || loggedIn.User == nil || loggedIn.User.DisplayName != "Jonathan Zhang" {
		t.Fatalf("dev login failed: %#v %v", loggedIn, err)
	}
}

type accountRoundTripper func(*http.Request) (*http.Response, error)

func (fn accountRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestAccountServiceCompletesConfiguredGitHubDeviceFlowWithoutPersistingTokenInProfile(t *testing.T) {
	ctx := context.Background()
	service := newAccountService(config.AppRuntimeConfig{Profile: config.ProfileDev, Environment: "development", GitHubOAuthClientID: "client_123"}, store.NewMemoryAccountStore())
	if _, err := service.session(ctx); err != nil {
		t.Fatal(err)
	}
	service.client = &http.Client{Transport: accountRoundTripper(func(request *http.Request) (*http.Response, error) {
		body := `{"device_code":"device-secret","user_code":"ABCD-EFGH","verification_uri":"https://github.com/login/device","expires_in":600,"interval":1}`
		switch request.URL.String() {
		case "https://github.com/login/oauth/access_token":
			body = `{"access_token":"oauth-secret-token","token_type":"bearer","scope":"read:user"}`
		case "https://api.github.com/user":
			if request.Header.Get("Authorization") != "Bearer oauth-secret-token" {
				t.Fatalf("GitHub user request did not use the OAuth token")
			}
			body = `{"id":42,"login":"cascade-user","name":"Cascade User","avatar_url":"https://avatars.githubusercontent.com/u/42","html_url":"https://github.com/cascade-user"}`
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	storedToken := ""
	service.storeToken = func(token string) error { storedToken = token; return nil }
	service.deleteToken = func() error { return nil }
	flow, err := service.startGitHubDevice(ctx)
	if err != nil {
		t.Fatal(err)
	}
	completed, err := service.pollGitHubDevice(ctx, flow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != "authorized" || completed.GitHub == nil || storedToken != "oauth-secret-token" {
		t.Fatalf("unexpected completed flow: %#v token=%q", completed, storedToken)
	}
	profile, err := service.profile(ctx)
	if err != nil || profile.GitHub == nil || strings.Contains(profile.GitHub.Login, "secret") {
		t.Fatalf("unexpected public profile: %#v %v", profile, err)
	}
}

func TestAccountServiceValidatesProfileAndCompletesMockGitHubFlow(t *testing.T) {
	ctx := context.Background()
	service := newAccountService(config.AppRuntimeConfig{Profile: config.ProfileDev, Environment: "development"}, store.NewMemoryAccountStore())
	if _, err := service.session(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := service.updateProfile(ctx, accountProfileUpdate{DisplayName: ""}); err == nil {
		t.Fatal("invalid profile should be rejected")
	}
	flow, err := service.startGitHubDevice(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if flow.Status != "pending" || flow.UserCode == "" {
		t.Fatalf("unexpected device flow: %#v", flow)
	}
	completed, err := service.pollGitHubDevice(ctx, flow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != "authorized" || completed.GitHub == nil {
		t.Fatalf("mock device flow was not authorized: %#v", completed)
	}
	profile, err := service.profile(ctx)
	if err != nil || profile.GitHub == nil {
		t.Fatalf("GitHub identity was not attached: %#v %v", profile, err)
	}
	profile, err = service.disconnectGitHub(ctx)
	if err != nil || profile.GitHub != nil {
		t.Fatalf("GitHub identity was not disconnected: %#v %v", profile, err)
	}
}

func TestAccountServiceVerifiesEmailWithExpiringHashedCodes(t *testing.T) {
	ctx := context.Background()
	accountStore := store.NewMemoryAccountStore()
	service := newAccountService(config.AppRuntimeConfig{Profile: config.ProfileDev, Environment: "development", AccountVerificationSecret: "test-secret"}, accountStore)
	service.generateVerificationCode = func() (string, error) { return "654321", nil }
	service.deliverVerification = func(context.Context, string, string, string, string) error { return nil }
	if _, err := service.session(ctx); err != nil {
		t.Fatal(err)
	}
	emailFlow, err := service.startVerification(ctx, "email", "jonathan@example.com")
	if err != nil || emailFlow.DevelopmentCode == "" || emailFlow.MaskedDestination == "jonathan@example.com" {
		t.Fatalf("unexpected email verification start: %#v %v", emailFlow, err)
	}
	if emailFlow.DevelopmentCode != "654321" {
		t.Fatalf("injected verification generator was not used: %#v", emailFlow)
	}
	persisted, err := accountStore.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	challenge := persisted.VerificationChallenges[verificationChallengeKey(verificationPurposeContact, "email")]
	if challenge.CodeHash == "" || challenge.CodeHash == emailFlow.DevelopmentCode || persisted.Profile.EmailVerification != "unverified" || persisted.Profile.Email != "" {
		t.Fatalf("verification code was not stored safely: %#v", persisted)
	}
	if _, err := service.confirmVerification(ctx, "email", "000000"); err == nil {
		t.Fatal("incorrect verification code should be rejected")
	}
	profile, err := service.confirmVerification(ctx, "email", emailFlow.DevelopmentCode)
	if err != nil || profile.EmailVerification != "verified" || profile.Email != "jonathan@example.com" {
		t.Fatalf("email verification failed: %#v %v", profile, err)
	}
	if _, err := service.startVerification(ctx, "phone", "+14155550123"); bridgeErrorCode(err) != "verification_destination_invalid" {
		t.Fatalf("phone verification should be unavailable, got %q: %v", bridgeErrorCode(err), err)
	}
	replacement, err := service.startVerification(ctx, "email", "changed@example.com")
	if err != nil {
		t.Fatal(err)
	}
	profile, _ = service.profile(ctx)
	if profile.Email != "jonathan@example.com" || profile.EmailVerification != "verified" {
		t.Fatalf("pending replacement changed the active contact: %#v", profile)
	}
	profile, err = service.confirmVerification(ctx, "email", replacement.DevelopmentCode)
	if err != nil || profile.Email != "changed@example.com" || profile.EmailVerification != "verified" {
		t.Fatalf("verified replacement was not committed: %#v %v", profile, err)
	}
}

func TestAccountVerificationExpiryAttemptLimitAndReplayPrevention(t *testing.T) {
	ctx := context.Background()
	accountStore := store.NewMemoryAccountStore()
	service := newAccountService(config.AppRuntimeConfig{Profile: config.ProfileDev, Environment: "development", AccountVerificationSecret: "0123456789abcdef0123456789abcdef"}, accountStore)
	currentTime := time.Date(2026, time.August, 2, 9, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return currentTime }
	service.generateVerificationCode = func() (string, error) { return "654321", nil }
	if _, err := service.session(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := service.startVerification(ctx, "email", "jonathan@example.com"); err != nil {
		t.Fatal(err)
	}
	currentTime = currentTime.Add(verificationLifetime + time.Second)
	if _, err := service.confirmVerification(ctx, "email", "654321"); bridgeErrorCode(err) != "verification_expired" {
		t.Fatalf("expired code returned %q: %v", bridgeErrorCode(err), err)
	}

	if _, err := service.startVerification(ctx, "email", "jonathan@example.com"); err != nil {
		t.Fatal(err)
	}
	for attempt := 1; attempt <= verificationMaxAttempts; attempt++ {
		_, err := service.confirmVerification(ctx, "email", "000000")
		wantCode := "verification_invalid"
		if attempt == verificationMaxAttempts {
			wantCode = "verification_attempts_exhausted"
		}
		if bridgeErrorCode(err) != wantCode {
			t.Fatalf("attempt %d returned %q: %v", attempt, bridgeErrorCode(err), err)
		}
	}
	if _, err := service.confirmVerification(ctx, "email", "654321"); bridgeErrorCode(err) != "verification_expired" {
		t.Fatalf("exhausted challenge should no longer accept its code: %v", err)
	}

	currentTime = currentTime.Add(verificationCooldown + time.Second)
	if _, err := service.startVerification(ctx, "email", "jonathan@example.com"); err != nil {
		t.Fatal(err)
	}
	if profile, err := service.confirmVerification(ctx, "email", "654321"); err != nil || profile.EmailVerification != model.AccountVerificationVerified {
		t.Fatalf("fresh challenge did not verify: %#v %v", profile, err)
	}
	if _, err := service.confirmVerification(ctx, "email", "654321"); bridgeErrorCode(err) != "verification_expired" {
		t.Fatalf("verified code was accepted more than once: %v", err)
	}
}

func TestRandomVerificationCodeIsAlwaysSixNumericDigits(t *testing.T) {
	for index := 0; index < 1_000; index++ {
		code, err := randomVerificationCode()
		if err != nil {
			t.Fatal(err)
		}
		if len(code) != 6 || strings.Trim(code, "0123456789") != "" {
			t.Fatalf("generated invalid verification code %q", code)
		}
	}
}

func TestAccountPasswordLifecycleAndVerifiedIdentifierLogin(t *testing.T) {
	ctx := context.Background()
	accountStore := store.NewMemoryAccountStore()
	service := newAccountService(config.AppRuntimeConfig{Profile: config.ProfileDev, Environment: "development", AccountVerificationSecret: "0123456789abcdef0123456789abcdef"}, accountStore)
	service.generateVerificationCode = func() (string, error) { return "654321", nil }
	if _, err := service.session(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := service.updateProfile(ctx, accountProfileUpdate{DisplayName: "Cascade Operator"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.startVerification(ctx, "email", "operator@example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.confirmVerification(ctx, "email", "654321"); err != nil {
		t.Fatal(err)
	}
	loggedOut, err := service.setPassword(ctx, "a sufficiently long password")
	if err != nil || loggedOut.Authenticated || loggedOut.PasswordSetupNeeded {
		t.Fatalf("password setup did not end the session: %#v %v", loggedOut, err)
	}
	persisted, _ := accountStore.Load(ctx)
	if persisted.PasswordHash == "a sufficiently long password" || !strings.HasPrefix(persisted.PasswordHash, "$argon2id$") {
		t.Fatalf("password was not stored as an Argon2id hash: %q", persisted.PasswordHash)
	}
	if _, err := service.passwordLogin(ctx, "operator@example.com", "wrong password"); bridgeErrorCode(err) != "account_login_invalid" {
		t.Fatalf("wrong password returned %q: %v", bridgeErrorCode(err), err)
	}
	loggedIn, err := service.passwordLogin(ctx, "cascade operator", "a sufficiently long password")
	if err != nil || !loggedIn.Authenticated || loggedIn.User == nil || !loggedIn.User.HasPassword {
		t.Fatalf("username login failed: %#v %v", loggedIn, err)
	}
	changed, err := service.changePassword(ctx, "a sufficiently long password", "a different long password")
	if err != nil || changed.Authenticated {
		t.Fatalf("password change did not end the session: %#v %v", changed, err)
	}
	if _, err := service.passwordLogin(ctx, "operator@example.com", "a different long password"); err != nil {
		t.Fatalf("verified email login failed: %v", err)
	}
}

func TestPasswordResetIsPurposeScopedAndAntiEnumerationSafe(t *testing.T) {
	ctx := context.Background()
	accountStore := store.NewMemoryAccountStore()
	service := newAccountService(config.AppRuntimeConfig{Profile: config.ProfileDev, Environment: "development", AccountVerificationSecret: "0123456789abcdef0123456789abcdef"}, accountStore)
	service.generateVerificationCode = func() (string, error) { return "654321", nil }
	if _, err := service.session(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := service.startVerification(ctx, "email", "operator@example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.confirmVerification(ctx, "email", "654321"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.setPassword(ctx, "a sufficiently long password"); err != nil {
		t.Fatal(err)
	}
	unknown, err := service.startPasswordReset(ctx, "email", "unknown@example.com")
	if err != nil || unknown.Status != "pending" || unknown.DevelopmentCode != "" {
		t.Fatalf("unknown destination leaked account state: %#v %v", unknown, err)
	}
	reset, err := service.startPasswordReset(ctx, "email", "operator@example.com")
	if err != nil || reset.DevelopmentCode != "654321" {
		t.Fatalf("verified destination reset did not start: %#v %v", reset, err)
	}
	if _, err := service.devLogin(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := service.confirmVerification(ctx, "email", "654321"); bridgeErrorCode(err) != "verification_expired" {
		t.Fatalf("reset code crossed into contact verification: %v", err)
	}
	result, err := service.confirmPasswordReset(ctx, "email", "operator@example.com", "654321", "a recovered long password")
	if err != nil || result.Authenticated {
		t.Fatalf("password reset failed: %#v %v", result, err)
	}
	if _, err := service.passwordLogin(ctx, "operator@example.com", "a recovered long password"); err != nil {
		t.Fatalf("recovered password did not authenticate: %v", err)
	}
}

func TestAccountServiceUsesConfiguredResendAPI(t *testing.T) {
	ctx := context.Background()
	runtime := config.AppRuntimeConfig{
		Profile: config.ProfileDev, Environment: "development", AccountVerificationSecret: "0123456789abcdef0123456789abcdef",
		ResendAPIKey: "resend-secret", VerificationEmailFrom: approvedVerificationSender,
	}
	service := newAccountService(runtime, store.NewMemoryAccountStore())
	service.now = func() time.Time { return time.Unix(1_725_000_000, 0).UTC() }
	requests := []string{}
	resendPayload := map[string]any{}
	service.client = &http.Client{Transport: accountRoundTripper(func(request *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(request.Body)
		responseBody := `{"id":"sent"}`
		if request.URL.Host == "api.resend.com" {
			if err := json.Unmarshal(body, &resendPayload); err != nil {
				t.Fatalf("decode Resend request: %v", err)
			}
		}
		requests = append(requests, request.Method+" "+request.URL.String()+" "+request.Header.Get("Authorization")+" user-agent="+request.Header.Get("User-Agent")+" idempotency="+request.Header.Get("Idempotency-Key")+" "+string(body))
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(strings.NewReader(responseBody)), Header: make(http.Header)}, nil
	})}
	if _, err := service.session(ctx); err != nil {
		t.Fatal(err)
	}
	if result, err := service.startVerification(ctx, "email", "jonathan@example.com"); err != nil || result.DevelopmentCode != "" {
		t.Fatalf("configured email delivery failed: %#v %v", result, err)
	}
	joined := strings.Join(requests, "\n")
	if !strings.Contains(joined, "https://api.resend.com/emails Bearer resend-secret") {
		t.Fatalf("Resend received an unexpected request: %s", joined)
	}
	if !strings.Contains(joined, "user-agent=cascade-demoops/1.0") || !strings.Contains(joined, "idempotency=cascade-account-verification/") || !strings.Contains(joined, `"html"`) || !strings.Contains(joined, `"account_verification"`) {
		t.Fatalf("Resend request is missing required delivery safeguards: %s", joined)
	}
	tags, _ := resendPayload["tags"].([]any)
	if resendPayload["from"] != approvedVerificationSender || resendPayload["subject"] != approvedVerificationSubject || !strings.Contains(fmt.Sprint(resendPayload["html"]), "Your Cascade verification code expires in 10 minutes") || !strings.Contains(fmt.Sprint(resendPayload["text"]), "— The Cascade AI team") || len(tags) != 1 {
		t.Fatalf("Resend request does not match the approved email template: %s", joined)
	}
}

func TestAccountServiceMapsResendRateLimitToRetryableMessage(t *testing.T) {
	service := newAccountService(config.AppRuntimeConfig{Profile: config.ProfileDev, Environment: "development", ResendAPIKey: "secret", VerificationEmailFrom: "verify@example.com"}, store.NewMemoryAccountStore())
	service.client = &http.Client{Transport: accountRoundTripper(func(request *http.Request) (*http.Response, error) {
		header := make(http.Header)
		header.Set("Retry-After", "3")
		return &http.Response{StatusCode: http.StatusTooManyRequests, Status: "429 Too Many Requests", Body: io.NopCloser(strings.NewReader(`{"name":"rate_limit_exceeded","message":"Too many requests"}`)), Header: header}, nil
	})}
	err := service.sendVerificationEmail(context.Background(), "jonathan@example.com", "123456")
	if err == nil || !strings.Contains(err.Error(), "try again in 3 seconds") || strings.Contains(err.Error(), "secret") {
		t.Fatalf("unexpected Resend rate-limit error: %v", err)
	}
}

func TestAccountServiceRetriesTransientResendFailureWithSameIdempotencyKey(t *testing.T) {
	service := newAccountService(config.AppRuntimeConfig{Profile: config.ProfileDev, Environment: "development", AccountVerificationSecret: "0123456789abcdef0123456789abcdef", ResendAPIKey: "secret", VerificationEmailFrom: approvedVerificationSender}, store.NewMemoryAccountStore())
	calls := 0
	keys := []string{}
	bodies := []string{}
	service.client = &http.Client{Transport: accountRoundTripper(func(request *http.Request) (*http.Response, error) {
		calls++
		keys = append(keys, request.Header.Get("Idempotency-Key"))
		body, _ := io.ReadAll(request.Body)
		bodies = append(bodies, string(body))
		if calls == 1 {
			return &http.Response{StatusCode: http.StatusServiceUnavailable, Status: "503 Service Unavailable", Body: io.NopCloser(strings.NewReader(`{"name":"application_error","message":"temporarily unavailable"}`)), Header: make(http.Header)}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(strings.NewReader(`{"id":"email_123"}`)), Header: make(http.Header)}, nil
	})}
	if err := service.sendVerificationEmail(context.Background(), "jonathan@example.com", "123456"); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || keys[0] == "" || keys[0] != keys[1] || bodies[0] != bodies[1] {
		t.Fatalf("Resend retry was not idempotent: calls=%d keys=%v", calls, keys)
	}
}

func TestAccountServiceRejectsMalformedResendSuccessAndUnsafeConfiguration(t *testing.T) {
	service := newAccountService(config.AppRuntimeConfig{Profile: config.ProfileDev, Environment: "development", ResendAPIKey: "secret", VerificationEmailFrom: approvedVerificationSender}, store.NewMemoryAccountStore())
	service.client = &http.Client{Transport: accountRoundTripper(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(strings.NewReader(`{"unexpected":true}`)), Header: make(http.Header)}, nil
	})}
	if err := service.sendVerificationEmail(context.Background(), "jonathan@example.com", "123456"); err == nil || bridgeErrorCode(err) != "verification_delivery_failed" {
		t.Fatalf("malformed Resend success should fail safely: %v", err)
	}
	if _, err := service.session(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := service.startVerification(context.Background(), "email", "jonathan@example.com"); err == nil || bridgeErrorCode(err) != "verification_not_configured" {
		t.Fatalf("short verification secret should block real delivery: %v", err)
	}
	service.runtime.AccountVerificationSecret = "0123456789abcdef0123456789abcdef"
	service.runtime.VerificationEmailFrom = "Wrong Sender <noreply@cascadeai.co>"
	if _, err := service.startVerification(context.Background(), "email", "jonathan@example.com"); err == nil || bridgeErrorCode(err) != "verification_sender_invalid" {
		t.Fatalf("unexpected sender should block real delivery: %v", err)
	}
}

func TestAccountServiceClassifiesResendProviderAndNetworkFailures(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusUnprocessableEntity} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			service := newAccountService(config.AppRuntimeConfig{Profile: config.ProfileDev, Environment: "development", AccountVerificationSecret: "0123456789abcdef0123456789abcdef", ResendAPIKey: "secret", VerificationEmailFrom: approvedVerificationSender}, store.NewMemoryAccountStore())
			service.client = &http.Client{Transport: accountRoundTripper(func(request *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: status, Status: fmt.Sprintf("%d %s", status, http.StatusText(status)), Body: io.NopCloser(strings.NewReader(`{"name":"provider_error","message":"request rejected"}`)), Header: make(http.Header)}, nil
			})}
			err := service.sendVerificationEmail(context.Background(), "jonathan@example.com", "123456")
			if err == nil {
				t.Fatal("provider error should be returned")
			}
			wantCode := "verification_delivery_failed"
			if status == http.StatusUnauthorized || status == http.StatusForbidden || status == http.StatusUnprocessableEntity {
				wantCode = "verification_delivery_config"
			}
			if bridgeErrorCode(err) != wantCode {
				t.Fatalf("code=%q want=%q error=%v", bridgeErrorCode(err), wantCode, err)
			}
		})
	}
	service := newAccountService(config.AppRuntimeConfig{Profile: config.ProfileDev, Environment: "development", AccountVerificationSecret: "0123456789abcdef0123456789abcdef", ResendAPIKey: "secret", VerificationEmailFrom: approvedVerificationSender}, store.NewMemoryAccountStore())
	calls := 0
	service.client = &http.Client{Transport: accountRoundTripper(func(request *http.Request) (*http.Response, error) { calls++; return nil, io.ErrUnexpectedEOF })}
	err := service.sendVerificationEmail(context.Background(), "jonathan@example.com", "123456")
	if err == nil || bridgeErrorCode(err) != "verification_delivery_failed" || !bridgeErrorRetryable(err) || calls != 2 {
		t.Fatalf("network failure was not retried and classified safely: calls=%d error=%v", calls, err)
	}
}
