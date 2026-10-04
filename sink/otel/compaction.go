package otel

import (
	"bytes"
	"hash/maphash"

	"google.golang.org/protobuf/encoding/protowire"
)

const (
	// fieldResource is the field number of the resource in a ResourceLogs.
	// https://github.com/open-telemetry/opentelemetry-proto/blob/v1.11.0/opentelemetry/proto/logs/v1/logs.proto#L53
	fieldResource = 1

	// fieldScopeLogs is the field number of the scope logs in a ResourceLogs.
	// https://github.com/open-telemetry/opentelemetry-proto/blob/v1.11.0/opentelemetry/proto/logs/v1/logs.proto#L56
	fieldScopeLogs = 2

	// fieldScope is the field number of the instrumentation scope in a
	// ScopeLogs.
	// https://github.com/open-telemetry/opentelemetry-proto/blob/v1.11.0/opentelemetry/proto/logs/v1/logs.proto#L72
	fieldScope = 1

	// fieldLogRecords is the field number of the log records in a ScopeLogs.
	// https://github.com/open-telemetry/opentelemetry-proto/blob/v1.11.0/opentelemetry/proto/logs/v1/logs.proto#L75
	fieldLogRecords = 2

	// bytesPerGroup defines how many groups a compactor holds before it starts
	// hashing resources, which is one group per bytesPerGroup bytes of resource.
	// From there, hashing every resource costs less than comparing it with each group.
	// That's the break-even measured for batches of 512 events, for plain and enriched
	// events alike, whose resources are about 370 and 820 bytes, so 17 and 38 groups.
	bytesPerGroup = 22
)

// A compactor rewrites a batch of frames so that the log records sharing a resource
// and an instrumentation scope are sent as one ResourceLogs, as the OTel Collector's
// groupbyattrs processor does when it compacts data. It reuses its buffers from one
// batch to the next, so it serves a single drainer.
//
// Records keep their order within a group, and groups follow the order in which
// their first record arrived.
type compactor struct {
	seed    maphash.Seed
	hashed  bool
	groups  []resourceGroup
	records []groupedRecords
}

// A resourceGroup holds the records that share a resource and a scope,
// which the compacted batch sends as one ResourceLogs. A frame that
// couldn't be parsed is sent unchanged in its own group.
type resourceGroup struct {
	hash     uint64
	resource []byte
	scope    []byte
	frame    []byte
	first    int
	last     int
	size     int
}

// groupedRecords holds the log records of one frame, linked to those of
// the next frame in the same group.
type groupedRecords struct {
	records []byte
	next    int
}

// frameParts holds the parts of a frame that grouping compares and copies.
// They point into the frame they were parsed from, each with its field tag
// and length included.
type frameParts struct {
	resource []byte
	scope    []byte
	records  []byte
}

func newCompactor() compactor {
	return compactor{seed: maphash.MakeSeed()}
}

// compact appends the compacted batch to dst and returns the extended
// buffer. A frame that can't be parsed is copied unchanged, as are any
// trailing bytes that don't form a whole frame.
func (c *compactor) compact(dst, frames []byte) []byte {
	rest := c.parseGroups(frames)

	for i := range c.groups {
		dst = c.appendGroup(dst, &c.groups[i])
	}
	dst = append(dst, rest...)

	// The groups and records point into frames, which is the drainer's
	// accumulation buffer. Clearing them lets the drainer release it.
	clear(c.groups)
	clear(c.records)

	return dst
}

// parseGroups sorts the frames of a batch into groups, and returns any
// trailing bytes that don't form a whole frame.
func (c *compactor) parseGroups(frames []byte) []byte {
	c.groups, c.records, c.hashed = c.groups[:0], c.records[:0], false

	for len(frames) > 0 {
		frame, rest, ok := readField(frames, fieldResourceLogs)
		if !ok {
			return frames
		}
		frames = rest

		if parts, ok := parseFrame(frame); ok {
			c.add(parts)
		} else {
			c.groups = append(c.groups, resourceGroup{frame: frame})
		}
	}
	return nil
}

// add puts the records of one frame into the group with the same resource
// and scope, after the records that group already contains. A frame with
// a resource or scope not seen yet starts a new group.
func (c *compactor) add(parts frameParts) {
	var (
		idx = c.findOrCreateGroup(parts)
		grp = &c.groups[idx]
		n   = len(c.records)
	)
	c.records = append(c.records, groupedRecords{records: parts.records, next: -1})
	if grp.last < 0 {
		grp.first = n
	} else {
		c.records[grp.last].next = n
	}
	grp.last = n
	grp.size += len(parts.records)
}

// findOrCreateGroup returns the index of the group for the given
// resource and scope, and creates that group if there's none yet.
func (c *compactor) findOrCreateGroup(parts frameParts) int {
	if c.shouldHash(parts.resource) {
		c.hashGroups()
	}
	var hash uint64

	if c.hashed {
		hash = maphash.Bytes(c.seed, parts.resource)
	}
	// Comparing bytes works because the encoder writes the resource
	// attributes in a fixed order, so two equal resources always encode
	// identically.
	for i := range c.groups {
		g := &c.groups[i]
		if g.frame != nil || c.hashed && g.hash != hash {
			continue
		}
		if bytes.Equal(g.resource, parts.resource) && bytes.Equal(g.scope, parts.scope) {
			return i
		}
	}
	c.groups = append(c.groups, resourceGroup{
		hash:     hash,
		resource: parts.resource,
		scope:    parts.scope,
		first:    -1,
		last:     -1,
	})
	return len(c.groups) - 1
}

// shouldHash reports whether the compactor should start hashing resources
// before it looks up the group of the given resource.
//
// Comparing bytes saves hashing every resource, which pays off while there
// are few groups. With many groups, each failed comparison reads the agent
// attributes that every resource starts with, so a hash is cheaper.
func (c *compactor) shouldHash(resource []byte) bool {
	return !c.hashed && len(c.groups)*bytesPerGroup >= len(resource)
}

// hashGroups hashes the resource of every group of the batch so far,
// so that the next lookups compare a hash before comparing any bytes.
func (c *compactor) hashGroups() {
	c.hashed = true

	for i := range c.groups {
		if g := &c.groups[i]; g.frame == nil {
			g.hash = maphash.Bytes(c.seed, g.resource)
		}
	}
}

// appendGroup writes a group at the end of dst as a single ResourceLogs,
// with the resource and scope written only once, followed by the group's
// records, and returns the extended buffer.
func (c *compactor) appendGroup(dst []byte, g *resourceGroup) []byte {
	if g.frame != nil {
		return append(dst, g.frame...)
	}
	var (
		scopeLogsLen = len(g.scope) + g.size
		rlogsLen     = len(g.resource) + protowire.SizeTag(fieldScopeLogs) + protowire.SizeBytes(scopeLogsLen)
	)
	dst = protowire.AppendTag(dst, fieldResourceLogs, protowire.BytesType)
	dst = protowire.AppendVarint(dst, uint64(rlogsLen)) //nolint:gosec
	dst = append(dst, g.resource...)
	dst = protowire.AppendTag(dst, fieldScopeLogs, protowire.BytesType)
	dst = protowire.AppendVarint(dst, uint64(scopeLogsLen)) //nolint:gosec
	dst = append(dst, g.scope...)

	for i := g.first; i >= 0; i = c.records[i].next {
		dst = append(dst, c.records[i].records...)
	}
	return dst
}

// parseFrame splits a frame into its resource, scope and log records.
// It reports false when the frame doesn't match the structure the encoder
// writes.
func parseFrame(frame []byte) (frameParts, bool) {
	resource, rlogs, ok := readField(fieldValue(frame), fieldResource)
	if !ok {
		return frameParts{}, false
	}
	scopeLogs, tail, ok := readField(rlogs, fieldScopeLogs)
	if !ok || len(tail) > 0 {
		return frameParts{}, false
	}
	scope, records, ok := readField(fieldValue(scopeLogs), fieldScope)
	if !ok || !isLogRecordsField(records) {
		return frameParts{}, false
	}
	return frameParts{
		resource: resource,
		scope:    scope,
		records:  records,
	}, true
}

// readField reads the field at the start of b, tag and length included,
// and returns it with the remaining bytes.
//
// It reports false if the field has another number, isn't length-delimited
// like a nested message, or is cut short by the end of the data.
func readField(b []byte, num protowire.Number) ([]byte, []byte, bool) {
	if len(b) == 0 {
		return nil, b, false
	}
	tagNum, tagType, tagLen := protowire.ConsumeTag(b)
	if tagLen < 0 || tagNum != num || tagType != protowire.BytesType {
		return nil, b, false
	}
	_, valueLen := protowire.ConsumeBytes(b[tagLen:])
	if valueLen < 0 {
		return nil, b, false
	}
	end := tagLen + valueLen

	return b[:end], b[end:], true
}

// fieldValue returns the contents of a length-delimited field,
// without its tag and length, or nil for an invalid field.
func fieldValue(field []byte) []byte {
	if len(field) == 0 {
		return nil
	}
	_, _, tagLen := protowire.ConsumeTag(field)
	if tagLen < 0 {
		return nil
	}
	value, _ := protowire.ConsumeBytes(field[tagLen:])

	return value
}

// isLogRecordsField reports whether b contains only log records.
func isLogRecordsField(b []byte) bool {
	for len(b) > 0 {
		var ok bool

		if _, b, ok = readField(b, fieldLogRecords); !ok {
			return false
		}
	}
	return true
}
