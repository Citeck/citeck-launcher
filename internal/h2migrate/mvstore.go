package h2migrate

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"sort"
	"strconv"
	"strings"
)

// MVStore is a read-only parser for H2 MVStore files.
// It reads the binary format enough to extract map names and key-value data.
//
// File layout:
//   - Bytes 0..4095: store header block 1 (text key=value pairs + fletcher32)
//   - Bytes 4096..8191: store header block 2 (independent copy)
//   - Bytes 8192+: chunks (each contains B-tree pages, then a 128-byte footer)
//
// Reference: org.h2.mvstore.MVStore, org.h2.mvstore.Chunk, org.h2.mvstore.Page
type MVStore struct {
	file   *os.File
	header map[string]string
	chunks []chunkMeta
	layout map[string]string // cached layout map: "meta.id"→hex, "root.<mapId>"→hex pos, "chunk.<id>"→info

	// chunkData caches decoded chunk blocks keyed by their file offset. A
	// B-tree walk revisits the same chunk once per child pointer, so without
	// this the reader re-reads (and, for multi-block chunks, re-allocates) the
	// same bytes hundreds of times.
	chunkData      map[int64][]byte
	chunkDataBytes int

	// partialReads records, per user-data map name, what a LENIENT B-tree walk
	// had to drop. Empty when every read was clean. Read back via
	// partialReadSummary — see readLoss for why this exists.
	partialReads map[string]*readLoss
}

const (
	// mvBlockSize is H2's MVStore BLOCK_SIZE.
	mvBlockSize = 4096
	// chunkFooterLength is org.h2.mvstore.Chunk.FOOTER_LENGTH: the trailing
	// 128 bytes of a chunk's last block hold a checksummed copy of the chunk's
	// identity, used to detect a torn write.
	chunkFooterLength = 128
	// maxChunkBlocks caps a single chunk at 16 MB so a corrupt `len:` attribute
	// cannot drive an unbounded allocation.
	maxChunkBlocks = 4096
	// maxChunkCacheBytes bounds the decoded-chunk cache.
	maxChunkCacheBytes = 64 << 20
	// maxExpandedPageBytes bounds a decompressed page payload.
	maxExpandedPageBytes = 1 << 24
	// maxLayoutCandidates bounds the torn-store recovery walk.
	maxLayoutCandidates = 8
)

// Page type flags (org.h2.mvstore.DataUtils).
const (
	pageFlagNode           = 1 // bit 0: 0 = leaf, 1 = internal node
	pageFlagCompressed     = 2 // DataUtils.PAGE_COMPRESSED
	pageFlagCompressedHigh = 4 // PAGE_COMPRESSED_HIGH == PAGE_COMPRESSED | 4
)

// Page payload compression kinds.
const (
	pageCompressNone = iota
	pageCompressLZF
	pageCompressDeflate
)

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
	id int
	// blockStart is the ACTUAL file offset at which the chunk header was
	// found. declaredBlock is the block number the chunk header claims
	// (`block:`) — OPTIONAL: the H2 build that wrote the stores on hand omits
	// it from the chunk header entirely (the position is self-evident) and
	// only records it in the store header. When present, a disagreement marks
	// a stale copy left behind by compactMoveChunks, or a "chunk:"-looking
	// page payload mistaken for a header.
	blockStart       int64
	declaredBlock    int64
	hasDeclaredBlock bool
	blockConflict    bool
	// blockCount is H2 Chunk.len — documented as "the length in number of
	// blocks". There is NO `blocks` attribute in an H2 chunk header; reading
	// `len` as a byte count (and therefore always falling back to a single
	// block) truncates every multi-block chunk to its first 4096 bytes and
	// makes every page beyond that boundary silently unreachable.
	//
	// This is proven on disk rather than inferred: a real store on hand holds
	// `chunk:19f,len:2` at byte 65536, and its checksum-valid footer sits at
	// 65536 + 2*4096 - 128. The footer could only be found there if `len`
	// counts blocks.
	blockCount    int
	pageCount     int
	layoutRootPos int64 // encoded layout-map root: (chunkId << 38) | (offset << 6) | length-code<<1 | type
	mapID         int   // last allocated map id at chunk write time (chunk `map` field; not a position)
	version       int64
	next          int64

	// footerOK is the chunk's self-consistency verdict: the checksummed
	// 128-byte trailer at the end of its last block agrees with its header.
	// Used to prefer the live copy of a chunk id over a stale one and to
	// detect a torn write.
	footerOK bool
}

// OpenMVStore opens an MVStore file for reading.
func OpenMVStore(path string) (*MVStore, error) {
	f, err := os.Open(path) //nolint:gosec // G304: path is an internal H2 database path
	if err != nil {
		return nil, fmt.Errorf("open mvstore: %w", err)
	}

	s := &MVStore{
		file:         f,
		chunkData:    make(map[int64][]byte),
		partialReads: make(map[string]*readLoss),
	}
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

// --- store header ----------------------------------------------------------

// readHeader parses the two 4K store-header blocks, verifies their fletcher32
// checksums and keeps the newest valid one — the same rule H2's own
// FileStore#readStoreHeader applies. Preferring block 0 unconditionally (the
// previous behavior) can pin the reader to a stale header after a crash
// between the two header writes.
func (s *MVStore) readHeader() error {
	var (
		best, newest        map[string]string
		bestVer, newestVer  int64 = -1, -1
		checksumFailures    int
		parsedAtLeastOneBlk bool
	)

	buf := make([]byte, mvBlockSize)
	for block := range 2 {
		if _, err := s.file.ReadAt(buf, int64(block)*mvBlockSize); err != nil && !errors.Is(err, io.EOF) {
			continue
		}
		attrs, checksumOK := parseChecksummedMap(buf)
		if attrs["H"] == "" {
			continue
		}
		parsedAtLeastOneBlk = true
		ver := hexAttr(attrs, "version", -1)
		if ver > newestVer {
			newestVer, newest = ver, attrs
		}
		if !checksumOK {
			checksumFailures++
			continue
		}
		if ver > bestVer {
			bestVer, best = ver, attrs
		}
	}

	switch {
	case best != nil:
		if checksumFailures > 0 {
			slog.Warn("MVStore store-header block failed its fletcher32 checksum — using the intact copy",
				"badBlocks", checksumFailures, "version", bestVer)
		}
		s.header = best
	case parsedAtLeastOneBlk:
		// Neither block verifies. Refusing outright would strand users whose
		// header was written by a slightly different H2 build; proceed with
		// the newer block but say so loudly.
		slog.Warn("MVStore store header failed its checksum in BOTH blocks — proceeding on a best-effort basis",
			"version", newestVer)
		s.header = newest
	default:
		return errors.New("not an H2 MVStore file: no parsable store header in the first two blocks")
	}

	return s.validateFormat()
}

// validateFormat gates on the REAL format attribute.
//
// The `H:` attribute is the MVStore magic constant and is 2 in H2 1.4.x and in
// H2 2.x alike — it is not a format version, so accepting "H:2" told us
// nothing. An H2 1.4.x file (format:1) has no layout map at all (map roots
// lived in meta), so this reader walks it into nonsense and used to surface
// the baffling "layout map missing meta.id entry" instead of naming the cause.
func (s *MVStore) validateFormat() error {
	if h := s.header["H"]; h != "2" {
		return fmt.Errorf("not an H2 MVStore file: header magic is H:%q, expected H:2", h)
	}

	raw := s.header["format"]
	if raw == "" {
		slog.Warn("MVStore store header carries no format attribute — assuming the H2 2.x layout-map format")
		return nil
	}
	format, err := strconv.ParseInt(raw, 16, 64)
	if err != nil {
		return fmt.Errorf("unparsable MVStore format attribute %q: %w", raw, err)
	}
	switch format {
	case 1:
		return errors.New("storage.db was written by H2 1.4.x (MVStore format:1), which stores map roots in " +
			"the meta map and has no layout map; this reader only supports the H2 2.x format (format:2 or " +
			"format:3) used by Citeck Launcher 1.x. Re-open the store once with a 1.x launcher build to " +
			"upgrade it, or send storage.db.kotlin-bak to support")
	case 2, 3:
		return nil
	default:
		return fmt.Errorf("unsupported MVStore format:%d (this reader supports the H2 2.x formats 2 and 3)", format)
	}
}

// parseChecksummedMap parses an H2 "key:hexvalue,...,fletcher:hex" attribute
// block and reports whether the fletcher32 checksum verifies. Mirrors
// org.h2.mvstore.DataUtils#parseChecksummedMap: the checksum covers everything
// before the ",fletcher:" separator.
func parseChecksummedMap(raw []byte) (attrs map[string]string, checksumOK bool) {
	end := len(raw)
	for i, b := range raw {
		if b == 0 || b == '\n' {
			end = i
			break
		}
	}
	text := strings.TrimSpace(string(raw[:end]))
	attrs = parseAttrText(text)

	sumHex, ok := attrs["fletcher"]
	if !ok {
		return attrs, false
	}
	delete(attrs, "fletcher")

	idx := strings.LastIndex(text, "fletcher")
	if idx <= 0 {
		return attrs, false
	}
	want, err := strconv.ParseUint(sumHex, 16, 64)
	if err != nil {
		return attrs, false
	}
	// idx-1 drops the ',' that precedes "fletcher". H2 writes the checksum with
	// Integer.toHexString, so a negative int arrives as 8 hex digits — compare
	// in the wider type rather than truncating the parsed value.
	return attrs, want == uint64(fletcher32([]byte(text[:idx-1])))
}

// parseAttrText splits "key:value,key:value" into a map, trimming the padding
// spaces H2 writes between the last attribute and the terminating newline.
// Without the trim the final attribute (usually `version:`) parses as 0.
func parseAttrText(text string) map[string]string {
	result := make(map[string]string)
	for part := range strings.SplitSeq(text, ",") {
		if key, val, ok := strings.Cut(part, ":"); ok {
			result[strings.TrimSpace(key)] = strings.TrimSpace(val)
		}
	}
	return result
}

func hexAttr(attrs map[string]string, key string, def int64) int64 {
	v, ok := attrs[key]
	if !ok {
		return def
	}
	n, err := strconv.ParseInt(v, 16, 64)
	if err != nil {
		return def
	}
	return n
}

// fletcher32 is a byte-for-byte port of org.h2.mvstore.DataUtils#getFletcher32,
// including its 720-byte reduction interval and odd-length zero padding.
func fletcher32(data []byte) uint32 {
	s1, s2 := uint32(0xffff), uint32(0xffff)
	evenLen := len(data) &^ 1
	i := 0
	for i < evenLen {
		end := min(i+720, evenLen)
		for i < end {
			x := uint32(data[i])<<8 | uint32(data[i+1])
			i += 2
			s1 += x
			s2 += s1
		}
		s1 = (s1 & 0xffff) + (s1 >> 16)
		s2 = (s2 & 0xffff) + (s2 >> 16)
	}
	if len(data)&1 != 0 {
		s1 += uint32(data[len(data)-1]) << 8
		s2 += s1
	}
	s1 = (s1 & 0xffff) + (s1 >> 16)
	s2 = (s2 & 0xffff) + (s2 >> 16)
	return (s2 << 16) | s1
}

// --- public map access -----------------------------------------------------

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
	sort.Strings(names)
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
	//
	// strict is deliberately FALSE for user data maps: a half-readable
	// entities map still lets the migration recover most of a user's
	// workspaces, whereas layout/meta are all-or-nothing (see readLayoutMap).
	// Everything the lenient walk drops is tallied into loss and surfaced by
	// partialReadSummary, so "lenient" no longer means "silent".
	loss := &readLoss{}
	entries, err := s.readPageAt(rootPos, pageOpts{versioned: true, loss: loss})
	s.noteMapLoss(mapName, loss)
	return entries, err
}

// noteMapLoss records (or clears) the per-map partial-read tally, warning once
// per affected map. It is called on EVERY ReadMap, including clean ones, so a
// re-read of a map that now parses cleanly does not leave a stale entry behind.
func (s *MVStore) noteMapLoss(mapName string, loss *readLoss) {
	if !loss.any() {
		delete(s.partialReads, mapName)
		return
	}
	if s.partialReads == nil {
		s.partialReads = make(map[string]*readLoss)
	}
	s.partialReads[mapName] = loss
	slog.Warn("MVStore: user map read was PARTIAL — entries were dropped", //nolint:gosec // G706: map names come from the store being migrated, not from user input
		"map", mapName, "lostEntries", loss.entries, "lostSubtrees", loss.subtrees,
		"defects", loss.defects)
}

// noteMapReadFailure records a map whose read failed OUTRIGHT (unparsable root
// pointer, missing chunk, unreadable chunk data). DumpForImport tolerates that
// with a `continue`, which drops the whole map — the coarsest silent loss there
// is — so it counts as a partial read of the store just like a torn sub-tree.
func (s *MVStore) noteMapReadFailure(mapName string, err error) {
	loss := &readLoss{}
	loss.record(0, 1, err)
	s.noteMapLoss(mapName, loss)
}

// partialRead summarizes every lenient user-map read that had to drop data.
// A nil return from partialReadSummary means every read was clean.
type partialRead struct {
	// Maps names the affected user-data maps, sorted for a stable record.
	Maps []string
	// LostEntries counts individual key/value pairs that could not be decoded.
	LostEntries int
	// LostSubtrees counts B-tree sub-trees (or whole maps) skipped wholesale.
	LostSubtrees int
	// Defects is a bounded, map-qualified sample of the underlying errors.
	Defects []string
}

// partialReadSummary reports what the lenient user-map reads dropped, or nil
// when nothing was dropped. Callers treat a non-nil result as "the store WAS
// readable, but this migration is incomplete" — deliberately distinct from the
// filesystem fallback, where the store could not be opened at all.
func (s *MVStore) partialReadSummary() *partialRead {
	if len(s.partialReads) == 0 {
		return nil
	}
	out := &partialRead{Maps: make([]string, 0, len(s.partialReads))}
	for name := range s.partialReads {
		out.Maps = append(out.Maps, name)
	}
	sort.Strings(out.Maps)
	for _, name := range out.Maps {
		l := s.partialReads[name]
		out.LostEntries += l.entries
		out.LostSubtrees += l.subtrees
		for _, d := range l.defects {
			if len(out.Defects) >= maxRecordedDefects {
				break
			}
			out.Defects = append(out.Defects, name+": "+d)
		}
	}
	return out
}

// decodePagePos extracts chunk ID and offset from an encoded page position.
// Layout: bits 63..38 chunkId, bits 37..6 offset, bits 5..1 length-code, bit 0 type.
func decodePagePos(pos int64) (chunkID, offset int) {
	chunkID = int(pos >> 38)
	offset = int((pos >> 6) & ((1 << 32) - 1))
	return
}

// --- chunks ----------------------------------------------------------------

// readChunkAt reads a chunk header at the given file offset and returns parsed
// metadata, including its self-consistency verdict (footer + block attribute).
func (s *MVStore) readChunkAt(offset int64) (chunkMeta, error) {
	buf := make([]byte, mvBlockSize)
	if _, err := s.file.ReadAt(buf, offset); err != nil && !errors.Is(err, io.EOF) {
		return chunkMeta{}, fmt.Errorf("read chunk at %d: %w", offset, err)
	}
	if string(buf[:6]) != "chunk:" {
		return chunkMeta{}, fmt.Errorf("no chunk header at offset %d", offset)
	}
	c := parseChunkHeader(buf)
	s.finalizeChunk(&c, offset)
	return c, nil
}

// finalizeChunk records where a chunk really sits and cross-checks it against
// what the chunk claims about itself.
func (s *MVStore) finalizeChunk(c *chunkMeta, offset int64) {
	c.blockStart = offset
	c.blockConflict = c.hasDeclaredBlock && c.declaredBlock*mvBlockSize != offset
	c.footerOK = s.verifyChunkFooter(*c)
}

// verifyChunkFooter validates the 128-byte trailer H2 writes at the very end of
// a chunk (org.h2.mvstore.Chunk#getFooterBytes).
//
// Observed shape in the stores this launcher has to read:
//
//	"chunk:<idHex>,len:<blocksHex>,version:<verHex>,fletcher:<sumHex>" + spaces + "\n"
//
// `block:` is optional (some H2 builds emit it, the ones here do not), so every
// attribute other than `chunk` is cross-checked only when present.
//
// A chunk whose footer does not verify was torn by an unclean shutdown — which
// DOES happen in the field (one real store on hand has no `clean:1`) — so the
// verdict is a preference signal used by findChunk/layoutCandidates and a
// stepping guard for scanChunks, never a hard rejection.
func (s *MVStore) verifyChunkFooter(c chunkMeta) bool {
	if c.blockCount <= 0 || c.blockCount > maxChunkBlocks {
		return false
	}
	at := c.blockStart + int64(c.blockCount)*mvBlockSize - chunkFooterLength
	if at < 0 {
		return false
	}
	buf := make([]byte, chunkFooterLength)
	if _, err := s.file.ReadAt(buf, at); err != nil && !errors.Is(err, io.EOF) {
		return false
	}
	attrs, ok := parseChecksummedMap(buf)
	if !ok {
		return false
	}
	if hexAttr(attrs, "chunk", -1) != int64(c.id) {
		return false
	}
	if _, has := attrs["len"]; has && hexAttr(attrs, "len", -1) != int64(c.blockCount) {
		return false
	}
	if _, has := attrs["version"]; has && hexAttr(attrs, "version", -1) != c.version {
		return false
	}
	if _, has := attrs["block"]; has && hexAttr(attrs, "block", -1) != c.blockStart/mvBlockSize {
		return false
	}
	return true
}

// findChunk returns the best copy of the chunk with the given ID.
//
// After H2's compactMoveChunks a stale copy of a chunk id can survive earlier
// in the file, so "first in file order" (the previous rule) can shadow the live
// chunk with dead bytes. Prefer the copy whose footer verifies and whose
// `block:` attribute matches where it actually sits.
func (s *MVStore) findChunk(id int) (chunkMeta, error) {
	if err := s.scanChunks(); err != nil {
		return chunkMeta{}, err
	}
	best := -1
	for i := range s.chunks {
		if s.chunks[i].id != id {
			continue
		}
		if best < 0 || chunkQuality(s.chunks[i]) > chunkQuality(s.chunks[best]) {
			best = i
		}
	}
	if best < 0 {
		return chunkMeta{}, fmt.Errorf("chunk %d not found", id)
	}
	return s.chunks[best], nil
}

func chunkQuality(c chunkMeta) int {
	q := 0
	if !c.blockConflict {
		q += 2
	}
	if c.footerOK {
		q += 4
	}
	return q
}

// scanChunks finds all chunks in the file by scanning from offset 8192.
//
// Stepping by the chunk's true block count matters twice over: it avoids
// re-entering a multi-block chunk's interior (where a page payload that happens
// to start with the bytes "chunk:" would be registered as a phantom chunk) and
// it keeps the walk O(chunks) rather than O(blocks). The block count is only
// trusted for stepping once the chunk's checksummed footer has confirmed it —
// otherwise the scan advances one block at a time so a bogus `len:` from a
// mis-detected header cannot skip real chunks.
func (s *MVStore) scanChunks() error {
	if len(s.chunks) > 0 {
		return nil
	}
	s.chunks = nil

	fi, err := s.file.Stat()
	if err != nil {
		return fmt.Errorf("stat MVStore file: %w", err)
	}
	fileSize := fi.Size()

	buf := make([]byte, mvBlockSize)
	offset := int64(2 * mvBlockSize)

	for offset < fileSize {
		n, err := s.file.ReadAt(buf, offset)
		if err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("read chunk at offset %d: %w", offset, err)
		}
		if n < 6 {
			break
		}
		if string(buf[:6]) != "chunk:" {
			offset += mvBlockSize
			continue
		}

		chunk := parseChunkHeader(buf[:n])
		s.finalizeChunk(&chunk, offset)
		s.chunks = append(s.chunks, chunk)

		step := int64(mvBlockSize)
		if chunk.footerOK && chunk.blockCount > 0 && chunk.blockCount <= maxChunkBlocks {
			step = int64(chunk.blockCount) * mvBlockSize
		}
		offset += step
	}

	return nil
}

// rememberChunk adds a chunk discovered outside scanChunks (e.g. via the store
// header's `block:` pointer) to the cache when the scan missed it.
func (s *MVStore) rememberChunk(c chunkMeta) {
	for _, existing := range s.chunks {
		if existing.blockStart == c.blockStart {
			return
		}
	}
	s.chunks = append(s.chunks, c)
}

// parseChunkHeader parses the text header of a chunk.
// Format: "chunk:id,block:N,len:N,map:N,max:N,next:N,pages:N,root:N,time:N,version:N\n"
// All values are hex; H2 pads the header with spaces before the newline, so
// values are trimmed (an untrimmed trailing-space value silently parses as 0).
func parseChunkHeader(data []byte) chunkMeta {
	end := len(data)
	for i, b := range data {
		if b == '\n' || b == 0 {
			end = i
			break
		}
	}
	attrs := parseAttrText(string(data[:end]))

	_, hasBlock := attrs["block"]
	c := chunkMeta{
		id:               int(hexAttr(attrs, "chunk", 0)),
		declaredBlock:    hexAttr(attrs, "block", 0),
		hasDeclaredBlock: hasBlock,
		// H2 Chunk.len is "the length in number of blocks". There is no
		// `blocks` attribute; reading `len` any other way truncates the chunk.
		blockCount:    int(hexAttr(attrs, "len", 0)),
		pageCount:     int(hexAttr(attrs, "pages", 0)),
		layoutRootPos: hexAttr(attrs, "root", 0),
		mapID:         int(hexAttr(attrs, "map", 0)),
		version:       hexAttr(attrs, "version", 0),
		next:          hexAttr(attrs, "next", 0),
	}

	// Last-resort guard only: a chunk that declares no length at all is
	// assumed to be a single block so the reader still sees its first page.
	if c.blockCount <= 0 {
		c.blockCount = 1
	}

	return c
}

// readChunkData reads a chunk's blocks (header included — page offsets are
// relative to the chunk's block start, not to the end of the header).
//
// There is no whole-chunk compression in the MVStore format: H2 compresses per
// PAGE (see expandPagePayload), and a chunk header carries no `compress`/`lenD`
// attribute, so no decompression happens here.
func (s *MVStore) readChunkData(c chunkMeta) ([]byte, error) {
	if cached, ok := s.chunkData[c.blockStart]; ok {
		return cached, nil
	}
	if c.blockCount > maxChunkBlocks {
		return nil, fmt.Errorf("chunk %d has %d blocks, exceeds maximum %d", c.id, c.blockCount, maxChunkBlocks)
	}
	chunkBytes := make([]byte, int64(c.blockCount)*mvBlockSize)
	if _, err := s.file.ReadAt(chunkBytes, c.blockStart); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("read chunk %d data: %w", c.id, err)
	}

	if s.chunkDataBytes+len(chunkBytes) <= maxChunkCacheBytes {
		s.chunkData[c.blockStart] = chunkBytes
		s.chunkDataBytes += len(chunkBytes)
	}
	return chunkBytes, nil
}

// loadChunkData returns the data for chunkID, reusing the caller's buffer only
// when it demonstrably belongs to the SAME chunk.
//
// The previous implementation compared against a store-level "current chunk"
// field that was only updated at the tree root, so at depth ≥ 2 a grandchild
// could be parsed against a sibling chunk's bytes.
func (s *MVStore) loadChunkData(chunkID, currentChunkID int, currentData []byte) ([]byte, error) {
	if chunkID == currentChunkID {
		return currentData, nil
	}
	chunk, err := s.findChunk(chunkID)
	if err != nil {
		return nil, err
	}
	return s.readChunkData(chunk)
}

// --- layout & meta ---------------------------------------------------------

// headerPointedChunk resolves the store header's `block:` pointer to the chunk
// H2 considered newest at its last successful commit, cross-checking it against
// the header's `chunk:` id and its own footer. Both mismatches are warnings
// rather than rejections: the caller falls back to older chunks anyway, and a
// noisy-but-readable store beats a refused migration.
func (s *MVStore) headerPointedChunk() (chunkMeta, bool) {
	blockNum := hexAttr(s.header, "block", 0)
	if blockNum <= 0 {
		return chunkMeta{}, false
	}
	c, err := s.readChunkAt(blockNum * mvBlockSize)
	if err != nil {
		slog.Warn("MVStore store header points at an unreadable chunk", "block", blockNum, "err", err)
		return chunkMeta{}, false
	}
	if hdrChunk := hexAttr(s.header, "chunk", -1); hdrChunk >= 0 && hdrChunk != int64(c.id) {
		slog.Warn("MVStore store header points at a chunk with a different id", //nolint:gosec // G706: ids read from the store file being migrated, not user input
			"headerChunk", hdrChunk, "foundChunk", c.id, "block", blockNum)
	}
	if !c.footerOK {
		slog.Warn("MVStore newest chunk has an invalid footer (torn write?)", //nolint:gosec // G706: ids read from the store file being migrated, not user input
			"chunk", c.id, "version", c.version, "block", blockNum)
	}
	return c, true
}

// layoutCandidates returns the chunks worth trying as the source of the layout
// map, newest-and-most-trustworthy first: the chunk the store header points at,
// then every scanned chunk that carries a layout root, ordered by footer
// validity and then by version.
func (s *MVStore) layoutCandidates() ([]chunkMeta, error) {
	if err := s.scanChunks(); err != nil {
		return nil, err
	}

	var ordered []chunkMeta
	seen := map[int64]bool{}

	if c, ok := s.headerPointedChunk(); ok {
		ordered = append(ordered, c)
		seen[c.blockStart] = true
		s.rememberChunk(c)
	}

	rest := make([]chunkMeta, 0, len(s.chunks))
	for _, c := range s.chunks {
		if seen[c.blockStart] || c.layoutRootPos == 0 {
			continue
		}
		rest = append(rest, c)
	}
	sort.SliceStable(rest, func(i, j int) bool {
		if rest[i].footerOK != rest[j].footerOK {
			return rest[i].footerOK
		}
		return rest[i].version > rest[j].version
	})

	return append(ordered, rest...), nil
}

// readLayoutMap returns the layout map cached on first access.
//
// Why this is separate from meta: in H2 MVStore the chunk header's `root`
// attribute points to the LAYOUT map root, not the meta map. The layout
// map holds the meta map's id (`meta.id`) plus every map's data-tree root
// pointer as `root.<mapId>` entries; meta itself is reached by looking up
// `root.<metaId>` in layout.
//
// The layout read is STRICT: a partially-read layout is worse than no layout at
// all, because the layout keys sort `chunk.*` < `meta.id` < `root.*` — dropping
// an unreadable tail sub-tree drops exactly `meta.id` and every map root, which
// is how a reader bug used to masquerade as "this store has no data" and send
// the migration down the stub-generating filesystem fallback.
//
// When the newest chunk cannot yield a usable layout (unclean shutdown, torn
// final write) the next-newest valid chunk is tried instead, rather than
// failing the whole store.
func (s *MVStore) readLayoutMap() (map[string]string, error) {
	if s.layout != nil {
		return s.layout, nil
	}

	candidates, err := s.layoutCandidates()
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, errors.New("no chunk with a layout root found in mvstore")
	}

	var firstErr error
	for i, c := range candidates {
		if i >= maxLayoutCandidates {
			break
		}
		layout, err := s.tryLayoutFromChunk(c)
		if err == nil {
			if i > 0 {
				slog.Warn("MVStore: recovered the layout map from an older chunk after the newest one failed",
					"chunk", c.id, "version", c.version, "rejectedCandidates", i)
			}
			s.layout = layout
			return layout, nil
		}
		if firstErr == nil {
			firstErr = err
		}
		slog.Warn("MVStore: layout candidate rejected", "chunk", c.id, "version", c.version, "err", err)
	}
	return nil, fmt.Errorf("read layout map: %w", firstErr)
}

func (s *MVStore) tryLayoutFromChunk(c chunkMeta) (map[string]string, error) {
	entries, err := s.readPageAt(c.layoutRootPos, pageOpts{strict: true})
	if err != nil {
		return nil, err
	}
	layout := make(map[string]string, len(entries))
	for k, v := range entries {
		layout[k] = string(v)
	}
	if _, ok := layout["meta.id"]; !ok {
		return nil, fmt.Errorf("layout map from chunk %d (version %d) carries no meta.id entry", c.id, c.version)
	}
	return layout, nil
}

// readMetaMap reads the meta map: layout.meta.id → metaId, layout.root.<metaId> → meta-map root.
// Strict for the same reason as the layout map.
func (s *MVStore) readMetaMap() (map[string]string, error) {
	layout, err := s.readLayoutMap()
	if err != nil {
		return nil, err
	}

	metaIDHex, ok := layout["meta.id"]
	if !ok {
		return nil, errors.New("layout map missing meta.id entry")
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

	entries, err := s.readPageAt(metaRootPos, pageOpts{strict: true})
	if err != nil {
		return nil, fmt.Errorf("read meta map: %w", err)
	}

	result := make(map[string]string, len(entries))
	for k, v := range entries {
		result[k] = string(v)
	}
	return result, nil
}

// --- pages -----------------------------------------------------------------

// maxRecordedDefects bounds how many distinct defect descriptions one lenient
// read keeps. The COUNT is the actionable part; the messages are a bounded
// sample for the log and the durable degraded-migration record, so a store that
// is corrupt in ten thousand places cannot balloon either one.
const maxRecordedDefects = 5

// readLoss accumulates what a LENIENT (user-data) B-tree walk silently dropped.
//
// It exists because tolerating a defect and being unable to report it are two
// different things, and only the first one was ever intended. pageOpts.reject
// used to swallow every non-strict defect with a slog.Debug and a nil error, so
// readPage → readInternalNode → ReadMap → DumpForImport → Migrate all reported
// success while entire sub-trees of a user's workspaces, namespaces or secrets
// went missing. That is the same silent-data-loss class the strict layout/meta
// read was introduced to kill — still fully reachable one level down, where a
// torn write clips ONE map instead of the whole store.
type readLoss struct {
	// entries counts individual leaf key/value pairs that were dropped.
	entries int
	// subtrees counts B-tree sub-trees (or whole pages) skipped wholesale.
	// Their entry count is unknowable — that is precisely why they were skipped.
	subtrees int
	// defects samples the underlying errors, capped at maxRecordedDefects.
	defects []string
}

// record folds one tolerated defect into the tally. Safe on a nil receiver so
// strict walks (which carry no accumulator) need no call-site guard.
func (l *readLoss) record(entries, subtrees int, err error) {
	if l == nil {
		return
	}
	if entries > 0 {
		l.entries += entries
	}
	if subtrees > 0 {
		l.subtrees += subtrees
	}
	if err != nil && len(l.defects) < maxRecordedDefects {
		l.defects = append(l.defects, err.Error())
	}
}

func (l *readLoss) any() bool {
	return l != nil && (l.entries > 0 || l.subtrees > 0)
}

// pageOpts carries the per-tree behaviors of the B-tree walk.
//
// strict decides what a malformed or unreachable page means. For the LAYOUT and
// META trees it is fatal: those maps are the index of everything else, and a
// silently truncated one turns a reader defect into apparent data loss. For
// USER data maps it stays lenient — recovering nine of ten workspaces beats
// recovering none — but every tolerated gap is now COUNTED into loss so the
// migration can warn about it instead of pretending it was clean.
type pageOpts struct {
	versioned bool
	strict    bool
	// loss accumulates what a lenient walk tolerated. Always nil for strict
	// walks, which tolerate nothing; readLoss.record handles the nil.
	loss *readLoss
}

// reject turns a page-level defect into an error (strict trees) or into a
// counted, logged, tolerated gap (user data trees). The default accounting unit
// is one dropped sub-tree; use rejectEntries when the exact number of lost
// key/value pairs is known.
func (o pageOpts) reject(err error) error {
	return o.rejectLost(0, 1, err)
}

// rejectEntries is reject for a defect whose blast radius is a known number of
// leaf entries rather than an opaque sub-tree.
func (o pageOpts) rejectEntries(n int, err error) error {
	return o.rejectLost(n, 0, err)
}

// rejectLost is the single decision point. The strict branch is byte-identical
// to the original behavior: return the error untouched, record nothing.
func (o pageOpts) rejectLost(entries, subtrees int, err error) error {
	if o.strict {
		return err
	}
	o.loss.record(entries, subtrees, err)
	slog.Debug("MVStore: tolerating a page defect in a user data map",
		"lostEntries", entries, "lostSubtrees", subtrees, "err", err)
	return nil
}

// readPageAt walks the B-tree at the given encoded page position and returns
// all leaf entries as raw bytes, resolving cross-chunk child pointers via
// findChunk.
func (s *MVStore) readPageAt(pos int64, opts pageOpts) (map[string][]byte, error) {
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
	return s.readPage(data, chunkID, offset, opts)
}

// readPage reads entries from a B-tree page (leaf or internal node).
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
// Value decoding here assumes string-typed maps (layout, meta) — for data
// maps with non-string value types the raw bytes are still returned, which
// preserves the existing (limited) contract for callers like
// migrateRuntimeState that re-base64 the bytes.
func (s *MVStore) readPage(data []byte, chunkID, offset int, opts pageOpts) (map[string][]byte, error) {
	result := make(map[string][]byte)

	if offset+6 >= len(data) {
		return result, opts.reject(fmt.Errorf("page header at offset %d truncated (chunk %d, %d bytes available)",
			offset, chunkID, len(data)))
	}

	pos := offset

	pageLen := int(binary.BigEndian.Uint32(data[pos:]))
	pos += 4
	if pageLen <= 0 || offset+pageLen > len(data) {
		return result, opts.reject(fmt.Errorf("page at offset %d in chunk %d declares length %d but only %d bytes are available",
			offset, chunkID, pageLen, len(data)-offset))
	}
	pageEnd := offset + pageLen

	pos += 2 // check value

	pos, err := skipVarInts(data, pos, 2) // pageNo, mapId
	if err != nil {
		return result, opts.reject(fmt.Errorf("page at offset %d in chunk %d: %w", offset, chunkID, err))
	}

	keyCount, n, err := readVarInt(data, pos)
	if err != nil {
		return result, opts.reject(fmt.Errorf("page at offset %d in chunk %d: key count: %w", offset, chunkID, err))
	}
	pos += n

	if pos >= pageEnd {
		return result, opts.reject(fmt.Errorf("page at offset %d in chunk %d: no type byte", offset, chunkID))
	}
	typeByte := data[pos]
	pos++

	compressType := pageCompressKind(typeByte)
	if typeByte&pageFlagNode != 0 {
		return s.readInternalNode(data, chunkID, pos, keyCount, pageEnd, compressType, opts, result)
	}

	payload, err := expandPagePayload(data[pos:pageEnd], compressType)
	if err != nil {
		return result, opts.reject(fmt.Errorf("leaf page at offset %d in chunk %d: %w", offset, chunkID, err))
	}

	return parseLeafKeysValues(payload, int(keyCount), opts, result)
}

func skipVarInts(data []byte, pos, count int) (int, error) {
	for range count {
		_, n, err := readVarInt(data, pos)
		if err != nil {
			return pos, fmt.Errorf("read varint header field: %w", err)
		}
		pos += n
	}
	return pos, nil
}

// pageCompressKind maps a page type byte to its payload compression.
//
// H2's PAGE_COMPRESSED_HIGH is PAGE_COMPRESSED|4 == 6, so a Deflate page also
// has bit 1 set. Testing bit 1 alone (the previous behavior) fed Deflate bytes
// to the LZF expander, which silently produced garbage keys.
func pageCompressKind(typeByte byte) int {
	if typeByte&pageFlagCompressed == 0 {
		return pageCompressNone
	}
	if typeByte&pageFlagCompressedHigh != 0 {
		return pageCompressDeflate
	}
	return pageCompressLZF
}

// expandPagePayload expands the keys+values block when the page is compressed.
// Wire format follows H2 org.h2.mvstore.Page#read:
// `varInt(expandedLen - compressedLen) || compressedBytes`.
func expandPagePayload(block []byte, compressType int) ([]byte, error) {
	if compressType == pageCompressNone {
		return block, nil
	}
	lenAdd, n, err := readVarInt(block, 0)
	if err != nil {
		return nil, fmt.Errorf("compressed page lenAdd: %w", err)
	}
	comp := block[n:]
	expanded := len(comp) + int(lenAdd)
	if expanded <= 0 || expanded > maxExpandedPageBytes {
		return nil, fmt.Errorf("compressed page bogus expanded length %d", expanded)
	}

	switch compressType {
	case pageCompressLZF:
		out, err := decompressLZF(comp, expanded)
		if err != nil {
			return nil, fmt.Errorf("decompress page (lzf): %w", err)
		}
		return out, nil
	case pageCompressDeflate:
		return inflatePagePayload(comp, expanded)
	default:
		return nil, fmt.Errorf("unknown page compression kind %d", compressType)
	}
}

// inflatePagePayload expands a PAGE_COMPRESSED_HIGH payload.
// org.h2.compress.CompressDeflate uses a plain java.util.zip.Deflater, i.e.
// zlib-wrapped deflate, not the raw stream.
func inflatePagePayload(comp []byte, expanded int) ([]byte, error) {
	zr, err := zlib.NewReader(bytes.NewReader(comp))
	if err != nil {
		return nil, fmt.Errorf("decompress page (deflate header): %w", err)
	}
	defer func() { _ = zr.Close() }()

	out := make([]byte, expanded)
	if _, err := io.ReadFull(zr, out); err != nil {
		return nil, fmt.Errorf("decompress page (deflate): %w", err)
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
func parseLeafKeysValues(payload []byte, keyCount int, opts pageOpts, result map[string][]byte) (map[string][]byte, error) {
	pos := 0
	keys := make([]string, keyCount)
	for i := range keyCount {
		str, n, err := readVarString(payload, pos)
		if err != nil {
			// A key that will not parse desynchronizes the whole key block, so
			// no value on this page can be trusted: the page's entire keyCount
			// is lost, not just this one entry.
			return result, opts.rejectEntries(keyCount,
				fmt.Errorf("leaf key %d of %d: %w", i, keyCount, err))
		}
		pos += n
		keys[i] = str
	}
	for i := range keyCount {
		// Values are length-prefixed back to back, so the first unreadable one
		// takes every value after it down with it: keyCount-i entries lost.
		if pos >= len(payload) {
			return result, opts.rejectEntries(keyCount-i,
				fmt.Errorf("leaf payload ends after %d of %d values", i, keyCount))
		}
		if opts.versioned {
			opID, n, err := readVarLong(payload, pos)
			if err != nil {
				return result, opts.rejectEntries(keyCount-i,
					fmt.Errorf("leaf value %d of %d: operation id: %w", i, keyCount, err))
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
			return result, opts.rejectEntries(keyCount-i,
				fmt.Errorf("leaf value %d of %d: length: %w", i, keyCount, err))
		}
		pos += n
		vLen := int(valLen)
		if vLen < 0 || pos+vLen > len(payload) {
			return result, opts.rejectEntries(keyCount-i,
				fmt.Errorf("leaf value %d of %d declares %d bytes, %d remain",
					i, keyCount, vLen, len(payload)-pos))
		}
		value := make([]byte, vLen)
		copy(value, payload[pos:pos+vLen])
		pos += vLen
		result[keys[i]] = value
	}
	return result, nil
}

// readInternalNode reads an internal B-tree node and recursively collects all
// leaf entries.
//
// Every `continue` in here used to drop an entire child sub-tree without a
// trace. In a strict (layout/meta) walk that silence is the whole bug: layout
// keys sort `chunk.*` < `meta.id` < `root.*`, so losing the tail leaf loses
// precisely `meta.id`, and the caller reports "layout map missing meta.id
// entry" — a symptom three layers away from the cause.
func (s *MVStore) readInternalNode(data []byte, chunkID, pos int, keyCount int64, pageEnd, compressType int,
	opts pageOpts, result map[string][]byte,
) (map[string][]byte, error) {
	childCount := int(keyCount) + 1
	childPositions := make([]int64, childCount)
	for i := range childCount {
		if pos+8 > pageEnd {
			return result, opts.reject(fmt.Errorf("node page in chunk %d: truncated at child %d of %d",
				chunkID, i, childCount))
		}
		childPositions[i] = int64(binary.BigEndian.Uint64(data[pos:])) //nolint:gosec // uint64→int64 is safe for page positions
		pos += 8
	}
	newPos, err := skipVarInts(data, pos, childCount)
	if err != nil {
		return result, opts.reject(fmt.Errorf("node page in chunk %d: descendant counts: %w", chunkID, err))
	}
	pos = newPos

	// Internal nodes also carry keys (used for B-tree routing) in the
	// compressed block following the children. We don't need them to walk
	// the tree exhaustively, so decompress only to validate the wire shape
	// when present, then ignore.
	if _, err := expandPagePayload(data[pos:pageEnd], compressType); err != nil {
		return result, opts.reject(fmt.Errorf("node page in chunk %d: routing keys: %w", chunkID, err))
	}

	for _, childPos := range childPositions {
		if err := s.walkChild(childPos, chunkID, data, opts, result); err != nil {
			return result, err
		}
	}
	return result, nil
}

// walkChild resolves one child pointer and merges its sub-tree into result.
// A returned error is always already filtered through opts.reject, so it is
// non-nil only for strict (layout/meta) walks.
func (s *MVStore) walkChild(childPos int64, parentChunkID int, parentData []byte,
	opts pageOpts, result map[string][]byte,
) error {
	if childPos == 0 {
		return opts.reject(fmt.Errorf("node page in chunk %d: child pointer is 0 (unwritten page)", parentChunkID))
	}
	childChunkID, childOffset := decodePagePos(childPos)
	childData, err := s.loadChunkData(childChunkID, parentChunkID, parentData)
	if err != nil {
		return opts.reject(fmt.Errorf("resolve child page in chunk %d: %w", childChunkID, err))
	}
	if childOffset < 0 || childOffset >= len(childData) {
		return opts.reject(fmt.Errorf("child page offset %d out of range in chunk %d (%d bytes)",
			childOffset, childChunkID, len(childData)))
	}
	entries, err := s.readPage(childData, childChunkID, childOffset, opts)
	if err != nil {
		return err
	}
	maps.Copy(result, entries)
	return nil
}
