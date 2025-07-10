package ru.citeck.launcher.core.namespace

import ru.citeck.launcher.core.entity.EntityRef
import ru.citeck.launcher.view.form.spec.ComponentSpec.NameField
import ru.citeck.launcher.view.form.spec.ComponentSpec.SelectField
import ru.citeck.launcher.view.form.spec.FormSpec

object NamespaceEntityDef {

    const val TYPE_ID = "namespace"

    const val FORM_FIELD_BUNDLES_REPO = "bundlesRepo"
    const val FORM_FIELD_BUNDLE_KEY = "bundleKey"
    const val FORM_FIELD_SNAPSHOT = "snapshot"
    const val FORM_FIELD_TEMPLATE = "template"

    val formSpec = FormSpec(
        "Namespace",
        components = listOf(
            NameField(),
            SelectField(
                FORM_FIELD_BUNDLES_REPO,
                "Bundles Repo",
                "",
                options = { ctx ->
                    ctx.workspaceServices
                        ?.workspaceConfig
                        ?.value
                        ?.bundleRepos
                        ?.map { SelectField.Option(it.id, it.name) } ?: emptyList()
                }
            ).mandatory(),
            SelectField(
                FORM_FIELD_BUNDLE_KEY,
                "Bundle",
                "",
                dependsOn = setOf(FORM_FIELD_BUNDLES_REPO),
                options = { ctx ->
                    ctx.workspaceServices
                        ?.bundlesService
                        ?.getRepoBundles(ctx.getStrValue(FORM_FIELD_BUNDLES_REPO))
                        ?.map { SelectField.Option(it.key, it.key) } ?: emptyList()
                }
            ).mandatory(),
            SelectField(
                FORM_FIELD_SNAPSHOT,
                "Snapshot",
                "",
                options = { ctx ->
                    ctx.workspaceServices?.workspaceConfig?.value?.snapshots?.map {
                        SelectField.Option(it.id, it.name)
                    } ?: emptyList()
                }
            )
        )
    )

    fun getRef(entity: NamespaceDto): EntityRef {
        return EntityRef.create(TYPE_ID, entity.id)
    }
}
