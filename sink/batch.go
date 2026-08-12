package sink

import (
	"github.com/segmentio/ksuid"
)

// A BatchID identifies one event batch.
// It is minted once when the batch is formed, before the retry
// loop, so that every attempt carries the same identifier.
type BatchID ksuid.KSUID

// NewBatchID returns an identifier for a newly formed batch.
func NewBatchID() BatchID {
	return BatchID(ksuid.New())
}

// String returns the identifier in its textual form, which sorts
// by the time the batch was formed.
func (id BatchID) String() string {
	return ksuid.KSUID(id).String()
}
