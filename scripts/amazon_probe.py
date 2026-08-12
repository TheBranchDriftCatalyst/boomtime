#!/usr/bin/env python3
"""amazon_probe.py — dev tool to poke the Audible/Kindle device-signed API using
a boomtime user's stored Amazon credential.

It grabs the user's encrypted_amazon_device blob + BOOM_ENCRYPTION_KEY from the
running cluster (via kubectl), decrypts it locally (AES-256-GCM, the same
[nonce|ciphertext|tag] layout as internal/auth/crypto.go), signs requests the
same way internal/amazon/signing.go does, and dumps whatever the API returns —
for inventorying fields / validating params before wiring the ingest.

SECURITY: this reads the master encryption key + a user's private device
credential out of prod into memory. Run it only if you already have cluster
access. It prints API DATA (book titles etc.) but never the key/token. Do NOT
redirect its output into the repo.

Usage:
    scripts/amazon_probe.py --user DJ                      # default probe set
    scripts/amazon_probe.py --user DJ --path "/1.0/library?num_results=1000&page=2"
    scripts/amazon_probe.py --user DJ --raw --out /tmp/lib.json
    # sourcing overrides (skip kubectl if you already have the values):
    BOOM_ENCRYPTION_KEY=... AMAZON_CRED_B64=... scripts/amazon_probe.py --user DJ

Env:
    KCTX  (default admin@catalyst-cluster)   kubectl --context
    KNS   (default boomtime)                 kubectl -n
    BOOM_ENCRYPTION_KEY / AMAZON_CRED_B64     bypass the kubectl fetch
"""
import argparse
import base64
import datetime
import json
import os
import subprocess
import sys
import urllib.error
import urllib.request

from cryptography.hazmat.primitives import hashes, serialization
from cryptography.hazmat.primitives.asymmetric import padding
from cryptography.hazmat.primitives.ciphers.aead import AESGCM

KCTX = os.environ.get("KCTX", "admin@catalyst-cluster")
KNS = os.environ.get("KNS", "boomtime")

# marketplace -> Amazon TLD (mirrors internal/amazon/register.go)
DOMAIN = {
    "us": "com", "uk": "co.uk", "de": "de", "ca": "ca", "au": "com.au",
    "fr": "fr", "it": "it", "es": "es", "in": "in", "jp": "co.jp", "br": "com.br",
}


def sh(cmd):
    return subprocess.run(cmd, capture_output=True, text=True, check=True).stdout


def fetch_cred_b64(user):
    """Pull the base64 encrypted_amazon_device for a username (or user_id)."""
    if os.environ.get("AMAZON_CRED_B64"):
        return os.environ["AMAZON_CRED_B64"].strip()
    q = (
        "SELECT encode(encrypted_amazon_device,'base64') FROM users "
        f"WHERE username = '{user}' AND encrypted_amazon_device IS NOT NULL LIMIT 1;"
    )
    out = sh([
        "kubectl", "--context", KCTX, "-n", KNS, "exec", "boomtime-postgres-1",
        "-c", "postgres", "--", "psql", "-U", "postgres", "-d", "boomtime", "-tAc", q,
    ])
    return out.replace("\n", "").replace(" ", "").strip()


def fetch_key():
    if os.environ.get("BOOM_ENCRYPTION_KEY"):
        return os.environ["BOOM_ENCRYPTION_KEY"].strip()
    return sh([
        "kubectl", "--context", KCTX, "-n", KNS, "exec", "deploy/boomtime",
        "-c", "boomtime", "--", "sh", "-c", 'printf "%s" "$BOOM_ENCRYPTION_KEY"',
    ]).strip()


def load_cred(user):
    cred_b64 = fetch_cred_b64(user)
    if not cred_b64:
        sys.exit(f"no Amazon credential stored for user {user!r}")
    key = base64.b64decode(fetch_key())
    blob = base64.b64decode(cred_b64)
    pt = AESGCM(key).decrypt(blob[:12], blob[12:], None)  # [nonce|ct|tag]
    return json.loads(pt)


class Signer:
    def __init__(self, cred):
        self.adp = cred["adp_token"]
        self.pk = serialization.load_pem_private_key(cred["device_private_key"].encode(), None)
        self.host = "api.audible." + DOMAIN.get(cred.get("marketplace", "us"), "com")

    def headers(self, method, path, body=b""):
        now = datetime.datetime.now(datetime.timezone.utc)
        date = now.strftime("%Y-%m-%dT%H:%M:%S.") + f"{now.microsecond // 1000:03d}Z"
        canon = "\n".join([method, path, date, body.decode() if body else "", self.adp])
        sig = self.pk.sign(canon.encode(), padding.PKCS1v15(), hashes.SHA256())
        return {
            "x-adp-token": self.adp,
            "x-adp-alg": "SHA256withRSA:1.0",
            "x-adp-signature": base64.b64encode(sig).decode() + ":" + date,
            "Accept": "application/json",
        }

    def get(self, path):
        req = urllib.request.Request("https://" + self.host + path, headers=self.headers("GET", path))
        try:
            r = urllib.request.urlopen(req, timeout=30)
            return r.status, r.read()
        except urllib.error.HTTPError as e:
            return e.code, e.read()


DEFAULT_LIB_GROUPS = ",".join([
    "product_desc", "product_attrs", "product_details", "contributors", "series",
    "category_ladders", "rating", "is_finished", "percent_complete",
    "listening_status", "relationships", "origin_asin", "order_details",
])


def default_probes(sg):
    return [
        ("library (all groups, page 1)",
         f"/1.0/library?response_groups={DEFAULT_LIB_GROUPS}&num_results=1000&page=1"),
        ("stats/status/finished", "/1.0/stats/status/finished"),
        ("stats/aggregates (monthly)",
         "/1.0/stats/aggregates?response_groups=total_listening_stats"
         "&monthly_listening_interval_duration=12&monthly_listening_interval_start_date=2024-01&store=Audible"),
    ]


def summarize(name, status, body):
    print(f"\n=== {name} -> HTTP {status} ===")
    try:
        j = json.loads(body)
    except Exception:
        print("  non-JSON:", body[:300])
        return None
    if isinstance(j, dict):
        print("  top-level keys:", sorted(j.keys()))
        items = j.get("items") or j.get("datasets") or j.get("aggregated_monthly_listening_stats")
        if isinstance(items, list):
            print("  records:", len(items))
            keys = set()
            for it in items[:60]:
                if isinstance(it, dict):
                    keys |= set(it.keys())
            print(f"  per-record fields ({len(keys)}):", sorted(keys))
            if items:
                print("  sample[0]:")
                print("    " + json.dumps(items[0], indent=2)[:1500].replace("\n", "\n    "))
    return j


def main():
    ap = argparse.ArgumentParser(description="Poke the Audible API with a boomtime user's Amazon credential.")
    ap.add_argument("--user", required=True, help="boomtime username (or user_id)")
    ap.add_argument("--path", help="poke a single custom endpoint path instead of the default set")
    ap.add_argument("--raw", action="store_true", help="print the full raw JSON body")
    ap.add_argument("--out", help="write the raw JSON body to a file")
    args = ap.parse_args()

    cred = load_cred(args.user)
    print(f"# credential: marketplace={cred.get('marketplace')} "
          f"customer_id={'yes' if cred.get('customer_id') else 'no'} "
          f"registered={cred.get('registered_at')}")
    signer = Signer(cred)

    probes = [("custom", args.path)] if args.path else default_probes(cred.get("marketplace"))
    for name, path in probes:
        status, body = signer.get(path)
        j = summarize(name, status, body)
        if args.out and j is not None:
            with open(args.out, "w") as f:
                json.dump(j, f, indent=2)
            print(f"  (wrote raw JSON -> {args.out})")
        if args.raw and j is not None:
            print(json.dumps(j, indent=2))


if __name__ == "__main__":
    main()
