package app

// BuildDeployRequest is the JSON body for POST /build-deploy.
type BuildDeployRequest struct {
	GithubRepoEndpoint string `json:"githubRepoEndpoint"`
	ProjectID          string `json:"projectID"`
	GitRef             string `json:"gitRef,omitempty"`
}

// BuildDeployStreamLine is one NDJSON line streamed to the client.
type BuildDeployStreamLine struct {
	Phase     string `json:"phase,omitempty"`
	Success   *bool  `json:"success,omitempty"`
	PublicURL string `json:"publicURL,omitempty"`
	Error     string `json:"error,omitempty"`
}
