package ru.citeck.launcher.view.ui

import ru.citeck.launcher.core.WorkspaceServices
import ru.citeck.launcher.core.entity.EntitiesService
import ru.citeck.launcher.core.form.FormMode
import ru.citeck.launcher.core.form.spec.FormSpec
import ru.citeck.launcher.core.ui.GitPullErrorResult
import ru.citeck.launcher.core.ui.UiProvider
import ru.citeck.launcher.core.utils.ActionStatus
import ru.citeck.launcher.core.utils.data.DataValue
import ru.citeck.launcher.view.commons.dialog.AskMasterPasswordDialog
import ru.citeck.launcher.view.commons.dialog.ConfirmDialog
import ru.citeck.launcher.view.commons.dialog.CreateMasterPwdDialog
import ru.citeck.launcher.view.commons.dialog.ErrorDialog
import ru.citeck.launcher.view.commons.dialog.GitPullErrorDialog
import ru.citeck.launcher.view.commons.dialog.GitPullRepoDialogRes
import ru.citeck.launcher.view.commons.dialog.GlobalMsgDialogParams
import ru.citeck.launcher.view.commons.dialog.LoadingDialog
import ru.citeck.launcher.view.commons.dialog.MessageDialog
import ru.citeck.launcher.view.form.FormDialog
import ru.citeck.launcher.view.popup.CiteckWindow
import ru.citeck.launcher.view.popup.DialogWidth

class ComposeUiProvider : UiProvider {

    override suspend fun showForm(
        entitiesService: EntitiesService,
        workspaceServices: WorkspaceServices?,
        spec: FormSpec,
        mode: FormMode,
        data: DataValue
    ): DataValue {
        return FormDialog.show(entitiesService, workspaceServices, spec, mode, data)
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
        FormDialog.show(entitiesService, workspaceServices, spec, mode, data, onCancel, onSubmit)
    }

    override suspend fun confirm(title: String, message: String): Boolean {
        return ConfirmDialog.showSuspended(title, message)
    }

    override suspend fun showGitPullError(
        error: Throwable,
        repoUrl: String,
        allowSkip: Boolean,
        allowCancel: Boolean
    ): GitPullErrorResult {
        val dialogRes = GitPullErrorDialog.showSuspend(
            repoUrl,
            org.apache.commons.lang3.exception.ExceptionUtils.getRootCauseMessage(error) ?: "no-msg",
            allowSkip,
            allowCancel
        )
        return when (dialogRes) {
            GitPullRepoDialogRes.CANCEL -> GitPullErrorResult.CANCEL
            GitPullRepoDialogRes.SKIP_IF_POSSIBLE -> GitPullErrorResult.SKIP
            GitPullRepoDialogRes.REPEAT -> GitPullErrorResult.RETRY
        }
    }

    override fun showError(error: Throwable) {
        ErrorDialog.show(error)
    }

    override fun showLoading(status: ActionStatus.Mut?): () -> Unit {
        return LoadingDialog.show(status)
    }

    override fun showMessage(title: String, message: String) {
        MessageDialog.show(GlobalMsgDialogParams(title, message, width = DialogWidth.EXTRA_SMALL))
    }

    override fun closeAllWindows() {
        CiteckWindow.closeAll()
    }

    override fun askMasterPassword(
        onSubmit: (CharArray) -> Boolean,
        onReset: () -> Unit
    ) {
        AskMasterPasswordDialog.show(onSubmit, onReset)
    }

    override suspend fun createMasterPassword(): CharArray {
        return CreateMasterPwdDialog.showSuspend()
    }
}
