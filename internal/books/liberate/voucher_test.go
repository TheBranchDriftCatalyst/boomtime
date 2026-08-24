package liberate

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/books/connect/amazon"
)

// Fixed test identity. Values are shaped like the real ones but are not real.
const (
	tstSerial   = "1a2b3c4d5e6f70819293a4b5c6d7e8f9"
	tstCustomer = "amzn1.account.AHTESTCUSTOMERID000000"
	tstASIN     = "B0BTESTASIN"
)

func testCred() *amazon.DeviceCredential {
	return &amazon.DeviceCredential{
		DeviceSerial: tstSerial,
		CustomerID:   tstCustomer,
		Marketplace:  amazon.MarketplaceUS,
	}
}

// sealVoucher builds a voucher the way Amazon does.
//
// NON-TAUTOLOGY NOTE: this deliberately does NOT call keyMaterial(). It spells
// the concatenation out literally, so if someone reorders the terms in
// keyMaterial the round-trip breaks and this test fails. A helper that shared the
// derivation with the implementation would pass no matter what order was used,
// which would make the single most important test in the package worthless.
func sealVoucher(t *testing.T, deviceType, serial, customerID, asin string, plaintext []byte) string {
	t.Helper()
	digest := sha256.Sum256([]byte(deviceType + serial + customerID + asin))
	block, err := aes.NewCipher(digest[0:16])
	if err != nil {
		t.Fatalf("aes: %v", err)
	}
	// Pad with NULs to a block boundary, matching what upstream observes on the
	// wire (the plaintext is NOT PKCS#7-padded).
	padded := append([]byte{}, plaintext...)
	for len(padded)%aes.BlockSize != 0 {
		padded = append(padded, 0)
	}
	out := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, digest[16:32]).CryptBlocks(out, padded)
	return base64.StdEncoding.EncodeToString(out)
}

const (
	wantKeyHex = "000102030405060708090a0b0c0d0e0f"
	wantIVHex  = "0f0e0d0c0b0a09080706050403020100"
)

func voucherJSON() []byte {
	return []byte(`{"key":"` + wantKeyHex + `","iv":"` + wantIVHex + `","rules":[{"name":"DefaultExpiresRule"}]}`)
}

func TestDecryptVoucherRoundTrip(t *testing.T) {
	sealed := sealVoucher(t, amazon.DeviceType(), tstSerial, tstCustomer, tstASIN, voucherJSON())

	got, err := DecryptVoucher(testCred(), tstASIN, sealed)
	if err != nil {
		t.Fatalf("DecryptVoucher: %v", err)
	}
	if got.HexKey() != wantKeyHex {
		t.Errorf("key = %s, want %s", got.HexKey(), wantKeyHex)
	}
	if got.HexIV() != wantIVHex {
		t.Errorf("iv = %s, want %s", got.HexIV(), wantIVHex)
	}
	if !got.Valid() {
		t.Error("Valid() = false for a 16/16 key")
	}
}

// The canonical order must be the ONLY one that decrypts a canonically-sealed
// voucher. If a second order also succeeded, the probe's "which order won"
// answer would be ambiguous and the sweep would report a false positive.
func TestDecryptVoucherWrongOrderFails(t *testing.T) {
	sealed := sealVoucher(t, amazon.DeviceType(), tstSerial, tstCustomer, tstASIN, voucherJSON())

	for _, order := range AllKeyOrders {
		if order == OrderCanonical {
			continue
		}
		if _, err := DecryptVoucherWith(order, testCred(), tstASIN, sealed); err == nil {
			t.Errorf("order %v decrypted a canonically-sealed voucher; orders must be mutually exclusive", order)
		} else if !errors.Is(err, ErrVoucherUndecryptable) {
			t.Errorf("order %v: err = %v, want ErrVoucherUndecryptable", order, err)
		}
	}
}

// A wrong key yields garbage, not an error, from AES itself. The only thing that
// turns that into a clean failure is the JSON-shape check in parseVoucherJSON —
// so exercise it with a voucher sealed under a DIFFERENT identity.
func TestDecryptVoucherWrongIdentityIsCleanFailure(t *testing.T) {
	sealed := sealVoucher(t, amazon.DeviceType(), "otherserial", "othercustomer", tstASIN, voucherJSON())

	_, err := DecryptVoucher(testCred(), tstASIN, sealed)
	if !errors.Is(err, ErrVoucherUndecryptable) {
		t.Fatalf("err = %v, want ErrVoucherUndecryptable", err)
	}
}

// The ASIN is part of the derivation, so a voucher for book A must not decrypt
// under book B. Guards against a caching bug reusing a voucher across titles.
func TestDecryptVoucherIsASINBound(t *testing.T) {
	sealed := sealVoucher(t, amazon.DeviceType(), tstSerial, tstCustomer, "B0OTHERASIN", voucherJSON())

	if _, err := DecryptVoucher(testCred(), tstASIN, sealed); !errors.Is(err, ErrVoucherUndecryptable) {
		t.Fatalf("a voucher for another ASIN decrypted: err = %v", err)
	}
}

// parseVoucherJSON must survive whichever trailing-junk flavour Amazon emits.
// This is the specific NEEDS-LIVE-VERIFY item the design doc flags, so all three
// plausible variants are pinned rather than one.
func TestDecryptVoucherTrailingJunkVariants(t *testing.T) {
	cases := map[string][]byte{
		"nul padded":      voucherJSON(),
		"pkcs7 remnant":   append(voucherJSON(), bytes.Repeat([]byte{0x0b}, 11)...),
		"trailing text":   append(voucherJSON(), []byte("\n\n garbage tail ")...),
		"leading garbage": append([]byte("\x00\x00"), voucherJSON()...),
	}
	for name, plaintext := range cases {
		t.Run(name, func(t *testing.T) {
			sealed := sealVoucher(t, amazon.DeviceType(), tstSerial, tstCustomer, tstASIN, plaintext)
			got, err := DecryptVoucher(testCred(), tstASIN, sealed)
			if err != nil {
				t.Fatalf("DecryptVoucher: %v", err)
			}
			if got.HexKey() != wantKeyHex {
				t.Errorf("key = %s, want %s", got.HexKey(), wantKeyHex)
			}
		})
	}
}

func TestDecryptVoucherInputValidation(t *testing.T) {
	valid := sealVoucher(t, amazon.DeviceType(), tstSerial, tstCustomer, tstASIN, voucherJSON())

	tests := []struct {
		name    string
		cred    *amazon.DeviceCredential
		asin    string
		voucher string
		wantErr string
	}{
		{"nil credential", nil, tstASIN, valid, "no device credential"},
		{"no serial", &amazon.DeviceCredential{CustomerID: tstCustomer}, tstASIN, valid, "no device_serial"},
		{"no customer id", &amazon.DeviceCredential{DeviceSerial: tstSerial}, tstASIN, valid, "no customer_id"},
		{"empty asin", testCred(), "", valid, "empty asin"},
		{"not base64", testCred(), tstASIN, "!!!not base64!!!", "not base64"},
		{"empty voucher", testCred(), tstASIN, "", "whole number of AES blocks"},
		{"partial block", testCred(), tstASIN, base64.StdEncoding.EncodeToString([]byte("short")), "whole number of AES blocks"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DecryptVoucher(tc.cred, tc.asin, tc.voucher)
			if err == nil {
				t.Fatal("want error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("err = %q, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

// A voucher whose key is the wrong LENGTH must be rejected: ffmpeg would
// otherwise be handed a short -audible_key and fail deep in the pipeline with a
// far less obvious message.
func TestDecryptVoucherRejectsWrongSizedKey(t *testing.T) {
	short := []byte(`{"key":"0011","iv":"` + wantIVHex + `"}`)
	sealed := sealVoucher(t, amazon.DeviceType(), tstSerial, tstCustomer, tstASIN, short)

	_, err := DecryptVoucher(testCred(), tstASIN, sealed)
	if err == nil || !strings.Contains(err.Error(), "want 16/16") {
		t.Fatalf("err = %v, want a 16/16 size complaint", err)
	}
}

// --- redaction -------------------------------------------------------------
//
// These are the tests that keep a content key out of the logs. They assert on
// the RENDERED output rather than on the method existing, because the failure we
// care about is material reaching a log sink, not an API shape.

func TestContentKeyRedactsUnderFormatting(t *testing.T) {
	k := ContentKey{Key: mustHex(t, wantKeyHex), IV: mustHex(t, wantIVHex)}

	for _, format := range []string{"%v", "%s", "%+v"} {
		rendered := sprintf(format, k)
		if strings.Contains(rendered, wantKeyHex) || strings.Contains(rendered, wantIVHex) {
			t.Errorf("format %s leaked key material: %s", format, rendered)
		}
		if !strings.Contains(rendered, "redacted") {
			t.Errorf("format %s = %q, want it to mention redaction", format, rendered)
		}
	}
}

func TestContentKeyRedactsUnderSlog(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	k := ContentKey{Key: mustHex(t, wantKeyHex), IV: mustHex(t, wantIVHex)}

	logger.Info("liberating", "contentKey", k)
	// Nested inside a group, which is how it would reach a log in practice.
	logger.Info("liberating", slog.Group("book", "asin", tstASIN, "contentKey", k))

	out := buf.String()
	if strings.Contains(out, wantKeyHex) || strings.Contains(out, wantIVHex) {
		t.Fatalf("slog leaked key material: %s", out)
	}
	if !strings.Contains(out, "key_len=16") {
		t.Errorf("slog output = %q, want the redacted summary with key_len", out)
	}
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("hex: %v", err)
	}
	return b
}
