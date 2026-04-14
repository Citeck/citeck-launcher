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
	currentChunkID int // ID of the chunk whose data is being processed
}

// chunkMeta holds parsed chunk header fields.
type chunkMeta struct {
	id              int
	blockStart      int64 // file offset in bytes
	blockCount      int   // number of 4096-byte blocks
	pageCount       int
	compressType    int // 0=none, 1=LZF, 2=deflate
	lenOnDisk       int // length on disk (after header)
	lenDecompressed int
	rootMapPos      int64 // encoded page position: (chunkId << 38) | (offset << 6) | type
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
// The "meta" map (map 0) stores entries like "name.mapName" → "mapId".
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

	// Find map root position from meta
	mapInfo, ok := meta["map."+mapName]
	if !ok {
		// Try finding by name entry
		nameEntry, ok := meta["name."+mapName]
		if !ok {
			return nil, fmt.Errorf("map %q not found in mvstore", mapName)
		}
		mapInfo, ok = meta["map."+nameEntry]
		if !ok {
			return nil, fmt.Errorf("map info for %q not found", mapName)
		}
	}

	// Parse map info to get root page position
	rootPos := parseMapRoot(mapInfo)
	if rootPos <= 0 {
		return nil, nil // empty map
	}

	return s.readPageTree(rootPos)
}

// readMetaMap reads the "meta" map (always map 0, stored in the last chunk).
// decodePagePos extracts chunk ID and offset from an encoded page position.
// H2 MVStore encodes: (chunkId << 38) | (offset << 6) | type
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

func (s *MVStore) readMetaMap() (map[string]string, error) {
	// The file header's "block" field points to the newest chunk.
	// Read it directly instead of scanning all chunks from offset 8192.
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

	// Decode root page position — encoded as (chunkId << 38) | (offset << 6) | type
	rootChunkID, rootOffset := decodePagePos(newestChunk.rootMapPos)

	// Read the chunk that contains the root page (may differ from newest)
	var rootChunk chunkMeta
	if rootChunkID == newestChunk.id {
		rootChunk = newestChunk
	} else {
		// Root page is in a different chunk — find it via the newest chunk's "toc" or scan
		if scanErr := s.scanChunks(); scanErr != nil {
			return nil, scanErr
		}
		found := false
		for _, c := range s.chunks {
			if c.id == rootChunkID {
				rootChunk = c
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("root chunk %d not found", rootChunkID)
		}
	}

	chunkData, err := s.readChunkData(rootChunk)
	if err != nil {
		return nil, fmt.Errorf("read root chunk %d: %w", rootChunkID, err)
	}

	if rootOffset <= 0 || rootOffset >= len(chunkData) {
		return nil, fmt.Errorf("invalid meta root offset: %d (chunk %d, data len %d)", rootOffset, rootChunkID, len(chunkData))
	}

	s.currentChunkID = rootChunkID
	entries, err := s.readLeafPage(chunkData, rootOffset)
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
			c.rootMapPos = hexInt64(val)
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

// readPageTree reads a B-tree page and all its children, returning leaf key-value pairs.
func (s *MVStore) readPageTree(rootPos int64) (map[string][]byte, error) {
	// rootPos encodes chunk ID and offset within chunk
	// Format: position = (chunkId << 38) | (offset << 6) | type
	chunkID := int(rootPos >> 38)
	offset := int((rootPos >> 6) & 0xFFFFFFFF)

	// Find the chunk
	var chunk *chunkMeta
	for i := range s.chunks {
		if s.chunks[i].id == chunkID {
			chunk = &s.chunks[i]
			break
		}
	}
	if chunk == nil {
		return nil, fmt.Errorf("chunk %d not found for position %d", chunkID, rootPos)
	}

	data, err := s.readChunkData(*chunk)
	if err != nil {
		return nil, err
	}

	if offset >= len(data) {
		return nil, fmt.Errorf("page offset %d exceeds chunk data size %d", offset, len(data))
	}

	return s.readLeafPage(data, offset)
}

// readLeafPage reads entries from a leaf page.
// Page format: int32 pageLength | int16 checkValue | varint pageNo | varint mapId | varint keyCount | byte type | ...
func (s *MVStore) readLeafPage(data []byte, offset int) (map[string][]byte, error) {
	result := make(map[string][]byte)

	if offset+6 >= len(data) {
		return result, nil
	}

	pos := offset

	// Page length (4 bytes, big-endian) — includes these 4 bytes
	pageLen := int(binary.BigEndian.Uint32(data[pos:]))
	pos += 4

	if pageLen <= 0 || offset+pageLen > len(data) {
		return result, nil
	}

	pageEnd := offset + pageLen // pageLen includes the 4-byte length field

	// Check value (2 bytes, big-endian short) — skip
	pos += 2

	// Page number (varint) — skip
	_, n, err := readVarInt(data, pos)
	if err != nil {
		return result, nil
	}
	pos += n

	// Map ID (varint) — skip
	_, n, err = readVarInt(data, pos)
	if err != nil {
		return result, nil
	}
	pos += n

	// Key count (varint)
	keyCount, n, err := readVarInt(data, pos)
	if err != nil {
		return result, nil
	}
	pos += n

	// Type byte
	if pos >= pageEnd {
		return result, nil
	}
	typeByte := data[pos]
	pos++

	isLeaf := (typeByte & 1) == 0

	if !isLeaf {
		return s.readInternalNode(data, pos, keyCount, result)
	}

	// Leaf page: read keys and values
	keys := make([]string, keyCount)
	for i := range int(keyCount) {
		str, n, err := readVarString(data, pos)
		if err != nil {
			return result, nil
		}
		pos += n
		keys[i] = str
	}

	// Read values
	for i := 0; i < int(keyCount) && pos < pageEnd; i++ {
		valLen, n, err := readVarInt(data, pos)
		if err != nil {
			break
		}
		pos += n

		vLen := int(valLen)
		if vLen < 0 || pos+vLen > pageEnd {
			break
		}

		value := make([]byte, vLen)
		copy(value, data[pos:pos+vLen])
		pos += vLen

		result[keys[i]] = value
	}

	return result, nil
}

// readInternalNode reads an internal B-tree node and recursively collects all leaf entries.
func (s *MVStore) readInternalNode(data []byte, pos int, keyCount int64, result map[string][]byte) (map[string][]byte, error) {
	// Read child page positions: (keyCount+1) x int64 big-endian
	childCount := int(keyCount) + 1
	childPositions := make([]int64, childCount)
	for i := range childCount {
		if pos+8 > len(data) {
			return result, nil
		}
		childPositions[i] = int64(binary.BigEndian.Uint64(data[pos:])) //nolint:gosec // uint64→int64 is safe for page positions
		pos += 8
	}
	// Skip descendant counts (varlong per child)
	for range childCount {
		_, n, err := readVarInt(data, pos)
		if err != nil {
			return result, nil
		}
		pos += n
	}
	// Recurse into each child — they may be in the same or different chunks
	for _, childPos := range childPositions {
		childChunkID, childOffset := decodePagePos(childPos)
		childData, err := s.loadChunkData(childChunkID, data)
		if err != nil {
			continue
		}
		if childOffset >= 0 && childOffset < len(childData) {
			entries, err := s.readLeafPage(childData, childOffset)
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

// parseMapRoot extracts the root page position from a map info string.
// Format: "root:hexvalue,..." or entries like "root:0x1234"
func parseMapRoot(info string) int64 {
	for part := range strings.SplitSeq(info, ",") {
		if key, val, ok := strings.Cut(part, ":"); ok && key == "root" {
			v, _ := strconv.ParseInt(val, 16, 64)
			return v
		}
	}
	return 0
}
