import org.h2.mvstore.MVStore;
import org.h2.mvstore.tx.*;
import org.h2.mvstore.type.ByteArrayDataType;
import org.h2.mvstore.type.StringDataType;
import java.nio.charset.StandardCharsets;

/**
 * Writes the fixture the Go MVStore reader is pinned against, using exactly the
 * types the Kotlin 1.x launcher used: StringDataType keys + ByteArrayDataType
 * values, both wrapped by TransactionStore's VersionedValueType.
 * See README.md.
 */
public class Fixture {

    static byte[] b(String s) { return s.getBytes(StandardCharsets.UTF_8); }

    public static void main(String[] args) throws Exception {
        String path = args[0];
        MVStore store = new MVStore.Builder().fileName(path).compress().open();
        TransactionStore ts = new TransactionStore(store);
        ts.init();

        // 1. a handful of entries on a single leaf page, with a non-ASCII value:
        //    ByteArrayDataType length-prefixes BYTES, StringDataType would
        //    length-prefix CHARS, and a Cyrillic namespace name is the shortest
        //    input that tells the two apart.
        Transaction tx = ts.begin();
        TransactionMap<String, byte[]> m = tx.openMap(
                "entities/ws1!namespace", StringDataType.INSTANCE, ByteArrayDataType.INSTANCE);
        for (int i = 0; i < 7; i++) {
            m.put("ns" + i, b("{\"id\":\"ns" + i + "\",\"name\":\"Namespace " + i + "\"}"));
        }
        m.put("nsRu", b("{\"id\":\"nsRu\",\"name\":\"Enterprise - 2026.1 - Транснефть\"}"));
        tx.commit();

        // 2. enough entries to force an internal node over many leaf pages.
        tx = ts.begin();
        TransactionMap<String, byte[]> big = tx.openMap(
                "entities/ws1!big", StringDataType.INSTANCE, ByteArrayDataType.INSTANCE);
        for (int i = 0; i < 300; i++) {
            big.put(String.format("k%04d", i), b("v" + i + "-" + "p".repeat(120)));
        }
        tx.commit();

        // 3. entries left UNCOMMITTED at close, so the page carries
        //    VersionedValueType's slow-path flag byte and per-entry op ids.
        tx = ts.begin();
        TransactionMap<String, byte[]> u = tx.openMap(
                "entities/ws1!namespace", StringDataType.INSTANCE, ByteArrayDataType.INSTANCE);
        u.put("ns3", b("UNCOMMITTED-EDIT"));
        u.put("nsNew", b("UNCOMMITTED-NEW"));
        store.commit();   // flush pages WITHOUT committing the transaction
        store.close();
        System.out.println("fixture written: " + path);
    }
}
