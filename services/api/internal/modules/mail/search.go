package mail

import (
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const maxSearchLength = 4096

var searchOperators = map[string]struct{}{
	"from": {}, "to": {}, "subject": {}, "after": {}, "before": {},
	"has": {}, "is": {}, "label": {}, "in": {},
}

type SearchValidationError struct {
	Code string
}

func (err *SearchValidationError) Error() string {
	return fmt.Sprintf("invalid search query (%s)", err.Code)
}

type SearchQuery struct {
	Text          string     `json:"text"`
	From          []string   `json:"from"`
	To            []string   `json:"to"`
	Subjects      []string   `json:"subjects"`
	After         *time.Time `json:"after"`
	Before        *time.Time `json:"before"`
	HasAttachment bool       `json:"hasAttachment"`
	Unread        bool       `json:"unread"`
	Starred       bool       `json:"starred"`
	Labels        []string   `json:"labels"`
	Mailboxes     []string   `json:"mailboxes"`
}

func ParseSearch(input string) (SearchQuery, error) {
	if !utf8.ValidString(input) || len(input) > maxSearchLength {
		return SearchQuery{}, &SearchValidationError{Code: "query_too_long"}
	}
	tokens, err := tokenizeSearch(input)
	if err != nil {
		return SearchQuery{}, err
	}
	query := SearchQuery{}
	var text []string
	for _, token := range tokens {
		key, value, found := strings.Cut(token, ":")
		if !found {
			text = append(text, strings.Trim(token, `"`))
			continue
		}
		key = strings.ToLower(key)
		if _, allowed := searchOperators[key]; !allowed {
			return SearchQuery{}, &SearchValidationError{Code: "unsupported_operator"}
		}
		value = strings.TrimSpace(strings.Trim(value, `"`))
		if value == "" {
			return SearchQuery{}, &SearchValidationError{Code: "missing_value"}
		}
		if len(value) > 1024 {
			return SearchQuery{}, &SearchValidationError{Code: "value_too_long"}
		}
		if err := applySearchOperator(&query, key, value); err != nil {
			return SearchQuery{}, err
		}
	}
	query.Text = strings.TrimSpace(strings.Join(text, " "))
	if query.Text == "" && len(query.From) == 0 && len(query.To) == 0 && len(query.Subjects) == 0 && query.After == nil && query.Before == nil && !query.HasAttachment && !query.Unread && !query.Starred && len(query.Labels) == 0 && len(query.Mailboxes) == 0 {
		return SearchQuery{}, &SearchValidationError{Code: "empty_query"}
	}
	if query.After != nil && query.Before != nil && !query.After.Before(*query.Before) {
		return SearchQuery{}, &SearchValidationError{Code: "invalid_range"}
	}
	return query, nil
}

func applySearchOperator(query *SearchQuery, key, value string) error {
	switch key {
	case "from":
		query.From = append(query.From, value)
	case "to":
		query.To = append(query.To, value)
	case "subject":
		query.Subjects = append(query.Subjects, value)
	case "label":
		query.Labels = append(query.Labels, value)
	case "in":
		query.Mailboxes = append(query.Mailboxes, value)
	case "after", "before":
		parsed, err := time.Parse("2006-01-02", value)
		if err != nil {
			return &SearchValidationError{Code: "invalid_value"}
		}
		if key == "after" {
			if query.After != nil {
				return &SearchValidationError{Code: "duplicate_operator"}
			}
			query.After = &parsed
		} else {
			if query.Before != nil {
				return &SearchValidationError{Code: "duplicate_operator"}
			}
			query.Before = &parsed
		}
	case "has":
		if value != "attachment" || query.HasAttachment {
			return &SearchValidationError{Code: "invalid_value"}
		}
		query.HasAttachment = true
	case "is":
		switch value {
		case "unread":
			if query.Unread {
				return &SearchValidationError{Code: "duplicate_operator"}
			}
			query.Unread = true
		case "starred":
			if query.Starred {
				return &SearchValidationError{Code: "duplicate_operator"}
			}
			query.Starred = true
		default:
			return &SearchValidationError{Code: "invalid_value"}
		}
	}
	return nil
}

func tokenizeSearch(input string) ([]string, error) {
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
	if quoted {
		return nil, &SearchValidationError{Code: "unclosed_quote"}
	}
	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}
	return tokens, nil
}

type SearchCursor struct {
	Rank   float32   `json:"rank"`
	SentAt time.Time `json:"sentAt"`
	ID     string    `json:"id"`
}

type SearchHit struct {
	Message Message `json:"message"`
	Rank    float32 `json:"rank"`
}

type SearchPage struct {
	Items []SearchHit   `json:"items"`
	Next  *SearchCursor `json:"next"`
}
