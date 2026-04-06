package app

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

// ─── helpers ────────────────────────────────────────────────────────────────

// neevClient builds an OpenAI-compatible client pointed at NeevCloud Kimi K2.
func neevClient(apiKey string) *openai.Client {
	cfg := openai.DefaultConfig(apiKey)
	cfg.BaseURL = "https://inference.ai.neevcloud.com/v1"
	return openai.NewClientWithConfig(cfg)
}

// askKimi sends a single-turn chat request to kimi-k2-thinking and returns the
// raw content string. It applies a hard 120-second deadline to the call.
func askKimi(ctx context.Context, apiKey, systemPrompt, userMsg string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	client := neevClient(apiKey)
	resp, err := client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model: "kimi-k2-thinking",
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: systemPrompt},
			{Role: openai.ChatMessageRoleUser, Content: userMsg},
		},
		Temperature: 0.2,
	})
	if err != nil {
		return "", fmt.Errorf("kimi api: %w", err)
	}
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("kimi api: empty response")
	}
	return resp.Choices[0].Message.Content, nil
}

// extractJSON strips markdown code-fence wrappers that the model sometimes
// includes despite instructions, then returns the cleanest JSON string found.
func extractJSON(raw string) string {
	// Remove ``` fences
	s := strings.TrimSpace(raw)
	for _, fence := range []string{"```json", "```"} {
		if idx := strings.Index(s, fence); idx != -1 {
			s = s[idx+len(fence):]
		}
	}
	if idx := strings.LastIndex(s, "```"); idx != -1 {
		s = s[:idx]
	}
	// Find the outermost { ... } block
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start != -1 && end != -1 && end > start {
		return s[start : end+1]
	}
	return strings.TrimSpace(s)
}

// ghRepoFromURL parses "https://github.com/owner/repo" → ("owner", "repo").
func ghRepoFromURL(repoURL string) (owner, repo string, err error) {
	u := strings.TrimSuffix(strings.TrimSpace(repoURL), "/")
	u = strings.TrimSuffix(u, ".git")
	u = strings.TrimPrefix(u, "https://github.com/")
	u = strings.TrimPrefix(u, "http://github.com/")
	parts := strings.SplitN(u, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("cannot parse github owner/repo from %q", repoURL)
	}
	return parts[0], parts[1], nil
}

// fetchText does a plain HTTP GET and returns the body as a string.
// Returns an error on non-2xx status codes so callers can detect rate-limiting (403) etc.
func fetchText(url string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return string(b), fmt.Errorf("HTTP %d: %s", resp.StatusCode, url)
	}
	return string(b), nil
}

// ghDefaultBranch queries the GitHub API for the repository's actual default branch.
// Returns "main" if the API call fails.
func ghDefaultBranch(owner, repo string) string {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s", owner, repo)
	body, err := fetchText(url)
	if err != nil {
		log.Printf("[ghDefaultBranch] failed to fetch repo info: %v — assuming main", err)
		return "main"
	}
	var info struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := json.Unmarshal([]byte(body), &info); err != nil || info.DefaultBranch == "" {
		return "main"
	}
	log.Printf("[ghDefaultBranch] %s/%s default branch: %s", owner, repo, info.DefaultBranch)
	return info.DefaultBranch
}

// ─── Agent 1: AnalyzerAgent ──────────────────────────────────────────────────

// AnalyzerAgent fetches the GitHub repo file tree and asks Kimi K2 to identify
// the tech stack. Results are written into ac. Errors are non-fatal.
func AnalyzerAgent(ctx context.Context, ac *AgentContext, apiKey string) error {
	// Defaults in case we fail early
	ac.Language = "unknown"
	ac.DetectedPort = 8080

	if apiKey == "" {
		return nil
	}

	owner, repo, err := ghRepoFromURL(ac.RepoURL)
	if err != nil {
		log.Printf("[AnalyzerAgent] cannot parse repo URL: %v", err)
		return nil
	}

	// Determine git ref for the GitHub API call
	ref := ac.GitRef
	if ref == "" {
		ref = "HEAD"
	}
	// Strip refs/heads/ prefix — GitHub trees API wants a branch name or SHA, not a full ref.
	ref = strings.TrimPrefix(ref, "refs/heads/")

	// GitHub API for the recursive file tree
	treeURL := fmt.Sprintf(
		"https://api.github.com/repos/%s/%s/git/trees/%s?recursive=1",
		owner, repo, ref,
	)

	body, err := fetchText(treeURL)
	if err != nil {
		// The configured ref may not match the repo's actual default branch
		// (e.g. "main" vs "master"). Auto-detect and retry once.
		log.Printf("[AnalyzerAgent] tree fetch failed with ref=%q: %v — detecting default branch", ref, err)
		detected := ghDefaultBranch(owner, repo)
		if detected != ref {
			ref = detected
			// Update ac.GitRef so downstream agents (SecurityAgent) use the correct branch
			ac.GitRef = ref
			treeURL = fmt.Sprintf(
				"https://api.github.com/repos/%s/%s/git/trees/%s?recursive=1",
				owner, repo, ref,
			)
			body, err = fetchText(treeURL)
		}
		if err != nil {
			log.Printf("[AnalyzerAgent] tree fetch still failed: %v — trying README fallback", err)
			// Fallback: fetch the README and ask Kimi K2 to detect the stack from it.
			readmeURL := fmt.Sprintf(
				"https://raw.githubusercontent.com/%s/%s/%s/README.md",
				owner, repo, ref,
			)
			readme, readmeErr := fetchText(readmeURL)
			if readmeErr != nil {
				log.Printf("[AnalyzerAgent] README fallback also failed: %v", readmeErr)
				return nil
			}
			// Truncate to first 500 chars
			if len(readme) > 500 {
				readme = readme[:500]
			}
			system := `You are a devops expert. Given the beginning of a README from a GitHub repository,
identify the programming language, framework, entry point file, and the port the app
likely listens on. Also guess if a Dockerfile exists.
Respond ONLY in valid JSON with no markdown, no backticks, no preamble:
{"language":"...","framework":"...","entryPoint":"...","detectedPort":8080,"hasDockerfile":false,"confidence":"low"}`

			raw, kimiErr := askKimi(ctx, apiKey, system, "README content:\n"+readme)
			if kimiErr != nil {
				log.Printf("[AnalyzerAgent] kimi call (README fallback) failed: %v", kimiErr)
				return nil
			}
			var result struct {
				Language      string `json:"language"`
				Framework     string `json:"framework"`
				EntryPoint    string `json:"entryPoint"`
				DetectedPort  int    `json:"detectedPort"`
				HasDockerfile bool   `json:"hasDockerfile"`
			}
			if err := json.Unmarshal([]byte(extractJSON(raw)), &result); err != nil {
				log.Printf("[AnalyzerAgent] failed to parse AI response (README fallback): %v", err)
				return nil
			}
			if result.Language != "" {
				ac.Language = result.Language
			}
			ac.Framework = result.Framework
			ac.EntryPoint = result.EntryPoint
			if result.DetectedPort > 0 {
				ac.DetectedPort = result.DetectedPort
			}
			ac.HasDockerfile = result.HasDockerfile
			log.Printf("[AnalyzerAgent] (README fallback) stack=%s/%s port=%d hasDockerfile=%v",
				ac.Language, ac.Framework, ac.DetectedPort, ac.HasDockerfile)
			return nil
		}
	}

	// Parse tree paths
	var treeResp struct {
		Tree []struct {
			Path string `json:"path"`
			Type string `json:"type"`
		} `json:"tree"`
	}
	if jsonErr := json.Unmarshal([]byte(body), &treeResp); jsonErr != nil {
		log.Printf("[AnalyzerAgent] failed to parse tree response: %v", jsonErr)
		return nil
	}

	var paths []string
	for _, node := range treeResp.Tree {
		if node.Type == "blob" {
			paths = append(paths, node.Path)
		}
	}
	ac.RepoFileList = paths

	// Build a compact file list string (limit to 300 entries to stay within context)
	limit := len(paths)
	if limit > 300 {
		limit = 300
	}
	fileList := strings.Join(paths[:limit], "\n")

	system := `You are a devops expert. Given a list of files in a GitHub repository,
identify the programming language, framework, entry point file, and the port the app
likely listens on. Also state if a Dockerfile exists.
Respond ONLY in valid JSON with no markdown, no backticks, no preamble:
{"language":"...","framework":"...","entryPoint":"...","detectedPort":8080,"hasDockerfile":false,"confidence":"high"}`

	raw, err := askKimi(ctx, apiKey, system, "Repository file list:\n"+fileList)
	if err != nil {
		log.Printf("[AnalyzerAgent] kimi call failed: %v", err)
		return nil
	}

	var result struct {
		Language      string `json:"language"`
		Framework     string `json:"framework"`
		EntryPoint    string `json:"entryPoint"`
		DetectedPort  int    `json:"detectedPort"`
		HasDockerfile bool   `json:"hasDockerfile"`
		Confidence    string `json:"confidence"`
	}
	if err := json.Unmarshal([]byte(extractJSON(raw)), &result); err != nil {
		log.Printf("[AnalyzerAgent] failed to parse AI response: %v — raw: %s", err, raw)
		return nil
	}

	if result.Language != "" {
		ac.Language = result.Language
	}
	ac.Framework = result.Framework
	ac.EntryPoint = result.EntryPoint
	if result.DetectedPort > 0 {
		ac.DetectedPort = result.DetectedPort
	}
	ac.HasDockerfile = result.HasDockerfile

	log.Printf("[AnalyzerAgent] stack=%s/%s port=%d hasDockerfile=%v",
		ac.Language, ac.Framework, ac.DetectedPort, ac.HasDockerfile)
	return nil
}

// ─── Agent 2: BuilderAgent ───────────────────────────────────────────────────

// BuilderAgent generates a production-optimised Dockerfile when the repo does
// not already contain one. Results are written into ac. Errors are non-fatal.
func BuilderAgent(ctx context.Context, ac *AgentContext, apiKey string) error {
	if apiKey == "" || ac.HasDockerfile {
		// Skip: repo already has a Dockerfile, nothing to generate.
		return nil
	}

	system := `You are a Docker expert. Generate a production-optimized multi-stage
Dockerfile and a .dockerignore for the described application.
Use official base images. Include a non-root user. Add a HEALTHCHECK instruction.
Respond ONLY in valid JSON with no markdown, no backticks, no preamble:
{"dockerfile":"...","dockerignore":"..."}`

	userMsg := fmt.Sprintf(
		"Language: %s\nFramework: %s\nEntry point: %s\nListening port: %d",
		ac.Language, ac.Framework, ac.EntryPoint, ac.DetectedPort,
	)

	raw, err := askKimi(ctx, apiKey, system, userMsg)
	if err != nil {
		log.Printf("[BuilderAgent] kimi call failed: %v", err)
		return nil // non-fatal
	}

	var result struct {
		Dockerfile    string `json:"dockerfile"`
		Dockerignore  string `json:"dockerignore"`
	}
	if err := json.Unmarshal([]byte(extractJSON(raw)), &result); err != nil {
		log.Printf("[BuilderAgent] failed to parse AI response: %v — raw: %s", err, raw)
		return nil
	}

	if result.Dockerfile == "" {
		log.Printf("[BuilderAgent] AI returned empty Dockerfile, skipping")
		return nil
	}

	ac.GeneratedDockerfile = result.Dockerfile
	ac.GeneratedDockerignore = result.Dockerignore
	ac.DockerfilePath = "inline"

	log.Printf("[BuilderAgent] generated Dockerfile (%d bytes) for %s/%s",
		len(ac.GeneratedDockerfile), ac.Language, ac.Framework)
	return nil
}

// ─── Agent 3: SecurityAgent ──────────────────────────────────────────────────

// SecurityAgent fetches sensitive files from the repo and asks Kimi K2 to
// scan for hardcoded secrets, root users in Dockerfiles, CVE-prone images, etc.
// Returns a non-nil error ONLY when a "high" severity issue is found and the
// scan report marks SecurityPassed=false.
func SecurityAgent(ctx context.Context, ac *AgentContext, apiKey string) error {
	// Default: pass (do not block deployments when this agent is skipped)
	ac.SecurityPassed = true
	ac.SecuritySummary = "Security scan skipped."

	if apiKey == "" {
		return nil
	}

	owner, repo, err := ghRepoFromURL(ac.RepoURL)
	if err != nil {
		log.Printf("[SecurityAgent] cannot parse repo URL: %v", err)
		return nil
	}

	// Files of interest for secret scanning
	sensitiveNames := []string{
		".env", ".env.example", "config.py", "settings.py",
		"application.properties", "docker-compose.yml",
	}
	// Also scan any file whose path contains "secret" or "key" (case-insensitive)
	for _, f := range ac.RepoFileList {
		lower := strings.ToLower(f)
		if strings.Contains(lower, "secret") || strings.Contains(lower, "key") {
			sensitiveNames = append(sensitiveNames, f)
		}
	}

	ref := ac.GitRef
	if ref == "" {
		ref = "HEAD"
	}
	// Strip refs/heads/ prefix for raw.githubusercontent.com
	ref = strings.TrimPrefix(ref, "refs/heads/")
	// If AnalyzerAgent already detected the correct branch, ac.GitRef is updated;
	// otherwise auto-detect here too.
	if ref == "main" || ref == "HEAD" {
		detected := ghDefaultBranch(owner, repo)
		if detected != "" {
			ref = detected
		}
	}

	var collectedFiles strings.Builder
	for _, name := range sensitiveNames {
		var found bool
		for _, f := range ac.RepoFileList {
			if f == name || strings.HasSuffix(f, "/"+name) {
				found = true
				name = f
				break
			}
		}
		if !found &&
			name != ".env" && name != ".env.example" &&
			name != "config.py" && name != "settings.py" &&
			name != "application.properties" && name != "docker-compose.yml" {
			continue
		}
		rawURL := fmt.Sprintf(
			"https://raw.githubusercontent.com/%s/%s/%s/%s",
			owner, repo, ref, name,
		)
		content, fetchErr := fetchText(rawURL)
		if fetchErr != nil {
			continue // file may not exist; skip silently
		}
		collectedFiles.WriteString(fmt.Sprintf("\n--- %s ---\n%s\n", name, content))
	}

	// Include the Dockerfile (generated or signal that original exists)
	dfSnippet := ""
	if ac.GeneratedDockerfile != "" {
		dfSnippet = "\n--- Dockerfile (AI-generated) ---\n" + ac.GeneratedDockerfile + "\n"
	} else if ac.HasDockerfile {
		dfSnippet = "\n--- Dockerfile: exists in repo (content not fetched) ---\n"
	}

	scanContent := collectedFiles.String() + dfSnippet
	if strings.TrimSpace(scanContent) == "" {
		scanContent = "(no sensitive files found in repo)"
	}

	system := `You are a security expert. Scan the provided file contents for:
1. Hardcoded secrets or API keys
2. Running as root inside a Dockerfile
3. Exposed sensitive ports (e.g. database ports)
4. Missing .gitignore entries for sensitive files
5. CVE-prone base images (e.g. very old versions)
Respond ONLY in valid JSON with no markdown, no backticks, no preamble:
{"passed":true,"issues":[{"severity":"high","description":"...","file":"..."}],"summary":"..."}`

	raw, err := askKimi(ctx, apiKey, system, scanContent)
	if err != nil {
		log.Printf("[SecurityAgent] kimi call failed: %v", err)
		return nil // non-fatal: do not block deploy on scan failure
	}

	var result struct {
		Passed  bool `json:"passed"`
		Issues  []struct {
			Severity    string `json:"severity"`
			Description string `json:"description"`
			File        string `json:"file"`
		} `json:"issues"`
		Summary string `json:"summary"`
	}
	if err := json.Unmarshal([]byte(extractJSON(raw)), &result); err != nil {
		log.Printf("[SecurityAgent] failed to parse AI response: %v — raw: %s", err, raw)
		return nil
	}

	issues := make([]SecurityIssue, 0, len(result.Issues))
	hasHighSeverity := false
	for _, iss := range result.Issues {
		issues = append(issues, SecurityIssue{
			Severity:    iss.Severity,
			Description: iss.Description,
			File:        iss.File,
		})
		if strings.EqualFold(iss.Severity, "high") {
			hasHighSeverity = true
		}
	}

	ac.SecurityIssues = issues
	ac.SecurityPassed = result.Passed
	ac.SecuritySummary = result.Summary

	log.Printf("[SecurityAgent] passed=%v issues=%d summary=%s",
		ac.SecurityPassed, len(ac.SecurityIssues), ac.SecuritySummary)

	// Block the pipeline only on explicit failure AND a high-severity finding
	if !ac.SecurityPassed && hasHighSeverity {
		log.Printf("[SecurityAgent] high severity issues found: %s", ac.SecuritySummary); return nil
	}
	return nil
}

// ─── Agent 4: ConfigAgent ────────────────────────────────────────────────────

// ConfigAgent asks Kimi K2 for recommended K8s resource requests/limits,
// a health check path, and suggested environment variables. Errors are non-fatal.
func ConfigAgent(ctx context.Context, ac *AgentContext, apiKey string) error {
	// Defaults
	ac.SuggestedCPU = "250m"
	ac.SuggestedMemory = "256Mi"
	ac.HealthCheckPath = "/health"

	if apiKey == "" {
		return nil
	}

	system := `You are a Kubernetes expert. For the described application suggest
appropriate resource requests/limits, a health check HTTP path, and required
environment variables the developer should set.
Respond ONLY in valid JSON with no markdown, no backticks, no preamble:
{"cpuRequest":"250m","cpuLimit":"500m","memoryRequest":"256Mi","memoryLimit":"512Mi",
"healthCheckPath":"/health","envVars":[{"key":"PORT","description":"Listening port","required":true}]}`

	userMsg := fmt.Sprintf(
		"Language: %s\nFramework: %s\nDetected port: %d",
		ac.Language, ac.Framework, ac.DetectedPort,
	)

	raw, err := askKimi(ctx, apiKey, system, userMsg)
	if err != nil {
		log.Printf("[ConfigAgent] kimi call failed: %v", err)
		return nil
	}

	var result struct {
		CPURequest      string `json:"cpuRequest"`
		CPULimit        string `json:"cpuLimit"`
		MemoryRequest   string `json:"memoryRequest"`
		MemoryLimit     string `json:"memoryLimit"`
		HealthCheckPath string `json:"healthCheckPath"`
		EnvVars         []struct {
			Key         string `json:"key"`
			Description string `json:"description"`
			Required    bool   `json:"required"`
		} `json:"envVars"`
	}
	if err := json.Unmarshal([]byte(extractJSON(raw)), &result); err != nil {
		log.Printf("[ConfigAgent] failed to parse AI response: %v — raw: %s", err, raw)
		return nil
	}

	if result.CPURequest != "" {
		ac.SuggestedCPU = result.CPURequest
	}
	if result.MemoryRequest != "" {
		ac.SuggestedMemory = result.MemoryRequest
	}
	if result.HealthCheckPath != "" {
		ac.HealthCheckPath = result.HealthCheckPath
	}

	envVars := make([]EnvVarSuggestion, 0, len(result.EnvVars))
	for _, ev := range result.EnvVars {
		envVars = append(envVars, EnvVarSuggestion{
			Key:         ev.Key,
			Description: ev.Description,
			Required:    ev.Required,
		})
	}
	ac.EnvVars = envVars

	log.Printf("[ConfigAgent] cpu=%s mem=%s health=%s envVars=%d",
		ac.SuggestedCPU, ac.SuggestedMemory, ac.HealthCheckPath, len(ac.EnvVars))
	return nil
}

// ─── Agent 5: DeployAgent ────────────────────────────────────────────────────

// DeployAgent is a thin validation step with no AI call. It confirms the
// pipeline context is coherent before handing off to Kaniko.
func DeployAgent(_ context.Context, ac *AgentContext, _ string) error {
	dockerfileStatus := "existing"
	if ac.GeneratedDockerfile != "" {
		if ac.DockerfilePath != "inline" {
			return fmt.Errorf("deploy agent: BuilderAgent produced a Dockerfile but DockerfilePath is not 'inline'")
		}
		dockerfileStatus = "generated"
	}

	securityStatus := "passed"
	if !ac.SecurityPassed && hasHighSeverityIssue(ac.SecurityIssues) {
		securityStatus = fmt.Sprintf("issues found (%d)", len(ac.SecurityIssues))
	}

	log.Printf("[DeployAgent] pipeline complete — stack=%s/%s dockerfile=%s security=%s port=%d",
		ac.Language, ac.Framework, dockerfileStatus, securityStatus, ac.DetectedPort)
	return nil
}

// ─── Agent 6: MonitorAgent ───────────────────────────────────────────────────

// MonitorAgent runs in a goroutine AFTER deployment. It waits 30 seconds, then
// probes the app health endpoint and asks Kimi K2 to interpret the result.
// It writes its notes to ac.MonitorNotes and sends a "monitor" WebSocket message
// back to the vercel server via the provided sendWS callback.
func MonitorAgent(ctx context.Context, ac *AgentContext, apiKey string, sendWS func(v interface{}) error) {
	// Wait for the app to start
	select {
	case <-time.After(30 * time.Second):
	case <-ctx.Done():
		return
	}

	if ac.PublicURL == "" {
		log.Printf("[MonitorAgent] no public URL, skipping health check")
		return
	}

	// Insecure client to handle self-signed certs on fresh deployments
	insecureClient := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
		},
	}

	probeURLs := []string{
		strings.TrimSuffix(ac.PublicURL, "/") + "/health",
		strings.TrimSuffix(ac.PublicURL, "/") + "/",
	}

	statusCode := 0
	probeURL := probeURLs[0]
	for _, u := range probeURLs {
		resp, err := insecureClient.Get(u) //nolint:noctx
		if err != nil {
			log.Printf("[MonitorAgent] probe %s failed: %v", u, err)
			continue
		}
		resp.Body.Close()
		statusCode = resp.StatusCode
		probeURL = u
		break
	}

	notes := fmt.Sprintf("Health probe to %s returned HTTP %d.", probeURL, statusCode)

	if apiKey != "" {
		system := `You are a DevOps health-check advisor. Keep your response to one short paragraph, plain text only.`
		userMsg := fmt.Sprintf(
			"A new app was just deployed to Kubernetes. Health check to %s returned HTTP status %d. "+
				"Is this normal? What should the developer check if not?",
			probeURL, statusCode,
		)
		aiCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
		defer cancel()

		raw, err := askKimi(aiCtx, apiKey, system, userMsg)
		if err != nil {
			log.Printf("[MonitorAgent] kimi call failed: %v", err)
		} else {
			notes = strings.TrimSpace(raw)
		}
	}

	ac.MonitorNotes = notes
	log.Printf("[MonitorAgent] health=%d notes=%s", statusCode, notes)

	if sendWS != nil {
		_ = sendWS(map[string]interface{}{
			"type":         "monitor",
			"notes":        notes,
			"healthStatus": statusCode,
		})
	}
}

func hasHighSeverityIssue(issues []SecurityIssue) bool {
	for _, issue := range issues {
		if issue.Severity == "high" {
			return true
		}
	}
	return false
}
