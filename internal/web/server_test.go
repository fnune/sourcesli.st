package web_test

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	sourceslist "github.com/fnune/sourcesli.st"
	"github.com/fnune/sourcesli.st/internal/store/query"
	"github.com/fnune/sourcesli.st/internal/web"
)

// These are end-to-end through the HTTP layer: a real router, the real
// templates, and real rendering. Only the database is a fake, because a
// handler's job is to turn rows into a page and that is what is being checked.
func newTestServer(t *testing.T, queries web.Queries) http.Handler {
	t.Helper()

	templates, err := fs.Sub(sourceslist.Web, "web/templates")
	if err != nil {
		t.Fatalf("reading templates: %v", err)
	}
	assets, err := fs.Sub(sourceslist.Web, "web/assets")
	if err != nil {
		t.Fatalf("reading assets: %v", err)
	}

	// Discard logs: a test that fails should fail on an assertion, not be found
	// by reading noise on stderr.
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	server, err := web.NewServer(queries, templates, assets, logger)
	if err != nil {
		t.Fatalf("building the server: %v", err)
	}
	return server.Handler()
}

func get(t *testing.T, handler http.Handler, target string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()

	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	// Otherwise a test asking whether a word is on the page searches the inlined
	// stylesheet too, and finds it among the colour names and class selectors.
	recorder.Body = bytes.NewBuffer(withoutStylesheet(recorder.Body.Bytes()))
	return recorder
}

func withoutStylesheet(document []byte) []byte {
	opening, closing := []byte("<style>"), []byte("</style>")

	start := bytes.Index(document, opening)
	if start < 0 {
		return document
	}
	end := bytes.Index(document[start:], closing)
	if end < 0 {
		return document
	}

	return append(document[:start+len(opening):start+len(opening)], document[start+end:]...)
}

func populatedQueries() *fakeQueries {
	syncedAt := time.Date(2026, time.August, 1, 9, 0, 0, 0, time.UTC)
	return &fakeQueries{
		suites: []query.Suite{
			{Name: "unstable", Codename: "sid", Role: "unstable", SortOrder: 40},
			{Name: "stable", Codename: "trixie", Version: "13", Role: "stable", SortOrder: 20},
		},
		search: []query.SearchPackagesRow{
			{Name: "bash", Kind: "binary", Suite: "unstable", Version: "5.3-3", Synopsis: "GNU Bourne Again SHell"},
		},
		completions: []query.CompletePackageNamesRow{
			{Name: "bash", Suite: "unstable", Version: "5.3-3", Synopsis: "GNU Bourne Again SHell"},
		},
		suggestions: []query.SuggestSimilarPackagesRow{
			{Name: "firefox-esr", Suite: "unstable", Synopsis: "Mozilla Firefox web browser"},
		},
		binaries: map[string][]query.GetBinaryPackageRow{
			"bash": {{
				Name: "bash", Version: "5.3-3", Architecture: "amd64", Suite: "unstable",
				SourceName: "bash", Synopsis: "GNU Bourne Again SHell",
				Description: "GNU Bourne Again SHell\n The GNU Bourne Again SHell.",
				Section:     "shells",
			}},
		},
		sources: map[string]query.SourcePackage{
			"bash": {Name: "bash", Version: "5.3-3", Suite: "unstable", MaintainerName: "Matthias Klose"},
		},
		bugList: []query.ListBugsForPackageRow{
			{
				Number: 1136390, Package: "bash", Source: "bash",
				Title: "terminal resize duplicates the command line", Severity: "minor",
				Status: "pending", SubmitterName: "Witold Baryluk", Tags: []string{"upstream"},
				Arrival:      time.Date(2026, time.May, 13, 10, 9, 3, 0, time.UTC),
				LastModified: time.Date(2026, time.June, 2, 8, 0, 0, 0, time.UTC),
			},
			{Number: 999999, Title: "data loss on upgrade", Severity: "grave"},
		},
		openBugs: 2,
		lastSync: &syncedAt,
		top: []query.ListTopPackagesRow{
			{Name: "firefox-esr", Suite: "unstable", Synopsis: "Mozilla Firefox web browser", Votes: 51115},
			{Name: "nautilus", Suite: "unstable", Synopsis: "file manager for GNOME", Votes: 48302},
		},
		packageVersions: []query.ListPackageVersionsRow{
			{Name: "gnome-shell", Version: "48.3-2"},
			{Name: "plasma-desktop", Version: "4:6.3.4-1"},
		},
		suiteVersions: []query.ListPackageSuitesRow{
			{Suite: "stable", Version: "5.2-1"},
			{Suite: "unstable", Version: "5.3-3"},
		},
		screenshots: map[string]query.GetScreenshotRow{
			"firefox-esr": {ContentType: "image/png", Image: []byte("not-really-a-png")},
		},
	}
}

func TestIndexRenders(t *testing.T) {
	t.Parallel()

	response := get(t, newTestServer(t, populatedQueries()), "/", nil)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	body := response.Body.String()
	for _, want := range []string{"Search Debian packages", "unstable", "sid", "Download Debian"} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %q", want)
		}
	}
}

// The front page offers something to browse cold: applications a person would
// recognise, each a link into the site, with a mirrored thumbnail when one
// exists.
func TestIndexListsPopularApplications(t *testing.T) {
	t.Parallel()

	queries := populatedQueries()
	queries.top[0].HasScreenshot = true

	body := get(t, newTestServer(t, queries), "/", nil).Body.String()

	for _, want := range []string{
		"Popular applications",
		`href="/p/unstable/firefox-esr"`,
		`src="/screenshots/firefox-esr"`,
		// The list is a heuristic of ours over a denylist of ours. Crediting
		// popcon alone would present our editing as Debian's measurement.
		"our call, not Debian's",
		"popcon.debian.org",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("index does not contain %q", want)
		}
	}
}

// A heading above the field said what the field already is, and it vanished on
// the first keystroke. The field carries its own label instead, on every page
// the form appears on.
func TestSearchFieldCarriesItsOwnLabel(t *testing.T) {
	t.Parallel()

	handler := newTestServer(t, populatedQueries())

	for _, target := range []string{"/", "/search?q=bash"} {
		body := get(t, handler, target, nil).Body.String()
		if !strings.Contains(body, `placeholder="Search Debian packages`) {
			t.Errorf("%s does not label the search field in its placeholder", target)
		}
	}
}

// Thumbnails are served from our own database with cache headers; hotlinking
// screenshots.debian.net would make every visitor a load on Debian.
func TestScreenshotsAreServedFromOurDatabase(t *testing.T) {
	t.Parallel()

	response := get(t, newTestServer(t, populatedQueries()), "/screenshots/firefox-esr", nil)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	if got := response.Header().Get("Content-Type"); got != "image/png" {
		t.Errorf("Content-Type = %q, want the stored type", got)
	}
	if response.Header().Get("Cache-Control") == "" {
		t.Error("thumbnails should carry a cache header")
	}

	if code := get(t, newTestServer(t, populatedQueries()), "/screenshots/nosuchthing", nil).Code; code != http.StatusNotFound {
		t.Errorf("missing screenshot status = %d, want 404", code)
	}
}

func TestFooterStatesWhenTheDataIsFrom(t *testing.T) {
	t.Parallel()

	response := get(t, newTestServer(t, populatedQueries()), "/", nil)

	if !strings.Contains(response.Body.String(), "1 August 2026") {
		t.Error("the footer should state the date the data is from")
	}
}

func TestEveryPageFooterLinksDebiansDonationPage(t *testing.T) {
	t.Parallel()

	server := newTestServer(t, populatedQueries())

	for _, path := range []string{"/", "/p/stable/firefox-esr", "/search?q=firefox"} {
		body := get(t, server, path, nil).Body.String()
		if !strings.Contains(body, "https://www.debian.org/donations") {
			t.Errorf("%s does not link to Debian's donation page", path)
		}
	}
}

// An empty database must say so rather than presenting a site with no packages
// as though it were current.
func TestFooterSaysSoWhenNothingHasBeenIngested(t *testing.T) {
	t.Parallel()

	queries := populatedQueries()
	queries.lastSync = nil

	response := get(t, newTestServer(t, queries), "/", nil)

	if !strings.Contains(response.Body.String(), "No data has been loaded yet") {
		t.Error("the footer should say that nothing has been ingested")
	}
}

// Stable is the default suite everywhere: it is what most visitors run, and
// the suite tabs put the rest one click away.
func TestDefaultSuiteIsStable(t *testing.T) {
	t.Parallel()

	queries := populatedQueries()
	get(t, newTestServer(t, queries), "/search?q=bash", nil)

	if queries.searchCalledWith.Suite != "stable" {
		t.Errorf("default search suite = %q, want stable", queries.searchCalledWith.Suite)
	}
}

// The front page carries no parameter to hold the suite, so without a cookie a
// reload puts a reader of unstable back into stable. Only a suite we serve is
// remembered, wherever the value came from.
func TestTheChosenSuiteSurvivesAReload(t *testing.T) {
	t.Parallel()

	handler := newTestServer(t, populatedQueries())

	response := get(t, handler, "/search?q=bash&suite=unstable", nil)
	var remembered *http.Cookie
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == "suite" {
			remembered = cookie
		}
	}
	if remembered == nil || remembered.Value != "unstable" {
		t.Fatalf("the chosen suite was not remembered: %+v", remembered)
	}
	if !remembered.HttpOnly || remembered.SameSite != http.SameSiteLaxMode {
		t.Error("the cookie should be unreadable to scripts and not travel cross-site")
	}

	queries := populatedQueries()
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	request.AddCookie(remembered)
	recorder := httptest.NewRecorder()
	newTestServer(t, queries).ServeHTTP(recorder, request)

	if queries.topCalledWith.Suite != "unstable" {
		t.Errorf("the front page read %q, want the remembered suite", queries.topCalledWith.Suite)
	}

	queries = populatedQueries()
	request = httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	request.AddCookie(&http.Cookie{Name: "suite", Value: "nosuchsuite"})
	newTestServer(t, queries).ServeHTTP(httptest.NewRecorder(), request)

	if queries.topCalledWith.Suite != "stable" {
		t.Errorf("an unknown suite was trusted: %q", queries.topCalledWith.Suite)
	}
}

func TestSearchRendersResults(t *testing.T) {
	t.Parallel()

	queries := populatedQueries()
	response := get(t, newTestServer(t, queries), "/search?q=bash&suite=unstable", nil)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	if !strings.Contains(response.Body.String(), `href="/p/unstable/bash"`) {
		t.Error("results should link to the package page")
	}
	if queries.searchCalledWith.Query != "bash" || queries.searchCalledWith.Suite != "unstable" {
		t.Errorf("search called with %+v, want the query and suite from the URL", queries.searchCalledWith)
	}
}

// A full page offers more instead of silently stopping; the hard cap is the
// only point where the page says it cut the list off.
func TestSearchOffersMoreResults(t *testing.T) {
	t.Parallel()

	queries := populatedQueries()
	queries.search = make([]query.SearchPackagesRow, 50)
	for i := range queries.search {
		queries.search[i] = query.SearchPackagesRow{Name: "bash", Kind: "binary", Suite: "unstable"}
	}

	body := get(t, newTestServer(t, queries), "/search?q=bash&suite=unstable", nil).Body.String()

	if !strings.Contains(body, "n=100") || !strings.Contains(body, "Show more") {
		t.Error("a full page should link to a larger one")
	}

	queries.search = make([]query.SearchPackagesRow, 250)
	for i := range queries.search {
		queries.search[i] = query.SearchPackagesRow{Name: "bash", Kind: "binary", Suite: "unstable"}
	}
	body = get(t, newTestServer(t, queries), "/search?q=bash&suite=unstable&n=250", nil).Body.String()

	if strings.Contains(body, "Show more") {
		t.Error("the cap reached should not offer more")
	}
	if !strings.Contains(body, "Only the first 250 matches are shown") {
		t.Error("hitting the cap should be said out loud")
	}
}

// An empty query has nothing to report, so it goes back to the front page
// rather than rendering a results page with no results and no query in it. The
// suite the reader had chosen survives the trip, but only a suite we serve: the
// redirect target is a name from the suite list, never one from the URL.
func TestSearchWithNoQueryGoesBackToTheFrontPage(t *testing.T) {
	t.Parallel()

	queries := populatedQueries()

	for target, want := range map[string]string{
		"/search":                      "/",
		"/search?q=":                   "/",
		"/search?q=%20&suite=unstable": "/?suite=unstable",
		"/search?q=&suite=stable":      "/",
		"/search?q=&suite=nosuchsuite": "/",
		"/search?q=&suite=..%2Fevil":   "/",
	} {
		response := get(t, newTestServer(t, queries), target, nil)
		if response.Code != http.StatusSeeOther {
			t.Errorf("%s status = %d, want 303", target, response.Code)
		}
		if got := response.Header().Get("Location"); got != want {
			t.Errorf("%s redirected to %q, want %q", target, got, want)
		}
	}

	if queries.searchCalledWith.Query != "" {
		t.Error("an empty query should not run a search")
	}
}

// Full-text search matches whole words, so a misspelt name matches nothing at
// all. Trigram similarity over the same names turns that dead end into the
// package the reader meant.
func TestSearchSuggestsSimilarNamesWhenNothingMatched(t *testing.T) {
	t.Parallel()

	queries := populatedQueries()
	queries.search = nil

	body := get(t, newTestServer(t, queries), "/search?q=faiafox&suite=unstable", nil).Body.String()

	if !strings.Contains(body, "Did you mean") {
		t.Error("a search with no results should offer what the reader may have meant")
	}
	if !strings.Contains(body, `href="/p/unstable/firefox-esr"`) {
		t.Error("the suggestions should link to the package pages")
	}
	if queries.suggestCalledWith.Query != "faiafox" || queries.suggestCalledWith.Suite != "unstable" {
		t.Errorf("suggestions asked for %+v, want the query and suite from the URL", queries.suggestCalledWith)
	}
}

// Suggestions cost a second query, so they are only worth running when the
// search itself came back empty.
func TestSearchWithResultsAsksForNoSuggestions(t *testing.T) {
	t.Parallel()

	queries := populatedQueries()
	get(t, newTestServer(t, queries), "/search?q=bash&suite=unstable", nil)

	if queries.suggestCalledWith.Query != "" {
		t.Error("a search that found something should not also ask for suggestions")
	}
}

func TestCompletionReturnsLinks(t *testing.T) {
	t.Parallel()

	response := get(t, newTestServer(t, populatedQueries()), "/complete?q=ba&suite=unstable", nil)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	if !strings.Contains(response.Body.String(), `href="/p/unstable/bash"`) {
		t.Error("completions should link to package pages")
	}
	if !strings.Contains(response.Body.String(), "5.3-3") {
		t.Error("completions should show the version in the suite being searched")
	}
}

// One character is a legitimate prefix although not a legitimate package name,
// and completion must start on the first keystroke.
func TestCompletionAcceptsASingleCharacter(t *testing.T) {
	t.Parallel()

	response := get(t, newTestServer(t, populatedQueries()), "/complete?q=b&suite=unstable", nil)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	if !strings.Contains(response.Body.String(), `href="/p/unstable/bash"`) {
		t.Error("a single-character prefix should complete")
	}
}

// A half-typed query is not a client error. It has no completions and that is
// all: no 400, no log noise, no database round trip.
func TestCompletionIgnoresInputThatCannotBeAPackageName(t *testing.T) {
	t.Parallel()

	for _, target := range []string{"/complete?q=", "/complete?q=../etc/passwd", "/complete?q=UPPER"} {
		response := get(t, newTestServer(t, populatedQueries()), target, nil)
		if response.Code != http.StatusOK {
			t.Errorf("%s status = %d, want 200", target, response.Code)
		}
		if strings.TrimSpace(response.Body.String()) != "" {
			t.Errorf("%s should return nothing, got %q", target, response.Body.String())
		}
	}
}

func TestPackagePageRendersEverything(t *testing.T) {
	t.Parallel()

	response := get(t, newTestServer(t, populatedQueries()), "/p/unstable/bash", nil)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	body := response.Body.String()
	for _, want := range []string{
		"GNU Bourne Again SHell",
		"amd64",
		"5.3-3",
		"Matthias Klose",
		`href="https://bugs.debian.org/1136390"`,
		"tracker.debian.org/pkg/bash",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("package page does not contain %q", want)
		}
	}
}

// The list is the whole of what this site says about a bug, so what the bug
// page used to show has to be on the row: there is nowhere else left to put it.
func TestBugRowsCarryTheWholeSnapshot(t *testing.T) {
	t.Parallel()

	body := get(t, newTestServer(t, populatedQueries()), "/p/unstable/bash", nil).Body.String()

	for _, want := range []string{
		"terminal resize duplicates the command line",
		"pending",
		"Witold Baryluk",
		"13 May 2026",
		"2 Jun 2026",
		"upstream",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the bug row does not carry %q", want)
		}
	}
}

// A NULL timestamp arrives as the zero time rather than a nil pointer, and a
// struct is always truthy in a template. Unguarded, a bug with no dates renders
// "1 January 0001".
func TestBugRowsOmitMissingDates(t *testing.T) {
	t.Parallel()

	queries := populatedQueries()
	queries.bugList = []query.ListBugsForPackageRow{
		{Number: 1136390, Title: "no dates recorded", Severity: "normal"},
	}

	body := get(t, newTestServer(t, queries), "/p/unstable/bash", nil).Body.String()

	if strings.Contains(body, "0001") {
		t.Error("a missing date should be omitted, not rendered as year 1")
	}
	for _, unwanted := range []string{"reported", "updated"} {
		if strings.Contains(body, unwanted) {
			t.Errorf("a bug with no dates should not be labelled %q", unwanted)
		}
	}
}

// A bug filed against another binary of the same source, or against the source
// alone, says so. Repeating the name of the package whose page this is would be
// noise on every other row.
func TestBugRowsNameOnlyTheUnexpectedPackage(t *testing.T) {
	t.Parallel()

	queries := populatedQueries()
	queries.bugList = []query.ListBugsForPackageRow{
		{Number: 1, Title: "against this package", Severity: "normal", Package: "bash", Source: "bash"},
		{Number: 2, Title: "against a sibling", Severity: "normal", Package: "bash-doc", Source: "bash"},
	}

	body := get(t, newTestServer(t, queries), "/p/unstable/bash", nil).Body.String()

	if !strings.Contains(body, "bash-doc") {
		t.Error("a bug against another binary of the source should name it")
	}
	if strings.Contains(body, "against <span class=\"font-mono\">bash</span>") {
		t.Error("a bug against this very package should not repeat its name")
	}
}

// Severity is what the list is ordered by, and colour is how a reader takes
// that order in. Each severity gets its own mark; one the BTS does not define
// gets the bare one rather than borrowing another severity's colour.
func TestBugRowsMarkEachSeverityInItsOwnColour(t *testing.T) {
	t.Parallel()

	queries := populatedQueries()
	queries.bugList = []query.ListBugsForPackageRow{
		{Number: 1, Title: "a", Severity: "grave"},
		{Number: 2, Title: "b", Severity: "wishlist"},
		{Number: 3, Title: "c", Severity: "spicy"},
	}

	body := get(t, newTestServer(t, queries), "/p/unstable/bash", nil).Body.String()

	for _, want := range []string{`"severity severity-grave"`, `"severity severity-wishlist"`} {
		if !strings.Contains(body, want) {
			t.Errorf("the list does not mark %s", want)
		}
	}
	if strings.Contains(body, "severity-spicy") {
		t.Error("a severity the BTS does not define should not be given a colour of its own")
	}
	if !strings.Contains(body, `"severity"`) {
		t.Error("an unrecognised severity should still get the bare mark")
	}
}

func TestPackagePageWithNoSourceStillRenders(t *testing.T) {
	t.Parallel()

	queries := populatedQueries()
	queries.sources = map[string]query.SourcePackage{}

	response := get(t, newTestServer(t, queries), "/p/unstable/bash", nil)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 even without a source package", response.Code)
	}
}

// Search offers source packages too. A source whose binaries all carry other
// names has no binary rows under its own name, and its page is the source
// record rather than a 404.
func TestSourceOnlyPackageRendersFromItsSourceRecord(t *testing.T) {
	t.Parallel()

	queries := populatedQueries()
	queries.sources["srconly"] = query.SourcePackage{
		Name: "srconly", Version: "1.0-1", Suite: "unstable", MaintainerName: "A Maintainer",
	}

	response := get(t, newTestServer(t, queries), "/p/unstable/srconly", nil)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for a source-only package", response.Code)
	}
	body := response.Body.String()
	if !strings.Contains(body, "A Maintainer") {
		t.Error("the page should render the source record")
	}
	if !strings.Contains(body, "No binary packages in this suite") {
		t.Error("the page should say there are no binaries rather than imply an empty archive")
	}
}

func TestPackagePageShowsDependencies(t *testing.T) {
	t.Parallel()

	queries := populatedQueries()
	rows := queries.binaries["bash"]
	rows[0].Depends = "base-files (>= 2.1.12), debianutils (>= 5.6-0.1)"
	rows[0].Recommends = "bash-completion"

	body := get(t, newTestServer(t, queries), "/p/unstable/bash", nil).Body.String()

	for _, want := range []string{
		"Dependencies",
		`href="/p/unstable/base-files"`,
		"(&gt;= 2.1.12)",
		`href="/p/unstable/bash-completion"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("package page does not contain %q", want)
		}
	}
}

// The suite tabs answer "where does this exist and at what version" up front,
// instead of letting a click discover a 404.
func TestPackagePageLinksTheOtherSuites(t *testing.T) {
	t.Parallel()

	queries := populatedQueries()
	queries.suites = append(queries.suites,
		query.Suite{Name: "experimental", Codename: "rc-buggy", Role: "experimental", SortOrder: 50})

	body := get(t, newTestServer(t, queries), "/p/unstable/bash", nil).Body.String()

	if !strings.Contains(body, `href="/p/stable/bash"`) {
		t.Error("a suite that carries the package should be a link")
	}
	if !strings.Contains(body, "5.2-1") {
		t.Error("the tab should carry the version in that suite")
	}
	// The picker in the masthead lists every suite; the tabs are the package's
	// own switcher and only offer the suites that carry it.
	tabs := body[strings.Index(body, `aria-label="Suites"`):]
	tabs = tabs[:strings.Index(tabs, "</nav>")]
	if strings.Contains(tabs, `href="/p/experimental/bash"`) {
		t.Error("a suite without the package should not be a tab link")
	}
}

func TestPackagePageBugFiltersReachTheQuery(t *testing.T) {
	t.Parallel()

	queries := populatedQueries()
	get(t, newTestServer(t, queries), "/p/unstable/bash?bugs=closed&severity=grave", nil)

	if queries.bugsCalledWith.Status != "closed" || queries.bugsCalledWith.Severity != "grave" {
		t.Errorf("bugs listed with %+v, want the status and severity from the URL", queries.bugsCalledWith)
	}
}

// The default view is open bugs: a package page is about the present, and an
// unfiltered dump of every closed bug would bury it.
func TestPackagePageBugsDefaultToOpen(t *testing.T) {
	t.Parallel()

	queries := populatedQueries()
	get(t, newTestServer(t, queries), "/p/unstable/bash", nil)

	if queries.bugsCalledWith.Status != "open" {
		t.Errorf("default bug status = %q, want open", queries.bugsCalledWith.Status)
	}
	if queries.bugsCalledWith.Severity != "" {
		t.Errorf("default bug severity = %q, want unfiltered", queries.bugsCalledWith.Severity)
	}
}

func TestPackagePageOffersMoreBugs(t *testing.T) {
	t.Parallel()

	queries := populatedQueries()
	queries.bugList = make([]query.ListBugsForPackageRow, 25)
	for i := range queries.bugList {
		queries.bugList[i] = query.ListBugsForPackageRow{Number: int32(i + 1), Severity: "normal"}
	}

	body := get(t, newTestServer(t, queries), "/p/unstable/bash", nil).Body.String()

	if !strings.Contains(body, "n=75") || !strings.Contains(body, "Show more") {
		t.Error("a full page of bugs should link to a larger one")
	}

	// The "show more" link must reach the query as a larger limit.
	get(t, newTestServer(t, queries), "/p/unstable/bash?n=75", nil)
	if queries.bugsCalledWith.MaxResults != 75 {
		t.Errorf("bugs listed with limit %d, want 75", queries.bugsCalledWith.MaxResults)
	}
}

func TestUnknownPackageIsNotFound(t *testing.T) {
	t.Parallel()

	response := get(t, newTestServer(t, populatedQueries()), "/p/unstable/nosuchthing", nil)

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", response.Code)
	}
	if !strings.Contains(response.Body.String(), "No package named nosuchthing") {
		t.Error("the page should name what was not found")
	}
}

// A path that cannot be a package name must be refused by the parser before it
// reaches a query, and must not be echoed back into the page.
func TestMalformedPackageNameIsRejected(t *testing.T) {
	t.Parallel()

	response := get(t, newTestServer(t, populatedQueries()), "/p/unstable/NotAPackage", nil)

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", response.Code)
	}
	if strings.Contains(response.Body.String(), "NotAPackage") {
		t.Error("the rejected name should not be echoed into the page")
	}
}

// The bug page this site used to serve is gone, but the links to it are not.
func TestTheOldBugPathRedirectsToTheBugTracker(t *testing.T) {
	t.Parallel()

	response := get(t, newTestServer(t, populatedQueries()), "/b/1136390", nil)

	if response.Code != http.StatusMovedPermanently {
		t.Fatalf("status = %d, want 301", response.Code)
	}
	if got := response.Header().Get("Location"); got != "https://bugs.debian.org/1136390" {
		t.Errorf("redirected to %q, want the bug in the tracking system", got)
	}
}

// The redirect sends the reader off this site, so what goes into its location
// has to be a bug number and nothing else.
func TestMalformedBugNumberIsRejected(t *testing.T) {
	t.Parallel()

	for _, target := range []string{"/b/abc", "/b/0", "/b/-1", "/b/99999999999999999999"} {
		response := get(t, newTestServer(t, populatedQueries()), target, nil)
		if response.Code != http.StatusNotFound {
			t.Errorf("%s status = %d, want 404", target, response.Code)
		}
	}
}

// The chooser is the front page, and the path it used to live at keeps working
// for the links already pointing at it.
func TestTheOldImagesPathRedirectsToTheFrontPage(t *testing.T) {
	t.Parallel()

	response := get(t, newTestServer(t, populatedQueries()), "/images", nil)

	if response.Code != http.StatusMovedPermanently {
		t.Fatalf("status = %d, want 301", response.Code)
	}
	if got := response.Header().Get("Location"); got != "/" {
		t.Errorf("redirected to %q, want /", got)
	}
}

// The catalogue is the page: no questions, no state, and every image Debian
// publishes for these machines listed with what it looks like and what it costs.
func TestFrontPageListsEveryImage(t *testing.T) {
	t.Parallel()

	body := get(t, newTestServer(t, populatedQueries()), "/", nil).Body.String()

	for _, want := range []string{
		"Live images", "GNOME 48.3", "KDE Plasma 6.3.4", "No desktop", "Network installer", "Full DVD",
		"64-bit PC", "64-bit ARM", "32-bit PC",
		"iso-hybrid/", "iso-cd/", "iso-dvd/",
		"/d/amd64-live-gnome", "/d/arm64-netinst", "/d/i386-dvd",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the front page does not contain %q", want)
		}
	}
	if strings.Contains(body, "<form id=\"images-form\"") {
		t.Error("the catalogue replaced the questionnaire; there is nothing to submit")
	}
	// What the live images share is stated once for the section, not on each of
	// the six tiles that differ only in which desktop they carry.
	if strings.Count(body, "Boot from USB without touching your disk") != 1 {
		t.Error("the shared description should appear once, on the section")
	}
}

// Debian builds live images for 64-bit PCs only. The page says so where it is
// true rather than offering a choice it then has to overrule.
func TestFrontPageSaysWhereLiveImagesAreMissing(t *testing.T) {
	t.Parallel()

	body := get(t, newTestServer(t, populatedQueries()), "/", nil).Body.String()

	if !strings.Contains(body, "Live images are 64-bit PC only") {
		t.Error("the ARM group should say why it lists no live images")
	}
	if !strings.Contains(body, "Debian 12, supported until June 2028") {
		t.Error("the 32-bit group should say which release it is on")
	}
}

// The User-Agent is a guess, so it orders the page and labels one group. It
// hides nothing: every architecture is listed for a reader whose browser lies.
func TestFrontPageLeadsWithTheDetectedHardware(t *testing.T) {
	t.Parallel()

	body := get(t, newTestServer(t, populatedQueries()), "/",
		map[string]string{"User-Agent": "Mozilla/5.0 (X11; Linux aarch64; rv:141.0) Gecko/20100101"}).Body.String()

	if !strings.Contains(body, "looks like this computer") {
		t.Error("the detected hardware should be marked")
	}
	arm, pc := strings.Index(body, "64-bit ARM"), strings.Index(body, "64-bit PC")
	if arm > pc {
		t.Error("the detected hardware should be listed first")
	}
	if !strings.Contains(body, "amd64-live-gnome") {
		t.Error("a guess must not remove images from the page")
	}
}

// The exact filename comes from the versions ingest observed, so every link
// points at a file that exists today rather than at a pattern.
func TestImagesUseTheIngestedVersions(t *testing.T) {
	t.Parallel()

	queries := populatedQueries()
	queries.isoVersions = []query.ListISOVersionsRow{{Channel: "stable", Version: "13.7.0"}}

	body := get(t, newTestServer(t, queries), "/", nil).Body.String()

	if !strings.Contains(body, "debian-live-13.7.0-amd64-gnome.iso") {
		t.Error("the page should offer the exact file for the ingested version")
	}
}

// One image has a page of its own: the file it lands as, and how to check that
// what landed is what Debian published.
func TestImagePageCarriesTheChecksums(t *testing.T) {
	t.Parallel()

	response := get(t, newTestServer(t, populatedQueries()), "/d/amd64-live-xfce", nil)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	body := response.Body.String()
	for _, want := range []string{
		"debian-live-13.6.0-amd64-xfce.iso",
		"SHA512SUMS.sign",
		"gpg --verify",
		"Debian 13 (trixie), stable",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the image page does not contain %q", want)
		}
	}
}

// An image identifier is a path segment, so anything that is not one of the
// twelve is a page that does not exist rather than a request we try to satisfy.
func TestUnknownImageIsNotFound(t *testing.T) {
	t.Parallel()

	handler := newTestServer(t, populatedQueries())

	for _, target := range []string{"/d/arm64-live-gnome", "/d/nonsense", "/d/..%2Fetc%2Fpasswd"} {
		if code := get(t, handler, target, nil).Code; code != http.StatusNotFound {
			t.Errorf("%s status = %d, want 404", target, code)
		}
	}
}

func TestDatabaseFailureBecomesAServerError(t *testing.T) {
	t.Parallel()

	queries := populatedQueries()
	queries.failWith = errors.New("connection refused")

	for _, target := range []string{"/", "/search?q=bash", "/p/unstable/bash"} {
		response := get(t, newTestServer(t, queries), target, nil)
		if response.Code != http.StatusInternalServerError {
			t.Errorf("%s status = %d, want 500", target, response.Code)
		}
		// The error names our database. It belongs in the logs, not the page.
		if strings.Contains(response.Body.String(), "connection refused") {
			t.Errorf("%s leaked the internal error to the reader", target)
		}
	}
}

func TestHealthReportsUnavailableWhenTheDatabaseIsDown(t *testing.T) {
	t.Parallel()

	queries := populatedQueries()
	queries.failWith = errors.New("connection refused")

	response := get(t, newTestServer(t, queries), "/healthz", nil)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", response.Code)
	}
}

// An empty database is healthy. It has nothing in it, which is a data problem,
// not a reason to fail a readiness probe and refuse traffic.
func TestHealthIsOkWhenNothingHasBeenIngested(t *testing.T) {
	t.Parallel()

	queries := populatedQueries()
	queries.lastSync = nil

	response := get(t, newTestServer(t, queries), "/healthz", nil)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
}

func TestAssetsAreServed(t *testing.T) {
	t.Parallel()

	handler := newTestServer(t, populatedQueries())

	for _, asset := range []string{"/assets/app.css", "/assets/vendor/htmx.min.js"} {
		response := get(t, handler, asset, nil)
		if response.Code != http.StatusOK {
			t.Errorf("%s status = %d, want 200", asset, response.Code)
		}
		if response.Body.Len() == 0 {
			t.Errorf("%s served an empty body", asset)
		}
	}
}

// A page reads in whichever scheme the reader's system asks for, and both sets
// of values live in the stylesheet. A shade named in a template is therefore a
// hole in the dark scheme, which is why the templates name roles instead.
func TestColoursAreSemanticAndCoverBothSchemes(t *testing.T) {
	t.Parallel()

	shade := regexp.MustCompile(`\b(?:bg|text|border|divide|ring)-(?:neutral|slate|gray|zinc|stone|red|orange|amber|yellow|green|blue)-\d{2,3}\b`)

	templates, err := fs.Sub(sourceslist.Web, "web/templates")
	if err != nil {
		t.Fatalf("reading templates: %v", err)
	}
	entries, err := fs.ReadDir(templates, ".")
	if err != nil {
		t.Fatalf("listing templates: %v", err)
	}
	for _, entry := range entries {
		content, err := fs.ReadFile(templates, entry.Name())
		if err != nil {
			t.Fatalf("reading %s: %v", entry.Name(), err)
		}
		if found := shade.FindString(string(content)); found != "" {
			t.Errorf("%s names the shade %q; use a role such as bg-surface or text-ink-muted", entry.Name(), found)
		}
	}

	stylesheet, err := fs.ReadFile(sourceslist.Web, "web/assets/app.css")
	if err != nil {
		t.Fatalf("reading the stylesheet: %v", err)
	}
	if !strings.Contains(string(stylesheet), "prefers-color-scheme") {
		t.Error("the stylesheet carries no dark scheme")
	}
}

// Every page must be usable with JavaScript disabled, which means the search
// control has to be a real form pointing at a real handler.
func TestSearchWorksWithoutJavaScript(t *testing.T) {
	t.Parallel()

	body := get(t, newTestServer(t, populatedQueries()), "/", nil).Body.String()

	if !strings.Contains(body, `action="/search" method="get"`) {
		t.Error("the search form must submit on its own without htmx")
	}
	if !strings.Contains(body, `type="submit"`) {
		t.Error("the search form must have a submit button")
	}
}
