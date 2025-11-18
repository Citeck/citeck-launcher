package ru.citeck.launcher.core.utils

import org.apache.commons.codec.binary.Base32
import ru.citeck.launcher.view.utils.NumUtils
import kotlin.random.Random

object IdUtils {

    private val generated = LinkedHashSet<String>()
    private val base32 = Base32.builder().get()

    fun createStrId(long: Long): String {
        return base32.encodeToString(NumUtils.toByteArray(long)).lowercase().substringBefore("=")
    }

    fun createStrId(long: Boolean = false): String {
        var result: String
        try {
            do {
                val barr = if (long) {
                    NumUtils.toByteArray(Random.nextLong())
                } else {
                    NumUtils.toByteArray(Random.nextInt())
                }
                result = base32.encodeToString(barr).lowercase().substringBefore("=")
            } while (!generated.add(result))
        } finally {
            if (generated.size > 1000) {
                generated.removeFirst()
            }
        }
        return result
    }
}
