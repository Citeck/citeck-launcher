package ru.citeck.launcher.core.utils.promise

import java.time.Duration
import java.util.Collections
import java.util.TreeSet
import java.util.concurrent.*
import java.util.concurrent.CompletableFuture
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicInteger
import java.util.concurrent.atomic.AtomicReference

private typealias PromiseAction<T> = ((T) -> Unit, (Throwable) -> Unit) -> Unit

object Promises {

    @JvmStatic
    fun <T> resolve(value: T): Promise<T> {
        return CompletedPromise(value, null)
    }

    @JvmStatic
    fun <T> reject(error: Throwable): Promise<T> {
        return CompletedPromise(null, error)
    }

    @JvmStatic
    @JvmOverloads
    fun <T> create(future: CompletableFuture<T>, flush: (() -> Unit)? = null): Promise<T> {
        return FuturePromise(future, null, flush)
    }

    @JvmStatic
    fun <T> create(action: PromiseAction<T>): Promise<T> {
        return create(action, null)
    }

    @JvmStatic
    fun <T> create(action: PromiseAction<T>, flush: (() -> Unit)? = null): Promise<T> {

        val completedFuture = CompletableFuture<Boolean>()

        val result = AtomicReference<T>()
        val error = AtomicReference<Throwable>()

        try {
            action.invoke(
                {
                    if (completedFuture.isDone) {
                        throw IllegalStateException("Future already completed")
                    }
                    result.set(it)
                    completedFuture.complete(true)
                },
                {
                    if (completedFuture.isDone) {
                        throw IllegalStateException("Future already completed")
                    }
                    error.set(it)
                    completedFuture.complete(true)
                }
            )
        } catch (e: Throwable) {
            return CompletedPromise(null, e)
        }

        return FuturePromise(completedFuture, null, flush).thenPromise {
            CompletedPromise(result.get(), error.get())
        }
    }

    @JvmStatic
    fun <T> all(vararg promises: Promise<T>): Promise<List<T>> {
        return all(promises.toList())
    }

    @JvmStatic
    fun <T> all(promises: Iterable<Promise<T>>): Promise<List<T>> {
        var results: MutableSet<Pair<T, Int>> = TreeSet { pair0, pair1 ->
            pair0.second.compareTo(pair1.second)
        }
        results = Collections.synchronizedSet(results)
        return create(
            { resolve, reject ->
                val count = AtomicInteger()
                val iterationFinished = AtomicBoolean(false)
                promises.forEachIndexed { idx, promise ->
                    count.incrementAndGet()
                    promise.then {
                        results.add(it to idx)
                        if (iterationFinished.get() && count.compareAndSet(results.size, -1)) {
                            resolve(results.map { pair -> pair.first })
                        }
                    }.catch {
                        reject(it)
                    }
                }
                iterationFinished.set(true)
                if (count.compareAndSet(results.size, -1)) {
                    resolve(results.map { pair -> pair.first })
                }
            },
            {
                promises.forEach { it.flush() }
            }
        )
    }

    fun <T> withTimeout(promise: Promise<T>, timeout: Duration): Promise<T> {
        return PromiseWithTimeout(promise, timeout)
    }

    private class PromiseWithTimeout<T>(
        val promise: Promise<T>,
        val timeout: Duration
    ) : Promise<T> {
        override fun <E : Throwable> catch(type: Class<E>, action: (E) -> T): Promise<T> {
            return promise.catch(type, action)
        }
        override fun <E : Throwable> catchPromise(type: Class<E>, action: (E) -> Promise<T>): Promise<T> {
            return promise.catchPromise(type, action)
        }

        override fun finally(action: () -> Unit): Promise<T> {
            return promise.finally(action)
        }

        override fun flush() {
            return promise.flush()
        }

        override fun get(): T {
            return promise.get(timeout)
        }

        override fun get(timeout: Duration): T {
            return if (this.timeout > timeout) {
                promise.get(timeout)
            } else {
                promise.get(this.timeout)
            }
        }

        override fun getState(): PromiseState {
            return promise.getState()
        }

        override fun isDone(): Boolean {
            return promise.isDone()
        }

        override fun <R> then(action: (T) -> R): Promise<R> {
            return promise.then(action)
        }

        override fun <R> thenPromise(action: (T) -> Promise<R>): Promise<R> {
            return promise.thenPromise(action)
        }

        override fun cancel(mayInterruptIfRunning: Boolean): Boolean {
            return promise.cancel(mayInterruptIfRunning)
        }

        override fun isCancelled(): Boolean {
            return promise.isCancelled()
        }
    }

    private class FuturePromise<T>(
        private val future: CompletableFuture<T>,
        // origin link required to protect original future
        // from GC when hard links to it doesn't exist
        @Suppress("UNUSED")
        private val origin: Promise<*>?,
        private val flush: (() -> Unit)? = null
    ) : Promise<T> {

        private val flushed = AtomicBoolean()

        override fun get(timeout: Duration): T {
            flush()
            try {
                return future.get(timeout.toMillis(), TimeUnit.MILLISECONDS)
            } catch (e: TimeoutException) {
                throw e
            } catch (e: Throwable) {
                throw PromiseException(unwrapError(e))
            }
        }

        override fun get(): T {
            flush()
            try {
                return future.get()
            } catch (e: Throwable) {
                throw PromiseException(unwrapError(e))
            }
        }

        override fun <R> then(action: (T) -> R): Promise<R> {
            return FuturePromise(future.thenApply { action.invoke(it) }, this, flush)
        }

        override fun <R> thenPromise(action: (T) -> Promise<R>): Promise<R> {
            val result = CompletableFuture<R>()
            future.handle { res, error ->
                if (error != null) {
                    result.completeExceptionally(unwrapNullableError(error))
                } else {
                    try {
                        action.invoke(res).then { actionRes ->
                            result.complete(actionRes)
                        }.catch(Throwable::class.java) { actionError ->
                            result.completeExceptionally(actionError)
                        }
                    } catch (e: Throwable) {
                        result.completeExceptionally(e)
                    }
                }
            }
            return FuturePromise(result, this, flush)
        }

        override fun <E : Throwable> catch(type: Class<E>, action: (E) -> T): Promise<T> {
            return FuturePromise(
                future.exceptionally { rawError ->
                    val error = unwrapError(rawError)
                    val errorForCatch = unwrapErrorForCatchHandler(error, type)
                    if (errorForCatch != null) {
                        action.invoke(type.cast(errorForCatch))
                    } else {
                        throw error
                    }
                },
                this,
                flush
            )
        }

        private fun unwrapErrorForCatchHandler(error: Throwable?, expectedType: Class<*>): Throwable? {
            var resError = error ?: return null
            if (!CompletionException::class.java.isAssignableFrom(expectedType)) {
                val cause = resError.cause
                while (resError is CompletionException && cause != null) {
                    resError = cause
                }
            }
            if (expectedType.isInstance(resError)) {
                return resError
            }
            return null
        }

        override fun <E : Throwable> catchPromise(type: Class<E>, action: (E) -> Promise<T>): Promise<T> {
            val result = CompletableFuture<T>()
            future.handle { res, rawError ->
                if (rawError != null) {
                    val error = unwrapNullableError(rawError)
                    val errorForCatch = unwrapErrorForCatchHandler(error, type)
                    if (errorForCatch != null) {
                        try {
                            action.invoke(type.cast(errorForCatch)).then { actionRes ->
                                result.complete(actionRes)
                            }.catch(Throwable::class.java) { actionError ->
                                actionError.addSuppressed(error)
                                result.completeExceptionally(actionError)
                            }
                        } catch (e: Throwable) {
                            e.addSuppressed(error)
                            result.completeExceptionally(e)
                        }
                    } else {
                        result.completeExceptionally(error)
                    }
                } else {
                    result.complete(res)
                }
            }
            return FuturePromise(result, this, flush)
        }

        override fun finally(action: () -> Unit): Promise<T> {
            return FuturePromise(
                future.whenComplete { _, rawError ->
                    val error = unwrapNullableError(rawError)
                    try {
                        action.invoke()
                    } catch (e: Throwable) {
                        if (rawError != null) {
                            e.addSuppressed(error)
                        }
                        throw e
                    }
                },
                this,
                flush
            )
        }

        override fun isDone(): Boolean {
            return future.isDone
        }

        override fun getState(): PromiseState {
            return when {
                !isDone() -> PromiseState.PENDING
                future.isCompletedExceptionally -> PromiseState.REJECTED
                else -> PromiseState.FULFILLED
            }
        }

        override fun flush() {
            if (flushed.compareAndSet(false, true) && !isDone()) {
                try {
                    flush?.invoke()
                } catch (e: Throwable) {
                    future.completeExceptionally(unwrapError(e))
                }
            }
        }

        override fun cancel(mayInterruptIfRunning: Boolean): Boolean {
            return future.cancel(mayInterruptIfRunning)
        }

        override fun isCancelled(): Boolean {
            return future.isCancelled
        }

        private fun unwrapError(error: Throwable): Throwable {
            return unwrapNullableError(error) ?: error
        }

        private fun unwrapNullableError(error: Throwable?): Throwable? {
            var result = error
            while (result is ExecutionException && result.cause != null) {
                result = result.cause
            }
            return result
        }
    }

    private class CompletedPromise<T>(
        private val result: T?,
        private val error: Throwable?
    ) : Promise<T> {

        override fun isDone(): Boolean {
            return true
        }

        override fun get(timeout: Duration): T {
            return get()
        }

        override fun get(): T {
            if (error != null) {
                throw PromiseException(error)
            }
            @Suppress("UNCHECKED_CAST")
            return result as T
        }

        override fun getState(): PromiseState {
            return when {
                error != null -> PromiseState.REJECTED
                else -> PromiseState.FULFILLED
            }
        }

        override fun flush() { }

        override fun cancel(mayInterruptIfRunning: Boolean): Boolean {
            return isCancelled()
        }

        override fun isCancelled(): Boolean {
            return error is CancellationException
        }

        override fun <R> thenPromise(action: (T) -> Promise<R>): Promise<R> {
            return if (error != null) {
                CompletedPromise(null, error)
            } else {
                try {
                    action.invoke(get())
                } catch (e: Throwable) {
                    CompletedPromise(null, e)
                }
            }
        }

        override fun <R> then(action: (T) -> R): Promise<R> {
            return if (error != null) {
                CompletedPromise(null, error)
            } else {
                try {
                    CompletedPromise(action.invoke(get()), null)
                } catch (e: Throwable) {
                    CompletedPromise(null, e)
                }
            }
        }

        override fun <E : Throwable> catchPromise(type: Class<E>, action: (E) -> Promise<T>): Promise<T> {
            if (error == null || !type.isInstance(error)) {
                return this
            }
            return try {
                action.invoke(type.cast(error))
            } catch (e: Throwable) {
                e.addSuppressed(error)
                return CompletedPromise(null, e)
            }
        }

        override fun <E : Throwable> catch(type: Class<E>, action: (E) -> T): Promise<T> {
            if (error == null || !type.isInstance(error)) {
                return this
            }
            return try {
                CompletedPromise(action.invoke(type.cast(error)), null)
            } catch (e: Throwable) {
                e.addSuppressed(error)
                CompletedPromise(null, e)
            }
        }

        override fun finally(action: () -> Unit): Promise<T> {
            try {
                action.invoke()
            } catch (e: Throwable) {
                if (error != null) {
                    e.addSuppressed(error)
                }
                return CompletedPromise(null, e)
            }
            return this
        }
    }
}
