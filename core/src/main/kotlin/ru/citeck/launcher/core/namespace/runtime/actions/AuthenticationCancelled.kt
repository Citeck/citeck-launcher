package ru.citeck.launcher.core.namespace.runtime.actions

import ru.citeck.launcher.core.secrets.auth.SecretDef

class AuthenticationCancelled(
    secretDef: SecretDef,
    requiredFor: String
) : RuntimeException("Authentication was cancelled. Secret: $secretDef required for $requiredFor")
