package otel

import (
	"testing"

	collogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	"google.golang.org/grpc/mem"
	"google.golang.org/protobuf/proto"

	"github.com/shibernetes/kem-agent/sink"
)

func TestCodecName(t *testing.T) {
	if got := (rawCodec{}).Name(); got != "proto" {
		t.Errorf("got %q, want %q", got, "proto")
	}
}

func TestCodecMarshalsWithoutCopying(t *testing.T) {
	payload := []byte("composed-payload")

	out, err := rawCodec{}.Marshal(payload)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("got %d buffers, want 1", len(out))
	}
	data := out[0].ReadOnlyData()

	if string(data) != string(payload) {
		t.Errorf("got %q, want %q", data, payload)
	}
	if &data[0] != &payload[0] {
		t.Error("got a copy of the payload, want the payload itself")
	}
}

func TestCodecUnmarshalsResponse(t *testing.T) {
	want := &collogs.ExportLogsServiceResponse{
		PartialSuccess: &collogs.ExportLogsPartialSuccess{
			RejectedLogRecords: 40,
			ErrorMessage:       "40 records dropped",
		},
	}
	b, err := proto.Marshal(want)
	if err != nil {
		t.Fatalf("failed to marshal response: %v", err)
	}
	var got collogs.ExportLogsServiceResponse

	if err := (rawCodec{}).Unmarshal(mem.BufferSlice{mem.SliceBuffer(b)}, &got); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if !proto.Equal(want, &got) {
		t.Errorf("got %v, want %v", got.GetPartialSuccess(), want.GetPartialSuccess())
	}
}

// TestCodecRejectsForeignValues asserts that a value the codec cannot
// handle is refused as permanent, so a miswired sink is never retried.
func TestCodecRejectsForeignValues(t *testing.T) {
	cases := map[string]func() error{
		"marshal": func() error {
			_, err := rawCodec{}.Marshal("not a payload")
			return err
		},
		"unmarshal": func() error {
			return rawCodec{}.Unmarshal(nil, "not a message")
		},
	}
	for name, call := range cases {
		t.Run(name, func(t *testing.T) {
			err := call()
			if err == nil {
				t.Fatal("got no error, want the value refused")
			}
			if got := sink.Classify(err); got != sink.ResultPermanent {
				t.Errorf("got %s, want %s", got, sink.ResultPermanent)
			}
		})
	}
}

func TestRefusedRecords(t *testing.T) {
	cases := map[string]struct {
		resp *collogs.ExportLogsServiceResponse
		want int
	}{
		"no response":     {nil, 0},
		"empty":           {&collogs.ExportLogsServiceResponse{}, 0},
		"nothing refused": {partialSuccess(0), 0},
		"partial success": {partialSuccess(40), 40},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := refusedRecords(tc.resp); got != tc.want {
				t.Errorf("got %d refused records, want %d", got, tc.want)
			}
		})
	}
}

func partialSuccess(rejected int64) *collogs.ExportLogsServiceResponse {
	return &collogs.ExportLogsServiceResponse{
		PartialSuccess: &collogs.ExportLogsPartialSuccess{RejectedLogRecords: rejected},
	}
}
