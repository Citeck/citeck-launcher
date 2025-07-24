package ru.citeck.launcher.core.git

import io.github.oshai.kotlinlogging.KotlinLogging
import kotlinx.coroutines.runBlocking
import org.apache.commons.io.FileUtils
import org.apache.commons.lang3.exception.ExceptionUtils
import org.eclipse.jgit.api.CloneCommand.Callback
import org.eclipse.jgit.api.Git
import org.eclipse.jgit.errors.TransportException
import org.eclipse.jgit.lib.AnyObjectId
import org.eclipse.jgit.transport.UsernamePasswordCredentialsProvider
import ru.citeck.launcher.core.LauncherServices
import ru.citeck.launcher.core.config.AppDir
import ru.citeck.launcher.core.database.Repository
import ru.citeck.launcher.core.entity.EntityIdType
import ru.citeck.launcher.core.secrets.auth.AuthSecret
import ru.citeck.launcher.core.secrets.auth.AuthSecretsService
import ru.citeck.launcher.core.secrets.auth.AuthType
import ru.citeck.launcher.core.secrets.auth.SecretDef
import ru.citeck.launcher.core.utils.LongTaskUtils
import ru.citeck.launcher.core.utils.json.Json
import ru.citeck.launcher.view.dialog.GitPullErrorDialog
import ru.citeck.launcher.view.dialog.GitPullRepoDialogRes
import java.io.File
import java.time.Instant
import java.time.OffsetDateTime
import java.time.OffsetTime
import java.time.ZoneId
import java.time.temporal.ChronoUnit
import kotlin.io.path.exists
import kotlin.system.measureTimeMillis

class GitRepoService {

    companion object {
        private val log = KotlinLogging.logger {}

        private const val FEEDBACK_REPEATS_COUNT = 5
        private const val FEEDBACK_TIMEOUT_MS = 10_000
    }

    private lateinit var authSecretsService: AuthSecretsService
    private lateinit var repositoriesInfo: Repository<String, GitRepoInstance>

    fun init(services: LauncherServices) {
        this.authSecretsService = services.authSecretsService
        repositoriesInfo = services.database.getRepo(
            EntityIdType.String,
            Json.getSimpleType(GitRepoInstance::class),
            "git-repo",
            "instances"
        )
    }

    fun initRepo(
        repoProps: GitRepoProps,
        updatePolicy: GitUpdatePolicy = GitUpdatePolicy.ALLOWED,
        cancelAvailable: Boolean = false
    ): GitRepoInfo {
        val relativePath = AppDir.PATH.relativize(repoProps.path).toString().replace(File.separator, "/")
        var updatePolicy = updatePolicy
        var nextFeedbackRepeats = FEEDBACK_REPEATS_COUNT
        var nextFeedbackTime = System.currentTimeMillis() + FEEDBACK_TIMEOUT_MS
        return LongTaskUtils.doWithWatcher(actionName = "Init repo ${repoProps.url}") {
            var exception: Throwable? = null
            while (true) {
                try {
                    return@doWithWatcher initRepoImpl(
                        relativePath,
                        repoProps,
                        updatePolicy,
                        isUnauthorizedException(exception)
                    )
                } catch (e: Throwable) {
                    exception = e
                    if (nextFeedbackRepeats-- > 0 && isUnauthorizedException(e) && repoProps.authType != AuthType.NONE) {
                        log.error { "Repo unauthorized: " + repoProps.url }
                        continue
                    }
                    log.error(e) { "Repo initialization failed. Props: $repoProps. Update policy: $updatePolicy" }
                    if (nextFeedbackRepeats <= 0 || System.currentTimeMillis() >= nextFeedbackTime) {
                        val allowSkip = (repositoriesInfo[relativePath]?.lastSyncTimeMs ?: 0L) > 0L
                        val dialogRes = runBlocking {
                            GitPullErrorDialog.showSuspend(
                                repoProps.url,
                                ExceptionUtils.getRootCauseMessage(e) ?: "no-msg",
                                allowSkip,
                                cancelAvailable
                            )
                        }
                        if (dialogRes == GitPullRepoDialogRes.CANCEL) {
                            throw GitPullCancelledException(repoProps.url)
                        }
                        if (dialogRes == GitPullRepoDialogRes.SKIP_IF_POSSIBLE) {
                            updatePolicy = GitUpdatePolicy.ALLOWED_IF_NOT_EXISTS
                        }
                        nextFeedbackRepeats = FEEDBACK_REPEATS_COUNT
                        nextFeedbackTime = System.currentTimeMillis() + FEEDBACK_TIMEOUT_MS
                    } else {
                        Thread.sleep(1000)
                    }
                }
            }
            error("Invalid state. Repo: ${repoProps.url}")
        }
    }

    private fun isUnauthorizedException(exception: Throwable?): Boolean {
        exception ?: return false
        val rootCause = ExceptionUtils.getRootCause(exception) ?: exception
        if (rootCause !is TransportException) {
            return false
        }
        val message = rootCause.message ?: return false
        return message.contains("authentication is required", ignoreCase = true) ||
            message.contains("not authorized", ignoreCase = true) ||
            message.contains("401") ||
            message.contains("403")
    }

    private fun isRepoShouldBeRecreated(propsBefore: GitRepoProps, propsAfter: GitRepoProps): Boolean {
        return propsBefore.url != propsAfter.url || propsBefore.branch != propsAfter.branch
    }

    private fun initRepoImpl(
        relativePath: String,
        repoProps: GitRepoProps,
        updatePolicy: GitUpdatePolicy = GitUpdatePolicy.ALLOWED,
        resetSecret: Boolean
    ): GitRepoInfo {

        log.info { "[$relativePath] Init repo ${repoProps.url} with branch '${repoProps.branch}'" }

        val existingRepo = repositoriesInfo[relativePath]
        if (existingRepo != null && isRepoShouldBeRecreated(existingRepo.props, repoProps)) {
            log.warn {
                "Found existing repo for path $relativePath and props ${existingRepo.props}. " +
                    "Repo will be replaced with data from $repoProps"
            }
            if (repoProps.path.exists()) {
                repoProps.path.toFile().deleteRecursively()
            }
            repositoriesInfo.delete(relativePath)
        }

        val credentialsProvider = if (repoProps.authType != AuthType.NONE) {

            val secret = runBlocking {
                authSecretsService.getSecret(
                    SecretDef(
                        repoProps.authId,
                        repoProps.authType
                    ),
                    repoProps.url,
                    resetSecret
                )
            }
            when (secret) {
                is AuthSecret.Token -> {
                    UsernamePasswordCredentialsProvider("", secret.token)
                }

                is AuthSecret.Basic -> {
                    UsernamePasswordCredentialsProvider(secret.username, secret.password)
                }
            }
        } else {
            UsernamePasswordCredentialsProvider("", "")
        }

        val repoDir = repoProps.path
        var repoInfo: GitRepoInstance
        if (existingRepo == null || !repoDir.resolve(".git").exists()) {
            if (updatePolicy == GitUpdatePolicy.DISABLED) {
                error("Repo doesn't exists and update is disabled")
            }
            log.info { "[$relativePath] Repo directory doesn't exists. Clone repo." }
            val totalMs = measureTimeMillis {
                FileUtils.deleteDirectory(repoDir.toFile())
                var hashOfLastCommit = ""
                Git.cloneRepository()
                    .setURI(repoProps.url)
                    .setDirectory(repoDir.toFile())
                    .setBranchesToClone(listOf("refs/heads/${repoProps.branch}"))
                    .setCloneAllBranches(false)
                    .setBranch("refs/heads/${repoProps.branch}")
                    .setCredentialsProvider(credentialsProvider)
                    .setDepth(1)
                    .setCallback(object : Callback {
                        override fun initializedSubmodules(submodules: MutableCollection<String>?) {
                            log.info { "initializedSubmodules: $submodules" }
                        }

                        override fun cloningSubmodule(path: String?) {
                            log.info { "cloningSubmodule: $path" }
                        }

                        override fun checkingOut(commit: AnyObjectId?, path: String?) {
                            log.info { "checkingOut. Commit: $commit Path: $path" }
                        }
                    })
                    .setNoTags()
                    .setTimeout(15)
                    .call()
                    .use { hashOfLastCommit = it.getLastCommitHash() }

                repoInfo = GitRepoInstance(repoProps, System.currentTimeMillis(), hashOfLastCommit)
                repositoriesInfo[relativePath] = repoInfo
            }
            log.info { "[$relativePath] Repo successfully cloned. Elapsed time: ${totalMs}ms" }
        } else {
            val currentTimeMs = System.currentTimeMillis()
            val lastSyncDiffMs = currentTimeMs - existingRepo.lastSyncTimeMs
            repoInfo = existingRepo

            if (updatePolicy == GitUpdatePolicy.DISABLED || updatePolicy == GitUpdatePolicy.ALLOWED_IF_NOT_EXISTS) {
                log.debug { "[$relativePath] Repo exists and update is disabled" }
            } else if (
                updatePolicy == GitUpdatePolicy.REQUIRED ||
                updatePolicy == GitUpdatePolicy.ALLOWED &&
                lastSyncDiffMs > repoProps.pullPeriod.toMillis()
            ) {
                log.info { "[$relativePath] Repo directory exists. Pull repo." }
                var hashOfLastCommit = ""
                val totalMs = measureTimeMillis {
                    Git.open(repoDir.toFile()).use {
                        it.pull()
                            .setCredentialsProvider(credentialsProvider)
                            .setTimeout(15)
                            .call()
                        hashOfLastCommit = it.getLastCommitHash()
                    }
                }
                repoInfo = existingRepo.withLastSync(currentTimeMs, hashOfLastCommit)
                repositoriesInfo[relativePath] = repoInfo
                log.info { "[$relativePath] Repo successfully pulled. Elapsed time: ${totalMs}ms" }
            } else {
                log.info {
                    "[$relativePath] Repo already in sync. " +
                        "Current time: ${currentTimeMs.timestampMsToCurrentTime()} " +
                        "Next sync time: ${(existingRepo.lastSyncTimeMs + repoProps.pullPeriod.toMillis()).timestampMsToCurrentTime()} " +
                        "Last sync time: ${existingRepo.lastSyncTimeMs.timestampMsToCurrentTime()}"
                }
            }
        }
        return GitRepoInfo(
            repoDir,
            repoInfo.lastCommitHash,
            repoInfo.lastSyncTimeMs + repoProps.pullPeriod.toMillis()
        )
    }

    private fun Long.timestampMsToCurrentTime(): OffsetTime {
        return OffsetDateTime.ofInstant(
            Instant.ofEpochMilli(this).truncatedTo(ChronoUnit.SECONDS),
            ZoneId.systemDefault()
        ).toOffsetTime()
    }

    private fun Git.getLastCommitHash(): String {
        return this.log().setMaxCount(1).call().firstOrNull()?.id?.name ?: ""
    }

    private data class GitRepoInstance(
        val props: GitRepoProps,
        val lastSyncTimeMs: Long,
        val lastCommitHash: String
    ) {
        fun withLastSync(time: Long, hash: String): GitRepoInstance {
            return copy(lastSyncTimeMs = time, lastCommitHash = hash)
        }
    }
}
