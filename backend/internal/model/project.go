package model

type AppMode string

const (
	AppModeWeb     AppMode = "web"
	AppModeDesktop AppMode = "desktop"
)

// ProjectContext contains the full MVP input contract. It supports both
// cloud/web mode and local desktop mode without changing downstream graph logic.
type ProjectContext struct {
	ID                 string       `json:"id"`
	Mode               AppMode      `json:"mode"`
	ProductURL         string       `json:"product_url"`
	DemoAccount        *DemoAccount `json:"demo_account,omitempty"`
	GitRepoURL         string       `json:"git_repo_url,omitempty"`         // web mode input
	LocalRepoPath      string       `json:"local_repo_path,omitempty"`      // desktop mode input
	ProductDescription string       `json:"product_description,omitempty"`
	TargetAudience     string       `json:"target_audience"`
	BrandTone          string       `json:"brand_tone,omitempty"`
	MustShow           []string     `json:"must_show"`
	MustNotShow        []string     `json:"must_not_show"`
	ForbiddenPages     []string     `json:"forbidden_pages"`
	ForbiddenData      []string     `json:"forbidden_data"`
}

type DemoAccount struct {
	UsernameSecretRef string `json:"username_secret_ref"`
	PasswordSecretRef string `json:"password_secret_ref"`
}

type ProductMap struct {
	ProjectID string         `json:"project_id"`
	Pages     []*ProductPage `json:"pages"`
	Features  []*Feature     `json:"features"`
	Summary   string         `json:"summary,omitempty"`
}

type ProductPage struct {
	URL            string   `json:"url"`
	Title          string   `json:"title,omitempty"`
	Purpose        string   `json:"purpose,omitempty"`
	Actions        []string `json:"actions"`
	DemoValueScore float64  `json:"demo_value_score"`
}

type Feature struct {
	Name         string   `json:"name"`
	UserValue    string   `json:"user_value"`
	BestAudience []string `json:"best_audience"`
	EvidenceIDs  []string `json:"evidence_ids,omitempty"`
}