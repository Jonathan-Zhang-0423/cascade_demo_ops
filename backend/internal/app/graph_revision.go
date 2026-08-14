package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"cascade-demoops/backend/internal/model"
	"cascade-demoops/backend/internal/orchestrator"
)

type GraphNodeRevision struct {
	NodeID       string `json:"node_id"`
	IsScreenshot *bool  `json:"is_screenshot,omitempty"`
	HasZoom      *bool  `json:"has_zoom,omitempty"`
}

type GraphRevisionRequest struct {
	BaseGraphDigestSHA256 string              `json:"base_graph_digest_sha256"`
	Patches               []GraphNodeRevision `json:"patches"`
	IdempotencyKey        string              `json:"idempotency_key"`
	OrgID                 string              `json:"org_id,omitempty"`
}

type GraphRevisionResult struct {
	State                    *orchestrator.CascadeState  `json:"state"`
	Build                    ClientExecutionPackageBuild `json:"build"`
	GraphDigestSHA256        string                      `json:"graph_digest_sha256"`
	ApprovalSubjectDigest    string                      `json:"approval_subject_digest_sha256"`
	ConfidenceAssessmentHash string                      `json:"confidence_assessment_hash"`
}

type graphRevisionCacheEntry struct {
	RequestDigest string
	Result        GraphRevisionResult
}

func (s *Service) ReviseWorkflowGraph(ctx context.Context, projectID string, request GraphRevisionRequest) (GraphRevisionResult, error) {
	projectID = strings.TrimSpace(projectID)
	request.IdempotencyKey = strings.TrimSpace(request.IdempotencyKey)
	if projectID == "" || request.IdempotencyKey == "" || strings.TrimSpace(request.BaseGraphDigestSHA256) == "" || len(request.Patches) == 0 {
		return GraphRevisionResult{}, errors.New("graph revision requires project_id, base_graph_digest_sha256, patches, and idempotency_key")
	}
	cacheKey := projectID + "|" + request.IdempotencyKey
	requestDigest, err := model.DigestCanonicalJSON(request)
	if err != nil {
		return GraphRevisionResult{}, err
	}
	s.graphRevisionMu.Lock()
	defer s.graphRevisionMu.Unlock()
	if cached, ok := s.graphRevisions[cacheKey]; ok {
		if cached.RequestDigest != requestDigest {
			return GraphRevisionResult{}, errors.New("graph revision idempotency key was reused with different input")
		}
		return cached.Result, nil
	}
	state, err := s.states.Load(ctx, projectID)
	if err != nil {
		return GraphRevisionResult{}, err
	}
	if state.WorkflowGraph == nil {
		return GraphRevisionResult{}, errors.New("workflow graph is missing")
	}
	state, err = cloneCascadeStateForRevision(state)
	if err != nil {
		return GraphRevisionResult{}, err
	}
	currentDigest, err := model.DigestCanonicalJSON(state.WorkflowGraph)
	if err != nil {
		return GraphRevisionResult{}, err
	}
	if currentDigest != strings.TrimSpace(request.BaseGraphDigestSHA256) {
		return GraphRevisionResult{}, &packagePreviewStaleError{}
	}
	graph, err := cloneWorkflowGraphForPackage(state.WorkflowGraph)
	if err != nil {
		return GraphRevisionResult{}, err
	}
	nodes := map[string]*model.GraphNode{}
	for _, node := range graph.Nodes {
		if node != nil {
			nodes[node.ID] = node
		}
	}
	seen := map[string]bool{}
	for _, patch := range request.Patches {
		patch.NodeID = strings.TrimSpace(patch.NodeID)
		node := nodes[patch.NodeID]
		if patch.NodeID == "" || node == nil || seen[patch.NodeID] {
			return GraphRevisionResult{}, errors.New("graph revision contains an unknown or duplicate node_id")
		}
		seen[patch.NodeID] = true
		if patch.IsScreenshot == nil && patch.HasZoom == nil {
			return GraphRevisionResult{}, errors.New("graph revision patch has no allowed fields")
		}
		if patch.IsScreenshot != nil {
			node.IsScreenshot = *patch.IsScreenshot
			if node.Capture != nil {
				node.Capture.Screenshot = *patch.IsScreenshot
			}
		}
		if patch.HasZoom != nil {
			node.HasZoom = *patch.HasZoom
			if node.Capture != nil {
				node.Capture.Zoom = *patch.HasZoom
			}
		}
	}
	graph.Version++
	graph.Status = model.GraphStatusReviewReady
	graph.UpdatedAt = time.Now().UTC()
	next, err := s.flow.RepackageReviewedGraph(ctx, state, graph)
	if err != nil {
		return GraphRevisionResult{}, err
	}
	build, err := buildClientExecutionPackageFromState(next, firstNonEmptyString(request.OrgID, defaultDesktopOrgID), time.Now().UTC())
	if err != nil {
		return GraphRevisionResult{}, err
	}
	if err := s.states.Save(ctx, next); err != nil {
		return GraphRevisionResult{}, err
	}
	graphDigest, err := model.DigestCanonicalJSON(next.WorkflowGraph)
	if err != nil {
		return GraphRevisionResult{}, err
	}
	result := GraphRevisionResult{State: next, Build: build, GraphDigestSHA256: graphDigest, ApprovalSubjectDigest: build.ApprovalSubjectDigestSHA256}
	if build.Package.ConfidenceSummary != nil {
		result.ConfidenceAssessmentHash = build.Package.ConfidenceSummary.AssessmentHash
	}
	s.approvalMu.Lock()
	for key := range s.approvedBuilds {
		if strings.HasPrefix(key, projectID+"|") {
			delete(s.approvedBuilds, key)
		}
	}
	s.approvalMu.Unlock()
	s.graphRevisions[cacheKey] = graphRevisionCacheEntry{RequestDigest: requestDigest, Result: result}
	return result, nil
}

func cloneCascadeStateForRevision(state *orchestrator.CascadeState) (*orchestrator.CascadeState, error) {
	if state == nil {
		return nil, errors.New("cascade state is nil")
	}
	data, err := model.CanonicalJSON(state)
	if err != nil {
		return nil, err
	}
	var out orchestrator.CascadeState
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
