package device

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestGenerateKeyDerivesIDAndFingerprint(t *testing.T) {
	key, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	id := key.ID()
	if len(id) != len("dev_")+20 {
		t.Fatalf("device id %q is %d characters, want dev_ plus 20 hex", id, len(id))
	}
	if id[:4] != "dev_" {
		t.Fatalf("device id %q does not start with dev_", id)
	}
	fp := key.Fingerprint()
	if len(fp) != 9 || fp[4] != '-' {
		t.Fatalf("fingerprint %q is not 4-hex-dash-4-hex", fp)
	}
	if fp != id[4:8]+"-"+id[8:12] {
		t.Fatalf("fingerprint %q does not match the device id %q", fp, id)
	}
}

func TestKeyFileRoundTripsAt0600InA0700Dir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "prizmal")

	key, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	if err := key.WriteKeyFile(dir); err != nil {
		t.Fatalf("WriteKeyFile: %v", err)
	}

	// Windows does not carry Unix permission bits: a file it creates reports
	// 0666 whatever mode was requested, so the mode assertions are POSIX-only.
	// The write still passes 0600/0700 at every call site (see config's own
	// mode test, same exemption).
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(keyPath(dir))
		if err != nil {
			t.Fatalf("stat key file: %v", err)
		}
		if perm := fi.Mode().Perm(); perm != 0o600 {
			t.Fatalf("key file mode is %o, want 600", perm)
		}
		di, err := os.Stat(dir)
		if err != nil {
			t.Fatalf("stat dir: %v", err)
		}
		if perm := di.Mode().Perm(); perm != 0o700 {
			t.Fatalf("dir mode is %o, want 700", perm)
		}
	}

	loaded, err := loadKeyFrom(dir)
	if err != nil {
		t.Fatalf("loadKeyFrom: %v", err)
	}
	if loaded.ID() != key.ID() {
		t.Fatalf("loaded key id %q != generated %q", loaded.ID(), key.ID())
	}
}

func TestLoadOrCreateReapprovesExistingKey(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "prizmal")
	t.Setenv("HOME", filepath.Dir(dir))

	// The user's home is the parent; Dir() appends .prizmal, so point the
	// directory name at prizmal by making home its parent.
	first, created, err := LoadOrCreateKey()
	if err != nil {
		t.Fatalf("first LoadOrCreateKey: %v", err)
	}
	if !created {
		t.Fatal("first call did not create a key")
	}

	second, created, err := LoadOrCreateKey()
	if err != nil {
		t.Fatalf("second LoadOrCreateKey: %v", err)
	}
	if created {
		t.Fatal("second call created a key instead of reusing the existing one")
	}
	if second.ID() != first.ID() {
		t.Fatalf("re-approving minted a new device id: %q != %q", second.ID(), first.ID())
	}
}

func TestLoadKeyMissingIsErrNoDeviceKey(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "prizmal")
	_, err := loadKeyFrom(dir)
	if err != ErrNoDeviceKey {
		t.Fatalf("loadKeyFrom on empty dir: %v, want ErrNoDeviceKey", err)
	}
}

func TestRefreshSignatureIsTaggedAndVerified(t *testing.T) {
	key, _ := GenerateKey()
	ts := "2026-09-25T12:00:00Z"
	sig := key.SignRefresh("s", ts)
	if !key.VerifyRefresh("s", ts, sig) {
		t.Fatal("signature did not verify against its own key")
	}
	// The env is inside the signed message: a staging signature does not
	// verify as production.
	if key.VerifyRefresh("p", ts, sig) {
		t.Fatal("a signature for env s verified for env p")
	}
	// A different device's signature does not verify.
	other, _ := GenerateKey()
	if other.VerifyRefresh("s", ts, sig) {
		t.Fatal("a signature verified against a different device's key")
	}
	// The signed bytes carry the domain tag, so the message is not a bare
	// concatenation that another message could collide with.
	want := itoa(len(refreshDomainTag)) + ":" + refreshDomainTag + "|1:s|" + itoa(len(key.ID())) + ":" + key.ID() + "|20:" + ts
	if got := string(EncodeMsg(refreshDomainTag, "s", key.ID(), ts)); got != want {
		t.Fatalf("EncodeMsg = %q, want %q", got, want)
	}
}

// itoa keeps the test independent of strconv's name.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func TestRefreshPostsTaggedSignatureAndParses200(t *testing.T) {
	key, _ := GenerateKey()
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)

	var gotBody map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != refreshPath {
			t.Errorf("path = %q, want %q", r.URL.Path, refreshPath)
		}
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"device_token": "pz-s-dt-token",
			"expires_in":   600,
			"reauth_by":    now.Add(7 * 24 * time.Hour).Format(time.RFC3339),
		})
	}))
	defer srv.Close()

	client := NewClient(srv.URL)
	tok, err := client.Refresh(key, now)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if tok.Value != "pz-s-dt-token" {
		t.Fatalf("token = %q", tok.Value)
	}
	if tok.ExpiresIn != 10*time.Minute {
		t.Fatalf("expires_in = %v, want 10m", tok.ExpiresIn)
	}
	if tok.ReauthBy == nil {
		t.Fatal("reauth_by was not parsed")
	}
	if gotBody["device_id"] != key.ID() {
		t.Fatalf("posted device_id %q != %q", gotBody["device_id"], key.ID())
	}
	if gotBody["ts"] != "2026-09-25T12:00:00Z" {
		t.Fatalf("posted ts %q", gotBody["ts"])
	}
	if !key.VerifyRefresh(client.Env, gotBody["ts"], gotBody["sig"]) {
		t.Fatal("posted signature does not verify for the posted ts and env")
	}
}

func TestRefreshStatusCodeMapping(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"404 pending", http.StatusNotFound, `{"error":{"code":"NOT_FOUND"}}`, ErrDeviceUnknown},
		{"401 generic", http.StatusUnauthorized, `{"error":{"code":"UNAUTHORIZED"}}`, ErrNotAuthorized},
		{"401 reauth", http.StatusUnauthorized, `{"error":{"code":"REAUTH_REQUIRED"}}`, ErrReauthRequired},
		{"429 throttle", http.StatusTooManyRequests, `{"error":{"code":"CONFLICT"}}`, ErrRateLimited},
		{"503 unavailable", http.StatusServiceUnavailable, `{"error":{"code":"UNAVAILABLE"}}`, ErrUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			key, _ := GenerateKey()
			_, err := NewClient(srv.URL).Refresh(key, now)
			if err != tc.want {
				t.Fatalf("Refresh on %d: err = %v, want %v", tc.status, err, tc.want)
			}
		})
	}
}

func TestEnvTagFromHost(t *testing.T) {
	cases := map[string]string{
		"https://api.prizmal.ai":         "p",
		"https://api.staging.prizmal.ai": "s",
		"http://localhost:8080":          "d",
		"https://example.com":            "d",
	}
	for host, want := range cases {
		if got := EnvTag(host); got != want {
			t.Errorf("EnvTag(%q) = %q, want %q", host, got, want)
		}
	}
}

func TestAuthorizeURLCarriesPublicKeyAndName(t *testing.T) {
	key, _ := GenerateKey()
	u := AuthorizeURL("https://app.prizmal.ai/", key, "Nicolas's MacBook")
	if u[:len("https://app.prizmal.ai/cli/authorize?")] != "https://app.prizmal.ai/cli/authorize?" {
		t.Fatalf("URL = %q", u)
	}
	if !contains(u, "pk="+key.PublicB64()) {
		t.Fatalf("URL %q does not carry the unpadded public key", u)
	}
	if !contains(u, "name=") {
		t.Fatalf("URL %q does not carry the device name", u)
	}
}

func TestPublicB64IsUnpaddedRawURL(t *testing.T) {
	// A public key whose raw encoding ends in a byte that produces padding
	// under standard base64: RawURLEncoding must not emit '='.
	pub := ed25519.PublicKey(make([]byte, ed25519.PublicKeySize))
	for i := range pub {
		pub[i] = byte(i)
	}
	k := &Key{Pub: pub}
	got := k.PublicB64()
	if contains(got, "=") {
		t.Fatalf("PublicB64 = %q contains padding", got)
	}
	raw, err := base64.RawURLEncoding.DecodeString(got)
	if err != nil || len(raw) != ed25519.PublicKeySize {
		t.Fatalf("PublicB64 does not decode to 32 bytes: %v", err)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
