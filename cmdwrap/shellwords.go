package cmdwrap

import (
	"fmt"
	"strings"
	"unicode"
)

func splitShellFields(input string) ([]string, error) {
	var fields []string
	var current strings.Builder
	var quote rune
	fieldStarted := false

	runes := []rune(input)
	for i := 0; i < len(runes); i++ {
		ch := runes[i]

		if quote == '\'' {
			if ch == '\'' {
				quote = 0
			} else {
				current.WriteRune(ch)
			}
			continue
		}

		if quote == '"' {
			switch ch {
			case '"':
				quote = 0
			case '\\':
				if i+1 < len(runes) && canEscapeInDoubleQuotes(runes[i+1]) {
					i++
					current.WriteRune(runes[i])
				} else {
					current.WriteRune(ch)
				}
			default:
				current.WriteRune(ch)
			}
			continue
		}

		switch {
		case unicode.IsSpace(ch):
			if fieldStarted {
				fields = append(fields, current.String())
				current.Reset()
				fieldStarted = false
			}
		case ch == '\'' || ch == '"':
			quote = ch
			fieldStarted = true
		case ch == '\\':
			fieldStarted = true
			if i+1 < len(runes) && canEscapeOutsideQuotes(runes[i+1]) {
				i++
				current.WriteRune(runes[i])
			} else {
				current.WriteRune(ch)
			}
		default:
			fieldStarted = true
			current.WriteRune(ch)
		}
	}

	if quote != 0 {
		return nil, fmt.Errorf("unterminated quoted string")
	}

	if fieldStarted {
		fields = append(fields, current.String())
	}

	return fields, nil
}

func canEscapeInDoubleQuotes(ch rune) bool {
	return ch == '"' || ch == '\\'
}

func canEscapeOutsideQuotes(ch rune) bool {
	return unicode.IsSpace(ch) || ch == '\'' || ch == '"' || ch == '\\'
}
