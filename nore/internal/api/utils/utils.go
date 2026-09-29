package utils

import (
	"encoding/json"
	maps0 "maps"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/neuron-runtime/neuron/shared/types/protocol"
)

func WriteJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func PathID(value string) string {
	return strings.TrimSpace(value)
}

func ErrorJSON(w http.ResponseWriter, status int, err error) {
	WriteJSON(w, status, protocol.Response{Message: err.Error(), Status: status})
}

func ParseBool(q url.Values, key string, defaultVal bool) bool {
	val := q.Get(key)
	if strings.EqualFold(val, "true") {
		return true
	}
	if strings.EqualFold(val, "false") {
		return false
	}
	return defaultVal
}

func GetQueryParams(r *http.Request) map[string]any {
	params := make(map[string]any)
	query := r.URL.Query()

	for key, values := range query {
		if len(values) == 1 {
			params[key] = ParseBoolVal(values[0])
		} else if len(values) > 1 {
			parsed := make([]any, len(values))
			for i, v := range values {
				parsed[i] = ParseBoolVal(v)
			}
			params[key] = parsed
		}
	}

	return params
}

func ParseBoolVal(v string) any {
	if b, err := strconv.ParseBool(v); err == nil {
		return b
	}
	return v
}

func MergeMaps(maps ...map[string]any) map[string]any {
	merged := make(map[string]any)
	for _, m := range maps {
		maps0.Copy(merged, m)
	}
	return merged
}
