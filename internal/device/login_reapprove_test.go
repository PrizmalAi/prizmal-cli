package device

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// reapproveServer answers every refresh with a success, as the Switch does for
// a key that is still approved. Refreshes up to laterAfter-1 carry the old
// deadline; from laterAfter on, the deadline moves a week later, as it does
// once someone approves the device again. status, when set for a poll number,
// replaces the success with that status.
type reapproveServer struct {
	mu         sync.Mutex
	polls      int
	pollTimes  []time.Time
	laterAfter int
	old, later time.Time
	status     map[int]int
	clock      *fakeClock
}

func (s *reapproveServer) handler(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	s.polls++
	n := s.polls
	s.pollTimes = append(s.pollTimes, s.clock.now())
	s.mu.Unlock()
	if code, ok := s.status[n]; ok {
		w.WriteHeader(code)
		_, _ = w.Write([]byte(`{"error":{"code":"RATE_LIMITED"}}`))
		return
	}
	deadline := s.old
	if n >= s.laterAfter {
		deadline = s.later
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"device_token": "pz-d-dt-token", "expires_in": 600, "reauth_by": deadline.Format(time.RFC3339),
	})
}

func newReapproveServer(t *testing.T, laterAfter int, status map[int]int) (*reapproveServer, *httptest.Server) {
	t.Helper()
	old := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	s := &reapproveServer{
		laterAfter: laterAfter, old: old, later: old.Add(7 * 24 * time.Hour), status: status,
		clock: &fakeClock{t: time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)},
	}
	srv := httptest.NewServer(http.HandlerFunc(s.handler))
	t.Cleanup(srv.Close)
	return s, srv
}

func reapproveOptions(s *reapproveServer, out *strings.Builder) LoginOptions {
	return LoginOptions{
		ApprovalURL: "https://app.example.invalid/cli/authorize",
		Interval:    2 * time.Second,
		Timeout:     time.Minute,
		Sleep:       s.clock.sleep,
		Now:         s.clock.now,
		OpenBrowser: func(string) error { return nil },
		Confirm:     func(done <-chan struct{}) (bool, error) { <-done; return false, nil },
		Out:         out,
		Reapprove:   &s.old,
	}
}

// A key that is still approved refreshes fine all the time, so a success proves
// nothing. The wait ends only on a refresh whose deadline is later than the one
// the login started from, and only then says it was approved in the browser.
func TestLoginReapproveWaitsForALaterDeadline(t *testing.T) {
	s, srv := newReapproveServer(t, 4, nil)
	var out strings.Builder
	tok, err := Login(NewClient(srv.URL), mustKey(t), reapproveOptions(s, &out))
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if tok.ReauthBy == nil || !tok.ReauthBy.Equal(s.later) {
		t.Fatalf("deadline = %v, want %v", tok.ReauthBy, s.later)
	}
	if s.polls != 4 {
		t.Fatalf("polled %d times, want 4: refreshes with the old deadline are not an approval", s.polls)
	}
	if !strings.Contains(out.String(), "To approve this device, open:") {
		t.Fatalf("no approval link:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "Approved in your browser.") {
		t.Fatalf("no approval line:\n%s", out.String())
	}
}

// The probe that found the key approved stamped the Switch's 30-second
// throttle, so the wait sleeps before its first refresh instead of asking at
// once.
func TestLoginReapproveDoesNotRefreshAtOnce(t *testing.T) {
	s, srv := newReapproveServer(t, 1, nil)
	start := s.clock.now()
	var out strings.Builder
	if _, err := Login(NewClient(srv.URL), mustKey(t), reapproveOptions(s, &out)); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if got := s.pollTimes[0].Sub(start); got < 2*time.Second {
		t.Fatalf("first refresh came %v after the link, want at least one interval", got)
	}
}

// A 429 is the throttle, not a failure: the wait sleeps and asks again.
func TestLoginReapproveSleepsThroughRateLimit(t *testing.T) {
	s, srv := newReapproveServer(t, 3, map[int]int{1: http.StatusTooManyRequests, 2: http.StatusTooManyRequests})
	var out strings.Builder
	tok, err := Login(NewClient(srv.URL), mustKey(t), reapproveOptions(s, &out))
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if !tok.ReauthBy.Equal(s.later) {
		t.Fatalf("deadline = %v", tok.ReauthBy)
	}
}

// Nobody approves: the wait ends at its timeout with the same message as a
// first login.
func TestLoginReapproveTimesOut(t *testing.T) {
	s, srv := newReapproveServer(t, 1<<30, nil)
	var out strings.Builder
	opts := reapproveOptions(s, &out)
	opts.Timeout = 10 * time.Second
	_, err := Login(NewClient(srv.URL), mustKey(t), opts)
	if err == nil || !strings.Contains(err.Error(), "timed out waiting for approval") {
		t.Fatalf("err = %v, want a timeout", err)
	}
}

func mustKey(t *testing.T) *Key {
	t.Helper()
	k, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	return k
}
