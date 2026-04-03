package app

import (
	"crypto/rand"
	math_rand "math/rand"
	"time"

	"io"
	"log"
	"net/mail"
	"os"
	"strconv"
	"strings"

	"github.com/NikhilSharmaWe/go-vercel-app/vercel/models"
	"github.com/NikhilSharmaWe/go-vercel-app/vercel/store"
	"github.com/gorilla/sessions"
	"github.com/labstack/echo/v4"

	"gorm.io/driver/postgres"

	"gorm.io/gorm"
)

// Application is the Vercel web app (Echo).
type Application struct {
	CookieStore *sessions.CookieStore
	store.UserStore

	OrchestratorAddr         string
	OrchestratorSharedSecret string
	OrchestratorGitRef       string
	OrchestratorHTTPTimeout  time.Duration
	// OrchestratorDeployPath is appended to ORCHESTRATOR_ADDR (default "/deploy-app").
	OrchestratorDeployPath string
}

func NewApplication() (*Application, error) {
	db := createDB()

	userStore := store.NewUserStore(db)
	// Ensure required tables exist on startup (useful for fresh deployments).
	if err := userStore.CreateTable(); err != nil {
		return nil, err
	}

	orchTimeout := 45 * time.Minute
	if v := os.Getenv("ORCHESTRATOR_HTTP_TIMEOUT_MINUTES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			orchTimeout = time.Duration(n) * time.Minute
		}
	}

	deployPath := os.Getenv("ORCHESTRATOR_DEPLOY_PATH")
	if deployPath == "" {
		deployPath = "/deploy-app"
	}
	if deployPath[0] != '/' {
		deployPath = "/" + deployPath
	}

	return &Application{
		CookieStore:              sessions.NewCookieStore([]byte(os.Getenv("SECRET"))),
		UserStore:                userStore,
		OrchestratorAddr:         os.Getenv("ORCHESTRATOR_ADDR"),
		OrchestratorSharedSecret: os.Getenv("ORCHESTRATOR_SHARED_SECRET"),
		OrchestratorGitRef:       os.Getenv("ORCHESTRATOR_DEFAULT_GIT_REF"),
		OrchestratorHTTPTimeout:  orchTimeout,
		OrchestratorDeployPath:   deployPath,
	}, nil
}

// DisplayPublicURL returns the URL shown in the UI. If LAUNCHPAD_PUBLIC_APP_URL_TEMPLATE is set
// (e.g. https://svc-{projectId}.launchpad.neev.work), it overrides the host from the orchestrator
// when that service still has a placeholder INGRESS_BASE_DOMAIN (e.g. apps.example.com).
func (app *Application) DisplayPublicURL(projectID, orchestratorURL string) string {
	tpl := strings.TrimSpace(os.Getenv("LAUNCHPAD_PUBLIC_APP_URL_TEMPLATE"))
	if tpl == "" {
		return orchestratorURL
	}
	return strings.NewReplacer(
		"{projectId}", projectID,
		"{projectID}", projectID,
	).Replace(tpl)
}

func createDB() *gorm.DB {
	db, err := gorm.Open(postgres.Open(os.Getenv("DBADDRESS")), &gorm.Config{})
	if err != nil {
		log.Fatal(err)
	}

	return db
}

func (app *Application) alreadyLoggedIn(c echo.Context) bool {
	session := c.Get("session").(*sessions.Session)

	email, ok := session.Values["email"].(string)
	if !ok {
		return false
	}

	if exists, err := app.UserStore.IsExists("email = ?", email); err != nil || !exists {
		return false
	}

	authenticated, ok := session.Values["authenticated"].(bool)
	if ok && authenticated {
		return true
	}

	return false
}

func setSession(c echo.Context, keyValues map[string]any) error {
	session := c.Get("session").(*sessions.Session)
	for k, v := range keyValues {
		session.Values[k] = v
	}

	return session.Save(c.Request(), c.Response())
}

func getSession(c echo.Context, key string) (string, error) {
	session := c.Get("session").(*sessions.Session)
	v, ok := session.Values[key]
	if !ok {
		return "", models.ErrInvalidRequest
	}

	return v.(string), nil
}

// clearDeploySessionKeys removes prior deploy form state so a new deploy does not reuse stale values.
func clearDeploySessionKeys(c echo.Context) {
	session := c.Get("session").(*sessions.Session)
	for _, k := range []string{
		"repo_endpoint", "project_id", "git_ref",
		"dockerfile", "dockerfile_content", "dockerfile_tmp",
		"container_port", "service_port",
	} {
		delete(session.Values, k)
	}
}

// getSessionOptional returns a string session value; missing or wrong type yields ("", nil).
func getSessionOptional(c echo.Context, key string) (string, error) {
	session := c.Get("session").(*sessions.Session)
	v, ok := session.Values[key]
	if !ok {
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", nil
	}
	return s, nil
}

func clearSessionHandler(c echo.Context) error {
	session := c.Get("session").(*sessions.Session)
	session.Options.MaxAge = -1
	return session.Save(c.Request(), c.Response())
}

func (app *Application) createIfNotExists(username, email string, githubAccess bool) error {
	exists, err := app.UserStore.IsExists("email = ?", email)
	if err != nil {
		return err
	}

	if exists {
		return models.ErrUserAlreadyExists
	}

	return app.UserStore.Create(models.UserDBModel{
		Username:     username,
		Email:        email,
		GithubAccess: githubAccess,
	})
}

func validMailAddress(address string) bool {
	_, err := mail.ParseAddress(address)
	return err == nil
}

func generateOTP(length int) (string, error) {
	b := make([]byte, length)
	_, err := io.ReadAtLeast(rand.Reader, b, length)
	if err != nil {
		return "", err
	}

	for i := range b {
		b[i] = byte('0' + int(b[i])%10)
	}
	return string(b), nil
}

func generateID(length int) string {
	charset := "abcdefghijklmnopqrstuvwxyz0123456789"
	id := make([]byte, length)

	seedRand := math_rand.New(math_rand.NewSource(time.Now().UnixNano()))
	for i := range id {
		id[i] = charset[seedRand.Intn(len(charset))]
	}

	return string(id)
}

// NOTE: OTP-by-email flow was removed in favor of simple email+password auth.
