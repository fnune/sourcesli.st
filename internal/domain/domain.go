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
