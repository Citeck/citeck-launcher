import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.net.UnixDomainSocketAddress;
import java.nio.ByteBuffer;
import java.nio.channels.SocketChannel;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.stream.Stream;

/**
 * HotSpot dynamic-attach client, run by the container's OWN jvm.
 *
 * Why this exists in a Go project: the runtime images ship a JRE, so there is no
 * jcmd/jmap/jstack to call, and the launcher cannot always attach from the host —
 * on macOS and Windows the containers live in a VM whose /proc the host cannot
 * reach. The launcher copies this class in (docker cp), runs it with the image's
 * own `java`, and removes it.
 *
 * It is a class file rather than a native helper on purpose: 3 KB and
 * architecture-independent, against ~2 MB per architecture for the smallest
 * possible Go binary (1.2 MB of that is the Go runtime floor, before any of our
 * code). Nothing executable is written into the container either.
 *
 * Only java.base is used, so it runs on any JRE — but UnixDomainSocketAddress
 * requires Java 16+, hence the --release 17 build. The equivalent Go
 * implementation lives in internal/jvmattach (host-side path); the two speak the
 * same protocol and their behavior must stay in step.
 *
 * Usage: java CiteckAttach <pid|0> <command> [arg1] [arg2] [arg3]
 *   pid 0 means "find the jvm in this container".
 */
public final class CiteckAttach {

    /** The only protocol version HotSpot has ever spoken. */
    private static final String PROTOCOL_VERSION = "1";
    /** The jvm reads exactly three argument slots, padding with empty strings. */
    private static final int ARG_SLOTS = 3;
    private static final int LISTENER_WAIT_MS = 5000;
    private static final int POLL_INTERVAL_MS = 100;

    public static void main(String[] args) {
        if (args.length < 2) {
            System.err.println("usage: CiteckAttach <pid|0> <command> [arg1] [arg2] [arg3]");
            System.exit(2);
        }
        try {
            int pid = Integer.parseInt(args[0]);
            if (pid == 0) {
                pid = findJvmPid();
            }
            String[] cmdArgs = new String[Math.max(0, args.length - 2)];
            System.arraycopy(args, 2, cmdArgs, 0, cmdArgs.length);
            System.out.print(execute(pid, args[1], cmdArgs));
        } catch (Exception e) {
            System.err.println(e.getMessage() == null ? e.toString() : e.getMessage());
            System.exit(1);
        }
    }

    /**
     * The jvm is usually NOT pid 1: the images run `sh -c /entrypoint.sh`, which
     * starts java as a child. Match on the executable and not on the command
     * line — this process's own command line contains "java".
     */
    private static int findJvmPid() throws IOException {
        try (Stream<Path> procs = Files.list(Path.of("/proc"))) {
            return procs
                .map(p -> p.getFileName().toString())
                .filter(name -> name.chars().allMatch(Character::isDigit))
                .mapToInt(Integer::parseInt)
                .filter(CiteckAttach::isJvm)
                .min()
                .orElseThrow(() -> new IOException("no jvm process found in this container"));
        }
    }

    private static boolean isJvm(int pid) {
        try {
            Path exe = Path.of("/proc", String.valueOf(pid), "exe").toRealPath();
            return exe.getFileName().toString().equals("java");
        } catch (Exception e) {
            // A process that exited mid-scan, or one owned by another user.
            return false;
        }
    }

    private static String execute(int pid, String command, String[] args) throws Exception {
        Path socket = Path.of("/proc", String.valueOf(pid), "root", "tmp", ".java_pid" + pid);
        if (!Files.exists(socket)) {
            startListener(pid, socket);
        }
        try (SocketChannel channel = SocketChannel.open(UnixDomainSocketAddress.of(socket))) {
            channel.write(ByteBuffer.wrap(request(command, args)));
            return parseResponse(readAll(channel));
        }
    }

    /**
     * With the trigger file in place, SIGQUIT starts the attach listener instead
     * of printing a thread dump to stdout. The file is removed afterwards: left
     * behind, it silently turns a later `kill -3` into a listener start.
     */
    private static void startListener(int pid, Path socket) throws Exception {
        Path trigger = Path.of("/proc", String.valueOf(pid), "root", "tmp", ".attach_pid" + pid);
        boolean created = false;
        try {
            try {
                Files.createFile(trigger);
                created = true;
            } catch (java.nio.file.FileAlreadyExistsException e) {
                // Another attach is in flight; do not remove a trigger we did not create.
            }
            // `kill` is a shell builtin: the images have no /bin/kill, and java
            // cannot send signals by itself.
            Process kill = new ProcessBuilder("sh", "-c", "kill -3 " + pid).start();
            if (kill.waitFor() != 0) {
                throw new IOException("failed to signal pid " + pid);
            }
            long deadline = System.currentTimeMillis() + LISTENER_WAIT_MS;
            while (!Files.exists(socket)) {
                if (System.currentTimeMillis() > deadline) {
                    throw new IOException("attach listener did not start within "
                        + LISTENER_WAIT_MS + "ms (pid " + pid + ")");
                }
                Thread.sleep(POLL_INTERVAL_MS);
            }
        } finally {
            if (created) {
                Files.deleteIfExists(trigger);
            }
        }
    }

    /** version NUL command NUL arg0 NUL arg1 NUL arg2 NUL */
    private static byte[] request(String command, String[] args) {
        StringBuilder request = new StringBuilder(PROTOCOL_VERSION).append('\0').append(command).append('\0');
        for (int i = 0; i < ARG_SLOTS; i++) {
            request.append(i < args.length ? args[i] : "").append('\0');
        }
        return request.toString().getBytes(StandardCharsets.UTF_8);
    }

    private static String readAll(SocketChannel channel) throws IOException {
        ByteArrayOutputStream out = new ByteArrayOutputStream();
        ByteBuffer buffer = ByteBuffer.allocate(65536);
        while (channel.read(buffer) > 0) {
            out.write(buffer.array(), 0, buffer.position());
            buffer.clear();
        }
        return out.toString(StandardCharsets.UTF_8);
    }

    /**
     * The reply is a decimal completion code on its own line followed by the
     * output. A non-zero code is an error, not output: a diagnostics file saying
     * "Unknown command" under a thread-dump heading is worse than one that says
     * the capture failed.
     */
    private static String parseResponse(String raw) throws IOException {
        int newline = raw.indexOf('\n');
        if (newline < 0) {
            throw new IOException("malformed attach response: " + raw);
        }
        String code = raw.substring(0, newline).trim();
        String body = raw.substring(newline + 1);
        if (!code.equals("0")) {
            throw new IOException("attach command failed (code " + code + "): " + body.trim());
        }
        return body;
    }

    private CiteckAttach() {
    }
}
