// Package images answers "which Debian image do I actually want".
//
// The logic is pure and the data is ours. Debian publishes its images under a
// stable directory layout, so a recommendation can be built from a few answers
// without contacting cdimage.debian.org at all. That is deliberate: a chooser
// that made a request per visitor would violate the rule that a user request
// never causes a request to a Debian service.
//
// What we cannot derive is the exact point-release filename, because that
// changes every few months. We link the directory and give the filename
// pattern, which is enough to act on and cannot go stale into a broken link.
package images

import (
	"fmt"
	"slices"
	"strings"
)

// Architecture is the hardware the reader is installing onto.
type Architecture string

// The architectures the chooser offers. Debian supports more; these are the
// ones a person choosing an image from a website is plausibly using.
const (
	ArchitectureAMD64 Architecture = "amd64"
	ArchitectureARM64 Architecture = "arm64"
	ArchitectureI386  Architecture = "i386"
)

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

// Recommendation is what to download and why.
type Recommendation struct {
	Title             string
	Rationale         string
	DirectoryURL      string
	FilenamePattern   string
	ChecksumURL       string
	SignatureURL      string
	VerifyCommand     string
	Notes             []string
	ApproximateSize   string
	AlternativeAdvice string
}

const cdimage = "https://cdimage.debian.org/debian-cd"

var liveArchitectures = []Architecture{ArchitectureAMD64}

// Architectures lists what the form offers, with labels a reader can map onto
// their own machine without knowing Debian's vocabulary.
func Architectures() []struct {
	Value Architecture
	Label string
} {
	return []struct {
		Value Architecture
		Label string
	}{
		{ArchitectureAMD64, "64-bit PC (most desktops and laptops, Intel or AMD)"},
		{ArchitectureARM64, "64-bit ARM (Raspberry Pi 4 and later, ARM servers)"},
		{ArchitectureI386, "32-bit PC (hardware from before roughly 2007)"},
	}
}

// Desktops lists the live desktop options.
func Desktops() []struct {
	Value Desktop
	Label string
} {
	return []struct {
		Value Desktop
		Label string
	}{
		{DesktopGNOME, "GNOME (Debian's default)"},
		{DesktopKDE, "KDE Plasma"},
		{DesktopXfce, "Xfce (light on resources)"},
		{DesktopCinnamon, "Cinnamon"},
		{DesktopMATE, "MATE"},
		{DesktopStandard, "No desktop, command line only"},
	}
}

// Recommend turns answers into something to download.
//
// It never returns an error for an odd combination. A reader who asks for a
// live ARM image gets the closest thing that exists plus an explanation, which
// is more use than a validation message.
func Recommend(choice Choice) Recommendation {
	if choice.Architecture == "" {
		choice.Architecture = ArchitectureAMD64
	}
	if choice.Flavour == "" {
		choice.Flavour = FlavourNetInstall
	}
	if choice.Desktop == "" {
		choice.Desktop = DesktopGNOME
	}

	// Debian publishes live images for amd64 only. Asking for one on another
	// architecture is a reasonable thing to want and an unreasonable thing to
	// be told nothing about.
	if choice.Flavour == FlavourLive && !slices.Contains(liveArchitectures, choice.Architecture) {
		return unavailableLive(choice)
	}

	if choice.Offline && choice.Flavour == FlavourNetInstall {
		choice.Flavour = FlavourFullDVD
	}

	switch choice.Flavour {
	case FlavourLive:
		return live(choice)
	case FlavourFullDVD:
		return fullDVD(choice)
	case FlavourNetInstall:
		return netInstall(choice)
	default:
		return netInstall(choice)
	}
}

func netInstall(choice Choice) Recommendation {
	directory := fmt.Sprintf("%s/current/%s/iso-cd/", cdimage, choice.Architecture)
	return Recommendation{
		Title:             fmt.Sprintf("Network installer for %s", choice.Architecture),
		Rationale:         "Smallest download. Fetches packages as it installs, so it needs a network connection.",
		DirectoryURL:      directory,
		FilenamePattern:   fmt.Sprintf("debian-<version>-%s-netinst.iso", choice.Architecture),
		ChecksumURL:       directory + "SHA512SUMS",
		SignatureURL:      directory + "SHA512SUMS.sign",
		VerifyCommand:     "sha512sum --check --ignore-missing SHA512SUMS",
		ApproximateSize:   "about 700 MB",
		Notes:             firmwareNote(),
		AlternativeAdvice: "No network during installation? Pick the full DVD instead.",
	}
}

func fullDVD(choice Choice) Recommendation {
	directory := fmt.Sprintf("%s/current/%s/iso-dvd/", cdimage, choice.Architecture)
	return Recommendation{
		Title:           fmt.Sprintf("Full DVD for %s", choice.Architecture),
		Rationale:       "Installs a complete system with no network at all.",
		DirectoryURL:    directory,
		FilenamePattern: fmt.Sprintf("debian-<version>-%s-DVD-1.iso", choice.Architecture),
		ChecksumURL:     directory + "SHA512SUMS",
		SignatureURL:    directory + "SHA512SUMS.sign",
		VerifyCommand:   "sha512sum --check --ignore-missing SHA512SUMS",
		ApproximateSize: "about 4 GB",
		Notes:           append(firmwareNote(), "Only disc 1 is needed. Ignore DVD-2 and later."),
	}
}

func live(choice Choice) Recommendation {
	directory := fmt.Sprintf("%s/current-live/%s/iso-hybrid/", cdimage, choice.Architecture)
	return Recommendation{
		Title:             fmt.Sprintf("Live image with %s", desktopLabel(choice.Desktop)),
		Rationale:         "Boots to a desktop without touching your disk, so you can check your hardware first. Installs from the same image.",
		DirectoryURL:      directory,
		FilenamePattern:   fmt.Sprintf("debian-live-<version>-%s-%s.iso", choice.Architecture, choice.Desktop),
		ChecksumURL:       directory + "SHA512SUMS",
		SignatureURL:      directory + "SHA512SUMS.sign",
		VerifyCommand:     "sha512sum --check --ignore-missing SHA512SUMS",
		ApproximateSize:   "about 3 GB",
		Notes:             append(firmwareNote(), "Write it straight to a USB stick. No extra preparation."),
		AlternativeAdvice: "Already know Debian runs on this machine? The network installer is far smaller.",
	}
}

func unavailableLive(choice Choice) Recommendation {
	recommendation := netInstall(choice)
	recommendation.Notes = append([]string{
		fmt.Sprintf("Debian publishes live images for amd64 only, so there is none for %s.",
			choice.Architecture),
	}, recommendation.Notes...)
	recommendation.AlternativeAdvice = "The network installer is the usual choice on this architecture."
	return recommendation
}

// firmwareNote exists because the most common piece of stale advice about
// Debian images is that you need a special unofficial build to get working
// wireless. That stopped being true in Debian 12.
func firmwareNote() []string {
	return []string{
		"Official images have included non-free firmware since Debian 12, so wireless and graphics " +
			"usually work out of the box. Ignore older guides pointing at unofficial firmware images.",
	}
}

func desktopLabel(desktop Desktop) string {
	for _, option := range Desktops() {
		if option.Value == desktop {
			return option.Label
		}
	}
	return strings.ToUpper(string(desktop))
}
