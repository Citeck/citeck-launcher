package ru.citeck.launcher.core.namespace.gen

object GlobalLinks {

    val LINKS: List<NamespaceLink> = listOf(
        NamespaceLink(
            url = "https://citeck-ecos.readthedocs.io/",
            name = "Documentation",
            description = "Citeck documentation",
            icon = "icons/app/docs.svg",
            order = 200f,
            category = "Resources",
            alwaysEnabled = true
        ),
        NamespaceLink(
            url = "https://t.me/haski_citeck_bot",
            name = "AI Documentation Bot",
            description = "Telegram bot for AI documentation assistance",
            icon = "icons/app/telegram.svg",
            order = 201f,
            category = "Resources",
            alwaysEnabled = true
        )
    )
}
