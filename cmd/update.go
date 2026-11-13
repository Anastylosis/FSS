package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/Anastylosis/FSS/internal/selfupdate"
)

// runSelfUpdate replaces this binary with the release just fetched.
//
// It refuses in two cases rather than doing something surprising: a build with
// no release to compare against, and a copy some package manager owns — see
// docs/usage.md § "Updating".
func runSelfUpdate(ctx context.Context, latest latestRelease, allowDowngrade bool) error {
	if strings.TrimPrefix(buildVersion, "v") == strings.TrimPrefix(latest.TagName, "v") {
		return nil
	}
	if buildVersion == "dev" || buildVersion == "" {
		return fmt.Errorf("this is a development build, not a release — install a release, or `go install github.com/Anastylosis/FSS@latest`")
	}
	if err := checkDowngrade(buildVersion, latest.TagName, allowDowngrade); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()

	exe, err := resolveExecutable()
	if err != nil {
		return err
	}

	install := selfupdate.Detect(selfupdate.Env{
		Path:     exe,
		Channel:  buildChannel,
		Version:  buildVersion,
		InDocker: selfupdate.InDocker(),
	})
	if isSystemPackage(install) {
		return fetchSystemPackage(ctx, install, latest, os.Stdout)
	}
	if install.Managed() {
		return fmt.Errorf("this copy is installed by %s (%s) — update it with:\n\n  %s",
			install.Method, install.Path, install.Advice)
	}

	fmt.Printf("\nUpdating %s\n", install.Describe())
	if err := selfupdate.Apply(ctx, selfupdate.Options{
		BinaryName: "fss",
		Tag:        latest.TagName,
		Assets:     latest.Assets,
		TargetPath: exe,
		Out:        os.Stdout,
	}); err != nil {
		return err
	}

	fmt.Printf("  installed %s\n", latest.TagName)
	if runtime.GOOS == "windows" {
		fmt.Printf("  the previous binary is %s.old and is removed on the next update\n", exe)
	}
	return nil
}

// resolveExecutable returns the running binary with symlinks resolved. The
// resolution matters twice: a symlink is what a Homebrew install looks like,
// and replacing the link rather than its target would leave the old binary in
// place under a new name.
func resolveExecutable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locating the running binary: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return "", fmt.Errorf("resolving %s: %w", exe, err)
	}
	return resolved, nil
}

// checkDowngrade refuses to move to an older release than the running build.
// An unparseable version on either side is let through: nothing can be said
// about its order.
func checkDowngrade(current, latest string, allow bool) error {
	if c, ok := selfupdate.CompareVersions(latest, current); ok && c < 0 && !allow {
		return fmt.Errorf("this build (%s) is newer than the latest release (%s) — pass --allow-downgrade to install it anyway", current, latest)
	}
	return nil
}

// isSystemPackage reports whether the copy is one apt/dnf owns, as opposed to a
// deb/rpm-stamped binary copied elsewhere, which updates in place.
func isSystemPackage(install selfupdate.Install) bool {
	return install.Managed() && (install.Method == selfupdate.MethodDeb || install.Method == selfupdate.MethodRPM)
}

// fetchSystemPackage downloads and verifies the .deb or .rpm for the latest
// release and prints the command that installs it. fss never runs sudo.
func fetchSystemPackage(ctx context.Context, install selfupdate.Install, latest latestRelease, out io.Writer) error {
	_, _ = fmt.Fprintf(out, "\nThis copy is installed by %s (%s); fetching the package for %s\n", install.Method, install.Path, latest.TagName)
	path, err := selfupdate.FetchPackage(ctx, install.Method, selfupdate.Options{
		Tag:    latest.TagName,
		Assets: latest.Assets,
		Out:    out,
	})
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "  saved %s\n\nInstall it with:\n\n  %s\n", path, selfupdate.PackageInstallCommand(install.Method, path))
	return nil
}
