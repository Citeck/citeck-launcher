package ru.citeck.launcher.core.utils

import java.util.*

object StringUtils {

    const val EMPTY = ""

    fun getLastNotEmptyCharIdx(str: String): Int {

        if (str.isEmpty()) {
            return -1
        }

        var idx = str.length - 1
        while (idx >= 0) {
            val char = str[idx]
            if (!char.isWhitespace()) {
                break
            }
            idx--
        }
        return idx
    }

    fun getNextNotEmptyCharIdx(str: String, startIdx: Int): Int {
        if (startIdx >= str.length) {
            return -1
        }
        var idx = startIdx
        var char = str[idx++]
        while (char.isWhitespace() && idx < str.length) {
            char = str[idx++]
        }
        return if (idx > str.length) {
            -1
        } else {
            idx - 1
        }
    }

    @JvmStatic
    fun defaultString(str: String?): String {
        return defaultString(str, EMPTY)
    }

    @JvmStatic
    fun defaultString(str: String?, defaultStr: String): String {
        return str ?: defaultStr
    }

    /**
     *
     * Checks if a CharSequence is whitespace, empty ("") or null.
     *
     * <pre>
     * StringUtils.isBlank(null)      = true
     * StringUtils.isBlank("")        = true
     * StringUtils.isBlank(" ")       = true
     * StringUtils.isBlank("bob")     = false
     * StringUtils.isBlank("  bob  ") = false
     </pre> *
     *
     * @param cs  the CharSequence to check, may be null
     * @return `true` if the CharSequence is null, empty or whitespace
     * @since 2.0
     * @since 3.0 Changed signature from isBlank(String) to isBlank(CharSequence)
     */
    @JvmStatic
    fun isBlank(cs: CharSequence?): Boolean {
        return cs?.isBlank() ?: true
    }

    /**
     *
     * Checks if a CharSequence is not empty (""), not null and not whitespace only.
     *
     * <pre>
     * StringUtils.isNotBlank(null)      = false
     * StringUtils.isNotBlank("")        = false
     * StringUtils.isNotBlank(" ")       = false
     * StringUtils.isNotBlank("bob")     = true
     * StringUtils.isNotBlank("  bob  ") = true
     </pre> *
     *
     * @param cs  the CharSequence to check, may be null
     * @return `true` if the CharSequence is not empty and not null and not whitespace
     * @since 2.0
     * @since 3.0 Changed signature from isNotBlank(String) to isNotBlank(CharSequence)
     */
    @JvmStatic
    fun isNotBlank(cs: CharSequence?): Boolean {
        return !isBlank(cs)
    }

    // Empty checks
    // -----------------------------------------------------------------------
    /**
     *
     * Checks if a CharSequence is empty ("") or null.
     *
     * <pre>
     * StringUtils.isEmpty(null)      = true
     * StringUtils.isEmpty("")        = true
     * StringUtils.isEmpty(" ")       = false
     * StringUtils.isEmpty("bob")     = false
     * StringUtils.isEmpty("  bob  ") = false
     </pre> *
     *
     *
     * NOTE: This method changed in Lang version 2.0.
     * It no longer trims the CharSequence.
     * That functionality is available in isBlank().
     *
     * @param cs  the CharSequence to check, may be null
     * @return `true` if the CharSequence is empty or null
     * @since 3.0 Changed signature from isEmpty(String) to isEmpty(CharSequence)
     */
    @JvmStatic
    fun isEmpty(cs: CharSequence?): Boolean {
        return cs == null || cs.isEmpty()
    }

    /**
     * Checks if a CharSequence contains only specified character.
     */
    @JvmStatic
    fun containsOnly(cs: CharSequence, character: Char): Boolean {
        if (cs.isEmpty()) {
            return false
        }
        for (element in cs) {
            if (element != character) {
                return false
            }
        }
        return true
    }

    // Count matches
    // -----------------------------------------------------------------------
    /**
     *
     * Counts how many times the substring appears in the larger string.
     *
     *
     * A `null` or empty ("") String input returns `0`.
     *
     * <pre>
     * StringUtils.countMatches(null, *)       = 0
     * StringUtils.countMatches("", *)         = 0
     * StringUtils.countMatches("abba", null)  = 0
     * StringUtils.countMatches("abba", "")    = 0
     * StringUtils.countMatches("abba", "a")   = 2
     * StringUtils.countMatches("abba", "ab")  = 1
     * StringUtils.countMatches("abba", "xxx") = 0
     </pre> *
     *
     * @param str  the CharSequence to check, may be null
     * @param sub  the substring to count, may be null
     * @return the number of occurrences, 0 if either CharSequence is `null`
     * @since 3.0 Changed signature from countMatches(String, String) to countMatches(CharSequence, CharSequence)
     */
    @JvmStatic
    fun countMatches(str: StringBuilder, sub: String): Int {
        if (isEmpty(str) || isEmpty(sub)) {
            return 0
        }
        var count = 0
        var idx = 0
        while (str.indexOf(sub, idx).also { idx = it } != -1) {
            count++
            idx += sub.length
        }
        return count
    }

    @JvmStatic
    fun escapeDoubleQuotes(name: String): String {
        var quoteIdx = name.indexOf('"')
        if (quoteIdx == -1) {
            return name
        }
        val sb = StringBuilder()
        var beforeIdx = 0
        while (quoteIdx >= 0) {
            sb.append(name, beforeIdx, quoteIdx)
            if (quoteIdx == 0 || name[quoteIdx - 1] != '\\') {
                sb.append('\\')
            }
            beforeIdx = quoteIdx
            quoteIdx = name.indexOf('"', quoteIdx + 1)
        }
        sb.append(name, beforeIdx, name.length)
        return sb.toString()
    }

    /**
     * Convert a comma delimited list (e.g., a row from a CSV file) into an
     * array of strings.
     * @param str the input `String` (potentially `null` or empty)
     * @return an array of strings, or the empty array in case of empty input
     */
    @JvmStatic
    fun commaDelimitedListToStringArray(str: String?): Array<String> {
        return delimitedListToStringArray(str, ",")
    }

    /**
     * Take a `String` that is a delimited list and convert it into a
     * `String` array.
     *
     * A single `delimiter` may consist of more than one character,
     * but it will still be considered as a single delimiter string, rather
     * than as bunch of potential delimiter characters, in contrast to
     * [.tokenizeToStringArray].
     * @param str the input `String` (potentially `null` or empty)
     * @param delimiter the delimiter between elements (this is a single delimiter,
     * rather than a bunch individual delimiter characters)
     * @return an array of the tokens in the list
     * @see .tokenizeToStringArray
     */
    @JvmStatic
    fun delimitedListToStringArray(str: String?, delimiter: String): Array<String> {
        return delimitedListToStringArray(str, delimiter, null)
    }

    /**
     * Take a `String` that is a delimited list and convert it into
     * a `String` array.
     *
     * A single `delimiter` may consist of more than one character,
     * but it will still be considered as a single delimiter string, rather
     * than as bunch of potential delimiter characters, in contrast to
     * [.tokenizeToStringArray].
     * @param str the input `String` (potentially `null` or empty)
     * @param delimiter the delimiter between elements (this is a single delimiter,
     * rather than a bunch individual delimiter characters)
     * @param charsToDelete a set of characters to delete; useful for deleting unwanted
     * line breaks: e.g. "\r\n\f" will delete all new lines and line feeds in a `String`
     * @return an array of the tokens in the list
     * @see .tokenizeToStringArray
     */
    @JvmStatic
    fun delimitedListToStringArray(
        str: String?,
        delimiter: String?,
        charsToDelete: String?
    ): Array<String> {
        if (str == null) {
            return emptyArray()
        }
        if (delimiter == null) {
            return arrayOf(str)
        }
        val result: MutableList<String> = ArrayList()
        if ("" == delimiter) {
            for (i in str.indices) {
                result.add(deleteAny(str.substring(i, i + 1), charsToDelete))
            }
        } else {
            var pos = 0
            var delPos: Int
            while (str.indexOf(delimiter, pos).also { delPos = it } != -1) {
                result.add(deleteAny(str.substring(pos, delPos), charsToDelete))
                pos = delPos + delimiter.length
            }
            if (str.isNotEmpty() && pos <= str.length) {
                // Add rest of String, but not in case of empty input.
                result.add(deleteAny(str.substring(pos), charsToDelete))
            }
        }
        return result.toTypedArray()
    }

    /**
     * Delete any character in a given `String`.
     * @param inString the original `String`
     * @param charsToDelete a set of characters to delete.
     * E.g. "az\n" will delete 'a's, 'z's and new lines.
     * @return the resulting `String`
     */
    @JvmStatic
    fun deleteAny(inString: String, charsToDelete: String?): String {
        if (charsToDelete == null || !hasLength(inString) || !hasLength(charsToDelete)) {
            return inString
        }
        val sb = StringBuilder(inString.length)
        for (element in inString) {
            if (charsToDelete.indexOf(element) == -1) {
                sb.append(element)
            }
        }
        return sb.toString()
    }

    /**
     * Check that the given `String` is neither `null` nor of length 0.
     *
     * Note: this method returns `true` for a `String` that
     * purely consists of whitespace.
     * @param str the `String` to check (may be `null`)
     * @return `true` if the `String` is not `null` and has length
     * @see .hasLength
     * @see .hasText
     */
    @JvmStatic
    fun hasLength(str: String?): Boolean {
        return str != null && str.isNotEmpty()
    }

    /**
     * Tokenize the given `String` into a `String` array via a
     * [StringTokenizer].
     *
     * Trims tokens and omits empty tokens.
     *
     * The given `delimiters` string can consist of any number of
     * delimiter characters. Each of those characters can be used to separate
     * tokens. A delimiter is always a single character; for multi-character
     * delimiters, consider using [.delimitedListToStringArray].
     * @param str the `String` to tokenize (potentially `null` or empty)
     * @param delimiters the delimiter characters, assembled as a `String`
     * (each of the characters is individually considered as a delimiter)
     * @return an array of the tokens
     * @see java.util.StringTokenizer
     *
     * @see String.trim
     * @see .delimitedListToStringArray
     */
    @JvmStatic
    fun tokenizeToStringArray(str: String?, delimiters: String): Array<String> {
        return tokenizeToStringArray(str, delimiters, trimTokens = true, ignoreEmptyTokens = true)
    }

    /**
     * Tokenize the given `String` into a `String` array via a
     * [StringTokenizer].
     *
     * The given `delimiters` string can consist of any number of
     * delimiter characters. Each of those characters can be used to separate
     * tokens. A delimiter is always a single character; for multi-character
     * delimiters, consider using [.delimitedListToStringArray].
     * @param str the `String` to tokenize (potentially `null` or empty)
     * @param delimiters the delimiter characters, assembled as a `String`
     * (each of the characters is individually considered as a delimiter)
     * @param trimTokens trim the tokens via [String.trim]
     * @param ignoreEmptyTokens omit empty tokens from the result array
     * (only applies to tokens that are empty after trimming; StringTokenizer
     * will not consider subsequent delimiters as token in the first place).
     * @return an array of the tokens
     * @see java.util.StringTokenizer
     *
     * @see String.trim
     * @see .delimitedListToStringArray
     */
    @JvmStatic
    fun tokenizeToStringArray(
        str: String?,
        delimiters: String,
        trimTokens: Boolean,
        ignoreEmptyTokens: Boolean
    ): Array<String> {
        if (str == null) {
            return emptyArray()
        }
        val st = StringTokenizer(str, delimiters)
        val tokens: MutableList<String> = ArrayList()
        while (st.hasMoreTokens()) {
            var token = st.nextToken()
            if (trimTokens) {
                token = token.trim { it <= ' ' }
            }
            if (!ignoreEmptyTokens || token.isNotEmpty()) {
                tokens.add(token)
            }
        }
        return tokens.toTypedArray()
    }
}
