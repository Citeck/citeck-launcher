package ru.citeck.launcher.cli.acme

import com.fasterxml.jackson.module.kotlin.jacksonObjectMapper
import com.fasterxml.jackson.module.kotlin.readValue
import io.github.oshai.kotlinlogging.KotlinLogging
import io.ktor.client.HttpClient
import io.ktor.client.plugins.HttpTimeout
import io.ktor.client.request.get
import io.ktor.client.request.head
import io.ktor.client.request.post
import io.ktor.client.request.setBody
import io.ktor.client.statement.bodyAsText
import io.ktor.http.ContentType
import io.ktor.http.HttpStatusCode
import io.ktor.http.contentType
import io.ktor.http.isSuccess
import io.ktor.server.engine.embeddedServer
import io.ktor.server.response.respond
import io.ktor.server.response.respondText
import io.ktor.server.routing.get
import io.ktor.server.routing.routing
import kotlinx.coroutines.runBlocking
import ru.citeck.launcher.core.utils.TlsCertGenerator
import java.nio.file.Path
import java.security.*
import java.security.spec.PKCS8EncodedKeySpec
import java.security.spec.X509EncodedKeySpec
import java.util.Base64
import kotlin.io.path.exists
import kotlin.io.path.readText
import kotlin.io.path.writeText
import io.ktor.client.engine.cio.CIO as ClientCIO
import io.ktor.server.cio.CIO as ServerCIO

data class AcmeResult(
    val success: Boolean,
    val certPath: String = "",
    val keyPath: String = "",
    val error: String? = null
)

object AcmeClient {

    private val log = KotlinLogging.logger {}
    private val mapper = jacksonObjectMapper()
    private val b64url = Base64.getUrlEncoder().withoutPadding()

    private const val LE_DIRECTORY = "https://acme-v02.api.letsencrypt.org/directory"
    private const val LE_STAGING_DIRECTORY = "https://acme-staging-v02.api.letsencrypt.org/directory"
    private const val PROFILE_SHORT_LIVED = "shortlived"
    private const val MAX_BAD_NONCE_RETRIES = 3

    fun obtainCertificate(hostname: String, email: String?, targetDir: Path, acmeDir: Path): AcmeResult {
        return try {
            doObtain(hostname, email, targetDir, acmeDir, LE_DIRECTORY)
        } catch (e: Exception) {
            log.error(e) { "ACME certificate obtainment failed for $hostname" }
            AcmeResult(success = false, error = e.message ?: "Unknown error")
        }
    }

    fun renewCertificate(hostname: String, targetDir: Path, acmeDir: Path): AcmeResult {
        log.info { "Renewing certificate for $hostname" }
        return obtainCertificate(hostname, null, targetDir, acmeDir)
    }

    fun validateWithStaging(hostname: String, email: String?, acmeDir: Path): AcmeResult {
        val stagingAcmeDir = acmeDir.resolve("staging")
        val stagingTargetDir = java.nio.file.Files.createTempDirectory("acme-staging")
        return try {
            log.info { "Running ACME staging validation for $hostname" }
            val result = doObtain(hostname, email, stagingTargetDir, stagingAcmeDir, LE_STAGING_DIRECTORY)
            log.info { "ACME staging validation succeeded for $hostname" }
            result
        } catch (e: Exception) {
            log.error(e) { "ACME staging validation failed for $hostname" }
            AcmeResult(success = false, error = e.message ?: "Unknown error")
        } finally {
            stagingTargetDir.toFile().deleteRecursively()
            stagingAcmeDir.toFile().deleteRecursively()
        }
    }

    private fun doObtain(
        hostname: String,
        email: String?,
        targetDir: Path,
        acmeDir: Path,
        directoryUrl: String
    ): AcmeResult {
        val client = HttpClient(ClientCIO) {
            install(HttpTimeout) {
                requestTimeoutMillis = 30_000
                connectTimeoutMillis = 10_000
            }
        }

        try {
            targetDir.toFile().mkdirs()
            acmeDir.toFile().mkdirs()

            // 1. Discover directory
            val directory = fetchDirectory(client, directoryUrl)
            log.info { "ACME directory fetched" }

            // 2. Load or create account key
            val accountKeyFile = acmeDir.resolve("account-key.json")
            val accountKey = loadOrCreateAccountKey(accountKeyFile)
            val thumbprint = jwkThumbprint(accountKey.public as java.security.interfaces.RSAPublicKey)

            // 3. Get initial nonce
            var nonce = fetchNonce(client, directory["newNonce"]!!)

            // 4. Register / find account
            val accountUrlFile = acmeDir.resolve("account-url.txt")
            val accountUrl: String
            if (accountUrlFile.exists()) {
                accountUrl = accountUrlFile.readText().trim()
                log.info { "Using existing ACME account: $accountUrl" }
            } else {
                val regPayload = buildMap<String, Any> {
                    put("termsOfServiceAgreed", true)
                    if (!email.isNullOrBlank()) {
                        put("contact", listOf("mailto:$email"))
                    }
                }
                val regResult = acmePost(
                    client,
                    directory["newAccount"]!!,
                    regPayload,
                    accountKey,
                    nonce,
                    null
                )
                nonce = regResult.nonce
                accountUrl = regResult.location
                    ?: error("ACME account registration did not return a Location header")
                accountUrlFile.writeText(accountUrl)
                log.info { "ACME account registered: $accountUrl" }
            }

            // 5. Create order
            val isIp = isIpAddress(hostname)
            val identifierType = if (isIp) "ip" else "dns"
            val orderPayload = buildMap<String, Any> {
                put("identifiers", listOf(mapOf("type" to identifierType, "value" to hostname)))
                if (isIp) {
                    put("profile", PROFILE_SHORT_LIVED)
                    log.info { "Using '$PROFILE_SHORT_LIVED' profile (required for IP address certificates)" }
                }
            }
            val orderResult = acmePost(
                client,
                directory["newOrder"]!!,
                orderPayload,
                accountKey,
                nonce,
                accountUrl
            )
            nonce = orderResult.nonce
            val order = orderResult.body
            val authorizations = (order["authorizations"] as? List<*>)
                ?: error("No authorizations in order response")
            val finalizeUrl = order["finalize"] as? String
                ?: error("No finalize URL in order response")

            // 6. Process authorizations (HTTP-01)
            for (authzUrl in authorizations) {
                val authzResult = acmePost(
                    client,
                    authzUrl as String,
                    null,
                    accountKey,
                    nonce,
                    accountUrl
                )
                nonce = authzResult.nonce
                val authz = authzResult.body

                val challenges = authz["challenges"] as? List<*>
                    ?: error("No challenges in authorization")

                val http01 = challenges.filterIsInstance<Map<String, Any?>>()
                    .find { it["type"] == "http-01" }
                    ?: error("No HTTP-01 challenge found for $hostname. Ensure port 80 is reachable.")

                val token = http01["token"] as String
                val keyAuth = "$token.$thumbprint"

                // Start challenge server on port 80
                val challengeServer = embeddedServer(ServerCIO, port = 80) {
                    routing {
                        get("/.well-known/acme-challenge/{token}") {
                            val reqToken = call.parameters["token"]
                            if (reqToken == token) {
                                call.respondText(keyAuth, ContentType.Text.Plain)
                            } else {
                                call.respond(HttpStatusCode.NotFound)
                            }
                        }
                    }
                }.start(wait = false)

                try {
                    // Tell ACME server we're ready
                    val challengeUrl = http01["url"] as String
                    val chalResult = acmePost(
                        client,
                        challengeUrl,
                        emptyMap<String, Any>(),
                        accountKey,
                        nonce,
                        accountUrl
                    )
                    nonce = chalResult.nonce

                    // Poll until valid
                    nonce = pollUntilValid(client, authzUrl, accountKey, nonce, accountUrl)
                } finally {
                    challengeServer.stop(gracePeriodMillis = 500, timeoutMillis = 2000)
                }
            }

            // 7. Generate key pair and CSR via keytool
            val tmpDir = java.nio.file.Files.createTempDirectory("acme-csr")
            val csrDer: ByteArray
            val serverKey: PrivateKey
            try {
                val p12 = tmpDir.resolve("temp.p12")
                val csrFile = tmpDir.resolve("temp.csr")
                val keytool = TlsCertGenerator.findKeytool()

                val sanExt = if (isIpAddress(hostname)) "ip:$hostname" else "dns:$hostname"

                // Generate keypair in temp keystore
                TlsCertGenerator.execKeytool(
                    keytool, "-genkeypair",
                    "-alias", "server",
                    "-keyalg", "RSA",
                    "-keysize", "2048",
                    "-validity", "1",
                    "-storetype", "PKCS12",
                    "-keystore", p12.toString(),
                    "-storepass", "changeit",
                    "-dname", "CN=$hostname"
                )

                // Generate CSR
                TlsCertGenerator.execKeytool(
                    keytool, "-certreq",
                    "-alias", "server",
                    "-keystore", p12.toString(),
                    "-storetype", "PKCS12",
                    "-storepass", "changeit",
                    "-file", csrFile.toString(),
                    "-ext", "SAN=$sanExt"
                )

                // Extract private key from keystore
                val ks = KeyStore.getInstance("PKCS12")
                p12.toFile().inputStream().use { ks.load(it, "changeit".toCharArray()) }
                serverKey = ks.getKey("server", "changeit".toCharArray()) as PrivateKey

                // Read CSR and convert PEM to DER
                val csrPem = csrFile.readText()
                val csrBase64 = csrPem
                    .replace("-----BEGIN NEW CERTIFICATE REQUEST-----", "")
                    .replace("-----END NEW CERTIFICATE REQUEST-----", "")
                    .replace("-----BEGIN CERTIFICATE REQUEST-----", "")
                    .replace("-----END CERTIFICATE REQUEST-----", "")
                    .replace("\\s".toRegex(), "")
                csrDer = Base64.getDecoder().decode(csrBase64)
            } finally {
                tmpDir.toFile().deleteRecursively()
            }

            val csrB64 = b64url.encodeToString(csrDer)

            // 8. Finalize order
            val finalizePayload = mapOf("csr" to csrB64)
            val finResult = acmePost(
                client,
                finalizeUrl,
                finalizePayload,
                accountKey,
                nonce,
                accountUrl
            )
            nonce = finResult.nonce

            // Poll order until valid
            val orderUrl = orderResult.location ?: error("No order URL from Location header")
            val (certUrl, freshNonce) = pollOrderUntilReady(client, orderUrl, accountKey, nonce, accountUrl)
            nonce = freshNonce

            // 9. Download certificate
            val certResult = acmePost(client, certUrl, null, accountKey, nonce, accountUrl)
            val certPem = certResult.rawBody

            // 10. Save files
            val certPath = targetDir.resolve("fullchain.pem")
            val keyPath = targetDir.resolve("privkey.pem")
            certPath.writeText(certPem)
            TlsCertGenerator.writePem(keyPath, "PRIVATE KEY", serverKey.encoded)

            log.info { "Certificate obtained and saved to $targetDir" }
            return AcmeResult(
                success = true,
                certPath = certPath.toAbsolutePath().toString(),
                keyPath = keyPath.toAbsolutePath().toString()
            )
        } finally {
            client.close()
        }
    }

    // --- ACME HTTP helpers ---

    private data class AcmeResponse(
        val body: Map<String, Any?>,
        val rawBody: String,
        val nonce: String,
        val location: String?
    )

    private fun fetchDirectory(client: HttpClient, directoryUrl: String): Map<String, String> {
        return runBlocking {
            val resp = client.get(directoryUrl)
            val body: Map<String, Any?> = mapper.readValue(resp.bodyAsText())
            body.filterValues { it is String }.mapValues { it.value as String }
        }
    }

    private fun fetchNonce(client: HttpClient, newNonceUrl: String): String {
        return runBlocking {
            val resp = client.head(newNonceUrl)
            resp.headers["Replay-Nonce"] ?: error("No Replay-Nonce header")
        }
    }

    private fun acmePost(
        client: HttpClient,
        url: String,
        payload: Any?,
        accountKey: KeyPair,
        nonce: String,
        kid: String?
    ): AcmeResponse {
        var currentNonce = nonce

        repeat(MAX_BAD_NONCE_RETRIES) { attempt ->
            val result = doAcmePost(client, url, payload, accountKey, currentNonce, kid)
            if (result.badNonce) {
                log.debug { "Got badNonce for $url (attempt ${attempt + 1}), retrying with fresh nonce" }
                currentNonce = result.response.nonce
                return@repeat
            }
            return result.response
        }

        // Final attempt — let errors propagate
        return doAcmePost(client, url, payload, accountKey, currentNonce, kid).response
    }

    private data class AcmePostResult(
        val response: AcmeResponse,
        val badNonce: Boolean
    )

    private fun doAcmePost(
        client: HttpClient,
        url: String,
        payload: Any?,
        accountKey: KeyPair,
        nonce: String,
        kid: String?
    ): AcmePostResult {
        val payloadJson = if (payload != null) mapper.writeValueAsString(payload) else ""
        val payloadB64 = if (payload != null) b64url.encodeToString(payloadJson.toByteArray()) else ""

        val protectedHeader = buildMap<String, Any> {
            put("alg", "RS256")
            put("nonce", nonce)
            put("url", url)
            if (kid != null) {
                put("kid", kid)
            } else {
                put("jwk", rsaJwk(accountKey.public as java.security.interfaces.RSAPublicKey))
            }
        }
        val protectedB64 = b64url.encodeToString(mapper.writeValueAsString(protectedHeader).toByteArray())

        val sigInput = "$protectedB64.$payloadB64"
        val signature = sign(sigInput.toByteArray(), accountKey.private)
        val sigB64 = b64url.encodeToString(signature)

        val jws = mapOf(
            "protected" to protectedB64,
            "payload" to payloadB64,
            "signature" to sigB64
        )

        return runBlocking {
            val resp = client.post(url) {
                contentType(ContentType("application", "jose+json"))
                setBody(mapper.writeValueAsString(jws))
            }
            val respNonce = resp.headers["Replay-Nonce"] ?: nonce
            val location = resp.headers["Location"]
            val rawBody = resp.bodyAsText()

            if (!resp.status.isSuccess()) {
                val errorBody: Map<String, Any?> = try {
                    mapper.readValue(rawBody)
                } catch (_: Exception) {
                    emptyMap()
                }

                // ACME spec: retry with fresh nonce on badNonce error
                val errorType = errorBody["type"]?.toString() ?: ""
                if (errorType.endsWith(":badNonce")) {
                    return@runBlocking AcmePostResult(
                        AcmeResponse(errorBody, rawBody, respNonce, location),
                        badNonce = true
                    )
                }

                val errorDetail = errorBody["detail"]?.toString() ?: rawBody
                error("ACME request to $url failed (${resp.status}): $errorDetail")
            }

            val body: Map<String, Any?> = try {
                mapper.readValue(rawBody)
            } catch (_: Exception) {
                emptyMap()
            }

            AcmePostResult(
                AcmeResponse(body, rawBody, respNonce, location),
                badNonce = false
            )
        }
    }

    private fun pollUntilValid(
        client: HttpClient,
        authzUrl: String,
        accountKey: KeyPair,
        startNonce: String,
        kid: String
    ): String {
        var nonce = startNonce
        repeat(30) {
            Thread.sleep(2000)
            val result = acmePost(client, authzUrl, null, accountKey, nonce, kid)
            nonce = result.nonce
            val status = result.body["status"] as? String
            when (status) {
                "valid" -> return nonce
                "invalid" -> error("Authorization failed: ${result.rawBody}")
            }
        }
        error("Authorization polling timed out")
    }

    private fun pollOrderUntilReady(
        client: HttpClient,
        orderUrl: String,
        accountKey: KeyPair,
        startNonce: String,
        kid: String
    ): Pair<String, String> {
        var nonce = startNonce
        repeat(30) {
            Thread.sleep(2000)
            val result = acmePost(client, orderUrl, null, accountKey, nonce, kid)
            nonce = result.nonce
            val status = result.body["status"] as? String
            when (status) {
                "valid" -> {
                    val certUrl = result.body["certificate"] as? String
                        ?: error("No certificate URL in valid order")
                    return Pair(certUrl, nonce)
                }
                "invalid" -> error("Order failed: ${result.rawBody}")
            }
        }
        error("Order polling timed out")
    }

    // --- Crypto helpers ---

    private fun loadOrCreateAccountKey(file: Path): KeyPair {
        if (file.exists()) {
            val data: Map<String, String> = mapper.readValue(file.readText())
            val kf = KeyFactory.getInstance("RSA")
            val privKey = kf.generatePrivate(PKCS8EncodedKeySpec(Base64.getDecoder().decode(data["private"])))
            val pubKey = kf.generatePublic(X509EncodedKeySpec(Base64.getDecoder().decode(data["public"])))
            return KeyPair(pubKey, privKey)
        }
        val gen = KeyPairGenerator.getInstance("RSA")
        gen.initialize(2048)
        val keyPair = gen.generateKeyPair()
        val data = mapOf(
            "private" to Base64.getEncoder().encodeToString(keyPair.private.encoded),
            "public" to Base64.getEncoder().encodeToString(keyPair.public.encoded)
        )
        file.writeText(mapper.writeValueAsString(data))
        return keyPair
    }

    private fun sign(data: ByteArray, privateKey: PrivateKey): ByteArray {
        val sig = Signature.getInstance("SHA256withRSA")
        sig.initSign(privateKey)
        sig.update(data)
        return sig.sign()
    }

    private fun rsaJwk(pub: java.security.interfaces.RSAPublicKey): Map<String, String> {
        return mapOf(
            "e" to b64url.encodeToString(toUnsignedBytes(pub.publicExponent)),
            "kty" to "RSA",
            "n" to b64url.encodeToString(toUnsignedBytes(pub.modulus))
        )
    }

    private fun jwkThumbprint(pub: java.security.interfaces.RSAPublicKey): String {
        val jwk = rsaJwk(pub)
        // Canonical JSON: keys in lexicographic order
        val canonical = """{"e":"${jwk["e"]}","kty":"${jwk["kty"]}","n":"${jwk["n"]}"}"""
        val digest = MessageDigest.getInstance("SHA-256").digest(canonical.toByteArray())
        return b64url.encodeToString(digest)
    }

    private fun toUnsignedBytes(bigInt: java.math.BigInteger): ByteArray {
        val bytes = bigInt.toByteArray()
        return if (bytes.isNotEmpty() && bytes[0] == 0.toByte()) {
            bytes.copyOfRange(1, bytes.size)
        } else {
            bytes
        }
    }

    fun isIpAddress(hostname: String): Boolean {
        return hostname.matches(Regex("^\\d{1,3}(\\.\\d{1,3}){3}$")) ||
            hostname.contains(":")
    }
}
