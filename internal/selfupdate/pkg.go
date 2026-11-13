package selfupdate

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// PackageName is the release asset for a .deb or .rpm. Asset names drop the
// tag's leading "v", and the two formats name architectures differently.
func PackageName(m Method, tag, goarch string) (string, error) {
	if !ValidTag(tag) {
		return "", fmt.Errorf("release tag %q is not a version", tag)
	}
	v := strings.TrimPrefix(tag, "v")
	switch m {
	case MethodDeb:
		if goarch == "amd64" || goarch == "arm64" {
			return fmt.Sprintf("fss_%s_%s.deb", v, goarch), nil
		}
	case MethodRPM:
		switch goarch {
		case "amd64":
			return fmt.Sprintf("fss-%s-1.x86_64.rpm", v), nil
		case "arm64":
			return fmt.Sprintf("fss-%s-1.aarch64.rpm", v), nil
		}
	default:
		return "", fmt.Errorf("%s is not a package install", m)
	}
	return "", fmt.Errorf("no %s package is published for %s", m, goarch)
}

// PackageInstallCommand is the command that installs the downloaded package.
// fss prints it and never runs it.
func PackageInstallCommand(m Method, path string) string {
	path = shellQuote(path)
	if m == MethodRPM {
		return "sudo dnf install " + path
	}
	return "sudo apt install " + path
}

var shellSafe = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./<>() -]*$`)

// shellQuote single-quotes a path unless it is plainly safe, so a cache
// directory with spaces or metacharacters cannot change the printed command.
func shellQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " <>()") && shellSafe.MatchString(s) {
		return s
	}
	if strings.HasPrefix(s, "<") && strings.HasSuffix(s, ">") && !strings.ContainsAny(s, "'") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// FetchPackage downloads the .deb or .rpm for opts.Tag, verifies it against the
// release's SHA256SUMS, and leaves it in the update cache. It returns the path.
func FetchPackage(ctx context.Context, m Method, opts Options) (string, error) {
	opts.applyDefaults()
	name, err := PackageName(m, opts.Tag, opts.GOARCH)
	if err != nil {
		return "", err
	}
	if strings.ContainsAny(name, `/\`) || name != filepath.Base(name) {
		return "", fmt.Errorf("refusing package name %q", name)
	}
	asset, err := findAsset(opts.Assets, name)
	if err != nil {
		return "", err
	}
	dir := opts.CacheDir
	if dir == "" {
		base, err := os.UserCacheDir()
		if err != nil {
			return "", fmt.Errorf("locating the cache directory: %w", err)
		}
		dir = filepath.Join(base, "fss", "updates")
	}
	blob, err := fetchVerified(ctx, opts, asset)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, ".dl-*")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(blob); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return "", err
	}
	dest := filepath.Join(dir, name)
	if err := os.Rename(tmp.Name(), dest); err != nil {
		return "", err
	}
	pruneCache(dir, name)
	return dest, nil
}

// pruneCache drops orphaned partial downloads and packages of older releases,
// keeping the one just saved.
func pruneCache(dir, keep string) {
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		n := e.Name()
		if n == keep {
			continue
		}
		stale := strings.HasPrefix(n, ".dl-")
		if stale {
			if fi, err := e.Info(); err != nil || time.Since(fi.ModTime()) < staleTempAge {
				continue
			}
		} else if !strings.HasPrefix(n, "fss") || (!strings.HasSuffix(n, ".deb") && !strings.HasSuffix(n, ".rpm")) {
			continue
		}
		_ = os.Remove(filepath.Join(dir, n))
	}
}
