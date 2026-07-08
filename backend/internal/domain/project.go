package domain

import "time"

type ProjectStatus string

const (
	ProjectStatusCreated      ProjectStatus = "created"
	ProjectStatusContextReady ProjectStatus = "context_ready"
	ProjectStatusGraphReady   ProjectStatus = "graph_ready"
	ProjectStatusRehearsing   ProjectStatus = "rehearsing"
	ProjectStatusAssetsReady  ProjectStatus = "assets_ready"
	ProjectStatusBlocked      ProjectStatus = "blocked"
)

type Project struct {
	ID          string        `json:"id"`
	TenantID    string        `json:"tenantId"`
	ProductName string        `json:"productName"`
	Status      ProjectStatus `json:"status"`
	CreatedBy   string        `json:"createdBy"`
	CreatedAt   time.Time     `json:"createdAt"`
	UpdatedAt   time.Time     `json:"updatedAt"`
}