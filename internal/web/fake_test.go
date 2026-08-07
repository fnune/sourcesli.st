package web_test

import (
	"context"
	"time"

	"github.com/fnune/sourcesli.st/internal/store/query"
	"github.com/jackc/pgx/v5"
)

// fakeQueries is a hand-written in-memory implementation of the seam the web
// package declares. Hand-written rather than generated because the point is to
// be able to say what the database returns in one readable place, including the
// awkward cases: a package with no source, a bug that does not exist, a
// database that has never been ingested into.
type fakeQueries struct {
	suites          []query.Suite
	search          []query.SearchPackagesRow
	completions     []query.CompletePackageNamesRow
	suggestions     []query.SuggestSimilarPackagesRow
	binaries        map[string][]query.GetBinaryPackageRow
	sources         map[string]query.SourcePackage
	fresh           map[string]query.GetFreshVersionRow
	bugList         []query.ListBugsForPackageRow
	openBugs        int64
	lastSync        *time.Time
	isoVersions     []query.ListISOVersionsRow
	top             []query.ListTopPackagesRow
	suiteVersions   []query.ListPackageSuitesRow
	packageVersions []query.ListPackageVersionsRow
	isoSizes        []query.ListISOSizesRow
	screenshots     map[string]query.GetScreenshotRow

	// failWith is returned by every method when set, so a test can assert that
	// a database failure becomes a 500 rather than a half-rendered page.
	failWith error

	searchCalledWith  query.SearchPackagesParams
	topCalledWith     query.ListTopPackagesParams
	suggestCalledWith query.SuggestSimilarPackagesParams
	bugsCalledWith    query.ListBugsForPackageParams
}

func (f *fakeQueries) ListSuites(context.Context) ([]query.Suite, error) {
	if f.failWith != nil {
		return nil, f.failWith
	}
	return f.suites, nil
}

func (f *fakeQueries) SearchPackages(
	_ context.Context,
	arg query.SearchPackagesParams,
) ([]query.SearchPackagesRow, error) {
	if f.failWith != nil {
		return nil, f.failWith
	}
	f.searchCalledWith = arg
	return f.search, nil
}

func (f *fakeQueries) CompletePackageNames(
	context.Context,
	query.CompletePackageNamesParams,
) ([]query.CompletePackageNamesRow, error) {
	if f.failWith != nil {
		return nil, f.failWith
	}
	return f.completions, nil
}

func (f *fakeQueries) SuggestSimilarPackages(
	_ context.Context,
	arg query.SuggestSimilarPackagesParams,
) ([]query.SuggestSimilarPackagesRow, error) {
	if f.failWith != nil {
		return nil, f.failWith
	}
	f.suggestCalledWith = arg
	return f.suggestions, nil
}

func (f *fakeQueries) GetBinaryPackage(
	_ context.Context,
	arg query.GetBinaryPackageParams,
) ([]query.GetBinaryPackageRow, error) {
	if f.failWith != nil {
		return nil, f.failWith
	}
	return f.binaries[arg.Name], nil
}

func (f *fakeQueries) GetSourcePackage(
	_ context.Context,
	arg query.GetSourcePackageParams,
) (query.SourcePackage, error) {
	if f.failWith != nil {
		return query.SourcePackage{}, f.failWith
	}
	source, ok := f.sources[arg.Name]
	if !ok {
		return query.SourcePackage{}, pgx.ErrNoRows
	}
	return source, nil
}

func (f *fakeQueries) GetFreshVersion(
	_ context.Context,
	arg query.GetFreshVersionParams,
) (query.GetFreshVersionRow, error) {
	if f.failWith != nil {
		return query.GetFreshVersionRow{}, f.failWith
	}
	fresh, ok := f.fresh[arg.Name]
	if !ok {
		return query.GetFreshVersionRow{}, pgx.ErrNoRows
	}
	return fresh, nil
}

func (f *fakeQueries) ListBugsForPackage(
	_ context.Context,
	arg query.ListBugsForPackageParams,
) ([]query.ListBugsForPackageRow, error) {
	if f.failWith != nil {
		return nil, f.failWith
	}
	f.bugsCalledWith = arg
	return f.bugList, nil
}

func (f *fakeQueries) CountOpenBugsForPackage(context.Context, string) (int64, error) {
	if f.failWith != nil {
		return 0, f.failWith
	}
	return f.openBugs, nil
}

func (f *fakeQueries) ListTopPackages(
	_ context.Context,
	arg query.ListTopPackagesParams,
) ([]query.ListTopPackagesRow, error) {
	if f.failWith != nil {
		return nil, f.failWith
	}
	f.topCalledWith = arg
	return f.top, nil
}

func (f *fakeQueries) ListPackageSuites(context.Context, string) ([]query.ListPackageSuitesRow, error) {
	if f.failWith != nil {
		return nil, f.failWith
	}
	return f.suiteVersions, nil
}

func (f *fakeQueries) ListISOSizes(context.Context) ([]query.ListISOSizesRow, error) {
	if f.failWith != nil {
		return nil, f.failWith
	}
	return f.isoSizes, nil
}

func (f *fakeQueries) ListPackageVersions(
	context.Context,
	query.ListPackageVersionsParams,
) ([]query.ListPackageVersionsRow, error) {
	if f.failWith != nil {
		return nil, f.failWith
	}
	return f.packageVersions, nil
}

func (f *fakeQueries) GetScreenshot(_ context.Context, name string) (query.GetScreenshotRow, error) {
	if f.failWith != nil {
		return query.GetScreenshotRow{}, f.failWith
	}
	shot, ok := f.screenshots[name]
	if !ok {
		return query.GetScreenshotRow{}, pgx.ErrNoRows
	}
	return shot, nil
}

func (f *fakeQueries) ListISOVersions(context.Context) ([]query.ListISOVersionsRow, error) {
	if f.failWith != nil {
		return nil, f.failWith
	}
	return f.isoVersions, nil
}

func (f *fakeQueries) GetLastSync(context.Context) (query.GetLastSyncRow, error) {
	if f.failWith != nil {
		return query.GetLastSyncRow{}, f.failWith
	}
	if f.lastSync == nil {
		return query.GetLastSyncRow{}, pgx.ErrNoRows
	}
	return query.GetLastSyncRow{SyncedAt: *f.lastSync, Etag: strPtr("\"abc\"")}, nil
}

func strPtr(s string) *string { return &s }
