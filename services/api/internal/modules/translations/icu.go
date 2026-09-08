package translations

import (
	"strings"
	"unicode"
)

func icuArguments(message string) (map[string]bool, bool) {
	parser := icuParser{input: []rune(message), arguments: make(map[string]bool)}
	if !parser.pattern(false) || parser.index != len(parser.input) {
		return nil, false
	}
	return parser.arguments, true
}

type icuParser struct {
	input     []rune
	index     int
	depth     int
	arguments map[string]bool
}

func (parser *icuParser) pattern(stopAtBrace bool) bool {
	quoted := false
	for parser.index < len(parser.input) {
		character := parser.input[parser.index]
		if character == '\'' {
			if parser.index+1 < len(parser.input) && parser.input[parser.index+1] == '\'' {
				parser.index += 2
				continue
			}
			quoted = !quoted
			parser.index++
			continue
		}
		if !quoted && character == '{' {
			if !parser.argument() {
				return false
			}
			continue
		}
		if !quoted && character == '}' {
			if !stopAtBrace {
				return false
			}
			parser.index++
			return true
		}
		parser.index++
	}
	return !stopAtBrace && !quoted
}

func (parser *icuParser) argument() bool {
	parser.depth++
	defer func() { parser.depth-- }()
	if parser.depth > 16 {
		return false
	}
	parser.index++
	parser.space()
	name := parser.identifier()
	if name == "" {
		return false
	}
	parser.arguments[name] = true
	parser.space()
	if parser.consume('}') {
		return true
	}
	if !parser.consume(',') {
		return false
	}
	parser.space()
	kind := strings.ToLower(parser.identifier())
	parser.space()
	if kind == "select" || kind == "plural" || kind == "selectordinal" {
		if !parser.consume(',') {
			return false
		}
		parser.space()
		return parser.options(kind != "select")
	}
	if kind != "number" && kind != "date" && kind != "time" {
		return false
	}
	for parser.index < len(parser.input) && parser.input[parser.index] != '}' {
		if parser.input[parser.index] == '{' {
			return false
		}
		parser.index++
	}
	return parser.consume('}')
}

func (parser *icuParser) options(plural bool) bool {
	if plural && strings.HasPrefix(string(parser.input[parser.index:]), "offset:") {
		parser.index += len([]rune("offset:"))
		if parser.number() == "" {
			return false
		}
		parser.space()
	}
	count := 0
	hasOther := false
	for parser.index < len(parser.input) && parser.input[parser.index] != '}' {
		selector := parser.identifier()
		if plural && selector == "" && parser.consume('=') {
			selector = "=" + parser.number()
		}
		if selector == "" {
			return false
		}
		hasOther = hasOther || selector == "other"
		parser.space()
		if !parser.consume('{') {
			return false
		}
		if !parser.pattern(true) {
			return false
		}
		count++
		parser.space()
	}
	return count > 0 && hasOther && parser.consume('}')
}

func (parser *icuParser) identifier() string {
	start := parser.index
	for parser.index < len(parser.input) {
		character := parser.input[parser.index]
		if !(unicode.IsLetter(character) || unicode.IsDigit(character) || character == '_' || character == '-' || character == '.') {
			break
		}
		parser.index++
	}
	if start == parser.index || unicode.IsDigit(parser.input[start]) {
		return ""
	}
	return string(parser.input[start:parser.index])
}

func (parser *icuParser) number() string {
	start := parser.index
	for parser.index < len(parser.input) && (unicode.IsDigit(parser.input[parser.index]) || parser.input[parser.index] == '.') {
		parser.index++
	}
	return string(parser.input[start:parser.index])
}

func (parser *icuParser) space() {
	for parser.index < len(parser.input) && unicode.IsSpace(parser.input[parser.index]) {
		parser.index++
	}
}

func (parser *icuParser) consume(expected rune) bool {
	if parser.index >= len(parser.input) || parser.input[parser.index] != expected {
		return false
	}
	parser.index++
	return true
}
