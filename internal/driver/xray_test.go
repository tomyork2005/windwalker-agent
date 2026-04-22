package driver

import (
	"agent/internal/config"
	"agent/internal/domain"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validXrayCfg() config.XrayConfig {
	return config.XrayConfig{
		Protocol:    "vless",
		VlessFlow:   "xtls-rprx-vision",
		PublicHost:  "node-1.example.com",
		Port:        443,
		SNI:         "www.microsoft.com",
		PublicKey:   "pubkey-xyz",
		ShortID:     "deadbeefcafe0000",
		Fingerprint: "chrome",
	}
}

func TestXrayDriver_BuildCreds_Success(t *testing.T) {
	d := NewXrayDriver(validXrayCfg())
	user := domain.User{ID: "uuid-111", AccountID: "acc-42", DriverType: "xray"}

	creds, err := d.BuildCreds(user)
	require.NoError(t, err)
	require.NotNil(t, creds)
	assert.Equal(t, "uuid-111", creds.GetUserId())
	assert.Equal(t, "xray", creds.GetDriverType())

	vless := creds.GetVless()
	require.NotNil(t, vless, "expected Vless oneof to be set")
	assert.Equal(t, "uuid-111", vless.GetUuid())
	assert.Equal(t, "node-1.example.com", vless.GetHost())
	assert.Equal(t, uint32(443), vless.GetPort())
	assert.Equal(t, "reality", vless.GetSecurity())
	assert.Equal(t, "www.microsoft.com", vless.GetSni())
	assert.Equal(t, "tcp", vless.GetNetwork())
	assert.Equal(t, "xtls-rprx-vision", vless.GetFlow())

	uri, err := url.Parse(vless.GetUri())
	require.NoError(t, err, "URI must be parseable")
	assert.Equal(t, "vless", uri.Scheme)
	assert.Equal(t, "uuid-111", uri.User.Username())
	assert.Equal(t, "node-1.example.com:443", uri.Host)
	assert.Equal(t, "acc-42", uri.Fragment)

	q := uri.Query()
	assert.Equal(t, "none", q.Get("encryption"))
	assert.Equal(t, "xtls-rprx-vision", q.Get("flow"))
	assert.Equal(t, "reality", q.Get("security"))
	assert.Equal(t, "www.microsoft.com", q.Get("sni"))
	assert.Equal(t, "chrome", q.Get("fp"))
	assert.Equal(t, "pubkey-xyz", q.Get("pbk"))
	assert.Equal(t, "deadbeefcafe0000", q.Get("sid"))
	assert.Equal(t, "tcp", q.Get("type"))
}

func TestXrayDriver_BuildCreds_MissingFields(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*config.XrayConfig)
		wantErr string
	}{
		{"public_host", func(c *config.XrayConfig) { c.PublicHost = "" }, "public_host"},
		{"sni", func(c *config.XrayConfig) { c.SNI = "" }, "sni"},
		{"public_key", func(c *config.XrayConfig) { c.PublicKey = "" }, "public_key"},
		{"short_id", func(c *config.XrayConfig) { c.ShortID = "" }, "short_id"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validXrayCfg()
			tc.mutate(&cfg)
			d := NewXrayDriver(cfg)

			_, err := d.BuildCreds(domain.User{ID: "uuid-1", DriverType: "xray"})
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestXrayDriver_BuildCreds_EmptyUserID(t *testing.T) {
	d := NewXrayDriver(validXrayCfg())
	_, err := d.BuildCreds(domain.User{DriverType: "xray"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "user id")
}

func TestXrayDriver_BuildCreds_UnsupportedProtocol(t *testing.T) {
	cfg := validXrayCfg()
	cfg.Protocol = "vmess"
	d := NewXrayDriver(cfg)

	_, err := d.BuildCreds(domain.User{ID: "uuid-1", DriverType: "xray"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported protocol")
}

func TestXrayDriver_BuildCreds_DefaultsApplied(t *testing.T) {
	cfg := validXrayCfg()
	cfg.Port = 0
	cfg.Fingerprint = ""
	d := NewXrayDriver(cfg)

	creds, err := d.BuildCreds(domain.User{ID: "uuid-1", DriverType: "xray"})
	require.NoError(t, err)
	assert.Equal(t, uint32(443), creds.GetVless().GetPort())
	assert.True(t, strings.Contains(creds.GetVless().GetUri(), "fp=chrome"))
}
