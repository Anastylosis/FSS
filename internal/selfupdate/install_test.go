package selfupdate

import (
	"runtime"
	"testing"
)

func TestDetect(t *testing.T) {
	cases := []struct {
		name        string
		env         Env
		want        Method
		wantManaged bool
	}{
		{"docker stamp", Env{Path: "/usr/local/bin/fss", Channel: "docker", Version: "v1.31.0"}, MethodDocker, true},
		{"inside a container", Env{Path: "/usr/local/bin/fss", Channel: "release", Version: "v1.31.0", InDocker: true}, MethodDocker, true},
		{"homebrew cellar", Env{Path: "/opt/homebrew/Cellar/fss/1.31.0/bin/fss", Channel: "release", Version: "v1.31.0"}, MethodHomebrew, true},
		{"linuxbrew cellar", Env{Path: "/home/linuxbrew/.linuxbrew/Cellar/fss/1.31.0/bin/fss", Channel: "", Version: "v1.31.0"}, MethodHomebrew, true},
		{"release tarball", Env{Path: "/usr/local/bin/fss", Channel: "release", Version: "v1.31.0"}, MethodRelease, false},
		{"unstamped build", Env{Path: "/home/u/bin/fss", Channel: "", Version: "v1.30.0"}, MethodUnknown, false},
		{"go install", Env{Path: "/home/u/go/bin/fss", Channel: "", Version: "dev"}, MethodSource, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Detect(c.env)
			if got.Method != c.want {
				t.Errorf("Method = %q, want %q", got.Method, c.want)
			}
			if got.Managed() != c.wantManaged {
				t.Errorf("Managed() = %v, want %v (advice %q)", got.Managed(), c.wantManaged, got.Advice)
			}
		})
	}
}

// The stamp says how the binary was shipped; the path says whether this copy is
// still the one the package owns. A .deb binary copied into ~/bin is nobody
// else's business and may update itself.
func TestDetectPackagedOnlyWhereThePackageManagerPutIt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no Linux package paths on Windows")
	}
	for _, channel := range []string{"deb", "rpm", "aur"} {
		managed := Detect(Env{Path: "/usr/bin/fss", Channel: channel, Version: "v1.31.0"})
		if !managed.Managed() {
			t.Errorf("%s at /usr/bin must defer to the package manager", channel)
		}
		if managed.Advice == "" {
			t.Errorf("%s: refusal must name the command to run instead", channel)
		}

		copied := Detect(Env{Path: "/home/u/bin/fss", Channel: channel, Version: "v1.31.0"})
		if copied.Managed() {
			t.Errorf("%s copied out of /usr/bin must be updatable, got advice %q", channel, copied.Advice)
		}
	}
}

// /usr/local/bin is where a hand-extracted tarball lands, and no package owns
// it — treating it as packaged would refuse exactly the case self-update exists
// for.
func TestDetectUsrLocalBinIsNotPackageOwned(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no Linux package paths on Windows")
	}
	if got := Detect(Env{Path: "/usr/local/bin/fss", Channel: "deb", Version: "v1.31.0"}); got.Managed() {
		t.Errorf("/usr/local/bin must be updatable, got advice %q", got.Advice)
	}
}

func TestArchiveName(t *testing.T) {
	cases := []struct{ goos, goarch, want string }{
		{"linux", "amd64", "fss-v1.31.0-linux-amd64.tar.gz"},
		{"darwin", "arm64", "fss-v1.31.0-darwin-arm64.tar.gz"},
		{"windows", "amd64", "fss-v1.31.0-windows-amd64.zip"},
	}
	for _, c := range cases {
		if got := ArchiveName("fss", "v1.31.0", c.goos, c.goarch); got != c.want {
			t.Errorf("ArchiveName(%s/%s) = %q, want %q", c.goos, c.goarch, got, c.want)
		}
	}
}

func TestSumFor(t *testing.T) {
	list := "aaa  fss-v1.31.0-darwin-arm64.tar.gz\nBBB *dist/fss-v1.31.0-linux-amd64.tar.gz\n\nmalformed\n"
	got, err := sumFor(list, "fss-v1.31.0-linux-amd64.tar.gz")
	if err != nil {
		t.Fatalf("sumFor: %v", err)
	}
	if got != "bbb" {
		t.Errorf("sum = %q, want %q", got, "bbb")
	}
	if _, err := sumFor(list, "fss-v1.31.0-windows-amd64.zip"); err == nil {
		t.Error("a file with no entry must be an error, not an empty hash")
	}
}
