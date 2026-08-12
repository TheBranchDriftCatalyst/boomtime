package amazon

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// register.go — the "Connect Amazon" device-registration flow that yields a
// DeviceCredential.
//
// The user authenticates to Amazon (username/password + captcha/OTP/2FA) via
// Amazon's OpenID Authorization-Code-with-PKCE flow, impersonating the Audible
// app's OAuth client (Amazon exposes NO third-party OAuth client for reader
// data — this is reverse-engineered, see book-tracking-research.md §3.1). The
// code is exchanged for an adp_token + RSA device key + refresh token.
//
// Two supported shapes:
//
//	A. Interactive (Register, below) — build the PKCE authorize URL, user
//	   completes Amazon login, we capture the redirect + register the device.
//	   This is the mkb79/Audible port and is the NEXT focused chunk: it is
//	   security-sensitive and MUST be verified live against a real Amazon login.
//	B. Import (ImportAuthFile) — user runs `audible quickstart` once and uploads
//	   the resulting .audible auth file; we parse it into a DeviceCredential.
//	   The pragmatic single-user path available today.

// ErrRegisterNotImplemented marks the interactive flow as the pending chunk.
var ErrRegisterNotImplemented = errors.New("amazon: interactive device registration not implemented yet — use ImportAuthFile, or see register.go")

// Register is the interactive Amazon device-registration flow (shape A). Pending
// the mkb79/Audible port + live verification.
func Register( /* ctx, marketplace, credentials/redirect */ ) (DeviceCredential, error) {
	return DeviceCredential{}, ErrRegisterNotImplemented
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
// DeviceCredential (shape B — the pragmatic one-time CLI-registration path).
// The caller then Store.Save's it (encrypted). now stamps RegisteredAt.
func ImportAuthFile(blob []byte, now time.Time) (DeviceCredential, error) {
	var f audibleAuthFile
	if err := json.Unmarshal(blob, &f); err != nil {
		return DeviceCredential{}, fmt.Errorf("amazon: parse .audible auth file: %w", err)
	}
	if f.AdpToken == "" || f.DevicePrivateKey == "" {
		return DeviceCredential{}, errors.New("amazon: auth file missing adp_token / device_private_key")
	}
	mk := Marketplace(f.Locale)
	if mk == "" {
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
