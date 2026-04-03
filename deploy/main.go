package main

import (
	"log"
	"strings"

	"github.com/NikhilSharmaWe/go-vercel-app/deploy/app"
	"github.com/joho/godotenv"
)

func init() {
	for _, p := range []string{"vars.env", "/app/vars.env"} {
		if err := godotenv.Load(p); err == nil {
			return
		}
	}
}

func main() {
	cfg := app.LoadConfig()
	if cfg.ListenAddr == "" {
		log.Fatal("ADDR must be set (e.g. :8081)")
	}
	if cfg.IngressBaseDomain != "" && strings.Contains(cfg.IngressBaseDomain, "example.com") {
		log.Printf("warning: INGRESS_BASE_DOMAIN=%q looks like a placeholder; HTTPRoute/Ingress hostnames will not match a real cluster. Set INGRESS_BASE_DOMAIN and PUBLIC_HOST_SUBDOMAIN_PREFIX to match deploy/k8s/orchestrator-configmap.yaml (e.g. launchpad.neev.work + svc).", cfg.IngressBaseDomain)
	}

	k8s, err := app.K8sClient()
	if err != nil {
		log.Fatal("kubernetes client: ", err)
	}

	orch := &app.Orchestrator{K8s: k8s, Config: cfg}
	e := app.NewEchoOrchestratorServer(orch)
	log.Fatal(e.Start(cfg.ListenAddr))
}
