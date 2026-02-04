package ru.citeck.launcher.core.namespace.runtime.actions

class RepoUnauthorizedException(val secretVersion: Long) : RuntimeException("Unauthorized error")
