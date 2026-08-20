package media

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	Seedance25MaxConcurrentEnv  = "CASCADE_SEEDANCE25_MAX_CONCURRENT"
	Seedance25CreateTaskRPMEnv  = "CASCADE_SEEDANCE25_CREATE_TASK_RPM"
	Seedance25IdempotencyTTLEnv = "CASCADE_SEEDANCE25_IDEMPOTENCY_TTL_SEC"
)

const (
	Seedance25AdmissionAccepted            = "accepted"
	Seedance25AdmissionReplay              = "idempotent_resume"
	Seedance25AdmissionConcurrentLimit     = "seedance_2_5_concurrent_limit_exceeded"
	Seedance25AdmissionRequestQuota        = "seedance_2_5_request_quota_exceeded"
	Seedance25AdmissionIdempotencyRequired = "seedance_2_5_idempotency_key_required"
	Seedance25AdmissionIdempotencyConflict = "seedance_2_5_idempotency_key_conflict"
	Seedance25AdmissionIdempotencyInFlight = "seedance_2_5_idempotency_in_progress"
	Seedance25AdmissionInvalidRequest      = "seedance_2_5_admission_request_invalid"
	Seedance25AdmissionInvalidPolicy       = "seedance_2_5_admission_policy_invalid"
)

// Seedance25AdmissionPolicy is Server-owned. The default production limits
// must be supplied by operations because Ark's account tier is not embedded
// in an App package. The gate is intentionally independent of price: no
// public billing rate is inferred or fabricated.
type Seedance25AdmissionPolicy struct {
	MaxConcurrent        int           `json:"max_concurrent"`
	Window               time.Duration `json:"window"`
	MaxRequestsPerWindow int           `json:"max_requests_per_window"`
	IdempotencyTTL       time.Duration `json:"idempotency_ttl"`
}

// Seedance25AdmissionPolicyFromEnv starts from the documented personal-tier
// ceiling (3 concurrent / 180 create-task RPM). Operations can lower or raise
// it only after confirming the actual account/Endpoint quota; the App never
// controls either value.
func Seedance25AdmissionPolicyFromEnv(getenv func(string) string) (Seedance25AdmissionPolicy, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	policy := Seedance25AdmissionPolicy{MaxConcurrent: 3, Window: time.Minute, MaxRequestsPerWindow: 180, IdempotencyTTL: 24 * time.Hour}
	var err error
	if policy.MaxConcurrent, err = positiveSeedance25EnvInt(getenv, Seedance25MaxConcurrentEnv, policy.MaxConcurrent); err != nil {
		return Seedance25AdmissionPolicy{}, err
	}
	if policy.MaxRequestsPerWindow, err = positiveSeedance25EnvInt(getenv, Seedance25CreateTaskRPMEnv, policy.MaxRequestsPerWindow); err != nil {
		return Seedance25AdmissionPolicy{}, err
	}
	seconds, err := positiveSeedance25EnvInt(getenv, Seedance25IdempotencyTTLEnv, int(policy.IdempotencyTTL/time.Second))
	if err != nil {
		return Seedance25AdmissionPolicy{}, err
	}
	policy.IdempotencyTTL = time.Duration(seconds) * time.Second
	if err := validateSeedance25AdmissionPolicy(policy); err != nil {
		return Seedance25AdmissionPolicy{}, err
	}
	return policy, nil
}

func positiveSeedance25EnvInt(getenv func(string) string, name string, fallback int) (int, error) {
	raw := strings.TrimSpace(getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0, &Seedance25AdmissionError{Code: Seedance25AdmissionInvalidPolicy, Message: name + " must be a positive integer"}
	}
	return value, nil
}

type Seedance25AdmissionSnapshot struct {
	WindowStartedAt time.Time `json:"window_started_at"`
	WindowEndsAt    time.Time `json:"window_ends_at"`
	Concurrent      int       `json:"concurrent"`
	RequestsUsed    int       `json:"requests_used"`
}

type Seedance25AdmissionError struct{ Code, Message string }

func (e *Seedance25AdmissionError) Error() string {
	if e == nil {
		return ""
	}
	return e.Code + ": " + e.Message
}

type seedance25AdmissionRecord struct {
	fingerprint    string
	providerTaskID string
	createdAt      time.Time
	completedAt    time.Time
	localExecuting bool
	remoteActive   bool
}

// Seedance25AdmissionLease is process-local. It holds no request payload,
// signed URL, access key, or provider response. ResumeProviderTaskID is used
// only to avoid submitting a second Ark task for an idempotent retry.
type Seedance25AdmissionLease struct {
	gate                 *Seedance25AdmissionGate
	recordKey            string
	ResumeProviderTaskID string
	Replayed             bool
	finished             bool
}

type Seedance25AdmissionGate struct {
	policy Seedance25AdmissionPolicy
	now    func() time.Time

	mu              sync.Mutex
	windowStartedAt time.Time
	requestsUsed    int
	concurrent      int
	records         map[string]*seedance25AdmissionRecord
}

func NewSeedance25AdmissionGate(policy Seedance25AdmissionPolicy, now func() time.Time) (*Seedance25AdmissionGate, error) {
	if err := validateSeedance25AdmissionPolicy(policy); err != nil {
		return nil, err
	}
	if now == nil {
		now = time.Now
	}
	started := now().UTC()
	return &Seedance25AdmissionGate{policy: policy, now: now, windowStartedAt: started, records: map[string]*seedance25AdmissionRecord{}}, nil
}

func validateSeedance25AdmissionPolicy(policy Seedance25AdmissionPolicy) error {
	invalid := func(message string) error {
		return &Seedance25AdmissionError{Code: Seedance25AdmissionInvalidPolicy, Message: message}
	}
	if policy.MaxConcurrent <= 0 {
		return invalid("max_concurrent must be greater than zero")
	}
	if policy.Window <= 0 {
		return invalid("window must be greater than zero")
	}
	if policy.MaxRequestsPerWindow <= 0 {
		return invalid("max_requests_per_window must be greater than zero")
	}
	if policy.IdempotencyTTL <= 0 {
		return invalid("idempotency_ttl must be greater than zero")
	}
	return nil
}

func (g *Seedance25AdmissionGate) Acquire(scope, idempotencyKey string, request ContentGenerationTaskRequest) (*Seedance25AdmissionLease, error) {
	if g == nil {
		return nil, &Seedance25AdmissionError{Code: Seedance25AdmissionInvalidPolicy, Message: "Seedance 2.5 admission gate is not initialized"}
	}
	scope, idempotencyKey = strings.TrimSpace(scope), strings.TrimSpace(idempotencyKey)
	if scope == "" || idempotencyKey == "" {
		code := Seedance25AdmissionInvalidRequest
		if idempotencyKey == "" {
			code = Seedance25AdmissionIdempotencyRequired
		}
		return nil, &Seedance25AdmissionError{Code: code, Message: "scope and idempotency_key are required"}
	}
	if len(idempotencyKey) > 160 || strings.ContainsAny(scope, "\x00\r\n") || strings.ContainsAny(idempotencyKey, "\x00\r\n") {
		return nil, &Seedance25AdmissionError{Code: Seedance25AdmissionInvalidRequest, Message: "scope or idempotency_key is invalid"}
	}
	fingerprint, err := seedance25AdmissionFingerprint(request)
	if err != nil {
		return nil, &Seedance25AdmissionError{Code: Seedance25AdmissionInvalidRequest, Message: err.Error()}
	}
	key := scope + "\x00" + idempotencyKey
	now := g.now().UTC()
	g.mu.Lock()
	defer g.mu.Unlock()
	g.advanceWindowLocked(now)
	g.purgeLocked(now)
	if existing := g.records[key]; existing != nil {
		if existing.fingerprint != fingerprint {
			return nil, &Seedance25AdmissionError{Code: Seedance25AdmissionIdempotencyConflict, Message: "idempotency_key was already used for a different Seedance request"}
		}
		if existing.localExecuting {
			return nil, &Seedance25AdmissionError{Code: Seedance25AdmissionIdempotencyInFlight, Message: "an identical Seedance request is already executing locally"}
		}
		existing.localExecuting = true
		return &Seedance25AdmissionLease{gate: g, recordKey: key, ResumeProviderTaskID: existing.providerTaskID, Replayed: existing.providerTaskID != ""}, nil
	}
	if g.concurrent >= g.policy.MaxConcurrent {
		return nil, &Seedance25AdmissionError{Code: Seedance25AdmissionConcurrentLimit, Message: "Seedance 2.5 concurrent task limit reached"}
	}
	if g.requestsUsed >= g.policy.MaxRequestsPerWindow {
		return nil, &Seedance25AdmissionError{Code: Seedance25AdmissionRequestQuota, Message: "Seedance 2.5 task creation quota reached for the current window"}
	}
	g.concurrent++
	g.requestsUsed++
	g.records[key] = &seedance25AdmissionRecord{fingerprint: fingerprint, createdAt: now, localExecuting: true, remoteActive: true}
	return &Seedance25AdmissionLease{gate: g, recordKey: key}, nil
}

func (l *Seedance25AdmissionLease) BindProviderTask(taskID string) {
	if l == nil || l.gate == nil || strings.TrimSpace(taskID) == "" {
		return
	}
	l.gate.mu.Lock()
	defer l.gate.mu.Unlock()
	if record := l.gate.records[l.recordKey]; record != nil {
		record.providerTaskID = strings.TrimSpace(taskID)
	}
}

// Finish keeps a non-terminal remote task reserved. A later retry using the
// same scope/key resumes GET polling without consuming another create-task
// quota or starting a duplicate model task.
func (l *Seedance25AdmissionLease) Finish(terminal bool) {
	if l == nil || l.gate == nil || l.finished {
		return
	}
	l.finished = true
	now := l.gate.now().UTC()
	l.gate.mu.Lock()
	defer l.gate.mu.Unlock()
	if record := l.gate.records[l.recordKey]; record != nil {
		record.localExecuting = false
		if terminal {
			record.remoteActive = false
			record.completedAt = now
			if l.gate.concurrent > 0 {
				l.gate.concurrent--
			}
		}
	}
}

func (l *Seedance25AdmissionLease) Abort() {
	if l == nil || l.gate == nil || l.finished {
		return
	}
	l.finished = true
	l.gate.mu.Lock()
	defer l.gate.mu.Unlock()
	if _, ok := l.gate.records[l.recordKey]; ok {
		delete(l.gate.records, l.recordKey)
		if l.gate.concurrent > 0 {
			l.gate.concurrent--
		}
	}
}

func (g *Seedance25AdmissionGate) Snapshot() Seedance25AdmissionSnapshot {
	if g == nil {
		return Seedance25AdmissionSnapshot{}
	}
	now := g.now().UTC()
	g.mu.Lock()
	defer g.mu.Unlock()
	g.advanceWindowLocked(now)
	g.purgeLocked(now)
	return Seedance25AdmissionSnapshot{WindowStartedAt: g.windowStartedAt, WindowEndsAt: g.windowStartedAt.Add(g.policy.Window), Concurrent: g.concurrent, RequestsUsed: g.requestsUsed}
}

func (g *Seedance25AdmissionGate) advanceWindowLocked(now time.Time) {
	if !now.Before(g.windowStartedAt.Add(g.policy.Window)) {
		g.windowStartedAt, g.requestsUsed = now, 0
	}
}

func (g *Seedance25AdmissionGate) purgeLocked(now time.Time) {
	for key, record := range g.records {
		if record == nil || record.localExecuting || record.remoteActive {
			continue
		}
		reference := record.completedAt
		if reference.IsZero() {
			reference = record.createdAt
		}
		if !now.Before(reference.Add(g.policy.IdempotencyTTL)) {
			delete(g.records, key)
		}
	}
}

func seedance25AdmissionFingerprint(request ContentGenerationTaskRequest) (string, error) {
	if request.Model != Seedance25ServerModel {
		return "", errors.New("Seedance admission requires the registered Seedance 2.5 model")
	}
	data, err := json.Marshal(request)
	if err != nil {
		return "", fmt.Errorf("cannot fingerprint Seedance request: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
