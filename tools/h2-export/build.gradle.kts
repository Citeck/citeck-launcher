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
    // Fat JAR: bundle only MVStore + required H2 packages (not full SQL engine)
    from(configurations.runtimeClasspath.get().map { if (it.isDirectory) it else zipTree(it) }) {
        include("org/h2/mvstore/**")
        include("org/h2/compress/**")
        include("org/h2/util/**")
        include("org/h2/security/**")
        include("org/h2/api/**")
        include("org/h2/message/**")
        include("org/h2/value/VersionedValue.class")
        include("org/h2/value/VersionedValue\$*")
        include("org/h2/engine/**")
        include("org/h2/store/fs/**")
    }
    duplicatesStrategy = DuplicatesStrategy.EXCLUDE
}
