# Moira – engineering handoff

Read this before changing anything. It records what exists, why, what was
verified against the real device, and the hard rules.

## Hard rules (from the owner)

1. **Never write to the iPhone.** Only `idevice_id`, `ideviceinfo` and
   `afcclient get`/`ls`/`info` are allowed. No `put`, `rm`, `mv`, `mkdir` on
   the device. The user is cleaning up irreplaceable photos.
2. **Never copy or delete user files without explicit confirmation.** The TUI
   requires a literal `y` before any export. Keep it that way.
3. **Never overwrite local files.** Same-size existing file = skip; different
   size = report, leave untouched.
4. Don't commit or push unless asked. Repo is `git init`ed with no commits.

## What Moira is

Go CLI + TUI (Bubble Tea) that reads an iPhone over USB via libimobiledevice.
It opens on a device dashboard (identity, storage breakdown, battery health),
from which you browse either **Photos** (the Photos library, per album) or
**Documents** (the non-photo AFC roots), tick what you want and copy it to the
Mac. Export only; nothing is ever written to the phone.

## Layout

| Path | Purpose |
|---|---|
| `cmd/moira/main.go` | Flags `-udid`, `-out` (default `~/Pictures/Moira`); `moira devices` and `moira info` subcommands; otherwise starts TUI. Two or more devices and no `-udid` opens `tui.PickDevice`, falling back to a "pick one with -udid" error when there is no terminal. `moira info` prints device/storage/battery as plain text — the cheapest way to check that data source without the TUI. |
| `internal/idevice/idevice.go` | Shells out to `idevice_id -l`, `ideviceinfo -k DeviceName`, `afcclient -u UDID get /path local`. |
| `internal/idevice/stat.go` | `Stat()` → `Info`: identity, disk-usage categories, battery health. `parseKV` reads ideviceinfo's `Key: value` output; `plistInt` regex-scrapes three GasGauge ints out of idevicediagnostics' XML. |
| `internal/idevice/afc.go` | `Ls()` → `[]Entry` for the documents browser: `afcclient ls` for names + one `afcclient info` per name for size/type/mtime. |
| `internal/idevice/idevice_test.go` | Fixture-based tests for both parsers. No device needed. |
| `internal/photos/photos.go` | Pulls `PhotoData/Photos.sqlite` (+`-wal`, `-shm`) to `~/Library/Caches/moira/<udid>/`, opens read-only with `modernc.org/sqlite` (pure Go, no cgo). Exposes `Albums()`, `Assets(album)` and `CloudOnly()`. The `localSQL` const is the single definition of "the original is on this phone", used by both `Assets` and `CloudOnly`. |
| `internal/photos/photos_test.go` | Unit test on a tiny fake schema. Run `make test`. |
| `internal/tui/tui.go` | Screens: loading → **home** → {albums → assets \| files} → confirm → exporting → done. `assetDelegate` and `fileDelegate` are separate on purpose — each type-asserts its own item type. |
| `internal/tui/export.go` | One copy loop over `exportItem{Remote,Name,Bytes,Local}`; `assetItems`/`fileItems` adapt photos and documents onto it. `.part` file → size check → rename. iCloud-only items reported as failed. |
| `internal/tui/tui_test.go` | `parentDir`, `docDstDir`, `fileItems` (directories are never exported). |
| `Makefile` | `build`, `test`, `lint` (vet + gofmt). |

Dependencies: bubbletea, bubbles, lipgloss, modernc.org/sqlite. Go 1.25 (go.mod).

## Photos.sqlite facts (verified on iOS on device 00008120-001E05D01A39A01E)

- Readable over plain AFC at `/PhotoData/Photos.sqlite` (190 MB, ~5 s pull).
  Always copy the `-wal` too or data is stale.
- `ZASSET`: `ZDIRECTORY` (e.g. `DCIM/105APPLE`), `ZFILENAME`, `ZKIND`
  (0 photo, 1 video), `ZFAVORITE`, `ZTRASHEDSTATE` (1 = Recently Deleted),
  `ZDATECREATED` (seconds since 2001-01-01 UTC), `ZDURATION`.
- `ZADDITIONALASSETATTRIBUTES.ZORIGINALFILESIZE` joined on `ZASSET = ZASSET.Z_PK`.
- `ZGENERICALBUM`: `ZKIND=2` are user albums; `ZTITLE`; skip `ZTRASHEDSTATE=1`.
- Album↔asset join table has a **schema-version number in its name and
  columns** (`Z_33ASSETS` with `Z_33ALBUMS`, `Z_3ASSETS` on this phone; differs
  across iOS versions). `discoverJoin()` finds it dynamically via
  `sqlite_master` + `PRAGMA table_info`. Do not hardcode.
- iCloud-only detection: an asset's original is on the phone iff
  `ZINTERNALRESOURCE` has a row with `ZASSET=a.Z_PK, ZVERSION=0,
  ZRESOURCETYPE IN (0,1), ZLOCALAVAILABILITY=1`. Exposed as `Asset.Local`.
- Smart albums are virtual (`photos.go` `smart` slice): All, Videos,
  Favorites, Recently Deleted. "All Photos & Videos" covers items in no album.

## Device stats (verified on device 00008120-001E05D01A39A01E, iOS 26.4.2)

| Data | Command | Keys |
|---|---|---|
| Identity | `ideviceinfo` | `DeviceName`, `ProductType`, `ProductVersion`, `ModelNumber`, `SerialNumber` |
| Storage | `ideviceinfo -q com.apple.disk_usage.factory` | `TotalDiskCapacity`, `TotalDataCapacity`, `AmountDataAvailable`, `PhotoUsage`, `CameraUsage`, `CalendarUsage`, `NotesUsage`, `VoicemailUsage`, `MediaCacheUsage`, `WebAppCacheUsage` |
| Battery charge | `ideviceinfo -q com.apple.mobile.battery` | `BatteryCurrentCapacity`, `BatteryIsCharging` |
| Battery health | `idevicediagnostics diagnostics All` | `GasGauge.CycleCount` (857), `.DesignCapacity` (3329 mAh), `.FullChargeCapacity` (100 = health %) |

- **`TotalDataAvailable` is stale — do not use it.** It reported 93.2 GB free on
  a 128 GB phone holding 43 GB of photos, which is impossible.
  `AmountDataAvailable` (6.9 GB) is the live figure. `Used = TotalDataCapacity -
  AmountDataAvailable`; `Other = Used - Σ(categories)` and absorbs apps, system
  and caches.
- `PhotoUsage` and `CameraUsage` are the same number here; they collapse into
  one row rather than being double-counted.
- `idevicediagnostics ioregistry` is **not** supported by this libimobiledevice
  build — `diagnostics All` is the one that works. It can also hang, so `Stat`
  gives it its own 10 s timeout.
- Everything except the base `ideviceinfo` call degrades to zero values. A phone
  that withholds a domain still renders a home screen.
- `ideviceinstaller` is not installed on the owner's Mac, so per-app Documents
  (`afcclient --documents <bundleid>`, house arrest) is not wired up. Documents
  means the plain-AFC media-partition roots only.

## Documents

`docRoots` in `tui.go` = `/Books`, `/Downloads`, `/Recordings`, `/Podcasts` —
everything plain AFC exposes that isn't the photo library. The browser lists a
virtual root of those four, descends into directories, and exports files into
`<out>/Documents/<mirrored phone path>`. Directories are never exported
(no recursion). `afcclient ls` returns names only, so each entry costs one
`afcclient info` call; fine for these directories, which hold tens of files.

## iCloud / purgeable space — answered, do not redo

The owner asked for an option to purge iCloud content. **It cannot be built**
and the premise is wrong:

- libimobiledevice has no API to free caches or purgeable space, and hard rule
  #1 forbids writing to the phone either way.
- The iCloud-only assets are *offloaded*: 184 items totalling 850 MB **if they
  were downloaded**. On the phone they cost almost nothing. Purging them would
  free nothing.
- The real gap (~65 GB of "Other") is apps plus iOS-managed purgeable cache.
  Only iOS reclaims that, on its own schedule, when the disk fills.

So it is a **report**: `photos.CloudOnly()` returns count and would-be bytes,
and the home screen states plainly that it is not reclaimable over USB.

## afcclient gotchas

- **Exits 0 on failure** (prints `Error: ... Not found (8)`). `idevice.Pull`
  checks output for `Error:` and stats the local file; `export.go` also
  verifies size against `ZORIGINALFILESIZE`.
- `afcclient get` per file spawns a process; fine for thousands of files,
  ~36 MB/s throughput observed.
- `afcclient info /DCIM/x` returns JSON with `st_size`.

## Size investigation (answered, do not redo)

Owner asked why Moira shows 36.65 GB while iMazing shows 88 GB.

| Measurement | Result |
|---|---|
| Sum of real file sizes in `/DCIM/*` (3694 files, stat'd one by one) | 37.8 GB |
| Moira total (library originals, non-trashed) | 36.65 GB |
| All `ZINTERNALRESOURCE` local rows (originals + edits + derivatives) | ~46 GB |
| iMazing / iOS Settings "Photos" | 88 GB |

Conclusion: the 88 GB is the iOS *storage category*, which includes
`PhotoData` caches (thumbnails, video renders, iCloud sync cache, indexes)
and Recently Deleted. Exportable media is ~38 GB. The 1 GB gap between 37.8
and 36.65 is Live Photo `.MOV` pairs and `.AAE` sidecars (not separate assets).

Also found: **187 assets are iCloud-only** (Optimize Storage offloaded them;
e.g. `DCIM/102APPLE` has 33 library items and 0 files on disk). They cannot be
fetched over USB. Moira labels them `☁ iCloud only` and skips them at export
with an error line.

Library snapshot at time of writing: 2143 items, 271 videos = 32.4 GB of the
36.65 GB; 17 user albums.

## Known gaps / ideas (not requested, don't build unless asked)

- No deletion from phone (impossible safely via libimobiledevice anyway).
- No per-app Documents (house arrest); would need `ideviceinstaller` for an app list.
- Documents export is not recursive — directories are skipped, not walked.
- No HEIC→JPEG conversion, no thumbnails/preview.
- Live Photo `.MOV` companions are not exported (only the library asset).
- Two albums with the same title export into the same directory.
- Export is sequential; parallel `afcclient` would be faster.
- Library is re-pulled on every launch (~5 s). Could cache by mtime.

## Running

```sh
brew install libimobiledevice   # already installed on owner's Mac
make build && ./moira devices && ./moira
```
```sh
./moira info    # device, storage breakdown, battery health as plain text
```

Keys: home — `p` photos · `d` documents · `q` quit.
Lists — enter open · space select · a all/none · / filter · e export · esc back · q quit.
