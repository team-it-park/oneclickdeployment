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
	// Gateway listener section (Gateway API parentRefs.sectionName), e.g. "http" or "https".
	GatewaySectionName string
	// PUBLIC_HOST_SUBDOMAIN_PREFIX: if non-empty, public host is "{prefix}-{projectID}.{INGRESS_BASE_DOMAIN}"
	// and HTTPRoute metadata name is "{prefix}-{projectID}" (e.g. prefix "svc" -> svc-abc12.launchpad.neev.work).
	PublicHostSubdomainPrefix string
	// PUBLIC_URL_USE_HTTPS: if true, PublicURL uses https (e.g. after fixing Gateway TLS).
	PublicURLUseHTTPS bool
	KanikoImage          string
	AppContainerPort     int32
	// K8sServicePort is the Service spec.ports[].port (ClusterIP port). HTTPRoute/Ingress target this; must match buildService.
	// Defaults to 80; set when you want the Service to expose a different port than 80 while the container still uses AppContainerPort.
	K8sServicePort int32
	BuildJobTimeoutSec   int
	SkipHarborTLSVerify  bool
	DefaultGitRef        string
	// KanikoDockerfile is the default --dockerfile path relative to repo root when the request omits "dockerfile".
	KanikoDockerfile string
	// MaxDockerfileContentBytes caps dockerfileContent JSON body size (default 524288).
	MaxDockerfileContentBytes int
	// NeevAPIKey is the API key for NeevCloud Kimi K2 AI inference.
	// When non-empty, the AI agent pipeline (Analyze → BuildDockerfile → Security →
	// Config → Deploy → Monitor) runs before every Kaniko build.
	// When empty, the pipeline is skipped and Kaniko runs directly.
	NeevAPIKey string
}

func LoadConfig() Config {
	port := int32(8080)
	if v := os.Getenv("APP_CONTAINER_PORT"); v != "" {
		var p int
		if _, err := fmt.Sscanf(v, "%d", &p); err == nil && p > 0 {
			port = int32(p)
		}
	}
	svcPort := int32(80)
	if v := os.Getenv("K8S_SERVICE_PORT"); v != "" {
		var p int
		if _, err := fmt.Sscanf(v, "%d", &p); err == nil && p > 0 && p <= 65535 {
			svcPort = int32(p)
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
	kanikoDockerfile := os.Getenv("KANIKO_DOCKERFILE")
	if kanikoDockerfile == "" {
		kanikoDockerfile = "Dockerfile"
	}
	maxDF := 524288
	if v := os.Getenv("MAX_DOCKERFILE_CONTENT_BYTES"); v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil && n > 0 && n <= 1048576 {
			maxDF = n
		}
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
		GatewaySectionName:   getenvDefault("GATEWAY_SECTION_NAME", "http"),
		PublicHostSubdomainPrefix: strings.TrimSpace(os.Getenv("PUBLIC_HOST_SUBDOMAIN_PREFIX")),
		PublicURLUseHTTPS:         strings.EqualFold(os.Getenv("PUBLIC_URL_USE_HTTPS"), "true"),
		KanikoImage:          kaniko,
		AppContainerPort:     port,
		K8sServicePort:       svcPort,
		BuildJobTimeoutSec:   timeout,
		SkipHarborTLSVerify:  strings.EqualFold(os.Getenv("ORCHESTRATOR_SKIP_HARBOR_TLS_VERIFY"), "true"),
		DefaultGitRef:        gitRef,
		KanikoDockerfile:          kanikoDockerfile,
		MaxDockerfileContentBytes: maxDF,
		NeevAPIKey:                os.Getenv("NEEV_API_KEY"),
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
	authKey := registry
	if registry == "docker.io" {
		authKey = "https://index.docker.io/v1/"
	}
	auth := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
	cfg := map[string]interface{}{
		"auths": map[string]interface{}{
			authKey: map[string]interface{}{
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
