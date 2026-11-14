# winget packaging

FSS is published to winget as the portable package `Anastylosis.FSS` (publisher and repository
owner `Anastylosis`, from the module path `github.com/Anastylosis/FSS`). The installer is the
release's `fss-<tag>-windows-amd64.zip`, installed unchanged; `fss.exe` is exposed as the `fss`
command.

The three YAML files here are the manifests for v1.34.0, with the real URL and SHA-256 taken from
that release's `SHA256SUMS`. They are a template and a reference, not something CI reads.

## One-time first submission

`.github/workflows/winget.yml` (winget-releaser) only *updates* a package that already exists in
`microsoft/winget-pkgs`, so the first version is submitted by hand:

```powershell
wingetcreate new https://github.com/Anastylosis/FSS/releases/download/v1.34.0/fss-v1.34.0-windows-amd64.zip
```

Answer the prompts with the values from these files (identifier `Anastylosis.FSS`, nested installer
`fss.exe`, alias `fss`), then submit. Alternatively copy the three files to
`manifests/a/Anastylosis/FSS/<version>/` in your fork of `microsoft/winget-pkgs`, run
`winget validate --manifest <dir>` and open the pull request yourself. Update the version and
`InstallerSha256` (from the release's `SHA256SUMS`) first if you are submitting a newer release.

Then set up the automation: fork `microsoft/winget-pkgs`, create a classic PAT with `public_repo`,
and add it as the `WINGET_TOKEN` repository secret. See `docs/install.md`.
