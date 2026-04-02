package app

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/NikhilSharmaWe/go-vercel-app/vercel/models"
	"github.com/google/go-github/github"
	"golang.org/x/oauth2"

	"gorm.io/gorm"

	"github.com/gorilla/sessions"
	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

func (app *Application) Router() *echo.Echo {

	e := echo.New()
	e.Pre(middleware.RemoveTrailingSlash())

	e.Static("/assets", "./public")

	e.Use(app.createSessionMiddleware)

	e.GET("/", ServeHTML("./public/signin.html"), app.IfAlreadyLogined)
	e.GET("/signup", ServeHTML("./public/signup.html"), app.IfAlreadyLogined)
	e.GET("/signup/email", ServeHTML("./public/email_signup.html"), app.IfAlreadyLogined)
	e.GET("/signin/email", ServeHTML("./public/email_login.html"), app.IfAlreadyLogined)
	e.GET("/signup/email", ServeHTML("./public/email_signup.html"), app.IfAlreadyLogined)
	e.GET("/verify", ServeHTML("./public/verification_code.html"), app.IfAlreadyLogined)

	e.GET("/home", ServeHTML("./public/home.html"), app.IfNotLogined)

	e.GET(app.GithubAPICallbackPath, app.HandleGithubCallback)
	e.GET("/continue/github", app.HandleGithubAuth)
	e.GET("/logout", app.HandleLogout, app.IfNotLogined)
	e.GET("/start-processing", app.HandleProcessing, app.IfNotLogined)

	e.POST("/continue/email", app.HandleAuthWithEmail, app.IfAlreadyLogined)
	e.POST("/verify", app.HandleVerifyEmail, app.IfAlreadyLogined)
	// e.POST("/deploy", app.HandleDeploy, app.IfNotLogined)
	e.POST("/deploy", app.HandleDeploy, app.IfNotLogined)

	return e
}

func ServeHTML(htmlPath string) echo.HandlerFunc {
	return func(c echo.Context) error {
		return c.File(htmlPath)
	}
}

func (app *Application) HandleAuthWithEmail(c echo.Context) error {
	operation := c.QueryParam("operation")
	email := c.FormValue("email")
	if !validMailAddress(email) {
		return echo.NewHTTPError(http.StatusBadRequest, models.ErrInvalidEmailAddr)
	}

	switch operation {
	case "signup":
		username := c.FormValue("username")
		exists, err := app.UserStore.IsExists("email = ?", email)
		if err != nil {
			c.Logger().Error(err)
			return err
		}

		if exists {
			return echo.NewHTTPError(http.StatusBadRequest, models.ErrUserAlreadyExists)
		}

		if err := app.setupEmailVerification(c, username, email); err != nil {
			c.Logger().Error(err)
			return err
		}

		return c.Redirect(http.StatusSeeOther, fmt.Sprintf("/verify?operation=signup&username=%s&email=%s", username, email))

	case "signin":
		user, err := app.UserStore.GetOne("email = ?", email)
		if err != nil {
			if err == gorm.ErrRecordNotFound {
				return echo.NewHTTPError(http.StatusBadRequest, models.ErrUserNotExists)
			}

			c.Logger().Error(err)
			return err
		}

		username := user.Username

		if err := app.setupEmailVerification(c, username, email); err != nil {
			c.Logger().Error(err)
			return err
		}

		return c.Redirect(http.StatusSeeOther, fmt.Sprintf("/verify?operation=signin&email=%s", email))

	default:
		return echo.NewHTTPError(http.StatusBadRequest, models.ErrInvalidOperation)
	}
}

func (app *Application) HandleGithubAuth(c echo.Context) error {
	operation := c.QueryParam("operation")
	if operation != "signin" && operation != "signup" && operation != "connect" {
		return echo.NewHTTPError(http.StatusBadRequest, models.ErrInvalidOperation)
	}

	if operation != "connect" {
		if app.alreadyLoggedIn(c) {
			return c.Redirect(http.StatusFound, "/home")
		}
	}

	c.Set("operation", operation)

	callbackURL := fmt.Sprintf("http://localhost%s%s?operation=%s", os.Getenv("ADDR"), app.GithubAPICallbackPath, operation)
	redirectURL := fmt.Sprintf("https://github.com/login/oauth/authorize?client_id=%s&scope=repo,user&redirect_uri=%s&prompt=consent", app.GithubClientID, callbackURL)

	return c.Redirect(http.StatusSeeOther, redirectURL)
}

func (app *Application) HandleGithubCallback(c echo.Context) error {
	operation := c.QueryParam("operation")
	if operation != "signin" && operation != "signup" && operation != "connect" {
		return echo.NewHTTPError(http.StatusBadRequest, models.ErrInvalidOperation)
	}

	if operation != "connect" {
		if app.alreadyLoggedIn(c) {
			return c.Redirect(http.StatusFound, "/home")
		}
	}

	code := c.QueryParam("code")
	tok, err := app.getGithubAccessToken(code)
	if err != nil {
		c.Logger().Error(err)
		return err
	}

	ts := oauth2.StaticTokenSource(
		&oauth2.Token{
			AccessToken: tok,
		},
	)

	tc := oauth2.NewClient(c.Request().Context(), ts)

	gc := github.NewClient(tc)

	ctx, cancelFunc := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelFunc()

	emails, _, err := gc.Users.ListEmails(ctx, &github.ListOptions{})
	if err != nil {
		c.Logger().Error(err)
		return err
	}

	var pEmail string

	for _, email := range emails {

		if email.GetPrimary() {
			pEmail = email.GetEmail()
			break
		}
	}

	user, _, err := gc.Users.Get(context.Background(), "")
	if err != nil {
		c.Logger().Error(err)
		return err
	}

	username := *user.Login

	switch operation {
	case "signup":
		err := app.createIfNotExists(username, pEmail, true)
		if err != nil {
			if err == models.ErrUserAlreadyExists {
				return echo.NewHTTPError(http.StatusBadRequest, err)
			}

			c.Logger().Error(err)
			return err
		}

	case "signin":
		user, err := app.UserStore.GetOne("email = ?", pEmail)
		if err != nil {
			if err == gorm.ErrRecordNotFound {
				return echo.NewHTTPError(http.StatusBadRequest, models.ErrUserNotExists)
			}

			c.Logger().Error(err)
			return err
		}

		if !user.GithubAccess {
			return echo.NewHTTPError(http.StatusUnauthorized, models.ErrUserDoNotHaveGithubAccess)
		}

	case "connect":
		exists, err := app.UserStore.IsExists("email = ?", pEmail)
		if err != nil {
			c.Logger().Error(err)
			return err
		}

		if !exists {
			return echo.NewHTTPError(http.StatusBadRequest, models.ErrUserNotExists)
		}

		if err := app.UserStore.Update(map[string]any{"github_access": true}, "email = ?", pEmail); err != nil {
			c.Logger().Error(err)
			return err
		}

	default:
		return echo.NewHTTPError(http.StatusBadRequest, models.ErrInvalidOperation)
	}

	if err := setSession(c, map[string]any{"email": pEmail, "authenticated": true}); err != nil {
		c.Logger().Error(err)
		return err
	}

	return c.Redirect(http.StatusSeeOther, "/home")
}

func (app *Application) HandleVerifyEmail(c echo.Context) error {
	session := c.Get("session").(*sessions.Session)
	code := session.Values["verification_code"]
	userCode := c.FormValue("verification_code")

	operation := c.QueryParam("operation")

	email := c.QueryParam("email")

	if code != userCode {
		return echo.NewHTTPError(http.StatusBadRequest, models.ErrWrongVerificationCode)
	}

	switch operation {
	case "signup":
		username := c.QueryParam("username")
		if err := app.createIfNotExists(username, email, false); err != nil {
			if err == models.ErrUserAlreadyExists {
				return echo.NewHTTPError(http.StatusBadRequest, err)
			}

			c.Logger().Error(err)
			return err
		}

	case "signin":
		break
	default:
		return echo.NewHTTPError(http.StatusBadRequest, models.ErrInvalidOperation)
	}

	if err := setSession(c, map[string]any{"email": email, "authenticated": true}); err != nil {
		c.Logger().Error(err)
		return err
	}

	return c.Redirect(http.StatusSeeOther, "/home")
}

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

func (app *Application) HandleDeploy(c echo.Context) error {
	var (
		repoEndpoint = c.FormValue("repo-endpoint")
		projectID    = generateID(5)
	)

	if err := setSession(c, map[string]any{
		"repo_endpoint": repoEndpoint,
		"project_id":    projectID,
	}); err != nil {
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

	ctx, cancel := context.WithTimeout(context.Background(), app.orchestratorHTTPTimeout()+2*time.Minute)
	defer cancel()

	publicURL, err := app.CallOrchestratorBuildDeploy(ctx, repoEndpoint, projectID, func(phase string) error {
		log.Printf("Project: %s phase: %s", projectID, phase)
		return writeWS(phase)
	})
	if err != nil {
		c.Logger().Error(err)
		_ = writeWS("error: " + err.Error())
		return err
	}

	log.Printf("Project: %s deployed at %s", projectID, publicURL)
	_ = writeWS("deployed")
	_ = writeWS(fmt.Sprintf("WEBSITE ENDPOINT IS: %s", publicURL))
	return nil
}
