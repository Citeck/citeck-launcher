package ru.citeck.launcher.core.actions

import io.github.oshai.kotlinlogging.KotlinLogging
import io.ktor.util.*
import org.apache.commons.lang3.exception.ExceptionUtils
import ru.citeck.launcher.core.utils.Disposable
import ru.citeck.launcher.core.utils.IdUtils
import ru.citeck.launcher.core.utils.ReflectUtils
import ru.citeck.launcher.core.utils.promise.Promise
import ru.citeck.launcher.core.utils.promise.Promises
import java.time.Duration
import java.util.*
import java.util.concurrent.*
import java.util.concurrent.atomic.AtomicBoolean
import kotlin.concurrent.thread
import kotlin.reflect.KClass

class ActionsService : Disposable {

    companion object {
        private val log = KotlinLogging.logger {}

        private val STILL_ACTIVE_ACTION_WARN_THRESHOLD_MS = Duration.ofMinutes(5).toMillis()
    }

    private val executor = Executors.newFixedThreadPool(20)
    private val scheduler = Executors.newScheduledThreadPool(1)
    private val actionExecutors = ConcurrentHashMap<KClass<ActionParams<Any>>, ExecutorInfo>()

    private val actionsInfo = Collections.newSetFromMap<ActionInfo>(ConcurrentHashMap())

    @Volatile
    private var watcherThread: Thread? = null
    private val watcherEnabled = AtomicBoolean()

    fun <R> execute(params: ActionParams<R>): Promise<R> {

        @Suppress("UNCHECKED_CAST")
        params as ActionParams<Any>
        @Suppress("UNCHECKED_CAST")
        val executorInfo = actionExecutors[params::class as KClass<ActionParams<Any>>]
            ?: error("Executor doesn't found for params ${params::class}")

        if (watcherEnabled.compareAndSet(false, true)) {
            watcherThread = thread(name = "actions-watcher") {
                while (watcherEnabled.get()) {
                    runWatcherLogic()
                    Thread.sleep(5000)
                }
            }
        }

        @Suppress("UNCHECKED_CAST")
        val actionContext = ActionContext(params as ActionParams<Any>)
        val actionInfo = ActionInfo(executorInfo, actionContext)
        if (actionsInfo.size > 1000) {
            error("Too much active actions: ${actionsInfo.size}. Looks like memory leak")
        }
        actionsInfo.add(actionInfo)
        log.info { "Schedule action: $actionInfo" }

        executeActionImpl(executorInfo, actionInfo)

        @Suppress("UNCHECKED_CAST")
        return Promises.create(actionInfo.future) as Promise<R>
    }

    private fun executeActionImpl(executorInfo: ExecutorInfo, actionInfo: ActionInfo) {

        val future = actionInfo.future
        future.executeFuture = executor.submit {
            val executionStartedAt = System.currentTimeMillis()
            actionInfo.executionStartedAt = executionStartedAt
            val context = actionInfo.actionContext
            var actionCompleted = false
            try {
                log.info { "Execute action: $actionInfo" }
                future.complete(executorInfo.executor.execute(context))
                log.info { "Action completed successfully: $actionInfo. ${actionInfo.getTimeInfo()}" }
                actionCompleted = true
            } catch (e: Throwable) {
                var exception = e
                if (exception is ExecutionException) {
                    exception = exception.cause ?: exception
                }
                context.lastError = exception
                context.retryIdx++
                val retryDelay = try {
                    executorInfo.executor.getRetryAfterErrorDelay(context, future)
                } catch (e: Throwable) {
                    log.error(e) { "Exception during getRetryAfterErrorDelay" }
                    -1
                }
                if (future.isDone) {
                    actionCompleted = true
                } else {
                    val rootCause = ExceptionUtils.getRootCause(exception) ?: exception
                    var logMsg = "Action completed exceptionally: $actionInfo. " +
                        "Error: ${e::class.simpleName}(${e.message}). "
                    if (rootCause !== exception) {
                        logMsg += "Root cause: ${rootCause::class.simpleName}(${rootCause.message}). "
                    }
                    logMsg += actionInfo.getTimeInfo()

                    if (retryDelay >= 0) {
                        logMsg += ". The action will be retried in ${retryDelay}ms (retryIdx ${context.retryIdx})."
                        if (log.isDebugEnabled()) {
                            log.debug(exception) { logMsg }
                        } else {
                            log.warn { logMsg }
                        }
                        future.scheduleFuture = scheduler.schedule({
                            executeActionImpl(executorInfo, actionInfo)
                            future.scheduleFuture = null
                        }, retryDelay, TimeUnit.MILLISECONDS)
                    } else {
                        logMsg += ". Action won't be retried."
                        if (!future.isDone) {
                            future.completeExceptionally(exception)
                        }
                        log.error(exception) {
                            "Action completed exceptionally: $actionInfo. " + actionInfo.getTimeInfo()
                        }
                        actionCompleted = true
                    }
                }
            } finally {
                actionInfo.executionStartedAt = -1L
                future.executeFuture = null
            }
            if (actionCompleted) {
                actionsInfo.remove(actionInfo)
            }
        }
    }

    private fun runWatcherLogic() {
        val stalledActions = ArrayList<ActionInfo>()
        for (info in actionsInfo) {
            val elapsedTimeSinceCreation = System.currentTimeMillis() - info.createdAt
            if (elapsedTimeSinceCreation < 120_000 || System.currentTimeMillis() < info.nextStillRunningReportTime) {
                continue
            }
            if (info.future.executeFuture.isFutureDone() && info.future.scheduleFuture.isFutureDone()) {
                log.error { "Found stalled action without active futures: $info. " + info.getTimeInfo() }
                stalledActions.add(info)
            } else {
                val message = "Action is still active: $info. " + info.getTimeInfo()
                if (elapsedTimeSinceCreation > STILL_ACTIVE_ACTION_WARN_THRESHOLD_MS) {
                    log.warn { message }
                } else {
                    log.info { message }
                }
                info.nextStillRunningReportTime = System.currentTimeMillis() + 60_000
            }
        }
        @Suppress("ConvertArgumentToSet")
        actionsInfo.removeAll(stalledActions)
    }

    private fun Future<*>?.isFutureDone(): Boolean = this == null || this.isDone

    fun register(actionExecutor: ActionExecutor<*, *>) {
        val args = ReflectUtils.getGenericArgs(actionExecutor::class, ActionExecutor::class)

        @Suppress("UNCHECKED_CAST")
        val executorInfo = ExecutorInfo(
            actionExecutor as ActionExecutor<ActionParams<Any>, Any>,
            args[0] as KClass<ActionParams<Any>>,
            args[1] as KClass<Any>
        )
        actionExecutors[executorInfo.paramsType] = executorInfo
    }

    private class ExecutorInfo(
        val executor: ActionExecutor<ActionParams<Any>, Any>,
        val paramsType: KClass<ActionParams<Any>>,
        val resultType: KClass<Any>
    )

    private class ActionInfo(
        val executorInfo: ExecutorInfo,
        val actionContext: ActionContext<ActionParams<Any>>,
        val createdAt: Long = System.currentTimeMillis(),
        @Volatile
        var executionStartedAt: Long = -1L,
        var nextStillRunningReportTime: Long = 0L,
        val future: ActionFuture = ActionFuture(),
    ) {
        val id = IdUtils.createStrId(false)

        override fun toString(): String {
            var str = "${executorInfo.executor.getName(actionContext)}-$id"
            if (actionContext.retryIdx >= 0) {
                str += "[retryIdx=${actionContext.retryIdx}]"
            }
            return str
        }

        fun getTimeInfo(): String {
            val nowMs = System.currentTimeMillis()
            var timeSinceExecStarted = 0L
            if (executionStartedAt > 0L) {
                timeSinceExecStarted = nowMs - executionStartedAt
            }
            return "Elapsed time: Since creation = ${nowMs - createdAt}ms " +
                "Since executionStarted = ${timeSinceExecStarted}ms"
        }
    }

    override fun dispose() {
        watcherEnabled.set(false)
        watcherThread?.interrupt()
        watcherThread = null
    }

    private class ActionFuture : CompletableFuture<Any>() {

        @Volatile
        var executeFuture: Future<*>? = null

        @Volatile
        var scheduleFuture: ScheduledFuture<*>? = null

        override fun cancel(mayInterruptIfRunning: Boolean): Boolean {
            try {
                executeFuture?.cancel(mayInterruptIfRunning)
            } catch (e: Throwable) {
                log.error(e) { "Exception during execution future cancelling" }
            }
            executeFuture = null
            try {
                scheduleFuture?.cancel(mayInterruptIfRunning)
            } catch (e: Throwable) {
                log.error(e) { "Exception during schedule future cancelling" }
            }
            scheduleFuture = null
            return super.cancel(mayInterruptIfRunning)
        }
    }
}
