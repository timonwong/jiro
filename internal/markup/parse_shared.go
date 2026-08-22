package markup

import (
	"bytes"
	"strings"
)

func dangerousDestinationScheme(destination []byte) (string, bool) {
	separator := bytes.IndexByte(destination, ':')
	if separator <= 0 {
		return "", false
	}
	for index, character := range destination[:separator] {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(index > 0 && ((character >= '0' && character <= '9') || character == '+' || character == '-' || character == '.')) {
			continue
		}
		return "", false
	}
	scheme := strings.ToLower(string(destination[:separator]))
	switch scheme {
	case "javascript", "vbscript", "data":
		return scheme, true
	default:
		return "", false
	}
}
