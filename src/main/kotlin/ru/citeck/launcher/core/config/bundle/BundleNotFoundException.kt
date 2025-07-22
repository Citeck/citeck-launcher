package ru.citeck.launcher.core.config.bundle

class BundleNotFoundException(val ref: BundleRef) :
    RuntimeException(
        "Bundle is not found by key '${ref.key}' in repo ${ref.repo}"
    )
