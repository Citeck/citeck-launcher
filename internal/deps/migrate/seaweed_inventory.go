package migrate

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sort"
)

// fs.meta.save exports length-prefixed protobuf FullEntry records. We retain
// their complete semantic contents, canonicalizing field/map order, and omit
// only volatile access times from attributes. This includes names, directories,
// versions, chunk references, inline data, encryption metadata and bucket config.
// The small wire decoder accepts only the protobuf wire types used by filer.proto
// and refuses truncation/unknown wire types instead of accepting partial data.
type seaweedInventory [][32]byte

func (a seaweedInventory) Diff(other Inventory) (problems, notes []string) {
	b, ok := other.(seaweedInventory)
	if !ok || len(a) != len(b) {
		return []string{"SeaweedFS metadata entry count changed after upgrade"}, nil
	}
	for i := range a {
		if a[i] != b[i] {
			return []string{"SeaweedFS object or bucket metadata changed after upgrade"}, nil
		}
	}
	return nil, nil
}
func parseSeaweedInventory(data []byte) (Inventory, error) {
	var out seaweedInventory
	for len(data) > 0 {
		if len(data) < 4 {
			return nil, fmt.Errorf("truncated SeaweedFS metadata frame")
		}
		n := uint64(binary.BigEndian.Uint32(data[:4]))
		data = data[4:]
		if n == 0 || n > seaweedMetadataLimit || n > uint64(len(data)) {
			return nil, fmt.Errorf("invalid SeaweedFS metadata frame length")
		}
		frameLength := int(n) //nolint:gosec // G115: n is bounded by 64 MiB and len(data) above.
		normalized, err := normalizeSeaweedProto(data[:frameLength], "full")
		if err != nil {
			return nil, err
		}
		out = append(out, sha256.Sum256(normalized))
		data = data[frameLength:]
	}
	sort.Slice(out, func(i, j int) bool { return bytes.Compare(out[i][:], out[j][:]) < 0 })
	return out, nil
}

type seaweedField struct {
	number uint64
	wire   uint64
	value  []byte
}

func readSeaweedField(data []byte) (seaweedField, []byte, error) {
	key, n := binary.Uvarint(data)
	if n <= 0 || key>>3 == 0 || key>>3 > (1<<29)-1 {
		return seaweedField{}, nil, fmt.Errorf("invalid SeaweedFS protobuf field")
	}
	data = data[n:]
	f := seaweedField{number: key >> 3, wire: key & 7}
	switch f.wire {
	case 0:
		v, k := binary.Uvarint(data)
		if k <= 0 {
			return f, nil, fmt.Errorf("truncated SeaweedFS protobuf integer")
		}
		f.value = binary.AppendUvarint(nil, v)
		data = data[k:]
	case 1, 5:
		size := 8
		if f.wire == 5 {
			size = 4
		}
		if len(data) < size {
			return f, nil, fmt.Errorf("truncated SeaweedFS protobuf fixed value")
		}
		f.value = bytes.Clone(data[:size])
		data = data[size:]
	case 2:
		size, k := binary.Uvarint(data)
		if k <= 0 {
			return f, nil, fmt.Errorf("truncated SeaweedFS protobuf bytes")
		}
		remaining := data[k:]
		if size > seaweedMetadataLimit || size > uint64(len(remaining)) {
			return f, nil, fmt.Errorf("invalid SeaweedFS protobuf bytes length")
		}
		count := int(size) //nolint:gosec // G115: size is bounded by 64 MiB and len(remaining).
		f.value = bytes.Clone(remaining[:count])
		data = remaining[count:]
	default:
		return f, nil, fmt.Errorf("unsupported SeaweedFS protobuf wire type")
	}
	return f, data, nil
}
func seaweedChild(kind string, field uint64) string {
	switch kind {
	case "full":
		if field == 2 {
			return "entry"
		}
	case "entry":
		switch field {
		case 3:
			return "chunk"
		case 4:
			return "attributes"
		case 5:
			return "map"
		}
	case "chunk":
		if field == 7 || field == 8 {
			return "fid"
		}
	}
	return ""
}
func normalizeSeaweedProto(data []byte, kind string) ([]byte, error) {
	var fields []seaweedField
	for len(data) > 0 {
		f, rest, err := readSeaweedField(data)
		if err != nil {
			return nil, err
		}
		data = rest
		if kind == "attributes" && (f.number == 22 || f.number == 23) {
			continue
		}
		if child := seaweedChild(kind, f.number); child != "" {
			if f.wire != 2 {
				return nil, fmt.Errorf("invalid SeaweedFS nested metadata")
			}
			f.value, err = normalizeSeaweedProto(f.value, child)
			if err != nil {
				return nil, err
			}
		}
		fields = append(fields, f)
	}
	sort.SliceStable(fields, func(i, j int) bool {
		if fields[i].number != fields[j].number {
			return fields[i].number < fields[j].number
		}
		if kind == "entry" && fields[i].number == 5 {
			return bytes.Compare(fields[i].value, fields[j].value) < 0
		}
		return false
	})
	if kind == "full" && (len(fields) != 2 || fields[0].number != 1 || fields[1].number != 2) {
		return nil, fmt.Errorf("invalid SeaweedFS FullEntry")
	}
	var out []byte
	for _, f := range fields {
		out = binary.AppendUvarint(out, f.number<<3|f.wire)
		if f.wire == 2 {
			out = binary.AppendUvarint(out, uint64(len(f.value)))
		}
		out = append(out, f.value...)
	}
	return out, nil
}
