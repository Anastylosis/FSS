// Package identify scans a directory of video files, matches each filename
// against previously scraped FSS scene metadata, and writes Kodi-style .nfo
// sidecar files for the matches. It is the engine behind `fss identify`.
package identify

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/internal/mediafetch"
	"github.com/Anastylosis/FSS/match"
	"github.com/Anastylosis/FSS/nfo"
)

var videoExtensions = map[string]bool{
	".mp4":  true,
	".mkv":  true,
	".avi":  true,
	".wmv":  true,
	".mov":  true,
	".flv":  true,
	".webm": true,
	".m4v":  true,
	".mpg":  true,
	".mpeg": true,
	".ts":   true,
}

// Result holds the outcome of matching a single video file against the scene index.
type Result struct {
	VideoPath  string
	NFOPath    string
	Confidence match.MatchConfidence
	Scene      *match.MergedScene
	Skipped    bool
	SkipReason string

	// PosterPath is the downloaded poster file, set only with Options.Poster.
	PosterPath string
	// PosterError records why a poster could not be saved. The NFO is still
	// written — a missing poster does not fail the identification.
	PosterError error
}

// Options controls the identify run behaviour.
type Options struct {
	Apply    bool
	Force    bool
	NoReport bool

	// Poster downloads each matched scene's thumbnail to a `-poster` file
	// beside the video and points the NFO at it. Off by default: it is a
	// network fetch per scene against the same hosts a scrape rate-limits.
	//
	// Without it, a scene's thumbnail stays a remote URL — and scraped CDN
	// URLs are frequently signed and short-lived, so the link is often dead
	// by the time a media manager follows it.
	Poster bool

	// PosterAllowPrivate permits poster URLs resolving to private or loopback
	// addresses, for locally-hosted media. See mediafetch.ValidateURL.
	PosterAllowPrivate bool
}

// FindVideos recursively lists all video files under dir.
func FindVideos(dir string) ([]string, error) {
	var videos []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if videoExtensions[ext] {
			videos = append(videos, path)
		}
		return nil
	})
	return videos, err
}

// Run matches each video against the scene index and optionally writes NFO
// sidecar files. It is RunContext with a background context.
func Run(videos []string, idx *match.SceneIndex, opts Options) []Result {
	return RunContext(context.Background(), videos, idx, opts)
}

// RunContext is Run with cancellation, which Options.Poster needs since it
// performs network fetches.
func RunContext(ctx context.Context, videos []string, idx *match.SceneIndex, opts Options) []Result {
	var results []Result
	client := httpx.NewClient(30 * time.Second)

	claimed := map[string]string{}
	for _, vpath := range videos {
		if ctx.Err() != nil {
			break
		}
		basename := filepath.Base(vpath)
		nfoPath := nfoPathFor(vpath)

		if first, dup := claimed[nfoPath]; dup {
			results = append(results, Result{
				VideoPath:  vpath,
				NFOPath:    nfoPath,
				Confidence: match.MatchNone,
				Skipped:    true,
				SkipReason: fmt.Sprintf("same NFO name as %s", filepath.Base(first)),
			})
			continue
		}
		claimed[nfoPath] = vpath

		if !opts.Force {
			if _, err := os.Stat(nfoPath); err == nil {
				results = append(results, Result{
					VideoPath:  vpath,
					NFOPath:    nfoPath,
					Confidence: match.MatchNone,
					Skipped:    true,
					SkipReason: "nfo exists",
				})
				continue
			}
		}

		mr := idx.Match(basename, probeDuration(vpath))
		if mr.Confidence == match.MatchNone || mr.Confidence == match.MatchAmbiguous {
			results = append(results, Result{
				VideoPath:  vpath,
				NFOPath:    nfoPath,
				Confidence: mr.Confidence,
			})
			continue
		}

		merged := match.MergeScenes(mr.Scenes, time.Time{})
		r := Result{
			VideoPath:  vpath,
			NFOPath:    nfoPath,
			Confidence: mr.Confidence,
			Scene:      &merged,
		}

		if opts.Apply {
			posterRef := ""
			if opts.Poster && merged.Thumbnail != "" {
				ref, err := savePoster(ctx, client, vpath, merged.Thumbnail, opts.PosterAllowPrivate)
				if err != nil {
					r.PosterError = err
				} else {
					posterRef = ref
					r.PosterPath = filepath.Join(filepath.Dir(vpath), ref)
				}
			}
			if err := writeNFO(nfoPath, merged, posterRef, opts.Poster); err != nil {
				r.Skipped = true
				r.SkipReason = fmt.Sprintf("write error: %v", err)
			}
		}

		results = append(results, r)
	}

	return results
}

// Stats aggregates identify results into counts.
type Stats struct {
	Total     int
	Matched   int
	Unmatched int
	Ambiguous int
	Skipped   int
}

// Summarize tallies results into matched/unmatched/ambiguous/skipped counts.
func Summarize(results []Result) Stats {
	var s Stats
	s.Total = len(results)
	for _, r := range results {
		switch {
		case r.Skipped:
			s.Skipped++
		case r.Confidence == match.MatchNone:
			s.Unmatched++
		case r.Confidence == match.MatchAmbiguous:
			s.Ambiguous++
		default:
			s.Matched++
		}
	}
	return s
}

// WriteReport writes an fss-report.txt listing unmatched and skipped files.
func WriteReport(dir string, results []Result) error {
	var sb strings.Builder
	sb.WriteString("# FSS Identify Report\n")
	fmt.Fprintf(&sb, "# Generated: %s\n\n", time.Now().UTC().Format(time.RFC3339))

	var unmatched, skipped []string
	for _, r := range results {
		rel, _ := filepath.Rel(dir, r.VideoPath)
		if rel == "" {
			rel = r.VideoPath
		}
		if r.Skipped {
			skipped = append(skipped, fmt.Sprintf("%s (%s)", rel, r.SkipReason))
		} else if r.Confidence == match.MatchNone || r.Confidence == match.MatchAmbiguous {
			unmatched = append(unmatched, rel)
		}
	}

	if len(unmatched) > 0 {
		fmt.Fprintf(&sb, "## Unmatched (%d)\n", len(unmatched))
		for _, f := range unmatched {
			sb.WriteString(f + "\n")
		}
		sb.WriteString("\n")
	}

	if len(skipped) > 0 {
		fmt.Fprintf(&sb, "## Skipped (%d)\n", len(skipped))
		for _, f := range skipped {
			sb.WriteString(f + "\n")
		}
		sb.WriteString("\n")
	}

	if len(unmatched) == 0 && len(skipped) == 0 {
		return nil
	}

	return os.WriteFile(filepath.Join(dir, "fss-report.txt"), []byte(sb.String()), 0o600)
}

func nfoPathFor(videoPath string) string {
	ext := filepath.Ext(videoPath)
	return videoPath[:len(videoPath)-len(ext)] + ".nfo"
}

// writeNFO renders the NFO, resolving what `<thumb>` should point at:
//
//   - posterRef set — a downloaded file, present by definition;
//   - posterWanted but no ref — the download was tried and failed, which
//     proves the remote URL is dead, so no thumb at all;
//   - otherwise — whatever FromMergedScene decides, which keeps the remote
//     URL unless its signed expiry has already passed.
func writeNFO(path string, m match.MergedScene, posterRef string, posterWanted bool) error {
	mov := nfo.FromMergedScene(m)
	switch {
	case posterRef != "":
		mov.Thumbnails = []nfo.Thumb{{Aspect: "poster", URL: posterRef}}
	case posterWanted:
		mov.Thumbnails = nil
	}
	data, err := nfo.Marshal(mov)
	if err != nil {
		return fmt.Errorf("marshalling NFO: %w", err)
	}
	return os.WriteFile(path, data, 0o600)
}

// posterExtensions maps the content types worth saving to a file extension.
// Anything else is refused rather than written under a misleading name.
var posterExtensions = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/webp": ".webp",
	"image/avif": ".avif",
	"image/gif":  ".gif",
}

// savePoster downloads thumbURL next to videoPath as `<basename>-poster.<ext>`,
// the local-artwork convention Kodi, Jellyfin and Emby all recognise. It
// returns the bare filename, which is what goes in the NFO.
func savePoster(ctx context.Context, client *http.Client, videoPath, thumbURL string, allowPrivate bool) (string, error) {
	if mediafetch.Expired(thumbURL, time.Now()) {
		return "", fmt.Errorf("thumbnail URL signature expired — re-scrape the studio for a fresh URL")
	}
	asset, err := mediafetch.Fetch(ctx, client, thumbURL, allowPrivate)
	if err != nil {
		return "", err
	}

	mediaType := asset.ContentType
	if i := strings.IndexByte(mediaType, ';'); i >= 0 {
		mediaType = mediaType[:i]
	}
	ext, ok := posterExtensions[strings.ToLower(strings.TrimSpace(mediaType))]
	if !ok {
		return "", fmt.Errorf("unexpected content type %q — not writing a poster", asset.ContentType)
	}

	base := filepath.Base(videoPath)
	base = base[:len(base)-len(filepath.Ext(base))]
	name := base + "-poster" + ext
	if err := os.WriteFile(filepath.Join(filepath.Dir(videoPath), name), asset.Data, 0o600); err != nil {
		return "", fmt.Errorf("writing poster: %w", err)
	}
	return name, nil
}

// probeDuration returns the file's duration in seconds via ffprobe, or 0 if
// ffprobe is not installed or fails. Best-effort: matching still works without
// duration, it just can't disambiguate same-title scenes.
func probeDuration(path string) float64 {
	out, err := exec.Command("ffprobe",
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		"--", path,
	).Output()
	if err != nil {
		return 0
	}
	dur, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil {
		return 0
	}
	return dur
}
