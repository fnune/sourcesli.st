package images_test

import (
	"strings"
	"testing"

	"github.com/fnune/sourcesli.st/internal/images"
)

func TestRecommendDefaultsToTheNetworkInstaller(t *testing.T) {
	t.Parallel()

	got := images.Recommend(images.Choice{})

	if !strings.Contains(got.DirectoryURL, "/current/amd64/iso-cd/") {
		t.Errorf("DirectoryURL = %q, want the amd64 netinst directory", got.DirectoryURL)
	}
	if !strings.Contains(got.FilenamePattern, "netinst") {
		t.Errorf("FilenamePattern = %q, want a netinst pattern", got.FilenamePattern)
	}
}

func TestRecommendUsesTheDVDWhenThereIsNoNetwork(t *testing.T) {
	t.Parallel()

	got := images.Recommend(images.Choice{
		Architecture: images.ArchitectureAMD64,
		Flavour:      images.FlavourNetInstall,
		Offline:      true,
	})

	if !strings.Contains(got.DirectoryURL, "iso-dvd") {
		t.Errorf("DirectoryURL = %q, want the DVD directory for an offline install", got.DirectoryURL)
	}
}

func TestRecommendFallsBackWhenLiveIsUnavailable(t *testing.T) {
	t.Parallel()

	got := images.Recommend(images.Choice{
		Architecture: images.ArchitectureARM64,
		Flavour:      images.FlavourLive,
	})

	if !strings.Contains(got.DirectoryURL, "/current/arm64/iso-cd/") {
		t.Errorf("DirectoryURL = %q, want the arm64 netinst directory", got.DirectoryURL)
	}

	var explained bool
	for _, note := range got.Notes {
		if strings.Contains(note, "amd64 only") {
			explained = true
		}
	}
	if !explained {
		t.Error("Notes should explain that live images exist for amd64 only")
	}
}

func TestLiveImageNamesTheChosenDesktop(t *testing.T) {
	t.Parallel()

	got := images.Recommend(images.Choice{
		Architecture: images.ArchitectureAMD64,
		Flavour:      images.FlavourLive,
		Desktop:      images.DesktopXfce,
	})

	if !strings.Contains(got.FilenamePattern, "-xfce.iso") {
		t.Errorf("FilenamePattern = %q, want it to name the xfce desktop", got.FilenamePattern)
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
			got := images.Recommend(images.Choice{Architecture: architecture, Flavour: flavour})

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
			if got.Title == "" || got.Rationale == "" {
				t.Errorf("%s/%s has no title or rationale", architecture, flavour)
			}
		}
	}
}

// The single most common piece of stale advice about Debian images is that
// official builds lack firmware. If this note ever disappears the chooser has
// stopped answering the question people actually arrive with.
func TestRecommendationsMentionFirmwareIsIncluded(t *testing.T) {
	t.Parallel()

	got := images.Recommend(images.Choice{})

	var mentioned bool
	for _, note := range got.Notes {
		if strings.Contains(note, "non-free firmware") {
			mentioned = true
		}
	}
	if !mentioned {
		t.Error("Notes should say that official images include non-free firmware since Debian 12")
	}
}
