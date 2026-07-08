package agents

import (
	"context"

	"cascade-demoops/backend/internal/model"
	"cascade-demoops/backend/internal/orchestrator"
)

type QAExecutorAgent struct{}

func NewQAExecutorAgent() *QAExecutorAgent { return &QAExecutorAgent{} }

func (a *QAExecutorAgent) ExecuteAndRehearse(ctx context.Context, graph *model.DemoWorkflowGraph) (orchestrator.RehearsalResult, error) {
	if err := ctx.Err(); err != nil {
		return orchestrator.RehearsalResult{}, err
	}
	return orchestrator.RehearsalResult{
		PassRate:       1.0,
		RecordingPaths: []string{"artifacts/rehearsal/recording.webm"},
		ScreenshotRefs: screenshotRefs(graph),
	}, nil
}

func screenshotRefs(graph *model.DemoWorkflowGraph) []string {
	refs := make([]string, 0)
	for _, node := range graph.Nodes {
		if node.IsScreenshot || (node.Capture != nil && node.Capture.Screenshot) {
			refs = append(refs, "screenshots/"+node.ID+".png")
		}
	}
	return refs
}
