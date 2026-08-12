package identity

import (
	"os"
	"testing"

	"github.com/shibernetes/kem-agent/internal/version"
)

func TestResolve(t *testing.T) {
	t.Setenv(nodeNameEnv, "node-001")
	t.Setenv(podNamespaceEnv, "shibernetes")
	t.Setenv(podNameEnv, "kem-agent-xx69x")

	want := AgentMetadata{
		Cluster:   "prod-eu-1",
		Node:      "node-001",
		Namespace: "shibernetes",
		Pod:       "kem-agent-xx69x",
		Version:   version.Get().Semver(),
		Commit:    version.Get().Commit,
	}
	if got := Resolve("prod-eu-1"); got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestResolvePodNameFallsBackToHostname(t *testing.T) {
	t.Setenv(podNameEnv, "")

	host, err := os.Hostname()
	if err != nil {
		t.Fatalf("failed to read hostname: %v", err)
	}
	if got := Resolve("").Pod; got != host {
		t.Errorf("got pod name %q, want the hostname %q", got, host)
	}
}

func TestResolveAbsentPartsStayEmpty(t *testing.T) {
	t.Setenv(nodeNameEnv, "")
	t.Setenv(podNamespaceEnv, "")

	metadata := Resolve("")

	for name, value := range map[string]string{
		"cluster":   metadata.Cluster,
		"node":      metadata.Node,
		"namespace": metadata.Namespace,
	} {
		if value != "" {
			t.Errorf("got %s %q, want it empty", name, value)
		}
	}
}
