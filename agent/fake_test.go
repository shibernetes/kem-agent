package agent

import (
	"context"
	"slices"
	"sync"

	"github.com/shibernetes/kem-agent/checkpoint"
	"github.com/shibernetes/kem-agent/event"
	"github.com/shibernetes/kem-agent/sink"
)

// callLog records the calls the fakes receive, in order.
type callLog struct {
	mu    sync.Mutex
	calls []string
}

func (l *callLog) add(call string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, call)
}

// list returns the calls recorded so far.
func (l *callLog) list() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.calls)
}

// fakeBatchSink records every send in a call log.
type fakeBatchSink struct {
	calls *callLog
}

func (*fakeBatchSink) Name() string {
	return "fake"
}

func (*fakeBatchSink) Shutdown(context.Context) error {
	return nil
}

func (s *fakeBatchSink) Encoder() sink.Encoder {
	return s
}

func (s *fakeBatchSink) Framer() sink.Framer {
	return s
}

func (s *fakeBatchSink) Send(context.Context, []byte, sink.BatchID) (int, error) {
	s.calls.add("send")
	return 0, nil
}

func (*fakeBatchSink) AppendEvent(dst []byte, ev *event.Event) []byte {
	return append(dst, ev.Name...)
}

func (*fakeBatchSink) Separator() []byte {
	return nil
}

func (*fakeBatchSink) Compose(dst, frames []byte, _ int) []byte {
	return append(dst, frames...)
}

func (*fakeBatchSink) Fixed() int {
	return 0
}

// fakeStore records every save in a call log.
type fakeStore struct {
	calls *callLog
}

func (*fakeStore) Load(context.Context) (checkpoint.State, error) {
	return checkpoint.State{}, nil
}

func (s *fakeStore) Save(context.Context, checkpoint.State) error {
	s.calls.add("save")
	return nil
}
