package ru.citeck.launcher.cli.output

object TableFormatter {

    fun format(headers: List<String>, rows: List<List<String>>): String {
        if (headers.isEmpty()) return ""

        val colWidths = headers.indices.map { col ->
            maxOf(
                headers[col].length,
                rows.maxOfOrNull { row -> row.getOrElse(col) { "" }.length } ?: 0
            )
        }

        val sb = StringBuilder()

        sb.appendLine(
            headers.mapIndexed { i, h -> h.padEnd(colWidths[i]) }.joinToString("  ")
        )

        for (row in rows) {
            sb.appendLine(
                headers.indices.joinToString("  ") { i ->
                    row.getOrElse(i) { "" }.padEnd(colWidths[i])
                }
            )
        }

        return sb.toString().trimEnd()
    }
}
