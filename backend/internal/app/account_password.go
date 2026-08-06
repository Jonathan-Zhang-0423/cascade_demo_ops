package app

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"cascade-demoops/backend/internal/model"
	"golang.org/x/crypto/argon2"
)

const (
	accountPasswordMinLength = 12
	accountPasswordMaxLength = 128
	loginFailureLimit        = 5
	loginFailureWindow       = 15 * time.Minute
	loginLockDuration        = 15 * time.Minute
	argonMemoryKiB           = 64 * 1024
	argonIterations          = 3
	argonParallelism         = 2
	argonSaltLength          = 16
	argonKeyLength           = 32
)

func (s *accountService) passwordLogin(ctx context.Context, identifier, password string) (model.AccountSession, error) {
	state, err := s.ensureState(ctx)
	if err != nil {
		return model.AccountSession{}, err
	}
	now := s.now()
	if state.LoginFailures.LockedUntil.After(now) {
		seconds := max(1, int(state.LoginFailures.LockedUntil.Sub(now).Seconds()))
		return s.sessionView(state), verificationError("account_login_rate_limited", fmt.Sprintf("too many sign-in attempts; try again in %d seconds", seconds), true)
	}
	identifier = strings.TrimSpace(identifier)
	identifierMatches := strings.EqualFold(identifier, strings.TrimSpace(state.Profile.DisplayName)) ||
		(state.Profile.EmailVerification == model.AccountVerificationVerified && strings.EqualFold(identifier, strings.TrimSpace(state.Profile.Email)))
	passwordMatches := false
	if strings.TrimSpace(state.PasswordHash) != "" {
		passwordMatches, _ = s.verifyPassword(state.PasswordHash, password)
	}
	if !identifierMatches || !passwordMatches {
		s.recordLoginFailure(state, now)
		_ = s.store.Save(ctx, state)
		if state.LoginFailures.LockedUntil.After(now) {
			return s.sessionView(state), verificationError("account_login_rate_limited", "too many sign-in attempts; try again in 15 minutes", true)
		}
		return s.sessionView(state), verificationError("account_login_invalid", "email, username, or password is incorrect", false)
	}
	state.Authenticated = true
	state.LoginFailures = model.AccountLoginFailures{}
	if err := s.store.Save(ctx, state); err != nil {
		return model.AccountSession{}, err
	}
	return s.sessionView(state), nil
}

func (s *accountService) setPassword(ctx context.Context, password string) (model.AccountSession, error) {
	state, err := s.ensureState(ctx)
	if err != nil {
		return model.AccountSession{}, err
	}
	if strings.TrimSpace(state.PasswordHash) != "" {
		if !state.Authenticated {
			return s.sessionView(state), errors.New("account session is not authenticated")
		}
		return s.sessionView(state), verificationError("account_password_exists", "a password is already configured", false)
	}
	return s.replacePassword(ctx, state, password)
}

func (s *accountService) changePassword(ctx context.Context, currentPassword, newPassword string) (model.AccountSession, error) {
	state, err := s.authenticatedState(ctx)
	if err != nil {
		return model.AccountSession{}, err
	}
	if strings.TrimSpace(state.PasswordHash) == "" {
		return s.sessionView(state), verificationError("account_password_not_set", "no password is configured", false)
	}
	matches, err := s.verifyPassword(state.PasswordHash, currentPassword)
	if err != nil || !matches {
		return s.sessionView(state), verificationError("account_current_password_invalid", "current password is incorrect", false)
	}
	if matches, _ := s.verifyPassword(state.PasswordHash, newPassword); matches {
		return s.sessionView(state), verificationError("account_password_reused", "new password must be different from the current password", false)
	}
	return s.replacePassword(ctx, state, newPassword)
}

func (s *accountService) replacePassword(ctx context.Context, state *model.AccountState, password string) (model.AccountSession, error) {
	if err := validateAccountPassword(password); err != nil {
		return s.sessionView(state), err
	}
	hash, err := s.hashPassword(password)
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

func (s *accountService) recordLoginFailure(state *model.AccountState, now time.Time) {
	if state.LoginFailures.WindowStart.IsZero() || now.Sub(state.LoginFailures.WindowStart) >= loginFailureWindow {
		state.LoginFailures = model.AccountLoginFailures{Attempts: 1, WindowStart: now}
		return
	}
	state.LoginFailures.Attempts++
	if state.LoginFailures.Attempts >= loginFailureLimit {
		state.LoginFailures.LockedUntil = now.Add(loginLockDuration)
	}
}

func validateAccountPassword(password string) error {
	length := utf8.RuneCountInString(password)
	if length < accountPasswordMinLength || length > accountPasswordMaxLength {
		return verificationError("account_password_policy", "password must contain 12 to 128 characters", false)
	}
	return nil
}

func hashAccountPassword(password string) (string, error) {
	if err := validateAccountPassword(password); err != nil {
		return "", err
	}
	salt := make([]byte, argonSaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, argonIterations, argonMemoryKiB, argonParallelism, argonKeyLength)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, argonMemoryKiB, argonIterations, argonParallelism, base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

func verifyAccountPassword(encodedHash, password string) (bool, error) {
	var version int
	var memory, iterations uint32
	var parallelism uint8
	parts := strings.Split(encodedHash, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errors.New("password hash has an unsupported format")
	}
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, errors.New("password hash has an unsupported version")
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &parallelism); err != nil {
		return false, errors.New("password hash parameters are invalid")
	}
	if memory < 8*1024 || memory > 256*1024 || iterations < 1 || iterations > 10 || parallelism < 1 || parallelism > 8 {
		return false, errors.New("password hash parameters are outside supported limits")
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) < 8 || len(salt) > 64 {
		return false, errors.New("password hash salt is invalid")
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(expected) < 16 || len(expected) > 64 {
		return false, errors.New("password hash digest is invalid")
	}
	actual := argon2.IDKey([]byte(password), salt, iterations, memory, parallelism, uint32(len(expected)))
	return subtle.ConstantTimeCompare(expected, actual) == 1, nil
}
