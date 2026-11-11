package main

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/device"
	launcher "github.com/PrizmalAi/prizmal-cli/internal/launcher"
)

// refreshStub answers every device refresh with the given status and error
// code, and counts the requests.
func refreshStub(t *testing.T, status int, code string) (*device.Client, *int) {
	t.Helper()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"error":{"code":"` + code + `"}}`))
	}))
	t.Cleanup(srv.Close)
	return device.NewClient(srv.URL), &calls
}

// enrolledKey writes a device key under the temp home and returns it with the
// prizmal directory.
func enrolledKey(t *testing.T) (*device.Key, string) {
	t.Helper()
	dir, err := device.Dir()
	if err != nil {
		t.Fatal(err)
	}
	key, err := device.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := key.WriteKeyFile(dir); err != nil {
		t.Fatal(err)
	}
	return key, dir
}

// answerPrompts sets the terminal state and the typed reply for one test, and
// returns what the prompt wrote.
func answerPrompts(t *testing.T, tty bool, reply string) *bytes.Buffer {
	t.Helper()
	oldTTY, oldIn, oldOut := launcher.StdinIsTerminal, launcher.ConfirmInput, launcher.ConfirmOutput
	var out bytes.Buffer
	launcher.StdinIsTerminal = func() bool { return tty }
	launcher.ConfirmInput = bufio.NewReader(strings.NewReader(reply))
	launcher.ConfirmOutput = &out
	t.Cleanup(func() {
		launcher.StdinIsTerminal, launcher.ConfirmInput, launcher.ConfirmOutput = oldTTY, oldIn, oldOut
	})
	return &out
}

func backupFiles(t *testing.T, dir string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, device.KeyFileName+".*"))
	if err != nil {
		t.Fatal(err)
	}
	return matches
}

// A revoked key that the operator agrees to back up moves to a backup file, and
// the login goes on with a new key.
func TestLoginBacksUpRevokedKeyWhenAsked(t *testing.T) {
	useTempHome(t)
	old, dir := enrolledKey(t)
	prompts := answerPrompts(t, true, "y\n")
	client, _ := refreshStub(t, http.StatusUnauthorized, "DEVICE_REVOKED")
	var out bytes.Buffer

	key, created, err := ensureDeviceKey(client, &out)
	if err != nil {
		t.Fatalf("ensureDeviceKey: %v", err)
	}
	if !created || key.ID() == old.ID() {
		t.Fatalf("created = %v, same id = %v; want a new key", created, key.ID() == old.ID())
	}
	backups := backupFiles(t, dir)
	if len(backups) != 1 {
		t.Fatalf("backups = %v, want exactly one", backups)
	}
	data, _ := os.ReadFile(backups[0])
	if !bytes.Equal(data, old.Priv.Seed()) {
		t.Fatal("the backup is not the revoked key")
	}
	if !strings.Contains(out.String(), "revoked") || !strings.Contains(out.String(), backups[0]) {
		t.Fatalf("output %q must say the device was revoked and where the backup went", out.String())
	}
	if !strings.Contains(strings.ToLower(prompts.String()), "back up") {
		t.Fatalf("the operator was not asked about a backup: %q", prompts.String())
	}
}

// Declining the backup replaces the key and keeps no copy.
func TestLoginReplacesRevokedKeyWhenBackupDeclined(t *testing.T) {
	useTempHome(t)
	old, dir := enrolledKey(t)
	answerPrompts(t, true, "n\n")
	client, _ := refreshStub(t, http.StatusUnauthorized, "DEVICE_REVOKED")

	key, created, err := ensureDeviceKey(client, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("ensureDeviceKey: %v", err)
	}
	if !created || key.ID() == old.ID() {
		t.Fatal("login did not make a new key")
	}
	if got := backupFiles(t, dir); len(got) != 0 {
		t.Fatalf("backups = %v, want none after declining", got)
	}
	loaded, err := device.LoadKey()
	if err != nil || loaded.ID() != key.ID() {
		t.Fatalf("the key on disk is not the new one: %v", err)
	}
}

// With no terminal nobody can answer, so the key is backed up and the output
// says where it went. No prompt is written.
func TestLoginBacksUpRevokedKeyWithoutATerminal(t *testing.T) {
	useTempHome(t)
	old, dir := enrolledKey(t)
	prompts := answerPrompts(t, false, "")
	client, _ := refreshStub(t, http.StatusUnauthorized, "DEVICE_REVOKED")
	var out bytes.Buffer

	key, created, err := ensureDeviceKey(client, &out)
	if err != nil {
		t.Fatalf("ensureDeviceKey: %v", err)
	}
	if !created || key.ID() == old.ID() {
		t.Fatal("login did not make a new key")
	}
	backups := backupFiles(t, dir)
	if len(backups) != 1 {
		t.Fatalf("backups = %v, want exactly one", backups)
	}
	if !strings.Contains(out.String(), backups[0]) {
		t.Fatalf("output %q does not say where the backup went", out.String())
	}
	if prompts.Len() != 0 {
		t.Fatalf("a prompt was written without a terminal: %q", prompts.String())
	}
}

// Only DEVICE_REVOKED retires a key. An unapproved, expired or otherwise
// refused key is reused, so the same device row is re-approved.
func TestLoginReusesKeyUnlessRevoked(t *testing.T) {
	cases := []struct {
		name   string
		status int
		code   string
	}{
		{"unauthorized", http.StatusUnauthorized, "UNAUTHORIZED"},
		{"sign-in expired", http.StatusUnauthorized, "REAUTH_REQUIRED"},
		{"not enrolled yet", http.StatusNotFound, "NOT_FOUND"},
		{"switch unavailable", http.StatusServiceUnavailable, "UNAVAILABLE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			useTempHome(t)
			old, dir := enrolledKey(t)
			prompts := answerPrompts(t, true, "y\n")
			client, calls := refreshStub(t, tc.status, tc.code)

			key, created, err := ensureDeviceKey(client, &bytes.Buffer{})
			if err != nil {
				t.Fatalf("ensureDeviceKey: %v", err)
			}
			if created || key.ID() != old.ID() {
				t.Fatalf("created = %v, same id = %v; want the key reused", created, key.ID() == old.ID())
			}
			if len(backupFiles(t, dir)) != 0 || prompts.Len() != 0 {
				t.Fatal("a key that was not revoked was backed up or prompted about")
			}
			if *calls != 1 {
				t.Fatalf("refresh calls = %d, want 1", *calls)
			}
		})
	}
}

// A machine with no key makes one without asking the switch anything.
func TestLoginMakesAKeyWhenNoneExists(t *testing.T) {
	useTempHome(t)
	answerPrompts(t, true, "")
	client, calls := refreshStub(t, http.StatusUnauthorized, "DEVICE_REVOKED")

	_, created, err := ensureDeviceKey(client, &bytes.Buffer{})
	if err != nil || !created {
		t.Fatalf("created = %v, err = %v; want a new key", created, err)
	}
	if *calls != 0 {
		t.Fatalf("refresh calls = %d, want none for a key that was just made", *calls)
	}
}

// The prompt and the messages never print key material: not the private seed,
// not the public key, in either encoding.
func TestLoginRevokedOutputNeverPrintsKeyMaterial(t *testing.T) {
	useTempHome(t)
	old, _ := enrolledKey(t)
	prompts := answerPrompts(t, true, "y\n")
	client, _ := refreshStub(t, http.StatusUnauthorized, "DEVICE_REVOKED")
	var out bytes.Buffer

	if _, _, err := ensureDeviceKey(client, &out); err != nil {
		t.Fatal(err)
	}
	seed := old.Priv.Seed()
	secrets := []string{
		string(seed), old.PublicB64(),
		base64.StdEncoding.EncodeToString(seed), base64.RawURLEncoding.EncodeToString(seed),
	}
	text := out.String() + prompts.String()
	for _, s := range secrets {
		if strings.Contains(text, s) {
			t.Fatalf("output printed key material %q", s)
		}
	}
}
