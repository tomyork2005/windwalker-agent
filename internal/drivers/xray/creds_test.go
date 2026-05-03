package xray

import (
	"net/url"
	"testing"

	"agent/internal/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validCfg() config.XrayConfig {
	return config.XrayConfig{
		Flow:        "xtls-rprx-vision",
		PublicHost:  "node-1.example.com",
		Port:        443,
		SNI:         "www.microsoft.com",
		PublicKey:   "pubkey-xyz",
		ShortID:     "deadbeefcafe0000",
		Fingerprint: "chrome",
	}
}

func driverWith(cfg config.XrayConfig) *Driver {
	return &Driver{cfg: cfg}
}

// TestBuildCreds_Success covers happy paths: golden URI, defaults, and the
// flow=...-omitted-when-empty case. Each row asserts the parsed URI structure.
func TestBuildCreds_Success(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*config.XrayConfig)
		userID string
		assert func(t *testing.T, u *url.URL)
	}{
		{
			name:   "golden uri",
			mutate: func(*config.XrayConfig) {},
			userID: "uuid-111",
			assert: func(t *testing.T, u *url.URL) {
				assert.Equal(t, "vless", u.Scheme)
				assert.Equal(t, "uuid-111", u.User.Username())
				assert.Equal(t, "node-1.example.com:443", u.Host)
				assert.Equal(t, "", u.Fragment)
				q := u.Query()
				assert.Equal(t, "none", q.Get("encryption"))
				assert.Equal(t, "xtls-rprx-vision", q.Get("flow"))
				assert.Equal(t, "reality", q.Get("security"))
				assert.Equal(t, "www.microsoft.com", q.Get("sni"))
				assert.Equal(t, "chrome", q.Get("fp"))
				assert.Equal(t, "pubkey-xyz", q.Get("pbk"))
				assert.Equal(t, "deadbeefcafe0000", q.Get("sid"))
				assert.Equal(t, "tcp", q.Get("type"))
			},
		},
		{
			name:   "default port when zero",
			mutate: func(c *config.XrayConfig) { c.Port = 0 },
			userID: "uuid-1",
			assert: func(t *testing.T, u *url.URL) {
				assert.Equal(t, "node-1.example.com:443", u.Host)
			},
		},
		{
			name:   "default fingerprint when empty",
			mutate: func(c *config.XrayConfig) { c.Fingerprint = "" },
			userID: "uuid-1",
			assert: func(t *testing.T, u *url.URL) {
				assert.Equal(t, "chrome", u.Query().Get("fp"))
			},
		},
		{
			name:   "flow omitted when empty",
			mutate: func(c *config.XrayConfig) { c.Flow = "" },
			userID: "uuid-1",
			assert: func(t *testing.T, u *url.URL) {
				assert.False(t, u.Query().Has("flow"))
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validCfg()
			tc.mutate(&cfg)
			d := driverWith(cfg)

			got, err := d.BuildCreds(tc.userID)
			require.NoError(t, err)

			u, err := url.Parse(got)
			require.NoError(t, err)
			tc.assert(t, u)
		})
	}
}

func TestBuildCreds_Errors(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*config.XrayConfig)
		userID  string
		wantErr string
	}{
		{"empty user id", func(*config.XrayConfig) {}, "", "user_id"},
		{"missing public_host", func(c *config.XrayConfig) { c.PublicHost = "" }, "uuid-1", "public_host"},
		{"missing sni", func(c *config.XrayConfig) { c.SNI = "" }, "uuid-1", "sni"},
		{"missing public_key", func(c *config.XrayConfig) { c.PublicKey = "" }, "uuid-1", "public_key"},
		{"missing short_id", func(c *config.XrayConfig) { c.ShortID = "" }, "uuid-1", "short_id"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validCfg()
			tc.mutate(&cfg)
			d := driverWith(cfg)

			_, err := d.BuildCreds(tc.userID)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}
