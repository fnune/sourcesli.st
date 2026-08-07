package web_test

import (
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
	return recorder
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
			{Name: "bash", Suite: "unstable", Synopsis: "GNU Bourne Again SHell"},
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
		bugs: map[int32]query.Bug{
			1136390: {
				Number: 1136390, Package: "bash", Title: "terminal resize duplicates the command line",
				Severity: "minor", Status: "pending",
				Arrival: time.Date(2026, time.May, 13, 10, 9, 3, 0, time.UTC),
			},
		},
		bugList: []query.ListBugsForPackageRow{
			{Number: 1136390, Title: "terminal resize duplicates the command line", Severity: "minor"},
			{Number: 999999, Title: "data loss on upgrade", Severity: "grave"},
		},
		bugTags:  []string{"upstream"},
		openBugs: 2,
		lastSync: &syncedAt,
		top: []query.ListTopPackagesRow{
			{Name: "firefox-esr", Suite: "unstable", Synopsis: "Mozilla Firefox web browser", Votes: 51115},
			{Name: "nautilus", Suite: "unstable", Synopsis: "file manager for GNOME", Votes: 48302},
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
		"popularity contest",
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

func TestSearchWithNoQueryDoesNotTouchTheDatabase(t *testing.T) {
	t.Parallel()

	queries := populatedQueries()
	response := get(t, newTestServer(t, queries), "/search", nil)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	if queries.searchCalledWith.Query != "" {
		t.Error("an empty query should not run a search")
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
		`href="/b/1136390"`,
		"tracker.debian.org/pkg/bash",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("package page does not contain %q", want)
		}
	}
}

// Release-critical bugs are styled differently, and that styling is the only
// signal on the page that a bug is serious.
func TestPackagePageMarksReleaseCriticalBugs(t *testing.T) {
	t.Parallel()

	response := get(t, newTestServer(t, populatedQueries()), "/p/unstable/bash", nil)

	if !strings.Contains(response.Body.String(), "bg-danger") {
		t.Error("a grave bug should be marked as release critical")
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
	if strings.Contains(body, `href="/p/experimental/bash"`) {
		t.Error("a suite without the package should not be a link")
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

func TestBugPageRenders(t *testing.T) {
	t.Parallel()

	response := get(t, newTestServer(t, populatedQueries()), "/b/1136390", nil)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	body := response.Body.String()
	for _, want := range []string{
		"terminal resize duplicates the command line",
		"13 May 2026",
		"upstream",
		"bugs.debian.org/1136390",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("bug page does not contain %q", want)
		}
	}
}

// The reader's suite has to survive the trip through a bug page rather than
// being reset to unstable by a hard-coded link.
func TestBugPageKeepsTheReadersSuite(t *testing.T) {
	t.Parallel()

	response := get(t, newTestServer(t, populatedQueries()), "/b/1136390?suite=stable", nil)

	if !strings.Contains(response.Body.String(), `href="/p/stable/bash"`) {
		t.Error("the package link should carry the suite the reader came with")
	}
}

func TestUnknownBugIsNotFound(t *testing.T) {
	t.Parallel()

	response := get(t, newTestServer(t, populatedQueries()), "/b/424242", nil)

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", response.Code)
	}
}

func TestMalformedBugNumberIsRejected(t *testing.T) {
	t.Parallel()

	for _, target := range []string{"/b/abc", "/b/0", "/b/-1", "/b/99999999999999999999"} {
		response := get(t, newTestServer(t, populatedQueries()), target, nil)
		if response.Code != http.StatusNotFound {
			t.Errorf("%s status = %d, want 404", target, response.Code)
		}
	}
}

// A NULL timestamp arrives as the zero time rather than a nil pointer, and a
// struct is always truthy in a template. Without the isZeroTime guard this page
// renders "1 January 0001".
func TestBugPageOmitsMissingDates(t *testing.T) {
	t.Parallel()

	queries := populatedQueries()
	queries.bugs[1136390] = query.Bug{Number: 1136390, Title: "no dates recorded", Severity: "normal"}

	response := get(t, newTestServer(t, queries), "/b/1136390", nil)

	if strings.Contains(response.Body.String(), "0001") {
		t.Error("a missing date should be omitted, not rendered as year 1")
	}
}

func TestImageChooserRendersWithoutADatabase(t *testing.T) {
	t.Parallel()

	response := get(t, newTestServer(t, populatedQueries()), "/images", nil)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	body := response.Body.String()
	for _, want := range []string{"Live image with GNOME", "iso-hybrid/", "SHA512SUMS"} {
		if !strings.Contains(body, want) {
			t.Errorf("chooser does not contain %q", want)
		}
	}
}

func TestImageChooserReflectsTheAnswers(t *testing.T) {
	t.Parallel()

	response := get(t, newTestServer(t, populatedQueries()),
		"/images?arch=amd64&flavour=live&desktop=xfce", nil)

	body := response.Body.String()
	if !strings.Contains(body, "current-live") {
		t.Error("a live choice should link the live directory")
	}
	if !strings.Contains(body, "amd64-xfce.iso") {
		t.Error("the filename pattern should name the chosen desktop")
	}
	// The form has to come back showing what was asked for, or the answer and
	// the controls disagree on the next change.
	if !regexp.MustCompile(`value="xfce"\s+checked`).MatchString(body) {
		t.Error("the form should keep the chosen desktop checked")
	}
}

// The first visit carries no answers, so the User-Agent supplies the
// architecture and the page leads with a finished recommendation instead of a
// questionnaire.
func TestImageChooserDetectsTheArchitecture(t *testing.T) {
	t.Parallel()

	response := get(t, newTestServer(t, populatedQueries()), "/images",
		map[string]string{"User-Agent": "Mozilla/5.0 (X11; Linux aarch64; rv:141.0) Gecko/20100101"})

	body := response.Body.String()
	if !strings.Contains(body, "Network installer for arm64") {
		t.Error("an ARM browser should get an ARM recommendation without being asked")
	}
}

// The exact filename comes from the versions ingest observed, so the download
// link points at a file that exists today rather than at a pattern.
func TestImageChooserUsesTheIngestedVersions(t *testing.T) {
	t.Parallel()

	queries := populatedQueries()
	queries.isoVersions = []query.ListISOVersionsRow{{Channel: "stable", Version: "13.7.0"}}

	body := get(t, newTestServer(t, queries), "/images", nil).Body.String()

	if !strings.Contains(body, "debian-live-13.7.0-amd64-gnome.iso") {
		t.Error("the page should offer the exact file for the ingested version")
	}
}

// Only stable and oldstable have released images, so the answer names the
// release it is offering and the page says outright that testing and unstable
// have none.
func TestImageChooserNamesTheRelease(t *testing.T) {
	t.Parallel()

	body := get(t, newTestServer(t, populatedQueries()), "/images", nil).Body.String()

	if !strings.Contains(body, "Debian 13 (trixie), the stable release") {
		t.Error("the answer should name the release it recommends")
	}
	if !strings.Contains(body, "Testing and unstable have no released images") {
		t.Error("the page should say that testing and unstable have no images")
	}
}

// Non-live images carry no desktop, so the desktop cards grey out under an
// overlay whose call to action is a plain label for the live radio: clicking
// it switches the flavour with no JavaScript beyond htmx's refresh.
func TestDesktopOverlayOffersTheLiveSwitch(t *testing.T) {
	t.Parallel()

	body := get(t, newTestServer(t, populatedQueries()), "/images?flavour=netinst", nil).Body.String()

	if !strings.Contains(body, `for="flavour-live"`) {
		t.Error("the overlay should offer switching to the live image via a label")
	}
	if !strings.Contains(body, `id="flavour-live"`) {
		t.Error("the live radio needs the id the overlay label points at")
	}
}

// The reader changes a radio and the answer updates; there is no submit
// button outside noscript. The form must still work as a plain GET form.
func TestImageChooserFormWorksWithoutJavaScript(t *testing.T) {
	t.Parallel()

	body := get(t, newTestServer(t, populatedQueries()), "/images", nil).Body.String()

	if !strings.Contains(body, `action="/images" method="get"`) {
		t.Error("the chooser must be a real form")
	}
	if !strings.Contains(body, "<noscript>") {
		t.Error("the no-JavaScript path needs its submit button")
	}
	if strings.Contains(body, "Show me the image") {
		t.Error("the always-visible submit button should be gone")
	}
}

func TestDatabaseFailureBecomesAServerError(t *testing.T) {
	t.Parallel()

	queries := populatedQueries()
	queries.failWith = errors.New("connection refused")

	for _, target := range []string{"/", "/search?q=bash", "/p/unstable/bash", "/b/1136390"} {
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
