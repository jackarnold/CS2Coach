package parser

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// KV3ParseError holds detailed error information.
type KV3ParseError struct {
	Message string
	Index   int
	Content string
}

func (e *KV3ParseError) Error() string {
	// Calculate line and column.
	substr := e.Content[:e.Index]
	lines := strings.Split(substr, "\n")
	lineNumber := len(lines)
	column := len(lines[len(lines)-1]) + 1

	// Provide context around the error.
	contextStart := e.Index - 20
	if contextStart < 0 {
		contextStart = 0
	}
	contextEnd := e.Index + 20
	if contextEnd > len(e.Content) {
		contextEnd = len(e.Content)
	}
	context := e.Content[contextStart:contextEnd]
	pointer := strings.Repeat(" ", e.Index-contextStart) + "^"
	return fmt.Sprintf("%s at line %d, column %d:\n\n%s\n%s", e.Message, lineNumber, column, context, pointer)
}

// KV3Parser parses KV3-formatted strings.
type KV3Parser struct {
	content string
	index   int
	// Allowed flags stored in a map for quick lookup.
	flags map[string]bool
}

// NewKV3Parser creates a new parser instance.
func NewKV3Parser(content string) *KV3Parser {
	return &KV3Parser{
		content: content,
		index:   0,
		flags: map[string]bool{
			"resource":     true,
			"resourcename": true,
			"panorama":     true,
			"soundevent":   true,
			"subclass":     true,
		},
	}
}

// Parse is the main entry point. It returns a native Go value:
// maps for objects, slices for arrays, and so on.
func (p *KV3Parser) Parse() (interface{}, error) {
	if err := p.skipHeader(); err != nil {
		return nil, err
	}
	val, err := p.parseValue()
	if err != nil {
		return nil, err
	}
	return val, nil
}

// skipHeader skips an optional header if it is present.
func (p *KV3Parser) skipHeader() error {
	// The header must occur at the very beginning.
	headerPattern := `^<!-- kv3 encoding:text:version{.*?} format:generic:version{.*?} -->`
	re := regexp.MustCompile(headerPattern)
	if loc := re.FindStringIndex(p.content); loc != nil && loc[0] == 0 {
		p.index = loc[1]
	}
	p.skipWhitespace()
	return nil
}

// skipWhitespace advances the index past any whitespace or comments.
func (p *KV3Parser) skipWhitespace() {
	for p.index < len(p.content) {
		c := p.content[p.index]
		if unicode.IsSpace(rune(c)) {
			p.index++
		} else if p.index+1 < len(p.content) && p.content[p.index:p.index+2] == "//" {
			// Skip single-line comment.
			newline := strings.Index(p.content[p.index:], "\n")
			if newline == -1 {
				p.index = len(p.content)
			} else {
				p.index += newline + 1
			}
		} else if p.index+1 < len(p.content) && p.content[p.index:p.index+2] == "/*" {
			// Skip multi-line comment.
			end := strings.Index(p.content[p.index:], "*/")
			if end == -1 {
				p.index = len(p.content)
			} else {
				p.index += end + 2
			}
		} else {
			break
		}
	}
}

// parseValue dispatches to the proper parser function based on the next token.
func (p *KV3Parser) parseValue() (interface{}, error) {
	p.skipWhitespace()
	if p.index >= len(p.content) {
		return nil, &KV3ParseError{"Unexpected end of input", p.index, p.content}
	}

	// Special case: if the next token is "subclass", then parse as a subclass.
	if p.index+8 <= len(p.content) && p.content[p.index:p.index+8] == "subclass" {
		return p.parseSubclass()
	}

	switch p.content[p.index] {
	case '{':
		return p.parseObject()
	case '[':
		return p.parseArray()
	case '"':
		return p.parseString()
	default:
		// If the first character is a digit or '-', assume a number.
		ch := p.content[p.index]
		if (ch >= '0' && ch <= '9') || ch == '-' {
			return p.parseNumber()
		}
		return p.parseKeywordOrResource()
	}
}

// parseSubclass handles the "subclass" special syntax.
func (p *KV3Parser) parseSubclass() (interface{}, error) {
	p.index += 8 // Skip "subclass"
	p.skipWhitespace()
	if p.index >= len(p.content) || p.content[p.index] != ':' {
		return nil, &KV3ParseError{"Expected ':' after 'subclass'", p.index, p.content}
	}
	p.index++ // Skip ':'
	p.skipWhitespace()
	if p.index >= len(p.content) || p.content[p.index] != '{' {
		return nil, &KV3ParseError{"Expected '{' after 'subclass:'", p.index, p.content}
	}
	obj, err := p.parseObject()
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{"subclass": obj}, nil
}

// parseObject parses an object enclosed in braces.
func (p *KV3Parser) parseObject() (interface{}, error) {
	result := make(map[string]interface{})
	if p.content[p.index] != '{' {
		return nil, &KV3ParseError{"Expected '{' at beginning of object", p.index, p.content}
	}
	p.index++ // Skip '{'
	for {
		p.skipWhitespace()
		if p.index >= len(p.content) {
			return nil, &KV3ParseError{"Unexpected end of input while parsing object", p.index, p.content}
		}
		if p.content[p.index] == '}' {
			p.index++ // Skip '}'
			return result, nil
		}

		// Parse key and optional flag.
		key, flag, err := p.parseKey()
		if err != nil {
			return nil, err
		}

		p.skipWhitespace()
		var value interface{}
		if p.index < len(p.content) && p.content[p.index] == '=' {
			p.index++ // Skip '='
			value, err = p.parseValue()
			if err != nil {
				return nil, err
			}
		} else if p.index < len(p.content) && p.content[p.index] == '{' {
			value, err = p.parseObject()
			if err != nil {
				return nil, err
			}
		} else {
			return nil, &KV3ParseError{"Expected '=' or '{' after key", p.index, p.content}
		}

		// If a flag was specified, wrap the value in a struct-like map.
		if flag != "" {
			result[key] = map[string]interface{}{
				"value": value,
				"flag":  flag,
			}
		} else {
			result[key] = value
		}

		p.skipWhitespace()
		if p.index >= len(p.content) {
			return nil, &KV3ParseError{"Unexpected end of input while parsing object", p.index, p.content}
		}
		if p.content[p.index] == ',' {
			p.index++ // Skip comma
		} else if p.content[p.index] != '}' {
			// Allow omitting commas between key-value pairs.
			continue
		}
	}
}

// parseArray parses an array enclosed in brackets.
func (p *KV3Parser) parseArray() (interface{}, error) {
	var result []interface{}
	if p.content[p.index] != '[' {
		return nil, &KV3ParseError{"Expected '[' at beginning of array", p.index, p.content}
	}
	p.index++ // Skip '['
	for {
		p.skipWhitespace()
		if p.index >= len(p.content) {
			return nil, &KV3ParseError{"Unexpected end of input while parsing array", p.index, p.content}
		}
		if p.content[p.index] == ']' {
			p.index++ // Skip ']'
			return result, nil
		}

		value, err := p.parseValue()
		if err != nil {
			return nil, err
		}
		result = append(result, value)

		p.skipWhitespace()
		if p.index >= len(p.content) {
			return nil, &KV3ParseError{"Unexpected end of input while parsing array", p.index, p.content}
		}
		if p.content[p.index] == ',' {
			p.index++ // Skip comma
		} else if p.content[p.index] != ']' {
			// Allow omitting commas in arrays.
			continue
		}
	}
}

// parseString parses a quoted string, handling escapes and multi-line strings.
func (p *KV3Parser) parseString() (interface{}, error) {
	// Check for multi-line string using triple quotes.
	if p.index+3 <= len(p.content) && p.content[p.index:p.index+3] == `"""` {
		return p.parseMultilineString()
	}
	if p.content[p.index] != '"' {
		return nil, &KV3ParseError{"Expected '\"' at beginning of string", p.index, p.content}
	}
	p.index++ // Skip opening quote
	var result strings.Builder
	for p.index < len(p.content) && p.content[p.index] != '"' {
		if p.content[p.index] == '\\' {
			p.index++
			if p.index >= len(p.content) {
				return nil, &KV3ParseError{"Unexpected end of input in string", p.index, p.content}
			}
			switch p.content[p.index] {
			case 'n':
				result.WriteByte('\n')
			case 't':
				result.WriteByte('\t')
			default:
				result.WriteByte(p.content[p.index])
			}
		} else {
			result.WriteByte(p.content[p.index])
		}
		p.index++
	}
	if p.index >= len(p.content) {
		return nil, &KV3ParseError{"Unterminated string", p.index, p.content}
	}
	p.index++ // Skip closing quote
	return result.String(), nil
}

// parseMultilineString parses a multi-line string enclosed by triple quotes.
func (p *KV3Parser) parseMultilineString() (interface{}, error) {
	p.index += 3 // Skip opening triple quotes
	// Optionally skip a newline after the opening quotes.
	if p.index < len(p.content) && p.content[p.index] == '\n' {
		p.index++
	}
	start := p.index
	// Look for the closing delimiter: a newline followed by triple quotes.
	for {
		if p.index >= len(p.content) {
			break
		}
		if p.index+4 <= len(p.content) && p.content[p.index:p.index+4] == "\n\"\"\"" {
			break
		}
		p.index++
	}
	if p.index+4 > len(p.content) || p.content[p.index:p.index+4] != "\n\"\"\"" {
		return nil, &KV3ParseError{"Unterminated multi-line string", p.index, p.content}
	}
	result := p.content[start:p.index]
	p.index += 4 // Skip the closing delimiter (newline and triple quotes)
	return result, nil
}

// parseNumber parses a number token (integer or float).
func (p *KV3Parser) parseNumber() (interface{}, error) {
	start := p.index
	for p.index < len(p.content) {
		ch := p.content[p.index]
		if (ch >= '0' && ch <= '9') || strings.ContainsRune(".-+e", rune(ch)) {
			p.index++
		} else {
			break
		}
	}
	numberStr := p.content[start:p.index]
	// Try to parse as integer.
	if i, err := strconv.Atoi(numberStr); err == nil {
		return i, nil
	}
	// Try to parse as float.
	if f, err := strconv.ParseFloat(numberStr, 64); err == nil {
		return f, nil
	}
	return nil, &KV3ParseError{Message: fmt.Sprintf("Invalid number: %s", numberStr), Index: start, Content: p.content}
}

// parseKeywordOrResource parses an unquoted keyword or resource.
func (p *KV3Parser) parseKeywordOrResource() (interface{}, error) {
	start := p.index
	for p.index < len(p.content) {
		ch := p.content[p.index]
		if unicode.IsSpace(rune(ch)) {
			break
		}
		if strings.ContainsRune("{}[]", rune(ch)) {
			substr := p.content[start:p.index]
			if strings.Count(substr, "\"")%2 == 0 {
				break
			}
		}
		p.index++
	}
	keyword := strings.TrimSpace(p.content[start:p.index])
	switch keyword {
	case "true":
		return true, nil
	case "false":
		return false, nil
	case "null":
		return nil, nil
	default:
		return keyword, nil
	}
}

// parseKey parses a key and an optional flag.
func (p *KV3Parser) parseKey() (string, string, error) {
	p.skipWhitespace()
	if p.index >= len(p.content) {
		return "", "", &KV3ParseError{"Unexpected end of input while parsing key", p.index, p.content}
	}
	var key string
	if p.content[p.index] == '"' {
		s, err := p.parseString()
		if err != nil {
			return "", "", err
		}
		var ok bool
		key, ok = s.(string)
		if !ok {
			return "", "", errors.New("Parsed key is not a string")
		}
	} else {
		start := p.index
		for p.index < len(p.content) {
			ch := p.content[p.index]
			if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_' {
				p.index++
			} else {
				break
			}
		}
		if start == p.index {
			return "", "", &KV3ParseError{"Invalid key", start, p.content}
		}
		key = p.content[start:p.index]
	}
	p.skipWhitespace()
	flag := ""
	if p.index < len(p.content) && p.content[p.index] == ':' {
		p.index++ // Skip colon
		flagStart := p.index
		for p.index < len(p.content) {
			ch := p.content[p.index]
			if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') {
				p.index++
			} else {
				break
			}
		}
		flag = p.content[flagStart:p.index]
		if !p.flags[flag] {
			return "", "", &KV3ParseError{Message: fmt.Sprintf("Invalid flag: %s", flag), Index: flagStart, Content: p.content}
		}
	}
	return key, flag, nil
}

// kv3Deserialize is a helper function that deserializes the KV3 input into native Go types.
func kv3Deserialize(kv3Content string) (interface{}, error) {
	parser := NewKV3Parser(kv3Content)
	return parser.Parse()
}

// Example usage.
func main() {
	// Example KV3 content. Adjust or replace with your own test string.
	example := `<!-- kv3 encoding:text:version{1} format:generic:version{1} -->
{
    "name" = "Example",
    "values" = [ 1, 2, 3 ],
    subclass: {
        "subkey" = "subvalue"
    },
    key_with_flag:resource = "flagged value"
}`

	parsedData, err := kv3Deserialize(example)
	if err != nil {
		fmt.Println("Error parsing KV3:", err)
		return
	}

	// Print the resulting Go data structure.
	// Objects are represented as map[string]interface{} and arrays as []interface{}.
	fmt.Printf("Deserialized KV3 data:\n%#v\n", parsedData)
}
