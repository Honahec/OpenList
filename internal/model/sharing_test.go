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

func TestCollectionCanBeViewedAfterUploadLimit(t *testing.T) {
	sharing := &Sharing{
		SharingDB: &SharingDB{Collect: true, Accessed: 1, MaxAccessed: 1},
		Files:     []string{"/collection"},
		Creator:   &User{Role: ADMIN, Permission: 1 << 14},
	}
	if sharing.Valid() {
		t.Fatal("collection at its upload limit should reject new uploads")
	}
	if !sharing.ValidForCollectionView() {
		t.Fatal("collection at its upload limit should still show visitor receipts")
	}
}
