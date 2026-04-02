package app

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

func (o *Orchestrator) runKanikoJob(ctx context.Context, projectID, gitContext, imageRef, jobName string) error {
	ns := o.Config.K8sNamespace
	args := []string{
		"--dockerfile=Dockerfile",
		"--context=" + gitContext,
		"--destination=" + imageRef,
		"--verbosity=info",
	}
	if o.Config.SkipHarborTLSVerify {
		args = append(args, "--skip-tls-verify")
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
						VolumeMounts: []corev1.VolumeMount{{
							Name:      "docker-config",
							MountPath: "/kaniko/.docker",
							ReadOnly:  true,
						}},
					}},
					Volumes: []corev1.Volume{{
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
					}},
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

func buildDeployment(projectID, imageRef string, port int32) *appsv1.Deployment {
	name := deploymentName(projectID)
	labels := workloadLabels(projectID)
	replicas := int32(1)
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
					}},
				},
			},
		},
	}
}

func (o *Orchestrator) applyDeployment(ctx context.Context, projectID, imageRef string) error {
	ns := o.Config.K8sNamespace
	name := deploymentName(projectID)
	desired := buildDeployment(projectID, imageRef, o.Config.AppContainerPort)

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

func buildService(projectID string, port int32) *corev1.Service {
	name := serviceName(projectID)
	labels := workloadLabels(projectID)
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
		Spec: corev1.ServiceSpec{
			Selector: labels,
			Ports: []corev1.ServicePort{{
				Port:       80,
				TargetPort: intstr.FromInt32(port),
			}},
		},
	}
}

func (o *Orchestrator) applyService(ctx context.Context, projectID string) error {
	ns := o.Config.K8sNamespace
	name := serviceName(projectID)
	desired := buildService(projectID, o.Config.AppContainerPort)

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

func (o *Orchestrator) applyIngress(ctx context.Context, projectID string) error {
	if o.Config.IngressBaseDomain == "" {
		return fmt.Errorf("INGRESS_BASE_DOMAIN is required")
	}
	ns := o.Config.K8sNamespace
	name := ingressName(projectID)
	host := fmt.Sprintf("%s.%s", projectID, o.Config.IngressBaseDomain)
	labels := workloadLabels(projectID)

	paths := []netv1.HTTPIngressPath{{
		Path:     "/",
		PathType: pathTypePtr(netv1.PathTypePrefix),
		Backend: netv1.IngressBackend{
			Service: &netv1.IngressServiceBackend{
				Name: serviceName(projectID),
				Port: netv1.ServiceBackendPort{Number: 80},
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
	if o.Config.IngressTLSSecretName != "" {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s.%s", scheme, projectID, o.Config.IngressBaseDomain)
}

// BuildDeploy runs the full pipeline. writePhase is called with "building" and "deploying" before each major step.
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

	if err := writePhase("building"); err != nil {
		return "", err
	}
	if err := o.ensureNamespace(ctx); err != nil {
		return "", fmt.Errorf("namespace: %w", err)
	}
	if err := o.ensureHarborSecret(ctx); err != nil {
		return "", fmt.Errorf("harbor secret: %w", err)
	}
	if err := o.runKanikoJob(ctx, req.ProjectID, gitCtx, imageRef, jobName); err != nil {
		return "", fmt.Errorf("kaniko: %w", err)
	}

	if err := writePhase("deploying"); err != nil {
		return "", err
	}
	if err := o.applyDeployment(ctx, req.ProjectID, imageRef); err != nil {
		return "", fmt.Errorf("deployment: %w", err)
	}
	if err := o.applyService(ctx, req.ProjectID); err != nil {
		return "", fmt.Errorf("service: %w", err)
	}
	if err := o.applyIngress(ctx, req.ProjectID); err != nil {
		return "", fmt.Errorf("ingress: %w", err)
	}

	return o.PublicURL(req.ProjectID), nil
}
