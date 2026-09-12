package cmd

import (
	"context"
	"fmt"
	"os"
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
func runSelfUpdate(ctx context.Context, latest latestRelease) error {
	if strings.TrimPrefix(buildVersion, "v") == strings.TrimPrefix(latest.TagName, "v") {
		return nil
	}
	if buildVersion == "dev" || buildVersion == "" {
		return fmt.Errorf("this is a development build, not a release — install a release, or `go install github.com/Anastylosis/FSS@latest`")
	}

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
		fmt.Printf("  the previous binary is %s.old and is removed on the next run\n", exe)
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
