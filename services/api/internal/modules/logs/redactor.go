package logs

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

const redacted = "[REDACTED]"

var (
	forbiddenFragments = []string{
		"authorization", "cookie", "credential", "password", "secret", "token",
		"subject", "body", "recipient", "sender", "email", "signed_url", "signedurl",
		"account_id", "userid", "user_id", "message_id", "thread_id", "draft_id",
	}
	emailPattern = regexp.MustCompile(`(?i)[a-z0-9.!#$%&'*+/=?^_` + "`" + `{|}~-]+@[a-z0-9-]+(?:\.[a-z0-9-]+)+`)
	jwtPattern   = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}(?:\.[A-Za-z0-9_-]{8,})?\b`)
	uuidPattern  = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}\b`)
)

func Redact(fields map[string]any) map[string]any {
	return redactMap(fields, 0)
}

func redactMap(fields map[string]any, depth int) map[string]any {
	clean := make(map[string]any, len(fields))
	for key, value := range fields {
		if forbiddenKey(key) {
			clean[key] = redacted
			continue
		}
		clean[key] = redactValue(value, depth+1)
	}
	return clean
}

func forbiddenKey(key string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "-", "_"), ".", "_"))
	for _, fragment := range forbiddenFragments {
		if strings.Contains(normalized, fragment) {
			return true
		}
	}
	return false
}

func redactValue(value any, depth int) any {
	if depth > 6 {
		return redacted
	}
	switch typed := value.(type) {
	case map[string]any:
		return redactMap(typed, depth)
	case []any:
		if len(typed) > 32 {
			return redacted
		}
		result := make([]any, len(typed))
		for index, item := range typed {
			result[index] = redactValue(item, depth+1)
		}
		return result
	case error:
		return redactString(typed.Error())
	case string:
		return redactString(typed)
	case fmt.Stringer:
		return redactString(typed.String())
	case nil, bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		return typed
	default:
		return redacted
	}
}

func redactString(value string) string {
	if len(value) > 2048 || emailPattern.MatchString(value) || jwtPattern.MatchString(value) || uuidPattern.MatchString(value) || strings.Contains(strings.ToLower(value), "bearer ") {
		return redacted
	}
	if parsed, err := url.Parse(value); err == nil && (parsed.User != nil || (parsed.RawQuery != "" && (parsed.IsAbs() || strings.HasPrefix(value, "/")))) {
		return redacted
	}
	return value
}
