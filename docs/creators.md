# Creators

One person often sells the same catalogue on several storefronts. Jane Eyre
has a Clips4Sale studio, a ManyVids profile, a LoyalFans page and her own site;
Irene Adler has six. FSS keys everything on the studio URL, so out of the box
those are six unrelated studios that happen to share a name — and often do not
even do that (`EmmaWoodhouse`, `Emma Woodhouse`, `Emma Woodhouse Films`).

A **creator** binds those storefronts together. It is what makes

```bash
fss scrape --creator "Jane Eyre"      # all four stores, one command
fss compare --from-creator "Becky Sharp"    # where is each clip cheapest?
```

possible, and it is the unit a cron job should work in.

---

## Where creators live

One YAML file per creator, in a `creators.d` directory:

```
~/.config/fss/creators.d/
├── jane-eyre.yaml
├── becky-sharp.yaml
└── irene-adler.yaml
```

Resolution order: `--creators-dir` → `creators_dir:` in `config.yaml` → the
default beside your config (`$XDG_CONFIG_HOME/fss/creators.d`). A missing
directory is not an error — creators are opt-in, and every command works
without them.

**Why a directory and not a block in `config.yaml`.** These files carry no
credentials, so a set of them can live in a git repository and be shared or
pulled; and because each creator is its own file, two people adding creators
never touch the same lines. Point `creators_dir:` at a clone to use someone
else's set, or symlink individual files out of one into your own directory.

The file name is cosmetic — the `name:` field is authoritative, so renaming a
file changes nothing. Non-YAML files are skipped, so a shared repository can
carry a `README.md`, a `LICENSE` and a `.git/` without special handling.

---

## File format

```yaml
name: Jane Eyre
aliases:
  - JaneEyreVIP          # optional: other spellings --creator should match
stores:
  - url: https://clipmarket.example/studio/4021/jane-eyre
  - url: https://janeeyre.example
    delay: 2000           # optional: ms between requests, this store only
    language: de          # optional: content language, if the site offers a choice
  - url: https://fanhub.example/jane-eyre
    enabled: false        # optional: skip on --creator / --all-creators runs
    note: needs a session cookie
```

| Key | Required | Meaning |
|-----|----------|---------|
| `name` | yes | The creator. Must be unique across all files, after normalisation |
| `aliases` | no | Extra spellings `--creator` and `--from-creator` match, and that a scrape folds into the creator's name when a storefront credits one — see [Storefront branding in performer credits](#storefront-branding-in-performer-credits) |
| `stores` | yes | At least one storefront |
| `stores[].url` | yes | Full studio URL including the scheme |
| `stores[].delay` | no | Per-request delay in ms for this store, overriding `site_delays` and `delay` |
| `stores[].language` | no | Content language for this store, for a site that publishes in several — overrides `site_languages` and `content_language`. See [usage.md](usage.md) |
| `stores[].enabled` | no | `false` skips the store on creator-driven runs. Default `true` |
| `stores[].note` | no | Free text, shown by `fss creators` |

Loading fails — loudly, naming the file — on a missing `name` or `stores`, a URL
without a scheme, a duplicated URL within one file, a negative delay, or two
files claiming the same creator name or alias. Unknown keys only warn, so a file
written for a newer FSS still loads.

**A note on `stores:`.** Writing `urls:` instead is the obvious mistake this
format invites, and it is a hard error rather than a creator with no
storefronts.

---

## Storefront branding in performer credits

Storefronts routinely credit themselves as the performer. A shop trading as
`Emma Woodhouse Films` lists `Emma Woodhouse Films` as the performer on every scene;
another spells it `EmmaWoodhouse`. Stored as published, one person becomes three
performers, and everything that reasons across stores fragments with them —
`fss compare`, a Stash import, and `creators suggest`'s own shared-performer
signal, which is how it finds storefronts whose *names* have nothing in common.

A creator file already says everything needed to fix this: **this person, these
links.** So a scrape of a listed store rewrites branding credits to the creator's
name, and **you do not have to configure anything for it** — no aliases, no extra
keys:

```yaml
name: Emma Woodhouse
stores:
  - url: https://clipmarket.example/studio/4021/emma-woodhouse
  - url: https://fanhub.example/emmawoodhousefilms
```

```
$ fss scrape --creator emma
[2/2] https://fanhub.example/emmawoodhousefilms
  performer credits normalised to "Emma Woodhouse" (creators.d)
```

A credit is treated as branding when it matches either:

- **the scene's own `Studio` value.** A shop crediting itself fills both fields
  with the same string, and on a creator's store the shop is the creator. This is
  the overwhelming majority of cases and needs nothing written down.
- **the creator's `name` or an `aliases:` entry.** The escape hatch for the
  residue, where a store spells its own name one way in the studio field and
  another in the credit — `Jane Eyre 1` as the studio, `JaneEyre` as the
  performer. Add an alias only when you see that happen.

Matching ignores case, spacing and punctuation, so `EmmaWoodhouse` and `Emma Woodhouse`
are already the same string as far as this is concerned. A scene crediting both
the shop and the person collapses to one name rather than storing them twice.

**Co-stars are never touched.** Only two things can be rewritten: the shop's own
name, and a spelling you declared. Anything else is somebody else and is stored
exactly as published, however much it resembles the creator's name — a credit of
`Emma` or `Woodhouse Bishop` on Emma Woodhouse's own store survives untouched. This is
deliberately not `--performer`, which replaces the whole list and would drop
every co-star on a multi-performer scene.

There is no fuzzy matching. Deciding that an unfamiliar name is really this
creator needs evidence a single scene does not carry — which storefronts credit
it, and nothing else — so that judgement stays in `fss creators suggest`, which
has the whole library to reason over and writes its conclusion to the file for
you to review.

A store listed under two creators is skipped rather than assigned to whichever
file loaded first. `fss creators` warns about the duplicate separately.

**This applies to every scrape**, not only `--creator` runs — naming a store's
URL directly has to store the same names as reaching it through its creator, or
the two routes disagree about who is in a scene. `--performer` still wins where
both apply: an explicit flag beats a standing default.

It changes what future scrapes *store*; it does not rewrite scenes already saved.
To fold an existing duplicate, re-run that store with `--refresh`.

### Studios are not merged, and should not be

Only performer credits are folded. Each storefront keeps its own studio, its own
row, and whatever name it publishes — a shop trading as `Emma Woodhouse Films` still
records `Emma Woodhouse Films` as the studio on every one of its scenes.

That asymmetry is the point, because the two cases are opposites:

| | duplication is | so a creator file |
|---|---|---|
| performer | **wrong** — one person filed as several | folds it |
| studio | **correct** — these really are different shops | leaves it |

Merging the studios would destroy `fss compare`, whose entire output — how many
scenes each store carries, which titles overlap, where each one is cheapest —
exists *because* the storefronts stay distinct. A creator is a grouping layer
**over** studios, not a replacement for them; `--from-creator` is how you ask for
all of them as one set.

One consequence worth knowing: `fss stash import` pushes `Scene.Studio` into
Stash, so each storefront arrives there as its own studio. If you would rather it
did not, relabel at scrape time:

```bash
fss scrape https://fanhub.example/emmawoodhousefilms --studio "Emma Woodhouse"
```

That replaces the studio on everything the run collects. It is per-invocation and
not something a creator file can set, deliberately — storing one name for four
shops would lose the distinction the stored data is keeping for you.

---

## Bootstrapping from what you already scrape

You do not have to write these by hand. `fss creators suggest` reads your
existing scene data and proposes the files:

```bash
fss creators suggest --db              # print proposals for review
fss creators suggest --db --write      # write them into creators.d
```

Studios are grouped on two signals:

1. **Name containment** — `EmmaWoodhouse`, `Emma Woodhouse` and `Emma Woodhouse Films` share a
   normalised substring. Fragments shorter than five characters are ignored, so
   a three-letter studio name cannot absorb every longer name containing it.
2. **A shared dominant performer** — a performer credited on at least half a
   store's scenes. This is what catches storefronts whose names have nothing in
   common, and what pulls `Baker Street Studio` into the Irene Adler group.

The creator is named after the dominant performer where there is one, since a
storefront label is often a brand (`Emma Woodhouse Films`) or a handle
(`odettelang`); every other spelling becomes an alias.

Both signals are heuristics, so `suggest` prints for review by default and
`--write` never overwrites an existing file without `--force`. It also **names**
the studios that grouped with nothing else — a storefront can be unmistakably
someone's to a human and invisible to both signals. `Duchess Mina` on
IWantClips is Mina Harker's, but its name shares nothing with `Mina Harker` and it
credits no performers at all, so nothing can link it automatically. Add the URL
to the right file by hand.

`--include-single` proposes the ungrouped studios as one-store creators instead,
which is still useful for `--all-creators`.

---

## Scraping by creator

```bash
fss scrape --creator "Jane Eyre"        # one creator's stores
fss scrape --creator jane --creator becky  # several (prefixes work)
fss scrape --all-creators                 # everything defined
```

`--creator` matches the name or any alias, ignoring case, spacing and
punctuation — `"Emma Woodhouse"`, `EmmaWoodhouse` and `emma-woodhouse` are the same lookup. A
value that uniquely prefixes one creator resolves to it; one that prefixes
several is an error naming them, never a silent pick.

Stores are scraped sequentially, in file order, and a URL reachable more than one
way is scraped once — naming a URL explicitly alongside `--all-creators` does not
scrape it twice. Every other `scrape` flag still applies, so
`fss scrape --all-creators --refresh` works as expected.

### `--stale`: cron without a crontab per creator

```bash
fss scrape --all-creators --stale 7d
```

skips any store scraped within the last seven days and prints its reasoning:

```
  skip  https://clipmarket.example/studio/4021/jane-eyre  (scraped 2d ago)
  run   https://janeeyre.example                          (scraped 9d ago)
  run   https://fanhub.example/jane-eyre                  (never scraped)

2 of 44 studio(s) due.
```

Durations accept `h`, `m`, `s` as Go does, plus `d` and `w`. Staleness is read
from the SQLite `studios` table, the only place a last-scrape time is recorded,
so `--stale` requires `--db` or a configured `db:` rather than silently scraping
everything.

That makes one crontab line enough:

```cron
0 * * * *  fss scrape --all-creators --stale 7d --db >> ~/.local/state/fss.log 2>&1
```

The run exits without touching the network when nothing is due. Note that
unattended runs skip the coverage-collapse prompt rather than answering it —
pass `--force` if you want those saves to proceed (see
[usage.md](usage.md#broken-scraper-detection)), and prefer `enabled: false` on
any store that cannot be scraped without a human.

**Do not put a first-time full traversal on a cron timer.** There is no resume:
an interrupted traversal saves what it got but no page cursor, and on a
date-sorted site the next incremental run stops early at a known ID and reports
success without ever reaching the older remainder. Let the first scrape of a
large catalogue finish in one go.

---

## Filtering by creator

`--from-creator` narrows the scene set for every command that reads stored
scenes — `fss compare`, `fss identify`, `fss stash import`, `fss creators
suggest`:

```bash
fss identify --db --from-creator "Becky Sharp" /media/clips
fss stash import --db --from-creator "Jane Eyre" --apply
```

It resolves to that creator's store URLs and then behaves exactly like listing
them under `--from-studio`. Within one flag any value matches; across flags every
flag must match.

---

## Inspecting

```bash
fss creators
```

lists what is defined, with each store's last scrape date when a database is
configured:

```
13 creator(s) in /home/you/.config/fss/creators.d

Emma Woodhouse  (aka Emma Woodhouse Films)
  https://clipmarket.example/studio/6055/emma-woodhouse-films  [scraped 2026-08-16]
  https://vidvault.example/profile/380818/emma-woodhouse/store/videos  [scraped 2026-08-16]
  https://clipstore.example/store/221/EmmaWoodhouse  [scraped 2026-08-16]
```

See [compare.md](compare.md) for what to do with the grouping once it exists.
