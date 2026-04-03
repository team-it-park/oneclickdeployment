package app

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

func (o *Orchestrator) httpRouteName(projectID string) string {
	if o.Config.PublicHostSubdomainPrefix != "" {
		return fmt.Sprintf("%s-%s", o.Config.PublicHostSubdomainPrefix, projectID)
	}
	return "route-" + projectID
}

// applyHTTPRoute creates/updates a Gateway API HTTPRoute that attaches to the configured Gateway
// and routes publicHostname(projectID) to svc-{projectID} on K8sServicePort in the workload namespace.
func (o *Orchestrator) applyHTTPRoute(ctx context.Context, projectID string) error {
	if o.Config.IngressBaseDomain == "" {
		return fmt.Errorf("INGRESS_BASE_DOMAIN is required")
	}
	if o.Config.GatewayName == "" {
		return fmt.Errorf("GATEWAY_NAME is required when using Gateway API")
	}

	ns := o.Config.K8sNamespace
	name := o.httpRouteName(projectID)
	host := o.publicHostname(projectID)

	gvr := schema.GroupVersionResource{
		Group:    "gateway.networking.k8s.io",
		Version:  "v1",
		Resource: "httproutes",
	}

	// Build an unstructured HTTPRoute (CRD) so we don't depend on the core REST codec.
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "gateway.networking.k8s.io/v1",
			"kind":       "HTTPRoute",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": ns,
				"labels":    workloadLabels(projectID),
			},
			"spec": map[string]interface{}{
				"parentRefs": []interface{}{
					map[string]interface{}{
						"name":        o.Config.GatewayName,
						"namespace":   o.Config.GatewayNamespace,
						"sectionName": o.Config.GatewaySectionName,
					},
				},
				"hostnames": []interface{}{host},
				"rules": []interface{}{
					map[string]interface{}{
						"matches": []interface{}{
							map[string]interface{}{
								"path": map[string]interface{}{
									"type":  "PathPrefix",
									"value": "/",
								},
							},
						},
						"backendRefs": []interface{}{
							map[string]interface{}{
								"group":     "",
								"kind":      "Service",
								"name":      serviceName(projectID),
								"namespace": ns,
								"port":      int64(o.Config.K8sServicePort),
								"weight":    1,
							},
						},
					},
				},
			},
		},
	}

	cfg, err := K8sRestConfig()
	if err != nil {
		return err
	}
	dc, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return err
	}

	res := dc.Resource(gvr).Namespace(ns)
	_, err = res.Create(ctx, obj, metav1.CreateOptions{})
	if err == nil {
		return nil
	}
	if !errors.IsAlreadyExists(err) {
		return err
	}

	cur, err := res.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	obj.SetResourceVersion(cur.GetResourceVersion())
	_, err = res.Update(ctx, obj, metav1.UpdateOptions{})
	return err
}

