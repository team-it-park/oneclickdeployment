package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/NikhilSharmaWe/go-vercel-app/vercel/models"
)

type orchestratorStreamLine struct {
	Phase     string `json:"phase,omitempty"`
	Success   *bool  `json:"success,omitempty"`
	PublicURL string `json:"publicURL,omitempty"`
	Error     string `json:"error,omitempty"`
}

// DeployAppOptions are optional fields for POST /deploy-app (per-app build/runtime config).
// Zero values are omitted from the JSON body so the orchestrator uses its env defaults.
type DeployAppOptions struct {
	GitRef             string
	Dockerfile         string
	DockerfileContent  string
	ContainerPort      int
	ServicePort        int
}

// CallOrchestratorBuildDeploy POSTs to the orchestrator and parses NDJSON lines.
// It invokes onPhase for each {"phase":"..."} line before the terminal line.
// opts may be nil; app-level ORCHESTRATOR_DEFAULT_GIT_REF applies when opts.GitRef is empty.
func (app *Application) CallOrchestratorBuildDeploy(ctx context.Context, repoURL, projectID string, opts *DeployAppOptions, onPhase func(string) error) (publicURL string, err error) {
	base := strings.TrimSuffix(app.OrchestratorAddr, "/")
	if base == "" {
		return "", fmt.Errorf("ORCHESTRATOR_ADDR is not set")
	}
	u := base + app.OrchestratorDeployPath

	body := map[string]interface{}{
		"githubRepoEndpoint": repoURL,
		"projectID":          projectID,
	}
	gitRef := ""
	if opts != nil && opts.GitRef != "" {
		gitRef = opts.GitRef
	}
	if gitRef == "" && app.OrchestratorGitRef != "" {
		gitRef = app.OrchestratorGitRef
	}
	if gitRef != "" {
		body["gitRef"] = gitRef
	}
	if opts != nil {
		if opts.DockerfileContent != "" {
			body["dockerfileContent"] = opts.DockerfileContent
		} else if opts.Dockerfile != "" {
			body["dockerfile"] = opts.Dockerfile
		}
		if opts.ContainerPort > 0 {
			body["containerPort"] = opts.ContainerPort
		}
		if opts.ServicePort > 0 {
			body["servicePort"] = opts.ServicePort
		}
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if app.OrchestratorSharedSecret != "" {
		req.Header.Set("X-Orchestrator-Secret", app.OrchestratorSharedSecret)
	}

	client := &http.Client{Timeout: app.orchestratorHTTPTimeout()}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("orchestrator %s: %s", resp.Status, string(b))
	}

	br := bufio.NewReader(resp.Body)
	var terminal orchestratorStreamLine
	for {
		line, rerr := br.ReadString('\n')
		if line == "" && rerr != nil {
			if rerr == io.EOF {
				break
			}
			return "", rerr
		}
		s := strings.TrimSpace(line)
		if s != "" {
			var sl orchestratorStreamLine
			if jerr := json.Unmarshal([]byte(s), &sl); jerr != nil {
				return "", fmt.Errorf("orchestrator response: %w", jerr)
			}
			if sl.Phase != "" {
				if err := onPhase(sl.Phase); err != nil {
					return "", err
				}
			}
			if sl.Success != nil || sl.Error != "" {
				terminal = sl
			}
		}
		if rerr != nil {
			break
		}
	}

	if terminal.Error != "" {
		return "", fmt.Errorf("%s", terminal.Error)
	}
	if terminal.Success == nil || !*terminal.Success {
		return "", models.ErrOrchestratorFailed
	}
	if terminal.PublicURL == "" {
		return "", models.ErrOrchestratorFailed
	}
	return terminal.PublicURL, nil
}

func (app *Application) orchestratorHTTPTimeout() time.Duration {
	if app.OrchestratorHTTPTimeout > 0 {
		return app.OrchestratorHTTPTimeout
	}
	return 45 * time.Minute
}
