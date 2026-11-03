# Changelog

## v0.0.3 - 2026-11-04

### Added

- The trust filter has "Not human reviewed", which selects machine confirmed and unverified concepts together, and the header counts concepts not human reviewed.
- A directory's side sheet lists the files in it that aren't concepts, each opening in a new tab, and a box on the map counts them next to its concepts.
- Links into sibling bundles: a relative link leaving its bundle by one level, such as `../kakeibo/accounts/index.md`, opens in the bundle it names when `serve` loaded it from the same directory, and is drawn on the map and counted as a backlink. This is an extension OKF v0.2 doesn't define.

## v0.0.2 - 2026-11-04

### Added

- The side sheet lists a concept's `sources`, each linked to its resource, and links `（source: <id>）` citations in the body to their sources. Ids missing from `sources` and resources missing from the bundle are marked like broken links.
- The side sheet can be maximized to the whole window and restored, with a button in its header or M. The state holds while moving between pages.

### Changed

- Directories, bundles included, are titled by their names instead of their `index.md`'s leading heading, which OKF uses to group entries.

## v0.0.1 - 2026-11-02

### Added

- `serve` browses OKF bundles: a map of every bundle with directories as nested boxes and links between concepts as arrows, search and filters by bundle, type, trust tier and state, and concept, directory and log pages.
