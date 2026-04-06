package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
)

// Orchestrator runs Kaniko builds and applies workload objects.
type Orchestrator struct {
	K8s    *kubernetes.Clientset
	Config Config
}

func workloadLabels(projectID string) map[string]string {
	return map[string]string{
		"app":        "go-vercel-app",
		"project-id": projectID,
	}
}

func deploymentName(projectID string) string {
	return "app-" + projectID
}

func serviceName(projectID string) string {
	return "svc-" + projectID
}

func ingressName(projectID string) string {
	return "ing-" + projectID
}

func int32Ptr(v int32) *int32 { return &v }

func int64Ptr(v int64) *int64 { return &v }

func (o *Orchestrator) ensureNamespace(ctx context.Context) error {
	ns := o.Config.K8sNamespace
	_, err := o.K8s.CoreV1().Namespaces().Get(ctx, ns, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		return fmt.Errorf("namespace %q not found: create it and apply deploy/k8s/namespace.yaml", ns)
	}
	return err
}

func (o *Orchestrator) ensureHarborSecret(ctx context.Context) error {
	ns := o.Config.K8sNamespace
	raw, err := DockerConfigJSON(o.Config.HarborRegistry, o.Config.HarborUsername, o.Config.HarborPassword)
	if err != nil {
		return err
	}
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "harbor-regcred", Namespace: ns},
		Type:       corev1.SecretTypeDockerConfigJson,
		Data:       map[string][]byte{corev1.DockerConfigJsonKey: raw},
	}
	_, err = o.K8s.CoreV1().Secrets(ns).Get(ctx, sec.Name, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		_, err = o.K8s.CoreV1().Secrets(ns).Create(ctx, sec, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}
	_, err = o.K8s.CoreV1().Secrets(ns).Update(ctx, sec, metav1.UpdateOptions{})
	return err
}

func validateDockerfilePath(p string) error {
	if strings.Contains(p, "..") {
		return fmt.Errorf("dockerfile path must not contain '..'")
	}
	return nil
}

// resolveDeployOpts merges request fields with orchestrator defaults.
// When dockerfileContent is non-empty, dockerfileRel is ignored by Kaniko (inline file is mounted instead).
func (o *Orchestrator) resolveDeployOpts(req BuildDeployRequest) (dockerfileRel string, dockerfileContent []byte, containerPort, servicePort int32, err error) {
	path := strings.TrimSpace(req.Dockerfile)
	content := strings.TrimSpace(req.DockerfileContent)
	if path != "" && content != "" {
		return "", nil, 0, 0, fmt.Errorf("specify either dockerfile path or dockerfileContent, not both")
	}
	if content != "" {
		if len(content) > o.Config.MaxDockerfileContentBytes {
			return "", nil, 0, 0, fmt.Errorf("dockerfileContent exceeds max %d bytes", o.Config.MaxDockerfileContentBytes)
		}
		containerPort = o.Config.AppContainerPort
		if req.ContainerPort > 0 {
			if req.ContainerPort > 65535 {
				return "", nil, 0, 0, fmt.Errorf("containerPort must be 1-65535")
			}
			containerPort = int32(req.ContainerPort)
		}
		servicePort = o.Config.K8sServicePort
		if req.ServicePort > 0 {
			if req.ServicePort > 65535 {
				return "", nil, 0, 0, fmt.Errorf("servicePort must be 1-65535")
			}
			servicePort = int32(req.ServicePort)
		}
		return "", []byte(content), containerPort, servicePort, nil
	}

	dockerfileRel = o.Config.KanikoDockerfile
	if path != "" {
		dockerfileRel = path
	}
	if dockerfileRel == "" {
		dockerfileRel = "Dockerfile"
	}
	if err := validateDockerfilePath(dockerfileRel); err != nil {
		return "", nil, 0, 0, err
	}
	containerPort = o.Config.AppContainerPort
	if req.ContainerPort > 0 {
		if req.ContainerPort > 65535 {
			return "", nil, 0, 0, fmt.Errorf("containerPort must be 1-65535")
		}
		containerPort = int32(req.ContainerPort)
	}
	servicePort = o.Config.K8sServicePort
	if req.ServicePort > 0 {
		if req.ServicePort > 65535 {
			return "", nil, 0, 0, fmt.Errorf("servicePort must be 1-65535")
		}
		servicePort = int32(req.ServicePort)
	}
	return dockerfileRel, nil, containerPort, servicePort, nil
}

func inlineDockerfileSecretName(projectID, jobName string) string {
	// Object name must stay ≤63 chars (DNS label); do not embed projectID verbatim.
	sum := sha256.Sum256([]byte(projectID + "\x00" + jobName))
	return "kn-df-" + hex.EncodeToString(sum[:8])
}

func (o *Orchestrator) runKanikoJob(ctx context.Context, projectID, gitContext, imageRef, jobName, dockerfileRel string, dockerfileContent []byte) error {
	ns := o.Config.K8sNamespace
	var dfArg string
	var secretName string
	if len(dockerfileContent) > 0 {
		secretName = inlineDockerfileSecretName(projectID, jobName)
		sec := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      secretName,
				Namespace: ns,
				Labels:    workloadLabels(projectID),
			},
			Type: corev1.SecretTypeOpaque,
			StringData: map[string]string{
				"Dockerfile": string(dockerfileContent),
			},
		}
		if _, err := o.K8s.CoreV1().Secrets(ns).Create(ctx, sec, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("create dockerfile secret: %w", err)
		}
		defer func() {
			delCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_ = o.K8s.CoreV1().Secrets(ns).Delete(delCtx, secretName, metav1.DeleteOptions{})
		}()
		dfArg = "/kaniko/user/Dockerfile"
	} else {
		dfArg = dockerfileRel
		if dfArg == "" {
			dfArg = "Dockerfile"
		}
	}

	args := []string{
		"--dockerfile=" + dfArg,
		"--context=" + gitContext,
		"--destination=" + imageRef,
		"--verbosity=info",
	}
	if o.Config.SkipHarborTLSVerify {
		args = append(args, "--skip-tls-verify")
	}

	volumes := []corev1.Volume{{
		Name: "docker-config",
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{
				SecretName: "harbor-regcred",
				Items: []corev1.KeyToPath{{
					Key:  corev1.DockerConfigJsonKey,
					Path: "config.json",
				}},
			},
		},
	}}
	volumeMounts := []corev1.VolumeMount{{
		Name:      "docker-config",
		MountPath: "/kaniko/.docker",
		ReadOnly:  true,
	}}
	if secretName != "" {
		volumes = append(volumes, corev1.Volume{
			Name: "inline-dockerfile",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: secretName,
					Items: []corev1.KeyToPath{{
						Key:  "Dockerfile",
						Path: "Dockerfile",
					}},
				},
			},
		})
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      "inline-dockerfile",
			MountPath: "/kaniko/user",
			ReadOnly:  true,
		})
	}

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: ns,
			Labels:    workloadLabels(projectID),
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            int32Ptr(0),
			TTLSecondsAfterFinished: int32Ptr(600),
			ActiveDeadlineSeconds:   int64Ptr(int64(o.Config.BuildJobTimeoutSec)),
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: workloadLabels(projectID)},
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers: []corev1.Container{{
						Name:  "kaniko",
						Image: o.Config.KanikoImage,
						Args:  args,
						Env: []corev1.EnvVar{{
							Name:  "DOCKER_CONFIG",
							Value: "/kaniko/.docker",
						}},
						VolumeMounts: volumeMounts,
					}},
					Volumes: volumes,
				},
			},
		},
	}

	_, err := o.K8s.BatchV1().Jobs(ns).Create(ctx, job, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("create kaniko job: %w", err)
	}

	deadline := time.Duration(o.Config.BuildJobTimeoutSec) * time.Second
	pollCtx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()

	return wait.PollUntilContextTimeout(pollCtx, 3*time.Second, deadline, true, func(ctx context.Context) (bool, error) {
		j, err := o.K8s.BatchV1().Jobs(ns).Get(ctx, jobName, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		if j.Status.Succeeded > 0 {
			return true, nil
		}
		if j.Status.Failed > 0 {
			return false, fmt.Errorf("kaniko job failed (kubectl logs -n %s -l job-name=%s)", ns, jobName)
		}
		return false, nil
	})
}

// deployConfig carries AI-suggested resource and health-check configuration
// from the ConfigAgent into the Deployment builder.
type deployConfig struct {
	CPURequest      string // e.g. "250m"
	CPULimit        string // e.g. "500m"
	MemoryRequest   string // e.g. "256Mi"
	MemoryLimit     string // e.g. "512Mi"
	HealthCheckPath string // e.g. "/health"
}

func defaultDeployConfig() deployConfig {
	return deployConfig{
		CPURequest:      "250m",
		CPULimit:        "500m",
		MemoryRequest:   "256Mi",
		MemoryLimit:     "512Mi",
		HealthCheckPath: "/health",
	}
}

// parseQuantitySafe parses a resource.Quantity string, returning a zero value
// and logging a warning on parse failure — never panics.
func parseQuantitySafe(s, fallback string) resource.Quantity {
	q, err := resource.ParseQuantity(s)
	if err != nil {
		log.Printf("[k8s_build] invalid resource quantity %q: %v — using %q", s, err, fallback)
		q, _ = resource.ParseQuantity(fallback)
	}
	return q
}

func buildDeployment(projectID, imageRef string, port int32, dc deployConfig) *appsv1.Deployment {
	name := deploymentName(projectID)
	labels := workloadLabels(projectID)
	replicas := int32(1)

	resourceReqs := corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    parseQuantitySafe(dc.CPURequest, "250m"),
			corev1.ResourceMemory: parseQuantitySafe(dc.MemoryRequest, "256Mi"),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    parseQuantitySafe(dc.CPULimit, "500m"),
			corev1.ResourceMemory: parseQuantitySafe(dc.MemoryLimit, "512Mi"),
		},
	}

	healthPath := dc.HealthCheckPath
	if healthPath == "" {
		healthPath = "/health"
	}
	livenessProbe := &corev1.Probe{
		ProbeHandler: corev1.ProbeHandler{
			HTTPGet: &corev1.HTTPGetAction{
				Path: healthPath,
				Port: intstr.FromInt32(port),
			},
		},
		InitialDelaySeconds: 15,
		PeriodSeconds:       20,
		FailureThreshold:    3,
	}

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					ImagePullSecrets: []corev1.LocalObjectReference{{Name: "harbor-regcred"}},
					Containers: []corev1.Container{{
						Name:            "app",
						Image:           imageRef,
						ImagePullPolicy: corev1.PullAlways,
						Ports:           []corev1.ContainerPort{{ContainerPort: port}},
						Resources:       resourceReqs,
						LivenessProbe:   livenessProbe,
					}},
				},
			},
		},
	}
}

func (o *Orchestrator) applyDeployment(ctx context.Context, projectID, imageRef string, containerPort int32, dc deployConfig) error {
	ns := o.Config.K8sNamespace
	name := deploymentName(projectID)
	desired := buildDeployment(projectID, imageRef, containerPort, dc)

	cur, err := o.K8s.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		_, err = o.K8s.AppsV1().Deployments(ns).Create(ctx, desired, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}
	desired.ResourceVersion = cur.ResourceVersion
	_, err = o.K8s.AppsV1().Deployments(ns).Update(ctx, desired, metav1.UpdateOptions{})
	return err
}

func buildService(projectID string, servicePort, targetPort int32) *corev1.Service {
	name := serviceName(projectID)
	labels := workloadLabels(projectID)
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
		Spec: corev1.ServiceSpec{
			Selector: labels,
			Ports: []corev1.ServicePort{{
				Port:       servicePort,
				TargetPort: intstr.FromInt32(targetPort),
			}},
		},
	}
}

func (o *Orchestrator) applyService(ctx context.Context, projectID string, servicePort, targetPort int32) error {
	ns := o.Config.K8sNamespace
	name := serviceName(projectID)
	desired := buildService(projectID, servicePort, targetPort)

	cur, err := o.K8s.CoreV1().Services(ns).Get(ctx, name, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		_, err = o.K8s.CoreV1().Services(ns).Create(ctx, desired, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}
	desired.ResourceVersion = cur.ResourceVersion
	desired.Spec.ClusterIP = cur.Spec.ClusterIP
	_, err = o.K8s.CoreV1().Services(ns).Update(ctx, desired, metav1.UpdateOptions{})
	return err
}

func pathTypePtr(t netv1.PathType) *netv1.PathType { return &t }

// publicHostname is the user-facing DNS name (Ingress host / HTTPRoute hostname).
func (o *Orchestrator) publicHostname(projectID string) string {
	if o.Config.PublicHostSubdomainPrefix != "" {
		return fmt.Sprintf("%s-%s.%s", o.Config.PublicHostSubdomainPrefix, projectID, o.Config.IngressBaseDomain)
	}
	return fmt.Sprintf("%s.%s", projectID, o.Config.IngressBaseDomain)
}

func (o *Orchestrator) applyIngress(ctx context.Context, projectID string, servicePort int32) error {
	if o.Config.IngressBaseDomain == "" {
		return fmt.Errorf("INGRESS_BASE_DOMAIN is required")
	}
	ns := o.Config.K8sNamespace
	name := ingressName(projectID)
	host := o.publicHostname(projectID)
	labels := workloadLabels(projectID)

	paths := []netv1.HTTPIngressPath{{
		Path:     "/",
		PathType: pathTypePtr(netv1.PathTypePrefix),
		Backend: netv1.IngressBackend{
			Service: &netv1.IngressServiceBackend{
				Name: serviceName(projectID),
				Port: netv1.ServiceBackendPort{Number: servicePort},
			},
		},
	}}

	ing := &netv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
		Spec: netv1.IngressSpec{
			Rules: []netv1.IngressRule{{
				Host: host,
				IngressRuleValue: netv1.IngressRuleValue{
					HTTP: &netv1.HTTPIngressRuleValue{Paths: paths},
				},
			}},
		},
	}
	if o.Config.IngressClassName != "" {
		ic := o.Config.IngressClassName
		ing.Spec.IngressClassName = &ic
	}
	if o.Config.IngressTLSSecretName != "" {
		ing.Spec.TLS = []netv1.IngressTLS{{
			Hosts:      []string{host},
			SecretName: o.Config.IngressTLSSecretName,
		}}
	}

	cur, err := o.K8s.NetworkingV1().Ingresses(ns).Get(ctx, name, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		_, err = o.K8s.NetworkingV1().Ingresses(ns).Create(ctx, ing, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}
	ing.ResourceVersion = cur.ResourceVersion
	_, err = o.K8s.NetworkingV1().Ingresses(ns).Update(ctx, ing, metav1.UpdateOptions{})
	return err
}

// PublicURL returns the URL shown to the user for this project.
func (o *Orchestrator) PublicURL(projectID string) string {
	scheme := "http"
	if o.Config.PublicURLUseHTTPS {
		scheme = "https"
	} else if o.Config.GatewayName == "" && o.Config.IngressTLSSecretName != "" {
		scheme = "https"
	}
	host := o.publicHostname(projectID)
	return fmt.Sprintf("%s://%s", scheme, host)
}

// BuildDeploy runs the full pipeline. writePhase is called before each major step.
// When NEEV_API_KEY is non-empty, an AI agent pipeline runs before Kaniko:
//   analyzing → generating-dockerfile → security-scan → configuring → building → deploying
func (o *Orchestrator) BuildDeploy(ctx context.Context, req BuildDeployRequest, writePhase func(string) error) (string, error) {
	if req.ProjectID == "" || req.GithubRepoEndpoint == "" {
		return "", fmt.Errorf("projectID and githubRepoEndpoint are required")
	}
	if o.Config.HarborRegistry == "" || o.Config.HarborUsername == "" || o.Config.HarborPassword == "" {
		return "", fmt.Errorf("HARBOR_REGISTRY, HARBOR_USERNAME, HARBOR_PASSWORD are required")
	}
	gitRef := req.GitRef
	if gitRef == "" {
		gitRef = o.Config.DefaultGitRef
	}
	gitCtx, err := ToGitContext(req.GithubRepoEndpoint, gitRef)
	if err != nil {
		return "", err
	}

	imageTag := fmt.Sprintf("build-%d", time.Now().Unix())
	imageRef := FullImageRef(o.Config, req.ProjectID, imageTag)
	jobName := fmt.Sprintf("kaniko-%s-%d", req.ProjectID, time.Now().UnixNano()%1e9)

	// ── Ensure Kubernetes namespace and Harbor credentials ──────────────────
	if err := o.ensureNamespace(ctx); err != nil {
		return "", fmt.Errorf("namespace: %w", err)
	}
	if err := o.ensureHarborSecret(ctx); err != nil {
		return "", fmt.Errorf("harbor secret: %w", err)
	}

	// ── AI agent pipeline (skipped gracefully when NEEV_API_KEY is empty) ───
	ac := &AgentContext{
		RepoURL:   req.GithubRepoEndpoint,
		ProjectID: req.ProjectID,
		GitRef:    gitRef,
	}
	dc := defaultDeployConfig()
	apiKey := o.Config.NeevAPIKey

	if apiKey != "" {
		// Agent 1: Analyze repository
		if wpErr := writePhase("analyzing"); wpErr != nil {
			return "", wpErr
		}
		if agErr := AnalyzerAgent(ctx, ac, apiKey); agErr != nil {
			log.Printf("[BuildDeploy] AnalyzerAgent non-fatal error: %v", agErr)
		}

		// Agent 2: Generate Dockerfile (skipped if repo already has one)
		if wpErr := writePhase("generating-dockerfile"); wpErr != nil {
			return "", wpErr
		}
		if agErr := BuilderAgent(ctx, ac, apiKey); agErr != nil {
			log.Printf("[BuildDeploy] BuilderAgent non-fatal error: %v", agErr)
		}

		// Agent 3: Security scan — fatal only on high-severity findings
		if wpErr := writePhase("security-scan"); wpErr != nil {
			return "", wpErr
		}
		if agErr := SecurityAgent(ctx, ac, apiKey); agErr != nil {
			return "", fmt.Errorf("security check failed: %w", agErr)
		}

		// Agent 4: Suggest K8s resource config
		if wpErr := writePhase("configuring"); wpErr != nil {
			return "", wpErr
		}
		if agErr := ConfigAgent(ctx, ac, apiKey); agErr != nil {
			log.Printf("[BuildDeploy] ConfigAgent non-fatal error: %v", agErr)
		}

		// Agent 5: Pre-deploy validation
		if agErr := DeployAgent(ctx, ac, apiKey); agErr != nil {
			return "", fmt.Errorf("deploy agent: %w", agErr)
		}

		// Propagate AI-suggested resource config into the deployment builder
		if ac.SuggestedCPU != "" {
			dc.CPURequest = ac.SuggestedCPU
			dc.CPULimit = ac.SuggestedCPU // use same value for limit when only one provided
		}
		if ac.SuggestedMemory != "" {
			dc.MemoryRequest = ac.SuggestedMemory
			dc.MemoryLimit = ac.SuggestedMemory
		}
		if ac.HealthCheckPath != "" {
			dc.HealthCheckPath = ac.HealthCheckPath
		}
	}

	// ── Recompute git context if agents detected a different default branch ──
	// AnalyzerAgent may update ac.GitRef when the configured DEFAULT_GIT_REF
	// (e.g. "main") doesn't match the repo's actual default branch (e.g. "master").
	if ac.GitRef != "" && ac.GitRef != gitRef && ac.GitRef != strings.TrimPrefix(gitRef, "refs/heads/") {
		newRef := ac.GitRef
		// Kaniko expects a full ref or branch name in the git context URL.
		log.Printf("[BuildDeploy] agents detected different branch %q (was %q), recomputing git context", newRef, gitRef)
		gitRef = newRef
		gitCtx, err = ToGitContext(req.GithubRepoEndpoint, gitRef)
		if err != nil {
			return "", err
		}
	}

	// ── Resolve dockerfile / port options ────────────────────────────────────
	// If the BuilderAgent generated a Dockerfile and the request did not already
	// supply one, inject it as inline content so Kaniko mounts it as a Secret.
	if ac.GeneratedDockerfile != "" && req.DockerfileContent == "" && req.Dockerfile == "" {
		req.DockerfileContent = ac.GeneratedDockerfile
	}
	dockerfileRel, dockerfileContent, containerPort, servicePort, err := o.resolveDeployOpts(req)
	if err != nil {
		return "", err
	}

	// ── Kaniko build ─────────────────────────────────────────────────────────
	if err := writePhase("building"); err != nil {
		return "", err
	}
	if err := o.runKanikoJob(ctx, req.ProjectID, gitCtx, imageRef, jobName, dockerfileRel, dockerfileContent); err != nil {
		return "", fmt.Errorf("kaniko: %w", err)
	}

	// ── Kubernetes workload apply ─────────────────────────────────────────────
	if err := writePhase("deploying"); err != nil {
		return "", err
	}
	if err := o.applyDeployment(ctx, req.ProjectID, imageRef, containerPort, dc); err != nil {
		return "", fmt.Errorf("deployment: %w", err)
	}
	if err := o.applyService(ctx, req.ProjectID, servicePort, containerPort); err != nil {
		return "", fmt.Errorf("service: %w", err)
	}
	if o.Config.GatewayName != "" {
		if err := o.applyHTTPRoute(ctx, req.ProjectID, servicePort); err != nil {
			return "", fmt.Errorf("httproute: %w", err)
		}
	} else {
		if err := o.applyIngress(ctx, req.ProjectID, servicePort); err != nil {
			return "", fmt.Errorf("ingress: %w", err)
		}
	}

	publicURL := o.PublicURL(req.ProjectID)

	// ── Agent 6: Monitor (non-blocking goroutine) ────────────────────────────
	if apiKey != "" {
		ac.PublicURL = publicURL
		go MonitorAgent(context.Background(), ac, apiKey, nil)
	}

	return publicURL, nil
}
