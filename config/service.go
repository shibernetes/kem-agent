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
	defaultHTTPAddr        = ":8081"
	defaultMetricsAddr     = ":8080"
	defaultShutdownTimeout = units.Duration(30 * time.Second)
	defaultCELEvalTimeout  = units.Duration(250 * time.Millisecond)
)

// Service is the configuration of the agent service.
type Service struct {
	// Name of the cluster
	ClusterName string `yaml:"cluster_name,omitempty"`

	// Maximum time to deliver the pending events and save the positions
	// when the agent shuts down
	ShutdownTimeout units.Duration `yaml:"shutdown_timeout,omitempty"`

	// Maximum time to evaluate one event against the filters of all
	// pipelines that read it
	CELEvalTimeout units.Duration `yaml:"cel_eval_timeout,omitempty"`

	// Enable hot reload of the filters. Other changes require a restart
	HotReload bool `yaml:"hot_reload,omitempty"`

	// Configuration of the HTTP server for the health probes and the
	// reload endpoint
	HTTPServer HTTPServer `yaml:"http_server,omitempty"`

	// Configuration of the HTTP server for the Prometheus metrics
	MetricsServer MetricsServer `yaml:"metrics_server,omitempty"`

	// Configuration of the HTTP server for the pprof and memory debug
	// endpoints
	PprofServer PprofServer `yaml:"pprof_server,omitempty"`

	// Configuration of the agent's logs
	Logging Logging `yaml:"logging,omitempty"`
}

// HTTPServer configures the HTTP server that serves the liveness and
// readiness probes and the config reload endpoint.
type HTTPServer struct {
	// Address to listen on, as host:port. An empty host listens on all
	// interfaces
	Addr string `yaml:"addr,omitempty"`
}

// MetricsServer configures the HTTP server that serves the Prometheus metrics.
type MetricsServer struct {
	// Address to listen on, as host:port. An empty host listens on all
	// interfaces
	Addr string `yaml:"addr,omitempty"`
}

// PprofServer configures the listener serving the net/http/pprof handlers.
type PprofServer struct {
	// Address to listen on, as host:port. An empty value disables the
	// pprof server
	Addr string `yaml:"addr,omitempty"`
}

// DefaultServiceConfig returns the default service configuration.
func DefaultServiceConfig() Service {
	return Service{
		ShutdownTimeout: defaultShutdownTimeout,
		CELEvalTimeout:  defaultCELEvalTimeout,
		HTTPServer:      HTTPServer{Addr: defaultHTTPAddr},
		MetricsServer:   MetricsServer{Addr: defaultMetricsAddr},
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
	if err := c.MetricsServer.Validate(); err != nil {
		return diag.Prefix(err, "metrics_server")
	}
	if err := c.PprofServer.Validate(); err != nil {
		return diag.Prefix(err, "pprof_server")
	}
	if err := c.validatePorts(); err != nil {
		return err
	}
	return diag.Prefix(c.Logging.Validate(), "logging")
}

// validatePorts checks that two servers don't listen on the same port.
func (c Service) validatePorts() error {
	servers := []struct {
		key, addr, defaultAddr string
	}{
		{key: "http_server", addr: c.HTTPServer.Addr, defaultAddr: defaultHTTPAddr},
		{key: "metrics_server", addr: c.MetricsServer.Addr, defaultAddr: defaultMetricsAddr},
		{key: "pprof_server", addr: c.PprofServer.Addr},
	}
	for i, s := range servers {
		for _, other := range servers[:i] {
			if s.addr == "" || !sharePort(s.addr, other.addr) {
				continue
			}
			// Report the address that was changed from its default,
			// since it's the one written in the file.
			if s.addr == s.defaultAddr && other.addr != other.defaultAddr {
				s, other = other, s
			}
			return diag.Prefix(
				diag.Pathf("addr", "addr %q uses the same port as %s.addr %q", s.addr, other.key, other.addr),
				s.key,
			)
		}
	}
	return nil
}

// Validate validates the configuration.
func (c HTTPServer) Validate() error {
	if c.Addr == "" {
		return diag.Pathf("addr", "addr is required")
	}
	return validateAddr(c.Addr)
}

// Validate validates the configuration.
func (c MetricsServer) Validate() error {
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

// sharePort reports whether two listen addresses bind the same port on a
// common interface. A host that's empty or unspecified, such as 0.0.0.0,
// listens on every interface. Port 0 picks a free port, so it never clashes.
func sharePort(a, b string) bool {
	hostA, portA, errA := net.SplitHostPort(a)
	hostB, portB, errB := net.SplitHostPort(b)
	if errA != nil || errB != nil {
		return false
	}
	numA, _ := strconv.ParseUint(portA, 10, 16)
	numB, _ := strconv.ParseUint(portB, 10, 16)
	if numA == 0 || numA != numB {
		return false
	}
	return hostA == hostB || isAnyHost(hostA) || isAnyHost(hostB)
}

func isAnyHost(host string) bool {
	return host == "" || net.ParseIP(host).IsUnspecified()
}

func addrReason(err error) string {
	if ae, ok := errors.AsType[*net.AddrError](err); ok {
		return ae.Err
	}
	return err.Error()
}
