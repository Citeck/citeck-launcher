package ru.citeck.launcher.utils

import org.assertj.core.api.Assertions.assertThat
import ru.citeck.launcher.core.utils.StringUtils
import kotlin.test.Test

class StringUtilsTest {

    @Test
    fun splitByGroupsTest() {
        val tests = listOf(
            "" to emptyList(),
            "a" to listOf("a"),
            "a12" to listOf("a", "12"),
            "a12b" to listOf("a", "12", "b"),
            "112b" to listOf("112", "b"),
            "qwe" to listOf("qwe"),
            "1q2w3r" to listOf("1", "q", "2", "w", "3", "r"),
        )
        for (test in tests) {
            assertThat(StringUtils.splitByGroups(test.first) { if (it.isDigit()) 1 else 0 })
                .describedAs(test.toString())
                .isEqualTo(test.second)
        }
    }
}
