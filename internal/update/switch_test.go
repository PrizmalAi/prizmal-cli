package update

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// switchStub stands in for the Switch. It answers the newest-release route with
// whatever respond returns, and records the Authorization header of every
// request, in order.
type switchStub struct {
	srv *httptest.Server

	mu    sync.Mutex
	auths []string
	paths []string
}

func newSwitchStub(t *testing.T, respond func(auth string, call int) (int, string)) *switchStub {
	t.Helper()
	s := &switchStub{}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.auths = append(s.auths, r.Header.Get("Authorization"))
		s.paths = append(s.paths, r.Method+" "+r.URL.Path)
		call := len(s.auths)
		s.mu.Unlock()
		status, body := respond(r.Header.Get("Authorization"), call)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *switchStub) requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.auths...)
}

// requestedPaths copies the request log under the same lock the handler appends
// with, so a test reading it never races the handler goroutine.
func (s *switchStub) requestedPaths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.paths...)
}

func answers(status int, body string) func(string, int) (int, string) {
	return func(string, int) (int, string) { return status, body }
}

// releaseList is the public release list the check read before it asked the
// Switch. It counts its requests and answers a tag for the release API and a
// version for the cask.
func releaseList(t *testing.T, tag string) *int {
	t.Helper()
	hits := new(int)
	serve(t, func(w http.ResponseWriter, r *http.Request) {
		*hits++
		if strings.HasSuffix(r.URL.Path, ".rb") {
			_, _ = w.Write([]byte("cask \"prizmal\" do\n  version \"" + strings.TrimPrefix(tag, "v") + "\"\nend\n"))
			return
		}
		_, _ = w.Write([]byte(`{"tag_name":"` + tag + `"}`))
	})
	return hits
}

// offerFor is the action a newer release earns inst: Offer when this install
// can run its own upgrade, Warn when it cannot, which is every install on
// Windows. Whether an install can upgrade is covered where CanUpgrade is
// tested. These tests assert the version.
func offerFor(inst Install) Action {
	if inst.CanUpgrade() {
		return Offer
	}
	return Warn
}

func withSwitch(s *switchStub, credential func() string) Switch {
	return Switch{BaseURL: s.srv.URL, Credential: credential}
}

func noCredential() string { return "" }

const switchRoute = "GET /v1/cli/releases/latest"

func TestSwitchAnswerNewerThanInstalledOffersTheUpgrade(t *testing.T) {
	setHome(t, t.TempDir())
	pinGoBin(t, "/x/bin")
	list := releaseList(t, "v0.0.1")
	sw := newSwitchStub(t, answers(200, `{"version":"0.2.0"}`))
	o := check(now, goInst, stateStore{dir: t.TempDir()}, LatestFor(goInst, withSwitch(sw, noCredential)))
	if o.Action != offerFor(goInst) || o.Latest != "v0.2.0" {
		t.Fatalf("outcome = %+v, want %v v0.2.0", o, offerFor(goInst))
	}
	if got := sw.requestedPaths(); len(got) != 1 || got[0] != switchRoute {
		t.Errorf("Switch requests = %v, want one %q", got, switchRoute)
	}
	if *list != 0 {
		t.Errorf("the release list was read %d times; the Switch answered", *list)
	}
}

func TestSwitchAnswerEqualOrOlderOffersNothing(t *testing.T) {
	setHome(t, t.TempDir())
	for _, v := range []string{"0.1.2", "v0.1.2", "0.1.1", "0.0.9"} {
		list := releaseList(t, "v9.9.9")
		sw := newSwitchStub(t, answers(200, `{"version":"`+v+`"}`))
		o := check(now, archInst, stateStore{dir: t.TempDir()}, LatestFor(archInst, withSwitch(sw, noCredential)))
		if o.Action != None {
			t.Errorf("answer %s: outcome = %+v, want None", v, o)
		}
		if *list != 0 {
			t.Errorf("answer %s: the release list was read", v)
		}
	}
}

// An empty version means nothing is announced yet, and the Switch is
// authoritative: a newer release on the public list stays unannounced.
func TestSwitchEmptyVersionOffersNothingAndSkipsTheReleaseList(t *testing.T) {
	setHome(t, t.TempDir())
	for name, inst := range map[string]Install{"tag": archInst, "cask": brewInst} {
		list := releaseList(t, "v9.9.9")
		sw := newSwitchStub(t, answers(200, `{"version":""}`))
		o := check(now, inst, stateStore{dir: t.TempDir()}, LatestFor(inst, withSwitch(sw, noCredential)))
		if o.Action != None {
			t.Errorf("%s: outcome = %+v, want None", name, o)
		}
		if *list != 0 {
			t.Errorf("%s: an empty answer read the release list %d times", name, *list)
		}
	}
}

// A Switch with no such route answers 404, so a user on an older Switch still
// learns about a release from the public list.
func TestSwitch404FallsBackToTheReleaseList(t *testing.T) {
	setHome(t, t.TempDir())
	pinGoBin(t, "/x/bin")
	for name, inst := range map[string]Install{"tag": goInst, "cask": brewInst} {
		list := releaseList(t, "v0.3.0")
		sw := newSwitchStub(t, answers(404, `{"error":{"message":"Route not found"}}`))
		o := check(now, inst, stateStore{dir: t.TempDir()}, LatestFor(inst, withSwitch(sw, noCredential)))
		if o.Action != offerFor(inst) || o.Latest != "v0.3.0" {
			t.Errorf("%s: outcome = %+v, want %v v0.3.0 from the list", name, o, offerFor(inst))
		}
		if *list != 1 {
			t.Errorf("%s: release list reads = %d, want 1", name, *list)
		}
	}
}

func TestSwitchUnreachableFallsBackToTheReleaseList(t *testing.T) {
	setHome(t, t.TempDir())
	list := releaseList(t, "v0.3.0")
	sw := Switch{BaseURL: "http://127.0.0.1:1", Credential: noCredential}
	o := check(now, archInst, stateStore{dir: t.TempDir()}, LatestFor(archInst, sw))
	if o.Action != Warn || o.Latest != "v0.3.0" || *list != 1 {
		t.Fatalf("outcome %+v with %d list reads, want Warn v0.3.0 from the list", o, *list)
	}
}

// With no Switch to ask, the check reads the release list as it always did.
func TestNoSwitchReadsTheReleaseList(t *testing.T) {
	setHome(t, t.TempDir())
	list := releaseList(t, "v0.3.0")
	o := check(now, archInst, stateStore{dir: t.TempDir()}, LatestFor(archInst, Switch{}))
	if o.Action != Warn || *list != 1 {
		t.Fatalf("outcome %+v with %d list reads, want Warn from the list", o, *list)
	}
}

// A refused credential is retried once with none, and the anonymous answer
// counts.
func TestSwitch401RetriesOnceWithNoCredential(t *testing.T) {
	setHome(t, t.TempDir())
	list := releaseList(t, "v9.9.9")
	sw := newSwitchStub(t, func(auth string, _ int) (int, string) {
		if auth != "" {
			return 401, `{"error":{"message":"Invalid API key."}}`
		}
		return 200, `{"version":"0.2.0"}`
	})
	o := check(now, archInst, stateStore{dir: t.TempDir()}, LatestFor(archInst, withSwitch(sw, func() string { return "pz-test-credential" })))
	if o.Action != Warn || o.Latest != "v0.2.0" {
		t.Fatalf("outcome = %+v, want Warn v0.2.0 from the anonymous retry", o)
	}
	got := sw.requests()
	if len(got) != 2 || got[0] != "Bearer pz-test-credential" || got[1] != "" {
		t.Fatalf("Authorization headers = %q, want the credential once, then none", got)
	}
	if *list != 0 {
		t.Errorf("the release list was read %d times", *list)
	}
}

// A second 401 reads as an unavailable route (an older Switch refuses a
// keyless request before it routes), so the check falls back, and it retries
// no more than once.
func TestSwitch401TwiceFallsBackAfterOneRetry(t *testing.T) {
	setHome(t, t.TempDir())
	list := releaseList(t, "v0.3.0")
	sw := newSwitchStub(t, answers(401, `{"error":{"message":"API key is required."}}`))
	o := check(now, archInst, stateStore{dir: t.TempDir()}, LatestFor(archInst, withSwitch(sw, func() string { return "pz-test-credential" })))
	if o.Action != Warn || o.Latest != "v0.3.0" {
		t.Fatalf("outcome = %+v, want Warn v0.3.0 from the list", o)
	}
	if got := sw.requests(); len(got) != 2 {
		t.Errorf("Switch requests = %d, want 2 (the credential, then none)", len(got))
	}
	if *list != 1 {
		t.Errorf("release list reads = %d, want 1", *list)
	}
}

// With no credential there is nothing to drop, so a 401 is not retried.
func TestSwitch401WithNoCredentialIsNotRetried(t *testing.T) {
	setHome(t, t.TempDir())
	releaseList(t, "v0.3.0")
	sw := newSwitchStub(t, answers(401, `{}`))
	_ = check(now, archInst, stateStore{dir: t.TempDir()}, LatestFor(archInst, withSwitch(sw, noCredential)))
	if got := sw.requests(); len(got) != 1 {
		t.Errorf("Switch requests = %d, want 1", len(got))
	}
}

func TestSwitchGetsTheCredentialTheCLIHolds(t *testing.T) {
	setHome(t, t.TempDir())
	sw := newSwitchStub(t, answers(200, `{"version":"0.1.2"}`))
	for name, tc := range map[string]struct {
		credential func() string
		want       string
	}{
		"a key":      {func() string { return "pz-test-key" }, "Bearer pz-test-key"},
		"none":       {noCredential, ""},
		"no lookup":  {nil, ""},
		"whitespace": {func() string { return "  " }, ""},
	} {
		_, _ = LatestFor(archInst, withSwitch(sw, tc.credential))()
		got := sw.requests()
		if last := got[len(got)-1]; last != tc.want {
			t.Errorf("%s: Authorization = %q, want %q", name, last, tc.want)
		}
	}
}

// The credential is looked up when the check is due, not when it is built, so
// a check the 24-hour cache answers never mints a device token.
func TestSwitchCredentialIsNotLookedUpWhileTheCacheAnswers(t *testing.T) {
	setHome(t, t.TempDir())
	sw := newSwitchStub(t, answers(200, `{"version":"0.2.0"}`))
	lookups := 0
	latest := LatestFor(archInst, withSwitch(sw, func() string { lookups++; return "pz-test-key" }))
	state := stateStore{dir: t.TempDir()}
	check(now, archInst, state, latest)
	check(now.Add(time.Hour), archInst, state, latest)
	if lookups != 1 {
		t.Errorf("credential lookups = %d, want 1", lookups)
	}
}

func TestSwitchAnswerIsReadOnceADay(t *testing.T) {
	setHome(t, t.TempDir())
	sw := newSwitchStub(t, answers(200, `{"version":"0.2.0"}`))
	latest := LatestFor(archInst, withSwitch(sw, noCredential))
	state := stateStore{dir: t.TempDir()}
	check(now, archInst, state, latest)
	if o := check(now.Add(23*time.Hour), archInst, state, latest); o.Action != Warn {
		t.Errorf("the cached answer was not replayed: %+v", o)
	}
	if got := len(sw.requests()); got != 1 {
		t.Fatalf("23 hours later: %d Switch requests, want 1", got)
	}
	check(now.Add(25*time.Hour), archInst, state, latest)
	if got := len(sw.requests()); got != 2 {
		t.Fatalf("25 hours later: %d Switch requests, want 2", got)
	}
}

// A Switch that has the route and cannot answer is not a Switch without the
// route. Reading the public list then would announce a release the Switch has
// not activated, so the read fails and runs again in an hour.
func TestSwitchServerErrorDoesNotReadTheReleaseList(t *testing.T) {
	setHome(t, t.TempDir())
	for _, status := range []int{500, 502, 503, 429} {
		list := releaseList(t, "v9.9.9")
		sw := newSwitchStub(t, answers(status, `{"error":{"message":"unavailable"}}`))
		if got, err := LatestFor(archInst, withSwitch(sw, noCredential))(); err == nil {
			t.Errorf("HTTP %d: got %q, nil; want an error", status, got)
		}
		if *list != 0 {
			t.Errorf("HTTP %d: the release list was read", status)
		}
	}
}

func TestSwitchAnswerThatIsNotJSONFails(t *testing.T) {
	setHome(t, t.TempDir())
	list := releaseList(t, "v9.9.9")
	sw := newSwitchStub(t, answers(200, `<html>`))
	if got, err := LatestFor(archInst, withSwitch(sw, noCredential))(); err == nil {
		t.Fatalf("got %q, nil; want an error", got)
	}
	if *list != 0 {
		t.Error("a garbled 200 read the release list")
	}
}

// A version that is no release tag never reaches the upgrade command.
func TestSwitchVersionThatIsNoTagIsNotOffered(t *testing.T) {
	setHome(t, t.TempDir())
	sw := newSwitchStub(t, answers(200, `{"version":"1.2.3; rm -rf /"}`))
	o := check(now, archInst, stateStore{dir: t.TempDir()}, LatestFor(archInst, withSwitch(sw, noCredential)))
	if o.Action != None {
		t.Fatalf("outcome = %+v, want None", o)
	}
}

// A testing process asks a Switch on this machine and no other, so a test that
// never names one cannot reach the real deployment.
func TestTestingProcessAsksOnlyALoopbackSwitch(t *testing.T) {
	setHome(t, t.TempDir())
	list := releaseList(t, "v0.3.0")
	t.Setenv(envTesting, "testing")
	t.Setenv(testURLEnv, apiBase) // the stub release host the seam names

	lookups := 0
	remote := Switch{BaseURL: "https://switch.example.invalid", Credential: func() string { lookups++; return "pz-test-key" }}
	o := check(now, archInst, stateStore{dir: t.TempDir()}, LatestFor(archInst, remote))
	if lookups != 0 {
		t.Errorf("a testing process looked up a credential for a Switch that is not on this machine")
	}
	if o.Action != Warn || *list != 1 {
		t.Errorf("outcome %+v with %d list reads, want Warn from the list", o, *list)
	}

	local := newSwitchStub(t, answers(200, `{"version":"0.2.0"}`))
	o = check(now, archInst, stateStore{dir: t.TempDir()}, LatestFor(archInst, withSwitch(local, noCredential)))
	if o.Action != Warn || o.Latest != "v0.2.0" || len(local.requests()) != 1 {
		t.Errorf("outcome %+v with %d requests to the local Switch, want Warn v0.2.0 from one", o, len(local.requests()))
	}
}

// A test that names only a Switch, through the config file or --url, still
// asks it. The guard reads the host the fetch uses, not only the release host.
func TestRunCheckInATestingProcessAsksTheSwitchItWasGiven(t *testing.T) {
	setHome(t, t.TempDir())
	list := releaseList(t, "v9.9.9")
	stub := newSwitchStub(t, answers(200, `{"version":"0.2.0"}`))
	t.Setenv(envTesting, "testing")
	t.Setenv(testURLEnv, "")
	o := RunCheck(now, archInst, withSwitch(stub, noCredential))
	if o.Action != Warn || o.Latest != "v0.2.0" || len(stub.requests()) != 1 {
		t.Fatalf("outcome %+v with %d Switch requests, want Warn v0.2.0 from one", o, len(stub.requests()))
	}
	if *list != 0 {
		t.Errorf("the release list was read %d times", *list)
	}
}

// With no release host named, a Switch that cannot answer never sends the check
// on to a real release list.
func TestRunCheckInATestingProcessNeverReadsAnUnnamedReleaseList(t *testing.T) {
	setHome(t, t.TempDir())
	list := releaseList(t, "v9.9.9") // stands where the real list would be
	stub := newSwitchStub(t, answers(404, `{}`))
	t.Setenv(envTesting, "testing")
	t.Setenv(testURLEnv, "")
	o := RunCheck(now, archInst, withSwitch(stub, noCredential))
	if o.Action != None || *list != 0 {
		t.Fatalf("outcome %+v with %d release list reads, want None and none", o, *list)
	}
}

// With nothing named at all the check does nothing.
func TestRunCheckInATestingProcessWithNoHostDoesNothing(t *testing.T) {
	setHome(t, t.TempDir())
	list := releaseList(t, "v9.9.9")
	t.Setenv(envTesting, "testing")
	t.Setenv(testURLEnv, "")
	lookups := 0
	sw := Switch{BaseURL: "https://switch.example.invalid", Credential: func() string { lookups++; return "pz-test-key" }}
	if o := RunCheck(now, archInst, sw); o.Action != None || *list != 0 || lookups != 0 {
		t.Fatalf("outcome %+v, %d list reads, %d credential lookups; want None and none", o, *list, lookups)
	}
}
