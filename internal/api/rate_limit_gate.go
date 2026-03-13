package api

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	apperrors "noovertime/internal/errors"
)

const (
	rateLimitBlockedCode    = "RATE_LIMIT_BLOCKED"
	rateLimitBlockedMessage = "too many attempts"

	rateLimitSceneMigrationRequest = "MIGRATION_REQUEST"
	rateLimitSceneMigrationConfirm = "MIGRATION_CONFIRM"
	rateLimitSceneRecoveryVerify   = "RECOVERY_VERIFY"
	rateLimitScenePairingReset     = "PAIRING_RESET"
	rateLimitSceneWebPairBind      = "WEB_PAIR_BIND"
	rateLimitSceneWebReadQuery     = "WEB_READ_QUERY"
)

type migrationRateLimitPolicy struct {
	Window             time.Duration
	FineLimit          int
	SubjectGlobalLimit int
	ClientGlobalLimit  int
	FineBlock          time.Duration
	GlobalBlock        time.Duration
}

type rateLimitCounter struct {
	Attempts     []time.Time
	BlockedUntil time.Time
}

type migrationRateGate struct {
	mu       sync.Mutex
	now      func() time.Time
	policies map[string]migrationRateLimitPolicy
	counters map[string]*rateLimitCounter
}

type rateLimitEntry struct {
	Key      string
	Limit    int
	BlockFor time.Duration
}

func newMigrationRateGate() *migrationRateGate {
	return &migrationRateGate{
		now: time.Now,
		policies: map[string]migrationRateLimitPolicy{
			rateLimitSceneMigrationRequest: {
				Window:             10 * time.Minute,
				FineLimit:          5,
				SubjectGlobalLimit: 12,
				ClientGlobalLimit:  12,
				FineBlock:          30 * time.Minute,
				GlobalBlock:        2 * time.Hour,
			},
			rateLimitSceneMigrationConfirm: {
				Window:             10 * time.Minute,
				FineLimit:          6,
				SubjectGlobalLimit: 15,
				ClientGlobalLimit:  15,
				FineBlock:          30 * time.Minute,
				GlobalBlock:        2 * time.Hour,
			},
			rateLimitSceneRecoveryVerify: {
				Window:             24 * time.Hour,
				FineLimit:          3,
				SubjectGlobalLimit: 5,
				ClientGlobalLimit:  5,
				FineBlock:          72 * time.Hour,
				GlobalBlock:        7 * 24 * time.Hour,
			},
			rateLimitScenePairingReset: {
				Window:             24 * time.Hour,
				FineLimit:          3,
				SubjectGlobalLimit: 5,
				ClientGlobalLimit:  5,
				FineBlock:          24 * time.Hour,
				GlobalBlock:        72 * time.Hour,
			},
			rateLimitSceneWebPairBind: {
				Window:             10 * time.Minute,
				FineLimit:          5,
				SubjectGlobalLimit: 15,
				ClientGlobalLimit:  15,
				FineBlock:          30 * time.Minute,
				GlobalBlock:        2 * time.Hour,
			},
			rateLimitSceneWebReadQuery: {
				Window:             10 * time.Minute,
				FineLimit:          5,
				SubjectGlobalLimit: 15,
				ClientGlobalLimit:  15,
				FineBlock:          30 * time.Minute,
				GlobalBlock:        2 * time.Hour,
			},
		},
		counters: make(map[string]*rateLimitCounter),
	}
}

func (g *migrationRateGate) Check(scene, subjectHash, clientFingerprintHash string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	policy, ok := g.policies[scene]
	if !ok {
		return apperrors.New(http.StatusInternalServerError, internalErrorCode, internalErrorMessage)
	}
	now := g.now().UTC()

	subject := normalizeRateLimitKeyPart(subjectHash, "SUBJECT")
	fingerprint := normalizeRateLimitKeyPart(clientFingerprintHash, "FINGERPRINT")
	entries := []rateLimitEntry{
		{
			Key:      fmt.Sprintf("%s|%s|%s", scene, subject, fingerprint),
			Limit:    policy.FineLimit,
			BlockFor: policy.FineBlock,
		},
		{
			Key:      fmt.Sprintf("%s|%s|GLOBAL", scene, subject),
			Limit:    policy.SubjectGlobalLimit,
			BlockFor: policy.GlobalBlock,
		},
		{
			Key:      fmt.Sprintf("%s|GLOBAL|%s", scene, fingerprint),
			Limit:    policy.ClientGlobalLimit,
			BlockFor: policy.GlobalBlock,
		},
	}

	for _, entry := range entries {
		counter := g.counter(entry.Key)
		counter.prune(now, policy.Window)
		if counter.BlockedUntil.After(now) {
			return apperrors.New(http.StatusTooManyRequests, rateLimitBlockedCode, rateLimitBlockedMessage)
		}
	}

	for _, entry := range entries {
		counter := g.counter(entry.Key)
		counter.Attempts = append(counter.Attempts, now)
		if len(counter.Attempts) > entry.Limit {
			blockedUntil := now.Add(entry.BlockFor)
			if blockedUntil.After(counter.BlockedUntil) {
				counter.BlockedUntil = blockedUntil
			}
		}
	}

	for _, entry := range entries {
		counter := g.counter(entry.Key)
		if counter.BlockedUntil.After(now) {
			return apperrors.New(http.StatusTooManyRequests, rateLimitBlockedCode, rateLimitBlockedMessage)
		}
	}

	return nil
}

func (g *migrationRateGate) counter(key string) *rateLimitCounter {
	existing, ok := g.counters[key]
	if ok {
		return existing
	}
	created := &rateLimitCounter{}
	g.counters[key] = created
	return created
}

func (c *rateLimitCounter) prune(now time.Time, window time.Duration) {
	if len(c.Attempts) == 0 {
		return
	}
	cutoff := now.Add(-window)
	filtered := c.Attempts[:0]
	for _, attemptAt := range c.Attempts {
		if !attemptAt.Before(cutoff) {
			filtered = append(filtered, attemptAt)
		}
	}
	c.Attempts = filtered
}

func normalizeRateLimitKeyPart(value, fallback string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fallback
	}
	return trimmed
}

func (s *Server) checkMigrationRequestRateLimit(subjectHash, clientFingerprintHash string) error {
	return s.migrationRateGate.Check(rateLimitSceneMigrationRequest, subjectHash, clientFingerprintHash)
}

func (s *Server) checkMigrationConfirmRateLimit(subjectHash, clientFingerprintHash string) error {
	return s.migrationRateGate.Check(rateLimitSceneMigrationConfirm, subjectHash, clientFingerprintHash)
}

func (s *Server) checkRecoveryVerifyRateLimit(subjectHash, clientFingerprintHash string) error {
	return s.migrationRateGate.Check(rateLimitSceneRecoveryVerify, subjectHash, clientFingerprintHash)
}

func (s *Server) checkPairingResetRateLimit(subjectHash, clientFingerprintHash string) error {
	return s.migrationRateGate.Check(rateLimitScenePairingReset, subjectHash, clientFingerprintHash)
}

func (s *Server) checkWebPairBindRateLimit(subjectHash, clientFingerprintHash string) error {
	return s.migrationRateGate.Check(rateLimitSceneWebPairBind, subjectHash, clientFingerprintHash)
}

func (s *Server) checkWebReadQueryRateLimit(subjectHash, clientFingerprintHash string) error {
	return s.migrationRateGate.Check(rateLimitSceneWebReadQuery, subjectHash, clientFingerprintHash)
}
