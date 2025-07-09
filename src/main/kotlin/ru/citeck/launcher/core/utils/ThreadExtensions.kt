package ru.citeck.launcher.core.utils

import ru.citeck.launcher.core.utils.promise.Promise
import ru.citeck.launcher.core.utils.promise.Promises
import java.util.concurrent.CompletableFuture
import java.util.concurrent.atomic.AtomicBoolean

fun <T> Thread.Builder.startForPromise(
    completed: AtomicBoolean = AtomicBoolean(false),
    action: (completed: AtomicBoolean) -> T
): Promise<T> {
    val future = FutureWithThread<T>(completed)
    future.thread = unstarted {
        try {
            future.complete(action.invoke(completed))
            completed.set(true)
        } catch (e: Throwable) {
            future.completeExceptionally(e)
        }
    }
    future.thread.start()
    return Promises.create(future)
}

private class FutureWithThread<T>(
    private val completed: AtomicBoolean
) : CompletableFuture<T>() {
    lateinit var thread: Thread
    override fun cancel(mayInterruptIfRunning: Boolean): Boolean {
        completed.set(true)
        if (mayInterruptIfRunning) {
            thread.interrupt()
        }
        return super.cancel(mayInterruptIfRunning)
    }
}
