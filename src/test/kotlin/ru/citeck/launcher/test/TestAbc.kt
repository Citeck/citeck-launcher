package ru.citeck.launcher.test

import org.junit.jupiter.api.Disabled
import org.junit.jupiter.api.Test
import ru.citeck.launcher.core.utils.data.DataValue
import ru.citeck.launcher.core.utils.file.CiteckFiles
import java.net.URI

@Disabled
class TestAbc {

    @Test
    fun test2() {

        val file = CiteckFiles.getFile("classpath:templates")

        println(file.getChildren().map { it.getName() })
        println(file.getUri())
        //println(file.readBytes().size)
    }

    @Test
    fun test() {


        val uri = URI.create("jdbc:postgresql://localhost:14523/ecos_history")
        println(uri.path)
        println(uri.host)

        val ob = DataValue.createObj()
        ob.set("/aa/bbb/ccc", 123)
        println(ob)
    }
}
