package app

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/NikhilSharmaWe/go-vercel-app/vercel/models"
	"golang.org/x/crypto/bcrypt"

	"gorm.io/gorm"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

func (app *Application) Router() *echo.Echo {

	e := echo.New()
	if os.Getenv("TRUST_PROXY") == "true" {
		e.IPExtractor = echo.ExtractIPFromXFFHeader()
	}
	e.Pre(middleware.RemoveTrailingSlash())

	e.Static("/assets", "./public")

	e.Use(app.createSessionMiddleware)

	e.GET("/", ServeHTML("./public/signin.html"), app.IfAlreadyLogined)
	e.GET("/signup", ServeHTML("./public/signup.html"), app.IfAlreadyLogined)
	e.GET("/signup/email", ServeHTML("./public/email_signup.html"), app.IfAlreadyLogined)
	e.GET("/signin/email", ServeHTML("./public/email_login.html"), app.IfAlreadyLogined)
	// legacy OTP verify page (no longer used)
	// e.GET("/verify", ServeHTML("./public/verification_code.html"), app.IfAlreadyLogined)

	e.GET("/home", ServeHTML("./public/home.html"), app.IfNotLogined)
	// GitHub OAuth routes removed for now
	e.GET("/logout", app.HandleLogout, app.IfNotLogined)
	e.GET("/start-processing", app.HandleProcessing, app.IfNotLogined)

	// Email + password auth
	e.POST("/signup/password", app.HandleSignupWithPassword, app.IfAlreadyLogined)
	e.POST("/signin/password", app.HandleSigninWithPassword, app.IfAlreadyLogined)
	e.POST("/deploy", app.HandleDeploy, app.IfNotLogined)

	return e
}

func ServeHTML(htmlPath string) echo.HandlerFunc {
	return func(c echo.Context) error {
		return c.File(htmlPath)
	}
}

func (app *Application) HandleSignupWithPassword(c echo.Context) error {
	username := c.FormValue("username")
	email := c.FormValue("email")
	password := c.FormValue("password")
	if !validMailAddress(email) {
		return echo.NewHTTPError(http.StatusBadRequest, models.ErrInvalidEmailAddr)
	}
	if len(password) < 8 {
		return echo.NewHTTPError(http.StatusBadRequest, models.ErrPasswordTooShort)
	}

	exists, err := app.UserStore.IsExists("email = ?", email)
	if err != nil {
		c.Logger().Error(err)
		return err
	}
	if exists {
		return echo.NewHTTPError(http.StatusBadRequest, models.ErrUserAlreadyExists)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		c.Logger().Error(err)
		return err
	}

	if err := app.UserStore.Create(models.UserDBModel{
		Username:     username,
		Email:        email,
		PasswordHash: string(hash),
		GithubAccess: false,
	}); err != nil {
		c.Logger().Error(err)
		return err
	}

	if err := setSession(c, map[string]any{"email": email, "authenticated": true}); err != nil {
		c.Logger().Error(err)
		return err
	}
	return c.Redirect(http.StatusSeeOther, "/home")
}

func (app *Application) HandleSigninWithPassword(c echo.Context) error {
	email := c.FormValue("email")
	password := c.FormValue("password")
	if !validMailAddress(email) {
		return echo.NewHTTPError(http.StatusBadRequest, models.ErrInvalidEmailAddr)
	}

	user, err := app.UserStore.GetOne("email = ?", email)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return echo.NewHTTPError(http.StatusUnauthorized, models.ErrInvalidCredentials)
		}
		c.Logger().Error(err)
		return err
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, models.ErrInvalidCredentials)
	}

	if err := setSession(c, map[string]any{"email": email, "authenticated": true}); err != nil {
		c.Logger().Error(err)
		return err
	}
	return c.Redirect(http.StatusSeeOther, "/home")
}

// HandleVerifyEmail is legacy from the OTP-by-email flow (no longer used).

func (app *Application) HandleLogout(c echo.Context) error {
	if err := clearSessionHandler(c); err != nil {
		c.Logger().Error(err)
		return err
	}

	if err := c.Redirect(http.StatusSeeOther, "/"); err != nil {
		c.Logger().Error(err)
		return err
	}

	return nil
}

const (
	maxDockerfileContentBytes = 524288
	maxDockerfileInSession    = 3500
)

func (app *Application) HandleDeploy(c echo.Context) error {
	clearDeploySessionKeys(c)

	repoEndpoint := c.FormValue("repo-endpoint")
	projectID := generateID(5)
	sess := map[string]any{
		"repo_endpoint": repoEndpoint,
		"project_id":    projectID,
	}
	if v := strings.TrimSpace(c.FormValue("git-ref")); v != "" {
		sess["git_ref"] = v
	}
	dfPath := strings.TrimSpace(c.FormValue("dockerfile"))
	dfContent := strings.TrimSpace(c.FormValue("dockerfile-content"))
	if dfPath != "" && dfContent != "" {
		return echo.NewHTTPError(http.StatusBadRequest, "specify either Dockerfile path or inline Dockerfile, not both")
	}
	if dfContent != "" {
		if len(dfContent) > maxDockerfileContentBytes {
			return echo.NewHTTPError(http.StatusBadRequest, "inline Dockerfile exceeds maximum size")
		}
		if len(dfContent) <= maxDockerfileInSession {
			sess["dockerfile_content"] = dfContent
		} else {
			f, err := os.CreateTemp("", "lp-df-*")
			if err != nil {
				c.Logger().Error(err)
				return err
			}
			if _, err := f.WriteString(dfContent); err != nil {
				_ = f.Close()
				_ = os.Remove(f.Name())
				c.Logger().Error(err)
				return err
			}
			if err := f.Close(); err != nil {
				_ = os.Remove(f.Name())
				c.Logger().Error(err)
				return err
			}
			sess["dockerfile_tmp"] = f.Name()
		}
	} else if dfPath != "" {
		sess["dockerfile"] = dfPath
	}
	if v := strings.TrimSpace(c.FormValue("container-port")); v != "" {
		sess["container_port"] = v
	}
	if v := strings.TrimSpace(c.FormValue("service-port")); v != "" {
		sess["service_port"] = v
	}

	if err := setSession(c, sess); err != nil {
		c.Logger().Error(err)
		return err
	}

	if err := c.File("./public/processing/processing.html"); err != nil {
		c.Logger().Error(err)
		return err
	}

	return nil
}

func (app *Application) HandleProcessing(c echo.Context) error {
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}

	projectID, err := getSession(c, "project_id")
	if err != nil {
		c.Logger().Error(err)
		return err
	}

	repoEndpoint, err := getSession(c, "repo_endpoint")
	if err != nil {
		c.Logger().Error(err)
		return err
	}

	conn, err := upgrader.Upgrade(c.Response(), c.Request(), nil)
	if err != nil {
		c.Logger().Error(err)
		return err
	}
	defer conn.Close()

	writeWS := func(msg string) error {
		return conn.WriteMessage(websocket.TextMessage, []byte(msg))
	}
	writeJSON := func(v interface{}) error {
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		return writeWS(string(b))
	}

	ctx, cancel := context.WithTimeout(context.Background(), app.orchestratorHTTPTimeout()+2*time.Minute)
	defer cancel()

	opts := &DeployAppOptions{}
	if s, _ := getSessionOptional(c, "git_ref"); s != "" {
		opts.GitRef = s
	}
	if s, _ := getSessionOptional(c, "dockerfile_content"); s != "" {
		opts.DockerfileContent = s
	}
	if p, _ := getSessionOptional(c, "dockerfile_tmp"); p != "" {
		defer os.Remove(p)
		b, err := os.ReadFile(p)
		if err != nil {
			c.Logger().Error(err)
			_ = writeJSON(map[string]string{"type": "error", "message": "could not read inline Dockerfile"})
			return nil
		}
		opts.DockerfileContent = string(b)
	}
	if s, _ := getSessionOptional(c, "dockerfile"); s != "" {
		opts.Dockerfile = s
	}
	if s, _ := getSessionOptional(c, "container_port"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			opts.ContainerPort = n
		}
	}
	if s, _ := getSessionOptional(c, "service_port"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			opts.ServicePort = n
		}
	}

	meta := map[string]interface{}{
		"type":       "meta",
		"projectId":  projectID,
		"repository": repoEndpoint,
	}
	if opts.GitRef != "" {
		meta["gitRef"] = opts.GitRef
	}
	if opts.DockerfileContent != "" {
		meta["inlineDockerfile"] = true
		meta["dockerfile"] = "(inline)"
	} else if opts.Dockerfile != "" {
		meta["dockerfile"] = opts.Dockerfile
	}
	if opts.ContainerPort > 0 {
		meta["containerPort"] = opts.ContainerPort
	}
	if opts.ServicePort > 0 {
		meta["servicePort"] = opts.ServicePort
	}
	if err := writeJSON(meta); err != nil {
		c.Logger().Error(err)
		return nil
	}

	publicURL, err := app.CallOrchestratorBuildDeploy(ctx, repoEndpoint, projectID, opts, func(phase string) error {
		log.Printf("Project: %s phase: %s", projectID, phase)
		return writeJSON(map[string]string{"type": "phase", "phase": phase})
	})
	if err != nil {
		// IMPORTANT: after websocket upgrade (connection hijack), do not return an HTTP error
		// or Echo will try to write an HTTP response on a hijacked connection.
		c.Logger().Error(err)
		_ = writeJSON(map[string]string{"type": "error", "message": err.Error()})
		return nil
	}

	displayURL := app.DisplayPublicURL(projectID, publicURL)
	log.Printf("Project: %s deployed at %s", projectID, displayURL)
	_ = writeJSON(map[string]string{"type": "done", "url": displayURL})
	return nil
}
