package agent

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestServersServeOnlyTheirOwnRoutes(t *testing.T) {
	var (
		ready  atomic.Bool
		logger = slog.New(slog.DiscardHandler)
	)
	ready.Store(true)

	agentSrv, err := newAgentServer("127.0.0.1:0", &ready, nil, logger)
	if err != nil {
		t.Fatalf("newAgentServer() failed: %v", err)
	}
	t.Cleanup(func() {
		_ = agentSrv.ln.Close()
	})
	metricsSrv, err := newMetricsServer("127.0.0.1:0", prometheus.NewRegistry(), logger)
	if err != nil {
		t.Fatalf("newMetricsServer() failed: %v", err)
	}
	t.Cleanup(func() {
		_ = metricsSrv.ln.Close()
	})

	cases := map[string]struct {
		srv  *httpServer
		path string
		want int
	}{
		"liveness on the agent server":    {srv: agentSrv, path: healthzPath, want: http.StatusOK},
		"readiness on the agent server":   {srv: agentSrv, path: readyzPath, want: http.StatusOK},
		"no metrics on the agent server":  {srv: agentSrv, path: metricsPath, want: http.StatusNotFound},
		"metrics on the metrics server":   {srv: metricsSrv, path: metricsPath, want: http.StatusOK},
		"no probes on the metrics server": {srv: metricsSrv, path: readyzPath, want: http.StatusNotFound},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			tc.srv.srv.Handler.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, tc.path, nil))

			if rec.Code != tc.want {
				t.Errorf("got status %d for %s, want %d", rec.Code, tc.path, tc.want)
			}
		})
	}
}
