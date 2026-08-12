package identity

import (
	"os"

	"github.com/shibernetes/kem-agent/internal/version"
)

const (
	nodeNameEnv     = "NODE_NAME"
	podNamespaceEnv = "POD_NAMESPACE"
	podNameEnv      = "POD_NAME"
)

// AgentMetadata identifies the agent that produced an event.
// Any field that could not be resolved is left empty.
type AgentMetadata struct {
	Cluster   string
	Node      string
	Namespace string
	Pod       string
	Version   string
	Commit    string
}

// Resolve reads the agent's identity from the Kubernetes downward-API
// environment, its version and commit from the build information, and
// its cluster name from the supplied configuration.
// The pod name fallbacks to the hostname, while the namespace and the
// node name are read from the environment only.
func Resolve(cluster string) AgentMetadata {
	build := version.Get()

	return AgentMetadata{
		Cluster:   cluster,
		Node:      os.Getenv(nodeNameEnv),
		Namespace: os.Getenv(podNamespaceEnv),
		Pod:       podName(),
		Version:   build.Semver(),
		Commit:    build.Commit,
	}
}

func podName() string {
	if v := os.Getenv(podNameEnv); v != "" {
		return v
	}
	name, _ := os.Hostname()
	return name
}
