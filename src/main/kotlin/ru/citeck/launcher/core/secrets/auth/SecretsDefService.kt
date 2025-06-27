package ru.citeck.launcher.core.secrets.auth

import ru.citeck.launcher.core.WorkspaceServices
import ru.citeck.launcher.core.config.AppDir
import ru.citeck.launcher.core.utils.json.Json
import java.util.concurrent.ConcurrentHashMap

class SecretsDefService {

    private val definitionsFile = AppDir.PATH.resolve("secrets/definitions.json")

    private val secrets: MutableMap<Long, SecretDef> = ConcurrentHashMap()

    fun init(services: WorkspaceServices) {
/*        services.entitiesService.register(EntityDef(
            SecretDef::class,
            "secret-def",
            { it.id },

        ))*/

       /* loadSecretsDefs().secrets.forEach {
            secrets[it.id] = it
        }*/
    }

    private fun loadSecretsDefs(): Storage {
        val defsFile = definitionsFile.toFile()
        if (!defsFile.exists()) {
            return Storage()
        }
        return Json.read(defsFile, Storage::class)
    }
/*
    fun addSecret(secretDef: SecretDef) {
        this.secrets[secretDef.id] = secretDef
    }*/

    private class Storage(
        val secrets: List<SecretDef> = emptyList()
    )
}
