package main

import (
	"context"
	"testing"
	"time"
)

// TestCachedUserByID_ReturnsFreshEntryWithoutDB seeds the cache directly (no DB
// call reachable) and confirms cachedUserByID returns the cached user instead
// of calling userByID.
func TestCachedUserByID_ReturnsFreshEntryWithoutDB(t *testing.T) {
	h := testHandler() // db == nil; userByID would panic/fail if called
	want := &User{ID: 1, Username: "alice"}
	h.userCache[1] = &userCacheEntry{user: want, expires: time.Now().Add(time.Minute)}

	got, err := h.cachedUserByID(context.Background(), 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != want {
		t.Errorf("got %+v, want the cached pointer %+v", got, want)
	}
}

func TestInvalidateUserCache_RemovesEntry(t *testing.T) {
	h := testHandler()
	h.userCache[3] = &userCacheEntry{user: &User{ID: 3}, expires: time.Now().Add(time.Minute)}

	h.invalidateUserCache(3)

	if _, ok := h.userCache[3]; ok {
		t.Error("entry still present after invalidateUserCache")
	}
}
