package ru.citeck.launcher.core.utils.promise

/**
 * In asynchronous interactions, the original stack trace leading up to the get method call can be
 * lost when an exception is thrown from a separate thread. The PromiseException class serves as a wrapper
 * to carry the original exception while preserving its stack trace for improved debugging and error tracing.
 * It ensures that the stack trace from the async context is retained and available upon rethrowing.
 */
class PromiseException(cause: Throwable) : RuntimeException(cause.message ?: "", cause)
