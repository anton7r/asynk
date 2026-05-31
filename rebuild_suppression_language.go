package main

import (
	"crypto/sha256"
	"encoding/hex"
	"go/scanner"
	"go/token"
	"hash"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

type languageAwareFingerprint struct {
	Size int64
	Hash string
}

func languageAwareFingerprintContent(pathStr string, content []byte) ([]byte, bool) {
	switch strings.ToLower(filepath.Ext(pathStr)) {
	case ".go":
		return canonicalizeGo(content)
	case ".js", ".mjs", ".cjs", ".ts", ".mts", ".cts":
		return canonicalizeJavaScriptLike(content)
	case ".sql":
		return canonicalizeSQL(content)
	default:
		return nil, false
	}
}

func languageAwareFingerprintHash(pathStr string, content []byte) (languageAwareFingerprint, bool) {
	switch strings.ToLower(filepath.Ext(pathStr)) {
	case ".go":
		return canonicalGoFingerprintHash(content)
	}

	canonicalContent, ok := languageAwareFingerprintContent(pathStr, content)
	if !ok {
		return languageAwareFingerprint{}, false
	}

	sum := sha256.Sum256(canonicalContent)
	return languageAwareFingerprint{
		Size: int64(len(canonicalContent)),
		Hash: hex.EncodeToString(sum[:]),
	}, true
}

type canonicalHashWriter struct {
	hash hash.Hash
	size int64
	buf  [1024]byte
	used int
}

func newCanonicalHashWriter() canonicalHashWriter {
	return canonicalHashWriter{hash: sha256.New()}
}

func (w *canonicalHashWriter) writeBytes(value []byte) {
	w.size += int64(len(value))
	for len(value) > 0 {
		copied := copy(w.buf[w.used:], value)
		w.used += copied
		value = value[copied:]
		if w.used == len(w.buf) {
			w.flush()
		}
	}
}

func (w *canonicalHashWriter) writeByte(value byte) {
	w.size++
	w.buf[w.used] = value
	w.used++
	if w.used == len(w.buf) {
		w.flush()
	}
}

func (w *canonicalHashWriter) flush() {
	if w.used == 0 {
		return
	}
	_, _ = w.hash.Write(w.buf[:w.used])
	w.used = 0
}

func (w *canonicalHashWriter) sumHex() string {
	w.flush()
	var sum [sha256.Size]byte
	w.hash.Sum(sum[:0])
	return hex.EncodeToString(sum[:])
}

func canonicalGoFingerprintHash(content []byte) (languageAwareFingerprint, bool) {
	if fingerprint, ok := canonicalGoFastFingerprintHash(content); ok {
		return fingerprint, true
	}

	return canonicalGoScannerFingerprintHash(content)
}

func canonicalGoFastFingerprintHash(content []byte) (languageAwareFingerprint, bool) {
	writer := newCanonicalHashWriter()
	previousTokenCanEndStatement := false

	for i := 0; i < len(content); {
		c := content[i]
		switch {
		case isGoASCIIWhitespace(c):
			end, hasNewline := scanGoWhitespaceEnd(content, i)
			if hasNewline && previousTokenCanEndStatement && nextGoSignificantByte(content, end) == '{' {
				writeGoFastCanonicalTokenToHash(&writer, ';', []byte("\n{"))
			}
			i = end
		case c == '/' && i+1 < len(content) && content[i+1] == '/':
			end := scanGoLineCommentEnd(content, i+2)
			writeGoFastCanonicalTokenToHash(&writer, 'c', content[i:end])
			previousTokenCanEndStatement = false
			i = end
		case c == '/' && i+1 < len(content) && content[i+1] == '*':
			end, ok := scanGoBlockCommentEnd(content, i+2)
			if !ok {
				return languageAwareFingerprint{}, false
			}
			writeGoFastCanonicalTokenToHash(&writer, 'c', content[i:end])
			previousTokenCanEndStatement = false
			i = end
		case c == '"' || c == '\'':
			end, ok := scanGoQuotedLiteral(content, i, c)
			if !ok {
				return languageAwareFingerprint{}, false
			}
			writeGoFastCanonicalTokenToHash(&writer, 's', content[i:end])
			previousTokenCanEndStatement = true
			i = end
		case c == '`':
			end, ok := scanGoRawString(content, i)
			if !ok {
				return languageAwareFingerprint{}, false
			}
			writeGoFastCanonicalTokenToHash(&writer, 's', content[i:end])
			previousTokenCanEndStatement = true
			i = end
		case isGoASCIIIdentifierStart(c):
			end := scanGoIdentifier(content, i)
			writeGoFastCanonicalTokenToHash(&writer, 'i', content[i:end])
			previousTokenCanEndStatement = goTokenCanEndStatement(content[i:end])
			i = end
		case isGoASCIIDigit(c) || (c == '.' && i+1 < len(content) && isGoASCIIDigit(content[i+1])):
			end, ok := scanGoSimpleNumber(content, i)
			if !ok {
				return languageAwareFingerprint{}, false
			}
			writeGoFastCanonicalTokenToHash(&writer, 'n', content[i:end])
			previousTokenCanEndStatement = true
			i = end
		case c < utf8.RuneSelf:
			end := scanGoOperator(content, i)
			if end == i {
				return languageAwareFingerprint{}, false
			}
			writeGoFastCanonicalTokenToHash(&writer, 'o', content[i:end])
			previousTokenCanEndStatement = goTokenCanEndStatement(content[i:end])
			i = end
		default:
			r, size := utf8.DecodeRune(content[i:])
			if r == utf8.RuneError && size == 1 {
				return languageAwareFingerprint{}, false
			}
			if unicode.IsSpace(r) {
				end, hasNewline := scanGoUnicodeWhitespaceEnd(content, i)
				if hasNewline && previousTokenCanEndStatement && nextGoSignificantByte(content, end) == '{' {
					writeGoFastCanonicalTokenToHash(&writer, ';', []byte("\n{"))
				}
				i = end
				continue
			}
			if isGoIdentifierStart(r) {
				end := scanGoIdentifier(content, i)
				writeGoFastCanonicalTokenToHash(&writer, 'i', content[i:end])
				previousTokenCanEndStatement = goTokenCanEndStatement(content[i:end])
				i = end
				continue
			}
			return languageAwareFingerprint{}, false
		}
	}

	return languageAwareFingerprint{
		Size: writer.size,
		Hash: writer.sumHex(),
	}, true
}

func canonicalGoScannerFingerprintHash(content []byte) (languageAwareFingerprint, bool) {
	canonicalContent, ok := canonicalizeGo(content)
	if !ok {
		return languageAwareFingerprint{}, false
	}

	sum := sha256.Sum256(canonicalContent)
	return languageAwareFingerprint{
		Size: int64(len(canonicalContent)),
		Hash: hex.EncodeToString(sum[:]),
	}, true
}

func scanGoLineCommentEnd(content []byte, start int) int {
	for i := start; i < len(content); i++ {
		if content[i] == '\n' || content[i] == '\r' {
			return i
		}
	}
	return len(content)
}

func scanGoWhitespaceEnd(content []byte, start int) (int, bool) {
	hasNewline := false
	i := start
	for i < len(content) && isGoASCIIWhitespace(content[i]) {
		if content[i] == '\n' || content[i] == '\r' {
			hasNewline = true
		}
		i++
	}
	return i, hasNewline
}

func scanGoUnicodeWhitespaceEnd(content []byte, start int) (int, bool) {
	hasNewline := false
	i := start
	for i < len(content) {
		c := content[i]
		if c < utf8.RuneSelf {
			if !isGoASCIIWhitespace(c) {
				break
			}
			if c == '\n' || c == '\r' {
				hasNewline = true
			}
			i++
			continue
		}

		r, size := utf8.DecodeRune(content[i:])
		if r == utf8.RuneError && size == 1 {
			break
		}
		if !unicode.IsSpace(r) {
			break
		}
		i += size
	}
	return i, hasNewline
}

func nextGoSignificantByte(content []byte, start int) byte {
	for i := start; i < len(content); {
		c := content[i]
		if c < utf8.RuneSelf {
			if isGoASCIIWhitespace(c) {
				i++
				continue
			}
			return c
		}

		r, size := utf8.DecodeRune(content[i:])
		if r == utf8.RuneError && size == 1 {
			return 0
		}
		if unicode.IsSpace(r) {
			i += size
			continue
		}
		return 0
	}
	return 0
}

func scanGoBlockCommentEnd(content []byte, start int) (int, bool) {
	for i := start; i+1 < len(content); i++ {
		if content[i] == '*' && content[i+1] == '/' {
			return i + 2, true
		}
	}
	return 0, false
}

func scanGoQuotedLiteral(content []byte, start int, quote byte) (int, bool) {
	for i := start + 1; i < len(content); i++ {
		c := content[i]
		if c == '\\' {
			return 0, false
		}
		if c == '\n' || c == '\r' {
			return 0, false
		}
		if c == quote {
			return i + 1, true
		}
	}
	return 0, false
}

func scanGoRawString(content []byte, start int) (int, bool) {
	for i := start + 1; i < len(content); i++ {
		if content[i] == '`' {
			return i + 1, true
		}
	}
	return 0, false
}

func scanGoIdentifier(content []byte, start int) int {
	i := start
	for i < len(content) {
		c := content[i]
		if c < utf8.RuneSelf {
			if !isGoASCIIIdentifierPart(c) {
				break
			}
			i++
			continue
		}

		r, size := utf8.DecodeRune(content[i:])
		if r == utf8.RuneError && size == 1 {
			break
		}
		if !isGoIdentifierPart(r) {
			break
		}
		i += size
	}
	return i
}

func scanGoSimpleNumber(content []byte, start int) (int, bool) {
	if content[start] == '.' {
		return 0, false
	}

	i := start
	for i < len(content) && isGoASCIIDigit(content[i]) {
		i++
	}

	if i < len(content) && (isGoASCIILetter(content[i]) || content[i] == '_' || content[i] == '.') {
		return 0, false
	}
	return i, true
}

func scanGoOperator(content []byte, start int) int {
	if start+3 <= len(content) {
		switch string(content[start : start+3]) {
		case "<<=", ">>=", "&^=", "...":
			return start + 3
		}
	}
	if start+2 <= len(content) {
		switch string(content[start : start+2]) {
		case "+=", "-=", "*=", "/=", "%=", "&=", "|=", "^=", "&&", "||", "<-", "++", "--", "==", "!=", "<=", ">=", ":=", "<<", ">>", "&^":
			return start + 2
		}
	}

	switch content[start] {
	case '+', '-', '*', '/', '%', '&', '|', '^', '<', '>', '=', '!', '(', '[', '{', ',', '.', ')', ']', '}', ';', ':':
		return start + 1
	default:
		return start
	}
}

func isGoASCIIWhitespace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

func isGoASCIILetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isGoASCIIDigit(c byte) bool {
	return c >= '0' && c <= '9'
}

func isGoASCIIIdentifierStart(c byte) bool {
	return c == '_' || isGoASCIILetter(c)
}

func isGoASCIIIdentifierPart(c byte) bool {
	return isGoASCIIIdentifierStart(c) || isGoASCIIDigit(c)
}

func isGoIdentifierStart(r rune) bool {
	return r == '_' || unicode.IsLetter(r)
}

func isGoIdentifierPart(r rune) bool {
	return isGoIdentifierStart(r) || unicode.IsDigit(r)
}

func goTokenCanEndStatement(token []byte) bool {
	switch string(token) {
	case "break", "continue", "fallthrough", "return", "++", "--", ")", "]", "}":
		return true
	case "package", "import", "func", "var", "const", "type", "struct", "interface", "map", "chan", "go", "defer", "if", "else", "for", "range", "switch", "select", "case", "default", "goto":
		return false
	}

	if len(token) == 0 {
		return false
	}
	c := token[0]
	return c == '_' || isGoASCIILetter(c) || isGoASCIIDigit(c) || c == '"' || c == '\'' || c == '`'
}

func canonicalizeGo(content []byte) ([]byte, bool) {
	fileSet := token.NewFileSet()
	file := fileSet.AddFile("", fileSet.Base(), len(content))
	var goScanner scanner.Scanner
	scannerErrors := 0
	goScanner.Init(file, content, func(_ token.Position, _ string) {
		scannerErrors++
	}, scanner.ScanComments)

	buffer := make([]byte, 0, len(content))
	for {
		pos, tok, lit := goScanner.Scan()
		if tok == token.EOF {
			break
		}
		if tok == token.SEMICOLON && lit == "\n" {
			if nextGoSignificantByte(content, fileSet.Position(pos).Offset) == '{' {
				buffer = appendCanonicalToken(buffer, "semicolon", "\n{")
			}
			continue
		}
		if lit == "" {
			lit = tok.String()
		}
		buffer = appendCanonicalToken(buffer, tok.String(), lit)
	}
	if scannerErrors > 0 {
		return nil, false
	}

	return buffer, true
}

func canonicalizeJavaScriptLike(content []byte) ([]byte, bool) {
	if !utf8.Valid(content) {
		return nil, false
	}

	source := string(content)
	var builder strings.Builder
	previousToken := ""

	for i := 0; i < len(source); {
		r, size := utf8.DecodeRuneInString(source[i:])
		if isJSWhitespace(r) {
			end, hasLineTerminator := scanJSWhitespaceEnd(source, i)
			if hasLineTerminator && jsRestrictedKeywordRequiresLineTerminator(previousToken) && nextJSSignificantRune(source, end) != 0 {
				writeCanonicalToken(&builder, "line-terminator", "\n")
			}
			i = end
			continue
		}

		switch {
		case strings.HasPrefix(source[i:], "//"):
			end := scanLineCommentEnd(source, i+2)
			previousToken = writeCanonicalToken(&builder, "comment", source[i:end])
			i = end
		case strings.HasPrefix(source[i:], "/*"):
			end := strings.Index(source[i+2:], "*/")
			if end < 0 {
				return nil, false
			}
			end = i + 2 + end + 2
			previousToken = writeCanonicalToken(&builder, "comment", source[i:end])
			i = end
		case r == '\'' || r == '"':
			end, ok := scanJSString(source, i, r)
			if !ok {
				return nil, false
			}
			previousToken = writeCanonicalToken(&builder, "string", source[i:end])
			i = end
		case r == '`':
			end, ok := scanJSTemplate(source, i)
			if !ok {
				return nil, false
			}
			previousToken = writeCanonicalToken(&builder, "template", source[i:end])
			i = end
		case r == ';':
			if shouldSuppressJSSemicolon(source, i) {
				i += size
				continue
			}
			previousToken = writeCanonicalToken(&builder, "punct", ";")
			i += size
		case r == '/' && !strings.HasPrefix(source[i:], "//") && !strings.HasPrefix(source[i:], "/*"):
			if jsPreviousTokenCanEndExpression(previousToken) {
				end := scanJSOperator(source, i)
				previousToken = writeCanonicalToken(&builder, "op", source[i:end])
				i = end
				continue
			}

			end, ok := scanJSRegex(source, i)
			if !ok {
				return nil, false
			}
			previousToken = writeCanonicalToken(&builder, "regex", source[i:end])
			i = end
		case isJSIdentifierStart(r):
			end := scanJSIdentifier(source, i)
			previousToken = writeCanonicalToken(&builder, "ident", source[i:end])
			i = end
		case unicode.IsDigit(r) || (r == '.' && hasDigitAfterRune(source, i+size)):
			end := scanJSNumber(source, i)
			previousToken = writeCanonicalToken(&builder, "number", source[i:end])
			i = end
		case isJSOperatorRune(r):
			end := scanJSOperator(source, i)
			previousToken = writeCanonicalToken(&builder, "op", source[i:end])
			i = end
		default:
			end := i + size
			previousToken = writeCanonicalToken(&builder, "rune", source[i:end])
			i = end
		}
	}

	return []byte(builder.String()), true
}

func canonicalizeSQL(content []byte) ([]byte, bool) {
	if !utf8.Valid(content) {
		return nil, false
	}

	source := string(content)
	var builder strings.Builder
	previousTokenKind := ""

	for i := 0; i < len(source); {
		r, size := utf8.DecodeRuneInString(source[i:])
		if unicode.IsSpace(r) {
			end, hasLineTerminator := scanSQLWhitespaceEnd(source, i)
			if hasLineTerminator && previousTokenKind == "string" && nextSQLSignificantRune(source, end) == '\'' {
				writeCanonicalToken(&builder, "line-terminator", "\n")
			}
			i = end
			continue
		}

		switch {
		case strings.HasPrefix(source[i:], "--"):
			end := scanSQLLineCommentEnd(source, i+2)
			writeCanonicalToken(&builder, "comment", source[i:end])
			previousTokenKind = "comment"
			i = end
		case strings.HasPrefix(source[i:], "/*"):
			end := strings.Index(source[i+2:], "*/")
			if end < 0 {
				return nil, false
			}
			end = i + 2 + end + 2
			writeCanonicalToken(&builder, "comment", source[i:end])
			previousTokenKind = "comment"
			i = end
		case r == '\'':
			end, ok := scanSQLSingleQuotedString(source, i)
			if !ok {
				return nil, false
			}
			writeCanonicalToken(&builder, "string", source[i:end])
			previousTokenKind = "string"
			i = end
		case r == '"':
			end, ok := scanSQLDelimitedIdentifier(source, i, '"')
			if !ok {
				return nil, false
			}
			writeCanonicalToken(&builder, "ident", source[i:end])
			previousTokenKind = "ident"
			i = end
		case r == '`':
			end, ok := scanSQLDelimitedIdentifier(source, i, '`')
			if !ok {
				return nil, false
			}
			writeCanonicalToken(&builder, "ident", source[i:end])
			previousTokenKind = "ident"
			i = end
		case r == '[':
			end, ok := scanSQLBracketIdentifier(source, i)
			if !ok {
				return nil, false
			}
			writeCanonicalToken(&builder, "ident", source[i:end])
			previousTokenKind = "ident"
			i = end
		case r == '$':
			end, ok, matched := scanSQLDollarQuotedString(source, i)
			if matched {
				if !ok {
					return nil, false
				}
				writeCanonicalToken(&builder, "string", source[i:end])
				previousTokenKind = "string"
				i = end
				continue
			}
			if end := scanSQLDollarParameter(source, i); end > i {
				writeCanonicalToken(&builder, "param", source[i:end])
				previousTokenKind = "param"
				i = end
				continue
			}
			writeCanonicalToken(&builder, "op", source[i:i+size])
			previousTokenKind = "op"
			i += size
		case isSQLIdentifierStart(r):
			end := scanSQLIdentifier(source, i)
			writeCanonicalToken(&builder, "ident", source[i:end])
			previousTokenKind = "ident"
			i = end
		case unicode.IsDigit(r) || (r == '.' && hasDigitAfterRune(source, i+size)):
			end := scanSQLNumber(source, i)
			writeCanonicalToken(&builder, "number", source[i:end])
			previousTokenKind = "number"
			i = end
		case isSQLOperatorRune(r):
			end := scanSQLOperator(source, i)
			writeCanonicalToken(&builder, "op", source[i:end])
			previousTokenKind = "op"
			i = end
		default:
			end := i + size
			writeCanonicalToken(&builder, "rune", source[i:end])
			previousTokenKind = "rune"
			i = end
		}
	}

	return []byte(builder.String()), true
}

func writeCanonicalToken(builder *strings.Builder, kind string, token string) string {
	builder.WriteString(kind)
	builder.WriteByte(':')
	builder.WriteString(strconv.Itoa(len(token)))
	builder.WriteByte(':')
	builder.WriteString(token)
	builder.WriteByte('\n')
	return token
}

func writeGoFastCanonicalTokenToHash(writer *canonicalHashWriter, kind byte, literal []byte) {
	var header [24]byte
	buffer := header[:0]
	buffer = append(buffer, kind, ':')
	buffer = strconv.AppendInt(buffer, int64(len(literal)), 10)
	buffer = append(buffer, ':')
	writer.writeBytes(buffer)
	writer.writeBytes(literal)
	writer.writeByte('\n')
}

func appendCanonicalToken(buffer []byte, kind string, token string) []byte {
	buffer = append(buffer, kind...)
	buffer = append(buffer, ':')
	buffer = strconv.AppendInt(buffer, int64(len(token)), 10)
	buffer = append(buffer, ':')
	buffer = append(buffer, token...)
	buffer = append(buffer, '\n')
	return buffer
}

func scanLineCommentEnd(source string, start int) int {
	for i := start; i < len(source); {
		r, size := utf8.DecodeRuneInString(source[i:])
		if isJSLineTerminator(r) {
			return i
		}
		i += size
	}
	return len(source)
}

func scanJSWhitespaceEnd(source string, start int) (int, bool) {
	hasLineTerminator := false
	i := start
	for i < len(source) {
		r, size := utf8.DecodeRuneInString(source[i:])
		if !isJSWhitespace(r) {
			break
		}
		if isJSLineTerminator(r) {
			hasLineTerminator = true
		}
		i += size
	}
	return i, hasLineTerminator
}

func nextJSSignificantRune(source string, start int) rune {
	for i := start; i < len(source); {
		r, size := utf8.DecodeRuneInString(source[i:])
		if !isJSWhitespace(r) {
			return r
		}
		i += size
	}
	return 0
}

func scanJSString(source string, start int, quote rune) (int, bool) {
	escaped := false
	for i := start + 1; i < len(source); {
		r, size := utf8.DecodeRuneInString(source[i:])
		if escaped {
			escaped = false
			i += size
			continue
		}
		if r == '\\' {
			escaped = true
			i += size
			continue
		}
		if isJSLineTerminator(r) {
			return 0, false
		}
		if r == quote {
			return i + size, true
		}
		i += size
	}
	return 0, false
}

func scanJSTemplate(source string, start int) (int, bool) {
	escaped := false
	for i := start + 1; i < len(source); {
		r, size := utf8.DecodeRuneInString(source[i:])
		if escaped {
			escaped = false
			i += size
			continue
		}
		if r == '\\' {
			escaped = true
			i += size
			continue
		}
		if r == '$' && i+size < len(source) && source[i+size] == '{' {
			return 0, false
		}
		if r == '`' {
			return i + size, true
		}
		i += size
	}
	return 0, false
}

func scanJSRegex(source string, start int) (int, bool) {
	inClass := false
	escaped := false
	for i := start + 1; i < len(source); {
		r, size := utf8.DecodeRuneInString(source[i:])
		if escaped {
			escaped = false
			i += size
			continue
		}
		if r == '\\' {
			escaped = true
			i += size
			continue
		}
		if isJSLineTerminator(r) {
			return 0, false
		}
		if r == '[' {
			inClass = true
			i += size
			continue
		}
		if r == ']' && inClass {
			inClass = false
			i += size
			continue
		}
		if r == '/' && !inClass {
			i += size
			for i < len(source) {
				flag, flagSize := utf8.DecodeRuneInString(source[i:])
				if !isJSIdentifierPart(flag) {
					break
				}
				i += flagSize
			}
			return i, true
		}
		i += size
	}
	return 0, false
}

func scanJSIdentifier(source string, start int) int {
	i := start
	for i < len(source) {
		r, size := utf8.DecodeRuneInString(source[i:])
		if !isJSIdentifierPart(r) {
			break
		}
		i += size
	}
	return i
}

func scanJSNumber(source string, start int) int {
	i := start
	for i < len(source) {
		r, size := utf8.DecodeRuneInString(source[i:])
		if !(unicode.IsDigit(r) || unicode.IsLetter(r) || r == '_' || r == '.' || r == '+' || r == '-') {
			break
		}
		i += size
	}
	return i
}

func scanJSOperator(source string, start int) int {
	i := start
	for i < len(source) {
		r, size := utf8.DecodeRuneInString(source[i:])
		if r == ';' || r == '\'' || r == '"' || r == '`' || !isJSOperatorRune(r) {
			break
		}
		if r == '/' && i+1 < len(source) && (source[i+1] == '/' || source[i+1] == '*') {
			break
		}
		i += size
	}
	if i == start {
		_, size := utf8.DecodeRuneInString(source[start:])
		return start + size
	}
	return i
}

func shouldSuppressJSSemicolon(source string, semicolon int) bool {
	lineEndSeen := false
	for i := semicolon + 1; i < len(source); {
		r, size := utf8.DecodeRuneInString(source[i:])
		if isJSLineTerminator(r) {
			lineEndSeen = true
			i += size
			continue
		}
		if isJSWhitespace(r) {
			i += size
			continue
		}
		if strings.HasPrefix(source[i:], "//") {
			i = scanLineCommentEnd(source, i+2)
			lineEndSeen = true
			continue
		}
		if strings.HasPrefix(source[i:], "/*") {
			end := strings.Index(source[i+2:], "*/")
			if end < 0 {
				return false
			}
			end = i + 2 + end + 2
			if containsJSLineTerminator(source[i:end]) {
				lineEndSeen = true
			}
			i = end
			continue
		}
		if !lineEndSeen {
			return false
		}
		return !isJSASIHazardStart(r)
	}
	return true
}

func containsJSLineTerminator(value string) bool {
	for _, r := range value {
		if isJSLineTerminator(r) {
			return true
		}
	}
	return false
}

func isJSASIHazardStart(r rune) bool {
	switch r {
	case '(', '[', '/', '+', '-', '.', '`', '?':
		return true
	default:
		return false
	}
}

func jsPreviousTokenCanEndExpression(token string) bool {
	if token == "" {
		return false
	}
	switch token {
	case "++", "--":
		return true
	case ")", "]", "}":
		return false
	case "return", "throw", "yield", "await", "delete", "void", "typeof", "new", "case", "else", "do", "if", "while", "for", "switch", "catch", "with", "in", "instanceof", "of":
		return false
	}

	r, _ := utf8.DecodeLastRuneInString(token)
	return isJSIdentifierPart(r) || unicode.IsDigit(r) || r == '\'' || r == '"' || r == '`'
}

func jsRestrictedKeywordRequiresLineTerminator(token string) bool {
	switch token {
	case "return", "throw", "yield", "break", "continue":
		return true
	default:
		return false
	}
}

func hasDigitAfterRune(source string, index int) bool {
	if index >= len(source) {
		return false
	}
	r, _ := utf8.DecodeRuneInString(source[index:])
	return unicode.IsDigit(r)
}

func isJSWhitespace(r rune) bool {
	return unicode.IsSpace(r)
}

func isJSLineTerminator(r rune) bool {
	return r == '\n' || r == '\r' || r == '\u2028' || r == '\u2029'
}

func isJSIdentifierStart(r rune) bool {
	return r == '_' || r == '$' || unicode.IsLetter(r)
}

func isJSIdentifierPart(r rune) bool {
	return isJSIdentifierStart(r) || unicode.IsDigit(r)
}

func isJSOperatorRune(r rune) bool {
	switch r {
	case '{', '}', '(', ')', '[', ']', ',', ':', '?', '~', '+', '-', '*', '%', '=', '&', '|', '^', '!', '<', '>', '.', '/', '@':
		return true
	default:
		return false
	}
}

func scanSQLWhitespaceEnd(source string, start int) (int, bool) {
	hasLineTerminator := false
	i := start
	for i < len(source) {
		r, size := utf8.DecodeRuneInString(source[i:])
		if !unicode.IsSpace(r) {
			break
		}
		if isJSLineTerminator(r) {
			hasLineTerminator = true
		}
		i += size
	}
	return i, hasLineTerminator
}

func nextSQLSignificantRune(source string, start int) rune {
	for i := start; i < len(source); {
		r, size := utf8.DecodeRuneInString(source[i:])
		if !unicode.IsSpace(r) {
			return r
		}
		i += size
	}
	return 0
}

func scanSQLLineCommentEnd(source string, start int) int {
	for i := start; i < len(source); {
		r, size := utf8.DecodeRuneInString(source[i:])
		if isJSLineTerminator(r) {
			return i
		}
		i += size
	}
	return len(source)
}

func scanSQLSingleQuotedString(source string, start int) (int, bool) {
	escaped := false
	for i := start + 1; i < len(source); {
		r, size := utf8.DecodeRuneInString(source[i:])
		if escaped {
			escaped = false
			i += size
			continue
		}
		if r == '\\' {
			escaped = true
			i += size
			continue
		}
		if r == '\'' {
			if i+size < len(source) && source[i+size] == '\'' {
				i += size + 1
				continue
			}
			return i + size, true
		}
		i += size
	}
	return 0, false
}

func scanSQLDelimitedIdentifier(source string, start int, quote rune) (int, bool) {
	for i := start + 1; i < len(source); {
		r, size := utf8.DecodeRuneInString(source[i:])
		if r == quote {
			if i+size < len(source) {
				next, nextSize := utf8.DecodeRuneInString(source[i+size:])
				if next == quote {
					i += size + nextSize
					continue
				}
			}
			return i + size, true
		}
		i += size
	}
	return 0, false
}

func scanSQLBracketIdentifier(source string, start int) (int, bool) {
	for i := start + 1; i < len(source); {
		r, size := utf8.DecodeRuneInString(source[i:])
		if r == ']' {
			if i+size < len(source) && source[i+size] == ']' {
				i += size + 1
				continue
			}
			return i + size, true
		}
		i += size
	}
	return 0, false
}

func scanSQLDollarQuotedString(source string, start int) (int, bool, bool) {
	if start >= len(source) || source[start] != '$' {
		return 0, false, false
	}

	endDelimiter := start + 1
	if endDelimiter >= len(source) {
		return 0, false, false
	}

	r, size := utf8.DecodeRuneInString(source[endDelimiter:])
	if r == '$' {
		delimiter := source[start : endDelimiter+size]
		closeIndex := strings.Index(source[endDelimiter+size:], delimiter)
		if closeIndex < 0 {
			return 0, false, true
		}
		return endDelimiter + size + closeIndex + len(delimiter), true, true
	}
	if !(r == '_' || unicode.IsLetter(r)) {
		return 0, false, false
	}
	endDelimiter += size

	for endDelimiter < len(source) {
		r, size := utf8.DecodeRuneInString(source[endDelimiter:])
		if r == '$' {
			delimiter := source[start : endDelimiter+size]
			closeIndex := strings.Index(source[endDelimiter+size:], delimiter)
			if closeIndex < 0 {
				return 0, false, true
			}
			return endDelimiter + size + closeIndex + len(delimiter), true, true
		}
		if !(r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)) {
			return 0, false, false
		}
		endDelimiter += size
	}

	return 0, false, false
}

func scanSQLDollarParameter(source string, start int) int {
	if start >= len(source) || source[start] != '$' {
		return start
	}

	i := start + 1
	for i < len(source) {
		r, size := utf8.DecodeRuneInString(source[i:])
		if !unicode.IsDigit(r) {
			break
		}
		i += size
	}

	if i == start+1 {
		return start
	}
	return i
}

func scanSQLIdentifier(source string, start int) int {
	i := start
	for i < len(source) {
		r, size := utf8.DecodeRuneInString(source[i:])
		if !isSQLIdentifierPart(r) {
			break
		}
		i += size
	}
	return i
}

func scanSQLNumber(source string, start int) int {
	i := start
	if i < len(source) && source[i] == '.' {
		i++
	}

	for i < len(source) {
		r, size := utf8.DecodeRuneInString(source[i:])
		if !unicode.IsDigit(r) {
			break
		}
		i += size
	}

	if i < len(source) && source[i] == '.' {
		i++
		for i < len(source) {
			r, size := utf8.DecodeRuneInString(source[i:])
			if !unicode.IsDigit(r) {
				break
			}
			i += size
		}
	}

	if i < len(source) && (source[i] == 'e' || source[i] == 'E') {
		exponentStart := i
		i++
		if i < len(source) && (source[i] == '+' || source[i] == '-') {
			i++
		}
		digitsStart := i
		for i < len(source) {
			r, size := utf8.DecodeRuneInString(source[i:])
			if !unicode.IsDigit(r) {
				break
			}
			i += size
		}
		if i == digitsStart {
			return exponentStart
		}
	}

	return i
}

func scanSQLOperator(source string, start int) int {
	i := start
	for i < len(source) {
		if strings.HasPrefix(source[i:], "--") || strings.HasPrefix(source[i:], "/*") {
			break
		}
		r, size := utf8.DecodeRuneInString(source[i:])
		if !isSQLOperatorRune(r) || r == '\'' || r == '"' || r == '`' || r == '[' || r == '$' {
			break
		}
		i += size
	}
	if i == start {
		_, size := utf8.DecodeRuneInString(source[start:])
		return start + size
	}
	return i
}

func isSQLIdentifierStart(r rune) bool {
	return r == '_' || unicode.IsLetter(r)
}

func isSQLIdentifierPart(r rune) bool {
	return isSQLIdentifierStart(r) || unicode.IsDigit(r)
}

func isSQLOperatorRune(r rune) bool {
	switch r {
	case '(', ')', ',', ';', ':', '?', '~', '+', '-', '*', '/', '%', '=', '&', '|', '^', '!', '<', '>', '.', '#', '@', '{', '}':
		return true
	default:
		return false
	}
}
