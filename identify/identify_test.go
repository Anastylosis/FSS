package identify

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Anastylosis/FSS/match"
	"github.com/Anastylosis/FSS/models"
)

func scene(id, siteID, title string) models.Scene {
	return models.Scene{
		ID:     id,
		SiteID: siteID,
		Title:  title,
		URL:    "https://example.com/" + id,
		Studio: "Test Studio",
	}
}

func TestFindVideos(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"scene1.mp4", "scene2.mkv", "readme.txt", "thumb.jpg"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	sub := filepath.Join(dir, "subdir")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "nested.avi"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	videos, err := FindVideos(dir)
	if err != nil {
		t.Fatalf("FindVideos: %v", err)
	}
	if len(videos) != 3 {
		t.Errorf("found %d videos, want 3: %v", len(videos), videos)
	}
}

func TestFindVideosExtensions(t *testing.T) {
	dir := t.TempDir()
	exts := []string{".mp4", ".mkv", ".avi", ".wmv", ".mov", ".flv", ".webm", ".m4v", ".mpg", ".mpeg", ".ts"}
	for _, ext := range exts {
		if err := os.WriteFile(filepath.Join(dir, "test"+ext), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "test.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	videos, err := FindVideos(dir)
	if err != nil {
		t.Fatalf("FindVideos: %v", err)
	}
	if len(videos) != len(exts) {
		t.Errorf("found %d videos, want %d", len(videos), len(exts))
	}
}

func TestRunDryRun(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Fostering the Bully.mp4"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	idx := match.BuildIndex([]models.Scene{
		scene("1", "manyvids", "Fostering the Bully"),
	})

	videos := []string{filepath.Join(dir, "Fostering the Bully.mp4")}
	results := Run(videos, idx, Options{Apply: false})

	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	r := results[0]
	if r.Scene == nil {
		t.Fatal("expected a matched scene")
	}
	if r.Scene.Title != "Fostering the Bully" {
		t.Errorf("title = %q, want %q", r.Scene.Title, "Fostering the Bully")
	}

	nfoPath := filepath.Join(dir, "Fostering the Bully.nfo")
	if _, err := os.Stat(nfoPath); err == nil {
		t.Error("NFO file should not exist in dry-run mode")
	}
}

func TestRunApply(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Fostering the Bully.mp4"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	idx := match.BuildIndex([]models.Scene{
		scene("1", "manyvids", "Fostering the Bully"),
	})

	videos := []string{filepath.Join(dir, "Fostering the Bully.mp4")}
	results := Run(videos, idx, Options{Apply: true})

	if len(results) != 1 || results[0].Scene == nil {
		t.Fatal("expected 1 matched result")
	}

	nfoPath := filepath.Join(dir, "Fostering the Bully.nfo")
	data, err := os.ReadFile(nfoPath)
	if err != nil {
		t.Fatalf("NFO file not written: %v", err)
	}
	s := string(data)
	if !strings.Contains(s, "<title>Fostering the Bully</title>") {
		t.Errorf("NFO missing title: %s", s)
	}
	if !strings.Contains(s, `<?xml version="1.0" encoding="UTF-8"?>`) {
		t.Errorf("NFO missing XML declaration")
	}
	if !strings.Contains(s, "<movie>") {
		t.Errorf("NFO missing <movie> root")
	}
}

func TestRunSkipsExistingNFO(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "scene.mp4"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "scene.nfo"), []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}

	idx := match.BuildIndex([]models.Scene{
		scene("1", "site", "scene"),
	})

	results := Run([]string{filepath.Join(dir, "scene.mp4")}, idx, Options{Apply: true})
	if len(results) != 1 || !results[0].Skipped {
		t.Errorf("expected skipped result, got %+v", results)
	}
	if results[0].SkipReason != "nfo exists" {
		t.Errorf("skip reason = %q, want %q", results[0].SkipReason, "nfo exists")
	}

	data, _ := os.ReadFile(filepath.Join(dir, "scene.nfo"))
	if string(data) != "existing" {
		t.Error("existing NFO was overwritten without --force")
	}
}

func TestRunForceOverwritesNFO(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "scene.mp4"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "scene.nfo"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	idx := match.BuildIndex([]models.Scene{
		scene("1", "site", "scene"),
	})

	results := Run([]string{filepath.Join(dir, "scene.mp4")}, idx, Options{Apply: true, Force: true})
	if len(results) != 1 || results[0].Skipped {
		t.Errorf("expected non-skipped result, got %+v", results)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "scene.nfo"))
	if string(data) == "old" {
		t.Error("NFO was not overwritten with --force")
	}
}

func TestRunNoMatch(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "unknown.mp4"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	idx := match.BuildIndex([]models.Scene{
		scene("1", "site", "Completely Different Title"),
	})

	results := Run([]string{filepath.Join(dir, "unknown.mp4")}, idx, Options{Apply: true})
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if results[0].Confidence != match.MatchNone {
		t.Errorf("confidence = %v, want NONE", results[0].Confidence)
	}
}

func TestSummarize(t *testing.T) {
	results := []Result{
		{Confidence: match.MatchExact, Scene: &match.MergedScene{}},
		{Confidence: match.MatchSubstring, Scene: &match.MergedScene{}},
		{Confidence: match.MatchNone},
		{Confidence: match.MatchAmbiguous},
		{Skipped: true, SkipReason: "nfo exists"},
	}

	s := Summarize(results)
	if s.Total != 5 {
		t.Errorf("total = %d, want 5", s.Total)
	}
	if s.Matched != 2 {
		t.Errorf("matched = %d, want 2", s.Matched)
	}
	if s.Unmatched != 1 {
		t.Errorf("unmatched = %d, want 1", s.Unmatched)
	}
	if s.Ambiguous != 1 {
		t.Errorf("ambiguous = %d, want 1", s.Ambiguous)
	}
	if s.Skipped != 1 {
		t.Errorf("skipped = %d, want 1", s.Skipped)
	}
}

func TestWriteReport(t *testing.T) {
	dir := t.TempDir()
	results := []Result{
		{VideoPath: filepath.Join(dir, "good-scene.mp4"), Confidence: match.MatchExact, Scene: &match.MergedScene{}},
		{VideoPath: filepath.Join(dir, "no-match.mp4"), Confidence: match.MatchNone},
		{VideoPath: filepath.Join(dir, "has-nfo.mp4"), Skipped: true, SkipReason: "nfo exists"},
	}

	if err := WriteReport(dir, results); err != nil {
		t.Fatalf("WriteReport: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "fss-report.txt"))
	if err != nil {
		t.Fatalf("report not written: %v", err)
	}
	s := string(data)
	if !strings.Contains(s, "no-match.mp4") {
		t.Error("report missing unmatched file")
	}
	if !strings.Contains(s, "has-nfo.mp4") {
		t.Error("report missing skipped file")
	}
	if strings.Contains(s, "good-scene.mp4") {
		t.Error("report should not contain matched files")
	}
}

func TestWriteReportSkipsWhenAllMatched(t *testing.T) {
	dir := t.TempDir()
	results := []Result{
		{VideoPath: filepath.Join(dir, "matched.mp4"), Confidence: match.MatchExact, Scene: &match.MergedScene{}},
	}

	if err := WriteReport(dir, results); err != nil {
		t.Fatalf("WriteReport: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "fss-report.txt")); err == nil {
		t.Error("report should not be written when all files matched")
	}
}

// --poster is what makes the NFO thumbnail trustworthy: the file is downloaded
// and the NFO points at it, so the reference is present by construction.
func TestRunPosterDownloadsAndReferencesLocalFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("\xff\xd8\xffjpegdata"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	video := filepath.Join(dir, "Fostering the Bully.mp4")
	if err := os.WriteFile(video, []byte("video"), 0o600); err != nil {
		t.Fatal(err)
	}

	idx := match.BuildIndex([]models.Scene{{
		ID: "1", SiteID: "test", Title: "Fostering the Bully",
		URL: "https://example.com/1", Thumbnail: srv.URL + "/cover.jpg",
	}})

	results := RunContext(context.Background(), []string{video}, idx, Options{
		Apply: true, Poster: true, PosterAllowPrivate: true,
	})
	if len(results) != 1 {
		t.Fatalf("got %d results", len(results))
	}
	if results[0].PosterError != nil {
		t.Fatalf("PosterError = %v", results[0].PosterError)
	}

	posterPath := filepath.Join(dir, "Fostering the Bully-poster.jpg")
	data, err := os.ReadFile(posterPath)
	if err != nil {
		t.Fatalf("poster not written: %v", err)
	}
	if string(data) != "\xff\xd8\xffjpegdata" {
		t.Errorf("poster content = %q", data)
	}

	nfoData, err := os.ReadFile(filepath.Join(dir, "Fostering the Bully.nfo"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(nfoData), "Fostering the Bully-poster.jpg") {
		t.Errorf("NFO does not reference the local poster:\n%s", nfoData)
	}
	if strings.Contains(string(nfoData), srv.URL) {
		t.Errorf("NFO still contains the remote URL:\n%s", nfoData)
	}
}

// A dead thumbnail must not fail the identification, and must not leave a
// <thumb> pointing at something that isn't there.
func TestRunPosterFailureStillWritesNFO(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusGone)
	}))
	defer srv.Close()

	dir := t.TempDir()
	video := filepath.Join(dir, "Scene One.mp4")
	if err := os.WriteFile(video, []byte("video"), 0o600); err != nil {
		t.Fatal(err)
	}

	idx := match.BuildIndex([]models.Scene{{
		ID: "1", SiteID: "test", Title: "Scene One",
		URL: "https://example.com/1", Thumbnail: srv.URL + "/gone.jpg",
	}})

	results := RunContext(context.Background(), []string{video}, idx, Options{
		Apply: true, Poster: true, PosterAllowPrivate: true,
	})
	if results[0].PosterError == nil {
		t.Error("expected a PosterError for a 410 thumbnail")
	}
	if results[0].Skipped {
		t.Error("a missing poster must not skip the scene")
	}

	nfoData, err := os.ReadFile(filepath.Join(dir, "Scene One.nfo"))
	if err != nil {
		t.Fatalf("NFO not written: %v", err)
	}
	if !strings.Contains(string(nfoData), "<title>Scene One</title>") {
		t.Errorf("NFO missing metadata:\n%s", nfoData)
	}
	if strings.Contains(string(nfoData), srv.URL+"/gone.jpg") {
		t.Errorf("NFO references a thumbnail that failed to download:\n%s", nfoData)
	}
}

// Without --poster nothing is fetched and no poster file appears.
func TestRunWithoutPosterDownloadsNothing(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		_, _ = w.Write([]byte("x"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	video := filepath.Join(dir, "Scene One.mp4")
	if err := os.WriteFile(video, []byte("video"), 0o600); err != nil {
		t.Fatal(err)
	}
	idx := match.BuildIndex([]models.Scene{{
		ID: "1", SiteID: "test", Title: "Scene One",
		URL: "https://example.com/1", Thumbnail: srv.URL + "/cover.jpg",
	}})

	RunContext(context.Background(), []string{video}, idx, Options{Apply: true})

	if hits != 0 {
		t.Errorf("made %d requests without --poster, want 0", hits)
	}
	if entries, _ := filepath.Glob(filepath.Join(dir, "*-poster.*")); len(entries) != 0 {
		t.Errorf("poster files written without --poster: %v", entries)
	}
}

// Content types that are not images are refused rather than saved under a
// misleading extension.
func TestRunPosterRejectsNonImage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>login</html>"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	video := filepath.Join(dir, "Scene One.mp4")
	if err := os.WriteFile(video, []byte("video"), 0o600); err != nil {
		t.Fatal(err)
	}
	idx := match.BuildIndex([]models.Scene{{
		ID: "1", SiteID: "test", Title: "Scene One",
		URL: "https://example.com/1", Thumbnail: srv.URL + "/cover.jpg",
	}})

	results := RunContext(context.Background(), []string{video}, idx, Options{
		Apply: true, Poster: true, PosterAllowPrivate: true,
	})
	if results[0].PosterError == nil {
		t.Error("expected an HTML response to be refused as a poster")
	}
	if entries, _ := filepath.Glob(filepath.Join(dir, "*-poster.*")); len(entries) != 0 {
		t.Errorf("wrote a poster from a non-image response: %v", entries)
	}
}
