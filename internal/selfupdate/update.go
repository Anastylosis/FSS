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
	if o.Verify == nil {
		o.Verify = verifyRuns
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

// Apply downloads the release archive for this platform, checks it against the
// release's SHA256SUMS, and replaces the running binary with it.
func Apply(ctx context.Context, opts Options) error {
	opts.applyDefaults()
	sweepLeftovers(opts.TargetPath)

	name := ArchiveName(opts.BinaryName, opts.Tag, opts.GOOS, opts.GOARCH)
	archive, err := findAsset(opts.Assets, name)
	if err != nil {
		return fmt.Errorf("%w — this platform may not be published", err)
	}
	sums, err := findAsset(opts.Assets, sumsAsset)
	if err != nil {
		return fmt.Errorf("%w; refusing to install an unverified binary", err)
	}

	_, _ = fmt.Fprintf(opts.Out, "  downloading %s\n", name)
	blob, err := download(ctx, opts.Client, archive.URL)
	if err != nil {
		return fmt.Errorf("downloading %s: %w", name, err)
	}
	sumList, err := download(ctx, opts.Client, sums.URL)
	if err != nil {
		return fmt.Errorf("downloading %s: %w", sumsAsset, err)
	}

	want, err := sumFor(string(sumList), name)
	if err != nil {
		return err
	}
	got := sha256.Sum256(blob)
	if hex.EncodeToString(got[:]) != want {
		return fmt.Errorf("checksum mismatch for %s: the download does not match the release's %s", name, sumsAsset)
	}
	_, _ = fmt.Fprintf(opts.Out, "  verified SHA-256 %s\n", want[:16])

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
	tmp, err := os.CreateTemp(dir, ".fss-update-*")
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
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return err
	}
	if err := opts.Verify(tmpName); err != nil {
		return fmt.Errorf("the downloaded binary does not run: %w", err)
	}
	return replace(tmpName, opts.TargetPath)
}

// verifyRuns executes the candidate with --version, which prints and exits
// without touching the network.
func verifyRuns(path string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--version").CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
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
