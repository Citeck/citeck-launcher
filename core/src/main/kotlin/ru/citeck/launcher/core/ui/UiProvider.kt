package ru.citeck.launcher.core.ui

import ru.citeck.launcher.core.WorkspaceServices
import ru.citeck.launcher.core.entity.EntitiesService
import ru.citeck.launcher.core.form.FormMode
import ru.citeck.launcher.core.form.spec.FormSpec
import ru.citeck.launcher.core.utils.ActionStatus
import ru.citeck.launcher.core.utils.data.DataValue

interface UiProvider {

    suspend fun showForm(
        entitiesService: EntitiesService,
        workspaceServices: WorkspaceServices?,
        spec: FormSpec,
        mode: FormMode,
        data: DataValue
    ): DataValue

    fun showForm(
        entitiesService: EntitiesService,
        workspaceServices: WorkspaceServices?,
        spec: FormSpec,
        mode: FormMode,
        data: DataValue,
        onCancel: () -> Unit,
        onSubmit: (DataValue, onComplete: () -> Unit) -> Unit
    )

    suspend fun confirm(title: String, message: String): Boolean

    suspend fun showGitPullError(
        error: Throwable,
        repoUrl: String,
        allowSkip: Boolean,
        allowCancel: Boolean
    ): GitPullErrorResult

    fun showError(error: Throwable)

    fun showMessage(title: String, message: String)

    fun showLoading(status: ActionStatus.Mut?): () -> Unit

    fun closeAllWindows()

    fun askMasterPassword(
        onSubmit: (CharArray) -> Boolean,
        onReset: () -> Unit
    )

    suspend fun createMasterPassword(): CharArray
}

enum class GitPullErrorResult {
    RETRY,
    SKIP,
    CANCEL
}
