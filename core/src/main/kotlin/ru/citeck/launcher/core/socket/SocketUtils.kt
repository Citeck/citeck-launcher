package ru.citeck.launcher.core.socket

import java.io.InputStream
import java.io.OutputStream

object SocketUtils {

    private const val MINUS_SIGN_BIT_IDX = 6
    private const val MINUS_SIGN_MASK = 1 shl MINUS_SIGN_BIT_IDX
    private const val LONG_NUM_MARKER_MASK = 128
    private const val LONG_NUM_LENGTH_MASK = 7

    fun writeInt(output: OutputStream, value: Int) {
        if (value in 0..127) {
            output.write(value)
        } else {
            var signMask = 0
            val valueToWrite = if (value < 0 && value > Int.MIN_VALUE) {
                signMask = MINUS_SIGN_MASK
                -value
            } else {
                value
            }
            var length = 1
            while (length < 4 && valueToWrite shr (length * 8) != 0) {
                length++
            }
            output.write(LONG_NUM_MARKER_MASK or (length - 1) or signMask)
            for (idx in (length - 1) downTo 0) {
                output.write((valueToWrite shr (8 * idx)) and 0xFF)
            }
        }
    }

    fun readInt(input: InputStream): Int {
        val value = input.read()
        if (value < 128) {
            return value
        }
        val sign = if (value and MINUS_SIGN_MASK != 0) {
            -1
        } else {
            1
        }
        val length = (value and LONG_NUM_LENGTH_MASK) + 1
        if (length > 4) {
            error("Length of int is too long: $length")
        }
        var result = 0
        for (idx in 0 until length) {
            result = (result shl 8) or input.read()
        }
        return sign * result
    }
}
