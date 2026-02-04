package ru.citeck.launcher.view.logs

import org.assertj.core.api.Assertions.assertThat
import kotlin.test.Test

class LogLevelDetectorTest {

    @Test
    fun `detect bracketed format`() {
        assertThat(LogLevelDetector.detect("[ERROR] Something went wrong"))
            .isEqualTo(LogLevel.ERROR)
        assertThat(LogLevelDetector.detect("[WARN] Warning message"))
            .isEqualTo(LogLevel.WARN)
        assertThat(LogLevelDetector.detect("[WARNING] Warning message"))
            .isEqualTo(LogLevel.WARN)
        assertThat(LogLevelDetector.detect("[INFO] Info message"))
            .isEqualTo(LogLevel.INFO)
        assertThat(LogLevelDetector.detect("[DEBUG] Debug message"))
            .isEqualTo(LogLevel.DEBUG)
        assertThat(LogLevelDetector.detect("[TRACE] Trace message"))
            .isEqualTo(LogLevel.TRACE)
    }

    @Test
    fun `detect case insensitive`() {
        assertThat(LogLevelDetector.detect("[error] lowercase"))
            .isEqualTo(LogLevel.ERROR)
        assertThat(LogLevelDetector.detect("[Error] mixed case"))
            .isEqualTo(LogLevel.ERROR)
        assertThat(LogLevelDetector.detect("[info] lowercase info"))
            .isEqualTo(LogLevel.INFO)
    }

    @Test
    fun `detect logback internal format`() {
        assertThat(LogLevelDetector.detect("|-ERROR in ch.qos.logback.classic"))
            .isEqualTo(LogLevel.ERROR)
        assertThat(LogLevelDetector.detect("|-WARN in ch.qos.logback.core"))
            .isEqualTo(LogLevel.WARN)
        assertThat(LogLevelDetector.detect("|-INFO in some.package"))
            .isEqualTo(LogLevel.INFO)
    }

    @Test
    fun `detect after timestamp`() {
        assertThat(LogLevelDetector.detect("10:30:45.123 ERROR Something failed"))
            .isEqualTo(LogLevel.ERROR)
        assertThat(LogLevelDetector.detect("10:30:45 WARN Warning here"))
            .isEqualTo(LogLevel.WARN)
        assertThat(LogLevelDetector.detect("23:59:59,999 INFO Message"))
            .isEqualTo(LogLevel.INFO)
    }

    @Test
    fun `detect Spring Boot format`() {
        assertThat(LogLevelDetector.detect("2024-01-15T10:30:45.123+00:00  INFO 12345 --- [main] c.e.App : Started"))
            .isEqualTo(LogLevel.INFO)
        assertThat(LogLevelDetector.detect("2024-01-15T10:30:45.123Z ERROR 12345 --- [main] c.e.App : Failed"))
            .isEqualTo(LogLevel.ERROR)
    }

    @Test
    fun `detect Python logging format`() {
        assertThat(LogLevelDetector.detect("ERROR: Connection refused"))
            .isEqualTo(LogLevel.ERROR)
        assertThat(LogLevelDetector.detect("WARNING: Deprecated function"))
            .isEqualTo(LogLevel.WARN)
        assertThat(LogLevelDetector.detect("INFO: Starting server"))
            .isEqualTo(LogLevel.INFO)
    }

    @Test
    fun `detect level surrounded by whitespace`() {
        assertThat(LogLevelDetector.detect("2024-01-15 ERROR message"))
            .isEqualTo(LogLevel.ERROR)
        assertThat(LogLevelDetector.detect("prefix WARN suffix"))
            .isEqualTo(LogLevel.WARN)
    }

    @Test
    fun `detect level at line start`() {
        assertThat(LogLevelDetector.detect("ERROR: message"))
            .isEqualTo(LogLevel.ERROR)
        assertThat(LogLevelDetector.detect("WARN message"))
            .isEqualTo(LogLevel.WARN)
        assertThat(LogLevelDetector.detect("INFO-message"))
            .isEqualTo(LogLevel.INFO)
        assertThat(LogLevelDetector.detect("DEBUG[thread]"))
            .isEqualTo(LogLevel.DEBUG)
    }

    @Test
    fun `return UNKNOWN for no match`() {
        assertThat(LogLevelDetector.detect("Just a regular message"))
            .isEqualTo(LogLevel.UNKNOWN)
        assertThat(LogLevelDetector.detect("    at java.lang.Thread.run(Thread.java:829)"))
            .isEqualTo(LogLevel.UNKNOWN)
        assertThat(LogLevelDetector.detect(""))
            .isEqualTo(LogLevel.UNKNOWN)
    }

    @Test
    fun `do not match partial words`() {
        assertThat(LogLevelDetector.detect("INFORMATION about something"))
            .isEqualTo(LogLevel.UNKNOWN)
        assertThat(LogLevelDetector.detect("Multiple ERRORS occurred"))
            .isEqualTo(LogLevel.UNKNOWN)
    }

    @Test
    fun `detect real-world log examples`() {
        assertThat(LogLevelDetector.detect("2024-01-15 10:30:45.123  INFO 1 --- [main] o.s.b.w.e.t.TomcatWebServer : Tomcat started"))
            .isEqualTo(LogLevel.INFO)
        assertThat(LogLevelDetector.detect("2024-01-15 10:30:45.123 ERROR 1 --- [http-nio-8080-exec-1] o.a.c.c.C.[.[.[/] : Servlet.service() threw exception"))
            .isEqualTo(LogLevel.ERROR)
        assertThat(LogLevelDetector.detect("10:30:45,123 |-INFO in ch.qos.logback.classic.LoggerContext[default] - Could NOT find resource"))
            .isEqualTo(LogLevel.INFO)
    }
}
