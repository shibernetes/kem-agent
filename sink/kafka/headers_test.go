package kafka

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/shibernetes/kem-agent/internal/identity"
)

func TestRecordHeaders(t *testing.T) {
	metadata := identity.AgentMetadata{
		Cluster:   "prod-eu-1",
		Namespace: "kem-system",
		Pod:       "kem-agent-0",
		Version:   "0.0.1",
	}
	want := []kgo.RecordHeader{
		{Key: "kem-agent-cluster", Value: []byte("prod-eu-1")},
		{Key: "kem-agent-pod", Value: []byte("kem-agent-0")},
		{Key: "kem-agent-namespace", Value: []byte("kem-system")},
		{Key: "kem-agent-version", Value: []byte("0.0.1")},
	}
	if diff := cmp.Diff(want, recordHeaders(metadata)); diff != "" {
		t.Errorf("the headers differ (-want +got):\n%s", diff)
	}
}
