package config

import (
	"errors"
	"net"
	"strconv"
	"time"

	"github.com/shibernetes/kem-agent/checkpoint"
	"github.com/shibernetes/kem-agent/config/diag"
	"github.com/shibernetes/kem-agent/config/units"
)

const (
	defaultHTTPAddr        = ":8080"
	defaultShutdownTimeout = units.Duration(30 * time.Second)
	defaultCELEvalTimeout  = units.Duration(250 * time.Millisecond)
)

// Service is the configuration of the agent service.
type Service struct {
	ClusterName     string         `yaml:"cluster_name,omitempty"`
	ShutdownTimeout units.Duration `yaml:"shutdown_timeout,omitempty"`
	CELEvalTimeout  units.Duration `yaml:"cel_eval_timeout,omitempty"`
	HotReload       bool           `yaml:"hot_reload,omitempty"`
	HTTPServer      HTTPServer     `yaml:"http_server,omitempty"`
	PprofServer     PprofServer    `yaml:"pprof_server,omitempty"`
	Logging         Logging        `yaml:"logging,omitempty"`
}

// HTTPServer configures the local HTTP server that exposes the metrics,
// liveness/readiness probes and reload endpoints.
type HTTPServer struct {
	Addr string `yaml:"addr,omitempty"`
}

// PprofServer configures the listener serving the net/http/pprof handlers.
type PprofServer struct {
	Addr string `yaml:"addr,omitempty"`
}

// DefaultServiceConfig returns the default service configuration.
func DefaultServiceConfig() Service {
	return Service{
		ShutdownTimeout: defaultShutdownTimeout,
		CELEvalTimeout:  defaultCELEvalTimeout,
		HTTPServer:      HTTPServer{Addr: defaultHTTPAddr},
		Logging: Logging{
			Format: LogFormatJSON,
			Level:  LogLevelInfo,
		},
	}
}

// Validate validates the configuration.
func (c Service) Validate() error {
	if c.ShutdownTimeout <= checkpoint.FinalSaveReserve {
		return diag.Pathf("shutdown_timeout",
			"shutdown_timeout must be greater than %s, which is reserved for the final checkpoint write",
			checkpoint.FinalSaveReserve,
		)
	}
	if c.CELEvalTimeout <= 0 {
		return diag.Pathf("cel_eval_timeout", "cel_eval_timeout must be positive")
	}
	if err := c.HTTPServer.Validate(); err != nil {
		return diag.Prefix(err, "http_server")
	}
	if err := c.PprofServer.Validate(); err != nil {
		return diag.Prefix(err, "pprof_server")
	}
	return diag.Prefix(c.Logging.Validate(), "logging")
}

// Validate validates the configuration.
func (c HTTPServer) Validate() error {
	if c.Addr == "" {
		return diag.Pathf("addr", "addr is required")
	}
	return validateAddr(c.Addr)
}

// Validate validates the configuration.
func (c PprofServer) Validate() error {
	if c.Addr == "" {
		return nil
	}
	return validateAddr(c.Addr)
}

// validateAddr checks that an address contains a host and a numeric port.
func validateAddr(addr string) error {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return diag.Pathf("addr", "invalid addr %q: %s", addr, addrReason(err))
	}
	// A service name is rejected, since net.Listen resolves one
	// through /etc/services, which a container image might not ship.
	if _, err := strconv.ParseUint(port, 10, 16); err != nil {
		return diag.Pathf("addr", "invalid port %q in addr %q", port, addr)
	}
	return nil
}

func addrReason(err error) string {
	if ae, ok := errors.AsType[*net.AddrError](err); ok {
		return ae.Err
	}
	return err.Error()
}
