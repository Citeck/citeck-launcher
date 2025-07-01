package ru.citeck.launcher.core.namespace.runtime.actions

import com.github.dockerjava.api.command.PullImageResultCallback
import com.github.dockerjava.api.exception.DockerClientException
import com.github.dockerjava.api.model.AuthConfig
import com.github.dockerjava.api.model.PullResponseItem
import io.github.oshai.kotlinlogging.KotlinLogging
import kotlinx.coroutines.runBlocking
import ru.citeck.launcher.core.actions.ActionContext
import ru.citeck.launcher.core.actions.ActionExecutor
import ru.citeck.launcher.core.actions.ActionParams
import ru.citeck.launcher.core.actions.ActionsService
import ru.citeck.launcher.core.appdef.ApplicationKind
import ru.citeck.launcher.core.namespace.runtime.AppRuntime
import ru.citeck.launcher.core.namespace.runtime.docker.DockerApi
import ru.citeck.launcher.core.secrets.auth.AuthSecret
import ru.citeck.launcher.core.secrets.auth.AuthSecretsService
import ru.citeck.launcher.core.secrets.auth.AuthType
import ru.citeck.launcher.core.secrets.auth.SecretDef
import ru.citeck.launcher.core.utils.promise.Promise
import ru.citeck.launcher.core.workspace.WorkspaceConfig
import java.time.Duration
import java.util.concurrent.CompletableFuture
import java.util.concurrent.LinkedBlockingQueue
import java.util.concurrent.Semaphore
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicLong
import java.util.concurrent.atomic.AtomicReference
import kotlin.math.max
import kotlin.math.min
import kotlin.math.roundToInt

class AppImagePullAction(
    private val dockerApi: DockerApi,
    private val authSecretsService: AuthSecretsService
) : ActionExecutor<AppImagePullAction.Params, Unit> {

    companion object {
        private val log = KotlinLogging.logger {}

        private const val RETRIES_COUNT_FOR_EXISTING_IMAGE = 3

        private val LAST_PULL_RESPONSE_TIMEOUT_MS = Duration.ofMinutes(1).toMillis()
        // global param to avoid errors while some pull actions wait until other actions completed
        private val lastPullResponseTime = AtomicLong(System.currentTimeMillis())

        private val RETRY_DELAYS = listOf(
            Duration.ZERO,
            Duration.ZERO,
            Duration.ofSeconds(1),
            Duration.ofSeconds(5),
            Duration.ofSeconds(10)
        )

        private val pullSemaphore = Semaphore(4)

        fun execute(
            service: ActionsService,
            appRuntime: AppRuntime,
            pullIfPresent: Boolean
        ): Promise<Unit> {
            return service.execute(Params(appRuntime, pullIfPresent))
        }
    }

    override fun getName(context: ActionContext<Params>): String {
        return "pull(${context.params.appRuntime.name})"
    }

    override fun getRetryAfterErrorDelay(context: ActionContext<Params>, future: CompletableFuture<Unit>): Long {
        val params = context.params
        if (
            params.pullIfPresent
            && context.retryIdx >= RETRIES_COUNT_FOR_EXISTING_IMAGE
            && dockerApi.inspectImageOrNull(params.appRuntime.image) != null
        ) {
            log.warn(context.lastError) {
                "Pulling failed for ${params.currentPulledImage} " +
                    "after $RETRIES_COUNT_FOR_EXISTING_IMAGE iterations but image exists locally. " +
                    "Pull retrying will be stopped."
            }
            future.complete(Unit)
            return -1
        }
        if (context.lastError is AuthenticationCancelled) {
            return -1
        }
        if (context.lastError is RepoUnauthorizedException) {
            return 0
        }
        return RETRY_DELAYS[min(context.retryIdx, RETRY_DELAYS.lastIndex)].toMillis()
    }

    override fun execute(context: ActionContext<Params>) {

        if (!pullSemaphore.tryAcquire(1, TimeUnit.MINUTES)) {
            error("Pull semaphore waiting timeout")
        }
        val params = context.params
        val appRuntime = params.appRuntime
        try {
            while (params.currentPulledImage != null || params.imagesToPull.isNotEmpty()) {
                var imageToPull = params.currentPulledImage
                if (imageToPull == null) {
                    imageToPull = params.imagesToPull.poll()
                    params.currentPulledImage = imageToPull
                }
                if (imageToPull == null) {
                    break
                }
                pullImage(params, imageToPull.image, imageToPull.kind, appRuntime, context.retryIdx, context.lastError)
                context.retryIdx = -1
                params.currentPulledImage = null
                params.pulledImagesCount++
            }
        } finally {
            pullSemaphore.release()
            appRuntime.statusText.value = ""
        }
    }

    private fun pullImage(
        params: Params,
        image: String,
        appKind: ApplicationKind,
        appRuntime: AppRuntime,
        retryIdx: Int,
        lastError: Throwable?
    ) {
        if (appKind.isCiteckApp() && !image.contains("/")) {
            return
        }
        if (!params.pullIfPresent && dockerApi.inspectImageOrNull(image) != null) {
            return
        }
        log.info { "Start image pulling: $image" }
        lastPullResponseTime.set(System.currentTimeMillis())
        val lastPullInfo = AtomicReference<PullResponseItem>()
        val pullFuture = CompletableFuture<Boolean>()

        val pullCmd = dockerApi.pullImage(image)

        val imageRepoUrl = image.substringBefore("/")
        val repoInfo = appRuntime.nsRuntime.workspaceConfig.imageReposByHost[imageRepoUrl]
        val secretDef: SecretDef? = if (repoInfo?.authType == WorkspaceConfig.ImageRepoAuth.BASIC) {
            SecretDef("images-repo:$imageRepoUrl", AuthType.BASIC)
        } else {
            null
        }
        val secret: AuthSecret.Basic? = secretDef?.let {
            runBlocking {
                authSecretsService.getSecret(
                    it,
                    imageRepoUrl,
                    (lastError as? RepoUnauthorizedException)?.secretVersion ?: 0L
                ) as? AuthSecret.Basic
            }
        }
        if (secret != null) {
            pullCmd.withAuthConfig(
                AuthConfig()
                    .withUsername(secret.username)
                    .withPassword(String(secret.password))
            )
        }

        val pullCallback = object : PullImageResultCallback() {
            override fun onNext(item: PullResponseItem?) {
                lastPullResponseTime.set(System.currentTimeMillis())
                super.onNext(item)
                lastPullInfo.set(item)
            }

            override fun onComplete() {
                lastPullResponseTime.set(System.currentTimeMillis())
                super.onComplete()
                val lastItemStatus = lastPullInfo.get()
                if (lastItemStatus?.isPullSuccessIndicated() == true) {
                    pullFuture.complete(true)
                } else {
                    pullFuture.completeExceptionally(RuntimeException("Pull doesn't indicated as successful"))
                }
            }

            private fun getMsgFromPullResult(): String {
                val pullItem = lastPullInfo.get() ?: return "no-item"
                val errorDetail = pullItem.errorDetail
                return if (errorDetail != null) {
                    "[${errorDetail.code}] Error: " + errorDetail.message
                } else {
                    "Status: " + pullItem.status
                }
            }
            override fun onError(throwable: Throwable?) {
                lastPullResponseTime.set(System.currentTimeMillis())
                val exception = if (throwable?.message?.contains("unauthorized", true) == true) {
                    RepoUnauthorizedException(secret?.version ?: 0L)
                } else {
                    DockerClientException("Could not pull image. " + getMsgFromPullResult())
                }
                if (throwable != null) {
                    exception.addSuppressed(throwable)
                }
                super.onError(exception)
                pullFuture.completeExceptionally(exception)
            }
        }
        try {
            pullCmd.exec(pullCallback)
        } catch (e: Throwable) {
            throw e
        }
        val pullInProgress = AtomicBoolean(true)
        Thread.ofVirtual().name("pull-watcher-${appRuntime.name}").start {
            var mbLen = 3
            while (pullInProgress.get()) {
                val details = lastPullInfo.get()?.progressDetail
                var totalBytes = 0L
                var actualBytes = 0L
                if (details != null) {
                    actualBytes = details.current ?: 0L
                    totalBytes = details.total ?: 0L
                }
                var statusText = if (totalBytes > 0) {
                    val percents = (actualBytes * 100f / totalBytes).roundToInt()
                    var megabytes = ((totalBytes * 10f / 1024f / 1024f).roundToInt() / 10f).toString()
                    mbLen = max(megabytes.length, mbLen)
                    megabytes = megabytes.padStart(mbLen)
                    "" + megabytes + "mb " + percents + "%"
                } else {
                    "--mb".padStart(mbLen) + " --%"
                }
                if (params.totalImagesToPull > 1) {
                    statusText = "(" + params.pulledImagesCount + "/" + params.totalImagesToPull + ") " + statusText
                }
                if (retryIdx >= 0) {
                    statusText += " (" + (retryIdx + 1) + ")"
                }
                appRuntime.statusText.value = statusText
                Thread.sleep(2000)
                if (System.currentTimeMillis() - lastPullResponseTime.get() > LAST_PULL_RESPONSE_TIMEOUT_MS) {
                    pullCallback.onError(RuntimeException("No pull updates during ${LAST_PULL_RESPONSE_TIMEOUT_MS}ms"))
                }
            }
        }
        try {
            pullFuture.get(1, TimeUnit.HOURS)
        } finally {
            pullInProgress.set(false)
        }
    }

    class Params(
        val appRuntime: AppRuntime,
        val pullIfPresent: Boolean
    ) : ActionParams<Unit> {

        @Volatile
        internal var currentPulledImage: ImageToPull? = null
        internal val imagesToPull: LinkedBlockingQueue<ImageToPull>
        val totalImagesToPull: Int
        @Volatile
        var pulledImagesCount: Int = 0

        init {
            val imagesToPullSet = HashSet<ImageToPull>()
            val def = appRuntime.def
            imagesToPullSet.add(ImageToPull(def.value.image, def.value.kind))
            def.value.initContainers.forEach { imagesToPullSet.add(ImageToPull(it.image, it.kind)) }
            this.imagesToPull = LinkedBlockingQueue(imagesToPullSet)
            totalImagesToPull = this.imagesToPull.size
        }
    }

    internal data class ImageToPull(
        val image: String,
        val kind: ApplicationKind
    ) {
        override fun toString(): String {
            return image
        }
    }
}
