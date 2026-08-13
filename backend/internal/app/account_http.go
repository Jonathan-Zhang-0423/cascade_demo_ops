package app

import (
	"errors"
	"net/http"
	"strings"
)

type accountProfileRequest struct {
	DisplayName string `json:"display_name"`
	Email       string `json:"email,omitempty"`
	Phone       string `json:"phone,omitempty"`
}

type accountLoginRequest struct {
	Identifier string `json:"identifier"`
	Password   string `json:"password"`
}

type accountPasswordSetRequest struct {
	Password string `json:"password"`
}

type accountPasswordChangeRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

type accountPasswordResetStartRequest struct {
	Channel     string `json:"channel"`
	Destination string `json:"destination"`
}

type accountPasswordResetConfirmRequest struct {
	Channel     string `json:"channel"`
	Destination string `json:"destination"`
	Code        string `json:"code"`
	NewPassword string `json:"new_password"`
}

type accountVerificationStartRequest struct {
	Destination string `json:"destination"`
}

type accountVerificationConfirmRequest struct {
	Code string `json:"code"`
}

func (s *DevHTTPServer) handleAccountSession(w http.ResponseWriter, r *http.Request) {
	if s == nil || s.service == nil || s.service.accounts == nil {
		writeBridgeValue(w, nil, errors.New("account service is unavailable"))
		return
	}
	value, err := s.service.accounts.session(r.Context())
	writeBridgeValue(w, value, err)
}

func (s *DevHTTPServer) handleAccountDevLogin(w http.ResponseWriter, r *http.Request) {
	if s == nil || s.service == nil || s.service.accounts == nil {
		writeBridgeValue(w, nil, errors.New("account service is unavailable"))
		return
	}
	value, err := s.service.accounts.devLogin(r.Context())
	writeBridgeValue(w, value, err)
}

func (s *DevHTTPServer) handleAccountPasswordLogin(w http.ResponseWriter, r *http.Request) {
	if s == nil || s.service == nil || s.service.accounts == nil {
		writeBridgeValue(w, nil, errors.New("account service is unavailable"))
		return
	}
	var request accountLoginRequest
	if err := decodeJSON(r, &request); err != nil {
		writeBridgeValue(w, nil, err)
		return
	}
	value, err := s.service.accounts.passwordLogin(r.Context(), request.Identifier, request.Password)
	writeBridgeValue(w, value, err)
}

func (s *DevHTTPServer) handleAccountLogout(w http.ResponseWriter, r *http.Request) {
	if s == nil || s.service == nil || s.service.accounts == nil {
		writeBridgeValue(w, nil, errors.New("account service is unavailable"))
		return
	}
	value, err := s.service.accounts.logout(r.Context())
	writeBridgeValue(w, value, err)
}

func (s *DevHTTPServer) handleAccountProfile(w http.ResponseWriter, r *http.Request) {
	if s == nil || s.service == nil || s.service.accounts == nil {
		writeBridgeValue(w, nil, errors.New("account service is unavailable"))
		return
	}
	if r.Method == http.MethodGet {
		value, err := s.service.accounts.profile(r.Context())
		writeBridgeValue(w, value, err)
		return
	}
	if r.Method != http.MethodPut {
		writeBridgeValue(w, nil, errors.New("unsupported account profile method"))
		return
	}
	var request accountProfileRequest
	if err := decodeJSON(r, &request); err != nil {
		writeBridgeValue(w, nil, err)
		return
	}
	value, err := s.service.accounts.updateProfile(r.Context(), accountProfileUpdate{DisplayName: request.DisplayName})
	writeBridgeValue(w, value, err)
}

func (s *DevHTTPServer) handleAccountPasswordSet(w http.ResponseWriter, r *http.Request) {
	if s == nil || s.service == nil || s.service.accounts == nil {
		writeBridgeValue(w, nil, errors.New("account service is unavailable"))
		return
	}
	var request accountPasswordSetRequest
	if err := decodeJSON(r, &request); err != nil {
		writeBridgeValue(w, nil, err)
		return
	}
	value, err := s.service.accounts.setPassword(r.Context(), request.Password)
	writeBridgeValue(w, value, err)
}

func (s *DevHTTPServer) handleAccountPasswordChange(w http.ResponseWriter, r *http.Request) {
	if s == nil || s.service == nil || s.service.accounts == nil {
		writeBridgeValue(w, nil, errors.New("account service is unavailable"))
		return
	}
	var request accountPasswordChangeRequest
	if err := decodeJSON(r, &request); err != nil {
		writeBridgeValue(w, nil, err)
		return
	}
	value, err := s.service.accounts.changePassword(r.Context(), request.CurrentPassword, request.NewPassword)
	writeBridgeValue(w, value, err)
}

func (s *DevHTTPServer) handleAccountPasswordResetStart(w http.ResponseWriter, r *http.Request) {
	if s == nil || s.service == nil || s.service.accounts == nil {
		writeBridgeValue(w, nil, errors.New("account service is unavailable"))
		return
	}
	var request accountPasswordResetStartRequest
	if err := decodeJSON(r, &request); err != nil {
		writeBridgeValue(w, nil, err)
		return
	}
	value, err := s.service.accounts.startPasswordReset(r.Context(), request.Channel, request.Destination)
	writeBridgeValue(w, value, err)
}

func (s *DevHTTPServer) handleAccountPasswordResetConfirm(w http.ResponseWriter, r *http.Request) {
	if s == nil || s.service == nil || s.service.accounts == nil {
		writeBridgeValue(w, nil, errors.New("account service is unavailable"))
		return
	}
	var request accountPasswordResetConfirmRequest
	if err := decodeJSON(r, &request); err != nil {
		writeBridgeValue(w, nil, err)
		return
	}
	value, err := s.service.accounts.confirmPasswordReset(r.Context(), request.Channel, request.Destination, request.Code, request.NewPassword)
	writeBridgeValue(w, value, err)
}

func (s *DevHTTPServer) handleAccountPlan(w http.ResponseWriter, r *http.Request) {
	if s == nil || s.service == nil || s.service.accounts == nil {
		writeBridgeValue(w, nil, errors.New("account service is unavailable"))
		return
	}
	value, err := s.service.accounts.plan(r.Context())
	writeBridgeValue(w, value, err)
}

func (s *DevHTTPServer) handleAccountVerificationStart(w http.ResponseWriter, r *http.Request) {
	if s == nil || s.service == nil || s.service.accounts == nil {
		writeBridgeValue(w, nil, errors.New("account service is unavailable"))
		return
	}
	var request accountVerificationStartRequest
	if err := decodeJSON(r, &request); err != nil {
		writeBridgeValue(w, nil, err)
		return
	}
	value, err := s.service.accounts.startVerification(r.Context(), r.PathValue("channel"), request.Destination)
	writeBridgeValue(w, value, err)
}

func (s *DevHTTPServer) handleAccountVerificationState(w http.ResponseWriter, r *http.Request) {
	if s == nil || s.service == nil || s.service.accounts == nil {
		writeBridgeValue(w, nil, errors.New("account service is unavailable"))
		return
	}
	value, err := s.service.accounts.verificationState(r.Context(), r.PathValue("channel"))
	writeBridgeValue(w, value, err)
}

func (s *DevHTTPServer) handleAccountVerificationConfirm(w http.ResponseWriter, r *http.Request) {
	if s == nil || s.service == nil || s.service.accounts == nil {
		writeBridgeValue(w, nil, errors.New("account service is unavailable"))
		return
	}
	var request accountVerificationConfirmRequest
	if err := decodeJSON(r, &request); err != nil {
		writeBridgeValue(w, nil, err)
		return
	}
	value, err := s.service.accounts.confirmVerification(r.Context(), r.PathValue("channel"), request.Code)
	writeBridgeValue(w, value, err)
}

func (s *DevHTTPServer) handleAccountGitHubDeviceStart(w http.ResponseWriter, r *http.Request) {
	if s == nil || s.service == nil || s.service.accounts == nil {
		writeBridgeValue(w, nil, errors.New("account service is unavailable"))
		return
	}
	value, err := s.service.accounts.startGitHubDevice(r.Context())
	writeBridgeValue(w, value, err)
}

func (s *DevHTTPServer) handleAccountGitHubDevicePoll(w http.ResponseWriter, r *http.Request) {
	if s == nil || s.service == nil || s.service.accounts == nil {
		writeBridgeValue(w, nil, errors.New("account service is unavailable"))
		return
	}
	flowID := strings.TrimSpace(r.PathValue("flowID"))
	if flowID == "" {
		writeBridgeValue(w, nil, errors.New("GitHub authorization flow ID is required"))
		return
	}
	value, err := s.service.accounts.pollGitHubDevice(r.Context(), flowID)
	writeBridgeValue(w, value, err)
}

func (s *DevHTTPServer) handleAccountGitHubDisconnect(w http.ResponseWriter, r *http.Request) {
	if s == nil || s.service == nil || s.service.accounts == nil {
		writeBridgeValue(w, nil, errors.New("account service is unavailable"))
		return
	}
	value, err := s.service.accounts.disconnectGitHub(r.Context())
	writeBridgeValue(w, value, err)
}
