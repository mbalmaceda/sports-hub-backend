package teamlink_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/mbalmaceda/sports-hub-backend/internal/domain/teamlink"
)

func TestUsable(t *testing.T) {
	now := time.Now()
	base := func() *teamlink.Invite {
		return &teamlink.Invite{MaxUses: 30, UsedCount: 3, ExpiresAt: now.Add(time.Hour)}
	}

	assert.True(t, base().Usable(now))

	expired := base()
	expired.ExpiresAt = now
	assert.False(t, expired.Usable(now), "vence en el instante, no un segundo después")

	full := base()
	full.UsedCount = 30
	assert.False(t, full.Usable(now))
	assert.Equal(t, 0, full.RemainingUses())

	revoked := base()
	revoked.RevokedAt = &now
	assert.False(t, revoked.Usable(now))
}
