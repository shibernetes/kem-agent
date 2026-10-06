package kafka

import (
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/shibernetes/kem-agent/internal/identity"
)

const (
	headerCluster   = "kem-agent-cluster"
	headerPod       = "kem-agent-pod"
	headerNamespace = "kem-agent-namespace"
	headerNode      = "kem-agent-node"
	headerVersion   = "kem-agent-version"
)

// recordHeaders returns the headers that identify the agent on each record.
// No header is sent for an unknown or empty identity value. The headers are
// built once and shared by every record, so nothing may modify them.
func recordHeaders(metadata identity.AgentMetadata) []kgo.RecordHeader {
	var headers []kgo.RecordHeader

	for _, h := range []struct {
		key   string
		value string
	}{
		{headerCluster, metadata.Cluster},
		{headerPod, metadata.Pod},
		{headerNamespace, metadata.Namespace},
		{headerNode, metadata.Node},
		{headerVersion, metadata.Version},
	} {
		if h.value != "" {
			headers = append(headers, kgo.RecordHeader{Key: h.key, Value: []byte(h.value)})
		}
	}
	return headers
}
