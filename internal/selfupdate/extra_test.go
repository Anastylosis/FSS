package selfupdate

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
		ok   bool
	}{
		{"v1.32.0", "v1.32.0", 0, true},
		{"v1.31.0", "v1.32.0", -1, true},
		{"v1.10.0", "v1.9.0", 1, true},
		{"v2.0.0", "v1.99.99", 1, true},
		{"v1.35.0-rc1", "v1.35.0", -1, true},
		{"v1.35.0-dev", "v1.34.0", 1, true},
		{"v1.35.0-rc.2", "v1.35.0-rc.10", -1, true},
		{"v1.35.0-rc.1", "v1.35.0-rc", 1, true},
		{"1.2.3+build", "v1.2.3", 0, true},
		{"dev", "v1.0.0", 0, false},
		{"v1.0", "v1.0.0", 0, false},
	}
	for _, c := range cases {
		got, ok := CompareVersions(c.a, c.b)
		if got != c.want || ok != c.ok {
			t.Errorf("CompareVersions(%q,%q) = %d,%v want %d,%v", c.a, c.b, got, ok, c.want, c.ok)
		}
	}
}

func TestPackageNameArchMapping(t *testing.T) {
	cases := []struct {
		m    Method
		arch string
		want string
		err  bool
	}{
		{MethodDeb, "amd64", "fss_1.32.0_amd64.deb", false},
		{MethodDeb, "arm64", "fss_1.32.0_arm64.deb", false},
		{MethodRPM, "amd64", "fss-1.32.0-1.x86_64.rpm", false},
		{MethodRPM, "arm64", "fss-1.32.0-1.aarch64.rpm", false},
		{MethodDeb, "386", "", true},
		{MethodRPM, "riscv64", "", true},
		{MethodAUR, "amd64", "", true},
	}
	for _, c := range cases {
		got, err := PackageName(c.m, "v1.32.0", c.arch)
		if got != c.want || (err != nil) != c.err {
			t.Errorf("PackageName(%s,%s) = %q,%v want %q err=%v", c.m, c.arch, got, err, c.want, c.err)
		}
	}
}

func TestPackageInstallCommandPerMethod(t *testing.T) {
	if got := PackageInstallCommand(MethodDeb, "/c/fss_1_amd64.deb"); got != "sudo apt install /c/fss_1_amd64.deb" {
		t.Errorf("deb advice = %q", got)
	}
	if got := PackageInstallCommand(MethodRPM, "/c/fss-1.x86_64.rpm"); got != "sudo dnf install /c/fss-1.x86_64.rpm" {
		t.Errorf("rpm advice = %q", got)
	}
}

func packageRelease(t *testing.T, name string, body []byte, sums string, sig []byte) []Asset {
	t.Helper()
	if sums == "" {
		d := sha256.Sum256(body)
		sums = fmt.Sprintf("%s  %s\n", hex.EncodeToString(d[:]), name)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch strings.TrimPrefix(r.URL.Path, "/") {
		case name:
			_, _ = w.Write(body)
		case sumsAsset:
			_, _ = w.Write([]byte(sums))
		case sigAsset:
			_, _ = w.Write(sig)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	assets := []Asset{{Name: name, URL: srv.URL + "/" + name}, {Name: sumsAsset, URL: srv.URL + "/" + sumsAsset}}
	if sig != nil {
		assets = append(assets, Asset{Name: sigAsset, URL: srv.URL + "/" + sigAsset})
	}
	return assets
}

func TestFetchPackageSavesVerifiedFile(t *testing.T) {
	name := "fss_1.32.0_amd64.deb"
	assets := packageRelease(t, name, []byte("deb-bytes"), "", nil)
	dir := t.TempDir()
	path, err := FetchPackage(context.Background(), MethodDeb, Options{Tag: "v1.32.0", Assets: assets, GOARCH: "amd64", CacheDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(dir, name) {
		t.Errorf("path = %s", path)
	}
	if b, _ := os.ReadFile(path); string(b) != "deb-bytes" {
		t.Errorf("content = %q", b)
	}
}

func TestFetchPackageRefusesBadChecksum(t *testing.T) {
	name := "fss-1.32.0-1.x86_64.rpm"
	assets := packageRelease(t, name, []byte("rpm"), strings.Repeat("0", 64)+"  "+name+"\n", nil)
	dir := t.TempDir()
	_, err := FetchPackage(context.Background(), MethodRPM, Options{Tag: "v1.32.0", Assets: assets, GOARCH: "amd64", CacheDir: dir})
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("err = %v", err)
	}
	if left, _ := os.ReadDir(dir); len(left) != 0 {
		t.Errorf("cache not empty: %v", left)
	}
}

func TestCheckSignature(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	key := base64.StdEncoding.EncodeToString(pub)
	sums := []byte("abc  fss.tar.gz\n")
	good := ed25519.Sign(priv, sums)
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	bad := ed25519.Sign(other, sums)

	cases := []struct {
		name    string
		key     string
		require bool
		sig     []byte
		wantErr string
		wantOut string
	}{
		{"no key skips with notice", "", false, nil, "", "[notice] release signature verification is not enabled"},
		{"valid raw", key, true, good, "", "verified release signature"},
		{"valid base64", key, true, []byte(base64.StdEncoding.EncodeToString(good)), "", "verified release signature"},
		{"invalid refuses", key, false, bad, "does not verify", ""},
		{"missing warns", key, false, nil, "", "will be refused in a future version"},
		{"missing refuses when required", key, true, nil, "unsigned", ""},
		{"garbage key", "nope", false, good, "not a base64 Ed25519 key", ""},
	}
	for _, c := range cases {
		var out bytes.Buffer
		err := checkSignature(&out, c.key, c.require, sums, c.sig)
		if c.wantErr == "" && err != nil {
			t.Errorf("%s: %v", c.name, err)
		}
		if c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)) {
			t.Errorf("%s: err = %v want %q", c.name, err, c.wantErr)
		}
		if !strings.Contains(out.String(), c.wantOut) {
			t.Errorf("%s: output %q lacks %q", c.name, out.String(), c.wantOut)
		}
	}
}

func TestApplyVerifiesSignatureOverSums(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	key := base64.StdEncoding.EncodeToString(pub)
	name := ArchiveName("fss", "v1.32.0", "linux", "amd64")
	archive := tarGz(t, "fss", newBinary)
	d := sha256.Sum256(archive)
	sums := fmt.Sprintf("%s  %s\n", hex.EncodeToString(d[:]), name)

	for _, tc := range []struct {
		name string
		sig  []byte
		ok   bool
	}{
		{"signed", ed25519.Sign(priv, []byte(sums)), true},
		{"tampered sums", ed25519.Sign(priv, []byte("other")), false},
	} {
		assets := packageRelease(t, name, archive, sums, tc.sig)
		path := target(t)
		opts := options(path, assets, nil)
		opts.PublicKey = &key
		err := Apply(context.Background(), opts)
		if (err == nil) != tc.ok {
			t.Errorf("%s: err = %v", tc.name, err)
		}
		if !tc.ok {
			assertUntouched(t, path)
		}
	}
}

func TestVerifyRunsRequiresTheNewTag(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script")
	}
	path := filepath.Join(t.TempDir(), "fss")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho fss version v1.31.0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := verifyRuns(context.Background(), path, "v1.32.0"); err == nil {
		t.Error("a binary reporting the old tag was accepted")
	}
	if err := verifyRuns(context.Background(), path, "v1.31.0"); err != nil {
		t.Errorf("matching tag rejected: %v", err)
	}
}

func TestApplyPreservesMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no modes")
	}
	name := ArchiveName("fss", "v1.32.0", "linux", "amd64")
	srv, assets := releaseServer(t, name, tarGz(t, "fss", newBinary), "")
	path := target(t)
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := Apply(context.Background(), options(path, assets, srv.Client())); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o700 {
		t.Errorf("mode = %v, want 0700", info.Mode().Perm())
	}
}

func TestSweepRemovesOnlyStaleTemps(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "fss")
	stale := filepath.Join(dir, ".fss-update-111")
	fresh := filepath.Join(dir, ".fss-update-222")
	for _, p := range []string{stale, fresh} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	sweepLeftovers(bin)
	if _, err := os.Stat(stale); err == nil {
		t.Error("stale temp survived")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Error("fresh temp removed")
	}
}

func TestDetectDebRpmAdviceMentionsInstall(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip()
	}
	if a := Detect(Env{Path: "/usr/bin/fss", Channel: "deb"}).Advice; !strings.Contains(a, "sudo apt install") || strings.Contains(a, "--only-upgrade") {
		t.Errorf("deb advice = %q", a)
	}
	if a := Detect(Env{Path: "/usr/bin/fss", Channel: "rpm"}).Advice; !strings.Contains(a, "sudo dnf install") || strings.Contains(a, "upgrade fss") {
		t.Errorf("rpm advice = %q", a)
	}
}

func TestCompareVersionsGitDescribeIsAfterTheTag(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v1.34.0-3-gabc123", "v1.34.0", 1},
		{"v1.34.0-dirty", "v1.34.0", 1},
		{"v1.34.0-3-gabc123-dirty", "v1.34.0", 1},
		{"v1.34.0-3-gabc123", "v1.34.0-rc1", 1},
		{"v1.34.0-3-gabc123", "v1.35.0", -1},
	}
	for _, c := range cases {
		if got, ok := CompareVersions(c.a, c.b); !ok || got != c.want {
			t.Errorf("CompareVersions(%q,%q) = %d,%v want %d", c.a, c.b, got, ok, c.want)
		}
	}
}

func TestValidTagAndPackageNameRejectOddTags(t *testing.T) {
	for _, tag := range []string{"v1.2.3", "1.2.3-rc.1"} {
		if !ValidTag(tag) {
			t.Errorf("%q rejected", tag)
		}
	}
	for _, tag := range []string{"", "latest", "v1.2.3/../x", "v1.2.3;rm", "../v1.2.3", "v1.2"} {
		if ValidTag(tag) {
			t.Errorf("%q accepted", tag)
		}
		if _, err := PackageName(MethodDeb, tag, "amd64"); err == nil {
			t.Errorf("PackageName accepted %q", tag)
		}
	}
}

func TestPackageInstallCommandQuotesThePath(t *testing.T) {
	got := PackageInstallCommand(MethodDeb, "/home/a b/it's/fss_1_amd64.deb")
	if got != `sudo apt install '/home/a b/it'\''s/fss_1_amd64.deb'` {
		t.Errorf("got %s", got)
	}
}

func TestMentionsTagIsTokenBased(t *testing.T) {
	cases := []struct {
		out  string
		want bool
	}{
		{"fss version v1.32.0 (abc, 2026, release)", true},
		{"fss v1.32.0\n", true},
		{"fss version v1.32.0-rc1 (abc)", false},
		{"fss version v11.32.0", false},
		{"fss version v1.32.01", false},
	}
	for _, c := range cases {
		if got := mentionsTag(c.out, "v1.32.0"); got != c.want {
			t.Errorf("mentionsTag(%q) = %v", c.out, got)
		}
	}
}

func TestFetchPackagePrunesCache(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "fss_1.31.0_amd64.deb")
	staleDL := filepath.Join(dir, ".dl-1")
	other := filepath.Join(dir, "notes.txt")
	for _, p := range []string{old, staleDL, other} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(staleDL, past, past); err != nil {
		t.Fatal(err)
	}
	name := "fss_1.32.0_amd64.deb"
	assets := packageRelease(t, name, []byte("d"), "", nil)
	if _, err := FetchPackage(context.Background(), MethodDeb, Options{Tag: "v1.32.0", Assets: assets, GOARCH: "amd64", CacheDir: dir}); err != nil {
		t.Fatal(err)
	}
	for p, want := range map[string]bool{old: false, staleDL: false, other: true, filepath.Join(dir, name): true} {
		if _, err := os.Stat(p); (err == nil) != want {
			t.Errorf("%s exists=%v want %v", filepath.Base(p), err == nil, want)
		}
	}
}
