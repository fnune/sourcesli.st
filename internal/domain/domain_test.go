package domain_test

import (
	"errors"
	"testing"

	"github.com/fnune/sourcesli.st/internal/domain"
)

func TestParsePackageNameAccepts(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"bash", "libc6", "g++", "python3.13", "gcc-14", "x11-common", "0ad"} {
		if _, err := domain.ParsePackageName(name); err != nil {
			t.Errorf("ParsePackageName(%q) = %v, want no error", name, err)
		}
	}
}

func TestParsePackageNameRejects(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"empty":          "",
		"whitespaceOnly": "   ",
		"uppercase":      "Bash",
		"leadingDash":    "-bash",
		"leadingDot":     ".bash",
		"tooShort":       "a",
		"slash":          "bash/1.0",
		"pathTraversal":  "../../etc/passwd",
		"space":          "two words",
		"nullByte":       "bash\x00",
	}

	for label, name := range cases {
		t.Run(label, func(t *testing.T) {
			t.Parallel()
			got, err := domain.ParsePackageName(name)
			if err == nil {
				t.Fatalf("ParsePackageName(%q) = %q, want an error", name, got)
			}
			if !errors.Is(err, domain.ErrInvalid) {
				t.Errorf("ParsePackageName(%q) error = %v, want it to wrap ErrInvalid", name, err)
			}
		})
	}
}

func TestParsePackageNameTrimsSurroundingWhitespace(t *testing.T) {
	t.Parallel()

	got, err := domain.ParsePackageName("  bash\n")
	if err != nil {
		t.Fatalf("ParsePackageName = %v, want no error", err)
	}
	if got != domain.PackageName("bash") {
		t.Errorf("ParsePackageName = %q, want %q", got, "bash")
	}
}

func TestParsePackageNamePrefix(t *testing.T) {
	t.Parallel()

	// A single character is a valid prefix although not a valid name.
	for _, prefix := range []string{"b", "0", "bash", "lib", "g+"} {
		if _, err := domain.ParsePackageNamePrefix(prefix); err != nil {
			t.Errorf("ParsePackageNamePrefix(%q) = %v, want no error", prefix, err)
		}
	}

	for _, prefix := range []string{"", "   ", "B", "-b", ".b", "b%", "b_", "two words", "../etc"} {
		got, err := domain.ParsePackageNamePrefix(prefix)
		if err == nil {
			t.Fatalf("ParsePackageNamePrefix(%q) = %q, want an error", prefix, got)
		}
		if !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("ParsePackageNamePrefix(%q) error = %v, want it to wrap ErrInvalid", prefix, err)
		}
	}
}

func TestSeveritiesAreCompleteAndOrdered(t *testing.T) {
	t.Parallel()

	severities := domain.Severities()
	if len(severities) != 7 {
		t.Fatalf("Severities() has %d entries, want the 7 the BTS defines", len(severities))
	}
	for i, severity := range severities {
		if severity.Rank() != i {
			t.Errorf("Severities()[%d] = %s with rank %d, want the list in rank order", i, severity, severity.Rank())
		}
	}
}

func TestParseDependencies(t *testing.T) {
	t.Parallel()

	groups := domain.ParseDependencies(
		"libc6 (>= 2.39), libgtk-3-0t64 (>= 3.13.7), fontconfig | fonts-liberation, libnss3:any, procps",
	)

	if len(groups) != 5 {
		t.Fatalf("groups = %d, want 5", len(groups))
	}
	first := groups[0][0]
	if first.Target != "libc6" || first.Constraint != "(>= 2.39)" {
		t.Errorf("first = %+v, want libc6 with its constraint", first)
	}
	if len(groups[2]) != 2 || groups[2][1].Target != "fonts-liberation" {
		t.Errorf("alternatives = %+v, want both sides of the bar", groups[2])
	}
	qualified := groups[3][0]
	if qualified.Name != "libnss3:any" || qualified.Target != "libnss3" {
		t.Errorf("qualified = %+v, want the bare name as the link target", qualified)
	}
}

func TestParseDependenciesToleratesJunk(t *testing.T) {
	t.Parallel()

	if got := domain.ParseDependencies(""); got != nil {
		t.Errorf("empty field = %+v, want nil", got)
	}

	groups := domain.ParseDependencies("UPPER-not-a-name (>= 1), bash")
	if len(groups) != 2 {
		t.Fatalf("groups = %d, want 2", len(groups))
	}
	if groups[0][0].Target != "" {
		t.Errorf("a token that is not a package name should have no link target, got %+v", groups[0][0])
	}
	if groups[1][0].Target != "bash" {
		t.Errorf("the valid clause should still link, got %+v", groups[1][0])
	}
}

func TestParseBugNumber(t *testing.T) {
	t.Parallel()

	accepted := map[string]domain.BugNumber{
		"1":        1,
		"1084321":  1084321,
		"#1084321": 1084321,
		" 42 ":     42,
	}
	for raw, want := range accepted {
		got, err := domain.ParseBugNumber(raw)
		if err != nil {
			t.Errorf("ParseBugNumber(%q) = %v, want %d", raw, err, want)
			continue
		}
		if got != want {
			t.Errorf("ParseBugNumber(%q) = %d, want %d", raw, got, want)
		}
	}

	for _, raw := range []string{"", "0", "-5", "abc", "12abc", "#"} {
		if got, err := domain.ParseBugNumber(raw); err == nil {
			t.Errorf("ParseBugNumber(%q) = %d, want an error", raw, got)
		}
	}
}

func TestSeverityRankOrdersAsTheBTSDoes(t *testing.T) {
	t.Parallel()

	ordered := []domain.Severity{
		domain.SeverityCritical,
		domain.SeverityGrave,
		domain.SeveritySerious,
		domain.SeverityImportant,
		domain.SeverityNormal,
		domain.SeverityMinor,
		domain.SeverityWishlist,
	}

	for i := 1; i < len(ordered); i++ {
		if ordered[i-1].Rank() >= ordered[i].Rank() {
			t.Errorf("%s ranks %d, %s ranks %d, want strictly increasing",
				ordered[i-1], ordered[i-1].Rank(), ordered[i], ordered[i].Rank())
		}
	}
}

func TestReleaseCriticalSeverities(t *testing.T) {
	t.Parallel()

	critical := map[domain.Severity]bool{
		domain.SeverityCritical:  true,
		domain.SeverityGrave:     true,
		domain.SeveritySerious:   true,
		domain.SeverityImportant: false,
		domain.SeverityNormal:    false,
		domain.SeverityMinor:     false,
		domain.SeverityWishlist:  false,
	}

	for severity, want := range critical {
		if got := severity.IsReleaseCritical(); got != want {
			t.Errorf("%s.IsReleaseCritical() = %t, want %t", severity, got, want)
		}
	}
}

func TestParseSeverityRejectsUnknownValues(t *testing.T) {
	t.Parallel()

	if got, err := domain.ParseSeverity("catastrophic"); err == nil {
		t.Errorf("ParseSeverity(\"catastrophic\") = %q, want an error", got)
	}

	got, err := domain.ParseSeverity("  GRAVE ")
	if err != nil {
		t.Fatalf("ParseSeverity(\"  GRAVE \") = %v, want no error", err)
	}
	if got != domain.SeverityGrave {
		t.Errorf("ParseSeverity(\"  GRAVE \") = %q, want %q", got, domain.SeverityGrave)
	}
}
