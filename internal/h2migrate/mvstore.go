package h2migrate

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"strconv"
	"strings"
)

// MVStore is a read-only parser for H2 MVStore files.
// It reads the binary format enough to extract map names and key-value data.
//
// File layout:
//   - Bytes 0..4095: file header block 1 (text key=value pairs)
//   - Bytes 4096..8191: file header block 2 (backup of block 1)
//   - Bytes 8192+: chunks (each contains B-tree pages)
//
// Reference: org.h2.mvstore.MVStore, org.h2.mvstore.Chunk
type MVStore struct {
	file           *os.File
	header         map[string]string
	chunks         []chunkMeta
	currentChunkID int               // ID of the chunk whose data is being processed
	layout         map[string]string // cached layout map: "meta.id"→hex, "root.<mapId>"→hex pos, "chunk.<id>"→info
}

// chunkMeta holds parsed chunk header fields.
//
// Why the distinction between layoutRootPos and the mapId fields matters:
// the `root` attribute in a chunk header is the root page position of the
// LAYOUT map (see H2 org.h2.mvstore.Chunk#layoutRootPos), not the meta map
// or a per-map data tree. The layout map in turn carries `meta.id` (the
// meta map's id) and `root.<mapId>` entries with each map's data-tree root
// position. Using `root` as if it were a meta or data-map root silently
// hits unrelated leaf bytes and yields zero results.
type chunkMeta struct {
	id              int
	blockStart      int64 // file offset in bytes
	blockCount      int   // number of 4096-byte blocks
	pageCount       int
	compressType    int // 0=none, 1=LZF, 2=deflate
	lenOnDisk       int // length on disk (after header)
	lenDecompressed int
	layoutRootPos   int64 // encoded layout-map root: (chunkId << 38) | (offset << 6) | length-code<<1 | type
	mapID           int   // last allocated map id at chunk write time (chunk `map` field; not a position)
}

// Page type constants for MVStore B-tree format.
// Leaf = 0, Internal = 1. Checked via bitwise operations in readPage().

// OpenMVStore opens an MVStore file for reading.
func OpenMVStore(path string) (*MVStore, error) {
	f, err := os.Open(path) //nolint:gosec // G304: path is an internal H2 database path
	if err != nil {
		return nil, fmt.Errorf("open mvstore: %w", err)
	}

	s := &MVStore{file: f}
	if err := s.readHeader(); err != nil {
		_ = f.Close()
		return nil, err
	}

	return s, nil
}

// Close releases the file.
func (s *MVStore) Close() error {
	if err := s.file.Close(); err != nil {
		return fmt.Errorf("close MVStore: %w", err)
	}
	return nil
}

// readHeader parses the text header at offset 0.
func (s *MVStore) readHeader() error {
	buf := make([]byte, 4096)
	if _, err := s.file.ReadAt(buf, 0); err != nil {
		return fmt.Errorf("read header: %w", err)
	}

	s.header = parseHeaderBlock(buf)
	if !isSupportedMVStoreVersion(s.header["H"]) {
		// Try backup header
		if _, err := s.file.ReadAt(buf, 4096); err != nil {
			return fmt.Errorf("read backup header: %w", err)
		}
		s.header = parseHeaderBlock(buf)
		if !isSupportedMVStoreVersion(s.header["H"]) {
			return fmt.Errorf("unsupported MVStore format version: %s", s.header["H"])
		}
	}

	return nil
}

// isSupportedMVStoreVersion returns true for H:2 and H:3 header versions.
func isSupportedMVStoreVersion(v string) bool {
	return v == "2" || v == "3"
}

// parseHeaderBlock parses "key:value,key:value\n" format from a 4K block.
func parseHeaderBlock(data []byte) map[string]string {
	// Find the null terminator or newline
	end := len(data)
	for i, b := range data {
		if b == 0 || b == '\n' {
			end = i
			break
		}
	}
	text := string(data[:end])

	result := make(map[string]string)
	for part := range strings.SplitSeq(text, ",") {
		if key, val, ok := strings.Cut(part, ":"); ok {
			result[key] = val
		}
	}
	return result
}

// ListMapNames returns all map names stored in the MVStore.
// The meta map stores entries like "name.<mapName>" → "<mapIdHex>".
func (s *MVStore) ListMapNames() ([]string, error) {
	meta, err := s.readMetaMap()
	if err != nil {
		return nil, err
	}

	var names []string
	for k := range meta {
		if after, ok := strings.CutPrefix(k, "name."); ok {
			names = append(names, after)
		}
	}
	return names, nil
}

// ReadMap reads all key-value pairs from a named map.
// Keys and values are returned as raw bytes (typically JSON for values, strings for keys).
func (s *MVStore) ReadMap(mapName string) (map[string][]byte, error) {
	meta, err := s.readMetaMap()
	if err != nil {
		return nil, err
	}

	// name.<mapName> → mapId hex (see H2 MVStore.createMap: meta.put(META_NAME + name, idHex))
	mapIDHex, ok := meta["name."+mapName]
	if !ok {
		return nil, fmt.Errorf("map %q not found in mvstore", mapName)
	}

	// The per-map data-tree root lives in the LAYOUT map under "root.<mapId>",
	// not in meta. Meta's "map.<id>" entry only carries name/createVersion/type
	// (see H2 MVMap.asString); the root pointer is held by FileStore via the
	// layout map (FileStore.writeChunk: layout.put(getMapRootKey, hexRoot)).
	layout, err := s.readLayoutMap()
	if err != nil {
		return nil, err
	}
	rootHex, ok := layout["root."+mapIDHex]
	if !ok {
		return nil, nil // map exists but has no committed root yet
	}
	rootPos, err := strconv.ParseInt(rootHex, 16, 64)
	if err != nil {
		return nil, fmt.Errorf("parse map %q root position %q: %w", mapName, rootHex, err)
	}
	if rootPos <= 0 {
		return nil, nil
	}

	// User-data maps in this store go through H2's TransactionStore which
	// wraps each value with VersionedValueType: `varLong(operationId) ||
	// committedValue`. For operationId == 0 (committed, no in-flight tx) the
	// prefix is a single 0x00 byte before the actual `varInt(len) || bytes`
	// payload. layout and meta maps are NOT transactional — they store raw
	// StringDataType values — so the prefix is only stripped here.
	return s.readPageTreeVersioned(rootPos, true)
}

// decodePagePos extracts chunk ID and offset from an encoded page position.
// Layout: bits 63..38 chunkId, bits 37..6 offset, bits 5..1 length-code, bit 0 type.
func decodePagePos(pos int64) (chunkID, offset int) {
	chunkID = int(pos >> 38)
	offset = int((pos >> 6) & ((1 << 32) - 1))
	return
}

// readChunkAt reads a chunk header at the given file offset and returns parsed metadata.
func (s *MVStore) readChunkAt(offset int64) (chunkMeta, error) {
	buf := make([]byte, 4096)
	if _, err := s.file.ReadAt(buf, offset); err != nil {
		return chunkMeta{}, fmt.Errorf("read chunk at %d: %w", offset, err)
	}
	if string(buf[:6]) != "chunk:" {
		return chunkMeta{}, fmt.Errorf("no chunk header at offset %d", offset)
	}
	c := parseChunkHeader(buf)
	c.blockStart = offset
	return c, nil
}

// readPageAt walks the B-tree at the given encoded page position and returns
// all leaf entries as raw bytes, resolving cross-chunk child pointers via
// findChunk. `versioned` strips the VersionedValueType wrapper before
// returning each value (see ReadMap for the wire-format rationale). The
// caller must ensure scanChunks has been invoked when the initial chunk
// may not yet be cached.
func (s *MVStore) readPageAt(pos int64, versioned bool) (map[string][]byte, error) {
	chunkID, offset := decodePagePos(pos)
	chunk, err := s.findChunk(chunkID)
	if err != nil {
		return nil, err
	}
	data, err := s.readChunkData(chunk)
	if err != nil {
		return nil, fmt.Errorf("read chunk %d data: %w", chunkID, err)
	}
	if offset < 0 || offset >= len(data) {
		return nil, fmt.Errorf("invalid page offset %d in chunk %d (data len %d)", offset, chunkID, len(data))
	}
	prev := s.currentChunkID
	s.currentChunkID = chunkID
	defer func() { s.currentChunkID = prev }()
	return s.readLeafPage(data, offset, versioned)
}

// readLayoutMap returns the layout map cached on first access.
// Why this is separate from meta: in H2 MVStore the chunk header's `root`
// attribute points to the LAYOUT map root, not the meta map. The layout
// map holds the meta map's id (`meta.id`) plus every map's data-tree root
// pointer as `root.<mapId>` entries; meta itself is reached by looking up
// `root.<metaId>` in layout.
func (s *MVStore) readLayoutMap() (map[string]string, error) {
	if s.layout != nil {
		return s.layout, nil
	}

	blockStr := s.header["block"]
	if blockStr == "" {
		return nil, fmt.Errorf("no block field in header")
	}
	blockNum, err := strconv.ParseInt(blockStr, 16, 64)
	if err != nil {
		return nil, fmt.Errorf("parse header block: %w", err)
	}

	newestChunk, err := s.readChunkAt(blockNum * 4096)
	if err != nil {
		return nil, fmt.Errorf("read newest chunk: %w", err)
	}
	// Cache the newest chunk so findChunk() can resolve cross-chunk pointers
	// without a full scan when the layout fits in one chunk.
	if scanErr := s.scanChunks(); scanErr != nil {
		return nil, scanErr
	}

	entries, err := s.readPageAt(newestChunk.layoutRootPos, false)
	if err != nil {
		return nil, fmt.Errorf("read layout map: %w", err)
	}

	layout := make(map[string]string, len(entries))
	for k, v := range entries {
		layout[k] = string(v)
	}
	s.layout = layout
	return layout, nil
}

// readMetaMap reads the meta map: layout.meta.id → metaId, layout.root.<metaId> → meta-map root.
func (s *MVStore) readMetaMap() (map[string]string, error) {
	layout, err := s.readLayoutMap()
	if err != nil {
		return nil, err
	}

	metaIDHex, ok := layout["meta.id"]
	if !ok {
		return nil, fmt.Errorf("layout map missing meta.id entry")
	}
	rootHex, ok := layout["root."+metaIDHex]
	if !ok {
		// An empty store may carry meta.id but no root entry yet.
		return map[string]string{}, nil
	}
	metaRootPos, err := strconv.ParseInt(rootHex, 16, 64)
	if err != nil {
		return nil, fmt.Errorf("parse meta root position %q: %w", rootHex, err)
	}
	if metaRootPos <= 0 {
		return map[string]string{}, nil
	}

	entries, err := s.readPageAt(metaRootPos, false)
	if err != nil {
		return nil, fmt.Errorf("read meta map: %w", err)
	}

	result := make(map[string]string, len(entries))
	for k, v := range entries {
		result[k] = string(v)
	}
	return result, nil
}

// findChunk returns the chunk with the given ID.
// It first checks already-loaded chunks, then tries to read from the file header's "toc" data,
// and finally falls back to a full scan.
func (s *MVStore) findChunk(id int) (chunkMeta, error) {
	for _, c := range s.chunks {
		if c.id == id {
			return c, nil
		}
	}
	// Try full scan if not found
	if err := s.scanChunks(); err != nil {
		return chunkMeta{}, err
	}
	for _, c := range s.chunks {
		if c.id == id {
			return c, nil
		}
	}
	return chunkMeta{}, fmt.Errorf("chunk %d not found", id)
}

// scanChunks finds all chunks in the file by scanning from offset 8192.
func (s *MVStore) scanChunks() error {
	if len(s.chunks) > 0 {
		return nil
	}

	// Reset on entry to avoid partial caches from a previous failed attempt
	s.chunks = nil

	fi, err := s.file.Stat()
	if err != nil {
		return fmt.Errorf("stat MVStore file: %w", err)
	}
	fileSize := fi.Size()

	// Scan chunk headers starting from block 2 (offset 8192)
	buf := make([]byte, 4096)
	offset := int64(8192)

	for offset < fileSize {
		n, err := s.file.ReadAt(buf, offset)
		if err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("read chunk at offset %d: %w", offset, err)
		}
		if n < 2 {
			break
		}

		// Check for chunk header: starts with "chunk:"
		if buf[0] != 'c' || string(buf[:6]) != "chunk:" {
			// Try next block
			offset += 4096
			continue
		}

		chunk := parseChunkHeader(buf[:n])
		chunk.blockStart = offset

		s.chunks = append(s.chunks, chunk)

		// Skip to next chunk
		nextOffset := offset + int64(chunk.blockCount)*4096
		if nextOffset <= offset {
			nextOffset = offset + 4096
		}
		offset = nextOffset
	}

	return nil
}

// parseChunkHeader parses the text header of a chunk.
// Format: "chunk:id,block:N,len:N,map:N,max:N,next:N,pages:N,root:N,time:N,version:N\n"
func parseChunkHeader(data []byte) chunkMeta {
	end := len(data)
	for i, b := range data {
		if b == '\n' || b == 0 {
			end = i
			break
		}
	}
	text := string(data[:end])

	var c chunkMeta
	// All MVStore header values are hex-encoded
	hexInt := func(s string) int {
		v, _ := strconv.ParseInt(s, 16, 64)
		return int(v)
	}
	hexInt64 := func(s string) int64 {
		v, _ := strconv.ParseInt(s, 16, 64)
		return v
	}
	for part := range strings.SplitSeq(text, ",") {
		key, val, ok := strings.Cut(part, ":")
		if !ok {
			continue
		}
		switch key {
		case "chunk":
			c.id = hexInt(val)
		case "block":
			c.blockStart = hexInt64(val) * 4096
		case "blocks":
			c.blockCount = hexInt(val)
		case "len":
			c.lenOnDisk = hexInt(val)
		case "lenD":
			c.lenDecompressed = hexInt(val)
		case "compress":
			c.compressType = hexInt(val)
		case "pages":
			c.pageCount = hexInt(val)
		case "root":
			c.layoutRootPos = hexInt64(val)
		case "map":
			c.mapID = hexInt(val)
		}
	}

	if c.blockCount == 0 {
		c.blockCount = 1
	}

	return c
}

// readChunkData reads and optionally decompresses a chunk's data (after the header line).
func (s *MVStore) readChunkData(c chunkMeta) ([]byte, error) {
	// Read the entire chunk from disk
	const maxChunkBlocks = 4096 // 16MB max per chunk
	if c.blockCount > maxChunkBlocks {
		return nil, fmt.Errorf("chunk %d has %d blocks, exceeds maximum %d", c.id, c.blockCount, maxChunkBlocks)
	}
	chunkBytes := make([]byte, int64(c.blockCount)*4096)
	if _, err := s.file.ReadAt(chunkBytes, c.blockStart); err != nil && err != io.EOF {
		return nil, fmt.Errorf("read chunk %d data: %w", c.id, err)
	}

	// Return full chunk block — page offsets are relative to block start (not data start).
	data := chunkBytes

	// Decompress if needed
	if c.compressType == 1 && c.lenDecompressed > 0 { // LZF
		decompressed, err := decompressLZF(data, c.lenDecompressed)
		if err != nil {
			return nil, fmt.Errorf("lzf decompress chunk %d: %w", c.id, err)
		}
		return decompressed, nil
	}

	return data, nil
}

// readPageTreeVersioned reads a B-tree page and all its children, returning
// leaf key-value pairs. When `versioned` is true the VersionedValueType
// wrapper is stripped from each value.
func (s *MVStore) readPageTreeVersioned(rootPos int64, versioned bool) (map[string][]byte, error) {
	return s.readPageAt(rootPos, versioned)
}

// readLeafPage reads entries from a B-tree page (leaf or internal node).
//
// Page wire format (from H2 org.h2.mvstore.Page#write):
//
//	int32  pageLength   (includes these 4 bytes)
//	int16  checkValue
//	varInt pageNo
//	varInt mapId
//	varInt keyCount
//	byte   type         (bit0: 0=leaf,1=node; bit1: PAGE_COMPRESSED; bit2: PAGE_COMPRESSED_HIGH)
//	[non-leaf] int64×(keyCount+1) child positions, varLong×(keyCount+1) descendant counts
//	[compressed]   varInt lenAdd  (expandedLen - compressedLen)
//	keys (key-type encoded)
//	[leaf] values (value-type encoded)
//
// When PAGE_COMPRESSED is set, the keys+values section (everything after the
// non-leaf children block) is LZF-compressed and must be expanded before
// parsing keys/values.
//
// Value decoding here assumes string-typed maps (layout, meta) — for data
// maps with non-string value types the raw bytes are still returned, which
// preserves the existing (limited) contract for callers like
// migrateRuntimeState that re-base64 the bytes.
func (s *MVStore) readLeafPage(data []byte, offset int, versioned bool) (map[string][]byte, error) {
	result := make(map[string][]byte)

	if offset+6 >= len(data) {
		return result, nil
	}

	pos := offset

	pageLen := int(binary.BigEndian.Uint32(data[pos:]))
	pos += 4
	if pageLen <= 0 || offset+pageLen > len(data) {
		return result, nil
	}
	pageEnd := offset + pageLen

	pos += 2 // check value

	_, n, err := readVarInt(data, pos) // pageNo
	if err != nil {
		return result, nil
	}
	pos += n

	_, n, err = readVarInt(data, pos) // mapID
	if err != nil {
		return result, nil
	}
	pos += n

	keyCount, n, err := readVarInt(data, pos)
	if err != nil {
		return result, nil
	}
	pos += n

	if pos >= pageEnd {
		return result, nil
	}
	typeByte := data[pos]
	pos++

	isLeaf := (typeByte & 1) == 0
	isCompressed := (typeByte & 2) != 0

	if !isLeaf {
		return s.readInternalNode(data, pos, keyCount, pageEnd, isCompressed, versioned, result)
	}

	// Leaf payload: optionally compressed keys + values block.
	payload, err := maybeDecompressPayload(data[pos:pageEnd], isCompressed)
	if err != nil {
		return result, nil
	}

	return parseLeafKeysValues(payload, int(keyCount), versioned, result), nil
}

// maybeDecompressPayload expands the keys+values block when PAGE_COMPRESSED
// is set. Wire format follows H2 org.h2.mvstore.Page#read:
// `varInt(expandedLen - compressedLen) || lzfBytes`.
func maybeDecompressPayload(block []byte, compressed bool) ([]byte, error) {
	if !compressed {
		return block, nil
	}
	lenAdd, n, err := readVarInt(block, 0)
	if err != nil {
		return nil, fmt.Errorf("compressed page lenAdd: %w", err)
	}
	comp := block[n:]
	expanded := len(comp) + int(lenAdd)
	if expanded <= 0 || expanded > 1<<24 {
		return nil, fmt.Errorf("compressed page bogus expanded length %d", expanded)
	}
	out, err := decompressLZF(comp, expanded)
	if err != nil {
		return nil, fmt.Errorf("decompress page: %w", err)
	}
	return out, nil
}

// parseLeafKeysValues reads keyCount string keys followed by keyCount values.
// When `versioned` is true, each value is wrapped in VersionedValueType
// (`varLong(operationId) || committedValue`). For operationId == 0 (the
// committed/no-in-flight-tx case that holds for any cleanly closed store)
// the committed value follows immediately. Non-zero operationId indicates
// an uncommitted version with an undoLog reference — we skip those entries
// to avoid surfacing torn data.
func parseLeafKeysValues(payload []byte, keyCount int, versioned bool, result map[string][]byte) map[string][]byte {
	pos := 0
	keys := make([]string, keyCount)
	for i := range keyCount {
		str, n, err := readVarString(payload, pos)
		if err != nil {
			return result
		}
		pos += n
		keys[i] = str
	}
	for i := 0; i < keyCount && pos < len(payload); i++ {
		if versioned {
			opID, n, err := readVarLong(payload, pos)
			if err != nil {
				break
			}
			pos += n
			if opID != 0 {
				// Uncommitted version: the on-disk shape carries an undoLog
				// reference rather than the committed value. Skip rather
				// than misinterpret bytes from the next value.
				continue
			}
		}
		valLen, n, err := readVarInt(payload, pos)
		if err != nil {
			break
		}
		pos += n
		vLen := int(valLen)
		if vLen < 0 || pos+vLen > len(payload) {
			break
		}
		value := make([]byte, vLen)
		copy(value, payload[pos:pos+vLen])
		pos += vLen
		result[keys[i]] = value
	}
	return result
}

// readInternalNode reads an internal B-tree node and recursively collects all leaf entries.
func (s *MVStore) readInternalNode(data []byte, pos int, keyCount int64, pageEnd int, compressed, versioned bool, result map[string][]byte) (map[string][]byte, error) {
	childCount := int(keyCount) + 1
	childPositions := make([]int64, childCount)
	for i := range childCount {
		if pos+8 > pageEnd {
			return result, nil
		}
		childPositions[i] = int64(binary.BigEndian.Uint64(data[pos:])) //nolint:gosec // uint64→int64 is safe for page positions
		pos += 8
	}
	for range childCount {
		_, n, err := readVarInt(data, pos)
		if err != nil {
			return result, nil
		}
		pos += n
	}

	// Internal nodes also carry keys (used for B-tree routing) in the
	// compressed block following the children. We don't need them to walk
	// the tree exhaustively, so decompress only to validate the wire shape
	// when present, then ignore.
	if _, err := maybeDecompressPayload(data[pos:pageEnd], compressed); err != nil {
		return result, nil
	}

	for _, childPos := range childPositions {
		childChunkID, childOffset := decodePagePos(childPos)
		childData, err := s.loadChunkData(childChunkID, data)
		if err != nil {
			continue
		}
		if childOffset >= 0 && childOffset < len(childData) {
			entries, err := s.readLeafPage(childData, childOffset, versioned)
			if err != nil {
				continue
			}
			maps.Copy(result, entries)
		}
	}
	return result, nil
}

// loadChunkData returns the chunk data for the given chunk ID, reusing the current
// chunk's data buffer when possible.
func (s *MVStore) loadChunkData(chunkID int, currentData []byte) ([]byte, error) {
	if chunkID == s.currentChunkID {
		return currentData, nil
	}
	chunk, err := s.findChunk(chunkID)
	if err != nil {
		return nil, err
	}
	return s.readChunkData(chunk)
}

