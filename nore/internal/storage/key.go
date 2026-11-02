package storage

import (
	"fmt"
	"strings"
)

// Key joins key segments with '/'. It deliberately accepts empty segments:
// assembly artifacts carry an optional environment segment, and a partial key
// is represented by the same layout as its resolved form. Segments containing
// '/' or NUL are rejected because they would make the prefix boundaries
// ambiguous.
func Key(parts ...string) (string, error) {
	if len(parts) == 0 {
		return "", fmt.Errorf("%w: at least one segment is required", ErrInvalidKey)
	}
	for _, part := range parts {
		if strings.ContainsAny(part, "/\x00") {
			return "", fmt.Errorf("%w: segment %q contains '/' or NUL", ErrInvalidKey, part)
		}
	}
	return strings.Join(parts, "/"), nil
}

// Prefix returns Key(parts...) with a trailing separator. Use it for List
// prefixes so "exec_a" does not also match keys belonging to "exec_ab".
func Prefix(parts ...string) (string, error) {
	key, err := Key(parts...)
	if err != nil {
		return "", err
	}
	return key + "/", nil
}
