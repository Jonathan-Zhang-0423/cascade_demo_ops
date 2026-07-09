package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"cascade-demoops/backend/internal/model"
	"cascade-demoops/backend/internal/orchestrator"
)

type DevHTTPServer struct {
	service *Service
}

type ExecutionPackageRequest struct {
	UserInput *orchestrator.UserInput `json:"user_input,omitempty"`
}

func NewDevHTTPServer(service *Service) *DevHTTPServer {
	return &DevHTTPServer{service: service}
}

func (s *DevHTTPServer) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/desktop/runtime-health", s.handleRuntimeHealth)
	mux.HandleFunc("GET /v1/desktop/model-diagnostics", s.handleModelDiagnostics)
	mux.HandleFunc("POST /v1/desktop/model-diagnostics", s.handleModelDiagnostics)
	mux.HandleFunc("POST /v1/desktop/projects", s.handleCreateProject)
	mux.HandleFunc("/v1/desktop/projects/", s.handleProjectRoute)
	return withDevCORS(mux)
}

func (s *DevHTTPServer) handleRuntimeHealth(w http.ResponseWriter, r *http.Request) {
	writeBridgeValue(w, NewRuntimeConfigView(s.service.RuntimeConfig()), nil)
}

func (s *DevHTTPServer) handleModelDiagnostics(w http.ResponseWriter, r *http.Request) {
	writeBridgeValue(w, s.service.DiagnoseModels(r.Context()), nil)
}

func (s *DevHTTPServer) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	var input orchestrator.UserInput
	if err := decodeJSON(r, &input); err != nil {
		writeBridgeValue(w, nil, err)
		return
	}
	state, err := s.service.CreateProject(r.Context(), input)
	writeBridgeValue(w, state, err)
}

func (s *DevHTTPServer) handleProjectRoute(w http.ResponseWriter, r *http.Request) {
	projectID, suffix, ok := splitProjectRoute(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	switch {
	case r.Method == http.MethodGet && suffix == "":
		state, err := s.service.LoadProject(r.Context(), projectID)
		writeBridgeValue(w, state, err)
	case r.Method == http.MethodPost && suffix == "/inputs":
		var inputs model.ProjectInputBundle
		if err := decodeJSON(r, &inputs); err != nil {
			writeBridgeValue(w, nil, err)
			return
		}
		projectContext, err := s.service.SaveProjectInput(r.Context(), projectID, inputs)
		writeBridgeValue(w, projectContext, err)
	case r.Method == http.MethodPost && suffix == "/execution-package":
		var request ExecutionPackageRequest
		if r.Body != nil && r.ContentLength != 0 {
			if err := decodeJSON(r, &request); err != nil {
				writeBridgeValue(w, nil, err)
				return
			}
		}
		var (
			state *orchestrator.CascadeState
			err   error
		)
		if request.UserInput != nil {
			state, err = s.service.GenerateExecutionPackage(r.Context(), *request.UserInput)
		} else {
			state, err = s.service.RegenerateExecutionPackage(r.Context(), projectID)
		}
		writeBridgeValue(w, state, err)
	case r.Method == http.MethodGet && suffix == "/execution-package":
		state, err := s.service.LoadProject(r.Context(), projectID)
		writeBridgeValue(w, state, err)
	default:
		http.NotFound(w, r)
	}
}

func decodeJSON(r *http.Request, target any) error {
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return nil
}

func writeBridgeValue(w http.ResponseWriter, value any, err error) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	status := http.StatusOK
	response := BridgeResponse{OK: true}
	if err != nil {
		status = http.StatusBadRequest
		response = BridgeResponse{OK: false, Error: err.Error()}
	} else if value != nil {
		data, marshalErr := json.Marshal(value)
		if marshalErr != nil {
			status = http.StatusInternalServerError
			response = BridgeResponse{OK: false, Error: marshalErr.Error()}
		} else {
			response.Data = data
		}
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(response)
}

func splitProjectRoute(path string) (string, string, bool) {
	const prefix = "/v1/desktop/projects/"
	if !strings.HasPrefix(path, prefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(path, prefix)
	if rest == "" {
		return "", "", false
	}
	parts := strings.SplitN(rest, "/", 2)
	projectID := strings.TrimSpace(parts[0])
	if projectID == "" {
		return "", "", false
	}
	if len(parts) == 1 {
		return projectID, "", true
	}
	return projectID, "/" + strings.TrimRight(parts[1], "/"), true
}

func withDevCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if isAllowedDevOrigin(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isAllowedDevOrigin(origin string) bool {
	if origin == "" {
		return true
	}
	return strings.HasPrefix(origin, "http://127.0.0.1:") || strings.HasPrefix(origin, "http://localhost:")
}

func EnsureLocalDevAddress(addr string) error {
	if strings.HasPrefix(addr, "127.0.0.1:") || strings.HasPrefix(addr, "localhost:") {
		return nil
	}
	if strings.HasPrefix(addr, ":") {
		return errors.New("dev bridge must bind to 127.0.0.1 or localhost")
	}
	return errors.New("dev bridge refuses non-local bind address")
}
