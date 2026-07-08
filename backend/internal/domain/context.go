package domain

type SecretRef struct {
	Provider string `json:"provider"`
	Ref      string `json:"ref"`
	Scope    string `json:"scope,omitempty"`
}

type FrontendAccess struct {
	URL                 string     `json:"url"`
	Environment         string     `json:"environment"`
	LoginMethod         string     `json:"loginMethod"`
	CredentialSecretRef *SecretRef `json:"credentialSecretRef,omitempty"`
	StartPath           string     `json:"startPath,omitempty"`
}

type GitHubAccess struct {
	InstallationID string `json:"installationId,omitempty"`
	Owner          string `json:"owner"`
	Repo           string `json:"repo"`
	Branch         string `json:"branch"`
	CommitSHA      string `json:"commitSha,omitempty"`
	Permission     string `json:"permission"`
}

type SSHAccess struct {
	Host          string    `json:"host"`
	Port          int       `json:"port"`
	Username      string    `json:"username"`
	AuthSecretRef SecretRef `json:"authSecretRef"`
	AllowedPaths  []string  `json:"allowedPaths"`
	AllowedCmds   []string  `json:"allowedCommands"`
	Permission    string    `json:"permission"`
}

type ProjectContext struct {
	ProjectID string `json:"projectId"`
	TenantID  string `json:"tenantId"`
	Product   struct {
		Name        string         `json:"name"`
		Description string         `json:"description,omitempty"`
		Frontend    FrontendAccess `json:"frontend"`
	} `json:"product"`
	GitHub *GitHubAccess `json:"github,omitempty"`
	SSH    *SSHAccess    `json:"ssh,omitempty"`
	Docs   []struct {
		Title string `json:"title"`
		URI   string `json:"uri"`
	} `json:"docs"`
	Audience struct {
		Primary   string   `json:"primary"`
		Secondary []string `json:"secondary"`
		Intent    string   `json:"intent"`
	} `json:"audience"`
	AssetsRequested []string `json:"assetsRequested"`
	Requirements    struct {
		MustShow       []string `json:"mustShow"`
		MustNotShow    []string `json:"mustNotShow"`
		ForbiddenPages []string `json:"forbiddenPages"`
		ForbiddenData  []string `json:"forbiddenData"`
	} `json:"requirements"`
	Brand struct {
		Tone           string   `json:"tone,omitempty"`
		VisualStyle    string   `json:"visualStyle,omitempty"`
		PreferredTerms []string `json:"preferredTerms"`
		ForbiddenTerms []string `json:"forbiddenTerms"`
	} `json:"brand"`
}