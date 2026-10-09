import ru.citeck.ecos.endpoints.lib.provider.EnvEcosEndpointsProvider;
import ru.citeck.ecos.secrets.lib.provider.EnvEcosSecretsProvider;
import io.minio.MinioClient;
import io.minio.BucketExistsArgs;
import io.minio.MakeBucketArgs;
import io.minio.PutObjectArgs;
import io.minio.GetObjectArgs;
import java.io.ByteArrayInputStream;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.util.Arrays;

// Run on the host with the native content environment exported by Launcher.
// Uses the real ECOS providers and MinIO library from the content image.
public class NativeEnvProbe {
  public static void main(String[] args) throws Exception {
    var endpoint = new EnvEcosEndpointsProvider().getEndpoint("content-storage-s3-endpoint");
    if (endpoint == null || !endpoint.getUrl().startsWith("http://127.0.0.1:")) {
      throw new IllegalStateException("Native endpoint is not a host loopback URL");
    }
    var secret = new EnvEcosSecretsProvider().getSecret("content-storage-s3-credentials");
    if (secret == null) throw new IllegalStateException("Native secret was not resolved");
    var basic = secret.getBasicData();
    var minio = MinioClient.builder().endpoint(endpoint.getUrl())
      .credentials(basic.getUsername(), basic.getPassword()).build();
    String bucket = "desktop-local-proof";
    if (!minio.bucketExists(BucketExistsArgs.builder().bucket(bucket).build())) {
      minio.makeBucket(MakeBucketArgs.builder().bucket(bucket).build());
    }
    byte[] expected = "Local content: UTF-8 и бинарные данные\u0000\u0001".getBytes(StandardCharsets.UTF_8);
    String key = "folder/local-content.bin";
    minio.putObject(PutObjectArgs.builder().bucket(bucket).object(key)
      .stream(new ByteArrayInputStream(expected), expected.length, -1).build());
    try (var input = minio.getObject(GetObjectArgs.builder().bucket(bucket).object(key).build())) {
      byte[] actual = input.readAllBytes();
      if (!Arrays.equals(MessageDigest.getInstance("SHA-256").digest(expected),
          MessageDigest.getInstance("SHA-256").digest(actual))) {
        throw new IllegalStateException("S3 content hash mismatch");
      }
    }
    System.out.println("PASS host JVM: native ECOS endpoint/secret providers, localhost S3 upload/readback, SHA256 matched");
  }
}
