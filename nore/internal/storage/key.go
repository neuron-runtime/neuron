package storage

import (
	"encoding/hex"
	"fmt"
	"strings"
	"time"
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

// TimerKey creates a timer key from execution, capability, and attempt IDs.
// The key format is "timers/{executionID}/{capabilityID}/{attemptID}".
func TimerKey(executionID, capabilityID, attemptID string) (string, error) {
	return Key("timers", executionID, capabilityID, attemptID)
}

// TimerPrefix creates a timer prefix for listing all timers belonging to
// a specific execution and capability combination.
// The prefix format is "timers/{executionID}/{capabilityID}/".
func TimerPrefix(executionID, capabilityID string) (string, error) {
	return Prefix("timers", executionID, capabilityID)
}

// TimerPrefixForExecution creates a timer prefix for listing all timers
// belonging to a specific execution.
// The prefix format is "timers/{executionID}/".
func TimerPrefixForExecution(executionID string) (string, error) {
	return Prefix("timers", executionID)
}

const (
	deadlineNamespace  = "execution_deadlines"
	waitDueNamespace   = "wait_due"
	waitDueLookupSpace = "wait_due_lookup"

	// A fixed-width UTC timestamp makes lexicographic key order match due-time
	// order, so discovery can stop at a batch bound instead of scanning the
	// whole namespace.
	deadlineTimeLayout = "20060102T150405.000000000Z"
)

// ExecutionDeadlineKey builds the dedicated, time-ordered key for one live
// execution deadline. It lives outside executions/ so deadline discovery never
// has to list execution records.
func ExecutionDeadlineKey(instanceID string, dueAt time.Time, executionID string) (string, error) {
	if dueAt.IsZero() {
		return "", fmt.Errorf("%w: execution deadline requires a due time", ErrInvalidKey)
	}
	return Key(deadlineNamespace, instanceID, dueAt.UTC().Format(deadlineTimeLayout), executionID)
}

// ExecutionDeadlinePrefix returns the List prefix for one instance's deadlines.
func ExecutionDeadlinePrefix(instanceID string) (string, error) {
	return Prefix(deadlineNamespace, instanceID)
}

// ParseExecutionDeadlineKey decodes a key produced by ExecutionDeadlineKey.
func ParseExecutionDeadlineKey(key string) (string, time.Time, string, error) {
	parts := strings.Split(key, "/")
	if len(parts) != 4 || parts[0] != deadlineNamespace {
		return "", time.Time{}, "", fmt.Errorf("%w: invalid execution deadline key %q", ErrInvalidKey, key)
	}
	dueAt, err := time.Parse(deadlineTimeLayout, parts[2])
	if err != nil {
		return "", time.Time{}, "", fmt.Errorf("%w: invalid execution deadline time: %v", ErrInvalidKey, err)
	}
	return parts[1], dueAt, parts[3], nil
}

// WaitDueKey builds an additive due-time index entry for one wait timer. The
// timer key is hex encoded so it cannot introduce an extra path segment.
func WaitDueKey(instanceID, timerKey string) (string, error) {
	if timerKey == "" {
		return "", fmt.Errorf("%w: wait due index requires a timer key", ErrInvalidKey)
	}
	return Key(waitDueNamespace, instanceID, hex.EncodeToString([]byte(timerKey)))
}

// WaitDuePrefix returns the List prefix for one instance's wait due entries.
func WaitDuePrefix(instanceID string) (string, error) {
	return Prefix(waitDueNamespace, instanceID)
}

// WaitDueLookupKey builds the pointer from a timer key to its instance-scoped
// due index entry. It makes fenced deletes exact rather than prefix scans.
func WaitDueLookupKey(timerKey string) (string, error) {
	if timerKey == "" {
		return "", fmt.Errorf("%w: wait due lookup requires a timer key", ErrInvalidKey)
	}
	return Key(waitDueLookupSpace, hex.EncodeToString([]byte(timerKey)))
}

// ParseWaitDueKey decodes a key produced by WaitDueKey.
func ParseWaitDueKey(key string) (string, string, error) {
	parts := strings.Split(key, "/")
	if len(parts) != 3 || parts[0] != waitDueNamespace {
		return "", "", fmt.Errorf("%w: invalid wait due key %q", ErrInvalidKey, key)
	}
	timerKey, err := hex.DecodeString(parts[2])
	if err != nil {
		return "", "", fmt.Errorf("%w: invalid wait due timer key: %v", ErrInvalidKey, err)
	}
	return parts[1], string(timerKey), nil
}
