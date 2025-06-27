package ru.citeck.launcher.view.utils

object NumUtils {

    fun toByteArray(value: Long): ByteArray {
        var remVal = value
        val result = ByteArray(8)

        for (i in 7 downTo 0) {
            result[i] = ((remVal and 255L).toInt()).toByte()
            remVal = remVal shr 8
        }
        return result
    }

    fun toByteArray(value: Int): ByteArray {
        return byteArrayOf(
            (value shr 24).toByte(),
            (value shr 16).toByte(),
            (value shr 8).toByte(),
            value.toByte()
        )
    }
}
