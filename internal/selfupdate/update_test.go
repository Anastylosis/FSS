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
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const newBinary = "#!/bin/sh\necho fss v1.32.0\n"

func tarGz(t *testing.T, name, content string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func zipped(t *testing.T, name, content string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// releaseServer serves one archive and a SHA256SUMS listing. sums, when
// non-empty, replaces the real digest so a corrupted download can be simulated.
func releaseServer(t *testing.T, archiveName string, archive []byte, sums string) (*httptest.Server, []Asset) {
	t.Helper()
	if sums == "" {
		d := sha256.Sum256(archive)
		sums = fmt.Sprintf("%s  %s\n", hex.EncodeToString(d[:]), archiveName)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch strings.TrimPrefix(r.URL.Path, "/") {
		case archiveName:
			_, _ = w.Write(archive)
		case sumsAsset:
			_, _ = w.Write([]byte(sums))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, []Asset{
		{Name: archiveName, URL: srv.URL + "/" + archiveName},
		{Name: sumsAsset, URL: srv.URL + "/" + sumsAsset},
	}
}

func target(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fss")
	if err := os.WriteFile(path, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func options(path string, assets []Asset, srvClient *http.Client) Options {
	return Options{
		BinaryName: "fss",
		Tag:        "v1.32.0",
		Assets:     assets,
		TargetPath: path,
		GOOS:       "linux",
		GOARCH:     "amd64",
		Client:     srvClient,
		Verify:     func(string) error { return nil },
	}
}

func TestApplyReplacesTheBinary(t *testing.T) {
	name := ArchiveName("fss", "v1.32.0", "linux", "amd64")
	srv, assets := releaseServer(t, name, tarGz(t, "fss", newBinary), "")
	path := target(t)

	if err := Apply(context.Background(), options(path, assets, srv.Client())); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != newBinary {
		t.Errorf("target holds %q, want the downloaded binary", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("installed binary is not executable (mode %v)", info.Mode().Perm())
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".fss-update-*")); len(left) != 0 {
		t.Errorf("temp files left behind: %v", left)
	}
}

// A download that does not match the release's own checksum must never reach
// the filesystem the operator runs from.
func TestApplyRefusesAChecksumMismatch(t *testing.T) {
	name := ArchiveName("fss", "v1.32.0", "linux", "amd64")
	srv, assets := releaseServer(t, name, tarGz(t, "fss", newBinary),
		"0000000000000000000000000000000000000000000000000000000000000000  "+name+"\n")
	path := target(t)

	err := Apply(context.Background(), options(path, assets, srv.Client()))
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("err = %v, want a checksum mismatch", err)
	}
	assertUntouched(t, path)
}

// Without SHA256SUMS there is nothing to check the download against, so the
// update is refused rather than trusted.
func TestApplyRefusesWithoutChecksums(t *testing.T) {
	name := ArchiveName("fss", "v1.32.0", "linux", "amd64")
	srv, assets := releaseServer(t, name, tarGz(t, "fss", newBinary), "")
	path := target(t)

	opts := options(path, assets[:1], srv.Client()) // archive only
	err := Apply(context.Background(), opts)
	if err == nil || !strings.Contains(err.Error(), sumsAsset) {
		t.Fatalf("err = %v, want a refusal naming %s", err, sumsAsset)
	}
	assertUntouched(t, path)
}

// The checksum proves the bytes arrived intact, not that they run here — a
// wrong-architecture build passes it and then cannot exec.
func TestApplyKeepsTheOldBinaryWhenTheNewOneWillNotRun(t *testing.T) {
	name := ArchiveName("fss", "v1.32.0", "linux", "amd64")
	srv, assets := releaseServer(t, name, tarGz(t, "fss", newBinary), "")
	path := target(t)

	opts := options(path, assets, srv.Client())
	opts.Verify = func(string) error { return fmt.Errorf("exec format error") }

	err := Apply(context.Background(), opts)
	if err == nil || !strings.Contains(err.Error(), "does not run") {
		t.Fatalf("err = %v, want a refusal", err)
	}
	assertUntouched(t, path)
}

func TestApplyReportsAMissingPlatformAsset(t *testing.T) {
	name := ArchiveName("fss", "v1.32.0", "linux", "amd64")
	srv, assets := releaseServer(t, name, tarGz(t, "fss", newBinary), "")
	path := target(t)

	opts := options(path, assets, srv.Client())
	opts.GOARCH = "riscv64"
	err := Apply(context.Background(), opts)
	if err == nil || !strings.Contains(err.Error(), "riscv64") {
		t.Fatalf("err = %v, want an error naming the missing asset", err)
	}
	assertUntouched(t, path)
}

func TestApplyExtractsFromAZipOnWindows(t *testing.T) {
	name := ArchiveName("fss", "v1.32.0", "windows", "amd64")
	srv, assets := releaseServer(t, name, zipped(t, "fss.exe", newBinary), "")
	path := target(t)

	opts := options(path, assets, srv.Client())
	opts.GOOS = "windows"
	if err := Apply(context.Background(), opts); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != newBinary {
		t.Errorf("target holds %q, want the downloaded binary", got)
	}
}

func TestExtractRejectsAnArchiveWithoutTheBinary(t *testing.T) {
	if _, err := extract(tarGz(t, "README.md", "not a binary"), "fss", "linux"); err == nil {
		t.Error("an archive without the binary must be an error")
	}
	if _, err := extract([]byte("not an archive"), "fss", "linux"); err == nil {
		t.Error("a non-gzip payload must be an error")
	}
}

// The default verifier is what stands between a corrupt download and the
// installed binary, so it has to actually reject one.
func TestVerifyRunsRejectsSomethingThatIsNotAProgram(t *testing.T) {
	path := filepath.Join(t.TempDir(), "junk")
	if err := os.WriteFile(path, []byte("\x00\x01not a program"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := verifyRuns(path); err == nil {
		t.Error("verifyRuns accepted a file that cannot execute")
	}
}

func assertUntouched(t *testing.T, path string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("target is gone: %v", err)
	}
	if string(got) != "old binary" {
		t.Errorf("target was modified: %q", got)
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".fss-update-*")); len(left) != 0 {
		t.Errorf("temp files left behind: %v", left)
	}
}
