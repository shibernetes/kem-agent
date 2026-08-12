package kube

import (
	"fmt"
	"runtime"

	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/metadata"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/shibernetes/kem-agent/internal/version"
)

const (
	acceptContentTypes = k8sruntime.ContentTypeProtobuf + "," + k8sruntime.ContentTypeJSON
	noRateLimit        = -1
	offlineHost        = "https://kubernetes.invalid"
)

// Config holds the settings of a Kubernetes client.
type Config struct {
	rest *rest.Config
}

// Load resolves the connection settings from an explicit kubeconfig
// path when one is given, and from the standard locations or the
// in-cluster environment otherwise.
func Load(kubeconfig string) (*Config, error) {
	cfg, err := resolve(kubeconfig)
	if err != nil {
		return nil, err
	}
	return newConfig(cfg), nil
}

// OfflineConfig returns the settings of a client that can be built but
// never connects, so a configuration can be checked when no cluster
// is available.
func OfflineConfig() *Config {
	return newConfig(&rest.Config{Host: offlineHost})
}

// ClientSet returns a Kubernetes typed client.
func (c *Config) ClientSet() (kubernetes.Interface, error) {
	client, err := kubernetes.NewForConfig(c.rest)
	if err != nil {
		return nil, fmt.Errorf("failed to build client: %w", err)
	}
	return client, nil
}

// MetadataClient returns a client that reads the metadata of any
// resource, in the form of PartialObjectMetadata objects.
func (c *Config) MetadataClient() (metadata.Interface, error) {
	client, err := metadata.NewForConfig(c.rest)
	if err != nil {
		return nil, fmt.Errorf("failed to build metadata client: %w", err)
	}
	return client, nil
}

func newConfig(cfg *rest.Config) *Config {
	cfg.UserAgent = userAgent()

	// Protobuf is what makes decoding a watch cheap, with JSON left
	// in the accept list for the endpoints that do not serve it.
	cfg.ContentType = k8sruntime.ContentTypeProtobuf
	cfg.AcceptContentTypes = acceptContentTypes

	// A negative QPS disables the rate limiter, while zero defaults
	// to a bucket of five a second. During startup, the agent opens
	// an informer per declared resource and watch, which the default
	// QPS would throttle, resulting in a failed cache sync.
	cfg.QPS = noRateLimit

	// The timeout stays unset, since it bounds every request alike
	// and would end each watch at its expiry.
	cfg.Timeout = 0

	return &Config{rest: cfg}
}

func resolve(kubeconfig string) (*rest.Config, error) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	rules.ExplicitPath = kubeconfig

	loader := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, &clientcmd.ConfigOverrides{})

	// The loader prefers a kubeconfig that resolves to a cluster, and
	// reaches for the in-cluster environment only when the merged files
	// come up empty. An explicit path that does not exist fails, where
	// an absent $KUBECONFIG or ~/.kube/config is skipped.
	cfg, err := loader.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load client config: %w", err)
	}
	return cfg, nil
}

func userAgent() string {
	build := version.Get()
	return fmt.Sprintf("%s/%s (%s/%s)",
		version.Name, build.Semver(), runtime.GOOS, runtime.GOARCH,
	)
}
