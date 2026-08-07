// Package web renders the site. It reads through the query layer and never
// writes, which is enforced at the database level by giving it a read-only role
// rather than by remembering not to.
package web

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/fnune/sourcesli.st/internal/domain"
	"github.com/fnune/sourcesli.st/internal/images"
	"github.com/fnune/sourcesli.st/internal/store/query"
	"github.com/jackc/pgx/v5"
)

const (
	searchResultLimit = 50
	searchResultMax   = 250
	completionLimit   = 8
	suggestionLimit   = 5
	bugListLimit      = 25
	bugListMax        = 200
	pageStep          = 50
	topPackagesLimit  = 12
)

// Queries is the slice of the generated query API this package uses. Declared
// here rather than imported wholesale because the interface belongs to the
// consumer: a handler that cannot see a write method cannot call one.
type Queries interface {
	ListSuites(ctx context.Context) ([]query.Suite, error)
	SearchPackages(ctx context.Context, arg query.SearchPackagesParams) ([]query.SearchPackagesRow, error)
	CompletePackageNames(
		ctx context.Context,
		arg query.CompletePackageNamesParams,
	) ([]query.CompletePackageNamesRow, error)
	SuggestSimilarPackages(
		ctx context.Context,
		arg query.SuggestSimilarPackagesParams,
	) ([]query.SuggestSimilarPackagesRow, error)
	GetBinaryPackage(ctx context.Context, arg query.GetBinaryPackageParams) ([]query.GetBinaryPackageRow, error)
	GetSourcePackage(ctx context.Context, arg query.GetSourcePackageParams) (query.SourcePackage, error)
	GetFreshVersion(ctx context.Context, arg query.GetFreshVersionParams) (query.GetFreshVersionRow, error)
	ListBugsForPackage(ctx context.Context, arg query.ListBugsForPackageParams) ([]query.ListBugsForPackageRow, error)
	CountOpenBugsForPackage(ctx context.Context, package_ string) (int64, error)
	GetLastSync(ctx context.Context) (query.GetLastSyncRow, error)
	ListISOVersions(ctx context.Context) ([]query.ListISOVersionsRow, error)
	ListTopPackages(ctx context.Context, arg query.ListTopPackagesParams) ([]query.ListTopPackagesRow, error)
	ListPackageSuites(ctx context.Context, name string) ([]query.ListPackageSuitesRow, error)
	ListPackageVersions(
		ctx context.Context,
		arg query.ListPackageVersionsParams,
	) ([]query.ListPackageVersionsRow, error)
	ListISOSizes(ctx context.Context) ([]query.ListISOSizesRow, error)
	GetScreenshot(ctx context.Context, name string) (query.GetScreenshotRow, error)
}

// Server holds the parsed templates and the query layer. Constructed once at
// startup so that a request never parses a template or reads a file.
type Server struct {
	queries   Queries
	pages     map[string]*template.Template
	fragments *template.Template
	assets    http.Handler
	logger    *slog.Logger
}

// NewServer parses the templates and returns a ready server. Template parsing
// failures are startup failures rather than 500s on an unlucky page.
func NewServer(queries Queries, templates, assets fs.FS, logger *slog.Logger) (*Server, error) {
	// Every page is parsed with the layout and the shared partials, as its own
	// template set. Parsing them all into one set is impossible because each
	// page defines "title" and "content" under the same names.
	shared := []string{"layout.html", "searchform.html", "results.html", "screen.html"}
	pageNames := []string{
		"index.html", "image.html", "search.html", "package.html", "message.html",
	}

	// Inline rather than linked: a page cannot paint before a stylesheet it
	// carries, and this one is smaller than any screenshot on the front page.
	stylesheet, err := fs.ReadFile(assets, "app.css")
	if err != nil {
		return nil, fmt.Errorf("reading the stylesheet: %w", err)
	}
	funcs := template.FuncMap{
		"stylesheet": func() template.CSS { return template.CSS(stylesheet) },
	}

	pages := make(map[string]*template.Template, len(pageNames))
	for _, page := range pageNames {
		parsed, err := template.New(page).Funcs(funcs).ParseFS(templates, append(shared, page)...)
		if err != nil {
			return nil, fmt.Errorf("parsing template %s: %w", page, err)
		}
		pages[page] = parsed
	}

	fragments, err := template.New("fragments").Funcs(funcs).ParseFS(templates, "complete.html")
	if err != nil {
		return nil, fmt.Errorf("parsing fragment templates: %w", err)
	}

	return &Server{
		queries:   queries,
		pages:     pages,
		fragments: fragments,
		assets:    http.StripPrefix("/assets/", http.FileServer(http.FS(assets))),
		logger:    logger,
	}, nil
}

// Handler returns the routes. Go's pattern syntax carries the method, so a
// route that only reads cannot be reached by a POST.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.HandleFunc("GET /search", s.handleSearch)
	mux.HandleFunc("GET /complete", s.handleComplete)
	mux.HandleFunc("GET /p/{suite}/{name}", s.handlePackage)
	mux.HandleFunc("GET /b/{number}", s.handleBugMoved)
	mux.HandleFunc("GET /d/{image}", s.handleImage)
	mux.HandleFunc("GET /images", s.handleImagesMoved)
	mux.HandleFunc("GET /screenshots/{name}", s.handleScreenshot)
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.Handle("GET /assets/", s.assets)
	return mux
}

type layoutData struct {
	DataAsOf *time.Time
	Suites   []suiteChoice
	Suite    string
	Query    string
}

// suiteChoice is one entry in the suite picker: where switching to it goes, and
// whether it is the one being read. A native select cannot hold a coloured dot,
// so the picker is a menu of links, which also makes switching a navigation
// rather than a form field nobody submits.
type suiteChoice struct {
	Name     string
	Codename string
	URL      string
	Current  bool
}

// suiteChoices builds that menu. The target is a route this package decides on
// rather than the path that was asked for: a menu built from the request echoes
// whatever a visitor put in the URL back into the page, including on the pages
// that exist to say the URL was wrong.
func suiteChoices(suites []query.Suite, current, searchQuery string) []suiteChoice {
	choices := make([]suiteChoice, 0, len(suites))
	for _, suite := range suites {
		parameters := url.Values{"suite": {suite.Name}}
		path := "/"
		if strings.TrimSpace(searchQuery) != "" {
			path = "/search"
			parameters.Set("q", searchQuery)
		}

		choices = append(choices, suiteChoice{
			Name:     suite.Name,
			Codename: suite.Codename,
			URL:      path + "?" + parameters.Encode(),
			Current:  suite.Name == current,
		})
	}
	return choices
}

type searchData struct {
	layoutData
	Results     []query.SearchPackagesRow
	Suggestions []query.SuggestSimilarPackagesRow
	// MoreN is the limit the "show more" link asks for, zero when there is
	// nothing more to ask.
	MoreN     int
	Truncated bool
}

type dependencySection struct {
	Label  string
	Groups []domain.DependencyGroup
}

type packageData struct {
	layoutData
	Name          string
	SourceName    string
	Synopsis      string
	Description   string
	Binaries      []query.GetBinaryPackageRow
	Dependencies  []dependencySection
	Source        *query.SourcePackage
	Fresh         *query.GetFreshVersionRow
	Bugs          []bugSummary
	OpenBugCount  int64
	BugStatus     string
	BugSeverity   string
	Severities    []domain.Severity
	BugsMoreN     int
	BugsTruncated bool
	SuiteVersions map[string]string
}

// requestedLimit reads the "n" parameter a "show more" link carries, clamped
// between the page's default and its hard cap.
func requestedLimit(r *http.Request, base, maximum int32) int32 {
	n, err := strconv.ParseInt(r.URL.Query().Get("n"), 10, 32)
	limit := int32(n)
	switch {
	case err != nil || limit < base:
		return base
	case limit > maximum:
		return maximum
	default:
		return limit
	}
}

// nextLimit says what the "show more" link should request: zero when the page
// is not full or the cap is reached.
func nextLimit(returned int, limit, maximum int32) int {
	if returned < int(limit) || limit >= maximum {
		return 0
	}
	return int(min(limit+pageStep, maximum))
}

// bugSummary is one row of the bug list, which is the whole of what this site
// says about a bug: there is no page behind it, only the bug tracking system.
// Dates arrive formatted because a NULL timestamp reaches Go as the zero time
// rather than a nil pointer, and a template cannot tell the two apart.
type bugSummary struct {
	Number   int32
	Title    string
	Severity string
	// SeverityKey is the severity the BTS defines, empty when it defines no such
	// severity. The page colours the mark by it, and has no colour for a value
	// nobody recognises.
	SeverityKey string
	Done        bool
	Status      string
	// Package and Source are set only when they add something the reader cannot
	// already see: this page is one package, and most bugs are filed against it.
	Package       string
	Source        string
	SubmitterName string
	// Forwarded is the address upstream, usually a URL long enough to swamp the
	// row it sits on. ForwardedTo is what the row shows: the host it points at,
	// with the address itself kept for the title attribute.
	Forwarded    string
	ForwardedTo  string
	Reported     dateLabel
	LastModified dateLabel
	Tags         []string
}

// HasDetail says whether the second line of a row would carry anything, so that
// a bug the snapshot knows nothing else about does not get an empty one.
func (b bugSummary) HasDetail() bool {
	return b.Done || b.Status != "" || b.Package != "" || b.Source != "" ||
		b.SubmitterName != "" || b.Forwarded != "" ||
		b.Reported.Text != "" || b.LastModified.Text != "" || len(b.Tags) > 0
}

// dateLabel is a date in both the forms a page needs: one for the reader and
// one for the machine attribute. Both are empty when nothing was recorded.
type dateLabel struct {
	Text    string
	Machine string
}

func formatDate(t time.Time) dateLabel {
	if t.IsZero() {
		return dateLabel{}
	}
	return dateLabel{Text: t.Format("2 Jan 2006"), Machine: t.Format("2006-01-02")}
}

// forwardedTo shortens an upstream address to the host it names. The BTS also
// carries addresses that are not URLs at all, and those are left as they are.
func forwardedTo(address string) string {
	parsed, err := url.Parse(address)
	if err != nil || parsed.Host == "" {
		return address
	}
	return parsed.Host
}

type messageData struct {
	layoutData
	Heading string
	Detail  string
}

type indexData struct {
	layoutData
	Top    []query.ListTopPackagesRow
	Groups []images.Group
	// Detected is the architecture the User-Agent suggests. Its group is listed
	// first and says so; it is a guess, so nothing is hidden on the strength of
	// it.
	Detected images.Architecture
}

type imageData struct {
	layoutData
	Image images.Image
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	layout, err := s.layout(w, r, "")
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	top, err := s.queries.ListTopPackages(r.Context(), query.ListTopPackagesParams{
		Suite:      layout.Suite,
		MaxResults: topPackagesLimit,
	})
	if err != nil {
		s.serverError(w, r, fmt.Errorf("listing the most installed packages: %w", err))
		return
	}

	detected := images.DetectArchitecture(r.UserAgent())

	s.render(w, r, "index.html", http.StatusOK, indexData{
		layoutData: layout,
		Top:        top,
		Groups:     leading(images.Catalogue(s.catalogueInput(r, s.releases(r))), detected),
		Detected:   detected,
	})
}

// handleImage is one image: what it is, what it lands as, and how to check it.
func (s *Server) handleImage(w http.ResponseWriter, r *http.Request) {
	layout, err := s.layout(w, r, "")
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	image, ok := images.Find(r.PathValue("image"), s.catalogueInput(r, s.releases(r)))
	if !ok {
		s.notFound(w, r, "No such image",
			"Every image we offer is listed on the front page.")
		return
	}

	s.render(w, r, "image.html", http.StatusOK, imageData{layoutData: layout, Image: image})
}

// leading puts the reader's own hardware first, so an ARM visitor does not
// scroll past eight images that will not boot on their machine.
func leading(groups []images.Group, architecture images.Architecture) []images.Group {
	ordered := make([]images.Group, 0, len(groups))
	for _, group := range groups {
		if group.Architecture == architecture {
			ordered = append(ordered, group)
		}
	}
	for _, group := range groups {
		if group.Architecture != architecture {
			ordered = append(ordered, group)
		}
	}
	return ordered
}

// handleImagesMoved keeps the links that pointed at the chooser working now
// that it is the front page.
func (s *Server) handleImagesMoved(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/", http.StatusMovedPermanently)
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	// An empty query has nothing to say beyond what the front page already
	// says, and a results page with no query in it reads as a failure.
	layout, err := s.layout(w, r, r.URL.Query().Get("q"))
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if strings.TrimSpace(layout.Query) == "" {
		http.Redirect(w, r, indexFor(layout.Suites, layout.Suite), http.StatusSeeOther)
		return
	}

	limit := requestedLimit(r, searchResultLimit, searchResultMax)
	results, err := s.queries.SearchPackages(r.Context(), query.SearchPackagesParams{
		Suite:      layout.Suite,
		Query:      layout.Query,
		MaxResults: limit,
	})
	if err != nil {
		s.serverError(w, r, fmt.Errorf("searching packages: %w", err))
		return
	}

	data := searchData{
		layoutData: layout,
		Results:    results,
		MoreN:      nextLimit(len(results), limit, searchResultMax),
		Truncated:  len(results) == searchResultMax,
	}

	// Full-text search matches whole words, so a misspelt name matches nothing
	// at all. Trigram similarity over the same names turns that dead end into
	// the package the reader meant.
	if len(results) == 0 {
		suggestions, err := s.queries.SuggestSimilarPackages(r.Context(), query.SuggestSimilarPackagesParams{
			Suite:      layout.Suite,
			Query:      layout.Query,
			MaxResults: suggestionLimit,
		})
		if err != nil {
			s.serverError(w, r, fmt.Errorf("suggesting packages similar to %q: %w", layout.Query, err))
			return
		}
		data.Suggestions = suggestions
	}

	s.render(w, r, "search.html", http.StatusOK, data)
}

// indexFor keeps the reader's suite when a search sends them back to the front
// page. The name comes from the suite list rather than from the URL, so the
// redirect can only ever point at a suite we serve, and stable is left implicit
// because it is the default.
func indexFor(suites []suiteChoice, selected string) string {
	for _, suite := range suites {
		if suite.Name == selected && suite.Name != "stable" {
			return "/?suite=" + suite.Name
		}
	}
	return "/"
}

func (s *Server) handleComplete(w http.ResponseWriter, r *http.Request) {
	prefix := strings.TrimSpace(r.URL.Query().Get("q"))
	if prefix == "" {
		return
	}

	name, err := domain.ParsePackageNamePrefix(prefix)
	if err != nil {
		// A prefix no package name could start with simply has no completions.
		// This is a half-typed query, not a client error worth reporting.
		return
	}

	suite := suiteOrDefault(r.URL.Query().Get("suite"))
	completions, err := s.queries.CompletePackageNames(r.Context(), query.CompletePackageNamesParams{
		Suite:      suite,
		Prefix:     name.String(),
		MaxResults: completionLimit,
	})
	if err != nil {
		s.serverError(w, r, fmt.Errorf("completing package names: %w", err))
		return
	}

	s.renderFragment(w, r, "complete", completions)
}

func (s *Server) handlePackage(w http.ResponseWriter, r *http.Request) {
	name, err := domain.ParsePackageName(r.PathValue("name"))
	if err != nil {
		s.notFound(w, r, "That is not a package name",
			"Debian package names are lowercase and contain only letters, digits, and the characters plus, dot, and hyphen.")
		return
	}
	suite := suiteOrDefault(r.PathValue("suite"))

	layout, layoutErr := s.layout(w, r, "")
	if layoutErr != nil {
		s.serverError(w, r, layoutErr)
		return
	}
	layout.Suite = suite
	for index := range layout.Suites {
		layout.Suites[index].URL = "/p/" + layout.Suites[index].Name + "/" + name.String()
		layout.Suites[index].Current = layout.Suites[index].Name == suite
	}

	binaries, err := s.queries.GetBinaryPackage(r.Context(), query.GetBinaryPackageParams{
		Name:  name.String(),
		Suite: suite,
	})
	if err != nil {
		s.serverError(w, r, fmt.Errorf("reading binary package %s: %w", name, err))
		return
	}

	data := packageData{
		layoutData: layout,
		Name:       name.String(),
		SourceName: name.String(),
		Severities: domain.Severities(),
	}
	if len(binaries) > 0 {
		data.SourceName = binaries[0].SourceName
		data.Synopsis = binaries[0].Synopsis
		data.Description = binaries[0].Description
		data.Binaries = binaries

		for _, field := range []struct{ label, value string }{
			{"Depends", binaries[0].Depends},
			{"Pre-Depends", binaries[0].PreDepends},
			{"Recommends", binaries[0].Recommends},
			{"Suggests", binaries[0].Suggests},
		} {
			if groups := domain.ParseDependencies(field.value); len(groups) > 0 {
				data.Dependencies = append(data.Dependencies,
					dependencySection{Label: field.label, Groups: groups})
			}
		}
	}

	// Search offers source packages too, and a source whose binaries all carry
	// other names has no binary rows under its own name. Its page is the source
	// record; only a name that is neither is not found.
	if source, err := s.queries.GetSourcePackage(r.Context(), query.GetSourcePackageParams{
		Name:  data.SourceName,
		Suite: suite,
	}); err == nil {
		data.Source = &source
	} else if !errors.Is(err, pgx.ErrNoRows) {
		s.serverError(w, r, fmt.Errorf("reading source package: %w", err))
		return
	}
	if len(binaries) == 0 && data.Source == nil {
		s.notFound(w, r, fmt.Sprintf("No package named %s in %s", name, suite),
			"It may exist in another suite, or it may have been removed from the archive.")
		return
	}

	if len(binaries) > 0 {
		if fresh, err := s.queries.GetFreshVersion(r.Context(), query.GetFreshVersionParams{
			Name:         name.String(),
			Suite:        suite,
			Architecture: preferredArchitecture(binaries),
		}); err == nil {
			data.Fresh = &fresh
		} else if !errors.Is(err, pgx.ErrNoRows) {
			s.serverError(w, r, fmt.Errorf("reading the freshness overlay: %w", err))
			return
		}
	}

	// The suite tabs say up front where the package exists and at what
	// version, instead of letting a click discover a 404.
	across, err := s.queries.ListPackageSuites(r.Context(), name.String())
	if err != nil {
		s.serverError(w, r, fmt.Errorf("listing suites for %s: %w", name, err))
		return
	}
	data.SuiteVersions = make(map[string]string, len(across))
	for _, row := range across {
		data.SuiteVersions[row.Suite] = row.Version
	}

	data.BugStatus, data.BugSeverity = bugFilter(r)
	status := data.BugStatus
	if status == "all" {
		status = ""
	}
	bugLimit := requestedLimit(r, bugListLimit, bugListMax)
	bugs, err := s.queries.ListBugsForPackage(r.Context(), query.ListBugsForPackageParams{
		Name:       name.String(),
		Status:     status,
		Severity:   data.BugSeverity,
		MaxResults: bugLimit,
	})
	if err != nil {
		s.serverError(w, r, fmt.Errorf("listing bugs for %s: %w", name, err))
		return
	}
	data.BugsMoreN = nextLimit(len(bugs), bugLimit, bugListMax)
	data.BugsTruncated = len(bugs) == bugListMax
	for _, bug := range bugs {
		// An unrecognised severity is upstream telling us something changed. It
		// is not worth failing a page over, but it must not pass unnoticed.
		severity, err := domain.ParseSeverity(bug.Severity)
		if err != nil {
			s.logger.WarnContext(r.Context(), "unrecognised bug severity",
				slog.Int("bug", int(bug.Number)),
				slog.String("severity", bug.Severity))
		}
		summary := bugSummary{
			Number:        bug.Number,
			Title:         bug.Title,
			Severity:      bug.Severity,
			SeverityKey:   severity.String(),
			Done:          bug.Done,
			Status:        bug.Status,
			SubmitterName: bug.SubmitterName,
			Forwarded:     bug.Forwarded,
			ForwardedTo:   forwardedTo(bug.Forwarded),
			Reported:      formatDate(bug.Arrival),
			LastModified:  formatDate(bug.LastModified),
			Tags:          bug.Tags,
		}
		if bug.Package != name.String() {
			summary.Package = bug.Package
		}
		if bug.Source != name.String() && bug.Source != bug.Package {
			summary.Source = bug.Source
		}
		data.Bugs = append(data.Bugs, summary)
	}

	count, err := s.queries.CountOpenBugsForPackage(r.Context(), name.String())
	if err != nil {
		s.serverError(w, r, fmt.Errorf("counting bugs for %s: %w", name, err))
		return
	}
	data.OpenBugCount = count

	s.render(w, r, "package.html", http.StatusOK, data)
}

// handleBugMoved sends the bug page this site used to serve to the bug tracking
// system, which holds the live record this page could only ever snapshot. The
// number is parsed rather than pasted into the location, so the redirect can
// only ever point at a bug.
func (s *Server) handleBugMoved(w http.ResponseWriter, r *http.Request) {
	number, err := domain.ParseBugNumber(r.PathValue("number"))
	if err != nil {
		s.notFound(w, r, "That is not a bug number", "Bug numbers are positive integers.")
		return
	}

	//nolint:gosec // G710 follows the path into the location without seeing that
	// what arrives there is an integer, and one this site parsed itself.
	http.Redirect(w, r, fmt.Sprintf("https://bugs.debian.org/%d", number.Int32()), http.StatusMovedPermanently)
}

// catalogueInput gathers what the catalogue reads from the database: what the
// releases are called, which point release they are at, what the desktops ship
// as, and what cdimage serves each file at. Every one of them degrades to a
// constant, so an unfilled database renders a page that is out of date rather
// than one that is broken.
func (s *Server) catalogueInput(r *http.Request, suites []query.Suite) images.Input {
	input := images.Input{
		Stable:    releaseOf(suites, "stable"),
		Oldstable: releaseOf(suites, "oldstable"),
		Desktops:  s.desktopVersions(r),
		Sizes:     s.isoSizes(r),
	}

	versions := s.isoVersions(r)
	input.Stable.Version = versions.Stable
	input.Oldstable.Version = versions.Oldstable
	return input
}

// releaseOf builds a release label out of the suite metadata we own, so that
// "Debian 13 (trixie), stable" survives trixie ceasing to be stable without an
// edit to the code.
func releaseOf(suites []query.Suite, name string) images.Release {
	for _, suite := range suites {
		if suite.Name != name || suite.Version == "" || suite.Codename == "" {
			continue
		}
		return images.Release{
			Label: fmt.Sprintf("Debian %s (%s), %s", suite.Version, suite.Codename, suite.Name),
		}
	}
	return images.Release{}
}

// releases reads the suite metadata the catalogue labels its groups with. The
// layout carries a menu rather than the rows, so the pages that need the rows
// ask for them.
func (s *Server) releases(r *http.Request) []query.Suite {
	suites, err := s.queries.ListSuites(r.Context())
	if err != nil {
		s.logger.WarnContext(r.Context(), "listing suites failed", slog.Any("error", err))
		return nil
	}
	return suites
}

// isoSizes is what cdimage served each image at when ingest last looked.
func (s *Server) isoSizes(r *http.Request) map[string]int64 {
	rows, err := s.queries.ListISOSizes(r.Context())
	if err != nil {
		s.logger.WarnContext(r.Context(), "reading the image sizes failed", slog.Any("error", err))
		return nil
	}

	sizes := make(map[string]int64, len(rows))
	for _, row := range rows {
		sizes[row.Image] = row.Bytes
	}
	return sizes
}

// desktopVersions is what each live image's desktop ships as, read from our own
// copy of the archive. An empty result leaves the tiles naming the desktop and
// nothing else, which is what an unfilled database can honestly say.
func (s *Server) desktopVersions(r *http.Request) map[string]string {
	rows, err := s.queries.ListPackageVersions(r.Context(), query.ListPackageVersionsParams{
		Suite: "stable",
		Names: images.DesktopPackages(),
	})
	if err != nil {
		s.logger.WarnContext(r.Context(), "reading the desktop versions failed", slog.Any("error", err))
		return nil
	}

	versions := make(map[string]string, len(rows))
	for _, row := range rows {
		versions[row.Name] = row.Version
	}
	return versions
}

// isoVersions is the point releases ingest last observed on cdimage. A missing
// or empty table degrades to the compiled-in fallbacks rather than failing the
// page.
func (s *Server) isoVersions(r *http.Request) (versions struct{ Stable, Oldstable string }) {
	rows, err := s.queries.ListISOVersions(r.Context())
	if err != nil {
		s.logger.WarnContext(r.Context(), "reading the ISO versions failed", slog.Any("error", err))
		return versions
	}
	for _, row := range rows {
		switch row.Channel {
		case "stable":
			versions.Stable = row.Version
		case "oldstable":
			versions.Oldstable = row.Version
		}
	}
	return versions
}

// handleScreenshot serves a mirrored thumbnail. The bytes come from our own
// database, so a visitor request never reaches screenshots.debian.net.
func (s *Server) handleScreenshot(w http.ResponseWriter, r *http.Request) {
	name, err := domain.ParsePackageName(r.PathValue("name"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	shot, err := s.queries.GetScreenshot(r.Context(), name.String())
	if errors.Is(err, pgx.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, fmt.Errorf("reading the screenshot for %s: %w", name, err))
		return
	}

	w.Header().Set("Content-Type", shot.ContentType)
	// A day of caching: thumbnails change at most once per sync.
	w.Header().Set("Cache-Control", "public, max-age=86400")
	if _, err := w.Write(shot.Image); err != nil {
		s.logger.WarnContext(r.Context(), "writing the screenshot failed", slog.Any("error", err))
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if _, err := s.queries.GetLastSync(r.Context()); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, "database unavailable", http.StatusServiceUnavailable)
		return
	}
	if _, err := w.Write([]byte("ok\n")); err != nil {
		s.logger.WarnContext(r.Context(), "writing the health response failed", slog.Any("error", err))
	}
}

// layout gathers what every page needs: the suite list, the suite being read,
// and the age of the data.
func (s *Server) layout(w http.ResponseWriter, r *http.Request, searchQuery string) (layoutData, error) {
	suites, err := s.queries.ListSuites(r.Context())
	if err != nil {
		return layoutData{}, fmt.Errorf("listing suites: %w", err)
	}

	current := s.suite(w, r, suites)
	data := layoutData{
		Suites: suiteChoices(suites, current, searchQuery),
		Suite:  current,
		Query:  searchQuery,
	}

	sync, err := s.queries.GetLastSync(r.Context())
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// Nothing has been ingested yet. The footer says so rather than
		// presenting an empty site as though it were up to date.
	case err != nil:
		return layoutData{}, fmt.Errorf("reading the last sync: %w", err)
	default:
		data.DataAsOf = &sync.SyncedAt
	}

	return data, nil
}

// preferredArchitecture picks the architecture the freshness overlay is read
// for: amd64 because it is what most readers run, then "all" for
// architecture-independent packages, then whatever is listed first.
func preferredArchitecture(binaries []query.GetBinaryPackageRow) string {
	for _, want := range []string{"amd64", "all"} {
		for _, binary := range binaries {
			if binary.Architecture == want {
				return want
			}
		}
	}
	return binaries[0].Architecture
}

// bugFilter reads the bug list filters from the URL. Anything unrecognised
// falls back to the default view rather than erroring: these arrive from typed
// URLs as well as from the form.
func bugFilter(r *http.Request) (status, severity string) {
	status = r.URL.Query().Get("bugs")
	if status != "closed" && status != "all" {
		status = "open"
	}
	if parsed, err := domain.ParseSeverity(r.URL.Query().Get("severity")); err == nil {
		severity = parsed.String()
	}
	return status, severity
}

// suiteOrDefault falls back to stable, which is what most visitors run. The
// suite tabs on every package page put the other suites one click away.
func suiteOrDefault(suite string) string {
	if suite == "" {
		return "stable"
	}
	return suite
}

// suiteCookie remembers which suite the reader is browsing. The front page
// carries no parameter to hold it, so without this a reload puts a reader of
// unstable back into stable. It stores one word chosen from a list we serve,
// never anything about the reader.
const suiteCookie = "suite"

// suite is the suite the page is about: the URL says so, otherwise the last
// choice, otherwise stable. A name that is not a suite we serve is ignored
// rather than trusted, wherever it came from.
func (s *Server) suite(w http.ResponseWriter, r *http.Request, suites []query.Suite) string {
	if chosen := r.URL.Query().Get("suite"); known(suites, chosen) {
		http.SetCookie(w, &http.Cookie{
			Name:     suiteCookie,
			Value:    chosen,
			Path:     "/",
			MaxAge:   int((365 * 24 * time.Hour).Seconds()),
			HttpOnly: true,
			// Secure unconditionally: the site is served over TLS, and browsers
			// treat loopback as a secure origin, so development keeps working.
			Secure:   true,
			SameSite: http.SameSiteLaxMode,
		})
		return chosen
	}

	if remembered, err := r.Cookie(suiteCookie); err == nil && known(suites, remembered.Value) {
		return remembered.Value
	}
	return suiteOrDefault(r.URL.Query().Get("suite"))
}

func known(suites []query.Suite, name string) bool {
	for _, suite := range suites {
		if suite.Name == name {
			return true
		}
	}
	return false
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, page string, status int, data any) {
	tmpl, ok := s.pages[page]
	if !ok {
		s.serverError(w, r, fmt.Errorf("no template named %s", page))
		return
	}

	// Rendered to a buffer first: a template that fails halfway must not leave
	// a half-written page with a 200 already committed to the wire.
	var buffer strings.Builder
	if err := tmpl.ExecuteTemplate(&buffer, "layout", data); err != nil {
		s.serverError(w, r, fmt.Errorf("rendering %s: %w", page, err))
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if _, err := w.Write([]byte(buffer.String())); err != nil {
		s.logger.WarnContext(r.Context(), "writing the response failed", slog.Any("error", err))
	}
}

func (s *Server) renderFragment(w http.ResponseWriter, r *http.Request, name string, data any) {
	var buffer strings.Builder
	if err := s.fragments.ExecuteTemplate(&buffer, name, data); err != nil {
		s.serverError(w, r, fmt.Errorf("rendering fragment %s: %w", name, err))
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if _, err := w.Write([]byte(buffer.String())); err != nil {
		s.logger.WarnContext(r.Context(), "writing the fragment failed", slog.Any("error", err))
	}
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request, heading, detail string) {
	layout, err := s.layout(w, r, "")
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, "message.html", http.StatusNotFound,
		messageData{layoutData: layout, Heading: heading, Detail: detail})
}

// serverError logs the detail and shows the reader a plain apology. The error
// text stays in the logs because it describes our database, not their request.
func (s *Server) serverError(w http.ResponseWriter, r *http.Request, err error) {
	s.logger.ErrorContext(r.Context(), "request failed",
		slog.String("path", r.URL.Path),
		slog.Any("error", err))
	http.Error(w, "Something went wrong on our side. Please try again.", http.StatusInternalServerError)
}
