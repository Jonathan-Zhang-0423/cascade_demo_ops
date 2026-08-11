package media

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	MiniMaxH3CallbackWakeRecordSchemaVersion = "demoops.minimax_h3_callback_wake_record.v1"
	defaultMiniMaxH3CallbackDedupCapacity    = 1024
)

// MiniMaxH3CallbackWakeRecord is an audit-safe hand-off from a callback
// notification to the normal query path. It deliberately contains no prompt,
// API key, or complete provider URL. A callback never changes task state by
// creating this record.
type MiniMaxH3CallbackWakeRecord struct {
	SchemaVersion    string    `json:"schema_version"`
	DedupKey         string    `json:"dedup_key"`
	TaskID           string    `json:"task_id"`
	Status           string    `json:"status"`
	PayloadSHA256    string    `json:"payload_sha256"`
	ReceivedAt       time.Time `json:"received_at"`
	Duplicate        bool      `json:"duplicate"`
	QueryRequired    bool      `json:"query_required"`
	NotificationOnly bool      `json:"notification_only"`
}

// MiniMaxH3CallbackDeduper is an in-memory, bounded deduper for callback wake
// notifications. It is intentionally not wired to an HTTP route or task
// store; production callback handling will require durable/distributed
// deduplication before being enabled.
type MiniMaxH3CallbackDeduper struct {
	mu       sync.Mutex
	capacity int
	seen     map[string]struct{}
	order    []string
}

func NewMiniMaxH3CallbackDeduper(capacity int) *MiniMaxH3CallbackDeduper {
	if capacity <= 0 {
		capacity = defaultMiniMaxH3CallbackDedupCapacity
	}
	return &MiniMaxH3CallbackDeduper{capacity: capacity, seen: make(map[string]struct{}, capacity)}
}

// Accept records a task callback and returns whether the normal query path
// should be woken. The first occurrence has QueryRequired=true; duplicates do
// not produce another query intent. Challenge events are rejected because
// they are handled by the verification handshake, not task wake-up logic.
func (d *MiniMaxH3CallbackDeduper) Accept(event MiniMaxH3CallbackEvent, raw []byte, receivedAt time.Time) (MiniMaxH3CallbackWakeRecord, error) {
	if d == nil {
		return MiniMaxH3CallbackWakeRecord{}, errors.New("callback deduper is nil")
	}
	if !event.HasTaskEvent || strings.TrimSpace(event.TaskID) == "" || strings.TrimSpace(event.Status) == "" {
		return MiniMaxH3CallbackWakeRecord{}, errors.New("task callback event is required")
	}
	if receivedAt.IsZero() {
		receivedAt = time.Now().UTC()
	} else {
		receivedAt = receivedAt.UTC()
	}
	payloadDigest := sha256.Sum256(raw)
	payloadSHA256 := hex.EncodeToString(payloadDigest[:])
	dedupKey := MiniMaxH3CallbackDedupKey(event.TaskID, event.Status, payloadSHA256)

	d.mu.Lock()
	defer d.mu.Unlock()
	_, duplicate := d.seen[dedupKey]
	if !duplicate {
		d.seen[dedupKey] = struct{}{}
		d.order = append(d.order, dedupKey)
		if len(d.order) > d.capacity {
			oldest := d.order[0]
			d.order = d.order[1:]
			delete(d.seen, oldest)
		}
	}
	return MiniMaxH3CallbackWakeRecord{
		SchemaVersion: MiniMaxH3CallbackWakeRecordSchemaVersion,
		DedupKey:      dedupKey, TaskID: strings.TrimSpace(event.TaskID), Status: strings.TrimSpace(event.Status),
		PayloadSHA256: payloadSHA256, ReceivedAt: receivedAt,
		Duplicate: duplicate, QueryRequired: !duplicate, NotificationOnly: true,
	}, nil
}

// MiniMaxH3CallbackDedupKey is deterministic and contains only identifiers and
// a digest; raw callback content is never retained by the deduper.
func MiniMaxH3CallbackDedupKey(taskID, status, payloadSHA256 string) string {
	return fmt.Sprintf("%s|%s|%s", strings.TrimSpace(taskID), strings.TrimSpace(status), strings.TrimSpace(payloadSHA256))
}

func (d *MiniMaxH3CallbackDeduper) Len() int {
	if d == nil {
		return 0
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.seen)
}
