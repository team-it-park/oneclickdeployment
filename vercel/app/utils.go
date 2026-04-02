package app

import (
	"bytes"
	"crypto/rand"
	math_rand "math/rand"
	"time"

	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/mail"
	"os"
	"strconv"

	"github.com/NikhilSharmaWe/go-vercel-app/vercel/models"
	"github.com/NikhilSharmaWe/go-vercel-app/vercel/store"
	"github.com/google/go-github/github"
	"github.com/gorilla/sessions"
	"github.com/labstack/echo/v4"

	"gorm.io/driver/postgres"

	"gorm.io/gorm"
)

// Application is the Vercel web app (Echo).
type Application struct {
	CookieStore *sessions.CookieStore
	store.UserStore

	GithubClientID        string
	GithubClientSecret    string
	GithubAPICallbackPath string
	GithubClients         map[string]*github.Client

	OrchestratorAddr         string
	OrchestratorSharedSecret string
	OrchestratorGitRef       string
	OrchestratorHTTPTimeout  time.Duration
}

func NewApplication() (*Application, error) {
	db := createDB()

	userStore := store.NewUserStore(db)

	clientID := os.Getenv("CLIENT_ID")
	clientSecret := os.Getenv("CLIENT_SECRET")
	callbackPath := os.Getenv("GITHUB_OAUTH_CALLBACK_PATH")

	orchTimeout := 45 * time.Minute
	if v := os.Getenv("ORCHESTRATOR_HTTP_TIMEOUT_MINUTES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			orchTimeout = time.Duration(n) * time.Minute
		}
	}

	return &Application{
		CookieStore:              sessions.NewCookieStore([]byte(os.Getenv("SECRET"))),
		UserStore:                userStore,
		GithubClientID:           clientID,
		GithubClientSecret:       clientSecret,
		GithubAPICallbackPath:    callbackPath,
		GithubClients:            make(map[string]*github.Client),
		OrchestratorAddr:         os.Getenv("ORCHESTRATOR_ADDR"),
		OrchestratorSharedSecret: os.Getenv("ORCHESTRATOR_SHARED_SECRET"),
		OrchestratorGitRef:       os.Getenv("ORCHESTRATOR_DEFAULT_GIT_REF"),
		OrchestratorHTTPTimeout:  orchTimeout,
	}, nil
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

func clearSessionHandler(c echo.Context) error {
	session := c.Get("session").(*sessions.Session)
	session.Options.MaxAge = -1
	return session.Save(c.Request(), c.Response())
}

func (app *Application) getGithubAccessToken(code string) (string, error) {

	requestBodyMap := map[string]string{"client_id": app.GithubClientID, "client_secret": app.GithubClientSecret, "code": code}
	requestJSON, err := json.Marshal(requestBodyMap)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", "https://github.com/login/oauth/access_token", bytes.NewBuffer(requestJSON))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}

	respbody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	// Represents the response received from Github
	type githubAccessTokenResponse struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		Scope       string `json:"scope"`
	}

	var ghresp githubAccessTokenResponse
	if err := json.Unmarshal(respbody, &ghresp); err != nil {
		return "", err
	}

	return ghresp.AccessToken, nil
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
