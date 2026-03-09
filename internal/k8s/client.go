package k8s

import (
	"fmt"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// NewClient creates a Kubernetes client using the given kubeconfig path.
// If kubeconfigPath is empty, it falls back to the KUBECONFIG env var,
// ~/.kube/config, and finally in-cluster config.
func NewClient(kubeconfigPath string) (client.Client, error) {
	cfg, err := buildRestConfig(kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to build k8s config: %w", err)
	}
	c, err := client.New(cfg, client.Options{})
	if err != nil {
		return nil, fmt.Errorf("failed to create k8s client: %w", err)
	}
	return c, nil
}

func buildRestConfig(kubeconfigPath string) (*rest.Config, error) {
	if kubeconfigPath != "" {
		return clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	}
	// Use KUBECONFIG env var or default ~/.kube/config
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		loadingRules, &clientcmd.ConfigOverrides{},
	).ClientConfig()
	if err != nil {
		// Fall back to in-cluster config
		return rest.InClusterConfig()
	}
	return cfg, nil
}
