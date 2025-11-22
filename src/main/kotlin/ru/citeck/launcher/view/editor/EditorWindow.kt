package ru.citeck.launcher.view.editor

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Text
import androidx.compose.material3.VerticalDivider
import androidx.compose.runtime.Composable
import androidx.compose.runtime.Stable
import androidx.compose.runtime.mutableStateOf
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.awt.SwingPanel
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.TextRange
import androidx.compose.ui.text.input.TextFieldValue
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.WindowPosition
import androidx.compose.ui.window.rememberWindowState
import org.fife.ui.rsyntaxtextarea.RSyntaxTextArea
import org.fife.ui.rsyntaxtextarea.SyntaxConstants
import org.fife.ui.rsyntaxtextarea.SyntaxScheme
import org.fife.ui.rsyntaxtextarea.Theme
import org.fife.ui.rtextarea.RTextScrollPane
import org.fife.ui.rtextarea.SearchContext
import org.fife.ui.rtextarea.SearchEngine
import ru.citeck.launcher.core.utils.file.CiteckFiles
import ru.citeck.launcher.view.commons.dialog.ErrorDialog
import ru.citeck.launcher.view.popup.CiteckWindow
import ru.citeck.launcher.view.utils.onEnterClick
import java.awt.*
import java.awt.event.ActionEvent
import java.awt.event.ComponentAdapter
import java.awt.event.ComponentEvent
import javax.swing.*
import javax.swing.plaf.basic.BasicScrollBarUI

class EditorWindow private constructor(
    private val filename: String,
    private val initialText: String,
    private val buttonsRowImpl: @Composable ButtonsRowContext.(EditorContext) -> Unit
) : CiteckWindow() {

    companion object {
        private val syntaxByExtension = mapOf(
            "yml" to SyntaxConstants.SYNTAX_STYLE_YAML,
            "yaml" to SyntaxConstants.SYNTAX_STYLE_YAML,
            "kt" to SyntaxConstants.SYNTAX_STYLE_KOTLIN,
            "java" to SyntaxConstants.SYNTAX_STYLE_JAVA,
            "js" to SyntaxConstants.SYNTAX_STYLE_JAVASCRIPT,
            "json" to SyntaxConstants.SYNTAX_STYLE_JSON
        )

        private val theme by lazy {
            val theme = CiteckFiles.getFile("classpath:org/fife/ui/rsyntaxtextarea/themes/vs.xml").read {
                Theme.load(it)
            }
            theme.markAllHighlightColor = java.awt.Color(100, 100, 200, 50)
            theme
        }
        private val font by lazy {
            CiteckFiles.getFile("classpath:fonts/jetbrains/JetBrainsMono-Regular.ttf").read {
                Font.createFont(Font.TRUETYPE_FONT, it)
            }
        }

        fun show(filename: String, text: String, buttonsRow: @Composable ButtonsRowContext.(EditorContext) -> Unit) {
            showWindow(EditorWindow(filename, text, buttonsRow))
        }
    }

    private val searchText = mutableStateOf(TextFieldValue(""))
    private val gutterSize = mutableStateOf(20.dp)
    private val searchFocusRequester = FocusRequester()

    private val scrollPane: RTextScrollPane by lazy {

        val textArea = RSyntaxTextArea(initialText)

        textArea.syntaxEditingStyle = syntaxByExtension[filename.substringAfterLast(".")]
            ?: SyntaxConstants.SYNTAX_STYLE_NONE
        textArea.isCodeFoldingEnabled = true
        textArea.antiAliasingEnabled = true
        textArea.tabSize = 2

/*        textArea.addKeyListener(object : KeyAdapter() {
            override fun keyTyped(e: KeyEvent) {
                if (e.keyCode = )
            }
        })*/
        // textArea.redoLastAction()

        fun setFont(newFont: Font?, size: Float) {
            var ss = textArea.syntaxScheme
            ss = (ss as SyntaxScheme).clone() as SyntaxScheme
            for (idx in 0 until ss.styleCount) {
                if (ss.getStyle(idx) != null) {
                    val font = ss.getStyle(idx).font
                    if (font != null) {
                        if (newFont != null) {
                            ss.getStyle(idx).font = newFont.deriveFont(font.style, size)
                        } else {
                            ss.getStyle(idx).font = font.deriveFont(size)
                        }
                    }
                }
            }
            textArea.setSyntaxScheme(ss)
            if (newFont != null) {
                textArea.font = newFont.deriveFont(textArea.font.style, size)
            } else {
                textArea.font = textArea.font.deriveFont(size)
            }
        }
        val scrollPane = RTextScrollPane(textArea)
        scrollPane.viewportBorder = BorderFactory.createEmptyBorder()

        theme.apply(textArea)
        setFont(font, 14f)

        val lineNumFont = scrollPane.gutter.lineNumberFont
        scrollPane.gutter.lineNumberFont = font.deriveFont(lineNumFont.style, lineNumFont.size.toFloat())
        scrollPane.verticalScrollBar.ui = RoundedScrollBarUI(theme)
        scrollPane.horizontalScrollBar.ui = RoundedScrollBarUI(theme)

        scrollPane.gutter.addComponentListener(object : ComponentAdapter() {
            override fun componentResized(e: ComponentEvent) {
                gutterSize.value = e.component.width.dp
            }
        })

        val inputMap: InputMap = scrollPane.getInputMap(JComponent.WHEN_IN_FOCUSED_WINDOW)
        val actionMap: ActionMap = scrollPane.actionMap

        inputMap.put(KeyStroke.getKeyStroke("control F"), "ctrlF")
        inputMap.put(KeyStroke.getKeyStroke("control shift Z"), "ctrlShiftZ")

        actionMap.put(
            "ctrlF",
            object : AbstractAction() {
                override fun actionPerformed(e: ActionEvent) {
                    searchText.value = searchText.value.copy(
                        selection = TextRange(0, searchText.value.text.length)
                    )
                    searchFocusRequester.requestFocus()
                }
            }
        )
        actionMap.put(
            "ctrlShiftZ",
            object : AbstractAction() {
                override fun actionPerformed(e: ActionEvent) {
                    if (textArea.canRedo()) {
                        textArea.redoLastAction()
                    }
                }
            }
        )

        textArea.caretPosition = 0
        textArea.requestFocus()

        textArea.discardAllEdits()
        scrollPane
    }

    private val editorContext = EditorContext()

    private fun search(next: Boolean) {
        val context = SearchContext(searchText.value.text, false)
        context.searchForward = next
        context.markAll = true
        context.searchWrap = true

        SearchEngine.find(scrollPane.textArea, context)
    }

    @Composable
    override fun render() {
        window(
            rememberWindowState(
                width = 1200.dp.coerceAtMost(screenSize.width * 0.9f),
                height = 1000.dp.coerceAtMost(screenSize.height * 0.9f),
                position = WindowPosition(Alignment.Center)
            )
        ) {
            Row(modifier = Modifier.height(25.dp)) {

                Spacer(modifier = Modifier.width(gutterSize.value - 1.dp))
                VerticalDivider()

                BasicTextField(
                    value = searchText.value,
                    onValueChange = {
                        searchText.value = it
                    },
                    maxLines = 1,
                    modifier = Modifier.padding(horizontal = 10.dp)
                        .align(Alignment.CenterVertically)
                        .focusRequester(searchFocusRequester)
                        .requiredWidthIn(min = 300.dp).onEnterClick {
                            search(true)
                        }
                )
                VerticalDivider()

                searchButton("Next") { search(true) }
                searchButton("Prev") { search(false) }
            }
            HorizontalDivider()
            if (dialogs.isEmpty()) {
                SwingPanel(
                    background = Color.White,
                    modifier = Modifier.weight(1f).fillMaxWidth(),
                    factory = {
                        JPanel().apply {
                            layout = BoxLayout(this, BoxLayout.Y_AXIS)
                            add(scrollPane)
                        }
                    }
                )
            } else {
                Box(modifier = Modifier.weight(1f).fillMaxWidth()) {}
            }
            HorizontalDivider()
            buttonsRow { buttonsRowImpl(editorContext) }
        }
    }

    @Composable
    private fun searchButton(text: String, onClick: () -> Unit) {
        Row(
            modifier = Modifier.fillMaxHeight().clickable {
                onClick()
            }
        ) {
            Text(
                text,
                modifier = Modifier.padding(horizontal = 5.dp)
                    .align(Alignment.CenterVertically)
            )
            VerticalDivider()
        }
    }

    private class RoundedScrollBarUI(theme: Theme) : BasicScrollBarUI() {

        companion object {
            private const val ARC = 10
        }

        private val thumbCustomColor = theme.gutterBorderColor.let {
            java.awt.Color(
                it.red.toFloat() / 255,
                it.green.toFloat() / 255,
                it.blue.toFloat() / 255,
                0.3f
            )
        }

        protected override fun paintTrack(g: Graphics, c: JComponent?, trackBounds: Rectangle) {
        }

        protected override fun paintThumb(g: Graphics, c: JComponent?, r: Rectangle) {
            if (!scrollbar.isEnabled || r.width > r.height && scrollbar.getOrientation() == JScrollBar.VERTICAL) {
                return
            }
            val g2 = g.create() as Graphics2D
            g2.setRenderingHint(RenderingHints.KEY_ANTIALIASING, RenderingHints.VALUE_ANTIALIAS_ON)
            g2.color = thumbCustomColor
            g2.fillRoundRect(r.x, r.y, r.width, r.height, ARC, ARC)
            g2.dispose()
        }

        override fun createDecreaseButton(orientation: Int): JButton {
            return createZeroButton()
        }

        override fun createIncreaseButton(orientation: Int): JButton {
            return createZeroButton()
        }

        private fun createZeroButton(): JButton {
            val btn = JButton()
            btn.preferredSize = Dimension(0, 0)
            btn.minimumSize = Dimension(0, 0)
            btn.maximumSize = Dimension(0, 0)
            btn.setBorder(null)
            btn.setFocusable(false)
            return btn
        }

        override fun getPreferredSize(c: JComponent): Dimension {
            return if (scrollbar.getOrientation() == JScrollBar.VERTICAL) {
                Dimension(8, super.getPreferredSize(c).height)
            } else {
                Dimension(super.getPreferredSize(c).width, 8)
            }
        }
    }

    @Stable
    inner class EditorContext {

        fun closeWindow() {
            this@EditorWindow.closeWindow()
        }

        fun getText(): String {
            return scrollPane.textArea.text
        }

        fun setText(text: String) {
            scrollPane.textArea.text = text
        }

        fun showError(e: Throwable) {
            showDialog(ErrorDialog(ErrorDialog.prepareParams(e)))
        }
    }
}
