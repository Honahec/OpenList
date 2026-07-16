package model

import "testing"

func TestCollectionSharingCannotBeRead(t *testing.T) {
	sharing := &Sharing{
		SharingDB: &SharingDB{FilesRaw: `["/collect"]`, Collect: true},
		Files:     []string{"/collect"},
		Creator:   &User{Role: ADMIN, Permission: 1 << 14},
	}
	if !sharing.Valid() {
		t.Fatal("collection should remain valid for collection endpoints")
	}
	if sharing.ValidForRead() {
		t.Fatal("collection must not be readable through normal sharing endpoints")
	}
}
