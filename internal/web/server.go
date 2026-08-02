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
	"strings"
	"time"

	"github.com/fnune/sourcesli.st/internal/domain"
	"github.com/fnune/sourcesli.st/internal/images"
	"github.com/fnune/sourcesli.st/internal/store/query"
	"github.com/jackc/pgx/v5"
)

const (
	searchResultLimit = 50
	completionLimit   = 8
	bugListLimit      = 25
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
	GetBinaryPackage(ctx context.Context, arg query.GetBinaryPackageParams) ([]query.GetBinaryPackageRow, error)
	GetSourcePackage(ctx context.Context, arg query.GetSourcePackageParams) (query.SourcePackage, error)
	GetFreshVersion(ctx context.Context, arg query.GetFreshVersionParams) (query.GetFreshVersionRow, error)
	ListBugsForPackage(ctx context.Context, arg query.ListBugsForPackageParams) ([]query.ListBugsForPackageRow, error)
	CountOpenBugsForPackage(ctx context.Context, package_ string) (int64, error)
	GetBug(ctx context.Context, number int32) (query.Bug, error)
	ListBugTags(ctx context.Context, bugNumber int32) ([]string, error)
	GetLastSync(ctx context.Context) (query.GetLastSyncRow, error)
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
	// A NULL timestamp reaches Go as the zero time rather than a nil pointer,
	// and a struct is always truthy in a template, so pages need this to tell
	// "no date recorded" from a date.
	functions := template.FuncMap{
		"isZeroTime": func(t time.Time) bool { return t.IsZero() },
	}

	shared := []string{"layout.html", "searchform.html", "results.html", "recommendation.html"}
	pageNames := []string{
		"index.html", "search.html", "package.html", "bug.html", "images.html", "message.html",
	}

	pages := make(map[string]*template.Template, len(pageNames))
	for _, page := range pageNames {
		parsed, err := template.New(page).Funcs(functions).ParseFS(templates, append(shared, page)...)
		if err != nil {
			return nil, fmt.Errorf("parsing template %s: %w", page, err)
		}
		pages[page] = parsed
	}

	fragments, err := template.New("fragments").ParseFS(templates, "results.html", "complete.html")
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
	mux.HandleFunc("GET /b/{number}", s.handleBug)
	mux.HandleFunc("GET /images", s.handleImages)
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.Handle("GET /assets/", s.assets)
	return mux
}

type layoutData struct {
	DataAsOf *time.Time
	Suites   []query.Suite
	Suite    string
	Query    string
}

type searchData struct {
	layoutData
	Results []query.SearchPackagesRow
}

type packageData struct {
	layoutData
	Name         string
	SourceName   string
	Synopsis     string
	Description  string
	Binaries     []query.GetBinaryPackageRow
	Source       *query.SourcePackage
	Fresh        *query.GetFreshVersionRow
	Bugs         []bugSummary
	OpenBugCount int64
}

type bugSummary struct {
	Number          int32
	Title           string
	Severity        string
	ReleaseCritical bool
}

type bugData struct {
	layoutData
	Bug  query.Bug
	Tags []string
}

type imagesData struct {
	layoutData
	Choice        images.Choice
	Architectures []struct {
		Value images.Architecture
		Label string
	}
	Desktops []struct {
		Value images.Desktop
		Label string
	}
	Recommendation images.Recommendation
}

type messageData struct {
	layoutData
	Heading string
	Detail  string
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	layout, err := s.layout(r, "")
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, "index.html", http.StatusOK, layout)
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	layout, err := s.layout(r, r.URL.Query().Get("q"))
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	data := searchData{layoutData: layout}
	if strings.TrimSpace(layout.Query) != "" {
		results, err := s.queries.SearchPackages(r.Context(), query.SearchPackagesParams{
			Suite:      layout.Suite,
			Query:      layout.Query,
			MaxResults: searchResultLimit,
		})
		if err != nil {
			s.serverError(w, r, fmt.Errorf("searching packages: %w", err))
			return
		}
		data.Results = results
	}

	// htmx asks for the results list alone; everything else gets a whole page.
	// Both come from the same template, so the two paths cannot drift.
	if r.Header.Get("HX-Request") == "true" {
		s.renderFragment(w, r, "results", data)
		return
	}
	s.render(w, r, "search.html", http.StatusOK, data)
}

func (s *Server) handleComplete(w http.ResponseWriter, r *http.Request) {
	prefix := strings.TrimSpace(r.URL.Query().Get("q"))
	if prefix == "" {
		return
	}

	name, err := domain.ParsePackageName(prefix)
	if err != nil {
		// A prefix that cannot become a package name simply has no completions.
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

	layout, layoutErr := s.layout(r, "")
	if layoutErr != nil {
		s.serverError(w, r, layoutErr)
		return
	}
	layout.Suite = suite

	binaries, err := s.queries.GetBinaryPackage(r.Context(), query.GetBinaryPackageParams{
		Name:  name.String(),
		Suite: suite,
	})
	if err != nil {
		s.serverError(w, r, fmt.Errorf("reading binary package %s: %w", name, err))
		return
	}
	if len(binaries) == 0 {
		s.notFound(w, r, fmt.Sprintf("No package named %s in %s", name, suite),
			"It may exist in another suite, or it may have been removed from the archive.")
		return
	}

	data := packageData{
		layoutData:  layout,
		Name:        name.String(),
		SourceName:  binaries[0].SourceName,
		Synopsis:    binaries[0].Synopsis,
		Description: binaries[0].Description,
		Binaries:    binaries,
	}

	if source, err := s.queries.GetSourcePackage(r.Context(), query.GetSourcePackageParams{
		Name:  binaries[0].SourceName,
		Suite: suite,
	}); err == nil {
		data.Source = &source
	} else if !errors.Is(err, pgx.ErrNoRows) {
		s.serverError(w, r, fmt.Errorf("reading source package: %w", err))
		return
	}

	if fresh, err := s.queries.GetFreshVersion(r.Context(), query.GetFreshVersionParams{
		Name:         name.String(),
		Suite:        suite,
		Architecture: binaries[0].Architecture,
	}); err == nil {
		data.Fresh = &fresh
	} else if !errors.Is(err, pgx.ErrNoRows) {
		s.serverError(w, r, fmt.Errorf("reading the freshness overlay: %w", err))
		return
	}

	bugs, err := s.queries.ListBugsForPackage(r.Context(), query.ListBugsForPackageParams{
		Name:       name.String(),
		OnlyOpen:   true,
		MaxResults: bugListLimit,
	})
	if err != nil {
		s.serverError(w, r, fmt.Errorf("listing bugs for %s: %w", name, err))
		return
	}
	for _, bug := range bugs {
		// An unrecognised severity is upstream telling us something changed. It
		// is not worth failing a page over, but it must not pass unnoticed.
		severity, err := domain.ParseSeverity(bug.Severity)
		if err != nil {
			s.logger.WarnContext(r.Context(), "unrecognised bug severity",
				slog.Int("bug", int(bug.Number)),
				slog.String("severity", bug.Severity))
		}
		data.Bugs = append(data.Bugs, bugSummary{
			Number:          bug.Number,
			Title:           bug.Title,
			Severity:        bug.Severity,
			ReleaseCritical: severity.IsReleaseCritical(),
		})
	}

	count, err := s.queries.CountOpenBugsForPackage(r.Context(), name.String())
	if err != nil {
		s.serverError(w, r, fmt.Errorf("counting bugs for %s: %w", name, err))
		return
	}
	data.OpenBugCount = count

	s.render(w, r, "package.html", http.StatusOK, data)
}

func (s *Server) handleBug(w http.ResponseWriter, r *http.Request) {
	number, err := domain.ParseBugNumber(r.PathValue("number"))
	if err != nil {
		s.notFound(w, r, "That is not a bug number", "Bug numbers are positive integers.")
		return
	}

	layout, err := s.layout(r, "")
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	bug, err := s.queries.GetBug(r.Context(), number.Int32())
	if errors.Is(err, pgx.ErrNoRows) {
		s.notFound(w, r, fmt.Sprintf("No bug numbered %s", number),
			"It may be newer than our last snapshot. The bug tracking system has the live record.")
		return
	}
	if err != nil {
		s.serverError(w, r, fmt.Errorf("reading bug %s: %w", number, err))
		return
	}

	tags, err := s.queries.ListBugTags(r.Context(), bug.Number)
	if err != nil {
		s.serverError(w, r, fmt.Errorf("reading tags for bug %s: %w", number, err))
		return
	}

	s.render(w, r, "bug.html", http.StatusOK, bugData{layoutData: layout, Bug: bug, Tags: tags})
}

// handleImages answers the "which image do I want" question. It reads no
// database and contacts nothing: the recommendation is derived from the answers
// and Debian's stable directory layout.
func (s *Server) handleImages(w http.ResponseWriter, r *http.Request) {
	layout, err := s.layout(r, "")
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	parameters := r.URL.Query()
	choice := images.Choice{
		Architecture: images.Architecture(parameters.Get("arch")),
		Flavour:      images.Flavour(parameters.Get("flavour")),
		Desktop:      images.Desktop(parameters.Get("desktop")),
		Offline:      parameters.Get("offline") != "",
	}

	recommendation := images.Recommend(choice)

	// Recommend fills in the defaults, so the form must be redrawn from what it
	// actually decided rather than from what arrived. Otherwise a first visit
	// shows empty selects beside a populated answer.
	if choice.Architecture == "" {
		choice.Architecture = images.ArchitectureAMD64
	}
	if choice.Flavour == "" {
		choice.Flavour = images.FlavourNetInstall
	}
	if choice.Desktop == "" {
		choice.Desktop = images.DesktopGNOME
	}

	s.render(w, r, "images.html", http.StatusOK, imagesData{
		layoutData:     layout,
		Choice:         choice,
		Architectures:  images.Architectures(),
		Desktops:       images.Desktops(),
		Recommendation: recommendation,
	})
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

// layout gathers what every page needs: the suite list and the age of the data.
func (s *Server) layout(r *http.Request, searchQuery string) (layoutData, error) {
	suites, err := s.queries.ListSuites(r.Context())
	if err != nil {
		return layoutData{}, fmt.Errorf("listing suites: %w", err)
	}

	data := layoutData{
		Suites: suites,
		Suite:  suiteOrDefault(r.URL.Query().Get("suite")),
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

// suiteOrDefault falls back to unstable, which is where a reader looking for
// the newest version of something almost always means to look.
func suiteOrDefault(suite string) string {
	if suite == "" {
		return "unstable"
	}
	return suite
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
	layout, err := s.layout(r, "")
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
