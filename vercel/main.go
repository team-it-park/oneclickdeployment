package main

import (
	"log"
	"os"

	"github.com/NikhilSharmaWe/go-vercel-app/vercel/app"
	"github.com/joho/godotenv"
)

func init() {
	// Optional file for local dev; in K8s use env from the Deployment/ConfigMap only.
	for _, p := range []string{"vars.env", "/app/vars.env"} {
		if err := godotenv.Load(p); err == nil {
			return
		}
	}
}

func main() {
	application, err := app.NewApplication()
	if err != nil {
		log.Fatal(err)
	}

	e := application.Router()
	log.Fatal(e.Start(os.Getenv("ADDR")))
}
