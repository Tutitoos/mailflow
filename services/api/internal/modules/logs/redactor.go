package logs

import "strings"

var forbiddenKeys = map[string]struct{}{
	"authorization": {}, "cookie": {}, "set-cookie": {}, "token": {}, "access_token": {},
	"refresh_token": {}, "subject": {}, "body": {}, "recipient": {}, "recipients": {},
}

func Redact(fields map[string]any) map[string]any {
	clean := make(map[string]any, len(fields))
	for key, value := range fields {
		if _, forbidden := forbiddenKeys[strings.ToLower(key)]; forbidden {
			clean[key] = "[REDACTED]"
			continue
		}
		clean[key] = value
	}
	return clean
}
