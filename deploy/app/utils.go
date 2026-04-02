package app

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Config holds orchestrator settings from the environment.
type Config struct {
	ListenAddr           string
	SharedSecret         string
	K8sNamespace         string
	HarborRegistry       string
	HarborProject        string
	HarborUsername       string
	HarborPassword       string
	IngressBaseDomain    string
	IngressClassName     string
	IngressTLSSecretName string
	// Gateway API (optional): if set, create HTTPRoute instead of Ingress.
	GatewayNamespace string
	GatewayName      string
	KanikoImage          string
	AppContainerPort     int32
	BuildJobTimeoutSec   int
	SkipHarborTLSVerify  bool
	DefaultGitRef        string
}

func LoadConfig() Config {
	port := int32(8080)
	if v := os.Getenv("APP_CONTAINER_PORT"); v != "" {
		var p int
		if _, err := fmt.Sscanf(v, "%d", &p); err == nil && p > 0 {
			port = int32(p)
		}
	}
	timeout := 1800
	if v := os.Getenv("BUILD_JOB_TIMEOUT_SEC"); v != "" {
		var t int
		if _, err := fmt.Sscanf(v, "%d", &t); err == nil && t > 0 {
			timeout = t
		}
	}
	kaniko := os.Getenv("KANIKO_IMAGE")
	if kaniko == "" {
		kaniko = "gcr.io/kaniko-project/executor:v1.23.2"
	}
	gitRef := os.Getenv("DEFAULT_GIT_REF")
	if gitRef == "" {
		gitRef = "refs/heads/main"
	}
	return Config{
		ListenAddr:           os.Getenv("ADDR"),
		SharedSecret:         os.Getenv("ORCHESTRATOR_SHARED_SECRET"),
		K8sNamespace:         getenvDefault("K8S_NAMESPACE", "user-apps"),
		HarborRegistry:       strings.TrimSuffix(os.Getenv("HARBOR_REGISTRY"), "/"),
		HarborProject:        getenvDefault("HARBOR_PROJECT", "go-vercel-apps"),
		HarborUsername:       os.Getenv("HARBOR_USERNAME"),
		HarborPassword:       os.Getenv("HARBOR_PASSWORD"),
		IngressBaseDomain:    os.Getenv("INGRESS_BASE_DOMAIN"),
		IngressClassName:     os.Getenv("INGRESS_CLASS_NAME"),
		IngressTLSSecretName: os.Getenv("INGRESS_TLS_SECRET_NAME"),
		GatewayNamespace:     getenvDefault("GATEWAY_NAMESPACE", "istio-system"),
		GatewayName:          os.Getenv("GATEWAY_NAME"),
		KanikoImage:          kaniko,
		AppContainerPort:     port,
		BuildJobTimeoutSec:   timeout,
		SkipHarborTLSVerify:  strings.EqualFold(os.Getenv("ORCHESTRATOR_SKIP_HARBOR_TLS_VERIFY"), "true"),
		DefaultGitRef:        gitRef,
	}
}

func getenvDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// K8sClient returns a kubernetes clientset (in-cluster or kubeconfig).
func K8sClient() (*kubernetes.Clientset, error) {
	cfg, err := K8sRestConfig()
	if err != nil {
		return nil, err
	}
	return kubernetes.NewForConfig(cfg)
}

// K8sRestConfig builds the Kubernetes REST config (in-cluster or kubeconfig).
func K8sRestConfig() (*rest.Config, error) {
	kc := os.Getenv("KUBECONFIG")
	if kc != "" {
		return clientcmd.BuildConfigFromFlags("", kc)
	}
	cfg, err := rest.InClusterConfig()
	if err == nil {
		return cfg, nil
	}
	return clientcmd.BuildConfigFromFlags("", clientcmd.RecommendedHomeFile)
}

// DockerConfigJSON builds a Docker config.json for Harbor auth.
func DockerConfigJSON(registry, username, password string) ([]byte, error) {
	auth := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
	cfg := map[string]interface{}{
		"auths": map[string]interface{}{
			registry: map[string]string{
				"username": username,
				"password": password,
				"auth":     auth,
			},
		},
	}
	return json.Marshal(cfg)
}

// ToGitContext converts https://github.com/org/repo(.git) to Kaniko git context.
func ToGitContext(httpsURL, gitRef string) (string, error) {
	u := strings.TrimSpace(httpsURL)
	u = strings.TrimSuffix(u, "/")
	if !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "http://") {
		return "", fmt.Errorf("repo URL must start with https:// or http://")
	}
	u = strings.TrimPrefix(u, "https://")
	u = strings.TrimPrefix(u, "http://")
	if !strings.Contains(u, "/") {
		return "", fmt.Errorf("invalid repo URL")
	}
	if !strings.HasSuffix(u, ".git") {
		u += ".git"
	}
	return "git://" + u + "#" + gitRef, nil
}

// FullImageRef returns harbor.example.com/project/id:tag
func FullImageRef(cfg Config, projectID, tag string) string {
	return fmt.Sprintf("%s/%s/%s:%s", cfg.HarborRegistry, cfg.HarborProject, projectID, tag)
}
