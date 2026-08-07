package images_test

import (
	"strings"
	"testing"

	"github.com/fnune/sourcesli.st/internal/images"
)

// The User-Agent is a hint, not a verdict: the page shows the guess and the
// form corrects it. What matters is that the guess never lands outside the
// architectures the chooser offers.
func TestDetectArchitecture(t *testing.T) {
	t.Parallel()

	cases := map[string]images.Architecture{
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36": images.ArchitectureAMD64,
		"Mozilla/5.0 (X11; Linux x86_64; rv:141.0) Gecko/20100101":     images.ArchitectureAMD64,
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit":  images.ArchitectureAMD64,
		"Mozilla/5.0 (X11; Linux aarch64; rv:141.0) Gecko/20100101":    images.ArchitectureARM64,
		"Mozilla/5.0 (Linux; Android 16; arm64) AppleWebKit/537.36":    images.ArchitectureARM64,
		"Mozilla/5.0 (X11; Linux i686; rv:109.0) Gecko/20100101":       images.ArchitectureI386,
		"": images.ArchitectureAMD64,
	}

	for userAgent, want := range cases {
		if got := images.DetectArchitecture(userAgent); got != want {
			t.Errorf("DetectArchitecture(%q) = %s, want %s", userAgent, got, want)
		}
	}
}

// The database versions win over the compiled-in fallbacks, because ingest
// refreshes them while a fallback only changes with a deploy.
func TestRecommendUsesTheObservedVersions(t *testing.T) {
	t.Parallel()

	got := images.Recommend(images.Choice{}, images.Versions{Stable: "13.9.0"})

	if got.Filename != "debian-live-13.9.0-amd64-gnome.iso" {
		t.Errorf("Filename = %q, want the observed stable version in it", got.Filename)
	}
	if got.Version != "13.9.0" {
		t.Errorf("Version = %q, want 13.9.0", got.Version)
	}
}

// Debian 13 dropped 32-bit PC images, so i386 must come from the oldstable
// archive and say so, or the download link is a guaranteed 404.
func TestI386ComesFromTheOldstableArchive(t *testing.T) {
	t.Parallel()

	got := images.Recommend(images.Choice{Architecture: images.ArchitectureI386},
		images.Versions{Oldstable: "12.16.0"})

	if !strings.Contains(got.DirectoryURL, "/archive/latest-oldstable/i386/iso-cd/") {
		t.Errorf("DirectoryURL = %q, want the oldstable archive", got.DirectoryURL)
	}
	if got.Filename != "debian-12.16.0-i386-netinst.iso" {
		t.Errorf("Filename = %q, want the oldstable version in it", got.Filename)
	}

	if !strings.Contains(got.Release, "oldstable") {
		t.Errorf("Release = %q, want it to name the release the image comes from", got.Release)
	}
	if !strings.Contains(got.Note, "Debian 12") {
		t.Errorf("Note = %q, want it to say this is Debian 12 and until when it is supported", got.Note)
	}
}

// Trying before installing is the default: the safest first step for someone
// who does not yet know what they want.
func TestRecommendDefaultsToTheLiveImage(t *testing.T) {
	t.Parallel()

	got := images.Recommend(images.Choice{}, images.Versions{})

	if !strings.Contains(got.DirectoryURL, "/current-live/amd64/iso-hybrid/") {
		t.Errorf("DirectoryURL = %q, want the amd64 live directory", got.DirectoryURL)
	}
	if !strings.HasSuffix(got.DownloadURL, "-amd64-gnome.iso") {
		t.Errorf("DownloadURL = %q, want the GNOME live file", got.DownloadURL)
	}
}

func TestRecommendUsesTheDVDWhenThereIsNoNetwork(t *testing.T) {
	t.Parallel()

	got := images.Recommend(images.Choice{
		Architecture: images.ArchitectureAMD64,
		Flavour:      images.FlavourNetInstall,
		Offline:      true,
	}, images.Versions{})

	if !strings.Contains(got.DirectoryURL, "iso-dvd") {
		t.Errorf("DirectoryURL = %q, want the DVD directory for an offline install", got.DirectoryURL)
	}
}

func TestRecommendFallsBackWhenLiveIsUnavailable(t *testing.T) {
	t.Parallel()

	got := images.Recommend(images.Choice{
		Architecture: images.ArchitectureARM64,
		Flavour:      images.FlavourLive,
	}, images.Versions{})

	if !strings.Contains(got.DirectoryURL, "/current/arm64/iso-cd/") {
		t.Errorf("DirectoryURL = %q, want the arm64 netinst directory", got.DirectoryURL)
	}

	if !strings.Contains(got.Note, "amd64 only") {
		t.Errorf("Note = %q, want it to explain that live images exist for amd64 only", got.Note)
	}
}

func TestLiveImageNamesTheChosenDesktop(t *testing.T) {
	t.Parallel()

	got := images.Recommend(images.Choice{
		Architecture: images.ArchitectureAMD64,
		Flavour:      images.FlavourLive,
		Desktop:      images.DesktopXfce,
	}, images.Versions{})

	if !strings.HasSuffix(got.Filename, "-xfce.iso") {
		t.Errorf("Filename = %q, want it to name the xfce desktop", got.Filename)
	}
	if !strings.Contains(got.DirectoryURL, "current-live") {
		t.Errorf("DirectoryURL = %q, want the live directory", got.DirectoryURL)
	}
}

// Every recommendation must be verifiable. An image download without a checksum
// and a signature to check it against is advice we should not be giving.
func TestEveryRecommendationCanBeVerified(t *testing.T) {
	t.Parallel()

	flavours := []images.Flavour{images.FlavourNetInstall, images.FlavourFullDVD, images.FlavourLive}
	architectures := []images.Architecture{
		images.ArchitectureAMD64,
		images.ArchitectureARM64,
		images.ArchitectureI386,
	}

	for _, flavour := range flavours {
		for _, architecture := range architectures {
			got := images.Recommend(images.Choice{Architecture: architecture, Flavour: flavour}, images.Versions{})

			if !strings.HasSuffix(got.ChecksumURL, "SHA512SUMS") {
				t.Errorf("%s/%s ChecksumURL = %q, want it to end in SHA512SUMS",
					architecture, flavour, got.ChecksumURL)
			}
			if !strings.HasSuffix(got.SignatureURL, "SHA512SUMS.sign") {
				t.Errorf("%s/%s SignatureURL = %q, want a detached signature",
					architecture, flavour, got.SignatureURL)
			}
			if !strings.HasPrefix(got.DirectoryURL, "https://cdimage.debian.org/") {
				t.Errorf("%s/%s DirectoryURL = %q, want it on cdimage.debian.org",
					architecture, flavour, got.DirectoryURL)
			}
			if got.Title == "" || got.Filename == "" || got.Version == "" {
				t.Errorf("%s/%s lacks a title, filename, or version", architecture, flavour)
			}
			if !strings.HasSuffix(got.DownloadURL, got.Filename) {
				t.Errorf("%s/%s DownloadURL = %q, want it to end in %q",
					architecture, flavour, got.DownloadURL, got.Filename)
			}
		}
	}
}
