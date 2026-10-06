package kafka

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kfake"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/shibernetes/kem-agent/event"
	"github.com/shibernetes/kem-agent/internal/identity"
)

// testEvent returns an event with no series and no enrichment.
func testEvent() *event.Event {
	return &event.Event{
		UID:                 "d1f5c2a0-1111-2222-3333-444455556666",
		ResourceVersion:     "184213",
		Namespace:           "team-a",
		Name:                "web-1.17f0a2b3c4d5e6f7",
		EventTime:           metav1.NewMicroTime(time.Date(2009, 1, 3, 18, 15, 5, 0, time.UTC)),
		ReportingController: "kubelet",
		ReportingInstance:   "node-001",
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

// testEvents returns n events that differ by their UID, and therefore by
// the key of their record.
func testEvents(n int) []*event.Event {
	events := make([]*event.Event, n)

	for i := range events {
		events[i] = testEvent()
		events[i].UID = types.UID("uid-" + strconv.Itoa(i))
	}
	return events
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

// newFakeCluster starts an in-process Kafka cluster, and creates the topic of
// the test configuration with the given number of partitions. Every partition
// gets its own broker, so a test can fail one partition independently.
func newFakeCluster(t *testing.T, partitions int32, opts ...kfake.Opt) *kfake.Cluster {
	t.Helper()

	topic := testConfig().Topic
	opts = append([]kfake.Opt{kfake.NumBrokers(int(partitions)), kfake.SeedTopics(partitions, topic)}, opts...)

	c, err := kfake.NewCluster(opts...)
	if err != nil {
		t.Fatalf("failed to start fake cluster: %v", err)
	}
	t.Cleanup(c.Close)

	// The cluster picks the leader of each partition randomly, so each
	// partition is moved to the broker that has its number.
	for p := range partitions {
		if err := c.MoveTopicPartition(topic, p, p); err != nil {
			t.Fatalf("failed to move partition %d: %v", p, err)
		}
	}
	return c
}

// fakeConfig returns the test configuration, pointed at a fake cluster.
func fakeConfig(c *kfake.Cluster) Config {
	cfg := testConfig()
	cfg.Brokers = c.ListenAddrs()
	cfg.TLS.Insecure = true

	return cfg
}

// failProduce configures a fake cluster with a control function that fails
// every produce request with the given error, for each of its partitions.
func failProduce(c *kfake.Cluster, code *kerr.Error) {
	c.ControlKey(int16(kmsg.Produce), func(req kmsg.Request) (kmsg.Response, error, bool) {
		c.KeepControl()

		return produceFailure(req, code), nil, true
	})
}

// failPartition configures a fake cluster with a control function that fails
// the produce requests to one partition with the given error, for as long as
// failing is set. It relies on the partition being led by the broker that has
// its number.
func failPartition(c *kfake.Cluster, partition int32, code *kerr.Error, failing *atomic.Bool) {
	c.ControlKey(int16(kmsg.Produce), func(req kmsg.Request) (kmsg.Response, error, bool) {
		if !failing.Load() || c.CurrentNode() != partition {
			return nil, nil, false
		}
		c.KeepControl()

		return produceFailure(req, code), nil, true
	})
}

// silenceProduce configures a fake cluster with a control function that
// intercepts every produce request and never returns a response.
func silenceProduce(c *kfake.Cluster) {
	c.ControlKey(int16(kmsg.Produce), func(kmsg.Request) (kmsg.Response, error, bool) {
		c.KeepControl()
		return nil, nil, true
	})
}

// moveLeaderOnProduce configures a fake cluster with a control function that
// moves the partition of the test topic to another broker on the first produce
// request, and fails every produce request that reaches the former leader with
// NOT_LEADER_FOR_PARTITION. It reports whether a request reached the former
// leader at all.
func moveLeaderOnProduce(c *kfake.Cluster, to int32) *atomic.Bool {
	var moved atomic.Bool

	c.ControlKey(int16(kmsg.Produce), func(req kmsg.Request) (kmsg.Response, error, bool) {
		if c.CurrentNode() == to {
			return nil, nil, false
		}
		c.KeepControl()

		// The cluster runs control functions and partition moves one at a time,
		// on a single goroutine, so moving the partition from here would wait
		// forever. The move happens on its own goroutine once this returns.
		if !moved.Swap(true) {
			go func() {
				_ = c.MoveTopicPartition(testConfig().Topic, 0, to)
			}()
		}
		return produceFailure(req, kerr.NotLeaderForPartition), nil, true
	})
	return &moved
}

// produceFailure builds the response to a produce request, with the given
// error for each of its partitions.
func produceFailure(req kmsg.Request, code *kerr.Error) kmsg.Response {
	preq := req.(*kmsg.ProduceRequest)
	resp := preq.ResponseKind().(*kmsg.ProduceResponse)

	for _, rt := range preq.Topics {
		st := kmsg.NewProduceResponseTopic()
		st.Topic, st.TopicID = rt.Topic, rt.TopicID

		for _, rp := range rt.Partitions {
			sp := kmsg.NewProduceResponseTopicPartition()
			sp.Partition, sp.ErrorCode = rp.Partition, code.Code
			st.Partitions = append(st.Partitions, sp)
		}
		resp.Topics = append(resp.Topics, st)
	}
	return resp
}

// consumeRecords reads the topic of the test configuration back
// from a fake cluster, until it has read at least n records.
func consumeRecords(t *testing.T, c *kfake.Cluster, n int) []*kgo.Record {
	t.Helper()

	cl, err := kgo.NewClient(
		kgo.SeedBrokers(c.ListenAddrs()...),
		kgo.ConsumeTopics(testConfig().Topic),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
	)
	if err != nil {
		t.Fatalf("failed to create consumer: %v", err)
	}
	defer cl.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	var records []*kgo.Record

	for len(records) < n {
		fetches := cl.PollFetches(ctx)
		if errs := fetches.Errors(); len(errs) > 0 {
			t.Fatalf("failed to consume records: %v", errs[0].Err)
		}
		records = append(records, fetches.Records()...)
	}
	return records
}

// plaintextListener starts a listener that reads the TLS hello of each
// connection and then closes it, as a broker without TLS does, and returns
// its address.
func plaintextListener(t *testing.T) string {
	t.Helper()

	ln, err := new(net.ListenConfig).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			// Closing with the hello unread would reset the connection
			// rather than end it, so the whole TLS record is read first.
			header := make([]byte, 5)
			if _, err := io.ReadFull(conn, header); err == nil {
				_, _ = io.CopyN(io.Discard, conn, int64(binary.BigEndian.Uint16(header[3:])))
			}
			_ = conn.Close()
		}
	}()
	return ln.Addr().String()
}
