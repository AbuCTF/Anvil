package handlers

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPersonalizeWarRoomDoesNotMutateSharedSnapshot(t *testing.T) {
	shared := &warRoom{Grid: []warCell{
		{TeamID: "alpha", ServiceSlug: "svc", Status: "up"},
		{TeamID: "bravo", ServiceSlug: "svc", Status: "down"},
	}}

	alpha := personalizeWarRoom(shared, "alpha")
	bravo := personalizeWarRoom(shared, "bravo")
	anonymous := personalizeWarRoom(shared, "")

	if !alpha.Grid[0].IsOwn || alpha.Grid[1].IsOwn {
		t.Fatalf("alpha markers = %#v", alpha.Grid)
	}
	if bravo.Grid[0].IsOwn || !bravo.Grid[1].IsOwn {
		t.Fatalf("bravo markers = %#v", bravo.Grid)
	}
	if anonymous.Grid[0].IsOwn || anonymous.Grid[1].IsOwn {
		t.Fatalf("anonymous markers = %#v", anonymous.Grid)
	}
	if shared.Grid[0].IsOwn || shared.Grid[1].IsOwn {
		t.Fatalf("shared snapshot was mutated: %#v", shared.Grid)
	}
}

func TestWarRoomSnapshotCoalescesConcurrentLoads(t *testing.T) {
	const callers = 12
	var loads atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	h := &GameHandler{}
	h.warLoad = func(context.Context) (*warRoom, error) {
		if loads.Add(1) == 1 {
			close(started)
		}
		<-release
		return &warRoom{Tick: 42, Grid: []warCell{}}, nil
	}

	type result struct {
		room  *warRoom
		cache string
		err   error
	}
	results := make(chan result, callers)
	var ready sync.WaitGroup
	ready.Add(callers)
	start := make(chan struct{})
	for range callers {
		go func() {
			ready.Done()
			<-start
			room, cache, err := h.warRoomSnapshot(context.Background())
			results <- result{room: room, cache: cache, err: err}
		}()
	}
	ready.Wait()
	close(start)
	<-started
	close(release)

	misses := 0
	for range callers {
		r := <-results
		if r.err != nil || r.room == nil || r.room.Tick != 42 {
			t.Fatalf("snapshot result = %#v, err %v", r.room, r.err)
		}
		if r.cache == "MISS" {
			misses++
		}
	}
	if got := loads.Load(); got != 1 {
		t.Fatalf("loader calls = %d, want 1", got)
	}
	if misses != 1 {
		t.Fatalf("cache misses = %d, want 1", misses)
	}
}

func TestWarRoomSnapshotFailureDoesNotPoisonNextRefresh(t *testing.T) {
	boom := errors.New("boom")
	var loads atomic.Int32
	h := &GameHandler{}
	h.warLoad = func(context.Context) (*warRoom, error) {
		if loads.Add(1) == 1 {
			return nil, boom
		}
		return &warRoom{Tick: 7, Grid: []warCell{}}, nil
	}

	if room, _, err := h.warRoomSnapshot(context.Background()); room != nil || !errors.Is(err, boom) {
		t.Fatalf("first refresh = %#v, %v; want boom", room, err)
	}
	room, cache, err := h.warRoomSnapshot(context.Background())
	if err != nil || room == nil || room.Tick != 7 || cache != "MISS" {
		t.Fatalf("retry = %#v, %q, %v", room, cache, err)
	}
	if got := loads.Load(); got != 2 {
		t.Fatalf("loader calls = %d, want 2", got)
	}
}

func TestWarRoomSnapshotServesBoundedStaleOnRefreshFailure(t *testing.T) {
	boom := errors.New("boom")
	h := &GameHandler{
		warRoom:    &warRoom{Tick: 6, Grid: []warCell{}},
		warExpires: time.Now().Add(-time.Second),
		warLoad: func(context.Context) (*warRoom, error) {
			return nil, boom
		},
	}

	room, cache, err := h.warRoomSnapshot(context.Background())
	if room == nil || room.Tick != 6 || cache != "STALE" || !errors.Is(err, boom) {
		t.Fatalf("stale refresh = %#v, %q, %v", room, cache, err)
	}

	h.warExpires = time.Now().Add(-arenaStaleTTL - time.Second)
	room, cache, err = h.warRoomSnapshot(context.Background())
	if room != nil || cache == "STALE" || !errors.Is(err, boom) {
		t.Fatalf("expired stale refresh = %#v, %q, %v", room, cache, err)
	}
}
