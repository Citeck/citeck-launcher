package ru.citeck.launcher.view.dialog

import kotlinx.coroutines.suspendCancellableCoroutine
import ru.citeck.launcher.core.appdef.ApplicationDef
import ru.citeck.launcher.core.utils.json.Yaml
import ru.citeck.launcher.view.editor.EditorWindow
import ru.citeck.launcher.view.form.exception.FormCancelledException
import kotlin.coroutines.resume
import kotlin.coroutines.resumeWithException

object AppCfgEditWindow {

    suspend fun show(appDef: ApplicationDef): AppEditResponse? {
        val resp = show("app-def.yml", Yaml.toString(appDef)) {
            Yaml.read(it, ApplicationDef::class)
        } ?: return null
        return AppEditResponse(resp.content, true)
    }

    suspend fun <T : Any> show(filename: String, content: String, conv: (String) -> T): FileEditResponse<T>? {
        return suspendCancellableCoroutine { continuation ->
            EditorWindow.show(filename, content, onClose = {
                continuation.resumeWithException(FormCancelledException())
                true
            }) { ctx ->
                spacer()
                button("Reset") {
                    continuation.resume(null)
                    ctx.closeWindow()
                }
                button("Cancel") {
                    continuation.resumeWithException(FormCancelledException())
                    ctx.closeWindow()
                }
                button("Submit") {
                    try {
                        val convertedValue = conv(ctx.getText())
                        continuation.resume(FileEditResponse(convertedValue))
                        ctx.closeWindow()
                    } catch (e: Throwable) {
                        ctx.showError(e)
                    }
                }
            }
        }
    }

    class AppEditResponse(
        val appDef: ApplicationDef,
        val locked: Boolean
    )

    class FileEditResponse<T : Any>(
        val content: T
    )
}
