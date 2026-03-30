package ru.citeck.launcher.migrate;

import org.h2.mvstore.MVMap;
import org.h2.mvstore.MVStore;
import org.h2.mvstore.tx.Transaction;
import org.h2.mvstore.tx.TransactionMap;
import org.h2.mvstore.tx.TransactionStore;

import java.io.*;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.Paths;
import java.util.*;

/**
 * Exports H2 MVStore data (storage.db) to JSON for Go launcher migration.
 * Uses TransactionStore to read data through the transaction layer.
 * <p>
 * Usage: java -jar h2-export.jar storage.db [output.json]
 * <p>
 * Output: JSON with base64-encoded values preserving encrypted secrets as-is.
 */
public class H2Export {

    public static void main(String[] args) {
        if (args.length < 1) {
            System.err.println("Usage: java -jar h2-export.jar <storage.db> [output.json]");
            System.exit(1);
        }

        Path dbPath = Paths.get(args[0]);
        if (!Files.exists(dbPath)) {
            System.err.println("ERROR: " + dbPath + " not found");
            System.exit(1);
        }

        Path out = args.length >= 2 ? Paths.get(args[1])
                : dbPath.getParent().resolve("h2-export.json");

        MVStore store = null;
        try {
            store = new MVStore.Builder()
                    .fileName(dbPath.toAbsolutePath().toString())
                    .readOnly()
                    .open();

            TransactionStore txStore = new TransactionStore(store);
            txStore.init();

            // Rollback any open transactions (crash recovery)
            for (Transaction t : txStore.getOpenTransactions()) {
                try { t.rollback(); } catch (Exception ignored) {}
            }

            Transaction tx = txStore.begin();
            StringBuilder sb = new StringBuilder();
            sb.append("{\"version\":1,\"maps\":{");

            Set<String> names = new TreeSet<>(store.getMapNames());
            boolean firstMap = true;
            int totalEntries = 0;

            for (String name : names) {
                // Skip internal/undoLog maps
                if (name.startsWith("undoLog") || name.startsWith("openTransactions")) continue;

                try {
                    TransactionMap<String, byte[]> map = tx.openMap(name);
                    if (map.sizeAsLong() == 0) continue;

                    if (!firstMap) sb.append(',');
                    firstMap = false;
                    sb.append(jsonStr(name)).append(":{");

                    boolean firstEntry = true;
                    for (Map.Entry<String, byte[]> e : map.entrySet()) {
                        if (!firstEntry) sb.append(',');
                        firstEntry = false;
                        sb.append(jsonStr(e.getKey())).append(':');
                        sb.append('"').append(Base64.getEncoder().encodeToString(e.getValue())).append('"');
                        totalEntries++;
                    }
                    sb.append('}');
                    System.err.println("  " + name + ": " + map.sizeAsLong() + " entries");
                } catch (Exception e) {
                    System.err.println("  SKIP " + name + ": " + e.getMessage());
                }
            }

            sb.append("}}");
            tx.commit();

            try (OutputStream os = new FileOutputStream(out.toFile())) {
                os.write(sb.toString().getBytes(StandardCharsets.UTF_8));
            }

            System.err.println("OK: " + out + " (" + totalEntries + " entries)");
        } catch (Exception e) {
            System.err.println("ERROR: " + e.getMessage());
            System.exit(2);
        } finally {
            if (store != null && !store.isClosed()) store.close();
        }
    }

    private static String jsonStr(String s) {
        StringBuilder sb = new StringBuilder("\"");
        for (char c : s.toCharArray()) {
            switch (c) {
                case '"': sb.append("\\\""); break;
                case '\\': sb.append("\\\\"); break;
                case '\n': sb.append("\\n"); break;
                case '\r': sb.append("\\r"); break;
                case '\t': sb.append("\\t"); break;
                default:
                    if (c < 0x20) sb.append(String.format("\\u%04x", (int) c));
                    else sb.append(c);
            }
        }
        return sb.append('"').toString();
    }
}
