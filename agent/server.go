package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	// serverReadTimeout bounds reading the whole request.
	serverReadTimeout = 15 * time.Second

	// serverReadHeaderTimeout bounds reading the request headers.
	serverReadHeaderTimeout = 10 * time.Second

	// serverWriteTimeout bounds writing the response, generous
	// enough for a large /metrics scrape.
	serverWriteTimeout = 60 * time.Second

	// serverIdleTimeout reaps idle keep-alive connections, which nothing
	// else closes, capping connection and goroutine growth from a caller
	// that opens many and holds them.
	serverIdleTimeout = 120 * time.Second
)

const (
	healthzPath = "/healthz"
	readyzPath  = "/readyz"
	reloadPath  = "/reload"
	metricsPath = "/metrics"
)

type httpServer struct {
	srv  *http.Server
	ln   net.Listener
	log  *slog.Logger
	name string
}

func (s *httpServer) run() error {
	s.log.LogAttrs(
		context.Background(),
		slog.LevelInfo, s.name+" server listening",
		slog.String("address", s.ln.Addr().String()),
	)
	if err := s.srv.Serve(s.ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (s *httpServer) shutdown(ctx context.Context) {
	_ = s.srv.Shutdown(ctx)
}

func newServer(addr string, ready *atomic.Bool, reload func(), metrics prometheus.Gatherer, logger *slog.Logger) (*httpServer, error) {
	const name = "http"

	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("failed to listen on %q: %w", addr, err)
	}
	mux := http.NewServeMux()

	mux.HandleFunc("GET "+healthzPath, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET "+readyzPath, func(w http.ResponseWriter, _ *http.Request) {
		if !ready.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("POST "+reloadPath, func(w http.ResponseWriter, _ *http.Request) {
		if reload == nil {
			w.WriteHeader(http.StatusNotImplemented)
			_, _ = io.WriteString(w, "hot reload is off, set service.hot_reload and restart the agent to turn it on\n")
			return
		}
		reload()
		w.WriteHeader(http.StatusAccepted)
	})
	mux.Handle("GET "+metricsPath, promhttp.HandlerFor(metrics, promhttp.HandlerOpts{
		ErrorLog: slog.NewLogLogger(
			logger.With(slog.String("component", "promhttp")).Handler(),
			slog.LevelError,
		),
	}))
	return &httpServer{
		srv: &http.Server{
			Handler:           mux,
			ReadHeaderTimeout: serverReadHeaderTimeout,
			ReadTimeout:       serverReadTimeout,
			WriteTimeout:      serverWriteTimeout,
			IdleTimeout:       serverIdleTimeout,
		},
		ln:   ln,
		log:  logger.With(slog.String("component", name)),
		name: name,
	}, nil
}
