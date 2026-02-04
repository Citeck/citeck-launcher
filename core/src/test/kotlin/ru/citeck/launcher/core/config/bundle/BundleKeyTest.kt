package ru.citeck.launcher.core.config.bundle

import org.assertj.core.api.Assertions.*
import ru.citeck.launcher.core.bundle.BundleKey
import ru.citeck.launcher.core.utils.json.Json
import java.io.ByteArrayInputStream
import kotlin.test.Test

class BundleKeyTest {

    @Test
    fun simpleCompareToTest() {
        val key0 = BundleKey("archive/archive/1")
        val key1 = BundleKey("archive/1")
        assertThat(key0).isLessThan(key1)
    }

    @Test
    fun parseTest() {
        val key0 = BundleKey("archive/")
        assertThat(key0.scope).containsExactly("archive")
        assertThat(key0.versionParts).isEmpty()
        assertThat(key0.suffixParts).isEmpty()
        val key1 = BundleKey("archive/arch2")
        assertThat(key1.scope).containsExactly("archive")
        assertThat(key1.versionParts).isEmpty()
        @Suppress("UNCHECKED_CAST")
        assertThat(key1.suffixParts).containsExactlyElementsOf(listOf("arch", BundleKey("2")) as List<Comparable<Any>>)
    }

    @Test
    fun testCompareTo() {

        val versions = mutableListOf(
            "1",
            "2.2.2.2.2.2-",
            "3.2.2.2.2.2@",
            "333.2.2.2.2.2",
            "555",
            "2025.5-RC1",
            "2025.5-RC1.1",
            "2025.5-RC2",
            "2025.5-RC2.1",
            "2025.5-RC2.1.1000",
            "2025.5-RC12"
        )
        val versionsCopy = versions.toList()
        versions.addAll(0, versionsCopy.map { "barchive/$it" })
        versions.addAll(0, versionsCopy.map { "archive/$it" })
        versions.addAll(0, versionsCopy.map { "archive/archive/$it" })
        versions.addAll(0, versionsCopy.map { "1archive/archive/$it" })

        for (idx in 0 until (versions.size - 1)) {
            val prev = BundleKey(versions[idx])
            for (nextIdx in (idx + 1) until versions.size) {
                val next = BundleKey(versions[nextIdx])
                assertThat(prev).isLessThan(next)
                assertThat(next).isGreaterThan(prev)
            }
        }
    }

    @Test
    fun serializationTest() {

        val raw = "2025.5-RC2.1.1000"
        val key = BundleKey(raw)
        assertThat(key.toString()).isEqualTo(raw)
        val jsonValue = Json.toString(key)
        assertThat(jsonValue).isEqualTo('"' + raw + '"')

        fun readBundleKey(json: String) = Json.readNotNull(
            ByteArrayInputStream(json.toByteArray()),
            BundleKey::class
        )
        val keyFromJson = readBundleKey(jsonValue)
        assertThat(keyFromJson).isEqualTo(key)
        assertThat(keyFromJson.toString()).isEqualTo(raw)

        val keyFromObj = readBundleKey("{\"rawKey\":\"2025.8-RC3\"}")
        assertThat(keyFromObj.toString()).isEqualTo("2025.8-RC3")
    }
}
