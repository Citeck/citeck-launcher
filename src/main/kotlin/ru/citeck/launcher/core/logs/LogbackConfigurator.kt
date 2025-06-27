package ru.citeck.launcher.core.logs

import ch.qos.logback.classic.Logger
import ch.qos.logback.classic.LoggerContext
import ch.qos.logback.classic.encoder.PatternLayoutEncoder
import ch.qos.logback.classic.spi.Configurator
import ch.qos.logback.classic.spi.Configurator.ExecutionStatus
import ch.qos.logback.classic.spi.ConfiguratorRank
import ch.qos.logback.classic.spi.ILoggingEvent
import ch.qos.logback.classic.util.LevelUtil
import ch.qos.logback.core.Appender
import ch.qos.logback.core.ConsoleAppender
import ch.qos.logback.core.encoder.Encoder
import ch.qos.logback.core.rolling.RollingFileAppender
import ch.qos.logback.core.rolling.TimeBasedRollingPolicy
import ch.qos.logback.core.spi.ContextAwareBase
import ch.qos.logback.core.util.FileSize
import ch.qos.logback.core.util.OptionHelper
import java.nio.charset.Charset

@ConfiguratorRank(ConfiguratorRank.CUSTOM_TOP_PRIORITY)
class LogbackConfigurator : ContextAwareBase(), Configurator {

    override fun configure(context: LoggerContext): ExecutionStatus {

        val rootLogger: Logger = setupLogger("ROOT", "INFO", null)
        rootLogger.addAppender(createConsoleAppender())
        rootLogger.addAppender(createFileAppender())

        return ExecutionStatus.DO_NOT_INVOKE_NEXT_IF_ANY
    }

    private fun setupLogger(loggerName: String, levelString: String?, additivity: Boolean?): Logger {

        val  loggerContext = context as LoggerContext
        val logger = loggerContext.getLogger(loggerName)

        if (!OptionHelper.isNullOrEmptyOrAllSpaces(levelString)) {
            val level = LevelUtil.levelStringToLevel(levelString)
            logger.level = level
        }
        if (additivity != null) {
            logger.isAdditive = additivity;
        }
        return logger
    }

    private fun createFileAppender(): Appender<ILoggingEvent> {

        val mainLogFilePath = AppLogUtils.getAppLogFilePath().toAbsolutePath()

        val logFileAppender = RollingFileAppender<ILoggingEvent>()
        logFileAppender.context = context
        logFileAppender.name = "logfile"
        logFileAppender.encoder = createEncoder(logFileAppender)
        logFileAppender.isAppend = true
        logFileAppender.file = mainLogFilePath.toString()

        val rollingLogFilePath = mainLogFilePath.parent.resolve("logfile-%d{yyyy-MM-dd}.log.zip")

        val logFilePolicy = TimeBasedRollingPolicy<ILoggingEvent>()
        logFilePolicy.context = context
        logFilePolicy.setParent(logFileAppender)
        logFilePolicy.fileNamePattern = rollingLogFilePath.toString()
        logFilePolicy.maxHistory = 5
        logFilePolicy.setTotalSizeCap(FileSize.valueOf("50 mb"))
        logFilePolicy.start()

        logFileAppender.rollingPolicy = logFilePolicy
        logFileAppender.start()

        return logFileAppender
    }

    private fun createEncoder(appender: Appender<ILoggingEvent>): Encoder<ILoggingEvent> {
        val patternLayoutEncoder = PatternLayoutEncoder()
        patternLayoutEncoder.context = context
        patternLayoutEncoder.pattern = "%d{yyyy-MM-dd'T'HH:mm:ss.SSS,GMT+0} [%thread] %-5level %logger{36} - %msg%n"
        patternLayoutEncoder.charset = Charset.forName("UTF-8")
        patternLayoutEncoder.setParent(appender)
        patternLayoutEncoder.start()
        return patternLayoutEncoder
    }

    private fun createConsoleAppender(): Appender<ILoggingEvent> {

        val appender = ConsoleAppender<ILoggingEvent>()
        appender.context = context
        appender.name = "CONSOLE"
        appender.encoder = createEncoder(appender)

        appender.start()
        return appender
    }
}
