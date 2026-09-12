// Package selfupdate replaces the running fss binary with the latest release.
//
// See docs/usage.md § "Updating" for what it refuses to do and why.
package selfupdate

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Method is how the running binary got onto this machine.
type Method string

const (
	MethodRelease  Method = "release"
	MethodDeb      Method = "deb"
	MethodRPM      Method = "rpm"
	MethodAUR      Method = "aur"
	MethodDocker   Method = "docker"
	MethodHomebrew Method = "homebrew"
	MethodSource   Method = "source"
	MethodUnknown  Method = "unknown"
)

// Env is what detection has to work with: the stamp the build left, and the
// resolved location of this copy.
type Env struct {
	// Path is the running binary with symlinks resolved — a Homebrew install
	// is a symlink into the Cellar, which is the only thing that identifies it.
	Path string
	// Channel is the build stamp (see cmd.SetVersion). Empty for anything built
	// before the stamp existed, and for a plain `go build`.
	Channel string
	// Version is the build version, "dev" for an unstamped local build.
	Version string
	// InDocker reports whether this process is running inside a container.
	InDocker bool
}

// Install is what detection concluded.
type Install struct {
	Method Method
	Path   string
	// Advice, when non-empty, is the command the operator should run instead:
	// this copy belongs to something else, and replacing it in place would
	// leave that thing holding a file it no longer recognises.
	Advice string
}

// Managed reports whether something other than fss owns this copy.
func (i Install) Managed() bool { return i.Advice != "" }

// InDocker reports whether the process is running inside a container.
func InDocker() bool {
	_, err := os.Stat("/.dockerenv")
	return err == nil
}

// Detect works out how this copy was installed.
//
// The build stamp says how the binary was *shipped*; the path says whether this
// copy is still the one that package owns. Both are needed: Homebrew ships the
// release archive unchanged so no stamp can name it, and a .deb binary someone
// copied into ~/bin is no longer apt's business and may update itself.
func Detect(env Env) Install {
	path := env.Path
	channel := strings.ToLower(strings.TrimSpace(env.Channel))

	switch {
	case channel == string(MethodDocker) || env.InDocker:
		return Install{Method: MethodDocker, Path: path,
			Advice: "docker pull ghcr.io/anastylosis/fss:latest"}

	case inHomebrewCellar(path):
		return Install{Method: MethodHomebrew, Path: path,
			Advice: "brew upgrade fss"}

	case channel == string(MethodDeb) && systemOwned(path):
		return Install{Method: MethodDeb, Path: path,
			Advice: "sudo apt update && sudo apt install --only-upgrade fss"}

	case channel == string(MethodRPM) && systemOwned(path):
		return Install{Method: MethodRPM, Path: path,
			Advice: "sudo dnf upgrade fss"}

	case channel == string(MethodAUR) && systemOwned(path):
		return Install{Method: MethodAUR, Path: path,
			Advice: "yay -Syu fss   (or your AUR helper of choice)"}

	case env.Version == "dev" && inGoBin(path):
		return Install{Method: MethodSource, Path: path,
			Advice: "go install github.com/Anastylosis/FSS@latest"}

	case channel == string(MethodRelease):
		return Install{Method: MethodRelease, Path: path}

	case channel == "":
		return Install{Method: MethodUnknown, Path: path}
	}
	return Install{Method: Method(channel), Path: path}
}

// inHomebrewCellar reports whether the resolved path lives in a Homebrew
// Cellar. `brew --prefix` varies (/opt/homebrew, /usr/local, linuxbrew), but
// every install keeps the real binary under a "Cellar" directory.
func inHomebrewCellar(path string) bool {
	for _, seg := range strings.Split(filepath.ToSlash(path), "/") {
		if seg == "Cellar" {
			return true
		}
	}
	return false
}

// systemOwned reports whether the path is where a Linux package manager puts a
// binary. /usr/local/bin is deliberately excluded: that is where a hand-extracted
// tarball goes, and no package owns it.
func systemOwned(path string) bool {
	if runtime.GOOS == "windows" {
		return false
	}
	dir := filepath.Dir(filepath.ToSlash(path))
	return dir == "/usr/bin" || dir == "/bin"
}

// inGoBin reports whether the path looks like `go install` output.
func inGoBin(path string) bool {
	slash := filepath.ToSlash(path)
	if gobin := os.Getenv("GOBIN"); gobin != "" {
		if strings.HasPrefix(slash, filepath.ToSlash(gobin)+"/") {
			return true
		}
	}
	if gopath := os.Getenv("GOPATH"); gopath != "" {
		if strings.HasPrefix(slash, filepath.ToSlash(gopath)+"/bin/") {
			return true
		}
	}
	return strings.Contains(slash, "/go/bin/")
}

// Describe renders the install for a human.
func (i Install) Describe() string {
	if i.Method == MethodUnknown {
		return fmt.Sprintf("%s (build names no channel)", i.Path)
	}
	return fmt.Sprintf("%s (%s)", i.Path, i.Method)
}
