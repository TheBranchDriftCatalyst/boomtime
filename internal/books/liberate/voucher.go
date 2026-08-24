// Package liberate is the catalyst-books LIBERATION domain (boom-w20s): the
// Libation (getlibation.com) rebuild. It turns an owned Audible title into a
// DRM-free, chaptered, tagged M4B on a filesystem library that Audiobookshelf /
// Plex / Jellyfin can scan.
//
// It is the Go equivalent of Libation's FileLiberator + AAXClean + FileManager
// projects. Libation's fourth project — AudibleApi (device registration, ADP
// request signing, the library sweep) — ALREADY EXISTS on this tree as
// internal/books/connect/amazon + internal/books/ingest/audible, so this package
// starts from an authenticated device credential and does only the four steps
// that follow:
//
//  1. license.go  POST /1.0/content/{asin}/licenserequest  → a sealed voucher
//  2. voucher.go  AES-CBC unseal the voucher               → the AAXC key/iv
//  3. fetch.go    stream the AAXC down from the CDN
//  4. decrypt.go  strip DRM + remux to M4B (ffmpeg, -c copy)
//
// See docs/design/catalyst-books-liberation-architecture.md.
//
// SECRET HANDLING. The per-book content key is on the same footing as the
// device credential itself (CLAUDE.md → Encryption at Rest): it is used in
// memory and discarded, never persisted, never logged. ContentKey implements
// fmt.Stringer AND slog.LogValuer so that even an accidental `slog.Info("...",
// "key", k)` or `%v` prints "[redacted]" rather than the material. Do not add a
// field to it without extending those two methods.
package liberate

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/books/connect/amazon"
)

// ErrVoucherUndecryptable is returned when the sealed voucher does not yield
// parseable JSON under the attempted key derivation. In practice this means the
// concat ORDER is wrong (see KeyOrder) rather than that anything upstream
// changed — AES-CBC on a wrong key produces clean garbage, not an error, so this
// is the only signal we get.
var ErrVoucherUndecryptable = errors.New("liberate: voucher did not decrypt to JSON (wrong key derivation?)")

// ContentKey is the AAXC content key + IV recovered from the license voucher.
// It is a SECRET: see the package doc. Both String and LogValue redact.
type ContentKey struct {
	Key []byte
	IV  []byte
}

// String redacts. This is what stops `fmt.Sprintf("%v", key)` leaking material
// into an error string or a log line.
func (ContentKey) String() string { return "ContentKey[redacted]" }

// LogValue redacts under slog, including when the ContentKey is nested inside a
// struct that gets logged. Reports only whether the material is present/sized,
// which is all an operator ever needs from a log.
func (k ContentKey) LogValue() slog.Value {
	return slog.GroupValue(
		slog.Bool("present", len(k.Key) > 0 && len(k.IV) > 0),
		slog.Int("key_len", len(k.Key)),
		slog.Int("iv_len", len(k.IV)),
	)
}

// compile-time assertions that the redaction hooks are actually wired.
var (
	_ fmt.Stringer   = ContentKey{}
	_ slog.LogValuer = ContentKey{}
)

// Valid reports whether both halves are the AES-128 size ffmpeg expects.
func (k ContentKey) Valid() bool { return len(k.Key) == 16 && len(k.IV) == 16 }

// HexKey / HexIV render the material for the ffmpeg -audible_key/-audible_iv
// flags. These are the ONLY two methods that expose it; they exist so the
// exposure is greppable (`rg 'HexKey|HexIV'` finds every use site).
func (k ContentKey) HexKey() string { return hex.EncodeToString(k.Key) }
func (k ContentKey) HexIV() string  { return hex.EncodeToString(k.IV) }

// KeyOrder is the concatenation order used to derive the voucher-unsealing key.
//
// WHY THIS IS AN ENUM AND NOT A CONSTANT. The derivation is
// sha256(<four values concatenated>) split 16/16 into key/iv. Upstream
// (mkb79/audible-cli's decrypt_voucher, from mkb79/Audible#3) uses
// device_type + device_serial + customer_id + asin. That is OrderCanonical and
// it is what DecryptVoucher uses. But a wrong order does not fail loudly — AES-CBC
// happily decrypts to garbage — so a single hard-coded guess would be a silent
// trap. Making the order a parameter lets the admin liberation probe (probe.go)
// try every candidate and REPORT which one yields JSON, which is exactly what
// boom-w20s.19 has to answer. Once the probe confirms it live, the fixture test
// in voucher_test.go pins it forever and nobody re-guesses.
type KeyOrder int

const (
	// OrderCanonical — device_type + device_serial + customer_id + asin (upstream).
	OrderCanonical KeyOrder = iota
	// OrderSerialFirst — device_serial + customer_id + device_type + asin.
	OrderSerialFirst
	// OrderCustomerFirst — customer_id + device_serial + device_type + asin.
	OrderCustomerFirst
	// OrderAsinFirst — asin + device_type + device_serial + customer_id.
	OrderAsinFirst
)

// String names the order for probe output + test failure messages.
func (o KeyOrder) String() string {
	switch o {
	case OrderCanonical:
		return "device_type+device_serial+customer_id+asin"
	case OrderSerialFirst:
		return "device_serial+customer_id+device_type+asin"
	case OrderCustomerFirst:
		return "customer_id+device_serial+device_type+asin"
	case OrderAsinFirst:
		return "asin+device_type+device_serial+customer_id"
	default:
		return fmt.Sprintf("KeyOrder(%d)", int(o))
	}
}

// AllKeyOrders is the permutation set the probe sweeps, canonical first.
var AllKeyOrders = []KeyOrder{OrderCanonical, OrderSerialFirst, OrderCustomerFirst, OrderAsinFirst}

// keyMaterial derives the (key, iv) pair that unseals the voucher, for one
// candidate order. Exported behavior is covered by DecryptVoucherWith; this stays
// unexported so the derivation has exactly one entry point.
func keyMaterial(order KeyOrder, deviceType, deviceSerial, customerID, asin string) (key, iv []byte) {
	var buf string
	switch order {
	case OrderSerialFirst:
		buf = deviceSerial + customerID + deviceType + asin
	case OrderCustomerFirst:
		buf = customerID + deviceSerial + deviceType + asin
	case OrderAsinFirst:
		buf = asin + deviceType + deviceSerial + customerID
	default: // OrderCanonical
		buf = deviceType + deviceSerial + customerID + asin
	}
	sum := sha256.Sum256([]byte(buf))
	return sum[0:16], sum[16:32]
}

// voucherPayload is the decrypted voucher body. `rules` carries Audible's
// playback restrictions; we round-trip it for diagnostics but act on none of it.
type voucherPayload struct {
	Key   string          `json:"key"`
	IV    string          `json:"iv"`
	Rules json.RawMessage `json:"rules,omitempty"`
}

// DecryptVoucher unseals a license_response into the AAXC ContentKey using the
// canonical derivation. This is what the liberation pipeline calls.
func DecryptVoucher(cred *amazon.DeviceCredential, asin, licenseResponse string) (ContentKey, error) {
	return DecryptVoucherWith(OrderCanonical, cred, asin, licenseResponse)
}

// DecryptVoucherWith unseals a license_response under a specific candidate order.
// The probe sweeps AllKeyOrders through here; production code calls DecryptVoucher.
func DecryptVoucherWith(order KeyOrder, cred *amazon.DeviceCredential, asin, licenseResponse string) (ContentKey, error) {
	if cred == nil {
		return ContentKey{}, amazon.ErrNotRegistered
	}
	if cred.DeviceSerial == "" {
		return ContentKey{}, errors.New("liberate: credential has no device_serial — reconnect Amazon")
	}
	if cred.CustomerID == "" {
		// Same failure mode the Kindle whispersync probe reports: older
		// registrations predate CustomerID capture.
		return ContentKey{}, errors.New("liberate: credential has no customer_id — reconnect Amazon to capture it")
	}
	if asin == "" {
		return ContentKey{}, errors.New("liberate: empty asin")
	}
	sealed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(licenseResponse))
	if err != nil {
		return ContentKey{}, fmt.Errorf("liberate: voucher is not base64: %w", err)
	}
	if len(sealed) == 0 || len(sealed)%aes.BlockSize != 0 {
		return ContentKey{}, fmt.Errorf("liberate: voucher length %d is not a whole number of AES blocks", len(sealed))
	}

	key, iv := keyMaterial(order, amazon.DeviceType(), cred.DeviceSerial, cred.CustomerID, asin)
	block, err := aes.NewCipher(key)
	if err != nil {
		return ContentKey{}, fmt.Errorf("liberate: aes: %w", err)
	}
	plain := make([]byte, len(sealed))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plain, sealed)

	payload, err := parseVoucherJSON(plain)
	if err != nil {
		return ContentKey{}, err
	}
	ck := ContentKey{}
	if ck.Key, err = hex.DecodeString(payload.Key); err != nil {
		return ContentKey{}, fmt.Errorf("liberate: voucher key is not hex: %w", err)
	}
	if ck.IV, err = hex.DecodeString(payload.IV); err != nil {
		return ContentKey{}, fmt.Errorf("liberate: voucher iv is not hex: %w", err)
	}
	if !ck.Valid() {
		return ContentKey{}, fmt.Errorf("liberate: voucher yielded key/iv of %d/%d bytes, want 16/16", len(ck.Key), len(ck.IV))
	}
	return ck, nil
}

// parseVoucherJSON extracts the JSON object from a decrypted voucher.
//
// The plaintext is NOT PKCS#7-padded the way a CBC decrypt normally would be —
// upstream strips trailing NULs. Rather than depend on which flavour of trailing
// junk Amazon emits (NULs, PKCS#7, or a partial block), we take the substring
// from the first '{' to the LAST '}' and parse that. This is robust to every
// padding variant AND to a trailing-garbage tail, and it fails cleanly with
// ErrVoucherUndecryptable when the key was simply wrong (garbage plaintext has
// no reason to contain a balanced brace pair with valid JSON inside).
func parseVoucherJSON(plain []byte) (voucherPayload, error) {
	start := bytesIndexByte(plain, '{')
	end := bytesLastIndexByte(plain, '}')
	if start < 0 || end < start {
		return voucherPayload{}, ErrVoucherUndecryptable
	}
	var p voucherPayload
	if err := json.Unmarshal(plain[start:end+1], &p); err != nil {
		return voucherPayload{}, ErrVoucherUndecryptable
	}
	if p.Key == "" || p.IV == "" {
		return voucherPayload{}, ErrVoucherUndecryptable
	}
	return p, nil
}

// bytesIndexByte / bytesLastIndexByte: local so the parse path has no import of
// bytes purely for two one-liners, and so the scan is obviously allocation-free
// over what may be a multi-KB secret plaintext.
func bytesIndexByte(b []byte, c byte) int {
	for i := range b {
		if b[i] == c {
			return i
		}
	}
	return -1
}

func bytesLastIndexByte(b []byte, c byte) int {
	for i := len(b) - 1; i >= 0; i-- {
		if b[i] == c {
			return i
		}
	}
	return -1
}
