package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	apperrors "noovertime/internal/errors"

	"github.com/jackc/pgx/v5"
)

const (
	membershipTierFree   = "FREE"
	membershipTierMember = "MEMBER"

	membershipRequiredCode = "MEMBERSHIP_REQUIRED"
)

type userMembership struct {
	Tier      string
	ExpiresAt *time.Time
}

func normalizeMembershipTier(raw string) string {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case membershipTierMember:
		return membershipTierMember
	default:
		return membershipTierFree
	}
}

func isMembershipActive(tier string, expiresAt *time.Time, now time.Time) bool {
	if normalizeMembershipTier(tier) != membershipTierMember {
		return false
	}
	if expiresAt == nil {
		return true
	}
	return !expiresAt.Before(now.UTC())
}

func membershipRequired() error {
	return apperrors.New(http.StatusForbidden, membershipRequiredCode, "membership is required for punch photo uploads")
}

func loadUserMembership(ctx context.Context, tx pgx.Tx, userID string) (userMembership, error) {
	if strings.TrimSpace(userID) == "" {
		return userMembership{Tier: membershipTierFree}, nil
	}

	const query = `
SELECT membership_tier, membership_expires_at
  FROM users
 WHERE user_id = $1::uuid
`

	var tier string
	var expiresAt *time.Time
	if err := tx.QueryRow(ctx, query, userID).Scan(&tier, &expiresAt); err != nil {
		return userMembership{}, err
	}

	return userMembership{
		Tier:      normalizeMembershipTier(tier),
		ExpiresAt: expiresAt,
	}, nil
}
