package app

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

// NewEchoOrchestratorServer registers routes on Echo.
func NewEchoOrchestratorServer(orch *Orchestrator) *echo.Echo {
	e := echo.New()
	e.Pre(middleware.RemoveTrailingSlash())
	h := newDeployAppHandler(orch)
	// Primary API: deploy a user's app (any Dockerfile: SPA, API, static site) into K8s user-apps + public route.
	e.POST("/deploy-app", h)
	// Legacy alias — same behavior.
	e.POST("/build-deploy", h)
	return e
}

func newDeployAppHandler(orch *Orchestrator) echo.HandlerFunc {
	return func(c echo.Context) error {
		if orch.Config.SharedSecret != "" {
			if c.Request().Header.Get("X-Orchestrator-Secret") != orch.Config.SharedSecret {
				return echo.NewHTTPError(http.StatusUnauthorized, "invalid or missing X-Orchestrator-Secret")
			}
		}

		var req BuildDeployRequest
		if err := c.Bind(&req); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}

		c.Response().Header().Set(echo.HeaderContentType, "application/x-ndjson")
		c.Response().WriteHeader(http.StatusOK)
		flusher, ok := c.Response().Writer.(http.Flusher)
		if !ok {
			return echo.NewHTTPError(http.StatusInternalServerError, "streaming not supported")
		}

		writeLine := func(line BuildDeployStreamLine) error {
			b, err := json.Marshal(line)
			if err != nil {
				return err
			}
			if _, err := c.Response().Writer.Write(append(b, '\n')); err != nil {
				return err
			}
			flusher.Flush()
			return nil
		}

		ctx := c.Request().Context()
		deadline := time.Duration(orch.Config.BuildJobTimeoutSec+120) * time.Second
		ctx, cancel := context.WithTimeout(ctx, deadline)
		defer cancel()

		writePhase := func(phase string) error {
			return writeLine(BuildDeployStreamLine{Phase: phase})
		}

		publicURL, err := orch.BuildDeploy(ctx, req, writePhase)
		if err != nil {
			fail := false
			_ = writeLine(BuildDeployStreamLine{Success: &fail, Error: err.Error()})
			return nil
		}
		pass := true
		_ = writeLine(BuildDeployStreamLine{Success: &pass, PublicURL: publicURL})
		return nil
	}
}
