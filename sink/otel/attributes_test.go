package otel

import (
	"strconv"
	"testing"
	"unsafe"

	"github.com/google/go-cmp/cmp"
)

func TestAttrsWritesEachType(t *testing.T) {
	var a attrs

	a.reset(3)
	a.str("string", "value")
	a.num("int", 42)
	a.flag("bool", true)

	want := []string{
		"string=value",
		"int=42",
		"bool=true",
	}
	if diff := cmp.Diff(want, attrPairs(a.attrs)); diff != "" {
		t.Errorf("the attributes differ (-want +got):\n%s", diff)
	}
}

// TestAttrsSkipsEmptyStrings asserts that an unset field writes
// no attribute, so it differs from an empty one.
func TestAttrsSkipsEmptyStrings(t *testing.T) {
	var a attrs

	a.reset(2)
	a.str("set", "value")
	a.str("unset", "")

	want := []string{"set=value"}
	if diff := cmp.Diff(want, attrPairs(a.attrs)); diff != "" {
		t.Errorf("the attributes differ (-want +got):\n%s", diff)
	}
}

// TestAttrsReusesStorage asserts that a reset empties the list
// and preserves the underlying storage.
func TestAttrsReusesStorage(t *testing.T) {
	var a attrs

	a.reset(4)
	a.str("first", "value")
	before := unsafe.SliceData(a.kvs)

	a.reset(4)
	if len(a.attrs) != 0 {
		t.Errorf("got %d attributes, want the list emptied", len(a.attrs))
	}
	if unsafe.SliceData(a.kvs) != before {
		t.Error("the storage was replaced, want it reused")
	}
}

func TestAttrsGrowsCapacity(t *testing.T) {
	var a attrs

	a.reset(2)
	a.reset(64)

	if got := cap(a.kvs); got < 64 {
		t.Errorf("got capacity %d, want at least 64", got)
	}
}

// TestAttrsStaticUsesNoStorage asserts that the static attributes
// the caller owns don't use the reserved storage.
func TestAttrsStaticUsesNoStorage(t *testing.T) {
	var owned attrs

	owned.reset(2)
	owned.str("service.name", "kem-agent")
	owned.str("k8s.cluster.name", "prod-eu-1")

	var a attrs

	a.reset(1)
	a.static(owned.attrs)
	a.str("own", "value")

	want := []string{
		"service.name=kem-agent",
		"k8s.cluster.name=prod-eu-1",
		"own=value",
	}
	if diff := cmp.Diff(want, attrPairs(a.attrs)); diff != "" {
		t.Errorf("the attributes differ (-want +got):\n%s", diff)
	}
}

// TestAttrsKeepsEveryValue asserts that a written value is not
// discarded once the list is full.
func TestAttrsKeepsEveryValue(t *testing.T) {
	const count = 64

	var a attrs
	a.reset(count)
	want := make([]string, count)

	for i := range count {
		key := "key" + strconv.Itoa(i)
		val := "value" + strconv.Itoa(i)

		a.str(key, val)
		want[i] = key + "=" + val
	}
	if diff := cmp.Diff(want, attrPairs(a.attrs)); diff != "" {
		t.Errorf("the attributes differ (-want +got):\n%s", diff)
	}
}
