package xray

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseStatName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantOK  bool
		wantEm  string
		wantDir string
	}{
		{"uplink", "user>>>uuid-111@xray.com>>>traffic>>>uplink", true, "uuid-111@xray.com", "uplink"},
		{"downlink", "user>>>uuid-111@xray.com>>>traffic>>>downlink", true, "uuid-111@xray.com", "downlink"},
		{"inbound prefix", "inbound>>>vless-in>>>traffic>>>downlink", false, "", ""},
		{"empty", "", false, "", ""},
		{"too few segments", "user>>>uuid@xray.com>>>traffic", false, "", ""},
		{"too many segments", "user>>>uuid@xray.com>>>traffic>>>uplink>>>extra", false, "", ""},
		{"wrong middle word", "user>>>uuid@xray.com>>>upload>>>uplink", false, "", ""},
		{"unknown direction", "user>>>uuid@xray.com>>>traffic>>>sideways", false, "", ""},
		{"uppercase prefix", "USER>>>uuid@xray.com>>>traffic>>>uplink", false, "", ""},
		{"no separators", "no-separators-at-all", false, "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			email, dir, ok := parseStatName(tc.input)
			assert.Equal(t, tc.wantOK, ok)
			assert.Equal(t, tc.wantEm, email)
			assert.Equal(t, tc.wantDir, dir)
		})
	}
}
