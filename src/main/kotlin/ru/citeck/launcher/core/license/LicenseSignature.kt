package ru.citeck.launcher.core.license

class LicenseSignature(
    val time: String,
    val issuer: String,
    val signature: ByteArray,
    val certificates: List<ByteArray>
) {
    override fun equals(other: Any?): Boolean {
        if (this === other) {
            return true
        }
        if (javaClass != other?.javaClass) {
            return false
        }
        other as LicenseSignature
        if (time != other.time) {
            return false
        }
        if (issuer != other.issuer) {
            return false
        }
        if (!signature.contentEquals(other.signature)) {
            return false
        }
        if (certificates.size != other.certificates.size) {
            return false
        }
        for (i in certificates.indices) {
            if (!certificates[i].contentEquals(other.certificates[i])) {
                return false
            }
        }
        return true
    }

    override fun hashCode(): Int {
        var result = time.hashCode()
        result = 31 * result + issuer.hashCode()
        result = 31 * result + signature.contentHashCode()
        result = 31 * result + certificates.hashCode()
        return result
    }
}
