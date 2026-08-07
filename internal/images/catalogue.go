// Package images is the catalogue of Debian images worth offering a person
// choosing one from a website.
//
// The catalogue is derived, not fetched. Debian publishes its images under a
// stable directory layout, so every filename and URL below is built from a few
// constants and the current point release: a page that made a request per
// visitor would violate the rule that a user request never causes a request to
// a Debian service.
//
// There are twelve images, and the whole set is small enough to show. That is
// the point: the reader picks a machine and reads two or eight lines, instead
// of answering questions whose combinations we then have to explain away.
//
// Point releases rename every file, so the exact filename needs the current
// version. Ingest refreshes it into the database on its own schedule and the
// handler passes it in as Versions; the constants below are the fallback for
// an empty database and the values tests run against.
package images

import (
	"fmt"
	"strings"
)

// The versions used when the database has none yet. Checked against cdimage
// on 2026-08-04; a point release makes the download link 404 until ingest has
// run once, and the directory link beside it keeps working regardless.
const (
	fallbackStableVersion    = "13.6.0"
	fallbackOldstableVersion = "12.15.0"
)

// The labels used when the suites table has none. Debian renames these roughly
// every two years, which is exactly why they are read from the database first.
const (
	fallbackStableRelease    = "Debian 13 (trixie), stable"
	fallbackOldstableRelease = "Debian 12 (bookworm), oldstable"
)

const (
	cdimage        = "https://cdimage.debian.org/debian-cd"
	cdimageArchive = "https://cdimage.debian.org/cdimage/archive/latest-oldstable"
)

// Release is one Debian the catalogue offers images from: what to call it, and
// which point release its filenames carry.
type Release struct {
	Label   string
	Version string
}

// Input is everything the catalogue reads from the database. Every field is
// optional and falls back to a constant, so an unfilled database renders a page
// that is out of date rather than a page that is broken.
type Input struct {
	Stable    Release
	Oldstable Release
	// Desktops maps a package name to its version in stable, for the desktop a
	// live image carries.
	Desktops map[string]string
	// Sizes maps an image identifier to the size cdimage serves it at.
	Sizes map[string]int64
}

func (i Input) stable() Release {
	if i.Stable.Label == "" {
		i.Stable.Label = fallbackStableRelease
	}
	if i.Stable.Version == "" {
		i.Stable.Version = fallbackStableVersion
	}
	return i.Stable
}

func (i Input) oldstable() Release {
	if i.Oldstable.Label == "" {
		i.Oldstable.Label = fallbackOldstableRelease
	}
	if i.Oldstable.Version == "" {
		i.Oldstable.Version = fallbackOldstableVersion
	}
	return i.Oldstable
}

// size is what cdimage serves the file at, or the rounded figure to fall back
// on. An exact number is worth having because the tiles sit in a list, where a
// size reads as a comparison between them.
func (i Input) size(id, fallback string) string {
	bytes, ok := i.Sizes[id]
	if !ok || bytes <= 0 {
		return fallback
	}
	if bytes >= 1_000_000_000 {
		return fmt.Sprintf("%.1f GB", float64(bytes)/1_000_000_000)
	}
	return fmt.Sprintf("%.0f MB", float64(bytes)/1_000_000)
}

// Architecture is the hardware the reader is installing onto.
type Architecture string

// The architectures the catalogue covers. Debian supports more; these are the
// ones a person choosing an image from a website is plausibly using.
const (
	ArchitectureAMD64 Architecture = "amd64"
	ArchitectureARM64 Architecture = "arm64"
	ArchitectureI386  Architecture = "i386"
)

// DetectArchitecture guesses the visitor's architecture from the User-Agent, so
// the page can lead with the images that will run. It is a guess, not a verdict:
// the page says so and lists every architecture regardless. Anything
// unrecognised is amd64, which is what almost every machine browsing this page
// is.
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

// Screen names the picture on an image's tile. Every tile shows what the medium
// looks like once it has booted: a desktop for the live images, the installer's
// own screens for the rest. The desktop names double as the filenames of the
// mirrored screenshots under /assets/desktops.
type Screen string

// The screens drawn in CSS rather than mirrored from screenshots.debian.net.
const (
	ScreenInstaller Screen = "installer"
	ScreenTasksel   Screen = "tasksel"
	ScreenTerminal  Screen = "terminal"
)

// Screenshot says whether the tile is a mirrored photograph of a desktop rather
// than one of the screens drawn in CSS.
func (s Screen) Screenshot() bool {
	switch s {
	case ScreenInstaller, ScreenTasksel, ScreenTerminal:
		return false
	default:
		return true
	}
}

// Image is one downloadable file, and everything a reader needs to decide on it
// and to check it once it has landed.
type Image struct {
	ID string
	// Title names the image on its own page; Name is what the tile says, which
	// for a live image is only the desktop, because everything else about it is
	// stated once for the section it sits in.
	Title        string
	Name         string
	Detail       string
	Architecture Architecture
	Hardware     string
	Screen       Screen
	// Medium is what the medium does, said on the section rather than on each
	// tile when a section shares it. The image's own page has no section to
	// carry it, so it is kept here too.
	Medium        string
	Release       string
	Version       string
	Size          string
	NeedsNetwork  bool
	Filename      string
	DownloadURL   string
	DirectoryURL  string
	ChecksumURL   string
	SignatureURL  string
	VerifyCommand string
}

// Group is the images for one kind of machine. The reader knows their hardware
// and nothing else, so it is the first division the page makes.
type Group struct {
	Architecture Architecture
	Hardware     string
	Detail       string
	Release      string
	Version      string
	// Note is what is true of this hardware and not of the others: a missing
	// flavour, or a release that is not the current one.
	Note     string
	Sections []Section
}

// Section is images that differ in one respect only. The live images differ by
// desktop and in nothing else, so what they share is said once here rather than
// six times over: six tiles repeating the same sentence is six times the words
// and none of the information.
type Section struct {
	Title  string
	Detail string
	Images []Image
}

// Images is every image in the group, whichever section it sits in.
func (g Group) Images() []Image {
	var all []Image
	for _, section := range g.Sections {
		all = append(all, section.Images...)
	}
	return all
}

const verifyCommand = "sha512sum --check --ignore-missing SHA512SUMS"

// The desktops Debian publishes live images for, in the order the page lists
// them: by how likely a newcomer is to have heard of them, with the one that is
// not a desktop at all last.
var liveDesktops = []struct {
	name   string
	label  string
	detail string
	// pkg is the package whose version dates the desktop on the image. Reading
	// it from our own copy of the archive is the only honest way to say what
	// ships: a number written here would be a claim about somebody else's
	// release, kept up to date by remembering to.
	pkg    string
	screen Screen
}{
	{"gnome", "GNOME", "Debian's default.", "gnome-shell", "gnome"},
	{"kde", "KDE Plasma", "Deeply configurable.", "plasma-desktop", "kde"},
	{"xfce", "Xfce", "Light on memory.", "xfdesktop4", "xfce"},
	{"cinnamon", "Cinnamon", "Linux Mint's desktop.", "cinnamon", "cinnamon"},
	{"mate", "MATE", "The GNOME 2 design.", "mate-desktop", "mate"},
	{"standard", "no desktop", "A console, nothing else.", "", ScreenTerminal},
}

// DesktopPackages names the packages whose versions the live tiles state, so
// the caller knows what to look up without knowing why.
func DesktopPackages() []string {
	packages := make([]string, 0, len(liveDesktops))
	for _, desktop := range liveDesktops {
		if desktop.pkg != "" {
			packages = append(packages, desktop.pkg)
		}
	}
	return packages
}

// upstreamVersion is the part of a Debian version a reader recognises: no
// epoch, no Debian revision. "1:6.3.4-2" is Plasma 6.3.4 to everyone but dpkg.
func upstreamVersion(version string) string {
	if _, after, found := strings.Cut(version, ":"); found {
		version = after
	}
	if index := strings.LastIndex(version, "-"); index > 0 {
		version = version[:index]
	}
	return version
}

// Catalogue is every image the site offers, grouped by hardware.
func Catalogue(input Input) []Group {
	return []Group{
		{
			Architecture: ArchitectureAMD64,
			Hardware:     "64-bit PC",
			Detail:       "Most desktops and laptops, Intel or AMD",
			Release:      input.stable().Label,
			Version:      input.stable().Version,
			Sections: []Section{
				{
					Title:  "Live images",
					Detail: "Boot from USB without touching your disk. Installs from the same file when you're ready.",
					Images: liveImages(input),
				},
				installers(ArchitectureAMD64, input),
			},
		},
		{
			Architecture: ArchitectureARM64,
			Hardware:     "64-bit ARM",
			Detail:       "Raspberry Pi 4 and later, ARM servers",
			Release:      input.stable().Label,
			Version:      input.stable().Version,
			Note:         "Live images are 64-bit PC only.",
			Sections:     []Section{installers(ArchitectureARM64, input)},
		},
		{
			Architecture: ArchitectureI386,
			Hardware:     "32-bit PC",
			Detail:       "Hardware from before roughly 2007",
			Release:      input.oldstable().Label,
			Version:      input.oldstable().Version,
			Note:         "Debian 13 dropped 32-bit. This is Debian 12, supported until June 2028.",
			Sections:     []Section{installers(ArchitectureI386, input)},
		},
	}
}

// Find returns one image by the identifier its page is addressed by. Building
// the catalogue to answer this costs nothing measurable and keeps one
// definition of what exists.
func Find(id string, input Input) (Image, bool) {
	for _, group := range Catalogue(input) {
		for _, image := range group.Images() {
			if image.ID == id {
				return image, true
			}
		}
	}
	return Image{}, false
}

func liveImages(input Input) []Image {
	images := make([]Image, 0, len(liveDesktops))
	for _, desktop := range liveDesktops {
		name := desktop.label
		if version := upstreamVersion(input.Desktops[desktop.pkg]); version != "" {
			name += " " + version
		}
		images = append(images, live(desktop.name, desktop.label, name, desktop.detail, desktop.screen, input))
	}
	return images
}

func installers(architecture Architecture, input Input) Section {
	return Section{
		Title:  "Just the installer",
		Detail: "No desktop to try first. Pick your desktop partway through installing.",
		Images: []Image{
			netInstall(architecture, input),
			fullDVD(architecture, input),
		},
	}
}

// release resolves which Debian a given architecture can run: Debian 13 dropped
// 32-bit PC images, so i386 downloads come from the oldstable archive.
func release(architecture Architecture, input Input) (directoryBase string, release Release) {
	if architecture == ArchitectureI386 {
		return cdimageArchive, input.oldstable()
	}
	return cdimage + "/current", input.stable()
}

// Live images exist for amd64 only, which is why they are built here rather
// than per architecture.
func live(desktop, label, name, detail string, screen Screen, input Input) Image {
	id := fmt.Sprintf("%s-live-%s", ArchitectureAMD64, desktop)
	directory := fmt.Sprintf("%s/current-live/%s/iso-hybrid/", cdimage, ArchitectureAMD64)
	filename := fmt.Sprintf("debian-live-%s-%s-%s.iso", input.stable().Version, ArchitectureAMD64, desktop)
	title := "Live " + label
	if desktop == "standard" {
		title, name = "Live, no desktop", "No desktop"
	}
	return finish(Image{
		ID:           id,
		Title:        title,
		Name:         name,
		Detail:       detail,
		Medium:       "Boots from USB without touching your disk, and installs from the same file.",
		Architecture: ArchitectureAMD64,
		Hardware:     hardwareLabel(ArchitectureAMD64),
		Screen:       screen,
		Release:      input.stable().Label,
		Version:      input.stable().Version,
		Size:         input.size(id, "about 3 GB"),
		Filename:     filename,
		DirectoryURL: directory,
	})
}

func netInstall(architecture Architecture, input Input) Image {
	base, release := release(architecture, input)
	id := fmt.Sprintf("%s-netinst", architecture)
	return finish(Image{
		ID:           id,
		Title:        "Network installer",
		Name:         "Network installer",
		Detail:       "Downloads as it installs.",
		Architecture: architecture,
		Hardware:     hardwareLabel(architecture),
		Screen:       ScreenInstaller,
		Release:      release.Label,
		Version:      release.Version,
		Size:         input.size(id, "about 700 MB"),
		NeedsNetwork: true,
		Filename:     fmt.Sprintf("debian-%s-%s-netinst.iso", release.Version, architecture),
		DirectoryURL: fmt.Sprintf("%s/%s/iso-cd/", base, architecture),
	})
}

func fullDVD(architecture Architecture, input Input) Image {
	base, release := release(architecture, input)
	id := fmt.Sprintf("%s-dvd", architecture)
	return finish(Image{
		ID:           id,
		Title:        "Full DVD",
		Name:         "Full DVD",
		Detail:       "Installs with no network.",
		Architecture: architecture,
		Hardware:     hardwareLabel(architecture),
		Screen:       ScreenTasksel,
		Release:      release.Label,
		Version:      release.Version,
		Size:         input.size(id, "about 4 GB"),
		Filename:     fmt.Sprintf("debian-%s-%s-DVD-1.iso", release.Version, architecture),
		DirectoryURL: fmt.Sprintf("%s/%s/iso-dvd/", base, architecture),
	})
}

// finish fills in what every image derives from its directory and filename, so
// no image can be published without the means to check it.
func finish(image Image) Image {
	image.DownloadURL = image.DirectoryURL + image.Filename
	image.ChecksumURL = image.DirectoryURL + "SHA512SUMS"
	image.SignatureURL = image.DirectoryURL + "SHA512SUMS.sign"
	image.VerifyCommand = verifyCommand
	return image
}

// hardwareLabel is the architecture in the words the page uses, so a reader
// reads about their machine rather than about a port name.
func hardwareLabel(architecture Architecture) string {
	switch architecture {
	case ArchitectureAMD64:
		return "64-bit PC"
	case ArchitectureARM64:
		return "64-bit ARM"
	case ArchitectureI386:
		return "32-bit PC"
	default:
		return string(architecture)
	}
}
