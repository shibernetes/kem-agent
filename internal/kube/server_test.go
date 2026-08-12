package kube

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	apiversion "k8s.io/apimachinery/pkg/version"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func TestCheckServerVersionAccepts(t *testing.T) {
	cases := map[string]string{
		"floor":         "v1.27.0",
		"patch version": "v1.27.11",
		"newer release": "v1.33.2",
		"pre-release":   "v1.28.0-alpha.3",
		"GKE build":     "v1.30.2-gke.1234000",
		"EKS build":     "v1.31.4-eks-a000000",
		"major bump":    "v2.0.0",
	}
	for name, gitVersion := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := CheckServerVersion(context.Background(), clientWithVersion(t, gitVersion))
			if err != nil {
				t.Fatalf("failed to check the server version: %v", err)
			}
			if got != gitVersion {
				t.Errorf("got version %q, want it reported as %q", got, gitVersion)
			}
		})
	}
}

func TestCheckServerVersionRejects(t *testing.T) {
	cases := map[string]string{
		"unsupported":       "v1.26.15",
		"no version":        "",
		"single component":  "v1",
		"unparseable build": "(ㆆ_ㆆ)",
		// A minor of 9 sorts above 27 as a string and below it as a number.
		"early release": "v1.9.0",
	}
	for name, gitVersion := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := CheckServerVersion(context.Background(), clientWithVersion(t, gitVersion)); err == nil {
				t.Errorf("version %q was accepted, want it refused", gitVersion)
			}
		})
	}
}

func TestCheckServerVersionRejectsUnreadableResponse(t *testing.T) {
	cases := map[string]http.HandlerFunc{
		"non-json response": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "not json")
		},
		"failing endpoint": func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "oops", http.StatusInternalServerError)
		},
	}
	for name, handler := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := CheckServerVersion(context.Background(), clientWithHandler(t, handler)); err == nil {
				t.Error("got no error, want the call to fail")
			}
		})
	}
}

func TestCheckServerVersionHonorsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// A request ignoring the context would reach the test server and
	// get a response, so an error proves the context is honored.
	if _, err := CheckServerVersion(ctx, clientWithVersion(t, "v1.33.0")); err == nil {
		t.Error("got no error, want the canceled context to cancel the request")
	}
}

func clientWithVersion(t *testing.T, version string) kubernetes.Interface {
	t.Helper()

	return clientWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/version" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(apiversion.Info{GitVersion: version})
	})
}

func clientWithHandler(t *testing.T, handler http.HandlerFunc) kubernetes.Interface {
	t.Helper()

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	clientSet, err := kubernetes.NewForConfig(&rest.Config{Host: srv.URL})
	if err != nil {
		t.Fatalf("failed to build client set: %v", err)
	}
	return clientSet
}
