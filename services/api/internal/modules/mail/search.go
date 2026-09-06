package mail

import (
	"fmt"
	"strings"
	"unicode"
)

var searchOperators = map[string]struct{}{
	"from": {}, "to": {}, "subject": {}, "after": {}, "before": {},
	"has": {}, "is": {}, "label": {}, "in": {},
}

type SearchQuery struct {
	Text      string              `json:"text"`
	Operators map[string][]string `json:"operators"`
}

func ParseSearch(input string) (SearchQuery, error) {
	query := SearchQuery{Operators: make(map[string][]string)}
	var text []string
	for _, token := range tokenizeSearch(input) {
		key, value, found := strings.Cut(token, ":")
		if !found {
			text = append(text, token)
			continue
		}
		key = strings.ToLower(key)
		if _, allowed := searchOperators[key]; !allowed {
			text = append(text, token)
			continue
		}
		value = strings.Trim(value, `"`)
		if value == "" {
			return SearchQuery{}, fmt.Errorf("search operator %q requires a value", key)
		}
		query.Operators[key] = append(query.Operators[key], value)
	}
	query.Text = strings.Join(text, " ")
	return query, nil
}

func tokenizeSearch(input string) []string {
	var tokens []string
	var current strings.Builder
	quoted := false
	for _, character := range input {
		switch {
		case character == '"':
			quoted = !quoted
			current.WriteRune(character)
		case unicode.IsSpace(character) && !quoted:
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(character)
		}
	}
	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}
	return tokens
}
