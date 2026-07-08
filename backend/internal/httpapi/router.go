package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"cascade-demoops/backend/internal/repository"
	"cascade-demoops/backend/internal/service"
)

type API struct {
	services service.Container
	logger   *slog.Logger
}

func NewRouter(services service.Container, logger *slog.Logger) http.Handler {
	api := &API{services: services, logger: logger}
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", api.health)
	mux.HandleFunc("POST /v1/projects", api.createProject)
	mux.HandleFunc("GET /v1/projects/{projectId}", api.getProject)
	mux.HandleFunc("POST /v1/projects/{projectId}/context", api.saveProjectContext)

	return api.withRecover(api.withJSON(mux))
}

func (api *API) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "service": "cascade-api-go"})
}

func (api *API) createProject(w http.ResponseWriter, r *http.Request) {
	var input service.CreateProjectInput
	if !decodeJSON(w, r, &input) {
		return
	}
	project, err := api.services.Projects.CreateProject(r.Context(), input)
	if err != nil {
		api.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, project)
}

func (api *API) getProject(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("projectId")
	project, err := api.services.Projects.GetProject(r.Context(), projectID)
	if err != nil {
		api.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, project)
}

func (api *API) saveProjectContext(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("projectId")
	var input service.SaveProjectContextInput
	if !decodeJSON(w, r, &input) {
		return
	}
	result, err := api.services.Projects.SaveProjectContext(r.Context(), projectID, input)
	if err != nil {
		api.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

func (api *API) writeError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	code := "internal_error"
	if errors.Is(err, repository.ErrNotFound) {
		status = http.StatusNotFound
		code = "not_found"
	} else if service.IsValidationError(err) {
		status = http.StatusBadRequest
		code = "validation_error"
	}
	if status >= 500 {
		api.logger.Error("request failed", "error", err)
	}
	writeJSON(w, status, map[string]any{"error": map[string]any{"code": code, "message": err.Error()}})
}

func (api *API) withJSON(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		next.ServeHTTP(w, r)
	})
}

func (api *API) withRecover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				api.logger.Error("panic recovered", "panic", recovered)
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"code": "panic", "message": "internal server error"}})
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, out any) bool {
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"code": "invalid_json", "message": err.Error()}})
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}