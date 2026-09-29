package event

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/storage"
	"github.com/neuron-runtime/neuron/nore/internal/storage/sqlite"
	"github.com/neuron-runtime/neuron/shared/types/core"
)

type persistedEvent struct {
	EventID       core.ID         `json:"event_id"`
	Type          Type            `json:"type"`
	CorrelationID core.ID         `json:"correlation_id"`
	ExecutionID   core.ID         `json:"execution_id"`
	CapabilityID  core.ID         `json:"capability_id"`
	OccurredAt    int64           `json:"occurred_at"`
	Payload       json.RawMessage `json:"payload"`
}

type Store struct {
	store storage.Store
}

func NewStore(store storage.Store) *Store {
	return &Store{store: store}
}

func (s *Store) Save(ctx context.Context, evt Event) error {
	payload, err := json.Marshal(evt.Payload)
	if err != nil {
		return fmt.Errorf("marshal event payload: %w", err)
	}

	pe := persistedEvent{
		EventID:       evt.Metadata.EventID,
		Type:          evt.Type,
		CorrelationID: evt.Metadata.CorrelationID,
		ExecutionID:   evt.Metadata.ExecutionID,
		CapabilityID:  evt.Metadata.CapabilityID,
		OccurredAt:    evt.Metadata.OccurredAt.UnixNano(),
		Payload:       payload,
	}

	data, err := json.Marshal(pe)
	if err != nil {
		return fmt.Errorf("marshal persisted event: %w", err)
	}

	return s.store.Put(ctx, s.key(evt.Metadata.ExecutionID, evt.Metadata.EventID), data)
}

func (s *Store) List(ctx context.Context, executionID core.ID) ([]Event, error) {
	prefix := sqlite.SanitizeKey("events", string(executionID))
	keys, err := s.store.List(ctx, prefix)
	if err != nil {
		return nil, err
	}

	events := make([]Event, 0, len(keys))
	for _, key := range keys {
		evt, err := s.load(ctx, key)
		if err != nil {
			continue
		}
		events = append(events, evt)
	}

	// Event IDs are millisecond-precise; a burst of events within one
	// millisecond can store out of order. Sort by the nanosecond occurrence
	// time, breaking ties by event ID, so history is always chronological.
	sort.SliceStable(events, func(i, j int) bool {
		if !events[i].Metadata.OccurredAt.Equal(events[j].Metadata.OccurredAt) {
			return events[i].Metadata.OccurredAt.Before(events[j].Metadata.OccurredAt)
		}
		return events[i].Metadata.EventID < events[j].Metadata.EventID
	})

	return events, nil
}

func (s *Store) ListAfter(ctx context.Context, executionID core.ID, afterEventID core.ID) ([]Event, error) {
	all, err := s.List(ctx, executionID)
	if err != nil {
		return nil, err
	}

	if afterEventID == "" {
		return all, nil
	}

	for i, evt := range all {
		if evt.Metadata.EventID == afterEventID {
			return all[i+1:], nil
		}
	}

	return nil, nil
}

func (s *Store) load(ctx context.Context, key string) (Event, error) {
	data, err := s.store.Get(ctx, key)
	if err != nil {
		return Event{}, err
	}

	var pe persistedEvent
	if err := json.Unmarshal(data, &pe); err != nil {
		return Event{}, fmt.Errorf("unmarshal persisted event: %w", err)
	}

	return Event{
		Type: pe.Type,
		Metadata: Metadata{
			EventID:       pe.EventID,
			ExecutionID:   pe.ExecutionID,
			CorrelationID: pe.CorrelationID,
			CapabilityID:  pe.CapabilityID,
			OccurredAt:    time.Unix(0, pe.OccurredAt).UTC(),
		},
		Payload: pe.Payload,
	}, nil
}

func (s *Store) key(executionID, eventID core.ID) string {
	return sqlite.SanitizeKey("events", string(executionID), string(eventID))
}

// DeleteExecution removes every persisted event recorded for the execution.
func (s *Store) DeleteExecution(ctx context.Context, executionID core.ID) error {
	prefix := sqlite.SanitizeKey("events", string(executionID))
	keys, err := s.store.List(ctx, prefix)
	if err != nil {
		return err
	}
	for _, key := range keys {
		if err := s.store.Delete(ctx, key); err != nil {
			return err
		}
	}
	return nil
}

// unixNanoToTime intentionally deleted; time.Unix is used directly in load.
