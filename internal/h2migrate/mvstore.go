package h2migrate

import (
	"encoding/binary"
	"fmt"
	"io"
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
	file   *os.File
	header map[string]string
	chunks []chunkMeta
}

// chunkMeta holds parsed chunk header fields.
type chunkMeta struct {
	id             int
	blockStart     int64 // file offset in bytes
	blockCount     int   // number of 4096-byte blocks
	pageCount      int
	compressType   int // 0=none, 1=LZF, 2=deflate
	lenOnDisk      int // length on disk (after header)
	lenDecompressed int
	rootMapPos     int64 // position of root page within chunk data
}

// pageType constants
const (
	pageTypeLeaf     = 0
	pageTypeInternal = 1
)

// OpenMVStore opens an MVStore file for reading.
func OpenMVStore(path string) (*MVStore, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open mvstore: %w", err)
	}

	s := &MVStore{file: f}
	if err := s.readHeader(); err != nil {
		f.Close()
		return nil, err
	}

	return s, nil
}

// Close releases the file.
func (s *MVStore) Close() error {
	return s.file.Close()
}

// readHeader parses the text header at offset 0.
func (s *MVStore) readHeader() error {
	buf := make([]byte, 4096)
	if _, err := s.file.ReadAt(buf, 0); err != nil {
		return fmt.Errorf("read header: %w", err)
	}

	s.header = parseHeaderBlock(buf)
	if s.header["H"] != "3" {
		// Try backup header
		if _, err := s.file.ReadAt(buf, 4096); err != nil {
			return fmt.Errorf("read backup header: %w", err)
		}
		s.header = parseHeaderBlock(buf)
		if s.header["H"] != "3" {
			return fmt.Errorf("unsupported MVStore format version: %s", s.header["H"])
		}
	}

	return nil
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
	for _, part := range strings.Split(text, ",") {
		kv := strings.SplitN(part, ":", 2)
		if len(kv) == 2 {
			result[kv[0]] = kv[1]
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
		if strings.HasPrefix(k, "name.") {
			names = append(names, strings.TrimPrefix(k, "name."))
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
func (s *MVStore) readMetaMap() (map[string]string, error) {
	if err := s.scanChunks(); err != nil {
		return nil, err
	}

	if len(s.chunks) == 0 {
		return nil, fmt.Errorf("no chunks found in mvstore")
	}

	// Use the last chunk (most recent)
	lastChunk := s.chunks[len(s.chunks)-1]

	// Read meta root from chunk
	chunkData, err := s.readChunkData(lastChunk)
	if err != nil {
		return nil, fmt.Errorf("read last chunk: %w", err)
	}

	// The meta map root is at the position indicated by the chunk header
	if lastChunk.rootMapPos <= 0 || int(lastChunk.rootMapPos) >= len(chunkData) {
		return nil, fmt.Errorf("invalid meta root position: %d", lastChunk.rootMapPos)
	}

	entries, err := s.readLeafPage(chunkData, int(lastChunk.rootMapPos))
	if err != nil {
		return nil, fmt.Errorf("read meta map: %w", err)
	}

	result := make(map[string]string, len(entries))
	for k, v := range entries {
		result[k] = string(v)
	}
	return result, nil
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
		return err
	}
	fileSize := fi.Size()

	// Scan chunk headers starting from block 2 (offset 8192)
	buf := make([]byte, 4096)
	offset := int64(8192)

	for offset < fileSize {
		n, err := s.file.ReadAt(buf, offset)
		if err != nil && err != io.EOF {
			return err
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

		chunk, err := parseChunkHeader(buf[:n])
		if err != nil {
			offset += 4096
			continue
		}
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
func parseChunkHeader(data []byte) (chunkMeta, error) {
	end := len(data)
	for i, b := range data {
		if b == '\n' || b == 0 {
			end = i
			break
		}
	}
	text := string(data[:end])

	var c chunkMeta
	for _, part := range strings.Split(text, ",") {
		kv := strings.SplitN(part, ":", 2)
		if len(kv) != 2 {
			continue
		}
		key, val := kv[0], kv[1]
		switch key {
		case "chunk":
			c.id, _ = strconv.Atoi(val)
		case "block":
			v, _ := strconv.ParseInt(val, 10, 64)
			c.blockStart = v * 4096
		case "blocks":
			c.blockCount, _ = strconv.Atoi(val)
		case "len":
			c.lenOnDisk, _ = strconv.Atoi(val)
		case "lenD":
			c.lenDecompressed, _ = strconv.Atoi(val)
		case "compress":
			c.compressType, _ = strconv.Atoi(val)
		case "pages":
			c.pageCount, _ = strconv.Atoi(val)
		case "root":
			// Root page position is encoded as hex
			v, _ := strconv.ParseInt(val, 16, 64)
			c.rootMapPos = v
		}
	}

	if c.blockCount == 0 {
		c.blockCount = 1
	}

	return c, nil
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
		return nil, err
	}

	// Find end of header line
	headerEnd := 0
	for i, b := range chunkBytes {
		if b == '\n' {
			headerEnd = i + 1
			break
		}
	}

	data := chunkBytes[headerEnd:]

	// Trim to actual data length
	if c.lenOnDisk > 0 && c.lenOnDisk < len(data) {
		data = data[:c.lenOnDisk]
	}

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
// This is a simplified reader that handles the common case of string keys and byte[] values.
func (s *MVStore) readLeafPage(data []byte, offset int) (map[string][]byte, error) {
	result := make(map[string][]byte)

	if offset >= len(data) {
		return result, nil
	}

	pos := offset

	// Read page header
	if pos+4 > len(data) {
		return result, nil
	}

	// Page length (4 bytes, big-endian)
	pageLen := int(binary.BigEndian.Uint32(data[pos:]))
	pos += 4

	if pageLen <= 0 || pos+pageLen > len(data) {
		return result, nil
	}

	pageEnd := pos + pageLen

	// Check byte
	if pos >= pageEnd {
		return result, nil
	}
	checkByte := data[pos]
	pos++

	// Map ID (varint)
	_, n, err := readVarInt(data, pos)
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

	isLeaf := (checkByte & 1) == 0

	if !isLeaf {
		return result, fmt.Errorf("internal B-tree page at offset %d (need leaf); map has grown beyond single-page size — use filesystem fallback", offset)
	}

	// Leaf page: read keys and values
	keys := make([]string, keyCount)
	for i := 0; i < int(keyCount); i++ {
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

// parseMapRoot extracts the root page position from a map info string.
// Format: "root:hexvalue,..." or entries like "root:0x1234"
func parseMapRoot(info string) int64 {
	for _, part := range strings.Split(info, ",") {
		kv := strings.SplitN(part, ":", 2)
		if len(kv) == 2 && kv[0] == "root" {
			v, _ := strconv.ParseInt(kv[1], 16, 64)
			return v
		}
	}
	return 0
}
