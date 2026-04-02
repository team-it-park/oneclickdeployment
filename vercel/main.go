package main

import (
	"log"
	"os"

	"github.com/NikhilSharmaWe/go-vercel-app/vercel/app"
	"github.com/joho/godotenv"
)

func init() {
	// In Kubernetes/containers we inject env vars directly; vars.env is optional (local dev).
	if err := godotenv.Load("vars.env"); err != nil {
		log.Println("vars.env not loaded:", err)
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
