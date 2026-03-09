package k8s

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// GatewaySummary holds a condensed view of a Gateway resource.
type GatewaySummary struct {
	Name      string
	Namespace string
	Class     string
	Listeners []string // e.g. "HTTP:80", "HTTPS:443"
}

// ListGateways returns all Gateway resources visible to the user,
// optionally scoped to a single namespace.
func ListGateways(ctx context.Context, c client.Client, namespace string) ([]GatewaySummary, error) {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "gateway.networking.k8s.io",
		Version: "v1",
		Kind:    "GatewayList",
	})

	opts := []client.ListOption{}
	if namespace != "" {
		opts = append(opts, client.InNamespace(namespace))
	}

	if err := c.List(ctx, list, opts...); err != nil {
		return nil, fmt.Errorf("failed to list gateways: %w", err)
	}

	summaries := make([]GatewaySummary, 0, len(list.Items))
	for _, item := range list.Items {
		summary := GatewaySummary{
			Name:      item.GetName(),
			Namespace: item.GetNamespace(),
		}

		spec, _ := item.Object["spec"].(map[string]interface{})
		if spec != nil {
			if cls, ok := spec["gatewayClassName"].(string); ok {
				summary.Class = cls
			}
			if listeners, ok := spec["listeners"].([]interface{}); ok {
				for _, l := range listeners {
					if listener, ok := l.(map[string]interface{}); ok {
						proto, _ := listener["protocol"].(string)
						port, _ := listener["port"].(int64)
						summary.Listeners = append(summary.Listeners, fmt.Sprintf("%s:%d", proto, port))
					}
				}
			}
		}

		summaries = append(summaries, summary)
	}

	return summaries, nil
}
