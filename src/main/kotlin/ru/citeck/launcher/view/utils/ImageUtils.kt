package ru.citeck.launcher.view.utils

import org.apache.batik.transcoder.Transcoder
import org.apache.batik.transcoder.TranscoderInput
import org.apache.batik.transcoder.TranscoderOutput
import org.apache.batik.transcoder.image.PNGTranscoder
import ru.citeck.launcher.core.utils.file.CiteckFiles
import java.awt.AlphaComposite
import java.awt.image.BufferedImage
import java.io.ByteArrayInputStream
import java.io.ByteArrayOutputStream
import javax.imageio.ImageIO

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

    fun addTransparentBorderToPng(pngData: ByteArray, borderSize: Int): ByteArray {

        val original = ImageIO.read(ByteArrayInputStream(pngData))

        val newWidth: Int = original.width + borderSize * 2
        val newHeight: Int = original.height + borderSize * 2

        val withBorder = BufferedImage(newWidth, newHeight, BufferedImage.TYPE_INT_ARGB)

        val g2d = withBorder.createGraphics()
        g2d.composite = AlphaComposite.Src
        g2d.drawImage(original, borderSize, borderSize, null)
        g2d.dispose()

        val baos = ByteArrayOutputStream()
        ImageIO.write(withBorder, "png", baos)
        return baos.toByteArray()
    }
}
