package main

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

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

	key, created, _, err := ensureDeviceKey(client, &out)
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
	// The message states the fact and names no one who caused it.
	if !slices.Contains(strings.Split(out.String(), "\n"), "This device was revoked.") {
		t.Fatalf("output %q must hold the line \"This device was revoked.\" and nothing about who revoked it", out.String())
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

	key, created, _, err := ensureDeviceKey(client, &bytes.Buffer{})
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

	key, created, _, err := ensureDeviceKey(client, &out)
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

			key, created, _, err := ensureDeviceKey(client, &bytes.Buffer{})
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

	_, created, _, err := ensureDeviceKey(client, &bytes.Buffer{})
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

	if _, _, _, err := ensureDeviceKey(client, &out); err != nil {
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

// refreshAnswering serves both routes a login uses: the refresh answers 200
// with a token and a deadline, or the given error, and the config route counts
// how often the CLI asks for an approval page.
func refreshAnswering(t *testing.T, status int, code string) (*device.Client, *int) {
	t.Helper()
	configAsks := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/cli/config":
			configAsks++
			_, _ = w.Write([]byte(`{"approval_url":"https://app.example.invalid/cli/authorize"}`))
		case "/v1/cli/token":
			if status == http.StatusOK {
				_, _ = w.Write([]byte(`{"device_token":"pz-d-dt-token","expires_in":600,"reauth_by":"2026-11-18T18:53:19Z"}`))
				return
			}
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":{"code":"` + code + `"}}`))
		}
	}))
	t.Cleanup(srv.Close)
	return device.NewClient(srv.URL), &configAsks
}

// A key that is still approved needs no approval: the first refresh succeeds,
// so the login hands back that token and asks the Switch for no approval page,
// so it can print no link.
func TestLoginWithAnApprovedKeyNeedsNoApprovalPage(t *testing.T) {
	useTempHome(t)
	old, _ := enrolledKey(t)
	answerPrompts(t, false, "")
	client, configAsks := refreshAnswering(t, http.StatusOK, "")

	appURL, key, created, approved, err := prepareLogin(client, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("prepareLogin: %v", err)
	}
	if approved == nil || approved.Value != "pz-d-dt-token" {
		t.Fatalf("approved = %+v, want the token the first refresh returned", approved)
	}
	if created || key.ID() != old.ID() || appURL != "" || *configAsks != 0 {
		t.Fatalf("created=%v sameKey=%v appURL=%q configAsks=%d; want the key reused and no approval page asked",
			created, key.ID() == old.ID(), appURL, *configAsks)
	}
}

// A key whose sign-in expired, or whose device is not enrolled yet, still goes
// the approval way: it asks for the page and returns no token.
func TestLoginWithAnExpiredKeyStillAsksForTheApprovalPage(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		code   string
	}{
		{"sign-in expired", http.StatusUnauthorized, "REAUTH_REQUIRED"},
		{"not enrolled", http.StatusNotFound, "NOT_FOUND"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			useTempHome(t)
			enrolledKey(t)
			answerPrompts(t, false, "")
			client, configAsks := refreshAnswering(t, tc.status, tc.code)

			appURL, _, _, approved, err := prepareLogin(client, &bytes.Buffer{})
			if err != nil || approved != nil || appURL == "" || *configAsks != 1 {
				t.Fatalf("appURL=%q approved=%v configAsks=%d err=%v; want the approval path", appURL, approved != nil, *configAsks, err)
			}
		})
	}
}

// The message for a device that is already approved names the deadline when
// there is one, and prints no link.
func TestReportAlreadyApproved(t *testing.T) {
	deadline := time.Date(2026, 11, 18, 18, 53, 19, 0, time.UTC)
	var out bytes.Buffer
	reportAlreadyApproved(&out, &device.Token{ReauthBy: &deadline})
	if got := out.String(); !strings.Contains(got, "This device is already approved until 2026-11-18T18:53:19Z.") {
		t.Fatalf("output = %q, want the deadline", got)
	}
	out.Reset()
	reportAlreadyApproved(&out, &device.Token{})
	if got := out.String(); !strings.Contains(got, "This device is already approved.") || strings.Contains(got, "until") {
		t.Fatalf("output = %q, want the plain line with no deadline", got)
	}
	if strings.Contains(out.String(), "http") {
		t.Fatalf("output %q holds a link", out.String())
	}
}
