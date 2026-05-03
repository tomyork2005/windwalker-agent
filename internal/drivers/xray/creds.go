package xray

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
)

const (
	credsNetwork  = "tcp"
	credsSecurity = "reality"
	credsEncoding = "none"

	defaultPort        uint32 = 443
	defaultFingerprint        = "chrome"
)

// BuildCreds returns a ready-to-paste vless://... URI for the given user UUID,
func (d *Driver) BuildCreds(userID string) (string, error) {
	if userID == "" {
		return "", errors.New("build creds: user_id is empty")
	}

	missing := make([]string, 0, 4)
	if d.cfg.PublicHost == "" {
		missing = append(missing, "public_host")
	}
	if d.cfg.SNI == "" {
		missing = append(missing, "sni")
	}
	if d.cfg.PublicKey == "" {
		missing = append(missing, "public_key")
	}
	if d.cfg.ShortID == "" {
		missing = append(missing, "short_id")
	}
	if len(missing) > 0 {
		return "", fmt.Errorf("build creds: missing xray config fields: %v", missing)
	}

	port := d.cfg.Port
	if port == 0 {
		port = defaultPort
	}
	fp := d.cfg.Fingerprint
	if fp == "" {
		fp = defaultFingerprint
	}

	q := url.Values{}
	q.Set("encryption", credsEncoding)
	if d.cfg.Flow != "" {
		q.Set("flow", d.cfg.Flow)
	}
	q.Set("security", credsSecurity)
	q.Set("sni", d.cfg.SNI)
	q.Set("fp", fp)
	q.Set("pbk", d.cfg.PublicKey)
	q.Set("sid", d.cfg.ShortID)
	q.Set("type", credsNetwork)

	u := url.URL{
		Scheme:   "vless",
		User:     url.User(userID),
		Host:     d.cfg.PublicHost + ":" + strconv.FormatUint(uint64(port), 10),
		RawQuery: q.Encode(),
	}
	return u.String(), nil
}
