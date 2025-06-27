package ru.citeck.launcher.core.actions

import java.util.concurrent.CompletableFuture

/**
 * Defines a contract for executing actions with parameters and producing a result.
 *
 * @param P the type of the parameters required to execute the action.
 * @param R the type of the result produced by the action.
 */
interface ActionExecutor<P : ActionParams<R>, R> {

    /**
     * Executes the action using the provided parameters.
     *
     * @param context the context with required data to perform the action.
     * @return the result of the action.
     */
    fun execute(context: ActionContext<P>): R

    /**
     * Returns a human-readable name or description for the given parameters.
     * Useful for logging, monitoring, or debugging purposes.
     *
     * @param context the context with required data to perform the action.
     * @return a descriptive name representing the action.
     */
    fun getName(context: ActionContext<P>): String

    /**
     * Defines a delay (in milliseconds) before retrying the action after an error.
     * This allows implementation of backoff strategies or disables retries completely.
     *
     * @param params the parameters used for the failed action.
     * @param iteration the retry attempt index, starting from 0 for the first retry.
     * @return
     *   - a positive delay in milliseconds before the next retry,
     *   - or a negative value to indicate that the action should not be retried.
     */
    fun getRetryAfterErrorDelay(context: ActionContext<P>, future: CompletableFuture<R>): Long = -1
}
