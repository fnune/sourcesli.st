// Package domain holds the vocabulary of the application: the types every
// other package speaks in. It performs no IO and imports nothing outside the
// standard library, so the rules encoded here cannot be bypassed by reaching
// for a driver or a template.
package domain

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// ErrInvalid is the class of every parse failure in this package. Callers at a
// process boundary distinguish it from an internal failure to decide between
// reporting a bad request and reporting an outage.
var ErrInvalid = errors.New("invalid value")

// Debian Policy 5.6.1 and 5.6.7. Enforcing them here means a name that reached
// a query has already been proven to be a name, so no caller needs to re-check.
var (
	packageNamePattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9+.-]+$`)
	architecturePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
)

const maxPackageNameLength = 100

// PackageName is a Debian binary or source package name that has been checked
// against Policy. The zero value is not a valid name; construct with
// ParsePackageName.
type PackageName string

// ParsePackageName returns the name or explains why it is not one.
func ParsePackageName(raw string) (PackageName, error) {
	name := strings.TrimSpace(raw)
	switch {
	case name == "":
		return "", fmt.Errorf("%w: package name is empty", ErrInvalid)
	case len(name) > maxPackageNameLength:
		return "", fmt.Errorf("%w: package name %q exceeds %d characters", ErrInvalid, name, maxPackageNameLength)
	case !packageNamePattern.MatchString(name):
		return "", fmt.Errorf("%w: package name %q is not permitted by Debian Policy 5.6.7", ErrInvalid, name)
	}
	return PackageName(name), nil
}

func (n PackageName) String() string { return string(n) }

// PackageNamePrefix is the beginning of a package name, as typed into
// autocompletion. It exists apart from PackageName because a single character
// is a legitimate prefix but not a legitimate name: Policy requires names to be
// at least two characters.
type PackageNamePrefix string

var packageNamePrefixPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9+.-]*$`)

// ParsePackageNamePrefix returns the prefix or explains why no package name
// could ever start with it.
func ParsePackageNamePrefix(raw string) (PackageNamePrefix, error) {
	prefix := strings.TrimSpace(raw)
	switch {
	case prefix == "":
		return "", fmt.Errorf("%w: package name prefix is empty", ErrInvalid)
	case len(prefix) > maxPackageNameLength:
		return "", fmt.Errorf("%w: package name prefix %q exceeds %d characters", ErrInvalid, prefix, maxPackageNameLength)
	case !packageNamePrefixPattern.MatchString(prefix):
		return "", fmt.Errorf("%w: no package name permitted by Debian Policy 5.6.7 starts with %q", ErrInvalid, prefix)
	}
	return PackageNamePrefix(prefix), nil
}

func (p PackageNamePrefix) String() string { return string(p) }

// Dependency is one alternative inside a relationship clause: a package name,
// where a link to it should point, and the constraint text that followed it.
type Dependency struct {
	// Name as written, including any architecture qualifier ("libc6:any").
	Name string
	// Target is the bare package name a link should use, empty when the name
	// does not parse as one.
	Target string
	// Constraint is everything after the name: "(>= 2.39)", "[amd64]", or "".
	Constraint string
}

// DependencyGroup is a set of alternatives separated by "|" in the field.
type DependencyGroup []Dependency

// ParseDependencies splits a Debian relationship field such as Depends into
// clauses and alternatives, so a page can link each name instead of printing
// the raw field. It never errors: a token that is not a package name comes
// back with an empty Target and renders as plain text.
func ParseDependencies(raw string) []DependencyGroup {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}

	var groups []DependencyGroup
	for _, clause := range strings.Split(trimmed, ",") {
		var group DependencyGroup
		for _, alternative := range strings.Split(clause, "|") {
			alternative = strings.TrimSpace(alternative)
			if alternative == "" {
				continue
			}

			name, constraint, _ := strings.Cut(alternative, " ")
			dependency := Dependency{Name: name, Constraint: strings.TrimSpace(constraint)}

			// "libc6:any" pins the architecture; the page for it is libc6's.
			base, _, _ := strings.Cut(name, ":")
			if parsed, err := ParsePackageName(base); err == nil {
				dependency.Target = parsed.String()
			}
			group = append(group, dependency)
		}
		if len(group) > 0 {
			groups = append(groups, group)
		}
	}
	return groups
}

// Architecture is a Debian architecture such as amd64, or the wildcard "all".
type Architecture string

// ParseArchitecture returns the architecture or explains why it is not one.
func ParseArchitecture(raw string) (Architecture, error) {
	arch := strings.TrimSpace(raw)
	if arch == "" {
		return "", fmt.Errorf("%w: architecture is empty", ErrInvalid)
	}
	if !architecturePattern.MatchString(arch) {
		return "", fmt.Errorf("%w: architecture %q is not well formed", ErrInvalid, arch)
	}
	return Architecture(arch), nil
}

func (a Architecture) String() string { return string(a) }

// SuiteName identifies a suite by its role name, such as "unstable". Codenames
// are resolved to role names during ingest so that exactly one spelling reaches
// the rest of the application.
type SuiteName string

func (s SuiteName) String() string { return string(s) }

// Suite is a Debian release as the site presents it.
type Suite struct {
	Name      SuiteName
	Codename  string
	Version   string
	Role      string
	SortOrder int
}

// BugNumber is a Debian BTS bug number.
//
// int32 rather than int because that is what the column holds. Making the
// narrower type the domain type means the range check happens once, in
// ParseBugNumber, instead of at every conversion on the way to a query.
type BugNumber int32

// ParseBugNumber accepts the numeric forms the BTS uses, including the "#1234"
// spelling people paste out of mailing lists.
func ParseBugNumber(raw string) (BugNumber, error) {
	trimmed := strings.TrimPrefix(strings.TrimSpace(raw), "#")
	if trimmed == "" {
		return 0, fmt.Errorf("%w: bug number is empty", ErrInvalid)
	}
	number, err := strconv.ParseInt(trimmed, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("%w: bug number %q is not a 32-bit integer", ErrInvalid, raw)
	}
	if number <= 0 {
		return 0, fmt.Errorf("%w: bug number %d is not positive", ErrInvalid, number)
	}
	return BugNumber(number), nil
}

func (b BugNumber) String() string { return strconv.Itoa(int(b)) }

// Int32 returns the value as the database column type.
func (b BugNumber) Int32() int32 { return int32(b) }

// Severity is a BTS severity. The order of the constants is the order the BTS
// itself ranks them, which is what bug lists sort by.
type Severity string

// The severities the Debian BTS defines, listed most severe first.
const (
	SeverityCritical  Severity = "critical"
	SeverityGrave     Severity = "grave"
	SeveritySerious   Severity = "serious"
	SeverityImportant Severity = "important"
	SeverityNormal    Severity = "normal"
	SeverityMinor     Severity = "minor"
	SeverityWishlist  Severity = "wishlist"
)

var severityRank = map[Severity]int{
	SeverityCritical:  0,
	SeverityGrave:     1,
	SeveritySerious:   2,
	SeverityImportant: 3,
	SeverityNormal:    4,
	SeverityMinor:     5,
	SeverityWishlist:  6,
}

// ParseSeverity rejects anything the BTS does not define, so an unexpected
// upstream value surfaces during ingest rather than as a mis-sorted bug list.
func ParseSeverity(raw string) (Severity, error) {
	severity := Severity(strings.ToLower(strings.TrimSpace(raw)))
	if _, ok := severityRank[severity]; !ok {
		return "", fmt.Errorf("%w: %q is not a BTS severity", ErrInvalid, raw)
	}
	return severity, nil
}

// Severities lists every BTS severity, most severe first: the order bug lists
// sort by, which is also the order filter controls should offer.
func Severities() []Severity {
	return []Severity{
		SeverityCritical,
		SeverityGrave,
		SeveritySerious,
		SeverityImportant,
		SeverityNormal,
		SeverityMinor,
		SeverityWishlist,
	}
}

// Rank orders severities as the BTS does, most severe first.
func (s Severity) Rank() int {
	rank, ok := severityRank[s]
	if !ok {
		return len(severityRank)
	}
	return rank
}

// IsReleaseCritical reports whether a bug at this severity keeps a package out
// of testing.
func (s Severity) IsReleaseCritical() bool {
	return s == SeverityCritical || s == SeverityGrave || s == SeveritySerious
}

func (s Severity) String() string { return string(s) }
