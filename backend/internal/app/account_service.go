package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/user"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/credentialstore"
	"cascade-demoops/backend/internal/model"
	"cascade-demoops/backend/internal/store"
)

const githubOAuthCredentialRef = "credential://github/oauth/account"

type accountService struct {
	runtime                  config.AppRuntimeConfig
	store                    store.AccountStore
	client                   *http.Client
	now                      func() time.Time
	storeToken               func(string) error
	deleteToken              func() error
	mu                       sync.Mutex
	flows                    map[string]*githubDeviceFlowState
	deliverVerification      func(context.Context, string, string, string, string) error
	generateVerificationCode func() (string, error)
	hashPassword             func(string) (string, error)
	verifyPassword           func(string, string) (bool, error)
}

type githubDeviceFlowState struct {
	view       model.GitHubDeviceFlow
	deviceCode string
	mock       bool
}

type accountProfileUpdate struct {
	DisplayName string
	// Retained for source compatibility with older internal callers. Contact
	// destinations are intentionally changed only through verified challenges.
	Email string
	Phone string
}

func newAccountService(runtime config.AppRuntimeConfig, accountStore store.AccountStore) *accountService {
	service := &accountService{
		runtime:                  runtime,
		store:                    accountStore,
		client:                   &http.Client{Timeout: 12 * time.Second},
		now:                      func() time.Time { return time.Now().UTC() },
		storeToken:               credentialstore.StoreGitHubOAuthToken,
		deleteToken:              credentialstore.DeleteGitHubOAuthToken,
		flows:                    map[string]*githubDeviceFlowState{},
		generateVerificationCode: randomVerificationCode,
		hashPassword:             hashAccountPassword,
		verifyPassword:           verifyAccountPassword,
	}
	service.deliverVerification = service.deliverVerificationCode
	return service
}

func (s *accountService) session(ctx context.Context) (model.AccountSession, error) {
	state, err := s.ensureState(ctx)
	if err != nil {
		return model.AccountSession{}, err
	}
	result := s.sessionView(state)
	if state.Authenticated || result.PasswordSetupNeeded {
		profile := s.publicProfile(state)
		result.User = &profile
	}
	return result, nil
}

func (s *accountService) devLogin(ctx context.Context) (model.AccountSession, error) {
	if s.runtime.Profile != config.ProfileDev && s.runtime.Environment != "development" {
		return model.AccountSession{}, errors.New("development login is unavailable in this runtime")
	}
	state, err := s.ensureState(ctx)
	if err != nil {
		return model.AccountSession{}, err
	}
	state.Authenticated = true
	if err := s.store.Save(ctx, state); err != nil {
		return model.AccountSession{}, err
	}
	return s.sessionView(state), nil
}

func (s *accountService) logout(ctx context.Context) (model.AccountSession, error) {
	state, err := s.ensureState(ctx)
	if err != nil {
		return model.AccountSession{}, err
	}
	state.Authenticated = false
	if err := s.store.Save(ctx, state); err != nil {
		return model.AccountSession{}, err
	}
	return s.sessionView(state), nil
}

func (s *accountService) profile(ctx context.Context) (model.AccountProfile, error) {
	state, err := s.authenticatedState(ctx)
	if err != nil {
		return model.AccountProfile{}, err
	}
	return s.publicProfile(state), nil
}

func (s *accountService) updateProfile(ctx context.Context, update accountProfileUpdate) (model.AccountProfile, error) {
	state, err := s.authenticatedState(ctx)
	if err != nil {
		return model.AccountProfile{}, err
	}
	displayName := strings.TrimSpace(update.DisplayName)
	if utf8.RuneCountInString(displayName) < 1 || utf8.RuneCountInString(displayName) > 80 {
		return model.AccountProfile{}, errors.New("display name must contain 1 to 80 characters")
	}
	state.Profile.DisplayName = displayName
	state.Profile.Initials = accountInitials(displayName)
	state.Profile.UpdatedAt = s.now()
	if err := s.store.Save(ctx, state); err != nil {
		return model.AccountProfile{}, err
	}
	return s.publicProfile(state), nil
}

func (s *accountService) plan(ctx context.Context) (model.AccountPlan, error) {
	state, err := s.authenticatedState(ctx)
	if err != nil {
		return model.AccountPlan{}, err
	}
	plan := state.Plan
	plan.CreditsRemaining = plan.CreditsIncluded - plan.CreditsUsed
	if plan.CreditsRemaining < 0 {
		plan.CreditsRemaining = 0
	}
	return plan, nil
}

func (s *accountService) startGitHubDevice(ctx context.Context) (model.GitHubDeviceFlow, error) {
	if _, err := s.authenticatedState(ctx); err != nil {
		return model.GitHubDeviceFlow{}, err
	}
	if strings.TrimSpace(s.runtime.GitHubOAuthClientID) == "" {
		if s.runtime.Profile != config.ProfileDev && s.runtime.Environment != "development" {
			return model.GitHubDeviceFlow{}, errors.New("GitHub OAuth is not configured")
		}
		flow := model.GitHubDeviceFlow{ID: randomAccountID("github"), Status: "pending", UserCode: "CASCADE-DEV", VerificationURI: "https://github.com/login/device", ExpiresAt: s.now().Add(10 * time.Minute), IntervalSeconds: 1}
		s.mu.Lock()
		s.flows[flow.ID] = &githubDeviceFlowState{view: flow, mock: true}
		s.mu.Unlock()
		return flow, nil
	}
	request := url.Values{"client_id": {s.runtime.GitHubOAuthClientID}, "scope": {"read:user user:email"}}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://github.com/login/device/code", strings.NewReader(request.Encode()))
	if err != nil {
		return model.GitHubDeviceFlow{}, err
	}
	httpRequest.Header.Set("Accept", "application/json")
	httpRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := s.client.Do(httpRequest)
	if err != nil {
		return model.GitHubDeviceFlow{}, fmt.Errorf("GitHub device authorization is unavailable: %w", err)
	}
	defer response.Body.Close()
	var payload struct {
		DeviceCode       string `json:"device_code"`
		UserCode         string `json:"user_code"`
		VerificationURI  string `json:"verification_uri"`
		ExpiresIn        int    `json:"expires_in"`
		Interval         int    `json:"interval"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return model.GitHubDeviceFlow{}, errors.New("GitHub returned an unreadable device authorization response")
	}
	if response.StatusCode != http.StatusOK || payload.DeviceCode == "" || payload.UserCode == "" {
		return model.GitHubDeviceFlow{}, errors.New(strings.TrimSpace(payload.ErrorDescription + " " + payload.Error))
	}
	interval := payload.Interval
	if interval < 1 {
		interval = 5
	}
	flow := model.GitHubDeviceFlow{ID: randomAccountID("github"), Status: "pending", UserCode: payload.UserCode, VerificationURI: payload.VerificationURI, ExpiresAt: s.now().Add(time.Duration(payload.ExpiresIn) * time.Second), IntervalSeconds: interval}
	s.mu.Lock()
	s.flows[flow.ID] = &githubDeviceFlowState{view: flow, deviceCode: payload.DeviceCode}
	s.mu.Unlock()
	return flow, nil
}

func (s *accountService) pollGitHubDevice(ctx context.Context, flowID string) (model.GitHubDeviceFlow, error) {
	s.mu.Lock()
	flowState := s.flows[flowID]
	if flowState == nil {
		s.mu.Unlock()
		return model.GitHubDeviceFlow{}, errors.New("GitHub authorization session was not found")
	}
	flow := flowState.view
	mock := flowState.mock
	deviceCode := flowState.deviceCode
	s.mu.Unlock()
	if flow.Status != "pending" {
		return flow, nil
	}
	if !s.now().Before(flow.ExpiresAt) {
		return s.finishGitHubFlow(flowID, "expired", nil, "GitHub authorization expired"), nil
	}
	if mock {
		identity := &model.GitHubIdentity{ID: 4243, Login: "jonathan-zhang", Name: "Jonathan Zhang", AvatarURL: "https://avatars.githubusercontent.com/u/4243?v=4", ProfileURL: "https://github.com/jonathan-zhang", ConnectedAt: s.now()}
		if err := s.attachGitHubIdentity(ctx, identity, ""); err != nil {
			return model.GitHubDeviceFlow{}, err
		}
		return s.finishGitHubFlow(flowID, "authorized", identity, ""), nil
	}
	token, status, detail, err := s.exchangeGitHubDeviceCode(ctx, deviceCode)
	if err != nil {
		return model.GitHubDeviceFlow{}, err
	}
	if status != "authorized" {
		return s.finishGitHubFlow(flowID, status, nil, detail), nil
	}
	identity, err := s.fetchGitHubIdentity(ctx, token)
	if err != nil {
		return model.GitHubDeviceFlow{}, err
	}
	if err := s.storeToken(token); err != nil {
		return model.GitHubDeviceFlow{}, errors.New("GitHub authorized the account, but the OS credential vault could not store the token")
	}
	if err := s.attachGitHubIdentity(ctx, identity, githubOAuthCredentialRef); err != nil {
		_ = s.deleteToken()
		return model.GitHubDeviceFlow{}, err
	}
	return s.finishGitHubFlow(flowID, "authorized", identity, ""), nil
}

func (s *accountService) disconnectGitHub(ctx context.Context) (model.AccountProfile, error) {
	state, err := s.authenticatedState(ctx)
	if err != nil {
		return model.AccountProfile{}, err
	}
	if state.GitHubCredentialRef != "" {
		if err := s.deleteToken(); err != nil {
			return model.AccountProfile{}, errors.New("GitHub credential could not be removed from the OS credential vault")
		}
	}
	state.GitHubCredentialRef = ""
	state.Profile.GitHub = nil
	state.Profile.UpdatedAt = s.now()
	if err := s.store.Save(ctx, state); err != nil {
		return model.AccountProfile{}, err
	}
	return s.publicProfile(state), nil
}

func (s *accountService) exchangeGitHubDeviceCode(ctx context.Context, deviceCode string) (string, string, string, error) {
	payload, _ := json.Marshal(map[string]string{"client_id": s.runtime.GitHubOAuthClientID, "device_code": deviceCode, "grant_type": "urn:ietf:params:oauth:grant-type:device_code"})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://github.com/login/oauth/access_token", bytes.NewReader(payload))
	if err != nil {
		return "", "", "", err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	response, err := s.client.Do(request)
	if err != nil {
		return "", "", "", fmt.Errorf("GitHub authorization status is unavailable: %w", err)
	}
	defer response.Body.Close()
	var result struct {
		AccessToken      string `json:"access_token"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return "", "", "", errors.New("GitHub returned an unreadable authorization response")
	}
	if result.AccessToken != "" {
		return result.AccessToken, "authorized", "", nil
	}
	switch result.Error {
	case "authorization_pending", "slow_down":
		return "", "pending", "", nil
	case "access_denied":
		return "", "denied", result.ErrorDescription, nil
	case "expired_token":
		return "", "expired", result.ErrorDescription, nil
	default:
		return "", "failed", strings.TrimSpace(result.ErrorDescription + " " + result.Error), nil
	}
}

func (s *accountService) fetchGitHubIdentity(ctx context.Context, token string) (*model.GitHubIdentity, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/user", nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	response, err := s.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("GitHub profile is unavailable: %w", err)
	}
	defer response.Body.Close()
	var payload struct {
		ID        int64  `json:"id"`
		Login     string `json:"login"`
		Name      string `json:"name"`
		AvatarURL string `json:"avatar_url"`
		HTMLURL   string `json:"html_url"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil || response.StatusCode != http.StatusOK || payload.Login == "" {
		return nil, errors.New("GitHub profile could not be verified")
	}
	return &model.GitHubIdentity{ID: payload.ID, Login: payload.Login, Name: payload.Name, AvatarURL: payload.AvatarURL, ProfileURL: payload.HTMLURL, ConnectedAt: s.now()}, nil
}

func (s *accountService) attachGitHubIdentity(ctx context.Context, identity *model.GitHubIdentity, credentialRef string) error {
	state, err := s.authenticatedState(ctx)
	if err != nil {
		return err
	}
	state.Profile.GitHub = identity
	state.Profile.UpdatedAt = s.now()
	state.GitHubCredentialRef = credentialRef
	return s.store.Save(ctx, state)
}

func (s *accountService) finishGitHubFlow(flowID, status string, identity *model.GitHubIdentity, detail string) model.GitHubDeviceFlow {
	s.mu.Lock()
	defer s.mu.Unlock()
	flowState := s.flows[flowID]
	if flowState == nil {
		return model.GitHubDeviceFlow{ID: flowID, Status: "failed", Error: "authorization session was not found"}
	}
	flowState.view.Status = status
	flowState.view.GitHub = identity
	flowState.view.Error = strings.TrimSpace(detail)
	return flowState.view
}

func (s *accountService) authenticatedState(ctx context.Context) (*model.AccountState, error) {
	state, err := s.ensureState(ctx)
	if err != nil {
		return nil, err
	}
	if !state.Authenticated {
		return nil, errors.New("account session is not authenticated")
	}
	return state, nil
}

func (s *accountService) ensureState(ctx context.Context) (*model.AccountState, error) {
	state, err := s.store.Load(ctx)
	if err == nil {
		if s.normalizeAccountState(state) {
			if err := s.store.Save(ctx, state); err != nil {
				return nil, err
			}
		}
		return state, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	state = defaultAccountState(s.now(), s.isDevelopmentRuntime())
	if err := s.store.Save(ctx, state); err != nil {
		return nil, err
	}
	return state, nil
}

func defaultAccountState(now time.Time, development bool) *model.AccountState {
	displayName := strings.TrimSpace(os.Getenv("CASCADE_DEV_USER_NAME"))
	if displayName == "" {
		if current, err := user.Current(); err == nil {
			displayName = strings.TrimSpace(current.Name)
		}
	}
	if displayName == "" {
		displayName = "Local User"
	}
	cycleStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	cycleEnd := cycleStart.AddDate(0, 1, 0)
	return &model.AccountState{
		Authenticated: development,
		Profile:       model.AccountProfile{ID: "user_local", DisplayName: displayName, Email: strings.TrimSpace(os.Getenv("CASCADE_DEV_USER_EMAIL")), Initials: accountInitials(displayName), EmailVerification: model.AccountVerificationUnverified, UpdatedAt: now},
		Plan:          model.AccountPlan{ID: "plan_local_pro", Name: "Pro", Status: "active", CreditsIncluded: 1000, CreditsUsed: 360, CreditsRemaining: 640, CycleStart: cycleStart, CycleEnd: cycleEnd, Usage: []model.AccountUsageCategory{{Category: "research", Credits: 80}, {Category: "browser", Credits: 160}, {Category: "video", Credits: 120}}},
	}
}

func (s *accountService) publicProfile(state *model.AccountState) model.AccountProfile {
	profile := state.Profile
	// Phone fields remain readable in private state for backwards compatibility,
	// but the email-only account API no longer exposes them.
	profile.Phone = ""
	profile.PhoneVerification = ""
	profile.HasPassword = strings.TrimSpace(state.PasswordHash) != ""
	return profile
}

func (s *accountService) sessionView(state *model.AccountState) model.AccountSession {
	result := model.AccountSession{
		Authenticated:       state.Authenticated,
		DevLoginAvailable:   s.isDevelopmentRuntime(),
		PasswordSetupNeeded: strings.TrimSpace(state.PasswordHash) == "",
	}
	if state.Authenticated {
		profile := s.publicProfile(state)
		result.User = &profile
	}
	return result
}

func (s *accountService) normalizeAccountState(state *model.AccountState) bool {
	changed := false
	if state.VerificationChallenges == nil {
		state.VerificationChallenges = map[string]model.AccountVerificationChallenge{}
		changed = true
	}
	if state.VerificationSends == nil {
		state.VerificationSends = map[string][]time.Time{}
		changed = true
	}
	for _, channel := range []string{"email", "phone"} {
		legacy, ok := state.VerificationChallenges[channel]
		if !ok {
			continue
		}
		legacy.Purpose = verificationPurposeContact
		state.VerificationChallenges[verificationChallengeKey(verificationPurposeContact, channel)] = legacy
		delete(state.VerificationChallenges, channel)
		changed = true
	}
	hasPassword := strings.TrimSpace(state.PasswordHash) != ""
	if state.Profile.HasPassword != hasPassword {
		state.Profile.HasPassword = hasPassword
		changed = true
	}
	if !s.isDevelopmentRuntime() && !hasPassword && state.Authenticated {
		state.Authenticated = false
		changed = true
	}
	return changed
}

func accountInitials(displayName string) string {
	parts := strings.Fields(displayName)
	if len(parts) == 0 {
		return "CU"
	}
	initials := string([]rune(parts[0])[:1])
	if len(parts) > 1 {
		last := []rune(parts[len(parts)-1])
		if len(last) > 0 {
			initials += string(last[:1])
		}
	}
	return strings.ToUpper(initials)
}

func randomAccountID(prefix string) string {
	value := make([]byte, 8)
	if _, err := rand.Read(value); err != nil {
		return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
	}
	return prefix + "_" + hex.EncodeToString(value)
}
