// Package photos reads the iPhone's Photos.sqlite library (pulled over AFC to
// a local cache) and exposes albums and their assets. The database is opened
// read-only and the phone is never written to.
package photos

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/AnimeshRy/moira/internal/idevice"
)

// Smart albums are virtual views over the whole library.
var smart = []struct{ Key, Title, Where string }{
	{"all", "All Photos & Videos", "a.ZTRASHEDSTATE=0"},
	{"videos", "Videos", "a.ZTRASHEDSTATE=0 AND a.ZKIND=1"},
	{"favorites", "Favorites", "a.ZTRASHEDSTATE=0 AND a.ZFAVORITE=1"},
	{"trash", "Recently Deleted", "a.ZTRASHEDSTATE=1"},
}

type Album struct {
	ID    int64  // ZGENERICALBUM.Z_PK; 0 for smart albums
	Smart string // smart key, "" for user albums
	Title string
	Count int
	Bytes int64
}

type Asset struct {
	ID       int64
	Dir      string // e.g. DCIM/100APPLE
	Name     string // e.g. IMG_0001.HEIC
	Video    bool
	Favorite bool
	Local    bool // original file is on the device (false = iCloud-only, cannot be exported)
	Bytes    int64
	Created  time.Time
	Duration time.Duration
}

func (a Asset) Path() string { return a.Dir + "/" + a.Name }

type Library struct {
	db       *sql.DB
	join     string // e.g. Z_33ASSETS
	albumCol string // e.g. Z_33ALBUMS
	assetCol string // e.g. Z_3ASSETS
}

// Open pulls Photos.sqlite (+WAL) from the device into the user cache dir and
// opens it read-only.
func Open(ctx context.Context, udid string) (*Library, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(cache, "moira", udid)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	for _, f := range []string{"Photos.sqlite", "Photos.sqlite-wal", "Photos.sqlite-shm"} {
		local := filepath.Join(dir, f)
		_ = os.Remove(local)
		if err := idevice.Pull(ctx, udid, "PhotoData/"+f, local); err != nil && f == "Photos.sqlite" {
			return nil, fmt.Errorf("read photo library: %w", err)
		}
	}
	return OpenFile(ctx, filepath.Join(dir, "Photos.sqlite"))
}

// OpenFile opens an already-downloaded Photos.sqlite.
func OpenFile(ctx context.Context, path string) (*Library, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return nil, err
	}
	l := &Library{db: db}
	if err := l.discoverJoin(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return l, nil
}

func (l *Library) Close() error { return l.db.Close() }

// discoverJoin finds the album<->asset join table, whose name/columns carry a
// schema-version number that changes between iOS releases (Z_33ASSETS, ...).
func (l *Library) discoverJoin(ctx context.Context) error {
	rows, err := l.db.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name LIKE 'Z\_%ASSETS' ESCAPE '\'`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return err
		}
		tables = append(tables, t)
	}
	for _, t := range tables {
		cols, err := l.db.QueryContext(ctx, "PRAGMA table_info("+t+")")
		if err != nil {
			return err
		}
		var album, asset string
		for cols.Next() {
			var cid, notnull, pk int
			var name, typ string
			var dflt sql.NullString
			if err := cols.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
				cols.Close()
				return err
			}
			switch {
			case strings.HasSuffix(name, "ALBUMS"):
				album = name
			case strings.HasSuffix(name, "ASSETS") && !strings.Contains(name, "FOK"):
				asset = name
			}
		}
		cols.Close()
		if album != "" && asset != "" {
			l.join, l.albumCol, l.assetCol = t, album, asset
			return nil
		}
	}
	return fmt.Errorf("unsupported Photos.sqlite schema: album/asset join table not found")
}

// Albums returns smart albums followed by the user's own albums.
func (l *Library) Albums(ctx context.Context) ([]Album, error) {
	var out []Album
	for _, s := range smart {
		var a Album
		err := l.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(x.ZORIGINALFILESIZE),0)
			FROM ZASSET a LEFT JOIN ZADDITIONALASSETATTRIBUTES x ON x.ZASSET=a.Z_PK
			WHERE a.ZFILENAME IS NOT NULL AND `+s.Where).Scan(&a.Count, &a.Bytes)
		if err != nil {
			return nil, err
		}
		a.Smart, a.Title = s.Key, s.Title
		out = append(out, a)
	}
	q := fmt.Sprintf(`SELECT g.Z_PK, COALESCE(g.ZTITLE,'(untitled)'), COUNT(a.Z_PK), COALESCE(SUM(x.ZORIGINALFILESIZE),0)
		FROM ZGENERICALBUM g
		LEFT JOIN %s j ON j.%s=g.Z_PK
		LEFT JOIN ZASSET a ON a.Z_PK=j.%s AND a.ZTRASHEDSTATE=0 AND a.ZFILENAME IS NOT NULL
		LEFT JOIN ZADDITIONALASSETATTRIBUTES x ON x.ZASSET=a.Z_PK
		WHERE g.ZKIND=2 AND g.ZTRASHEDSTATE=0
		GROUP BY g.Z_PK ORDER BY g.ZTITLE COLLATE NOCASE`, l.join, l.albumCol, l.assetCol)
	rows, err := l.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var a Album
		if err := rows.Scan(&a.ID, &a.Title, &a.Count, &a.Bytes); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// localSQL is true when an asset's original file is on the phone. Anything else
// has been offloaded to iCloud by Optimize Storage and cannot be fetched over
// USB. One definition, used by both Assets and CloudOnly.
const localSQL = `EXISTS(SELECT 1 FROM ZINTERNALRESOURCE r WHERE r.ZASSET=a.Z_PK AND r.ZVERSION=0 AND r.ZRESOURCETYPE IN (0,1) AND r.ZLOCALAVAILABILITY=1)`

// CloudOnly counts the non-trashed assets whose originals live only in iCloud,
// and what they would weigh if they were downloaded. They take almost no space
// on the phone, which is the point of reporting them.
func (l *Library) CloudOnly(ctx context.Context) (count int, bytes int64, err error) {
	err = l.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(x.ZORIGINALFILESIZE),0)
		FROM ZASSET a LEFT JOIN ZADDITIONALASSETATTRIBUTES x ON x.ZASSET=a.Z_PK
		WHERE a.ZTRASHEDSTATE=0 AND a.ZFILENAME IS NOT NULL AND NOT `+localSQL).Scan(&count, &bytes)
	return count, bytes, err
}

// Assets lists an album's assets, newest first.
func (l *Library) Assets(ctx context.Context, alb Album) ([]Asset, error) {
	q := `SELECT a.Z_PK, a.ZDIRECTORY, a.ZFILENAME, a.ZKIND, COALESCE(a.ZFAVORITE,0),
		COALESCE(x.ZORIGINALFILESIZE,0), COALESCE(a.ZDATECREATED,0), COALESCE(a.ZDURATION,0),
		` + localSQL + `
		FROM ZASSET a LEFT JOIN ZADDITIONALASSETATTRIBUTES x ON x.ZASSET=a.Z_PK `
	var args []any
	where := "a.ZDIRECTORY IS NOT NULL AND a.ZFILENAME IS NOT NULL AND "
	if alb.Smart != "" {
		for _, s := range smart {
			if s.Key == alb.Smart {
				where += s.Where
			}
		}
	} else {
		q += fmt.Sprintf("JOIN %s j ON j.%s=a.Z_PK AND j.%s=? ", l.join, l.assetCol, l.albumCol)
		args = append(args, alb.ID)
		where += "a.ZTRASHEDSTATE=0"
	}
	rows, err := l.db.QueryContext(ctx, q+"WHERE "+where+" ORDER BY a.ZDATECREATED DESC", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Asset
	for rows.Next() {
		var a Asset
		var kind int
		var created, dur float64
		if err := rows.Scan(&a.ID, &a.Dir, &a.Name, &kind, &a.Favorite, &a.Bytes, &created, &dur, &a.Local); err != nil {
			return nil, err
		}
		a.Video = kind == 1
		a.Created = coreDataEpoch.Add(time.Duration(created * float64(time.Second)))
		a.Duration = time.Duration(dur * float64(time.Second))
		out = append(out, a)
	}
	return out, rows.Err()
}

var coreDataEpoch = time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)
