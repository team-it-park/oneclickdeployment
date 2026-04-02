package main

import (
	"log"

	"github.com/NikhilSharmaWe/go-vercel-app/deploy/app"
	"github.com/joho/godotenv"
)

func init() {
	if err := godotenv.Load("vars.env"); err != nil {
		log.Fatal(err)
	}
}

func main() {
	cfg := app.LoadConfig()
	if cfg.ListenAddr == "" {
		log.Fatal("ADDR must be set (e.g. :8081)")
	}

	k8s, err := app.K8sClient()
	if err != nil {
		log.Fatal("kubernetes client: ", err)
	}

	orch := &app.Orchestrator{K8s: k8s, Config: cfg}
	e := app.NewEchoOrchestratorServer(orch)
	log.Fatal(e.Start(cfg.ListenAddr))
}
