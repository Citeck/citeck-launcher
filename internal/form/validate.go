package form

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
)

// Validate checks form data against a spec and returns field errors.
func Validate(spec *Spec, data map[string]any) []FieldError {
	var errs []FieldError
	for _, comp := range spec.Components {
		val, exists := data[comp.Key]

		// Required check
		if comp.Required && (!exists || isEmpty(val)) {
			errs = append(errs, FieldError{
				Key:     comp.Key,
				Message: fmt.Sprintf("%s is required", comp.Label),
			})
			continue
		}

		if !exists || val == nil {
			continue
		}

		// Type-specific validation rules
		for _, rule := range comp.Validations {
			if err := validateRule(comp.Key, comp.Label, val, rule); err != nil {
				errs = append(errs, *err)
			}
		}
	}
	return errs
}

func isEmpty(val any) bool {
	if val == nil {
		return true
	}
	switch v := val.(type) {
	case string:
		return strings.TrimSpace(v) == ""
	case bool:
		return false // booleans are never "empty"
	default:
		return false
	}
}

func validateRule(key, label string, val any, rule ValidationRule) *FieldError {
	switch rule.Type {
	case "minLength":
		s, ok := val.(string)
		if !ok {
			return nil
		}
		minLen := toInt(rule.Value)
		if len(s) < minLen {
			return &FieldError{Key: key, Message: ruleMessage(rule, fmt.Sprintf("%s must be at least %d characters", label, minLen))}
		}

	case "maxLength":
		s, ok := val.(string)
		if !ok {
			return nil
		}
		maxLen := toInt(rule.Value)
		if len(s) > maxLen {
			return &FieldError{Key: key, Message: ruleMessage(rule, fmt.Sprintf("%s must be at most %d characters", label, maxLen))}
		}

	case "pattern":
		s, ok := val.(string)
		if !ok {
			return nil
		}
		pattern, ok := rule.Value.(string)
		if !ok {
			return nil
		}
		re, err := compilePattern(pattern)
		if err != nil {
			return nil // invalid pattern in spec — skip
		}
		if !re.MatchString(s) {
			return &FieldError{Key: key, Message: ruleMessage(rule, fmt.Sprintf("%s has invalid format", label))}
		}

	case "min":
		num := toFloat(val)
		minVal := toFloat(rule.Value)
		if num < minVal {
			return &FieldError{Key: key, Message: ruleMessage(rule, fmt.Sprintf("%s must be at least %.0f", label, minVal))}
		}

	case "max":
		num := toFloat(val)
		maxVal := toFloat(rule.Value)
		if num > maxVal {
			return &FieldError{Key: key, Message: ruleMessage(rule, fmt.Sprintf("%s must be at most %.0f", label, maxVal))}
		}
	}
	return nil
}

func ruleMessage(rule ValidationRule, fallback string) string {
	if rule.Message != "" {
		return rule.Message
	}
	return fallback
}

func toInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case float64:
		return int(n)
	case int64:
		return int(n)
	default:
		return 0
	}
}

// patternCache caches compiled regexps to avoid re-compilation on every validation call.
var (
	patternMu    sync.RWMutex
	patternCache = make(map[string]*regexp.Regexp)
)

func compilePattern(pattern string) (*regexp.Regexp, error) {
	patternMu.RLock()
	re, ok := patternCache[pattern]
	patternMu.RUnlock()
	if ok {
		return re, nil
	}

	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("compile pattern %q: %w", pattern, err)
	}

	patternMu.Lock()
	patternCache[pattern] = re
	patternMu.Unlock()
	return re, nil
}

func toFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	default:
		return 0
	}
}
