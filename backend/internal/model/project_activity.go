package model

import "time"

type ProjectCanvasMode string

const (
	ProjectCanvasModeEmpty   ProjectCanvasMode = "empty"
	ProjectCanvasModeAct     ProjectCanvasMode = "act"
	ProjectCanvasModeBrowser ProjectCanvasMode = "browser"
	ProjectCanvasModeEditor  ProjectCanvasMode = "editor"
)

type ProjectActivityStatus string

const (
	ProjectActivityQueued             ProjectActivityStatus = "queued"
	ProjectActivityRunning            ProjectActivityStatus = "running"
	ProjectActivityWaitingForApproval ProjectActivityStatus = "waiting_for_approval"
	ProjectActivityCompleted          ProjectActivityStatus = "completed"
	ProjectActivityFailed             ProjectActivityStatus = "failed"
	ProjectActivityCanceled           ProjectActivityStatus = "canceled"
)

type ProjectBrowserActivity struct {
	URL      string `json:"url,omitempty"`
	Title    string `json:"title,omitempty"`
	FrameRef string `json:"frame_ref,omitempty"`
	Redacted bool   `json:"redacted"`
}

type ProjectCaptureActivity struct {
	Kind  string `json:"kind"`
	Phase string `json:"phase"`
}

type ProjectActivityEvent struct {
	ID              string                  `json:"id"`
	ProjectID       string                  `json:"project_id"`
	RunID           string                  `json:"run_id"`
	Mode            ProjectCanvasMode       `json:"mode"`
	Kind            string                  `json:"kind"`
	Status          ProjectActivityStatus   `json:"status"`
	Title           string                  `json:"title"`
	Detail          string                  `json:"detail,omitempty"`
	Progress        int                     `json:"progress,omitempty"`
	OccurredAt      time.Time               `json:"occurred_at"`
	Browser         *ProjectBrowserActivity `json:"browser,omitempty"`
	Capture         *ProjectCaptureActivity `json:"capture,omitempty"`
	EditorSessionID string                  `json:"editor_session_id,omitempty"`
	EditorRevision  int                     `json:"editor_revision,omitempty"`
}

type ProjectActivityState struct {
	ProjectID       string                 `json:"project_id"`
	Mode            ProjectCanvasMode      `json:"mode"`
	RunID           string                 `json:"run_id,omitempty"`
	Status          ProjectActivityStatus  `json:"status,omitempty"`
	Current         *ProjectActivityEvent  `json:"current,omitempty"`
	Recent          []ProjectActivityEvent `json:"recent,omitempty"`
	LastEventID     string                 `json:"last_event_id,omitempty"`
	EditorSessionID string                 `json:"editor_session_id,omitempty"`
	UpdatedAt       time.Time              `json:"updated_at"`
}
