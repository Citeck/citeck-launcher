package ru.citeck.launcher.view.dialog

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Button
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.snapshots.SnapshotStateList
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.em
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlin.coroutines.resume

object GlobalGitPullErrorDialog {

    private lateinit var showDialog: (GitPullErrorDialogParams) -> (() -> Unit)

    fun show(
        repoUrl: String,
        errorMsg: String,
        skipAvailable: Boolean,
        cancelAvailable: Boolean,
        onSubmit: (GitPullRepoDialogRes) -> Unit
    ) {
        showDialog(GitPullErrorDialogParams(repoUrl, errorMsg, skipAvailable, cancelAvailable, onSubmit))
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

    @Composable
    fun PullErrorDialog(statesList: SnapshotStateList<CiteckDialogState>) {

        showDialog = CiteckDialog(statesList) { params, closeDialog ->

            Dialog(
                properties = DialogProperties(
                    usePlatformDefaultWidth = false
                ),
                onDismissRequest = {}
            ) {
                Surface(
                    shape = RoundedCornerShape(10.dp),
                    tonalElevation = 0.dp,
                    modifier = Modifier.width(600.dp).padding(5.dp)
                ) {
                    Column(modifier = Modifier.padding(top = 10.dp, bottom = 10.dp, start = 20.dp, end = 20.dp)) {
                        Text(
                            "Git Repo Pull Failed",
                            textAlign = TextAlign.Left,
                            fontSize = 1.2.em,
                            modifier = Modifier.fillMaxWidth(),
                            style = MaterialTheme.typography.titleLarge
                        )
                        Spacer(modifier = Modifier.height(5.dp))
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
                        Spacer(modifier = Modifier.height(16.dp))

                        Row(
                            modifier = Modifier.fillMaxWidth().height(50.dp).padding(5.dp)
                        ) {
                            if (params.cancelAvailable) {
                                Button(
                                    onClick = {
                                        params.onSubmit(GitPullRepoDialogRes.CANCEL)
                                        closeDialog()
                                    },
                                    modifier = Modifier.fillMaxHeight()
                                ) {
                                    Text("Cancel")
                                }
                            }
                            Spacer(Modifier.weight(1f))
                            if (params.skipAvailable) {
                                Button(
                                    onClick = {
                                        params.onSubmit(GitPullRepoDialogRes.SKIP_IF_POSSIBLE)
                                        closeDialog()
                                    },
                                    modifier = Modifier.fillMaxHeight()
                                ) {
                                    Text("Skip Pulling")
                                }
                                Spacer(modifier = Modifier.width(10.dp))
                            }
                            Button(
                                onClick = {
                                    params.onSubmit(GitPullRepoDialogRes.REPEAT)
                                    closeDialog()
                                },
                                modifier = Modifier.fillMaxHeight()
                            ) {
                                Text("Try Again")
                            }
                        }
                    }
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

private class GitPullErrorDialogParams(
    val repoUrl: String,
    val errorMessage: String,
    val skipAvailable: Boolean,
    val cancelAvailable: Boolean,
    val onSubmit: (GitPullRepoDialogRes) -> Unit
)
