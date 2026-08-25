# H2 fixture generator

`../txnstore_h2.db` is a REAL store written by H2's own `MVStore` +
`TransactionStore` — the exact stack the Kotlin 1.x launcher used. It exists
because the hand-built synthetic stores in `mvstore_test.go` encode what we
*believed* the wire format was, and the belief was wrong: H2 writes ONE
`VersionedValueType` fast-path flag byte per PAGE, not one operation-id varint
per ENTRY. A synthetic fixture agreed with the reader and both were wrong, so
only a byte stream produced by H2 itself can pin this down.

Regenerate with:

```bash
java -cp ~/.m2/repository/com/h2database/h2/2.4.240/h2-2.4.240.jar \
     Fixture.java /path/to/txnstore_h2.db
```

Contents (all values written through `TransactionMap`, i.e. wrapped in
`VersionedValueType`):

| map                       | entries | covers                                        |
|---------------------------|---------|-----------------------------------------------|
| `entities/ws1!namespace`  | 7       | one leaf page, plus two entries left           |
|                           |         | UNCOMMITTED at close (slow-path flag byte)     |
| `entities/ws1!big`        | 300     | an internal node over many leaf pages          |

The uncommitted entries are what H2 itself discards on
`TransactionStore.init()` (it rolls back open transactions), so the expected
read is the COMMITTED value — `ns3` must come back as its committed JSON, and
`nsNew` must not appear at all.
