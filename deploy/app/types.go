package app

// BuildDeployRequest is the JSON body for POST /deploy-app and POST /build-deploy.
// Optional fields fall back to orchestrator environment defaults (see deploy/vars.env.example).
type BuildDeployRequest struct {
	GithubRepoEndpoint string `json:"githubRepoEndpoint"`
	ProjectID          string `json:"projectID"`
	GitRef             string `json:"gitRef,omitempty"`
	// Dockerfile path relative to the repository root (Kaniko --dockerfile). Default: KANIKO_DOCKERFILE or "Dockerfile".
	// Ignored when dockerfileContent is set.
	Dockerfile string `json:"dockerfile,omitempty"`
	// DockerfileContent is raw Dockerfile text. When set, it is mounted into the Kaniko pod; git context is unchanged.
	// Mutually exclusive with "dockerfile" path in the same request.
	DockerfileContent string `json:"dockerfileContent,omitempty"`
	// ContainerPort is the port your process listens on inside the container (Deployment containerPort / Service targetPort).
	// 0 = use APP_CONTAINER_PORT from the orchestrator environment.
	ContainerPort int `json:"containerPort,omitempty"`
	// ServicePort is the Kubernetes Service spec.ports[].port (what Ingress/HTTPRoute targets).
	// 0 = use K8S_SERVICE_PORT from the orchestrator environment.
	ServicePort int `json:"servicePort,omitempty"`
}

// BuildDeployStreamLine is one NDJSON line streamed to the client.
type BuildDeployStreamLine struct {
	Phase     string `json:"phase,omitempty"`
	Success   *bool  `json:"success,omitempty"`
	PublicURL string `json:"publicURL,omitempty"`
	Error     string `json:"error,omitempty"`
}
