plugins {
    java
    application
}

java {
    sourceCompatibility = JavaVersion.VERSION_1_8
    targetCompatibility = JavaVersion.VERSION_1_8
}

repositories {
    mavenCentral()
}

dependencies {
    implementation("com.h2database:h2:2.3.232")
}

application {
    mainClass.set("ru.citeck.launcher.migrate.H2Export")
}

tasks.jar {
    manifest {
        attributes["Main-Class"] = "ru.citeck.launcher.migrate.H2Export"
    }
    // Fat JAR: bundle all dependencies
    from(configurations.runtimeClasspath.get().map { if (it.isDirectory) it else zipTree(it) })
    duplicatesStrategy = DuplicatesStrategy.EXCLUDE
}
