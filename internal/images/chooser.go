// Package images answers "which Debian image do I actually want".
//
// The logic is pure. Debian publishes its images under a stable directory
// layout, so a recommendation is built from a few answers without contacting
// cdimage.debian.org: a chooser that made a request per visitor would violate
// the rule that a user request never causes a request to a Debian service.
//
// Point releases rename every file, so the exact filename needs the current
// version. Ingest refreshes it into the database on its own schedule and the
// handler passes it in as Versions; the constants below are the fallback for
// an empty database and the values tests run against.
package images

import (
	"fmt"
	"slices"
	"strings"
)

// The versions used when the database has none yet. Checked against cdimage
// on 2026-08-04; a point release makes the download link 404 until ingest has
// run once, and the directory link beside it keeps working regardless.
const (
	fallbackStableVersion    = "13.6.0"
	fallbackOldstableVersion = "12.15.0"
)

// Every image the chooser offers is a released Debian, and the label says which
// so that the page states it once instead of explaining it in a paragraph
// nobody reads.
const (
	stableRelease    = "Debian 13 (trixie), stable"
	oldstableRelease = "Debian 12 (bookworm), oldstable"
)

// Versions is what ingest last observed on cdimage. Zero values fall back to
// the compiled-in constants.
type Versions struct {
	Stable    string
	Oldstable string
}

func (v Versions) stable() string {
	if v.Stable == "" {
		return fallbackStableVersion
	}
	return v.Stable
}

func (v Versions) oldstable() string {
	if v.Oldstable == "" {
		return fallbackOldstableVersion
	}
	return v.Oldstable
}

// Architecture is the hardware the reader is installing onto.
type Architecture string

// The architectures the chooser offers. Debian supports more; these are the
// ones a person choosing an image from a website is plausibly using.
const (
	ArchitectureAMD64 Architecture = "amd64"
	ArchitectureARM64 Architecture = "arm64"
	ArchitectureI386  Architecture = "i386"
)

// DetectArchitecture guesses the visitor's architecture from the User-Agent,
// so the page can lead with an answer instead of a questionnaire. It is a
// default, not a verdict: the form shows the guess and lets the reader correct
// it. Anything unrecognised is amd64, which is what almost every machine
// browsing this page is.
func DetectArchitecture(userAgent string) Architecture {
	ua := strings.ToLower(userAgent)
	switch {
	case strings.Contains(ua, "aarch64") || strings.Contains(ua, "arm64"):
		return ArchitectureARM64
	case strings.Contains(ua, "i686") || strings.Contains(ua, "i586"):
		return ArchitectureI386
	default:
		return ArchitectureAMD64
	}
}

// Flavour is the kind of medium: something you install from, or something you
// boot and try first.
type Flavour string

// The flavours the chooser offers.
const (
	// FlavourNetInstall is the small installer that downloads as it goes.
	FlavourNetInstall Flavour = "netinst"
	// FlavourFullDVD carries enough packages to install without a network.
	FlavourFullDVD Flavour = "dvd"
	// FlavourLive boots to a desktop that can be tried before installing.
	FlavourLive Flavour = "live"
)

// Desktop is the desktop environment on a live image.
type Desktop string

// The desktops Debian publishes live images for.
const (
	DesktopGNOME    Desktop = "gnome"
	DesktopKDE      Desktop = "kde"
	DesktopXfce     Desktop = "xfce"
	DesktopCinnamon Desktop = "cinnamon"
	DesktopMATE     Desktop = "mate"
	DesktopStandard Desktop = "standard"
)

// Choice is what the reader answered.
type Choice struct {
	Architecture Architecture
	Flavour      Flavour
	Desktop      Desktop
	Offline      bool
}

// Recommendation is what to download: the exact latest file, where it lives,
// and how to check it. Note carries the one sentence of context shown when the
// answer differs from what was asked for; it is empty otherwise.
type Recommendation struct {
	Title         string
	Release       string
	Version       string
	Filename      string
	DownloadURL   string
	DirectoryURL  string
	ChecksumURL   string
	SignatureURL  string
	VerifyCommand string
	Size          string
	NeedsNetwork  bool
	Note          string
}

const (
	cdimage        = "https://cdimage.debian.org/debian-cd"
	cdimageArchive = "https://cdimage.debian.org/cdimage/archive/latest-oldstable"
)

var liveArchitectures = []Architecture{ArchitectureAMD64}

// Recommend turns answers into something to download.
//
// It never returns an error for an odd combination. A reader who asks for a
// live ARM image gets the closest thing that exists plus an explanation, which
// is more use than a validation message.
func Recommend(choice Choice, versions Versions) Recommendation {
	if choice.Architecture == "" {
		choice.Architecture = ArchitectureAMD64
	}
	// Trying before installing is the default because it is the safest first
	// step for someone who does not yet know what they want. Architectures
	// without live images degrade to the network installer below.
	if choice.Flavour == "" {
		choice.Flavour = FlavourLive
	}
	if choice.Desktop == "" {
		choice.Desktop = DesktopGNOME
	}

	// Debian publishes live images for amd64 only. Asking for one on another
	// architecture is a reasonable thing to want and an unreasonable thing to
	// be told nothing about.
	if choice.Flavour == FlavourLive && !slices.Contains(liveArchitectures, choice.Architecture) {
		return unavailableLive(choice, versions)
	}

	if choice.Offline && choice.Flavour == FlavourNetInstall {
		choice.Flavour = FlavourFullDVD
	}

	switch choice.Flavour {
	case FlavourLive:
		return live(choice, versions)
	case FlavourFullDVD:
		return fullDVD(choice, versions)
	case FlavourNetInstall:
		return netInstall(choice, versions)
	default:
		return netInstall(choice, versions)
	}
}

// release resolves which Debian a given architecture can run: Debian 13
// dropped 32-bit PC images, so i386 downloads come from the oldstable archive.
func release(arch Architecture, versions Versions) (directoryBase, version, label string) {
	if arch == ArchitectureI386 {
		return cdimageArchive, versions.oldstable(), oldstableRelease
	}
	return cdimage + "/current", versions.stable(), stableRelease
}

func netInstall(choice Choice, versions Versions) Recommendation {
	base, version, label := release(choice.Architecture, versions)
	directory := fmt.Sprintf("%s/%s/iso-cd/", base, choice.Architecture)
	filename := fmt.Sprintf("debian-%s-%s-netinst.iso", version, choice.Architecture)
	recommendation := Recommendation{
		Title:         fmt.Sprintf("Network installer for %s", choice.Architecture),
		Release:       label,
		Version:       version,
		Filename:      filename,
		DownloadURL:   directory + filename,
		DirectoryURL:  directory,
		ChecksumURL:   directory + "SHA512SUMS",
		SignatureURL:  directory + "SHA512SUMS.sign",
		VerifyCommand: "sha512sum --check --ignore-missing SHA512SUMS",
		Size:          "about 700 MB",
		NeedsNetwork:  true,
	}
	return withOldstableNote(recommendation, choice.Architecture)
}

func fullDVD(choice Choice, versions Versions) Recommendation {
	base, version, label := release(choice.Architecture, versions)
	directory := fmt.Sprintf("%s/%s/iso-dvd/", base, choice.Architecture)
	filename := fmt.Sprintf("debian-%s-%s-DVD-1.iso", version, choice.Architecture)
	recommendation := Recommendation{
		Title:         fmt.Sprintf("Full DVD for %s", choice.Architecture),
		Release:       label,
		Version:       version,
		Filename:      filename,
		DownloadURL:   directory + filename,
		DirectoryURL:  directory,
		ChecksumURL:   directory + "SHA512SUMS",
		SignatureURL:  directory + "SHA512SUMS.sign",
		VerifyCommand: "sha512sum --check --ignore-missing SHA512SUMS",
		Size:          "about 4 GB",
		NeedsNetwork:  false,
	}
	return withOldstableNote(recommendation, choice.Architecture)
}

func live(choice Choice, versions Versions) Recommendation {
	directory := fmt.Sprintf("%s/current-live/%s/iso-hybrid/", cdimage, choice.Architecture)
	filename := fmt.Sprintf("debian-live-%s-%s-%s.iso", versions.stable(), choice.Architecture, choice.Desktop)
	return Recommendation{
		Title:         fmt.Sprintf("Live image with %s", desktopLabel(choice.Desktop)),
		Release:       stableRelease,
		Version:       versions.stable(),
		Filename:      filename,
		DownloadURL:   directory + filename,
		DirectoryURL:  directory,
		ChecksumURL:   directory + "SHA512SUMS",
		SignatureURL:  directory + "SHA512SUMS.sign",
		VerifyCommand: "sha512sum --check --ignore-missing SHA512SUMS",
		Size:          "about 3 GB",
		NeedsNetwork:  false,
	}
}

func unavailableLive(choice Choice, versions Versions) Recommendation {
	recommendation := netInstall(choice, versions)
	note := fmt.Sprintf("Debian publishes live images for amd64 only, so this is the network installer for %s.",
		choice.Architecture)
	if recommendation.Note != "" {
		note += " " + recommendation.Note
	}
	recommendation.Note = note
	return recommendation
}

func withOldstableNote(recommendation Recommendation, arch Architecture) Recommendation {
	if arch != ArchitectureI386 {
		return recommendation
	}
	recommendation.Note = "Debian 13 dropped 32-bit PC images, so this is Debian 12, supported until June 2028."
	return recommendation
}

func desktopLabel(desktop Desktop) string {
	switch desktop {
	case DesktopGNOME:
		return "GNOME"
	case DesktopKDE:
		return "KDE Plasma"
	case DesktopXfce:
		return "Xfce"
	case DesktopCinnamon:
		return "Cinnamon"
	case DesktopMATE:
		return "MATE"
	case DesktopStandard:
		return "no desktop"
	default:
		return strings.ToUpper(string(desktop))
	}
}
