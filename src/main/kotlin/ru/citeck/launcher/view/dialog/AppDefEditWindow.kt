package ru.citeck.launcher.view.dialog

import kotlinx.coroutines.suspendCancellableCoroutine
import ru.citeck.launcher.core.appdef.ApplicationDef
import ru.citeck.launcher.core.utils.json.Yaml
import ru.citeck.launcher.view.editor.EditorWindow
import ru.citeck.launcher.view.form.exception.FormCancelledException
import kotlin.coroutines.resume
import kotlin.coroutines.resumeWithException

object AppDefEditWindow {

    suspend fun show(appDef: ApplicationDef): EditResponse? {
        return suspendCancellableCoroutine { continuation ->
            EditorWindow.show("app-def.yml", Yaml.toString(appDef)) { ctx ->
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
                        val appDef = Yaml.read(ctx.getText(), ApplicationDef::class)
                        continuation.resume(EditResponse(appDef, true))
                        ctx.closeWindow()
                    } catch (e: Throwable) {
                        ctx.showError(e)
                    }
                }
            }
        }
    }

    class EditResponse(
        val appDef: ApplicationDef,
        val locked: Boolean
    )
}
