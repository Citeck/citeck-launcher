package ru.citeck.launcher.view.utils

import org.apache.batik.transcoder.Transcoder
import org.apache.batik.transcoder.TranscoderInput
import org.apache.batik.transcoder.TranscoderOutput
import org.apache.batik.transcoder.image.PNGTranscoder
import ru.citeck.launcher.core.utils.file.CiteckFiles
import java.io.ByteArrayOutputStream

object ImageUtils {

    fun cpLoad(path: String): ByteArray {
        return load("classpath:$path")
    }

    fun load(path: String): ByteArray {
        return CiteckFiles.getFile(path).read { inputBytes ->
            inputBytes.readBytes()
        }
    }

    fun loadPng(path: String, size: Float): ByteArray {

        return CiteckFiles.getFile(path).read { inputBytes ->

            if (path.endsWith(".svg")) {

                val transcoder: Transcoder = PNGTranscoder()

                transcoder.addTranscodingHint(PNGTranscoder.KEY_WIDTH, size)
                transcoder.addTranscodingHint(PNGTranscoder.KEY_HEIGHT, size)

                val input = TranscoderInput(inputBytes)
                val outputBytes = ByteArrayOutputStream()
                val output = TranscoderOutput(outputBytes)

                transcoder.transcode(input, output)
                outputBytes.toByteArray()
            } else {
                inputBytes.readBytes()
            }
        }
    }

/*    private fun convertSvgToPng(svgFilePath: String?, pngFilePath: String?) {

        FileInputStream(svgFilePath).use { inputStream ->
            FileOutputStream(pngFilePath).use { outputStream ->


            }
        }
    }*/
}
