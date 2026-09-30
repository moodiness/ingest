package backups

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"filippo.io/age"
	"github.com/jackc/pgx/v5"
	"github.com/moodiness/ingest/internal/model"
	"github.com/moodiness/ingest/internal/providers"
)

const maxArchiveBytes int64 = 1 << 40 // 1 TiB, including extracted dump; fail closed above this bound.
const maxManifestBytes = 4 << 20

type entryDigest struct {
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}
type manifest struct {
	Version         int                    `json:"version"`
	CreatedAt       time.Time              `json:"created_at"`
	PostgreSQLMajor int                    `json:"postgresql_major"`
	Entries         map[string]entryDigest `json:"entries"`
	Tables          map[string]int64       `json:"tables"`
	Migrations      []int                  `json:"migrations"`
}
type hashWriter struct {
	io.Writer
	n int64
}

func (w *hashWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > maxArchiveBytes-w.n {
		return 0, errors.New("archive exceeds safe size limits")
	}
	n, e := w.Writer.Write(p)
	w.n += int64(n)
	return n, e
}

func tableCounts(ctx context.Context, tx pgx.Tx) (map[string]int64, error) {
	rows, err := tx.Query(ctx, `SELECT n.nspname,c.relname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.relkind IN ('r','p') AND n.nspname NOT IN ('pg_catalog','information_schema') AND n.nspname NOT LIKE 'pg_toast%' ORDER BY n.nspname,c.relname`)
	if err != nil {
		return nil, errors.New("cannot inspect backup tables")
	}
	names := [][2]string{}
	for rows.Next() {
		var a, b string
		if err = rows.Scan(&a, &b); err != nil {
			rows.Close()
			return nil, errors.New("cannot inspect backup tables")
		}
		names = append(names, [2]string{a, b})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, errors.New("cannot inspect backup tables")
	}
	counts := make(map[string]int64, len(names))
	for _, n := range names {
		var count int64
		if err = tx.QueryRow(ctx, "SELECT count(*) FROM "+pgx.Identifier{n[0], n[1]}.Sanitize()).Scan(&count); err != nil {
			return nil, errors.New("cannot count backup records")
		}
		counts[pgx.Identifier{n[0], n[1]}.Sanitize()] = count
	}
	return counts, nil
}
func migrationVersions(ctx context.Context, tx pgx.Tx) ([]int, error) {
	rows, err := tx.Query(ctx, `SELECT version FROM ingest.schema_migrations ORDER BY version`)
	if err != nil {
		return nil, errors.New("cannot inspect backup schema")
	}
	defer rows.Close()
	versions := []int{}
	for rows.Next() {
		var v int
		if err = rows.Scan(&v); err != nil {
			return nil, errors.New("cannot inspect backup schema")
		}
		versions = append(versions, v)
	}
	return versions, rows.Err()
}
func writeEntry(z *zip.Writer, m *manifest, name string, reader io.Reader) error {
	h := &zip.FileHeader{Name: name, Method: zip.Store}
	h.SetMode(0600)
	out, err := z.CreateHeader(h)
	if err != nil {
		return errors.New("cannot write encrypted archive")
	}
	sum := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, sum), io.LimitReader(reader, maxArchiveBytes+1))
	if err != nil || n > maxArchiveBytes {
		return errors.New("archive entry exceeds limits or could not be written")
	}
	m.Entries[name] = entryDigest{Bytes: n, SHA256: hex.EncodeToString(sum.Sum(nil))}
	return nil
}
func (s *Service) createArchive(ctx context.Context, j model.BackupJob) (int64, string, map[string]any, error) {
	recipient, err := age.ParseX25519Recipient(j.Recipient)
	if err != nil {
		return 0, "", nil, errors.New("a valid recovery recipient is required")
	}
	conn, err := connect(ctx, s.options.DatabaseURL)
	if err != nil {
		return 0, "", nil, err
	}
	defer closeConn(conn)
	major, err := checkVersion(ctx, s.tools, conn)
	if err != nil {
		return 0, "", nil, err
	}
	var tx pgx.Tx
	var snapshot string
	var sources []providers.SnapshotFile
	err = s.options.Registry.WithSnapshot(ctx, func(files []providers.SnapshotFile) error {
		var e error
		tx, e = conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
		if e != nil {
			return errors.New("cannot establish database snapshot")
		}
		if e = tx.QueryRow(ctx, "SELECT pg_export_snapshot()").Scan(&snapshot); e != nil {
			return errors.New("cannot export database snapshot")
		}
		sources = files
		return nil
	})
	if tx != nil {
		defer func() {
			cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = tx.Rollback(cleanup)
		}()
	}
	if err != nil {
		return 0, "", nil, err
	}
	if err = s.phase(ctx, j.ID, "snapshot"); err != nil {
		return 0, "", nil, err
	}
	m := manifest{Version: 1, CreatedAt: time.Now().UTC(), PostgreSQLMajor: major, Entries: map[string]entryDigest{}}
	m.Tables, err = tableCounts(ctx, tx)
	if err != nil {
		return 0, "", nil, err
	}
	m.Migrations, err = migrationVersions(ctx, tx)
	if err != nil {
		return 0, "", nil, err
	}
	temp, err := s.root.OpenFile(j.ID+".partial", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return 0, "", nil, errors.New("cannot create private encrypted backup")
	}
	defer temp.Close()
	staged := false
	defer func() {
		if !staged {
			_ = s.root.Remove(j.ID + ".partial")
		}
	}()
	sum := sha256.New()
	count := &hashWriter{Writer: io.MultiWriter(temp, sum)}
	encrypted, err := age.Encrypt(count, recipient)
	if err != nil {
		return 0, "", nil, errors.New("cannot initialize backup encryption")
	}
	z := zip.NewWriter(encrypted)
	if err = s.phase(ctx, j.ID, "dumping"); err != nil {
		return 0, "", nil, err
	}
	dumpHeader := &zip.FileHeader{Name: "database.dump", Method: zip.Store}
	dumpHeader.SetMode(0600)
	dumpWriter, err := z.CreateHeader(dumpHeader)
	if err != nil {
		return 0, "", nil, errors.New("cannot create database archive entry")
	}
	dumpHash := sha256.New()
	dumpCount := &hashWriter{Writer: io.MultiWriter(dumpWriter, dumpHash)}
	if err = runPG(ctx, s.tools.dump, s.options.DatabaseURL, dumpCount, "--format=custom", "--no-owner", "--no-privileges", "--snapshot="+snapshot); err != nil {
		return 0, "", nil, err
	}
	if dumpCount.n > maxArchiveBytes {
		return 0, "", nil, errors.New("database dump exceeds archive limits")
	}
	m.Entries["database.dump"] = entryDigest{Bytes: dumpCount.n, SHA256: hex.EncodeToString(dumpHash.Sum(nil))}
	if err = tx.Commit(ctx); err != nil {
		return 0, "", nil, errors.New("database snapshot was interrupted")
	}
	for _, source := range sources {
		if err = writeEntry(z, &m, "providers/"+source.Name, bytesReader(source.Data)); err != nil {
			return 0, "", nil, err
		}
	}
	if err = writeEntry(z, &m, "master.key", bytesReader(s.masterKey)); err != nil {
		return 0, "", nil, err
	}
	data, err := json.Marshal(m)
	if err != nil || len(data) > maxManifestBytes {
		return 0, "", nil, errors.New("backup manifest exceeds limits")
	}
	mh := &zip.FileHeader{Name: "manifest.json", Method: zip.Store}
	mh.SetMode(0600)
	out, err := z.CreateHeader(mh)
	if err != nil {
		return 0, "", nil, errors.New("cannot write backup manifest")
	}
	if _, err = out.Write(data); err != nil {
		return 0, "", nil, errors.New("cannot write backup manifest")
	}
	if err = z.Close(); err != nil {
		return 0, "", nil, errors.New("cannot finalize archive")
	}
	if err = encrypted.Close(); err != nil {
		return 0, "", nil, errors.New("cannot finalize encryption")
	}
	if err = temp.Sync(); err != nil {
		return 0, "", nil, errors.New("cannot sync encrypted backup")
	}
	if err = temp.Close(); err != nil {
		return 0, "", nil, errors.New("cannot close encrypted backup")
	}
	checksum := hex.EncodeToString(sum.Sum(nil))
	report := map[string]any{"tables": len(m.Tables), "source_files": len(sources), "postgresql_major": major}
	// An uncertain database acknowledgement must not discard completed ciphertext.
	staged = true
	if err = s.options.Store.StageBackupArchive(ctx, j.ID, s.owner, count.n, checksum, report); err != nil {
		return 0, "", nil, err
	}
	if err = s.root.Rename(j.ID+".partial", j.ID+".age"); err != nil {
		return 0, "", nil, errors.New("cannot finalize private backup")
	}
	dir, e := s.root.Open(".")
	if e != nil {
		return 0, "", nil, errors.New("cannot sync backup directory")
	}
	e = dir.Sync()
	dir.Close()
	if e != nil {
		return 0, "", nil, errors.New("cannot sync backup directory")
	}
	return count.n, checksum, report, nil
}

// A tiny reader avoids converting secret byte slices to immutable strings.
type sliceReader struct{ data []byte }

func (r *sliceReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, nil
}
func bytesReader(data []byte) io.Reader { return &sliceReader{data: data} }

func safeEntry(name string) bool {
	if name == "database.dump" || name == "master.key" || name == "manifest.json" {
		return true
	}
	if filepath.ToSlash(name) != name || len(name) > 300 {
		return false
	}
	dir, base := filepath.Split(name)
	return dir == "providers/" && base != "" && base != "." && base != ".." && filepath.Base(base) == base && !containsBackslash(base) && hasSourceArchiveSuffix(base)
}
func sortedEntryNames(m map[string]entryDigest) []string {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
