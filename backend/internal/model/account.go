package model

import "time"

type AccountVerificationStatus string

const (
	AccountVerificationUnverified AccountVerificationStatus = "unverified"
	AccountVerificationPending    AccountVerificationStatus = "pending"
	AccountVerificationVerified   AccountVerificationStatus = "verified"
)

type GitHubIdentity struct {
	ID          int64     `json:"id"`
	Login       string    `json:"login"`
	Name        string    `json:"name,omitempty"`
	AvatarURL   string    `json:"avatar_url,omitempty"`
	ProfileURL  string    `json:"profile_url,omitempty"`
	ConnectedAt time.Time `json:"connected_at"`
}

type AccountProfile struct {
	ID                string                    `json:"id"`
	DisplayName       string                    `json:"display_name"`
	Email             string                    `json:"email,omitempty"`
	Phone             string                    `json:"phone,omitempty"`
	AvatarURL         string                    `json:"avatar_url,omitempty"`
	Initials          string                    `json:"initials"`
	EmailVerification AccountVerificationStatus `json:"email_verification"`
	PhoneVerification AccountVerificationStatus `json:"phone_verification,omitempty"`
	GitHub            *GitHubIdentity           `json:"github,omitempty"`
	HasPassword       bool                      `json:"has_password"`
	UpdatedAt         time.Time                 `json:"updated_at"`
}

type AccountUsageCategory struct {
	Category string `json:"category"`
	Credits  int    `json:"credits"`
}

type AccountPlan struct {
	ID               string                 `json:"id"`
	Name             string                 `json:"name"`
	Status           string                 `json:"status"`
	CreditsIncluded  int                    `json:"credits_included"`
	CreditsUsed      int                    `json:"credits_used"`
	CreditsRemaining int                    `json:"credits_remaining"`
	CycleStart       time.Time              `json:"cycle_start"`
	CycleEnd         time.Time              `json:"cycle_end"`
	Usage            []AccountUsageCategory `json:"usage"`
}

type AccountSession struct {
	Authenticated       bool            `json:"authenticated"`
	User                *AccountProfile `json:"user,omitempty"`
	DevLoginAvailable   bool            `json:"dev_login_available"`
	PasswordSetupNeeded bool            `json:"password_setup_required"`
}

// AccountState is persisted locally. CredentialRef is never serialized in an
// API response; handlers return the public Profile and Plan values instead.
type AccountState struct {
	Authenticated          bool                                    `json:"authenticated"`
	Profile                AccountProfile                          `json:"profile"`
	Plan                   AccountPlan                             `json:"plan"`
	PasswordHash           string                                  `json:"password_hash,omitempty"`
	GitHubCredentialRef    string                                  `json:"github_credential_ref,omitempty"`
	VerificationChallenges map[string]AccountVerificationChallenge `json:"verification_challenges,omitempty"`
	VerificationSends      map[string][]time.Time                  `json:"verification_sends,omitempty"`
	LoginFailures          AccountLoginFailures                    `json:"login_failures,omitempty"`
}

type AccountLoginFailures struct {
	Attempts    int       `json:"attempts,omitempty"`
	WindowStart time.Time `json:"window_start,omitempty"`
	LockedUntil time.Time `json:"locked_until,omitempty"`
}

// AccountVerificationChallenge is private persisted state and is never returned
// through the account profile endpoints. Verification codes are stored only as
// keyed hashes, with bounded lifetime and attempts.
type AccountVerificationChallenge struct {
	UserID      string    `json:"user_id"`
	Purpose     string    `json:"purpose,omitempty"`
	Channel     string    `json:"channel"`
	Destination string    `json:"destination"`
	CodeHash    string    `json:"code_hash"`
	ExpiresAt   time.Time `json:"expires_at"`
	ResendAt    time.Time `json:"resend_at"`
	Attempts    int       `json:"attempts"`
	CreatedAt   time.Time `json:"created_at"`
}

type AccountVerificationState struct {
	Channel           string                    `json:"channel"`
	Status            AccountVerificationStatus `json:"status"`
	MaskedDestination string                    `json:"masked_destination,omitempty"`
	ExpiresAt         *time.Time                `json:"expires_at,omitempty"`
	ResendAt          *time.Time                `json:"resend_at,omitempty"`
}

type AccountVerificationStart struct {
	Channel           string    `json:"channel"`
	MaskedDestination string    `json:"masked_destination"`
	Status            string    `json:"status"`
	ExpiresAt         time.Time `json:"expires_at"`
	ResendAt          time.Time `json:"resend_at"`
	DevelopmentCode   string    `json:"development_code,omitempty"`
}

type GitHubDeviceFlow struct {
	ID              string          `json:"id"`
	Status          string          `json:"status"`
	UserCode        string          `json:"user_code"`
	VerificationURI string          `json:"verification_uri"`
	ExpiresAt       time.Time       `json:"expires_at"`
	IntervalSeconds int             `json:"interval_seconds"`
	GitHub          *GitHubIdentity `json:"github,omitempty"`
	Error           string          `json:"error,omitempty"`
}
