package ru.citeck.launcher.core.license

import ru.citeck.launcher.core.LauncherServices
import ru.citeck.launcher.core.secrets.storage.SecretsStorage
import java.util.concurrent.atomic.AtomicBoolean

class LicenseService {

    companion object {
        private const val LICENSES_LIST_KEY = "licenses"
    }

    private lateinit var secretsStorage: SecretsStorage
    private var licenses = emptyList<LicenseInstance>()
    private val initialized = AtomicBoolean(false)

    fun init(services: LauncherServices) {
        this.secretsStorage = services.secretsStorage
    }

    fun getLicenses(): List<LicenseInstance> {
        if (initialized.compareAndSet(false, true)) {
            licenses = secretsStorage.getList(LICENSES_LIST_KEY, LicenseInstance::class)
        }
        return licenses
    }

    fun hasValidEntLicense(): Boolean {
        return getLicenses().any { it.isValid() }
    }

    fun addLicense(license: LicenseInstance) {
        licenses = listOf(*getLicenses().toTypedArray(), license)
        secretsStorage.put(LICENSES_LIST_KEY, licenses)
    }
}
