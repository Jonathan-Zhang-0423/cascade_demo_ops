package app

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"cascade-demoops/backend/internal/model"
	"cascade-demoops/backend/internal/orchestrator"
)

type DevHTTPServer struct {
	service *Service
	events  *devEventStore
}

type ExecutionPackageRequest struct {
	UserInput *orchestrator.UserInput `json:"user_input,omitempty"`
}

type DevExecutionEvent struct {
	ID        int64                      `json:"id"`
	ProjectID string                     `json:"project_id"`
	Level     orchestrator.ProgressLevel `json:"level"`
	Node      orchestrator.NodeName      `json:"node,omitempty"`
	Message   string                     `json:"message"`
	Detail    string                     `json:"detail,omitempty"`
	ElapsedMS int64                      `json:"elapsed_ms,omitempty"`
	CreatedAt time.Time                  `json:"created_at"`
}

func NewDevHTTPServer(service *Service) *DevHTTPServer {
	return &DevHTTPServer{service: service, events: newDevEventStore()}
}

func (s *DevHTTPServer) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/desktop/runtime-health", s.handleRuntimeHealth)
	mux.HandleFunc("GET /v1/desktop/model-diagnostics", s.handleModelDiagnostics)
	mux.HandleFunc("POST /v1/desktop/model-diagnostics", s.handleModelDiagnostics)
	mux.HandleFunc("POST /v1/desktop/projects", s.handleCreateProject)
	mux.HandleFunc("/v1/desktop/projects/", s.handleProjectRoute)
	s.registerDevExchangeRoutes(mux)
	return withDevLogging(withDevCORS(mux))
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
		startedAt := time.Now()
		log.Printf("dev_bridge execution_package_start project_id=%s", projectID)
		s.emitProjectEvent(projectID, orchestrator.ProgressEvent{
			Level:   orchestrator.ProgressLevelInfo,
			Message: "Dev Bridge 已收到执行包生成请求",
			Detail:  "本地后端开始调度 Agent 链路。",
		})
		var request ExecutionPackageRequest
		if r.Body != nil && r.ContentLength != 0 {
			if err := decodeJSON(r, &request); err != nil {
				log.Printf("dev_bridge execution_package_bad_request project_id=%s error=%s", projectID, err)
				s.emitProjectEvent(projectID, orchestrator.ProgressEvent{
					Level:   orchestrator.ProgressLevelError,
					Message: "执行包请求解析失败",
					Detail:  err.Error(),
				})
				writeBridgeValue(w, nil, err)
				return
			}
		}
		ctx := orchestrator.WithProgressSink(r.Context(), func(event orchestrator.ProgressEvent) {
			s.emitProjectEvent(projectID, event)
		})
		var (
			state *orchestrator.CascadeState
			err   error
		)
		if request.UserInput != nil {
			state, err = s.service.GenerateExecutionPackage(ctx, *request.UserInput)
		} else {
			state, err = s.service.RegenerateExecutionPackage(ctx, projectID)
		}
		if err != nil {
			log.Printf("dev_bridge execution_package_error project_id=%s elapsed_ms=%d error=%s", projectID, time.Since(startedAt).Milliseconds(), err)
			s.emitProjectEvent(projectID, orchestrator.ProgressEvent{
				Level:     orchestrator.ProgressLevelError,
				Message:   "执行包生成失败",
				Detail:    err.Error(),
				ElapsedMS: time.Since(startedAt).Milliseconds(),
			})
		} else if state != nil {
			s.events.CopyProjectEvents(projectID, state.ProjectID)
			log.Printf("dev_bridge execution_package_done project_id=%s generated_project_id=%s node=%s status=%s elapsed_ms=%d", projectID, state.ProjectID, state.CurrentNode, state.Status, time.Since(startedAt).Milliseconds())
			s.emitProjectEvent(projectID, orchestrator.ProgressEvent{
				Level:     orchestrator.ProgressLevelSuccess,
				Node:      state.CurrentNode,
				Message:   "Dev Bridge 已返回执行包",
				Detail:    "前端可以展示中文思路文档、执行计划 JSON 和 TS 脚本。",
				ElapsedMS: time.Since(startedAt).Milliseconds(),
			})
			s.events.CopyProjectEvents(projectID, state.ProjectID)
		}
		writeBridgeValue(w, state, err)
	case r.Method == http.MethodGet && suffix == "/execution-package":
		state, err := s.service.LoadProject(r.Context(), projectID)
		writeBridgeValue(w, state, err)
	case r.Method == http.MethodGet && suffix == "/execution-events":
		afterID, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
		writeBridgeValue(w, s.events.List(projectID, afterID), nil)
	default:
		http.NotFound(w, r)
	}
}

func (s *DevHTTPServer) emitProjectEvent(projectID string, event orchestrator.ProgressEvent) {
	if s.events == nil {
		return
	}
	s.events.Add(projectID, event)
}

func withDevLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startedAt := time.Now()
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		log.Printf("dev_bridge request method=%s path=%s status=%d elapsed_ms=%d", r.Method, r.URL.Path, recorder.status, time.Since(startedAt).Milliseconds())
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
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
		response = BridgeResponse{OK: false, Error: redactBridgeError(err.Error())}
	} else if value != nil {
		data, marshalErr := json.Marshal(value)
		if marshalErr != nil {
			status = http.StatusInternalServerError
			response = BridgeResponse{OK: false, Error: redactBridgeError(marshalErr.Error())}
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
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Cascade-Org-ID")
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
		if devExchangeRemoteBindAllowed() {
			return nil
		}
		return errors.New("dev bridge must bind to 127.0.0.1 or localhost")
	}
	if devExchangeRemoteBindAllowed() {
		return nil
	}
	return errors.New("dev bridge refuses non-local bind address")
}

var (
	windowsPathPattern = regexp.MustCompile(`[A-Za-z]:\\[^:\r\n"]+`)
	unixPathPattern    = regexp.MustCompile(`(/[^:\r\n"\s]+)+`)
)

func redactBridgeError(message string) string {
	message = windowsPathPattern.ReplaceAllString(message, "[local_path]")
	message = unixPathPattern.ReplaceAllString(message, "[local_path]")
	return message
}

type devEventStore struct {
	mu      sync.Mutex
	nextID  int64
	events  map[string][]DevExecutionEvent
	maxSize int
}

func newDevEventStore() *devEventStore {
	return &devEventStore{
		events:  map[string][]DevExecutionEvent{},
		maxSize: 200,
	}
}

func (s *devEventStore) Add(projectID string, event orchestrator.ProgressEvent) DevExecutionEvent {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.nextID++
	stored := DevExecutionEvent{
		ID:        s.nextID,
		ProjectID: projectID,
		Level:     event.Level,
		Node:      event.Node,
		Message:   event.Message,
		Detail:    redactBridgeError(event.Detail),
		ElapsedMS: event.ElapsedMS,
		CreatedAt: time.Now().UTC(),
	}
	if stored.Level == "" {
		stored.Level = orchestrator.ProgressLevelInfo
	}
	s.events[projectID] = append(s.events[projectID], stored)
	if len(s.events[projectID]) > s.maxSize {
		s.events[projectID] = append([]DevExecutionEvent{}, s.events[projectID][len(s.events[projectID])-s.maxSize:]...)
	}
	return stored
}

func (s *devEventStore) List(projectID string, afterID int64) []DevExecutionEvent {
	s.mu.Lock()
	defer s.mu.Unlock()

	events := s.events[projectID]
	result := make([]DevExecutionEvent, 0, len(events))
	for _, event := range events {
		if event.ID > afterID {
			result = append(result, event)
		}
	}
	return result
}

func (s *devEventStore) CopyProjectEvents(fromProjectID string, toProjectID string) {
	if fromProjectID == "" || toProjectID == "" || fromProjectID == toProjectID {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	source := s.events[fromProjectID]
	if len(source) == 0 {
		return
	}
	existingIDs := map[int64]bool{}
	for _, event := range s.events[toProjectID] {
		existingIDs[event.ID] = true
	}
	for _, event := range source {
		if existingIDs[event.ID] {
			continue
		}
		copied := event
		copied.ProjectID = toProjectID
		s.events[toProjectID] = append(s.events[toProjectID], copied)
	}
	if len(s.events[toProjectID]) > s.maxSize {
		s.events[toProjectID] = append([]DevExecutionEvent{}, s.events[toProjectID][len(s.events[toProjectID])-s.maxSize:]...)
	}
}
