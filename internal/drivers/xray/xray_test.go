package xray

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEmailFor(t *testing.T) {
	tests := []struct {
		name   string
		userID string
		want   string
	}{
		{"short id", "abc-123", "abc-123@xray.com"},
		{"uuid", "66ad4540-b58c-4ad2-9926-ea63445a9b57", "66ad4540-b58c-4ad2-9926-ea63445a9b57@xray.com"},
		{"empty", "", "@xray.com"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, emailFor(tc.userID))
		})
	}
}

func TestUserIDFromEmail(t *testing.T) {
	tests := []struct {
		name   string
		email  string
		wantID string
		wantOK bool
	}{
		{"short id", "abc-123@xray.com", "abc-123", true},
		{"uuid", "66ad4540-b58c-4ad2-9926-ea63445a9b57@xray.com", "66ad4540-b58c-4ad2-9926-ea63445a9b57", true},
		{"only domain", "@xray.com", "", true},
		{"wrong domain", "abc-123@other.com", "", false},
		{"no at sign", "no-at-sign", "", false},
		{"empty", "", "", false},
		{"suffix in middle", "abc-123@xray.com.evil", "", false},
		{"only fake suffix", "@xray.com.evil", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotID, gotOK := userIDFromEmail(tc.email)
			assert.Equal(t, tc.wantOK, gotOK)
			assert.Equal(t, tc.wantID, gotID)
		})
	}
}
