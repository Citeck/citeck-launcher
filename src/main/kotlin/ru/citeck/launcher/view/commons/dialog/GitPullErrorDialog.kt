package ru.citeck.launcher.view.commons.dialog

import androidx.compose.foundation.layout.*
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.em
import kotlinx.coroutines.suspendCancellableCoroutine
import ru.citeck.launcher.view.popup.CiteckDialog
import kotlin.coroutines.resume

class GitPullErrorDialog(private val params: GitPullErrorDialogParams) : CiteckDialog() {

    companion object {
        fun show(
            repoUrl: String,
            errorMsg: String,
            skipAvailable: Boolean,
            cancelAvailable: Boolean,
            onSubmit: (GitPullRepoDialogRes) -> Unit
        ) {
            showDialog(
                GitPullErrorDialog(
                    GitPullErrorDialogParams(
                        repoUrl,
                        errorMsg,
                        skipAvailable,
                        cancelAvailable,
                        onSubmit
                    )
                )
            )
        }

        suspend fun showSuspend(
            repoUrl: String,
            errorMsg: String,
            skipAvailable: Boolean,
            cancelAvailable: Boolean
        ): GitPullRepoDialogRes {
            return suspendCancellableCoroutine { continuation ->
                show(repoUrl, errorMsg, skipAvailable, cancelAvailable) { continuation.resume(it) }
            }
        }
    }

    @Composable
    override fun render() {
        dialog {
            title("Git Repo Pull Failed")
            Text(
                params.errorMessage,
                textAlign = TextAlign.Left,
                fontSize = 0.8.em,
                modifier = Modifier.fillMaxWidth()
            )
            Spacer(modifier = Modifier.height(10.dp))
            Text(
                params.repoUrl,
                textAlign = TextAlign.Left
            )
            Spacer(modifier = Modifier.height(10.dp))
            if (params.skipAvailable) {
                Text(
                    "You can skip this pull or try again",
                    textAlign = TextAlign.Left
                )
            } else {
                Text(
                    "You can't skip this step because repo doesn't cloned before",
                    textAlign = TextAlign.Left
                )
            }
            buttonsRow {
                if (params.cancelAvailable) {
                    button("Cancel") {
                        params.onSubmit(GitPullRepoDialogRes.CANCEL)
                        closeDialog()
                    }
                }
                spacer()
                if (params.skipAvailable) {
                    button("Skip Pulling") {
                        params.onSubmit(GitPullRepoDialogRes.SKIP_IF_POSSIBLE)
                        closeDialog()
                    }
                }
                button("Try Again") {
                    params.onSubmit(GitPullRepoDialogRes.REPEAT)
                    closeDialog()
                }
            }
        }
    }
}

enum class GitPullRepoDialogRes {
    REPEAT,
    SKIP_IF_POSSIBLE,
    CANCEL
}

class GitPullErrorDialogParams(
    val repoUrl: String,
    val errorMessage: String,
    val skipAvailable: Boolean,
    val cancelAvailable: Boolean,
    val onSubmit: (GitPullRepoDialogRes) -> Unit
)
