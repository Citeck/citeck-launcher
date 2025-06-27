package ru.citeck.launcher.core.utils

class NameValidator(
    private val maxLength: Int,
    private val regex: Regex,
    private val allowEmpty: Boolean
) {
    companion object {
        private val DEFAULT = Builder().build()

        fun create(): Builder {
            return Builder()
        }

        fun validate(name: String) {
            DEFAULT.validate(name)
        }
    }

    fun validate(name: String) {
        when {
            !allowEmpty && name.isBlank() -> error("Name cannot be blank")
            name.length > maxLength -> error("Name length must be less than $maxLength characters")
            !name.matches(regex) -> error("Invalid name: '$name'")
        }
    }

    class Builder {

        private var maxLength = 50
        private var regex: Regex = "^(\\w+|\\w[\\w\$/.-]+\\w)\$".toRegex()
        private var allowEmpty: Boolean = false

        fun withMaxlength(maxLength: Int): Builder {
            this.maxLength = maxLength
            return this
        }

        fun withRegex(regex: Regex): Builder {
            this.regex = regex
            return this
        }

        fun withAllowEmpty(allowEmpty: Boolean): Builder {
            this.allowEmpty = allowEmpty
            return this
        }

        fun build(): NameValidator {
            return NameValidator(maxLength, regex, allowEmpty)
        }
    }
}
