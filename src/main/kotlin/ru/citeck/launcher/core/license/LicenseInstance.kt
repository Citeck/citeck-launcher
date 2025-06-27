package ru.citeck.launcher.core.license

import com.fasterxml.jackson.annotation.JsonIgnore
import com.fasterxml.jackson.core.JsonGenerator
import com.fasterxml.jackson.databind.SerializerProvider
import com.fasterxml.jackson.databind.annotation.JsonDeserialize
import com.fasterxml.jackson.databind.annotation.JsonSerialize
import com.fasterxml.jackson.databind.ser.std.StdSerializer
import ru.citeck.launcher.core.utils.data.DataValue
import ru.citeck.launcher.core.utils.json.Json
import java.io.ByteArrayInputStream
import java.security.Signature
import java.security.cert.CertificateFactory
import java.security.cert.X509Certificate
import java.time.Instant
import java.util.*

@JsonDeserialize(builder = LicenseInstance.Builder::class)
data class LicenseInstance(
    val id: String,
    val tenant: String,
    val priority: Long,
    val issuedTo: String,
    @JsonSerialize(using = LicenseDateSerializer::class)
    val issuedAt: Instant,
    @JsonSerialize(using = LicenseDateSerializer::class)
    val validFrom: Instant,
    @JsonSerialize(using = LicenseDateSerializer::class)
    val validUntil: Instant,
    val content: DataValue,
    val signatures: List<LicenseSignature>
) {

    companion object {

        private const val EMPTY_TIME_POSTFIX = "T00:00:00Z"

        @JvmStatic
        fun create(): Builder {
            return Builder()
        }

        @JvmStatic
        fun create(builder: Builder.() -> Unit): LicenseInstance {
            val builderObj = Builder()
            builder.invoke(builderObj)
            return builderObj.build()
        }
    }

    fun addSignature(signature: LicenseSignature): LicenseInstance {
        return copy().withSignatures(listOf(signature, *this.signatures.toTypedArray())).build()
    }

    @JsonIgnore
    fun getContentForSign(): ByteArray {

        val json = DataValue.create(Json.toNonDefaultJsonObj(this))
        json.remove("signatures")

        fun normalizeKeys(value: DataValue): DataValue {
            return if (value.isObject()) {
                val newObj = DataValue.createObj()
                val orderedKeys = TreeSet(value.fieldNamesList())
                for (key in orderedKeys) {
                    newObj[key] = normalizeKeys(value[key])
                }
                newObj
            } else if (value.isArray()) {
                val newArr = DataValue.createArr()
                for (item in value) {
                    newArr.add(normalizeKeys(item))
                }
                newArr
            } else {
                value
            }
        }
        return Json.toBytes(normalizeKeys(json))
    }

    @JsonIgnore
    fun isValid(): Boolean {
        val now = Instant.now()
        if (signatures.isEmpty() || validFrom.isAfter(now) || validUntil.isBefore(now)) {
            return false
        }
        val contentForSign = getContentForSign()
        return signatures.any { checkSign(contentForSign, it) }
    }

    private fun checkSign(data: ByteArray, citeckSignature: LicenseSignature): Boolean {

        val signCert = citeckSignature.certificates.firstOrNull()?.let {
            val certFactory = CertificateFactory.getInstance("X.509")
            certFactory.generateCertificate(ByteArrayInputStream(it)) as X509Certificate
        } ?: return false

        if (signCert.getIssuerX500Principal().toString() != citeckSignature.issuer) {
            return false
        }

        val signature: Signature = Signature.getInstance(signCert.sigAlgName)
        signature.initVerify(signCert.publicKey)
        signature.update(data)
        signature.update(citeckSignature.time.toByteArray(Charsets.UTF_8))

        return signature.verify(citeckSignature.signature)
    }

    fun copy(): Builder {
        return Builder(this)
    }

    class Builder() {

        var id: String = ""
        var tenant: String = ""
        var priority: Long = 0
        var issuedTo: String = ""
        var issuedAt: Instant = Instant.EPOCH
        var validFrom: Instant = Instant.EPOCH
        var validUntil: Instant = Instant.EPOCH
        var content: DataValue = DataValue.createObj()
        var signatures: List<LicenseSignature> = emptyList()

        constructor(base: LicenseInstance) : this() {
            id = base.id
            tenant = base.tenant
            priority = base.priority
            issuedTo = base.issuedTo
            validUntil = base.validUntil
            validFrom = base.validFrom
            issuedAt = base.issuedAt
            content = base.content.copy()
            signatures = base.signatures
        }

        fun withId(id: String?): Builder {
            this.id = id ?: ""
            return this
        }

        fun withTenant(tenant: String?): Builder {
            this.tenant = tenant ?: ""
            return this
        }

        fun withPriority(priority: Long?): Builder {
            this.priority = priority ?: 0
            return this
        }

        fun withIssuedTo(issuedTo: String?): Builder {
            this.issuedTo = issuedTo ?: ""
            return this
        }

        fun withIssuedAt(issuedAt: Instant?): Builder {
            this.issuedAt = issuedAt ?: Instant.EPOCH
            return this
        }

        fun withValidFrom(validFrom: Instant?): Builder {
            this.validFrom = validFrom ?: Instant.EPOCH
            return this
        }

        fun withValidUntil(validUntil: Instant?): Builder {
            this.validUntil = validUntil ?: Instant.EPOCH
            return this
        }

        fun withContent(content: DataValue?): Builder {
            this.content = content ?: DataValue.createObj()
            return this
        }

        fun withSignatures(signatures: List<LicenseSignature>?): Builder {
            this.signatures = signatures ?: emptyList()
            return this
        }

        fun build(): LicenseInstance {
            return LicenseInstance(
                id = id,
                tenant = tenant,
                priority = priority,
                issuedTo = issuedTo,
                issuedAt = issuedAt,
                validFrom = validFrom,
                validUntil = validUntil,
                content = DataValue.create(content.asUnmodifiable()),
                signatures = signatures
            )
        }
    }

    private class LicenseDateSerializer : StdSerializer<Instant>(Instant::class.java) {
        override fun serialize(value: Instant, gen: JsonGenerator, provider: SerializerProvider) {
            var result = value.toString()
            if (result.endsWith(EMPTY_TIME_POSTFIX)) {
                result = result.substring(0, result.length - EMPTY_TIME_POSTFIX.length)
            }
            gen.writeString(result)
        }
    }
}
