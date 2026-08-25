package h2migrate

import (
	"fmt"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// realTxnStore is a store written by H2's own MVStore + TransactionStore —
// see testdata/gen/README.md for what it contains and how to regenerate it.
const realTxnStore = "testdata/txnstore_h2.db"

// TestTransactionalMapReadsEveryEntry is the regression net for the migration
// bug that shipped in 2.x: H2's VersionedValueType writes ONE fast-path flag
// byte for a page's whole value block and then the committed values back to
// back, while the reader expected a per-entry operation-id varint. The first
// entry of every leaf decoded (its value's length varint was mistaken for a
// non-zero operation id on entry two, which the reader silently skipped), so a
// user with 19 namespaces migrated exactly one per page — with no error, no
// partial-read tally and no warning anywhere.
func TestTransactionalMapReadsEveryEntry(t *testing.T) {
	s, err := OpenMVStore(realTxnStore)
	require.NoError(t, err)
	defer s.Close()

	t.Run("single leaf page", func(t *testing.T) {
		entries, err := s.ReadMap("entities/ws1!namespace")
		require.NoError(t, err)
		require.Len(t, entries, 8, "every committed entry must survive the read")
		for i := range 7 {
			key := fmt.Sprintf("ns%d", i)
			assert.JSONEq(t,
				fmt.Sprintf(`{"id":"ns%d","name":"Namespace %d"}`, i, i),
				string(entries[key]), "value for %s", key)
		}
	})

	t.Run("value length is bytes, not characters", func(t *testing.T) {
		// ByteArrayDataType length-prefixes BYTES. Reading that prefix as a
		// character count (StringDataType's shape) would truncate every value
		// holding non-ASCII — and real namespace names here are Russian.
		entries, err := s.ReadMap("entities/ws1!namespace")
		require.NoError(t, err)
		assert.JSONEq(t, `{"id":"nsRu","name":"Enterprise - 2026.1 - Транснефть"}`,
			string(entries["nsRu"]))
	})

	t.Run("internal node over many leaves", func(t *testing.T) {
		entries, err := s.ReadMap("entities/ws1!big")
		require.NoError(t, err)
		require.Len(t, entries, 300)
		for i := range 300 {
			key := fmt.Sprintf("k%04d", i)
			require.Contains(t, entries, key)
		}
	})

	t.Run("read is clean, not merely tolerated", func(t *testing.T) {
		// A read that dropped entries but recorded the loss would still be a
		// bug; assert the reader claims — truthfully — that nothing was lost.
		assert.Nil(t, s.partialReadSummary())
	})
}

// TestUncommittedEntryFallsBackToItsCommittedValue pins the slow path. H2
// rolls open transactions back on TransactionStore.init(), so the value a
// 1.x launcher would have shown after a crash is the COMMITTED one — an
// entry that only ever existed inside the open transaction must not appear.
func TestUncommittedEntryFallsBackToItsCommittedValue(t *testing.T) {
	s, err := OpenMVStore(realTxnStore)
	require.NoError(t, err)
	defer s.Close()

	entries, err := s.ReadMap("entities/ws1!namespace")
	require.NoError(t, err)

	assert.JSONEq(t, `{"id":"ns3","name":"Namespace 3"}`, string(entries["ns3"]),
		"an entry edited by a still-open transaction must read back as its committed value")
	assert.NotContains(t, entries, "nsNew",
		"a key created only inside a still-open transaction was never committed")
}

// TestDumpForImportKeepsEveryEntry walks the same store through the layer the
// migration actually calls, so a future regression cannot hide behind
// DumpForImport's own filtering.
func TestDumpForImportKeepsEveryEntry(t *testing.T) {
	s, err := OpenMVStore(realTxnStore)
	require.NoError(t, err)
	defer s.Close()

	dump, err := s.DumpForImport()
	require.NoError(t, err)
	require.Empty(t, dumpFallbackReason(dump, err))

	assert.Len(t, dump["entities/ws1!namespace"], 8)
	assert.Len(t, dump["entities/ws1!big"], 300)
}

// TestMalformedKeyLengthDoesNotExhaustMemory pins the blast radius of a bad
// varint. readVarString sized its buffer from the DECLARED character count, so
// a page whose key block is misaligned (or simply corrupt) could ask for a
// multi-terabyte allocation and take the whole daemon down with a Go runtime
// "out of memory" fatal error — unrecoverable, so the lenient user-map walk
// never got its chance to tolerate the defect. Reproduced against a real store
// by reading H2's own undoLog map, whose value type is not ByteArrayDataType.
func TestMalformedKeyLengthDoesNotExhaustMemory(t *testing.T) {
	// varInt for 0x10000000000 (1 TiB) characters, followed by four bytes.
	payload := []byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x20, 'a', 'b', 'c', 'd'}

	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	_, _, err := readVarString(payload, 0)
	runtime.ReadMemStats(&after)

	require.Error(t, err, "a length that overruns the payload is corrupt by construction")
	assert.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(1<<20),
		"the buffer must be sized from the bytes actually available, not from the declared count")
}
