package ru.citeck.launcher.core.config.bundle

import org.assertj.core.api.Assertions.*
import kotlin.test.Test

class BundleKeyTest {

    @Test
    fun testCompareTo() {

        val versions = listOf(
            "1",
            "2.2.2.2.2.2",
            "3.2.2.2.2.2",
            "333.2.2.2.2.2",
            "555",
            "2025.5-RC1",
            "2025.5-RC2",
            "2025.5-RC12"
        )
        for (idx in 0 until (versions.size - 1)) {
            val prev = BundleKey(versions[idx])
            val next = BundleKey(versions[idx + 1])
            assertThat(prev).isLessThan(next)
        }
    }
}
