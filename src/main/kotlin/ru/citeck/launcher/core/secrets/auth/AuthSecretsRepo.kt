package ru.citeck.launcher.core.secrets.auth

import kotlinx.coroutines.runBlocking
import ru.citeck.launcher.core.database.Repository

class AuthSecretsRepo(
    private val service: AuthSecretsService
) : Repository<String, AuthSecret> {

    override fun set(id: String, value: AuthSecret) {
        error("Not implemented")
    }

    override fun get(id: String): AuthSecret? {
        return runBlocking {
            service.getSecretsMap()[id]
        }
    }

    override fun delete(id: String) {
        service.deleteSecret(id)
    }

    override fun find(max: Int): List<AuthSecret> {
        return service.getSecrets()
    }

    override fun getFirst(): AuthSecret? {
        return service.getSecrets().firstOrNull()
    }

    override fun forEach(action: (String, AuthSecret) -> Boolean) {
        service.getSecrets().forEach { action.invoke(it.id, it) }
    }
}
