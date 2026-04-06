package app

// AgentContext is the shared state object that flows through all 6 agents
// in the AI pipeline that runs before the Kaniko build step.
type AgentContext struct {
	// ── Input ────────────────────────────────────────────────────────────────
	RepoURL   string
	ProjectID string
	GitRef    string

	// ── AnalyzerAgent output ─────────────────────────────────────────────────
	Language     string   // "python", "node", "go", "java", etc.
	Framework    string   // "fastapi", "express", "gin", "spring", etc.
	EntryPoint   string   // detected start command / main file
	DetectedPort int      // port the app likely listens on
	HasDockerfile bool    // true if a Dockerfile already exists in the repo
	RepoFileList  []string // top-level (and recursive) file paths found in the repo

	// ── BuilderAgent output ──────────────────────────────────────────────────
	GeneratedDockerfile   string // full Dockerfile text produced by AI
	GeneratedDockerignore string // .dockerignore text produced by AI
	DockerfilePath        string // "inline" when AI-generated, else repo-relative path

	// ── SecurityAgent output ─────────────────────────────────────────────────
	SecurityIssues  []SecurityIssue
	SecurityPassed  bool
	SecuritySummary string

	// ── ConfigAgent output ───────────────────────────────────────────────────
	SuggestedCPU    string           // CPU request, e.g. "250m"
	SuggestedMemory string           // Memory request, e.g. "256Mi"
	HealthCheckPath string           // HTTP path for liveness probe, e.g. "/health"
	EnvVars         []EnvVarSuggestion

	// ── Deploy output (populated by existing pipeline after Kaniko) ──────────
	PublicURL string

	// ── MonitorAgent output ──────────────────────────────────────────────────
	MonitorNotes string
}

// SecurityIssue represents a single finding from the SecurityAgent.
type SecurityIssue struct {
	Severity    string // "high", "medium", "low"
	Description string
	File        string
}

// EnvVarSuggestion is one environment variable the ConfigAgent recommends.
type EnvVarSuggestion struct {
	Key         string
	Description string
	Required    bool
}
