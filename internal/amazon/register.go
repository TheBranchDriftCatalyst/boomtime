package amazon

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// register.go — the "Connect Amazon" device-registration flow that yields a
// DeviceCredential, ported from mkb79/Audible (login.py + register.py +
// localization.py). ONE registration authenticates BOTH Audible and Kindle.
//
// The web UX is a paste-the-URL flow (Amazon redirects to its own maplanding
// URL after login, not to us):
//
//	1. BuildAuthorizeURL(mk) → the Amazon /ap/signin URL + a RegistrationSession
//	   (PKCE verifier + device serial). The handler seals the session (encrypted)
//	   into an opaque token the client echoes back.
//	2. User opens the URL, logs into Amazon (captcha/OTP/2FA), and lands on
//	   https://www.amazon.<tld>/ap/maplanding?...&openid.oa2.authorization_code=...
//	3. User pastes that maplanding URL back → CompleteRegistration exchanges the
//	   code at /auth/register for the adp_token + RSA device key + refresh token.
//
// ImportAuthFile is the alternative for someone who already ran `audible
// quickstart` and has a .audible file. Values are Amazon app constants and may
// drift with the upstream library — pinned here.
const (
	deviceType      = "A2CZJZGLK2JJVM" // Audible-iOS device_type
	appName         = "Audible"
	appVersion      = "3.56.2"
	softwareVersion = "35602678"
	deviceModel     = "iPhone"
	osVersion       = "15.0.0"
)

// httpClient bounds the register/token calls to Amazon.
var httpClient = &http.Client{Timeout: 30 * time.Second}

// mkInfo is a marketplace's Amazon TLD + ids (mkb79 LOCALE_TEMPLATES).
type mkInfo struct {
	countryCode   string
	domain        string // Amazon TLD suffix, e.g. "com", "co.uk"
	marketPlaceID string
}

var marketplaces = map[Marketplace]mkInfo{
	MarketplaceUS: {"us", "com", "AF2M0KC94RCEA"},
	MarketplaceUK: {"uk", "co.uk", "A2I9A3Q2GNFNGQ"},
	MarketplaceDE: {"de", "de", "AN7V1F1VY261K"},
	MarketplaceCA: {"ca", "ca", "A2CQZ5RBY40XE"},
	MarketplaceAU: {"au", "com.au", "AN7EY7DTAW63G"},
	"fr":          {"fr", "fr", "A2728XDNODOQ8T"},
	"it":          {"it", "it", "A2N7FU2W2BU2ZC"},
	"in":          {"in", "in", "AJO3FBRUE6J4S"},
	"jp":          {"jp", "co.jp", "A1QAP3MOU4173J"},
	"es":          {"es", "es", "ALMIKO4SZCSAR"},
	"br":          {"br", "com.br", "A10J1VAYUDTYRN"},
}

// RegistrationSession is the server-held state between BuildAuthorizeURL and
// CompleteRegistration (PKCE verifier + serial + marketplace).
type RegistrationSession struct {
	CodeVerifier string      `json:"code_verifier"`
	DeviceSerial string      `json:"device_serial"`
	Marketplace  Marketplace `json:"marketplace"`
}

// BuildAuthorizeURL generates PKCE + a device serial and returns the Amazon
// authorize URL the user opens to log in, plus the session to persist.
func BuildAuthorizeURL(mk Marketplace) (string, RegistrationSession, error) {
	if mk == "" {
		mk = MarketplaceUS
	}
	info, ok := marketplaces[mk]
	if !ok {
		return "", RegistrationSession{}, fmt.Errorf("amazon: unknown marketplace %q", mk)
	}
	verifier, challenge, err := pkce()
	if err != nil {
		return "", RegistrationSession{}, err
	}
	serial := deviceSerial()
	clientID := hex.EncodeToString([]byte(serial + "#" + deviceType))

	q := url.Values{}
	q.Set("openid.oa2.response_type", "code")
	q.Set("openid.oa2.code_challenge_method", "S256")
	q.Set("openid.oa2.code_challenge", challenge)
	q.Set("openid.return_to", "https://www.amazon."+info.domain+"/ap/maplanding")
	q.Set("openid.assoc_handle", "amzn_audible_ios_"+info.countryCode)
	q.Set("openid.oa2.client_id", "device:"+clientID)
	q.Set("openid.oa2.scope", "device_auth_access")
	q.Set("pageId", "amzn_audible_ios")
	q.Set("marketPlaceId", info.marketPlaceID)
	q.Set("openid.mode", "checkid_setup")
	q.Set("openid.ns", "http://specs.openid.net/auth/2.0")
	q.Set("openid.claimed_id", "http://specs.openid.net/auth/2.0/identifier_select")
	q.Set("openid.identity", "http://specs.openid.net/auth/2.0/identifier_select")
	q.Set("openid.ns.oa2", "http://www.amazon.com/ap/ext/oauth/2")
	q.Set("openid.ns.pape", "http://specs.openid.net/extensions/pape/1.0")
	q.Set("openid.pape.max_auth_age", "0")
	q.Set("forceMobileLayout", "true")

	authURL := "https://www.amazon." + info.domain + "/ap/signin?" + q.Encode()
	return authURL, RegistrationSession{CodeVerifier: verifier, DeviceSerial: serial, Marketplace: mk}, nil
}

// CompleteRegistration extracts the authorization_code from the pasted-back
// maplanding URL and exchanges it (with the PKCE verifier + serial) at Amazon's
// /auth/register endpoint for the device credential.
func CompleteRegistration(ctx context.Context, sess RegistrationSession, redirectURL string, now time.Time) (DeviceCredential, error) {
	code, err := extractAuthCode(redirectURL)
	if err != nil {
		return DeviceCredential{}, err
	}
	info, ok := marketplaces[sess.Marketplace]
	if !ok {
		return DeviceCredential{}, fmt.Errorf("amazon: unknown marketplace %q", sess.Marketplace)
	}
	clientID := hex.EncodeToString([]byte(sess.DeviceSerial + "#" + deviceType))

	body := map[string]any{
		"requested_token_type": []string{"bearer", "mac_dms", "website_cookies", "store_authentication_cookie"},
		"cookies": map[string]any{
			"website_cookies": []any{},
			"domain":          ".amazon." + info.domain,
		},
		"registration_data": map[string]any{
			"domain":           "Device",
			"app_version":      appVersion,
			"device_serial":    sess.DeviceSerial,
			"device_type":      deviceType,
			"device_name":      "%FIRST_NAME%%FIRST_NAME_POSSESSIVE_STRING%%DUPE_STRATEGY_1ST%Audible for iPhone",
			"os_version":       osVersion,
			"software_version": softwareVersion,
			"device_model":     deviceModel,
			"app_name":         appName,
		},
		"auth_data": map[string]any{
			"client_id":          clientID,
			"authorization_code": code,
			"code_verifier":      sess.CodeVerifier,
			"code_algorithm":     "SHA-256",
			"client_domain":      "DeviceLegacy",
		},
		"requested_extensions": []string{"device_info", "customer_info"},
	}
	buf, _ := json.Marshal(body)
	endpoint := "https://api.amazon." + info.domain + "/auth/register"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(buf))
	if err != nil {
		return DeviceCredential{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return DeviceCredential{}, fmt.Errorf("amazon: /auth/register request failed: %w", err)
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	var parsed struct {
		Response struct {
			Success struct {
				Tokens struct {
					MacDMS struct {
						AdpToken         string `json:"adp_token"`
						DevicePrivateKey string `json:"device_private_key"`
					} `json:"mac_dms"`
					Bearer struct {
						AccessToken  string `json:"access_token"`
						RefreshToken string `json:"refresh_token"`
					} `json:"bearer"`
				} `json:"tokens"`
				Extensions struct {
					DeviceInfo struct {
						DeviceSerialNumber string `json:"device_serial_number"`
					} `json:"device_info"`
					CustomerInfo struct {
						UserID string `json:"user_id"`
					} `json:"customer_info"`
				} `json:"extensions"`
			} `json:"success"`
		} `json:"response"`
	}
	if err := json.Unmarshal(rb, &parsed); err != nil {
		return DeviceCredential{}, fmt.Errorf("amazon: parse /auth/register response (status %d): %w", resp.StatusCode, err)
	}
	s := parsed.Response.Success
	if s.Tokens.MacDMS.AdpToken == "" || s.Tokens.MacDMS.DevicePrivateKey == "" {
		return DeviceCredential{}, fmt.Errorf("amazon: /auth/register returned no device tokens (status %d) — the authorization_code may be expired/used; re-run Connect and paste a fresh maplanding URL", resp.StatusCode)
	}
	serial := s.Extensions.DeviceInfo.DeviceSerialNumber
	if serial == "" {
		serial = sess.DeviceSerial
	}
	return DeviceCredential{
		AdpToken:         s.Tokens.MacDMS.AdpToken,
		DevicePrivateKey: s.Tokens.MacDMS.DevicePrivateKey,
		RefreshToken:     s.Tokens.Bearer.RefreshToken,
		Marketplace:      sess.Marketplace,
		DeviceSerial:     serial,
		CustomerID:       s.Extensions.CustomerInfo.UserID,
		RegisteredAt:     now.UTC(),
	}, nil
}

// pkce returns (code_verifier, code_challenge). Matches mkb79: verifier =
// base64url(32 random bytes); challenge = base64url(SHA-256(verifier-string)).
func pkce() (verifier, challenge string, err error) {
	raw := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		return "", "", err
	}
	verifier = base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	return verifier, challenge, nil
}

// deviceSerial returns 32 uppercase hex chars (mkb79: uuid4().hex.upper()).
func deviceSerial() string {
	b := make([]byte, 16)
	_, _ = io.ReadFull(rand.Reader, b)
	return strings.ToUpper(hex.EncodeToString(b))
}

// extractAuthCode pulls openid.oa2.authorization_code from the maplanding URL.
func extractAuthCode(redirectURL string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(redirectURL))
	if err != nil {
		return "", fmt.Errorf("amazon: parse redirect URL: %w", err)
	}
	code := u.Query().Get("openid.oa2.authorization_code")
	if code == "" {
		return "", errors.New("amazon: redirect URL has no openid.oa2.authorization_code — paste the FULL .../ap/maplanding URL you landed on after logging in")
	}
	return code, nil
}

// audibleAuthFile is the subset of a mkb79 `.audible` auth-file we read.
type audibleAuthFile struct {
	AdpToken         string `json:"adp_token"`
	DevicePrivateKey string `json:"device_private_key"`
	RefreshToken     string `json:"refresh_token"`
	Locale           string `json:"locale"`
	DeviceInfo       struct {
		DeviceSerialNumber string `json:"device_serial_number"`
	} `json:"device_info"`
	CustomerInfo struct {
		UserID string `json:"user_id"`
	} `json:"customer_info"`
}

// ImportAuthFile parses a mkb79 `.audible` auth-file (JSON) into a
// DeviceCredential (the one-time-CLI-registration path). now stamps RegisteredAt.
func ImportAuthFile(blob []byte, now time.Time) (DeviceCredential, error) {
	var f audibleAuthFile
	if err := json.Unmarshal(blob, &f); err != nil {
		return DeviceCredential{}, fmt.Errorf("amazon: parse .audible auth file: %w", err)
	}
	if f.AdpToken == "" || f.DevicePrivateKey == "" {
		return DeviceCredential{}, errors.New("amazon: auth file missing adp_token / device_private_key")
	}
	mk := Marketplace(f.Locale)
	if _, ok := marketplaces[mk]; !ok {
		mk = MarketplaceUS
	}
	return DeviceCredential{
		AdpToken:         f.AdpToken,
		DevicePrivateKey: f.DevicePrivateKey,
		RefreshToken:     f.RefreshToken,
		Marketplace:      mk,
		DeviceSerial:     f.DeviceInfo.DeviceSerialNumber,
		CustomerID:       f.CustomerInfo.UserID,
		RegisteredAt:     now.UTC(),
	}, nil
}
