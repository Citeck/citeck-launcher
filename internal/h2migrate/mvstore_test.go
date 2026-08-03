package h2migrate

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Synthetic MVStore builder.
//
// The real Kotlin-written storage.db files on a developer machine hold private
// data (encrypted secrets, private repo URLs) and must never be checked in, so
// every reader test below hand-crafts the bytes instead. The builder mirrors
// org.h2.mvstore wire formats exactly:
//
//	store header block   : "key:hexval,...,fletcher:hex\n" padded to 4096, twice
//	chunk header         : "chunk:hex,block:hex,len:hex,...\n" at the chunk start
//	chunk footer         : last 128 bytes of the chunk's LAST block
//	page                 : int32 len | int16 check | varInt pageNo | varInt mapId
//	                       | varInt keyCount | byte type | [children] | keys | values
//
// The builder DOES emit a `block:` chunk attribute, which the H2 build that
// wrote the real stores omits — that variant is pinned separately by
// TestRealWorldWireFormats, so both shapes stay covered.
// ---------------------------------------------------------------------------

func putVarUint(b *bytes.Buffer, v uint64) {
	for v >= 0x80 {
		b.WriteByte(byte(v) | 0x80)
		v >>= 7
	}
	b.WriteByte(byte(v))
}

func encodeVarString(b *bytes.Buffer, s string) {
	putVarUint(b, uint64(len(s)))
	b.WriteString(s)
}

// finishPage prepends the 4-byte page length and the 2-byte check value.
func finishPage(body []byte) []byte {
	out := make([]byte, 6+len(body))
	binary.BigEndian.PutUint32(out, uint32(6+len(body)))
	copy(out[6:], body)
	return out
}

// encodeLeaf builds an uncompressed leaf page. When versioned is set each
// value is wrapped the way H2's TransactionStore does it for user maps:
// varLong(operationId=0) || varInt(len) || bytes.
func encodeLeaf(mapID int, kv [][2]string, versioned bool) []byte {
	body := &bytes.Buffer{}
	putVarUint(body, 0) // pageNo
	putVarUint(body, uint64(mapID))
	putVarUint(body, uint64(len(kv)))
	body.WriteByte(0) // type: leaf, uncompressed
	for _, e := range kv {
		encodeVarString(body, e[0])
	}
	for _, e := range kv {
		if versioned {
			putVarUint(body, 0) // operationId
		}
		putVarUint(body, uint64(len(e[1])))
		body.WriteString(e[1])
	}
	return finishPage(body.Bytes())
}

// encodeNode builds an internal B-tree node page pointing at len(children)
// child pages (H2 stores keyCount = len(children)-1 routing keys).
func encodeNode(mapID int, keys []string, children []int64) []byte {
	body := &bytes.Buffer{}
	putVarUint(body, 0) // pageNo
	putVarUint(body, uint64(mapID))
	putVarUint(body, uint64(len(keys)))
	body.WriteByte(1) // type: internal node, uncompressed
	for _, c := range children {
		_ = binary.Write(body, binary.BigEndian, c)
	}
	for range children {
		putVarUint(body, 1) // descendant count
	}
	for _, k := range keys {
		encodeVarString(body, k)
	}
	return finishPage(body.Bytes())
}

// pagePos encodes a page position the way H2's DataUtils.getPagePos does:
// bits 63..38 chunkId, 37..6 offset, 5..1 length code, bit 0 page type.
func pagePos(chunkID, offset int) int64 {
	return int64(chunkID)<<38 | int64(offset)<<6
}

type synthChunk struct {
	id         int
	block      int64 // block number at which the chunk starts
	blocks     int   // `len:` — number of 4096-byte blocks
	version    int64
	layoutRoot int64
	pages      map[int][]byte // offset within the chunk → page bytes

	corruptFooter    bool
	corruptBlockAttr bool
}

func (c synthChunk) headerText() string {
	blockAttr := c.block
	if c.corruptBlockAttr {
		blockAttr = 0xdead
	}
	return fmt.Sprintf("chunk:%x,block:%x,len:%x,map:%x,max:%x,next:%x,pages:%x,root:%x,time:0,version:%x",
		c.id, blockAttr, c.blocks, 1, 0x100, c.block+int64(c.blocks), len(c.pages), c.layoutRoot, c.version)
}

func (c synthChunk) footerBytes() []byte {
	body := fmt.Sprintf("chunk:%x,block:%x,version:%x", c.id, c.block, c.version)
	sum := fletcher32([]byte(body))
	if c.corruptFooter {
		sum++
	}
	full := body + ",fletcher:" + strconv.FormatUint(uint64(sum), 16)
	for len(full) < chunkFooterLength-1 {
		full += " "
	}
	full += "\n"
	return []byte(full)
}

func buildHeaderBlock(fields []string) []byte {
	text := strings.Join(fields, ",")
	sum := fletcher32([]byte(text))
	text += ",fletcher:" + strconv.FormatUint(uint64(sum), 16) + "\n"
	blk := make([]byte, mvBlockSize)
	copy(blk, text)
	return blk
}

// writeSynthStore materializes a complete MVStore file and returns its path.
// `extra` overrides the default store-header attributes; an empty value removes
// the attribute entirely (used to simulate an unclean shutdown: no `clean:1`).
func writeSynthStore(t *testing.T, chunks []synthChunk, newest synthChunk, extra map[string]string) string {
	t.Helper()

	fields := map[string]string{
		"H":       "2",
		"format":  "3",
		"created": "0",
		"clean":   "1",
		"block":   strconv.FormatInt(newest.block, 16),
		"chunk":   strconv.FormatInt(int64(newest.id), 16),
		"version": strconv.FormatInt(newest.version, 16),
	}
	for k, v := range extra {
		if v == "" {
			delete(fields, k)
			continue
		}
		fields[k] = v
	}
	// Deterministic order, H2's own attribute order.
	var parts []string
	for _, k := range []string{"H", "block", "blockSize", "chunk", "created", "clean", "format", "formatRead", "version"} {
		if v, ok := fields[k]; ok {
			parts = append(parts, k+":"+v)
		}
	}

	size := int64(2 * mvBlockSize)
	for _, c := range chunks {
		if end := (c.block + int64(c.blocks)) * mvBlockSize; end > size {
			size = end
		}
	}
	buf := make([]byte, size)
	hdr := buildHeaderBlock(parts)
	copy(buf[0:], hdr)
	copy(buf[mvBlockSize:], hdr)

	for _, c := range chunks {
		start := c.block * mvBlockSize
		copy(buf[start:], c.headerText())
		buf[start+int64(len(c.headerText()))] = '\n'
		for off, page := range c.pages {
			copy(buf[start+int64(off):], page)
		}
		footerAt := (c.block+int64(c.blocks))*mvBlockSize - chunkFooterLength
		copy(buf[footerAt:], c.footerBytes())
	}

	path := filepath.Join(t.TempDir(), "storage.db")
	require.NoError(t, os.WriteFile(path, buf, 0o600))
	return path
}

// ---------------------------------------------------------------------------
// 1. `len:` is a BLOCK count, not a byte length.
// ---------------------------------------------------------------------------

// TestParseChunkHeaderLenIsBlockCount is the core regression test for the
// user-visible "layout map missing meta.id entry" failure: H2's Chunk.len is
// documented as "the length in number of blocks" and there is no `blocks`
// attribute at all, so a header parser that maps `blocks`→blockCount always
// yields 0 → forced to 1 → every chunk truncated to its first 4096 bytes.
//
// The literal header below is taken from a real Kotlin-written store, where
// block(0x10) + len(2) == next(0x12) proves `len` counts blocks.
func TestParseChunkHeaderLenIsBlockCount(t *testing.T) {
	raw := []byte("chunk:19f,block:10,len:2,map:14,max:1a00,next:12,pages:5,root:67e0000000c1,time:2a3b,version:19f\n")
	c := parseChunkHeader(raw)

	assert.Equal(t, 0x19f, c.id)
	assert.Equal(t, 2, c.blockCount, "len:2 must mean two 4096-byte blocks")
	assert.Equal(t, int64(0x10), c.declaredBlock)
	assert.Equal(t, int64(0x12), c.next)
	assert.Equal(t, int64(0x19f), c.version)
	assert.Equal(t, int64(0x67e0000000c1), c.layoutRootPos)
	assert.Equal(t, c.declaredBlock+int64(c.blockCount), c.next,
		"block + len == next is the on-disk invariant that proves len is a block count")
}

// TestRealWorldWireFormats pins the exact byte shapes observed in real
// Kotlin-written stores. These literals carry no user data (chunk ids, block
// counts and versions only), so unlike the stores themselves they can be
// checked in — and they are what keeps the fletcher32 port and the footer
// layout honest without a private fixture.
func TestRealWorldWireFormats(t *testing.T) {
	t.Run("store header checksum", func(t *testing.T) {
		line := "H:2,block:12,blockSize:1000,chunk:1b1,created:19d5eaa9c4f,format:3,version:1b1"
		assert.Equal(t, uint32(0xb1394061), fletcher32([]byte(line)))

		attrs, ok := parseChecksummedMap([]byte(line + ",fletcher:b1394061\n"))
		require.True(t, ok, "a real store header must verify")
		assert.Equal(t, "3", attrs["format"])
		assert.Equal(t, "12", attrs["block"])
	})

	// The footer H2 writes here is "chunk:<id>,len:<blocks>,version:<ver>" —
	// note there is NO `block:` attribute, and `len` reappears as the block
	// count. The footer of the 2-block chunk below was found at
	// 65536 + 2*4096 - 128 in a real file: on-disk proof that len counts blocks.
	t.Run("chunk footer checksum", func(t *testing.T) {
		for body, sum := range map[string]uint32{
			"chunk:1a0,len:1,version:1a0": 0x8d60d34e,
			"chunk:19f,len:2,version:19f": 0xe45a3fff,
			"chunk:1b2,len:1,version:1b2": 0xa36dd750,
		} {
			assert.Equal(t, sum, fletcher32([]byte(body)), body)

			padded := body + ",fletcher:" + strconv.FormatUint(uint64(sum), 16)
			for len(padded) < chunkFooterLength-1 {
				padded += " "
			}
			attrs, ok := parseChecksummedMap([]byte(padded + "\n"))
			require.True(t, ok, "real footer must verify: %s", body)
			assert.NotEmpty(t, attrs["len"])
		}
	})

	// A real chunk HEADER from the same store: no `block:` attribute at all.
	// A parser that requires one, or that treats its absence as a mismatch,
	// distrusts every chunk in every real store.
	t.Run("chunk header without a block attribute", func(t *testing.T) {
		c := parseChunkHeader([]byte(
			"chunk:19f,len:2,pages:7,max:1470,map:30,root:67c000031a16,time:10504d655,version:19f,next:12,toc:112a" +
				strings.Repeat(" ", 24) + "\n"))
		assert.Equal(t, 0x19f, c.id)
		assert.Equal(t, 2, c.blockCount)
		assert.False(t, c.hasDeclaredBlock, "real chunk headers here carry no block: attribute")
		assert.Equal(t, int64(0x19f), c.version)
		assert.Equal(t, int64(0x67c000031a16), c.layoutRootPos)
	})
}

// TestParseChunkHeaderTrimsPadding covers H2's writeChunkHeader, which pads the
// header with spaces up to the reserved length before the newline. Without a
// TrimSpace the padded final attribute parses as 0.
func TestParseChunkHeaderTrimsPadding(t *testing.T) {
	c := parseChunkHeader([]byte("chunk:7,block:4,len:1,pages:2,root:8,version:2b     \n"))
	assert.Equal(t, int64(0x2b), c.version)
	assert.Equal(t, 1, c.blockCount)
}

// TestReadChunkDataReadsEveryBlock: a 2-block chunk must yield 8192 bytes.
// Today blockCount is always 1, so only the first 4096 are ever visible and
// any page living past that boundary is silently unreachable.
func TestReadChunkDataReadsEveryBlock(t *testing.T) {
	c := synthChunk{id: 1, block: 2, blocks: 2, version: 1, pages: map[int][]byte{}}
	path := writeSynthStore(t, []synthChunk{c}, c, nil)

	s, err := OpenMVStore(path)
	require.NoError(t, err)
	defer s.Close()

	meta, err := s.readChunkAt(c.block * mvBlockSize)
	require.NoError(t, err)
	require.Equal(t, 2, meta.blockCount)

	data, err := s.readChunkData(meta)
	require.NoError(t, err)
	assert.Len(t, data, 2*mvBlockSize, "a 2-block chunk must be read whole")
}

// ---------------------------------------------------------------------------
// 2. Pages past the first block of a multi-block chunk.
// ---------------------------------------------------------------------------

// multiBlockStore builds a realistic 2-block store whose layout/meta/data
// pages deliberately sit PAST offset 4096 — exactly the shape that made the
// truncating reader emit "layout map missing meta.id entry".
func multiBlockStore(t *testing.T) string {
	t.Helper()

	const (
		chunkID    = 3
		metaMapID  = 1
		dataMapID  = 5
		layoutOff  = 5000 // second block
		metaOff    = 5600
		dataOff    = 6200
		blockStart = 2
	)

	dataPage := encodeLeaf(dataMapID, [][2]string{{"k1", "v1"}, {"k2", "v2"}}, true)
	metaPage := encodeLeaf(metaMapID, [][2]string{
		{"name.demo", strconv.FormatInt(dataMapID, 16)},
	}, false)
	layoutPage := encodeLeaf(0, [][2]string{
		// Layout keys are sorted: chunk.* < meta.id < root.*
		{"chunk." + strconv.FormatInt(chunkID, 16), "chunk:3,block:2,len:2"},
		{"meta.id", strconv.FormatInt(metaMapID, 16)},
		{"root." + strconv.FormatInt(dataMapID, 16), strconv.FormatInt(pagePos(chunkID, dataOff), 16)},
		{"root." + strconv.FormatInt(metaMapID, 16), strconv.FormatInt(pagePos(chunkID, metaOff), 16)},
	}, false)

	c := synthChunk{
		id: chunkID, block: blockStart, blocks: 2, version: 7,
		layoutRoot: pagePos(chunkID, layoutOff),
		pages: map[int][]byte{
			layoutOff: layoutPage,
			metaOff:   metaPage,
			dataOff:   dataPage,
		},
	}
	return writeSynthStore(t, []synthChunk{c}, c, nil)
}

// TestPagePastFirstBlockIsReadable fails today: the layout page lives at
// offset 5000 of a 2-block chunk, the reader only materializes 4096 bytes,
// readLeafPage's pageLen guard trips and returns an EMPTY map with a nil
// error, and readMetaMap then reports "layout map missing meta.id entry".
func TestPagePastFirstBlockIsReadable(t *testing.T) {
	s, err := OpenMVStore(multiBlockStore(t))
	require.NoError(t, err)
	defer s.Close()

	layout, err := s.readLayoutMap()
	require.NoError(t, err, "layout must be readable when its page sits past block 0")
	assert.Equal(t, "1", layout["meta.id"])

	names, err := s.ListMapNames()
	require.NoError(t, err)
	assert.Equal(t, []string{"demo"}, names)

	entries, err := s.ReadMap("demo")
	require.NoError(t, err)
	assert.Equal(t, map[string][]byte{"k1": []byte("v1"), "k2": []byte("v2")}, entries)
}

// TestScanChunksAdvancesByBlockCount pins the second half of the same defect:
// scanChunks must step over a multi-block chunk in one go instead of walking
// into its interior and registering page payloads as phantom chunks.
func TestScanChunksAdvancesByBlockCount(t *testing.T) {
	s, err := OpenMVStore(multiBlockStore(t))
	require.NoError(t, err)
	defer s.Close()

	require.NoError(t, s.scanChunks())
	assert.Len(t, s.chunks, 1, "a single 2-block chunk must register exactly once")
	assert.Equal(t, 3, s.chunks[0].id)
	assert.True(t, s.chunks[0].footerOK, "the synthetic footer must validate")
}

// ---------------------------------------------------------------------------
// 3. Strict layout/meta reads.
// ---------------------------------------------------------------------------

// brokenLayoutStore points the layout root at an internal node whose child
// lives in a chunk that does not exist. Losing that sub-tree loses the tail of
// the sorted key range — i.e. exactly `meta.id` and the `root.*` entries.
func brokenLayoutStore(t *testing.T) string {
	t.Helper()
	const chunkID = 4
	const goodLeafOff = 1200
	const nodeOff = 1600

	goodLeaf := encodeLeaf(0, [][2]string{{"chunk.4", "x"}}, false)
	node := encodeNode(0, []string{"m"}, []int64{
		pagePos(chunkID, goodLeafOff),
		pagePos(99, 2048), // chunk 99 does not exist
	})

	c := synthChunk{
		id: chunkID, block: 2, blocks: 1, version: 3,
		layoutRoot: pagePos(chunkID, nodeOff),
		pages:      map[int][]byte{goodLeafOff: goodLeaf, nodeOff: node},
	}
	return writeSynthStore(t, []synthChunk{c}, c, nil)
}

// TestStrictLayoutReadFailsLoudly: partial data is never acceptable for the
// layout map. Today the unreadable child is `continue`d and the caller gets a
// silently truncated map plus the misleading "missing meta.id" message.
func TestStrictLayoutReadFailsLoudly(t *testing.T) {
	s, err := OpenMVStore(brokenLayoutStore(t))
	require.NoError(t, err)
	defer s.Close()

	_, err = s.readLayoutMap()
	require.Error(t, err, "an unreadable layout sub-tree must surface as an error")
	assert.NotContains(t, err.Error(), "missing meta.id",
		"the error must name the real cause, not the downstream symptom")
	assert.Contains(t, err.Error(), "chunk 99")
}

// TestLenientUserMapReadKeepsPartialData documents the deliberate asymmetry:
// for a USER data map a partial read still beats no read at all, so the
// lenient path stays in place there.
func TestLenientUserMapReadKeepsPartialData(t *testing.T) {
	const chunkID = 6
	const leafOff = 1200
	const nodeOff = 1700
	const dataMapID = 9
	const metaOff = 2200
	const layoutOff = 2700

	leaf := encodeLeaf(dataMapID, [][2]string{{"present", "yes"}}, true)
	node := encodeNode(dataMapID, []string{"m"}, []int64{
		pagePos(chunkID, leafOff),
		pagePos(99, 2048), // unreachable
	})
	metaPage := encodeLeaf(1, [][2]string{{"name.demo", strconv.FormatInt(dataMapID, 16)}}, false)
	layoutPage := encodeLeaf(0, [][2]string{
		{"meta.id", "1"},
		{"root." + strconv.FormatInt(dataMapID, 16), strconv.FormatInt(pagePos(chunkID, nodeOff), 16)},
		{"root.1", strconv.FormatInt(pagePos(chunkID, metaOff), 16)},
	}, false)

	c := synthChunk{
		id: chunkID, block: 2, blocks: 1, version: 4,
		layoutRoot: pagePos(chunkID, layoutOff),
		pages: map[int][]byte{
			leafOff: leaf, nodeOff: node, metaOff: metaPage, layoutOff: layoutPage,
		},
	}
	s, err := OpenMVStore(writeSynthStore(t, []synthChunk{c}, c, nil))
	require.NoError(t, err)
	defer s.Close()

	entries, err := s.ReadMap("demo")
	require.NoError(t, err, "user maps stay lenient — partial beats nothing")
	assert.Equal(t, map[string][]byte{"present": []byte("yes")}, entries)
}

// ---------------------------------------------------------------------------
// 4. Store-header validation.
// ---------------------------------------------------------------------------

// TestRejectsH2Format1WithActionableError: `H:2` is the MVStore magic in BOTH
// H2 1.4.x and 2.x, so gating on it lets a 1.4.x file through and the failure
// only surfaces later as the baffling "layout map missing meta.id entry".
func TestRejectsH2Format1WithActionableError(t *testing.T) {
	c := synthChunk{id: 1, block: 2, blocks: 1, version: 1, pages: map[int][]byte{}}
	path := writeSynthStore(t, []synthChunk{c}, c, map[string]string{"format": "1"})

	_, err := OpenMVStore(path)
	require.Error(t, err)
	assert.Contains(t, strings.ToLower(err.Error()), "1.4",
		"the error must name the H2 version so the user knows what to do")
}

// TestPrefersHeaderBlockWithHigherVersion mirrors H2's own readStoreHeader:
// the two header blocks are independent 4K copies and the LIVE one is the one
// with the greater `version`, not simply block 0.
func TestPrefersHeaderBlockWithHigherVersion(t *testing.T) {
	c := synthChunk{id: 2, block: 2, blocks: 1, version: 9, pages: map[int][]byte{}}
	path := writeSynthStore(t, []synthChunk{c}, c, nil)

	raw, err := os.ReadFile(path) //nolint:gosec // test fixture
	require.NoError(t, err)
	// Block 0 = an older, still-checksum-valid header pointing at version 1.
	stale := buildHeaderBlock([]string{"H:2", "block:2", "chunk:2", "created:0", "format:3", "version:1"})
	copy(raw[0:], stale)
	require.NoError(t, os.WriteFile(path, raw, 0o600))

	s, err := OpenMVStore(path)
	require.NoError(t, err)
	defer s.Close()
	assert.Equal(t, "9", s.header["version"], "the newer header block must win")
}

// TestRejectsHeaderWithBadChecksum: a torn block-0 header must not be trusted
// when block 1 is intact.
func TestFallsBackToIntactHeaderBlock(t *testing.T) {
	c := synthChunk{id: 2, block: 2, blocks: 1, version: 5, pages: map[int][]byte{}}
	path := writeSynthStore(t, []synthChunk{c}, c, nil)

	raw, err := os.ReadFile(path) //nolint:gosec // test fixture
	require.NoError(t, err)
	// Corrupt block 0's payload while leaving its (now wrong) fletcher in place.
	copy(raw[0:], "H:2,block:ff,chunk:ff,created:0,format:3,version:ffff,fletcher:1\n")
	require.NoError(t, os.WriteFile(path, raw, 0o600))

	s, err := OpenMVStore(path)
	require.NoError(t, err)
	defer s.Close()
	assert.Equal(t, "5", s.header["version"], "the checksum-valid backup block must win")
}

// ---------------------------------------------------------------------------
// 5. Torn newest chunk → fall back to the newest VALID chunk.
// ---------------------------------------------------------------------------

// TestTornNewestChunkFallsBackToOlderValidChunk: real stores are found in the
// field without `clean:1` (unclean shutdown). When the chunk the store header
// points at cannot yield a layout, the reader must retry the next-newest valid
// chunk rather than give up on the whole database.
func TestTornNewestChunkFallsBackToOlderValidChunk(t *testing.T) {
	const oldID, newID = 1, 2
	const layoutOff, metaOff = 1200, 1700

	metaPage := encodeLeaf(1, [][2]string{{"name.demo", "5"}}, false)
	layoutPage := encodeLeaf(0, [][2]string{
		{"meta.id", "1"},
		{"root.1", strconv.FormatInt(pagePos(oldID, metaOff), 16)},
	}, false)
	good := synthChunk{
		id: oldID, block: 2, blocks: 1, version: 1,
		layoutRoot: pagePos(oldID, layoutOff),
		pages:      map[int][]byte{layoutOff: layoutPage, metaOff: metaPage},
	}
	// The newest chunk's layout root points into empty space: torn write.
	torn := synthChunk{
		id: newID, block: 3, blocks: 1, version: 2,
		layoutRoot:    pagePos(newID, 3000),
		pages:         map[int][]byte{},
		corruptFooter: true,
	}

	path := writeSynthStore(t, []synthChunk{good, torn}, torn, map[string]string{"clean": ""})
	s, err := OpenMVStore(path)
	require.NoError(t, err)
	defer s.Close()

	layout, err := s.readLayoutMap()
	require.NoError(t, err, "a torn newest chunk must not sink the whole store")
	assert.Equal(t, "1", layout["meta.id"])

	names, err := s.ListMapNames()
	require.NoError(t, err)
	assert.Equal(t, []string{"demo"}, names)
}

// TestFindChunkPrefersValidCopy: after H2's compactMoveChunks a stale copy of
// a chunk id can still be present earlier in the file. The live copy is the
// one whose `block:` attribute matches where it actually sits and whose footer
// validates — "first in file order" is not a safe rule.
func TestFindChunkPrefersValidCopy(t *testing.T) {
	stale := synthChunk{
		id: 8, block: 2, blocks: 1, version: 1, pages: map[int][]byte{},
		corruptBlockAttr: true, corruptFooter: true,
	}
	live := synthChunk{id: 8, block: 3, blocks: 1, version: 4, pages: map[int][]byte{}}

	path := writeSynthStore(t, []synthChunk{stale, live}, live, nil)
	s, err := OpenMVStore(path)
	require.NoError(t, err)
	defer s.Close()

	c, err := s.findChunk(8)
	require.NoError(t, err)
	assert.Equal(t, int64(3*mvBlockSize), c.blockStart, "the self-consistent copy must win")
	assert.True(t, c.footerOK)
}

// ---------------------------------------------------------------------------
// 6. Page compression variants.
// ---------------------------------------------------------------------------

// TestDeflateCompressedPageIsNotMisreadAsLZF: H2's PAGE_COMPRESSED_HIGH is
// 2|4, so a Deflate page also has bit 1 set. Testing bit 1 alone feeds Deflate
// bytes to the LZF expander, which produces garbage or a bogus error.
func TestDeflateCompressedPageIsNotMisreadAsLZF(t *testing.T) {
	// Big and repetitive so the compressed form is genuinely shorter and
	// lenAdd is a normal positive varInt, exactly as H2 writes it.
	plain := bytes.Repeat([]byte("root.1a:67e0000000c1;"), 200)
	comp := deflateForTest(t, plain)
	require.Less(t, len(comp), len(plain))

	block := &bytes.Buffer{}
	putVarUint(block, uint64(len(plain)-len(comp))) // lenAdd
	block.Write(comp)

	out, err := expandPagePayload(block.Bytes(), pageCompressDeflate)
	require.NoError(t, err, "Deflate pages must be inflated, not fed to LZF")
	assert.Equal(t, plain, out)

	// And the type-byte classifier must route it there in the first place:
	// PAGE_COMPRESSED_HIGH is 2|4, so bit 1 alone cannot distinguish it.
	assert.Equal(t, pageCompressDeflate, pageCompressKind(pageFlagCompressed|pageFlagCompressedHigh))
	assert.Equal(t, pageCompressLZF, pageCompressKind(pageFlagCompressed))
	assert.Equal(t, pageCompressNone, pageCompressKind(0))
}

// deflateForTest mirrors org.h2.compress.CompressDeflate, which uses a plain
// java.util.zip.Deflater — i.e. zlib-wrapped, not raw deflate.
func deflateForTest(t *testing.T, plain []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	_, err := w.Write(plain)
	require.NoError(t, err)
	require.NoError(t, w.Close())
	return buf.Bytes()
}

// ---------------------------------------------------------------------------
// 7. LZF hardening.
// ---------------------------------------------------------------------------

// TestDecompressLZFTruncatedLiteralDoesNotPanic: decompressLiteral slices the
// input with no bounds check, so a truncated page panics the daemon instead of
// falling back to the filesystem migration.
func TestDecompressLZFTruncatedLiteralDoesNotPanic(t *testing.T) {
	// ctrl=0x04 announces a 5-byte literal run but only 2 bytes follow.
	input := []byte{0x04, 'a', 'b'}
	require.NotPanics(t, func() {
		_, err := decompressLZF(input, 5)
		assert.Error(t, err)
	})
}

func TestDecompressLZFLiteralOverflowingOutputErrors(t *testing.T) {
	input := []byte{0x1f, 'a', 'b', 'c', 'd', 'e', 'f', 'g', 'h', 'i', 'j',
		'k', 'l', 'm', 'n', 'o', 'p', 'q', 'r', 's', 't', 'u', 'v', 'w',
		'x', 'y', 'z', '0', '1', '2', '3', '4'}
	require.NotPanics(t, func() {
		_, err := decompressLZF(input, 4)
		assert.Error(t, err)
	})
}

// ---------------------------------------------------------------------------
// 8. Opt-in corpus test over REAL Kotlin-written stores.
//
// The real files hold the user's private data and are never checked in. Point
// CITECK_H2_CORPUS at a colon-separated list of storage.db paths to run this
// as a regression net over the currently-working path.
// ---------------------------------------------------------------------------

func TestRealStoreCorpus(t *testing.T) {
	spec := os.Getenv("CITECK_H2_CORPUS")
	if spec == "" {
		t.Skip("set CITECK_H2_CORPUS=/path/a.db:/path/b.db to run the real-store corpus")
	}
	for path := range strings.SplitSeq(spec, ":") {
		if path == "" {
			continue
		}
		t.Run(filepath.Base(filepath.Dir(path))+"_"+filepath.Base(path), func(t *testing.T) {
			if _, err := os.Stat(path); err != nil {
				t.Skipf("corpus file absent: %v", err)
			}
			s, err := OpenMVStore(path)
			require.NoError(t, err)
			defer s.Close()

			dump, err := s.DumpForImport()
			require.NoError(t, err)
			require.Empty(t, dumpFallbackReason(dump, err),
				"a real Kotlin store must never take the filesystem fallback")

			// Nor may it take the PARTIAL-read path: a lenient user-map walk
			// that quietly drops a sub-tree now marks the migration degraded,
			// so a regression in the page/leaf decoder shows up here as a
			// named map instead of as missing rows in a user's launcher.
			if pr := s.partialReadSummary(); pr != nil {
				t.Errorf("real store read was PARTIAL: %d entries and %d sub-trees lost in maps %v; defects: %v",
					pr.LostEntries, pr.LostSubtrees, pr.Maps, pr.Defects)
			}

			names := make([]string, 0, len(dump))
			entries := 0
			for n, m := range dump {
				names = append(names, n)
				entries += len(m)
			}
			sort.Strings(names)
			t.Logf("format=%s clean=%q chunks=%d maps=%d entries=%d %v",
				s.header["format"], s.header["clean"], len(s.chunks), len(dump), entries, names)

			var multi, footerBad int
			for _, c := range s.chunks {
				if c.blockCount > 1 {
					multi++
				}
				if !c.footerOK {
					footerBad++
				}
			}
			t.Logf("chunks: total=%d multiBlock=%d footerInvalid=%d", len(s.chunks), multi, footerBad)
			assert.Zero(t, footerBad,
				"every chunk of a real store must pass footer validation — a non-zero count means the "+
					"footer parser is wrong and would spam a false 'torn write' warning")
		})
	}
}
