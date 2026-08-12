package otel

import (
	"context"
	"crypto/tls"
	"encoding/pem"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	collogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/stats"
	"google.golang.org/protobuf/proto"
	corev1 "k8s.io/api/core/v1"
	eventsv1 "k8s.io/api/events/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/shibernetes/kem-agent/event"
	"github.com/shibernetes/kem-agent/internal/identity"
	"github.com/shibernetes/kem-agent/internal/tlsconfig"
	"github.com/shibernetes/kem-agent/sink"
)

// A collector is an OTLP logs service capturing the requests it receives.
type collector struct {
	collogs.UnimplementedLogsServiceServer

	addr     string
	caPEM    string
	received []*collogs.ExportLogsServiceRequest
	sizes    payloadSizes
	headers  metadata.MD
	reply    *collogs.ExportLogsServiceResponse
	err      error
	mu       sync.Mutex
}

// newCollector returns a collector listening in plaintext.
func newCollector(t *testing.T) *collector {
	t.Helper()

	return startCollector(t, nil)
}

// newSecureCollector returns a collector listening with TLS.
func newSecureCollector(t *testing.T) *collector {
	t.Helper()

	cert, caPEM := serverCert(t)
	col := startCollector(t, credentials.NewServerTLSFromCert(&cert))
	col.caPEM = caPEM

	return col
}

func startCollector(t *testing.T, creds credentials.TransportCredentials) *collector {
	t.Helper()

	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	var opts []grpc.ServerOption

	if creds != nil {
		opts = append(opts, grpc.Creds(creds))
	}
	col := &collector{addr: ln.Addr().String()}
	opts = append(opts, grpc.StatsHandler(col))
	srv := grpc.NewServer(opts...)
	collogs.RegisterLogsServiceServer(srv, col)

	go func() {
		_ = srv.Serve(ln)
	}()
	t.Cleanup(srv.Stop)

	return col
}

// Export implements the [collogs.LogsServiceServer] interface.
func (c *collector) Export(ctx context.Context, req *collogs.ExportLogsServiceRequest) (*collogs.ExportLogsServiceResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.received = append(c.received, req)
	c.headers, _ = metadata.FromIncomingContext(ctx)

	if c.err != nil {
		return nil, c.err
	}
	if c.reply != nil {
		return c.reply, nil
	}
	return &collogs.ExportLogsServiceResponse{}, nil
}

// setResp sets the response the collector replies with.
func (c *collector) setResp(resp *collogs.ExportLogsServiceResponse, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.reply, c.err = resp, err
}

// lastRequest returns the last request the collector received.
func (c *collector) lastRequest(t *testing.T) *collogs.ExportLogsServiceRequest {
	t.Helper()

	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.received) == 0 {
		t.Fatal("no request received")
	}
	return c.received[len(c.received)-1]
}

// requestCount returns how many requests the collector received.
func (c *collector) requestCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return len(c.received)
}

// recordCount returns how many log records the collector received.
func (c *collector) recordCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	var n int
	for _, req := range c.received {
		for _, rl := range req.GetResourceLogs() {
			for _, sl := range rl.GetScopeLogs() {
				n += len(sl.GetLogRecords())
			}
		}
	}
	return n
}

// header returns the value of the metadata key with name from the last request.
func (c *collector) header(name string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.headers.Get(name)
}

// payloadSizes holds the sizes of a request payload, before and after
// compression. Neither includes the gRPC or HTTP/2 framing.
type payloadSizes struct {
	raw        int
	compressed int
}

func (c *collector) lastRequestPayloadSizes() payloadSizes {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.sizes
}

// HandleRPC implements the [stats.Handler] interface.
// It records the size of each request received.
func (c *collector) HandleRPC(_ context.Context, rpc stats.RPCStats) {
	in, ok := rpc.(*stats.InPayload)
	if !ok {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	c.sizes = payloadSizes{raw: in.Length, compressed: in.CompressedLength}
}

func (c *collector) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context { return ctx }

func (c *collector) TagRPC(ctx context.Context, _ *stats.RPCTagInfo) context.Context { return ctx }
func (c *collector) HandleConn(context.Context, stats.ConnStats)                     {}

// serverCert borrows the certificate httptest generates, rather
// than making one. It is valid for the loopback address.
func serverCert(t *testing.T) (tls.Certificate, string) {
	t.Helper()

	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(server.Close)

	return server.TLS.Certificates[0], string(pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: server.Certificate().Raw,
	}))
}

// attrPairs renders attributes as key=value, in the order they were written.
func attrPairs(kvs []*commonpb.KeyValue) []string {
	pairs := make([]string, 0, len(kvs))
	for _, kv := range kvs {
		pairs = append(pairs, kv.GetKey()+"="+attrValue(kv.GetValue()))
	}
	return pairs
}

func attrValue(v *commonpb.AnyValue) string {
	switch value := v.GetValue().(type) {
	case *commonpb.AnyValue_StringValue:
		return value.StringValue
	case *commonpb.AnyValue_IntValue:
		return strconv.FormatInt(value.IntValue, 10)
	case *commonpb.AnyValue_BoolValue:
		return strconv.FormatBool(value.BoolValue)
	}
	return ""
}

// timedOutContext returns a context whose deadline has passed.
func timedOutContext(t *testing.T) context.Context {
	t.Helper()

	ctx, cancel := context.WithTimeoutCause(t.Context(), time.Millisecond, sink.ErrSendTimeout)
	t.Cleanup(cancel)
	<-ctx.Done()

	return ctx
}

// newTestSink returns a non-opened sink.
func newTestSink(t *testing.T, cfg Config) *Sink {
	t.Helper()

	return New("collector", cfg, testAgentMetadata())
}

// newTestOpenedSink returns an opened sink exporting to the config endpoint.
func newTestOpenedSink(t *testing.T, cfg Config) *Sink {
	t.Helper()

	s := newTestSink(t, cfg)
	if err := s.Open(t.Context()); err != nil {
		t.Fatalf("failed to open sink: %v", err)
	}
	t.Cleanup(func() {
		_ = s.Shutdown(context.Background())
	})
	return s
}

// validConfig returns the smallest configuration that validates.
func validConfig() Config {
	cfg := DefaultConfig()
	cfg.Type, cfg.Endpoint = "otel", "collector:4317"

	return cfg
}

// testConfig returns a configuration exporting to a collector.
func testConfig(t *testing.T, col *collector, opts func(*Config)) Config {
	t.Helper()

	cfg := DefaultConfig()

	cfg.Type = "otel"
	cfg.Endpoint = col.addr
	if col.caPEM != "" {
		cfg.TLS = &tlsconfig.Config{CAPEM: col.caPEM}
	}
	if opts != nil {
		opts(&cfg)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("configuration did not validate: %v", err)
	}
	return cfg
}

// composeBatch runs the sink's encoder and framer over the given events,
// and returns the resulting payload.
func composeBatch(s *Sink, events ...*event.Event) []byte {
	var frames []byte

	for _, ev := range events {
		frames = s.Encoder().AppendEvent(frames, ev)
	}
	return s.Framer().Compose(nil, frames, len(events))
}

// decodeBatch returns the export request of a composed payload.
func decodeBatch(t *testing.T, payload []byte) *collogs.ExportLogsServiceRequest {
	t.Helper()

	var req collogs.ExportLogsServiceRequest
	if err := proto.Unmarshal(payload, &req); err != nil {
		t.Fatalf("failed to decode payload: %v", err)
	}
	return &req
}

func testAgentMetadata() identity.AgentMetadata {
	return identity.AgentMetadata{
		Cluster:   "prod-eu-1",
		Node:      "node-001",
		Namespace: "kem-system",
		Pod:       "kem-agent-0",
		Version:   "0.0.1",
		Commit:    "abc1234",
	}
}

// testEvent returns an event with no series and no enrichment.
func testEvent() *event.Event {
	return &event.Event{
		UID:                 "d1f5c2a0-1111-2222-3333-444455556666",
		ResourceVersion:     "184213",
		Namespace:           "team-a",
		Name:                "web-1.17f0a2b3c4d5e6f7",
		EventTime:           metav1.NewMicroTime(time.Date(2009, 1, 3, 18, 15, 5, 0, time.UTC)),
		ReportingController: "kubelet",
		ReportingInstance:   "kubelet-node-001",
		Reason:              "OOMKilled",
		Note:                "Container web was OOM killed",
		Type:                "Warning",
		Regarding: corev1.ObjectReference{
			APIVersion: "v1",
			Kind:       "Pod",
			Namespace:  "team-a",
			Name:       "web-1",
			UID:        "9c8b7a6d-5e4f-3210-9876-543210fedcba",
		},
	}
}

// fullEvent returns an event with every field the sink reads, so that
// the golden file covers a repeating and enriched one.
func fullEvent() *event.Event {
	ev := testEvent()

	ev.Action = "Killing"
	ev.Regarding.ResourceVersion = "48277"
	ev.Regarding.FieldPath = "spec.containers{web}"
	ev.Series = &eventsv1.EventSeries{
		Count:            7,
		LastObservedTime: metav1.NewMicroTime(time.Date(2009, 1, 3, 18, 20, 0, 0, time.UTC)),
	}
	ev.RegardingObject = &event.RegardingObject{
		Owner: &corev1.ObjectReference{
			APIVersion: "apps/v1",
			Kind:       "ReplicaSet",
			Namespace:  "team-a",
			Name:       "web-7d9f4c",
			UID:        "3c4d5e6f-7a8b-9c0d-1e2f-3a4b5c6d7e8f",
		},
		Terminating: true,
	}
	ev.RegardingObject.Labels = map[string]string{"tier": "front", "app": "web"}
	ev.RegardingObject.Annotations = map[string]string{"team": "platform"}

	return ev
}
