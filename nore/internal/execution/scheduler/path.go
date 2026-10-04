package scheduler

import (
	"fmt"
	"strings"
)

func getPath(object map[string]any, path string) (any, bool) {
	path = strings.TrimSpace(path)
	if path == "" {
		return object, true
	}
	segments := strings.Split(path, ".")
	var current any = object
	for _, segment := range segments {
		segment = strings.TrimSpace(segment)
		if segment == "" {
			return nil, false
		}
		currentObject, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = currentObject[segment]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

func setPath(object map[string]any, path string, value any) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("target path is required")
	}
	segments := strings.Split(path, ".")
	current := object
	for index, rawSegment := range segments {
		segment := strings.TrimSpace(rawSegment)
		if segment == "" {
			return fmt.Errorf("target path %q contains an empty segment", path)
		}
		if index == len(segments)-1 {
			current[segment] = value
			return nil
		}
		existing, exists := current[segment]
		if !exists {
			child := make(map[string]any)
			current[segment] = child
			current = child
			continue
		}
		child, ok := existing.(map[string]any)
		if !ok {
			return fmt.Errorf("path segment %q is not an object", segment)
		}
		current = child
	}
	return nil
}
