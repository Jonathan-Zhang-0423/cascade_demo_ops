package media

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	Seedance25CallbackWakeRecordSchemaVersion = "demoops.seedance_2_5_callback_wake_record.v1"
	defaultSeedance25CallbackDedupCapacity    = 1024
)

// Seedance25CallbackWakeRecord is an audit-safe wake-up handoff. It stores no
// callback secret, full callback payload, or complete signed provider URL.
type Seedance25CallbackWakeRecord struct {
	SchemaVersion    string    `json:"schema_version"`
	DedupKey         string    `json:"dedup_key"`
	TaskID           string    `json:"task_id"`
	Status           string    `json:"status"`
	UpdatedAtUnixSec int64     `json:"updated_at_unix_sec"`
	ReceivedAt       time.Time `json:"received_at"`
	Duplicate        bool      `json:"duplicate"`
	QueryRequired    bool      `json:"query_required"`
	NotificationOnly bool      `json:"notification_only"`
}

// Seedance25CallbackDeduper is an in-memory bounded deduper. It is purposely
// not an HTTP handler or durable task store. Production enablement requires a
// persistent task store and one-time callback-secret verification upstream.
type Seedance25CallbackDeduper struct {
	mu       sync.Mutex
	capacity int
	seen     map[string]struct{}
	order    []string
}

func NewSeedance25CallbackDeduper(capacity int) *Seedance25CallbackDeduper {
	if capacity <= 0 {
		capacity = defaultSeedance25CallbackDedupCapacity
	}
	return &Seedance25CallbackDeduper{capacity: capacity, seen: make(map[string]struct{}, capacity)}
}

// Accept turns an already authenticated and parsed callback into at most one
// GET-query wake-up per (task_id, status, updated_at). It never trusts the
// callback as task completion evidence.
func (d *Seedance25CallbackDeduper) Accept(event Seedance25CallbackEvent, receivedAt time.Time) (Seedance25CallbackWakeRecord, error) {
	if d == nil {
		return Seedance25CallbackWakeRecord{}, errors.New("Seedance 2.5 callback deduper is nil")
	}
	if event.SchemaVersion != Seedance25CallbackSchemaVersion || strings.TrimSpace(event.TaskID) == "" || !seedance25CallbackStatusAllowed(event.Status) || event.UpdatedAtUnixSec <= 0 || !event.RequiresQuery || !event.NotificationOnly {
		return Seedance25CallbackWakeRecord{}, errors.New("authenticated Seedance 2.5 callback event is required")
	}
	if receivedAt.IsZero() {
		receivedAt = time.Now().UTC()
	} else {
		receivedAt = receivedAt.UTC()
	}
	key := Seedance25CallbackDedupKey(event.TaskID, event.Status, event.UpdatedAtUnixSec)

	d.mu.Lock()
	defer d.mu.Unlock()
	_, duplicate := d.seen[key]
	if !duplicate {
		d.seen[key] = struct{}{}
		d.order = append(d.order, key)
		if len(d.order) > d.capacity {
			oldest := d.order[0]
			d.order = d.order[1:]
			delete(d.seen, oldest)
		}
	}
	return Seedance25CallbackWakeRecord{
		SchemaVersion: Seedance25CallbackWakeRecordSchemaVersion,
		DedupKey:      key, TaskID: event.TaskID, Status: event.Status, UpdatedAtUnixSec: event.UpdatedAtUnixSec,
		ReceivedAt: receivedAt, Duplicate: duplicate, QueryRequired: !duplicate, NotificationOnly: true,
	}, nil
}

func Seedance25CallbackDedupKey(taskID, status string, updatedAtUnixSec int64) string {
	return fmt.Sprintf("%s|%s|%d", strings.TrimSpace(taskID), strings.TrimSpace(status), updatedAtUnixSec)
}

func (d *Seedance25CallbackDeduper) Len() int {
	if d == nil {
		return 0
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.seen)
}
