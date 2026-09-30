package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/moodiness/ingest/internal/backups"
)

func backupCommand(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		fmt.Print(`Usage: ingest backup restore [options]

Restore a TRUSTED server-generated age archive into a NEW PostgreSQL database
and NEW private state directory. PostgreSQL archives contain executable SQL:
never restore an archive from an untrusted sender. Existing destinations are
always refused. No --clean/overwrite mode exists.

Required:
  --archive PATH          Encrypted .age backup
  --identity-file PATH    Private file containing the age recovery identity
  --database NAME         NEW destination database name (lowercase letters,
                          digits and underscores, starting with a letter)
  --state-dir PATH        NEW state directory; parent must already exist
  --trust-archive         Confirm the archive is trusted executable SQL

Environment:
  INGEST_RESTORE_DATABASE_URL  Maintenance connection to destination server
                              (fallback DATABASE_URL). Never put credentials
                              in command-line arguments.
  INGEST_PG_DUMP               pg_dump executable path (default: PATH)
  INGEST_PG_RESTORE            pg_restore executable path (default: PATH)

Client, archive and server PostgreSQL major versions must match. The database
role needs CREATEDB. Restore authenticates the entire archive, verifies schema,
all table row counts and vault/MFA decryptability, then pauses runs, cancels
outbound deliveries, revokes sessions, and disables shares and schedules.

Exact original source files remain in recovered-sources/. The active providers/
directory is empty. Review each JSON source before copying it into providers/;
convert historical YAML with the offline migrate-sources command first.
Configure a new administrator password, use the restored vault.key, and review
shares, webhooks and schedules before enabling them. Do not start collection
or reuse the original source configuration automatically.
`)
		return nil
	}
	if args[0] != "restore" {
		return errors.New("unknown backup subcommand; run ingest backup help")
	}
	flags := flag.NewFlagSet("backup restore", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var archive, identityFile, database, state string
	var trusted bool
	flags.StringVar(&archive, "archive", "", "encrypted archive")
	flags.StringVar(&identityFile, "identity-file", "", "private recovery identity file")
	flags.StringVar(&database, "database", "", "new destination database")
	flags.StringVar(&state, "state-dir", "", "new private state directory")
	flags.BoolVar(&trusted, "trust-archive", false, "trust archive executable SQL")
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return backupCommand(ctx, []string{"help"})
		}
		return errors.New("invalid restore arguments; run ingest backup help")
	}
	if flags.NArg() != 0 || archive == "" || identityFile == "" || database == "" || state == "" || !trusted {
		return errors.New("restore requires archive, identity-file, database, state-dir and trust-archive; run ingest backup help")
	}
	info, err := os.Lstat(identityFile)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 1024 {
		return errors.New("recovery identity must be a private regular file of at most 1024 bytes")
	}
	secret, err := readPrivateFile(identityFile)
	if err != nil {
		return errors.New("cannot read private recovery identity")
	}
	defer clear(secret)
	databaseURL := os.Getenv("INGEST_RESTORE_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = os.Getenv("DATABASE_URL")
	}
	if databaseURL == "" {
		return errors.New("INGEST_RESTORE_DATABASE_URL is required")
	}
	operation, cancel := context.WithTimeout(ctx, 2*time.Hour)
	defer cancel()
	report, err := backups.Restore(operation, backups.RestoreOptions{ArchivePath: archive, Identity: strings.TrimSpace(string(secret)), DatabaseURL: databaseURL, TargetDatabase: database, StateDir: state})
	if err != nil {
		return err
	}
	fmt.Printf("Recovery verified: %d tables, %d exact source files, %d decryptable vault secrets (PostgreSQL %d).\n", report.Tables, report.SourceFiles, report.Secrets, report.PostgreSQLMajor)
	fmt.Print("The new destination is ready for review. Active sources are empty; original source files are in recovered-sources/. Sessions are invalidated, active runs paused, outbound deliveries cancelled, and shares/webhooks/schedules disabled. Configure a new administrator password, use the restored vault.key, review each JSON source before moving it into providers/, convert historical YAML with the offline migrate-sources command first, and explicitly enable only the automation you intend.\n")
	return nil
}
