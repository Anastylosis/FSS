# Comparing storefronts

`fss compare` answers the questions that only appear once you know a creator's
storefronts belong together:

- What does each store actually hold, and how much of it overlaps?
- Of the clips on more than one store, where is each one cheapest?
- What can I only get from one place?

```bash
fss compare --db
fss compare --db --from-creator "Jane Eyre" --top 5 --exclusives
```

---

## Reading the report

```
Jane Eyre — 1407 scenes across 4 storefronts

  store                scenes   shared  exclusive  avg price
  clipmarket.example      658       49        609     $22.17
  janeeyre.example       343      191        152     $10.02
  vidvault.example        292      191        101     $22.66
  fanhub.example          114       67         43          —

  Cheapest source for the 227 titles carried by more than one store:
    janeeyre.example     148 titles, $2276.48 below the dearest in total
    clipmarket.example     20 titles, $109.00 below the dearest in total
    vidvault.example       12 titles, $23.63 below the dearest in total

  Widest price gaps:
    Midnight Rehearsal                            $  9.99 janeeyre.example   vs $ 49.99 vidvault.example  (-$40.00)
    The Long Way Home                             $  9.99 janeeyre.example   vs $ 49.99 vidvault.example  (-$40.00)
    Borrowed Time (Part One)                      $  9.99 janeeyre.example   vs $ 41.99 clipmarket.example  (-$32.00)

  Carried by one storefront only:
    clipmarket.example    609 titles
    janeeyre.example     152 titles
    vidvault.example      101 titles
    fanhub.example         43 titles
```

- **shared** — titles this store carries that at least one other store also
  carries.
- **exclusive** — titles no other tracked store of this creator has. It is a
  statement about *your data*, not about the world: a store you have never
  scraped cannot contribute.
- **avg price** — mean current price over the store's priced listings. A dash
  means the store exposes no prices at all (`fanhub.example`, above), not that it is
  free.
- **saves … in total** — summed across the titles where this store is strictly
  cheapest, the gap to the dearest store carrying the same title. It is an upper
  bound on what buying everywhere-else would have cost, not a rebate.

---

## How titles are matched

Within one creator, titles are grouped on a key that lowercases and drops
everything that is not a letter or digit, so `Borrowed Time (Part One)` and
`borrowed time part one!` are one title. Matching is only ever done *within* a
creator, never across, which is what keeps a generic title from collapsing two
people's catalogues.

Two consequences worth knowing:

- A store listing the same title twice contributes only its cheapest listing,
  and is not counted as sharing with itself.
- A creator who genuinely releases two different clips under one title has them
  merged. Series-numbered titles are safe; bare ones are not.

## How prices are compared

The price used is the **most recent** snapshot's effective price — sale price if
on sale, zero if free or 100% off — not `Scene.LowestPrice`. `LowestPrice` is
the lowest ever recorded, which answers "was this cheaper once" rather than
"where should I buy it".

A listing whose snapshot records no amount at all ("not free, price unknown")
counts as carrying the title but is excluded from every price comparison.
Treating it as `$0.00` would make the store with the worst data look like the
cheapest — the exact opposite of useful.

A tie has no winner. Two stores at the same price give the buyer no reason to
prefer either, so neither is credited as the cheapest source.

> Prices are only as fresh as your last scrape of each store, and a scrape only
> re-reads a scene's price when it re-fetches the scene. Incremental runs stop
> early at known IDs, so **price data is refreshed by `--refresh`/`--full`, not
> by the default mode.** A comparison across stores last scraped months apart is
> comparing history, not offers.

---

## Grouping

Storefronts are grouped from [`creators.d`](creators.md). With no creator files
defined, `compare` falls back to the same clustering heuristic as
`fss creators suggest` and says so:

```
[notice] No creators defined — storefronts were grouped by the `fss creators suggest`
         heuristic. Run `fss creators suggest --write` to make the grouping exact.
```

The fallback exists so the command is useful before any setup, but it inherits
every limitation of the heuristic — including storefronts it cannot link at all.
Define the creators.

Creators present on fewer than two storefronts in the loaded scene set are
skipped; there is nothing to compare.

---

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--top` | 10 | How many of the widest price gaps to list per creator |
| `--exclusives` | false | Also report, per store, how many titles only it carries |
| `--csv` | _(none)_ | Write every shared title to this CSV file |

Plus the shared scene-source flags — `--db`, `--json`, `--dir`,
`--from-creator`, `--from-studio`, `--from-performer` — documented in
[usage.md](usage.md#scene-sources-fss-stash-import-fss-identify-fss-compare-fss-creators-suggest).

### CSV output

`--csv shopping-list.csv` writes one row per title carried by more than one
store:

```
creator,title,stores,cheapest_store,cheapest_price,dearest_store,dearest_price,spread,cheapest_url
Jane Eyre,The Long Way Home,2,janeeyre.example,9.99,vidvault.example,49.99,40.00,https://...
```

`cheapest_url` is the scene URL at the cheapest store, so the file is directly
actionable. Empty price columns mean that store exposed no amount. As with every
CSV FSS writes, cells are guarded against spreadsheet formula injection —
scraped titles are attacker-controlled text.
