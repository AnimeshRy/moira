package photos

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// Minimal replica of the Photos.sqlite tables Moira depends on, with the
// versioned join table named differently from the real phone to prove
// discovery works.
const schema = `
CREATE TABLE ZGENERICALBUM (Z_PK INTEGER PRIMARY KEY, ZTITLE TEXT, ZKIND INTEGER, ZTRASHEDSTATE INTEGER);
CREATE TABLE ZASSET (Z_PK INTEGER PRIMARY KEY, ZDIRECTORY TEXT, ZFILENAME TEXT, ZKIND INTEGER, ZFAVORITE INTEGER, ZTRASHEDSTATE INTEGER, ZDATECREATED REAL, ZDURATION REAL);
CREATE TABLE ZADDITIONALASSETATTRIBUTES (Z_PK INTEGER PRIMARY KEY, ZASSET INTEGER, ZORIGINALFILESIZE INTEGER);
CREATE TABLE ZINTERNALRESOURCE (Z_PK INTEGER PRIMARY KEY, ZASSET INTEGER, ZVERSION INTEGER, ZRESOURCETYPE INTEGER, ZLOCALAVAILABILITY INTEGER);
CREATE TABLE Z_99ASSETS (Z_99ALBUMS INTEGER, Z_3ASSETS INTEGER, Z_FOK_3ASSETS INTEGER);
INSERT INTO ZGENERICALBUM VALUES (1,'Trip',2,0),(2,'Deleted album',2,1),(3,'Folder',1500,0);
INSERT INTO ZASSET VALUES (10,'DCIM/100APPLE','IMG_1.HEIC',0,1,0,0,0),(11,'DCIM/100APPLE','IMG_2.MOV',1,0,0,86400,12.5),(12,'DCIM/100APPLE','IMG_3.HEIC',0,0,1,0,0);
INSERT INTO ZADDITIONALASSETATTRIBUTES VALUES (1,10,100),(2,11,2000),(3,12,5);
INSERT INTO ZINTERNALRESOURCE VALUES (1,10,0,0,1),(2,11,0,1,-1);
INSERT INTO Z_99ASSETS VALUES (1,10,0),(1,11,1),(1,12,2),(2,10,0);
`

func TestAlbumsAndAssets(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "Photos.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	db.Close()

	lib, err := OpenFile(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer lib.Close()
	if lib.join != "Z_99ASSETS" || lib.albumCol != "Z_99ALBUMS" || lib.assetCol != "Z_3ASSETS" {
		t.Fatalf("join discovery: %+v", lib)
	}

	albums, err := lib.Albums(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][2]int64{ // title -> count, bytes
		"All Photos & Videos": {2, 2100}, "Videos": {1, 2000}, "Favorites": {1, 100},
		"Recently Deleted": {1, 5}, "Trip": {2, 2100},
	}
	if len(albums) != len(want) {
		t.Fatalf("got %d albums: %+v", len(albums), albums)
	}
	for _, a := range albums {
		if w := want[a.Title]; int64(a.Count) != w[0] || a.Bytes != w[1] {
			t.Errorf("%s: got %d/%d want %d/%d", a.Title, a.Count, a.Bytes, w[0], w[1])
		}
	}

	assets, err := lib.Assets(ctx, albums[len(albums)-1]) // Trip
	if err != nil {
		t.Fatal(err)
	}
	if len(assets) != 2 || assets[0].Name != "IMG_2.MOV" || !assets[0].Video || assets[0].Path() != "DCIM/100APPLE/IMG_2.MOV" {
		t.Fatalf("assets: %+v", assets)
	}
	if assets[0].Local || !assets[1].Local {
		t.Errorf("local flags: %v %v", assets[0].Local, assets[1].Local)
	}
	if got := assets[0].Created.Format("2006-01-02"); got != "2001-01-02" {
		t.Errorf("created = %s", got)
	}
}
