# Potential Enhancements

Ideas and known gaps that are not built yet. Once something ships it moves to
the reference docs and comes out of this file — see the index in the
[README](../README.md#documentation).


## Packagecloud / Cloudsmith — APT and DNF Auto-Updates

The release workflow builds `.deb` and `.rpm` packages with [nfpm](https://nfpm.goreleaser.com/) and attaches them to each GitHub Release, but they're manual one-off installs. For `apt upgrade` / `dnf upgrade` to pick up new versions automatically, the packages need to be hosted in a proper repository.

**Options**: [Packagecloud](https://packagecloud.io/) (free for open-source), [Cloudsmith](https://cloudsmith.com/) (free tier), or [Gemfury](https://gemfury.com/). All provide APT and YUM/DNF repos with a stable URL users add once.

**Scope**: Add a post-release GitHub Actions step that pushes `.deb`/`.rpm` artifacts to the hosted repo. It belongs in the shared release workflow (`Anastylosis/.github`), next to the AUR and Homebrew jobs, plus a one-time account setup.

## `fss identify` — Future Improvements

`fss identify` is implemented — see [identify.md](identify.md) for full documentation. Potential future additions:

- **`--nfo-dir`**: Write `.nfo` files to a `.nfo/` subdirectory instead of next to the video, keeping the video folder clean. The Stash NFO scraper supports both locations.

