package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// probeSegment fills a {wildcard} in a route pattern. It is deliberately
// not a plausible id: a value like "directory" or "stats" would match a
// more specific sibling pattern and silently test the wrong route.
const probeSegment = "population-probe-x"

// concreteProbePath turns a ServeMux pattern into a path that can be
// requested. Patterns here only ever carry a trailing {name...} wildcard,
// so truncating at the brace and appending a filler segment is enough.
func concreteProbePath(pattern string) string {
	if i := strings.Index(pattern, "{"); i >= 0 {
		return pattern[:i] + probeSegment
	}
	return pattern
}

// TestAPIRoutePopulationIsAuthenticatedOrAllowlisted is the regression
// gate GHSA-3hfw-5v8p-p588 asks for. It does not carry a list of routes:
// it runs the Handler's own registerRoutes, which is the single function
// the live mux is built from, and checks every route that function
// declares. A route added later without authentication and without an
// entry in unauthenticatedAPIRoutes fails this test without anyone
// having to remember to update it.
//
// Each route is checked twice. The declaration check catches an
// unauthenticated route in the table. The behavioural check sends a real
// credential-less request through ServeHTTP and asserts the status, which
// is what catches a route that is declared authenticated but wired wrong.
func TestAPIRoutePopulationIsAuthenticatedOrAllowlisted(t *testing.T) {
	h, _ := testAPI(t)
	specs := h.routeSpecs()

	// A refactor that silently stopped registering anything would make
	// every assertion below vacuous, so pin the population size loosely.
	if len(specs) < 20 {
		t.Fatalf("only %d /api routes enumerated; the route table looks broken", len(specs))
	}

	seen := make(map[string]bool, len(specs))
	for _, spec := range specs {
		key := spec.Method + " " + spec.Pattern
		if seen[key] {
			t.Errorf("route %s registered twice", key)
		}
		seen[key] = true

		t.Run(strings.TrimSpace(key), func(t *testing.T) {
			if !strings.HasPrefix(spec.Pattern, "/api") {
				t.Fatalf("pattern %q is outside the /api surface this test governs", spec.Pattern)
			}

			reason, allowlisted := unauthenticatedAPIRoutes[spec.Pattern]
			if !spec.Authenticated && !allowlisted {
				t.Fatalf("route %q is registered without authentication and is not in "+
					"unauthenticatedAPIRoutes; either wrap it in authMiddleware or add it to "+
					"the allowlist with a justification", key)
			}
			if !spec.Authenticated && strings.TrimSpace(reason) == "" {
				t.Fatalf("allowlist entry %q has no justification", spec.Pattern)
			}

			method := spec.Method
			if method == "" {
				method = http.MethodGet
			}
			req := httptest.NewRequest(method, concreteProbePath(spec.Pattern), nil)
			// A mutating request with no Origin is refused by the CSRF
			// check before it ever reaches the auth layer. Set a
			// same-origin header so this assertion stays about
			// authentication and nothing else; the CSRF behaviour has
			// its own test.
			if isMutatingMethod(method) {
				req.Header.Set("Origin", "http://"+req.Host)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)

			if spec.Authenticated && w.Code != http.StatusUnauthorized {
				t.Errorf("%s without a credential returned %d, want 401", key, w.Code)
			}
			if !spec.Authenticated && w.Code == http.StatusUnauthorized {
				t.Errorf("%s is allowlisted as unauthenticated but returned 401", key)
			}
		})
	}
}

// TestUnauthenticatedAllowlistHasNoStaleEntries closes the other
// direction: an allowlist entry naming a route that no longer exists
// would quietly pre-authorise that pattern if it were ever added back
// for a different purpose.
func TestUnauthenticatedAllowlistHasNoStaleEntries(t *testing.T) {
	h, _ := testAPI(t)

	registered := make(map[string]bool)
	for _, spec := range h.routeSpecs() {
		registered[spec.Pattern] = true
	}

	for pattern, reason := range unauthenticatedAPIRoutes {
		if !registered[pattern] {
			t.Errorf("unauthenticatedAPIRoutes names %q, which is not a registered route", pattern)
		}
		if strings.TrimSpace(reason) == "" {
			t.Errorf("unauthenticatedAPIRoutes entry %q carries no justification", pattern)
		}
	}
}

// registrationCall matches a call to Handle or HandleFunc on any
// receiver, so renaming the variable from mux to router or apiMux does
// not dodge the scan. The old check was a case-sensitive substring on
// the literal "mux.Handle(", which two of those three would have slipped
// past silently.
var registrationCall = regexp.MustCompile(`(?i)\b[A-Za-z_][A-Za-z0-9_]*\s*\.\s*Handle(Func)?\s*\(`)

// patternLiteral pulls the first double-quoted string out of a line,
// which for a registration call is the ServeMux pattern.
var patternLiteral = regexp.MustCompile(`"([^"]*)"`)

// goSourceFiles lists the non-test .go files in dir.
func goSourceFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		out = append(out, filepath.Join(dir, name))
	}
	if len(out) == 0 {
		t.Fatalf("no non-test Go files found in %s; the scan would pass vacuously", dir)
	}
	return out
}

// TestRoutesAreOnlyRegisteredThroughTheRegistry keeps the enumeration
// above honest. routeSpecs can only see routes that go through
// routeRegistry.handle, so a route registered any other way is invisible
// to the population test. Two places can create one:
//
//   - internal/api, where routes.go is the only file allowed to register
//     anything at all;
//   - cmd/iplayer-arr, which owns the outer mux and legitimately mounts
//     the /api/ subtree on this handler. Anything more specific than
//     "/api/" registered there would be dispatched by the outer mux and
//     never reach our registry, so those are refused.
//
// Known limit: this cannot see dispatch performed inside an existing
// handler, for example a handler that parses r.URL.Path itself and
// branches. Nothing in the tree does that today, but the scan is a guard
// against the ordinary mistake, not a proof that no such path exists.
func TestRoutesAreOnlyRegisteredThroughTheRegistry(t *testing.T) {
	// internal/api: routes.go is the only file that may register.
	for _, path := range goSourceFiles(t, ".") {
		if filepath.Base(path) == "routes.go" {
			continue
		}
		forEachRegistration(t, path, func(line string, lineNo int, pattern string) {
			t.Errorf("%s:%d registers a route outside routes.go; the route-population "+
				"test cannot see it. Register it through routeRegistry.handle instead:\n\t%s",
				path, lineNo, strings.TrimSpace(line))
		})
	}

	// cmd/iplayer-arr: the outer mux may mount "/api/" and nothing
	// narrower. A pattern like "/api/foo" there would shadow our subtree.
	const cmdDir = "../../cmd/iplayer-arr"
	for _, path := range goSourceFiles(t, cmdDir) {
		forEachRegistration(t, path, func(line string, lineNo int, pattern string) {
			if !strings.HasPrefix(pattern, "/api") {
				return // /newznab/, /sabnzbd/, /health, / are not ours
			}
			if pattern == "/api/" {
				return // the expected mount point for this handler
			}
			t.Errorf("%s:%d registers %q on the outer mux, which shadows the /api subtree "+
				"and is invisible to the route-population test. Register it through "+
				"routeRegistry.handle in internal/api/routes.go instead:\n\t%s",
				path, lineNo, pattern, strings.TrimSpace(line))
		})
	}
}

// forEachRegistration calls fn for every Handle/HandleFunc call site in
// path, passing the line, its 1-based number, and the first string
// literal on it (the pattern, or "" when there is none).
func forEachRegistration(t *testing.T, path string, fn func(line string, lineNo int, pattern string)) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	for i, line := range strings.Split(string(data), "\n") {
		code := line
		if idx := strings.Index(code, "//"); idx >= 0 {
			code = code[:idx] // ignore prose that merely mentions Handle(
		}
		if !registrationCall.MatchString(code) {
			continue
		}
		pattern := ""
		if m := patternLiteral.FindStringSubmatch(code); m != nil {
			pattern = m[1]
		}
		fn(line, i+1, pattern)
	}
}

// TestTrailingSlashKeepsRoutesReachable pins the regression the abandoned
// v1.6.0 mux refactor shipped: http.ServeMux does not treat /api/status/
// as /api/status, so around ten paths fell through to the /api/ catch-all
// and started answering 404. ServeHTTP folds the trailing slash first.
func TestTrailingSlashKeepsRoutesReachable(t *testing.T) {
	paths := []string{
		"/api/status",
		"/api/config",
		"/api/downloads",
		"/api/downloads/directory",
		"/api/history",
		"/api/history/stats",
		"/api/overrides",
		"/api/system",
		"/api/logs",
		"/api/healthz",
	}

	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			h, _ := testAPI(t)

			plain := httptest.NewRecorder()
			h.ServeHTTP(plain, httptest.NewRequest("GET", path+"?apikey=test-api-key", nil))

			slashed := httptest.NewRecorder()
			h.ServeHTTP(slashed, httptest.NewRequest("GET", path+"/?apikey=test-api-key", nil))

			if plain.Code != slashed.Code {
				t.Errorf("GET %s = %d but GET %s/ = %d; the trailing slash changed the route",
					path, plain.Code, path, slashed.Code)
			}
			if slashed.Code == http.StatusNotFound {
				t.Errorf("GET %s/ returned 404; it should reach the same handler as %s", path, path)
			}
		})
	}
}

// TestNormaliseTrailingSlashDoesNotMutateTheOriginal: the request and its
// URL are shared with the server and with any handler that inspects them,
// so the fold has to work on a copy.
func TestNormaliseTrailingSlashDoesNotMutateTheOriginal(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/status/", nil)
	out := normaliseTrailingSlash(req)

	if req.URL.Path != "/api/status/" {
		t.Errorf("original request path was mutated to %q", req.URL.Path)
	}
	if out.URL.Path != "/api/status" {
		t.Errorf("normalised path = %q, want /api/status", out.URL.Path)
	}
	if out.URL == req.URL {
		t.Error("normalised request shares its URL pointer with the original")
	}
}

// TestUnknownAPIPathIs404NotUnauthorized: answering 401 on an unknown
// path would turn the API into a route oracle, letting an unauthenticated
// caller separate real gated routes from typos by status code alone.
func TestUnknownAPIPathIs404NotUnauthorized(t *testing.T) {
	h, _ := testAPI(t)

	for _, path := range []string{"/api/does-not-exist", "/api/", "/api"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, w.Code)
		}
		if !strings.Contains(w.Body.String(), "not found") {
			t.Errorf("GET %s body = %q, want the JSON not-found shape", path, w.Body.String())
		}
	}
}

// TestConcurrentFirstRequestsDoNotRaceOnTheMux reproduces the condition
// that made the abandoned v1.6.0 branch fail go test -race: a lazily
// built router with a plain nil check races when two requests arrive
// before it exists. Meaningful only under -race, which is what CI runs.
func TestConcurrentFirstRequestsDoNotRaceOnTheMux(t *testing.T) {
	h, _ := testAPI(t)

	const callers = 16
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(callers)
	for i := 0; i < callers; i++ {
		go func() {
			defer wg.Done()
			<-start
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("GET", "/api/healthz", nil))
			if w.Code != http.StatusOK {
				t.Errorf("status = %d, want 200", w.Code)
			}
		}()
	}
	close(start)
	wg.Wait()
}

// TestHealthzIsUnauthenticatedAndDiscloseNothing pins the single
// allowlisted route: it must answer without a credential, and it must not
// grow a payload that reports on the instance.
func TestHealthzIsUnauthenticatedAndDiscloseNothing(t *testing.T) {
	h, _ := testAPI(t)

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/healthz", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 without a credential", w.Code)
	}
	if got := strings.TrimSpace(w.Body.String()); got != `{"ok":true}` {
		t.Errorf("body = %q, want exactly {\"ok\":true}", got)
	}
}
