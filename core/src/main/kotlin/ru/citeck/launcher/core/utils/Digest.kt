package ru.citeck.launcher.core.utils

import java.io.InputStream
import java.security.MessageDigest

class Digest private constructor(private val digest: MessageDigest) {

    companion object {
        fun sha256(): Digest = Digest(MessageDigest.getInstance("SHA-256"))
    }

    fun update(value: ByteArray, offset: Int, len: Int): Digest {
        digest.update(value, offset, len)
        return this
    }

    fun update(value: Any?): Digest {
        if (value == null) {
            digest.update(ElementType.NULL.bytes)
            return this
        }
        when (value) {
            is ElementType -> digest.update(value.bytes)
            is Map<*, *> -> {
                digest.update(ElementType.MAP.bytes)
                for ((mapKey, mapValue) in value) {
                    update(mapKey)
                    update(mapValue)
                }
            }
            is Collection<*> -> {
                digest.update(ElementType.LIST.bytes)
                for (element in value) {
                    update(element)
                }
            }
            is Int -> {
                digest.update(ElementType.INT.bytes)
                digest.update((value shr 0).toByte())
                digest.update((value shr 8).toByte())
                digest.update((value shr 16).toByte())
                digest.update((value shr 24).toByte())
            }
            is String -> {
                digest.update(ElementType.STRING.bytes)
                digest.update(value.toByteArray(Charsets.UTF_8))
            }
            is InputStream -> {
                val buffer = ByteArray(2048)
                var bytesCount = value.read(buffer)
                while (bytesCount > 0) {
                    digest.update(buffer, 0, bytesCount)
                    bytesCount = value.read(buffer)
                }
            }
            is ByteArray -> {
                digest.update(value)
            }
            else -> error("Unknown type: " + value.javaClass)
        }
        return this
    }

    fun toHex(): String {
        val hexString = StringBuilder()
        for (b in digest.digest()) {
            val hex = Integer.toHexString(0xFF and b.toInt())
            if (hex.length == 1) {
                hexString.append('0')
            }
            hexString.append(hex)
        }
        return hexString.toString()
    }

    private enum class ElementType {
        INT,
        MAP,
        LIST,
        STRING,
        NULL;

        val bytes = name.toByteArray(Charsets.UTF_8)
    }
}
