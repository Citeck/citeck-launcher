package ru.citeck.launcher.core.ui

import io.github.oshai.kotlinlogging.KotlinLogging
import ru.citeck.launcher.core.WorkspaceServices
import ru.citeck.launcher.core.entity.EntitiesService
import ru.citeck.launcher.core.form.FormMode
import ru.citeck.launcher.core.form.spec.FormSpec
import ru.citeck.launcher.core.utils.ActionStatus
import ru.citeck.launcher.core.utils.data.DataValue

class HeadlessUiProvider : UiProvider {

    companion object {
        private val log = KotlinLogging.logger {}
    }

    override suspend fun showForm(
        entitiesService: EntitiesService,
        workspaceServices: WorkspaceServices?,
        spec: FormSpec,
        mode: FormMode,
        data: DataValue
    ): DataValue {
        throw UnsupportedOperationException("Forms are not supported in headless mode")
    }

    override fun showForm(
        entitiesService: EntitiesService,
        workspaceServices: WorkspaceServices?,
        spec: FormSpec,
        mode: FormMode,
        data: DataValue,
        onCancel: () -> Unit,
        onSubmit: (DataValue, onComplete: () -> Unit) -> Unit
    ) {
        throw UnsupportedOperationException("Forms are not supported in headless mode")
    }

    override suspend fun confirm(title: String, message: String): Boolean {
        log.info { "Auto-confirming in headless mode: $title - $message" }
        return true
    }

    override suspend fun showGitPullError(
        error: Throwable,
        repoUrl: String,
        allowSkip: Boolean,
        allowCancel: Boolean
    ): GitPullErrorResult {
        log.error(error) { "Git pull error for repo: $repoUrl" }
        return GitPullErrorResult.RETRY
    }

    override fun showError(error: Throwable) {
        log.error(error) { "Error occurred" }
    }

    override fun showLoading(status: ActionStatus.Mut?): () -> Unit {
        return {}
    }

    override fun showMessage(title: String, message: String) {
        log.info { "Message: $title - $message" }
    }

    override fun closeAllWindows() {
        // no-op in headless mode
    }

    override fun askMasterPassword(
        onSubmit: (CharArray) -> Boolean,
        onReset: () -> Unit
    ) {
        throw UnsupportedOperationException("Master password dialog is not supported in headless mode")
    }

    override suspend fun createMasterPassword(): CharArray {
        throw UnsupportedOperationException("Master password creation is not supported in headless mode")
    }
}
