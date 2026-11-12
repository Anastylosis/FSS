//go:build !windows

package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/models"
)

func TestSQLitePragmasSurviveANewConnection(t *testing.T) {
	s, err := NewSQLite(filepath.Join(t.TempDir(), "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	// Idle connections are dropped, so every query below opens a fresh one:
	// the DSN, not a one-off Exec, must be what carries the pragmas.
	s.db.SetMaxIdleConns(0)

	for i := 0; i < 2; i++ {
		var fk, busy int
		if err := s.db.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil {
			t.Fatal(err)
		}
		if err := s.db.QueryRow("PRAGMA busy_timeout").Scan(&busy); err != nil {
			t.Fatal(err)
		}
		if fk != 1 || busy != 60000 {
			t.Fatalf("connection %d: foreign_keys=%d busy_timeout=%d, want 1 and 60000", i, fk, busy)
		}
	}
}

func TestSQLiteConcurrentFirstOpenMigratesOnce(t *testing.T) {
	for round := 0; round < 10; round++ {
		path := filepath.Join(t.TempDir(), "fresh.db")
		var wg sync.WaitGroup
		errs := make(chan error, 2)
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				s, err := NewSQLite(path)
				if err != nil {
					errs <- err
					return
				}
				_ = s.Close()
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Fatalf("round %d: concurrent open: %v", round, err)
		}
		s, err := NewSQLite(path)
		if err != nil {
			t.Fatalf("round %d: reopen: %v", round, err)
		}
		var v int
		if err := s.db.QueryRow(`SELECT MAX(version) FROM schema_version`).Scan(&v); err != nil || v != latestSchemaVersion {
			t.Fatalf("round %d: version %d err %v, want %d", round, v, err, latestSchemaVersion)
		}
		_ = s.Close()
	}
}

func TestSQLiteDoubleSlashPath(t *testing.T) {
	dir := t.TempDir()
	s, err := NewSQLite("/" + filepath.Join(dir, "x.db"))
	if err != nil {
		t.Fatalf("NewSQLite with a // path: %v", err)
	}
	_ = s.Close()
}

func TestSQLiteTwoHandlesSaveDifferentStudios(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared.db")
	a, err := NewSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	b, err := NewSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })

	mk := func(studio string, n int) []models.Scene {
		out := make([]models.Scene, n)
		for i := range out {
			out[i] = models.Scene{
				ID: fmt.Sprint(i), SiteID: "x", StudioURL: studio, Title: "t",
				URL: studio + "/" + fmt.Sprint(i), ScrapedAt: time.Now().UTC(),
				Tags: []string{"a", "b"}, Performers: []string{"p" + fmt.Sprint(i)},
			}
		}
		return out
	}

	var wg sync.WaitGroup
	errs := make(chan error, 40)
	for round := 0; round < 10; round++ {
		for _, tc := range []struct {
			s   *SQLite
			url string
		}{{a, "https://a.example/s"}, {b, "https://b.example/s"}} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := tc.s.Save(tc.url, mk(tc.url, 50)); err != nil {
					errs <- err
				}
			}()
		}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("Save: %v", err)
	}
	for _, u := range []string{"https://a.example/s", "https://b.example/s"} {
		got, err := a.Load(u)
		if err != nil || len(got) != 50 {
			t.Errorf("Load(%s) = %d scenes, err %v; want 50", u, len(got), err)
		}
	}
}

func TestLockContextWaitIsCancellableAndAnnounced(t *testing.T) {
	var buf strings.Builder
	var mu sync.Mutex
	origOut, origAfter, origPoll := lockNoticeOut, lockNoticeAfter, lockPollInterval
	lockNoticeOut = writerFunc(func(p []byte) (int, error) { mu.Lock(); defer mu.Unlock(); return buf.Write(p) })
	lockNoticeAfter, lockPollInterval = 30*time.Millisecond, 5*time.Millisecond
	t.Cleanup(func() { lockNoticeOut, lockNoticeAfter, lockPollInterval = origOut, origAfter, origPoll })

	dir := t.TempDir()
	check := func(t *testing.T, hold func() (interface{ Close() error }, error), wait func(context.Context) error) {
		held, err := hold()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = held.Close() }()
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- wait(ctx) }()
		time.Sleep(150 * time.Millisecond)
		cancel()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("wait error = %v, want context.Canceled", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("lock wait ignored cancellation")
		}
		mu.Lock()
		defer mu.Unlock()
		if !strings.Contains(buf.String(), "waiting for lock on https://example.com/studio") {
			t.Errorf("no wait notice, got %q", buf.String())
		}
	}

	t.Run("flat", func(t *testing.T) {
		buf.Reset()
		other := NewFlat(dir, nil) // a second handle stands in for another process
		f := NewFlat(dir, nil)
		check(t,
			func() (interface{ Close() error }, error) { return other.Lock("https://example.com/studio") },
			func(ctx context.Context) error {
				_, err := f.LockContext(ctx, "https://example.com/studio")
				return err
			})
	})
	t.Run("sqlite", func(t *testing.T) {
		buf.Reset()
		s := newFileDB(t, dir)
		check(t,
			func() (interface{ Close() error }, error) { return s.Lock("https://example.com/studio") },
			func(ctx context.Context) error {
				_, err := s.LockContext(ctx, "https://example.com/studio")
				return err
			})
	})
}

type writerFunc func([]byte) (int, error)

func (w writerFunc) Write(p []byte) (int, error) { return w(p) }

func newFileDB(t *testing.T, dir string) *SQLite {
	t.Helper()
	s, err := NewSQLite(filepath.Join(dir, "lock.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func lockedWithin(f *Flat, url string, d time.Duration) (ok bool, release func()) {
	type res struct {
		c   interface{ Close() error }
		err error
	}
	ch := make(chan res, 1)
	go func() {
		c, err := f.Lock(url)
		ch <- res{c, err}
	}()
	select {
	case r := <-ch:
		return r.err == nil, func() { _ = r.c.Close() }
	case <-time.After(d):
		return false, func() {}
	}
}

func TestFlatDoubleCloseKeepsNewerHolder(t *testing.T) {
	f := NewFlat(t.TempDir(), nil)
	first, err := f.Lock(flatTestURL)
	if err != nil {
		t.Fatal(err)
	}
	_ = first.Close()
	second, err := f.Lock(flatTestURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Close() }()
	_ = first.Close() // stale second Close must be a no-op

	ok, release := lockedWithin(f, flatTestURL, 2*time.Second)
	if !ok {
		t.Fatal("a stale second Close dropped the newer holder; the next Lock blocked on its own flock")
	}
	release()
}

func TestFlatWaitOnOneStudioDoesNotBlockAnother(t *testing.T) {
	dir := t.TempDir()
	f := NewFlat(dir, nil)
	foreign := NewFlat(dir, nil)

	heldA, err := f.Lock("https://example.com/a")
	if err != nil {
		t.Fatal(err)
	}
	blocker, err := foreign.Lock("https://example.com/b")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	waiting := make(chan error, 1)
	go func() { _, err := f.LockContext(ctx, "https://example.com/b"); waiting <- err }()
	time.Sleep(100 * time.Millisecond)

	closed := make(chan struct{})
	go func() {
		_ = heldA.Close()
		c, err := f.Lock("https://example.com/c")
		if err == nil {
			_ = c.Close()
		}
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("Lock/Close on other studios blocked behind a wait for studio b")
	}
	cancel()
	if err := <-waiting; !errors.Is(err, context.Canceled) {
		t.Errorf("waiting Lock = %v, want context.Canceled", err)
	}
	_ = blocker.Close()

	// A failed wait leaves no stale entry behind.
	c, err := f.Lock("https://example.com/b")
	if err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
}

func TestFlatJoinerRetriesWhenAcquirerIsCancelled(t *testing.T) {
	dir := t.TempDir()
	f := NewFlat(dir, nil)
	foreign := NewFlat(dir, nil)
	blocker, err := foreign.Lock(flatTestURL)
	if err != nil {
		t.Fatal(err)
	}

	ctxA, cancelA := context.WithCancel(context.Background())
	aDone := make(chan error, 1)
	go func() { _, err := f.LockContext(ctxA, flatTestURL); aDone <- err }()
	time.Sleep(100 * time.Millisecond)

	bDone := make(chan error, 1)
	go func() {
		c, err := f.LockContext(context.Background(), flatTestURL)
		if err == nil {
			_ = c.Close()
		}
		bDone <- err
	}()
	time.Sleep(100 * time.Millisecond)
	cancelA()
	if err := <-aDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("acquirer = %v, want context.Canceled", err)
	}
	_ = blocker.Close()
	select {
	case err := <-bDone:
		if err != nil {
			t.Fatalf("joiner inherited the acquirer's failure: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("joiner never acquired the lock")
	}
}
