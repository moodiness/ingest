package providers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

// SnapshotFile retains the actual filename and exact bytes, including invalid JSON.
type SnapshotFile struct {
	Name string
	Data []byte
}

// WithSnapshot holds the same cross-process lock as source writes. The callback
// must establish its database snapshot before returning and must not re-enter
// the registry. Unsafe/unreadable or oversized files fail the entire backup.
func (r *Registry) WithSnapshot(ctx context.Context, fn func([]SnapshotFile) error) error {
	if err := r.acquireSnapshot(ctx); err != nil {
		return err
	}
	defer r.release()
	dir, err := r.root.Open(".")
	if err != nil {
		return fmt.Errorf("cannot open source snapshot")
	}
	entries, err := dir.ReadDir(-1)
	dir.Close()
	if err != nil {
		return fmt.Errorf("cannot enumerate source snapshot")
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	files := make([]SnapshotFile, 0, len(entries))
	var total int64
	for _, entry := range entries {
		name := entry.Name()
		lower := strings.ToLower(name)
		if !strings.HasSuffix(lower, ".json") {
			continue
		}
		if !utf8.ValidString(name) || strings.ContainsRune(name, '\\') {
			return fmt.Errorf("source snapshot contains a nonportable filename")
		}
		info, err := r.root.Lstat(name)
		if err != nil || !privateRegular(info) {
			return fmt.Errorf("source snapshot contains an unsafe file")
		}
		if info.Size() > 8<<20 || len(files) >= 10000 {
			return fmt.Errorf("source snapshot exceeds safe archive limits")
		}
		file, err := r.root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
		if err != nil {
			return fmt.Errorf("cannot read source snapshot")
		}
		opened, err := file.Stat()
		if err != nil || !privateRegular(opened) || !os.SameFile(info, opened) {
			file.Close()
			return fmt.Errorf("source file changed during snapshot")
		}
		raw, err := io.ReadAll(io.LimitReader(file, (8<<20)+1))
		file.Close()
		after, statErr := r.root.Lstat(name)
		if err != nil || statErr != nil || !privateRegular(after) || !os.SameFile(opened, after) || after.Size() != int64(len(raw)) || !after.ModTime().Equal(opened.ModTime()) || len(raw) > 8<<20 {
			return fmt.Errorf("cannot read a stable source snapshot")
		}
		total += int64(len(raw))
		if total > 64<<20 {
			return fmt.Errorf("source snapshot exceeds safe archive limits")
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		files = append(files, SnapshotFile{Name: name, Data: raw})
	}
	return fn(files)
}

// Unlike ordinary short registry operations, backup lock acquisition is
// cancellable so a stopped process holding a flock cannot defeat job deadlines.
func (r *Registry) acquireSnapshot(ctx context.Context) error {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for !r.mu.TryLock() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
	if r.root == nil {
		r.mu.Unlock()
		return fmt.Errorf("provider registry is closed")
	}
	for {
		err := syscall.Flock(int(r.lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EAGAIN) && !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EINTR) {
			r.mu.Unlock()
			return fmt.Errorf("cannot lock source snapshot")
		}
		select {
		case <-ctx.Done():
			r.mu.Unlock()
			return ctx.Err()
		case <-ticker.C:
		}
	}
	info, err := r.root.Lstat(".registry.lock")
	opened, openedErr := r.lock.Stat()
	if err != nil || openedErr != nil || !privateRegular(info) || !os.SameFile(info, opened) {
		r.release()
		return fmt.Errorf("source snapshot lock changed")
	}
	return nil
}
