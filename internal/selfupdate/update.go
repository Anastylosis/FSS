package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
)

// maxArchiveBytes caps the download. The archives are ~10 MB; the cap is there
// so a redirected or replaced URL cannot stream forever.
const maxArchiveBytes = 256 << 20

// sumsAsset is the release asset listing each archive's SHA-256.
const sumsAsset = "SHA256SUMS"

// Asset is one downloadable file on a release.
type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

// Options configures Apply. GOOS/GOARCH and the verifier are injectable so the
// whole flow can be exercised offline.
type Options struct {
	BinaryName string
	Tag        string
	Assets     []Asset
	TargetPath string
	GOOS       string
	GOARCH     string
	Client     *http.Client
	Out        io.Writer
	// Verify runs the freshly extracted binary before it replaces anything.
	// A download that cannot execute must not become the installed fss.
	Verify func(path string) error
	// CacheDir is where FetchPackage leaves a package; empty means
	// $XDG_CACHE_HOME/fss/updates.
	CacheDir string
	// PublicKey and RequireSignature default to the package-level values.
	PublicKey        *string
	RequireSignature *bool
}

func (o *Options) applyDefaults() {
	if o.GOOS == "" {
		o.GOOS = runtime.GOOS
	}
	if o.GOARCH == "" {
		o.GOARCH = runtime.GOARCH
	}
	if o.Client == nil {
		o.Client = httpx.NewClient(5 * time.Minute)
	}
	if o.Out == nil {
		o.Out = io.Discard
	}
	if o.PublicKey == nil {
		o.PublicKey = &PublicKey
	}
	if o.RequireSignature == nil {
		req := RequireSignature
		o.RequireSignature = &req
	}
}

// ArchiveName is the release asset this platform needs.
func ArchiveName(binary, tag, goos, goarch string) string {
	ext := ".tar.gz"
	if goos == "windows" {
		ext = ".zip"
	}
	return fmt.Sprintf("%s-%s-%s-%s%s", binary, tag, goos, goarch, ext)
}

func findAsset(assets []Asset, name string) (Asset, error) {
	for _, a := range assets {
		if a.Name == name {
			return a, nil
		}
	}
	return Asset{}, fmt.Errorf("release has no asset %q", name)
}

// fetchVerified downloads the asset and checks it against the release's
// SHA256SUMS, itself checked against SHA256SUMS.sig when a key is embedded.
func fetchVerified(ctx context.Context, opts Options, asset Asset) ([]byte, error) {
	sums, err := findAsset(opts.Assets, sumsAsset)
	if err != nil {
		return nil, fmt.Errorf("%w; refusing to install an unverified download", err)
	}
	_, _ = fmt.Fprintf(opts.Out, "  downloading %s\n", asset.Name)
	blob, err := download(ctx, opts.Client, asset.URL)
	if err != nil {
		return nil, fmt.Errorf("downloading %s: %w", asset.Name, err)
	}
	sumList, err := download(ctx, opts.Client, sums.URL)
	if err != nil {
		return nil, fmt.Errorf("downloading %s: %w", sumsAsset, err)
	}
	var sig []byte
	if sa, err := findAsset(opts.Assets, sigAsset); err == nil {
		if sig, err = download(ctx, opts.Client, sa.URL); err != nil {
			return nil, fmt.Errorf("downloading %s: %w", sigAsset, err)
		}
	}
	if err := checkSignature(opts.Out, *opts.PublicKey, *opts.RequireSignature, sumList, sig); err != nil {
		return nil, err
	}

	want, err := sumFor(string(sumList), asset.Name)
	if err != nil {
		return nil, err
	}
	got := sha256.Sum256(blob)
	if hex.EncodeToString(got[:]) != want {
		return nil, fmt.Errorf("checksum mismatch for %s: the download does not match the release's %s", asset.Name, sumsAsset)
	}
	_, _ = fmt.Fprintf(opts.Out, "  verified SHA-256 %s\n", want[:16])
	return blob, nil
}

// Apply downloads the release archive for this platform, checks it against the
// release's SHA256SUMS, and replaces the running binary with it.
func Apply(ctx context.Context, opts Options) error {
	opts.applyDefaults()
	if opts.Verify == nil {
		opts.Verify = func(p string) error { return verifyRuns(ctx, p, opts.Tag) }
	}
	if !ValidTag(opts.Tag) {
		return fmt.Errorf("release tag %q is not a version", opts.Tag)
	}
	sweepLeftovers(opts.TargetPath)

	name := ArchiveName(opts.BinaryName, opts.Tag, opts.GOOS, opts.GOARCH)
	archive, err := findAsset(opts.Assets, name)
	if err != nil {
		return fmt.Errorf("%w — this platform may not be published", err)
	}
	blob, err := fetchVerified(ctx, opts, archive)
	if err != nil {
		return err
	}

	binary := opts.BinaryName
	if opts.GOOS == "windows" {
		binary += ".exe"
	}
	extracted, err := extract(blob, binary, opts.GOOS)
	if err != nil {
		return err
	}

	return install(extracted, opts)
}

// install writes the new binary beside the old one and swaps it in. The temp
// file is deliberately in the target's own directory: a rename across
// filesystems is not atomic, and /tmp is routinely a different one.
func install(binary []byte, opts Options) error {
	dir := filepath.Dir(opts.TargetPath)
	pattern := ".fss-update-*"
	if opts.GOOS == "windows" {
		pattern += ".exe"
	}
	mode := os.FileMode(0o755)
	info, statErr := os.Stat(opts.TargetPath)
	if statErr == nil {
		mode = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return fmt.Errorf("cannot write to %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(binary); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return err
	}
	if mode&0o100 == 0 {
		// The check has to execute it; the original mode is restored below.
		if err := os.Chmod(tmpName, mode|0o100); err != nil {
			return err
		}
	}
	if err := opts.Verify(tmpName); err != nil {
		return fmt.Errorf("the downloaded binary does not run: %w", err)
	}
	if mode&0o100 == 0 {
		if err := os.Chmod(tmpName, mode); err != nil {
			return err
		}
	}
	if statErr == nil {
		preserveOwner(tmpName, info)
	}
	return replace(tmpName, opts.TargetPath)
}

// verifyRuns executes the candidate with --version, which prints and exits
// without touching the network, and requires the output to name the release
// being installed: the checksum cannot catch a mispackaged archive.
func verifyRuns(ctx context.Context, path, tag string) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--version").CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	if !mentionsTag(string(out), tag) {
		return fmt.Errorf("it reports %q, not %s", strings.TrimSpace(string(out)), tag)
	}
	return nil
}

// mentionsTag matches the tag as a whole version token, so 1.32.0 is not
// satisfied by 1.32.0-rc1 or 11.32.0.
func mentionsTag(out, tag string) bool {
	want := strings.TrimPrefix(tag, "v")
	if want == "" {
		return true
	}
	re := regexp.MustCompile(`(^|[^0-9A-Za-z.+-])v?` + regexp.QuoteMeta(want) + `($|[^0-9A-Za-z.+-])`)
	return re.MatchString(out)
}

// staleTempAge is how old a .fss-update-* file must be before it is treated as
// the orphan of an interrupted update rather than a concurrent one's.
const staleTempAge = 10 * time.Minute

func sweepStaleTemps(targetPath string) {
	matches, _ := filepath.Glob(filepath.Join(filepath.Dir(targetPath), ".fss-update-*"))
	for _, m := range matches {
		if fi, err := os.Stat(m); err == nil && time.Since(fi.ModTime()) > staleTempAge {
			_ = os.Remove(m)
		}
	}
}

func download(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	resp, err := httpx.Do(ctx, client, httpx.Request{URL: url})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBodyN(resp.Body, maxArchiveBytes)
}

// sumFor reads one file's hash out of a SHA256SUMS listing.
func sumFor(list, name string) (string, error) {
	for _, line := range strings.Split(list, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		// The second field carries a leading "*" for binary mode on some
		// implementations, and may be a path rather than a bare name.
		if path.Base(strings.TrimPrefix(fields[1], "*")) == name {
			return strings.ToLower(fields[0]), nil
		}
	}
	return "", fmt.Errorf("%s lists no entry for %s", sumsAsset, name)
}

// extract pulls the single binary out of the release archive.
func extract(blob []byte, binary, goos string) ([]byte, error) {
	if goos == "windows" {
		return fromZip(blob, binary)
	}
	return fromTarGz(blob, binary)
}

func fromTarGz(blob []byte, binary string) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(blob))
	if err != nil {
		return nil, fmt.Errorf("archive is not gzip: %w", err)
	}
	defer func() { _ = gz.Close() }()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading archive: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg || path.Base(hdr.Name) != binary {
			continue
		}
		return io.ReadAll(io.LimitReader(tr, maxArchiveBytes))
	}
	return nil, fmt.Errorf("archive contains no %s", binary)
}

func fromZip(blob []byte, binary string) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(blob), int64(len(blob)))
	if err != nil {
		return nil, fmt.Errorf("archive is not a zip: %w", err)
	}
	for _, f := range zr.File {
		if path.Base(f.Name) != binary {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer func() { _ = rc.Close() }()
		return io.ReadAll(io.LimitReader(rc, maxArchiveBytes))
	}
	return nil, fmt.Errorf("archive contains no %s", binary)
}
