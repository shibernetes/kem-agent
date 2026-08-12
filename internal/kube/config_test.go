package kube

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"k8s.io/client-go/rest"

	"github.com/shibernetes/kem-agent/internal/version"
)

var kubeconfigPath = filepath.Join("testdata", "kubeconfig")

const (
	testServer = "https://127.0.0.1:6443"
)

func TestLoadAppliesTheSharedSettings(t *testing.T) {
	cfg := load(t, kubeconfigPath).rest

	if cfg.Host != testServer {
		t.Errorf("got host %q, want %q", cfg.Host, testServer)
	}
	if want := "application/vnd.kubernetes.protobuf"; cfg.ContentType != want {
		t.Errorf("got content type %q, want %q", cfg.ContentType, want)
	}
	if want := "application/vnd.kubernetes.protobuf,application/json"; cfg.AcceptContentTypes != want {
		t.Errorf("got accepted content types %q, want %q", cfg.AcceptContentTypes, want)
	}
	if !strings.HasPrefix(cfg.UserAgent, version.Name+"/") {
		t.Errorf("got user agent %q, want it to start with the agent name", cfg.UserAgent)
	}
	if cfg.Timeout != 0 {
		t.Errorf("got timeout %s, want none", cfg.Timeout)
	}
}

func TestLoadReadsTheDefaultLocations(t *testing.T) {
	t.Setenv("KUBECONFIG", kubeconfigPath)

	if cfg := load(t, "").rest; cfg.Host != testServer {
		t.Errorf("got host %q, want the one from $KUBECONFIG", cfg.Host)
	}
}

func TestLoadRejectsMissingKubeconfig(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Error("got no error, want an explicit path that does not exist to fail")
	}
}

func TestOfflineConfigBuildsClients(t *testing.T) {
	cfg := OfflineConfig()

	if _, err := cfg.ClientSet(); err != nil {
		t.Errorf("failed to build client set: %v", err)
	}
	if _, err := cfg.MetadataClient(); err != nil {
		t.Errorf("failed to build metadata client: %v", err)
	}
}

func TestClientSetIsNotRateLimited(t *testing.T) {
	clientSet, err := load(t, kubeconfigPath).ClientSet()
	if err != nil {
		t.Fatalf("failed to build client set: %v", err)
	}
	restClient, ok := clientSet.CoreV1().RESTClient().(*rest.RESTClient)
	if !ok {
		t.Fatalf("got REST client %T, want *rest.RESTClient", clientSet.CoreV1().RESTClient())
	}
	// A zero QPS leaves the default bucket of five a second, which
	// throttles the informers on a large cluster during startup.
	if limiter := restClient.GetRateLimiter(); limiter != nil {
		t.Errorf("got rate limiter %T, want none", limiter)
	}
}

func TestUserAgent(t *testing.T) {
	got := userAgent()

	if !strings.HasPrefix(got, version.Name+"/") {
		t.Errorf("got user agent %q, want it to start with the agent name", got)
	}
	if platform := runtime.GOOS + "/" + runtime.GOARCH; !strings.Contains(got, platform) {
		t.Errorf("got user agent %q, want it to contain the platform %q", got, platform)
	}
}

func load(t *testing.T, kubeconfig string) *Config {
	t.Helper()

	cfg, err := Load(kubeconfig)
	if err != nil {
		t.Fatalf("failed to load kubeconfig: %v", err)
	}
	return cfg
}
