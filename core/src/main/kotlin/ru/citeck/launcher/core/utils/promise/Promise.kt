package ru.citeck.launcher.core.utils.promise

import java.time.Duration

/**
 * A Promise represents the result of an asynchronous
 * computation. Methods are provided to check if the computation is
 * complete, to wait for its completion, and to retrieve the result of
 * the computation. The result can only be retrieved using method
 * {@code get} when the computation has completed, blocking if
 * necessary until it is ready.
 */
interface Promise<out T> {

    /**
     * If this method return true then method get should
     * return result of this future without any delay
     *
     * Method should not throw any exception
     */
    fun isDone(): Boolean

    /**
     * Method may throw error occurred while promise execution
     *
     * @throws java.util.concurrent.TimeoutException
     */
    fun get(timeout: Duration): T

    /**
     * Block current thread until promise execution
     *
     * Method may throw error occurred while promise execution
     */
    fun get(): T

    /**
     * Signal for async task to start execution.
     * Interface doesn't give any guaranties about execution moment
     * of a task under this promise. Calling of this method may do nothing.
     *
     * Method should not throw any exception.
     */
    fun flush()

    fun getState(): PromiseState

    /**
     * Method should not throw any exception
     */
    fun <R> then(action: (T) -> R): Promise<R>

    /**
     * Method should not throw any exception
     */
    fun <R> thenPromise(action: (T) -> Promise<R>): Promise<R>

    /**
     * Method should not throw any exception
     */
    fun catch(action: (Throwable) -> @UnsafeVariance T): Promise<T> {
        return catch(Throwable::class.java, action)
    }

    /**
     * Method should not throw any exception
     */
    fun catchPromise(action: (Throwable) -> Promise<@UnsafeVariance T>): Promise<T> {
        return catchPromise(Throwable::class.java, action)
    }

    /**
     * Method should not throw any exception
     */
    fun <E : Throwable> catch(type: Class<E>, action: (E) -> @UnsafeVariance T): Promise<T>

    /**
     * Method should not throw any exception
     */
    fun <E : Throwable> catchPromise(type: Class<E>, action: (E) -> Promise<@UnsafeVariance T>): Promise<T>

    /**
     * Attaches a callback that is invoked when the Promise is settled (fulfilled or rejected). The
     * resolved value cannot be modified from the callback.
     *
     * Method should not throw any exception
     *
     * @param action The callback to execute when the Promise is settled (fulfilled or rejected).
     * @returns A Promise for the completion of the callback.
     */
    fun finally(action: () -> Unit): Promise<T>

    /**
     * Attempts to cancel execution of this task.  This method has no
     * effect if the task is already completed or cancelled, or could
     * not be cancelled for some other reason.  Otherwise, if this
     * task has not started when {@code cancel} is called, this task
     * should never run.  If the task has already started, then the
     * {@code mayInterruptIfRunning} parameter determines whether the
     * thread executing this task (when known by the implementation)
     * is interrupted in an attempt to stop the task.
     *
     * <p>The return value from this method does not necessarily
     * indicate whether the task is now cancelled; use {@link
     * #isCancelled}.
     *
     * @param mayInterruptIfRunning {@code true} if the thread
     * executing this task should be interrupted (if the thread is
     * known to the implementation); otherwise, in-progress tasks are
     * allowed to complete
     * @return {@code false} if the task could not be cancelled,
     * typically because it has already completed; {@code true}
     * otherwise. If two or more threads cause a task to be cancelled,
     * then at least one of them returns {@code true}. Implementations
     * may provide stronger guarantees.
     */
    fun cancel(mayInterruptIfRunning: Boolean): Boolean

    /**
     * Returns {@code true} if this task was cancelled before it completed
     * normally.
     *
     * @return {@code true} if this task was cancelled before it completed
     */
    fun isCancelled(): Boolean
}
