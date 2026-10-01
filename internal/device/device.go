// Package device holds the CLI's device credential: an ed25519 key under
// ~/.prizmal that a tenant member approves once in their browser, and the
// cache of short-lived device tokens it refreshes against the Switch.
//
// The private key never leaves the machine (device.key, 0600, in the 0700
// directory). The token cache (token.json, 0600) holds the bearer credential a
// harness sends on inference requests; it expires 10 minutes after issue.
package device

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/PrizmalAi/prizmal-cli/internal/config"
)

// KeyFileName is the device private key file inside the prizmal directory.
const KeyFileName = "device.key"

// TokenFileName is the cached device token file inside the prizmal directory.
const TokenFileName = "token.json"

// ErrNoDeviceKey is returned by LoadKey when no device key exists yet. Callers
// use it to offer enrollment rather than to fail.
var ErrNoDeviceKey = errors.New("prizmal device key not found")

// refreshDomainTag separates the refresh message from every other message an
// ed25519 device key or the token signer could sign. The signed bytes are
// (tag, env, deviceID, ts) length-prefixed, the EncodeMsg wire format the
// server verifies verbatim.
const refreshDomainTag = "cli-refresh-v1"

// Key is a device identity: the ed25519 key pair and the ids derived from it.
type Key struct {
	Priv ed25519.PrivateKey
	Pub  ed25519.PublicKey
}

// GenerateKey creates a fresh device key pair.
func GenerateKey() (*Key, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return &Key{Priv: priv, Pub: pub}, nil
}

// ID derives the device id the Config API stores: "dev_" plus the first 20
// hex characters of the public key's SHA-256. The CLI computes it before
// enrollment, so it can poll without a callback.
func (k *Key) ID() string {
	sum := sha256.Sum256(k.Pub)
	return "dev_" + hex.EncodeToString(sum[:])[:20]
}

// PublicB64 is the public key in the enrollment URL's encoding: 32 bytes,
// base64url without padding, and never percent-encoded.
func (k *Key) PublicB64() string {
	return base64.RawURLEncoding.EncodeToString(k.Pub)
}

// Fingerprint renders the device id the way the consent page does, so the
// operator can compare them by eye: the first eight hex characters, split in
// half ("9f2a-c41d").
func (k *Key) Fingerprint() string {
	hexPart := strings.TrimPrefix(k.ID(), "dev_")[:8]
	return hexPart[:4] + "-" + hexPart[4:]
}

// keyPath returns the device key file's path under the given prizmal
// directory.
func keyPath(dir string) string { return filepath.Join(dir, KeyFileName) }

// tokenPath returns the token cache file's path under the given prizmal
// directory.
func tokenPath(dir string) string { return filepath.Join(dir, TokenFileName) }

// writeKeyFile writes the 32-byte private key seed, creating the directory
// with 0700 and the file with 0600. The seed is the private key: ed25519
// derives the public half from it, so a seed round-trips through LoadKey.
func (k *Key) writeKeyFile(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(keyPath(dir), k.Priv.Seed(), 0o600)
}

// WriteKeyFile persists the key under dir (the prizmal directory).
func (k *Key) WriteKeyFile(dir string) error { return k.writeKeyFile(dir) }

// loadKeyFrom reads the device key from dir.
func loadKeyFrom(dir string) (*Key, error) {
	data, err := os.ReadFile(keyPath(dir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNoDeviceKey
		}
		return nil, err
	}
	if len(data) != ed25519.SeedSize {
		return nil, fmt.Errorf("device key file %s holds %d bytes, want %d",
			keyPath(dir), len(data), ed25519.SeedSize)
	}
	priv := ed25519.NewKeyFromSeed(data)
	return &Key{Priv: priv, Pub: priv.Public().(ed25519.PublicKey)}, nil
}

// LoadKey reads the device key from the user's prizmal directory.
func LoadKey() (*Key, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	return loadKeyFrom(dir)
}

// LoadOrCreateKey reads the device key, generating and persisting one when
// none exists. Re-approving an existing device must not mint a second key:
// the Config API binds one public key to one device id, and a fresh key
// silently orphans the old enrollment. A key file that exists but is
// unreadable or corrupt is an error, never a regeneration.
func LoadOrCreateKey() (*Key, bool, error) {
	dir, err := Dir()
	if err != nil {
		return nil, false, err
	}
	key, err := loadKeyFrom(dir)
	if err == nil {
		return key, false, nil
	}
	if !errors.Is(err, ErrNoDeviceKey) {
		return nil, false, err
	}
	key, err = GenerateKey()
	if err != nil {
		return nil, false, err
	}
	if err := key.writeKeyFile(dir); err != nil {
		return nil, false, err
	}
	return key, true, nil
}

// SignRefresh produces the sig the refresh request carries: the ed25519
// signature over (cli-refresh-v1, env, deviceID, ts) length-prefixed.
func (k *Key) SignRefresh(env, ts string) string {
	msg := EncodeMsg(refreshDomainTag, env, k.ID(), ts)
	sig := ed25519.Sign(k.Priv, msg)
	return base64.RawURLEncoding.EncodeToString(sig)
}

// VerifyRefresh is the server side's arithmetic, replicated for tests: the
// signature over the tagged message verifies against the public key.
func (k *Key) VerifyRefresh(env, ts, sigB64 string) bool {
	sig, err := base64.RawURLEncoding.DecodeString(sigB64)
	if err != nil {
		return false
	}
	return ed25519.Verify(k.Pub, EncodeMsg(refreshDomainTag, env, k.ID(), ts), sig)
}

// EncodeMsg builds a length-prefixed canonical message from components:
// len(comp1):comp1|len(comp2):comp2|..., the format the token signer and the
// refresh verifier both canonicalize with.
func EncodeMsg(parts ...string) []byte {
	var b []byte
	for i, p := range parts {
		if i > 0 {
			b = append(b, '|')
		}
		b = append(b, strconv.Itoa(len(p))...)
		b = append(b, ':')
		b = append(b, p...)
	}
	return b
}

// CachedToken is the persisted token cache: the device token string, its
// expiry, and the sign-in deadline the last refresh reported, if one applies.
type CachedToken struct {
	Token    string     `json:"token"`
	Expiry   time.Time  `json:"expiry"`
	ReauthBy *time.Time `json:"reauth_by,omitempty"`
}

// loadCachedTokenFrom reads the token cache from dir. A missing cache is not
// an error: a device that never completed enrollment simply has none.
func loadCachedTokenFrom(dir string) (*CachedToken, error) {
	data, err := os.ReadFile(tokenPath(dir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var ct CachedToken
	if err := json.Unmarshal(data, &ct); err != nil {
		return nil, fmt.Errorf("device token cache %s is unreadable: %w", tokenPath(dir), err)
	}
	return &ct, nil
}

// LoadCachedToken reads the token cache from the user's prizmal directory.
func LoadCachedToken() (*CachedToken, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	return loadCachedTokenFrom(dir)
}

// saveTo writes the cache with 0600, creating the directory with 0700.
func (ct *CachedToken) saveTo(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(ct, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(tokenPath(dir), data, 0o600)
}

// Save writes the cache to the user's prizmal directory.
func (ct *CachedToken) Save() error {
	dir, err := Dir()
	if err != nil {
		return err
	}
	return ct.saveTo(dir)
}

// Dir returns the user's prizmal directory, the same directory the CLI's
// config file lives in. It derives from config.Path so there is one definition
// of where that directory is.
func Dir() (string, error) {
	p, err := config.Path()
	if err != nil {
		return "", err
	}
	return filepath.Dir(p), nil
}
