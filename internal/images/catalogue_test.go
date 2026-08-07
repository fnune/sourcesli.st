package images_test

import (
	"strings"
	"testing"

	"github.com/fnune/sourcesli.st/internal/images"
)

// The User-Agent is a guess, not a verdict: the page says so and lists every
// architecture regardless. What matters is that the guess never lands outside
// the architectures the catalogue covers.
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

// Debian publishes live images for 64-bit PCs only. The catalogue says so by
// not listing any elsewhere, and the group carries the sentence that explains
// the absence rather than leaving a reader to wonder.
func TestOnlyTheSixtyFourBitPCHasLiveImages(t *testing.T) {
	t.Parallel()

	for _, group := range images.Catalogue(images.Input{}) {
		live := 0
		for _, image := range group.Images() {
			if strings.Contains(image.ID, "-live-") {
				live++
			}
		}

		switch group.Architecture {
		case images.ArchitectureAMD64:
			if live != 6 {
				t.Errorf("%s has %d live images, want one per desktop", group.Architecture, live)
			}
		default:
			if live != 0 {
				t.Errorf("%s has %d live images, and Debian publishes none", group.Architecture, live)
			}
			if group.Note == "" {
				t.Errorf("%s should say why it has no live images", group.Architecture)
			}
		}
	}
}

// The database versions win over the compiled-in fallbacks, because ingest
// refreshes them while a fallback only changes with a deploy.
func TestFilenamesCarryTheObservedVersions(t *testing.T) {
	t.Parallel()

	catalogue := images.Catalogue(images.Input{
		Stable:    images.Release{Version: "13.9.0"},
		Oldstable: images.Release{Version: "12.16.0"},
	})

	gnome, ok := images.Find("amd64-live-gnome", images.Input{Stable: images.Release{Version: "13.9.0"}})
	if !ok {
		t.Fatal("the catalogue should have a GNOME live image")
	}
	if gnome.Filename != "debian-live-13.9.0-amd64-gnome.iso" {
		t.Errorf("Filename = %q, want the observed stable version in it", gnome.Filename)
	}

	for _, group := range catalogue {
		for _, image := range group.Images() {
			if !strings.Contains(image.Filename, group.Version) {
				t.Errorf("%s carries %q, want the version its group states", image.ID, image.Filename)
			}
		}
	}
}

// Debian 13 dropped 32-bit PC images, so i386 must come from the oldstable
// archive and say so, or every link in the group is a guaranteed 404.
func TestThirtyTwoBitComesFromTheOldstableArchive(t *testing.T) {
	t.Parallel()

	for _, group := range images.Catalogue(images.Input{}) {
		if group.Architecture != images.ArchitectureI386 {
			continue
		}
		if !strings.Contains(group.Release, "oldstable") {
			t.Errorf("Release = %q, want it to name the release these come from", group.Release)
		}
		if !strings.Contains(group.Note, "Debian 12") {
			t.Errorf("Note = %q, want it to say this is Debian 12 and until when it is supported", group.Note)
		}
		for _, image := range group.Images() {
			if !strings.Contains(image.DirectoryURL, "/archive/latest-oldstable/i386/") {
				t.Errorf("%s points at %q, want the oldstable archive", image.ID, image.DirectoryURL)
			}
		}
	}
}

// Every image must be verifiable and addressable. A download without a checksum
// and a signature to check it against is advice we should not be giving, and an
// identifier that is not unique makes two images share a page.
func TestEveryImageCanBeVerifiedAndAddressed(t *testing.T) {
	t.Parallel()

	seen := map[string]bool{}
	count := 0

	for _, group := range images.Catalogue(images.Input{}) {
		for _, image := range group.Images() {
			count++
			if seen[image.ID] {
				t.Errorf("%s is listed twice", image.ID)
			}
			seen[image.ID] = true

			if found, ok := images.Find(image.ID, images.Input{}); !ok || found.Filename != image.Filename {
				t.Errorf("Find(%q) does not return the image the catalogue lists", image.ID)
			}
			if !strings.HasSuffix(image.ChecksumURL, "SHA512SUMS") {
				t.Errorf("%s ChecksumURL = %q, want it to end in SHA512SUMS", image.ID, image.ChecksumURL)
			}
			if !strings.HasSuffix(image.SignatureURL, "SHA512SUMS.sign") {
				t.Errorf("%s SignatureURL = %q, want a detached signature", image.ID, image.SignatureURL)
			}
			if !strings.HasPrefix(image.DirectoryURL, "https://cdimage.debian.org/") {
				t.Errorf("%s DirectoryURL = %q, want it on cdimage.debian.org", image.ID, image.DirectoryURL)
			}
			if !strings.HasSuffix(image.DownloadURL, image.Filename) {
				t.Errorf("%s DownloadURL = %q, want it to end in %q", image.ID, image.DownloadURL, image.Filename)
			}
			if image.Title == "" || image.Name == "" || image.Detail == "" ||
				image.Size == "" || image.Hardware == "" {
				t.Errorf("%s lacks a title, name, detail, size, or hardware", image.ID)
			}
			// The tile shows what the medium looks like once booted, so a
			// screenshot means a desktop and a drawn screen means everything
			// else. Getting this wrong renders a missing image.
			if image.Screen.Screenshot() != (strings.Contains(image.ID, "-live-") && image.Name != "No desktop") {
				t.Errorf("%s has screen %q, which does not match its kind", image.ID, image.Screen)
			}
		}
	}

	if count != 12 {
		t.Errorf("the catalogue has %d images, want the 12 Debian publishes for these machines", count)
	}
}

func TestFindRejectsAnythingNotInTheCatalogue(t *testing.T) {
	t.Parallel()

	for _, id := range []string{"", "amd64-live", "amd64-live-plan9", "arm64-live-gnome", "../etc/passwd"} {
		if _, ok := images.Find(id, images.Input{}); ok {
			t.Errorf("Find(%q) returned an image", id)
		}
	}
}

// The tile says what the desktop ships as, and takes the number from the
// archive rather than from a constant somebody has to remember to update. A
// Debian version is not what a reader recognises, so the epoch and the Debian
// revision come off.
func TestLiveTilesNameTheDesktopVersionThatShips(t *testing.T) {
	t.Parallel()

	input := images.Input{Desktops: map[string]string{
		"gnome-shell":    "48.3-2",
		"plasma-desktop": "4:6.3.4-1",
		"xfdesktop4":     "4.20.0-1",
	}}

	named := map[string]string{}
	for _, group := range images.Catalogue(input) {
		for _, image := range group.Images() {
			named[image.ID] = image.Name
		}
	}

	for id, want := range map[string]string{
		"amd64-live-gnome":    "GNOME 48.3",
		"amd64-live-kde":      "KDE Plasma 6.3.4",
		"amd64-live-xfce":     "Xfce 4.20.0",
		"amd64-live-cinnamon": "Cinnamon",
		"amd64-live-standard": "No desktop",
	} {
		if named[id] != want {
			t.Errorf("%s is named %q, want %q", id, named[id], want)
		}
	}
}

// A size on a tile sits beside eleven others, so it reads as a comparison. The
// figure ingest observed wins over the rounded fallback, and the fallback is
// what an unfilled database says rather than a wrong number.
func TestSizesComeFromWhatWasObserved(t *testing.T) {
	t.Parallel()

	input := images.Input{Sizes: map[string]int64{
		"amd64-live-gnome": 3_216_178_176,
		"amd64-netinst":    663_748_608,
	}}

	sizes := map[string]string{}
	for _, group := range images.Catalogue(input) {
		for _, image := range group.Images() {
			sizes[image.ID] = image.Size
		}
	}

	for id, want := range map[string]string{
		"amd64-live-gnome": "3.2 GB",
		"amd64-netinst":    "664 MB",
		"arm64-netinst":    "about 700 MB",
		"i386-dvd":         "about 4 GB",
	} {
		if sizes[id] != want {
			t.Errorf("%s is %q, want %q", id, sizes[id], want)
		}
	}
}

// The release labels are Debian's to change, and it does roughly every two
// years. They come from the suites we seed rather than from a constant.
func TestReleaseLabelsComeFromTheInput(t *testing.T) {
	t.Parallel()

	input := images.Input{
		Stable:    images.Release{Label: "Debian 14 (forky), stable", Version: "14.0.0"},
		Oldstable: images.Release{Label: "Debian 13 (trixie), oldstable", Version: "13.9.0"},
	}

	for _, group := range images.Catalogue(input) {
		want := input.Stable
		if group.Architecture == images.ArchitectureI386 {
			want = input.Oldstable
		}
		if group.Release != want.Label || group.Version != want.Version {
			t.Errorf("%s is %q at %q, want %q at %q",
				group.Architecture, group.Release, group.Version, want.Label, want.Version)
		}
	}
}
