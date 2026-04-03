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

// CallOrchestratorBuildDeploy POSTs to the orchestrator and parses NDJSON lines.
// It invokes onPhase for each {"phase":"..."} line before the terminal line.
func (app *Application) CallOrchestratorBuildDeploy(ctx context.Context, repoURL, projectID string, onPhase func(string) error) (publicURL string, err error) {
	base := strings.TrimSuffix(app.OrchestratorAddr, "/")
	if base == "" {
		return "", fmt.Errorf("ORCHESTRATOR_ADDR is not set")
	}
	u := base + app.OrchestratorDeployPath

	body := map[string]string{
		"githubRepoEndpoint": repoURL,
		"projectID":          projectID,
	}
	if app.OrchestratorGitRef != "" {
		body["gitRef"] = app.OrchestratorGitRef
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
