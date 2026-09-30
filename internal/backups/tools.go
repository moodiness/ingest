// Package backups creates encrypted, self-contained PostgreSQL recovery archives.
package backups

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgservicefile"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var toolVersion = regexp.MustCompile(`PostgreSQL\) ([0-9]+)[.]`)
var targetName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)
var identifier = regexp.MustCompile(`^[a-f0-9]{32}$`)

type tools struct{ dump, restore string }

func configuredTools(dump, restore string) tools {
	if dump == "" {
		dump = os.Getenv("INGEST_PG_DUMP")
	}
	if dump == "" {
		dump = "pg_dump"
	}
	if restore == "" {
		restore = os.Getenv("INGEST_PG_RESTORE")
	}
	if restore == "" {
		restore = "pg_restore"
	}
	return tools{dump: dump, restore: restore}
}
func (t tools) versions(ctx context.Context) (int, error) {
	var major int
	for _, name := range []string{t.dump, t.restore} {
		cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		cmd := exec.CommandContext(cctx, name, "--version")
		cmd.Stderr = io.Discard
		out, err := cmd.Output()
		cancel()
		if err != nil {
			return 0, errors.New("PostgreSQL tools are unavailable; configure INGEST_PG_DUMP and INGEST_PG_RESTORE")
		}
		match := toolVersion.FindSubmatch(out)
		if len(match) != 2 {
			return 0, errors.New("PostgreSQL client version could not be verified")
		}
		v, _ := strconv.Atoi(string(match[1]))
		if major != 0 && major != v {
			return 0, errors.New("pg_dump and pg_restore must have the same major version")
		}
		major = v
	}
	return major, nil
}
func connectionConfig(databaseURL string) (*pgx.ConnConfig, error) {
	config, _, err := connectionSettings(databaseURL)
	return config, err
}

func connectionSettings(databaseURL string) (*pgx.ConnConfig, map[string]string, error) {
	settings, err := postgresSecuritySettings(databaseURL)
	if err != nil {
		return nil, nil, err
	}
	// Strip pool-only options exactly as Store.Open does before opening a
	// dedicated snapshot/restore connection.
	pool, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, nil, errors.New("invalid backup database configuration")
	}
	pool.ConnConfig.ConnectTimeout = 10 * time.Second
	settings["min_protocol_version"] = pool.ConnConfig.MinProtocolVersion
	settings["max_protocol_version"] = pool.ConnConfig.MaxProtocolVersion
	return pool.ConnConfig, settings, nil
}
func connect(ctx context.Context, databaseURL string) (*pgx.Conn, error) {
	config, err := connectionConfig(databaseURL)
	if err != nil {
		return nil, err
	}
	conn, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		return nil, errors.New("backup database connection failed")
	}
	return conn, nil
}
func closeConn(c *pgx.Conn) {
	if c == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = c.Close(ctx)
}
func serverMajor(ctx context.Context, c *pgx.Conn) (int, error) {
	var version string
	if err := c.QueryRow(ctx, "SHOW server_version_num").Scan(&version); err != nil {
		return 0, errors.New("cannot determine PostgreSQL server version")
	}
	n, err := strconv.Atoi(version)
	if err != nil {
		return 0, errors.New("invalid PostgreSQL server version")
	}
	return n / 10000, nil
}

// These settings are implemented by both pgx and the PostgreSQL clients.
// Service configuration overrides the environment; URI parameters override both.
var postgresSecurityEnv = map[string]string{
	"sslmode": "PGSSLMODE", "sslrootcert": "PGSSLROOTCERT",
	"sslcert": "PGSSLCERT", "sslkey": "PGSSLKEY",
	"sslsni": "PGSSLSNI", "sslnegotiation": "PGSSLNEGOTIATION",
	"channel_binding": "PGCHANNELBINDING", "require_auth": "PGREQUIREAUTH",
	"target_session_attrs": "PGTARGETSESSIONATTRS",
	"min_protocol_version": "PGMINPROTOCOLVERSION", "max_protocol_version": "PGMAXPROTOCOLVERSION",
}

// pgx cannot enforce these libpq policies. Reject them instead of allowing a
// backup's snapshot connection and subprocess to apply different security rules.
var unsupportedPostgresEnv = map[string]string{
	"hostaddr": "PGHOSTADDR", "sslcrl": "PGSSLCRL", "sslcrldir": "PGSSLCRLDIR",
	"ssl_min_protocol_version": "PGSSLMINPROTOCOLVERSION", "ssl_max_protocol_version": "PGSSLMAXPROTOCOLVERSION",
	"sslcertmode": "PGSSLCERTMODE", "sslcompression": "PGSSLCOMPRESSION",
	// libpq has no PGSSLPASSWORD equivalent; environment-only subprocesses
	// cannot safely use pgx's encrypted-key password option.
	"sslpassword": "PGSSLPASSWORD", "requiressl": "PGREQUIRESSL",
	"gssdelegation": "PGGSSDELEGATION",
	"sslkeylogfile": "PGSSLKEYLOGFILE", "gssencmode": "PGGSSENCMODE",
	"gsslib": "PGGSSLIB", "krbsrvname": "PGKRBSRVNAME", "krbspn": "",
	"requirepeer": "PGREQUIREPEER", "oauth_issuer": "PGOAUTHISSUER",
	"oauth_client_id": "PGOAUTHCLIENTID", "oauth_client_secret": "PGOAUTHCLIENTSECRET",
	"oauth_scope": "PGOAUTHSCOPE",
}

func postgresSecuritySettings(databaseURL string) (map[string]string, error) {
	u, err := url.Parse(databaseURL)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return nil, errors.New("backup tools require a PostgreSQL URL")
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return nil, errors.New("invalid backup database configuration")
	}
	settings := map[string]string{"sslmode": "prefer"}
	serviceFile := ""
	// pgx defaults use the OS account's home, not an inherited HOME override.
	if account, err := user.Current(); err == nil {
		serviceFile = filepath.Join(account.HomeDir, ".pg_service.conf")
		certDir := filepath.Join(account.HomeDir, ".postgresql")
		if runtime.GOOS == "windows" {
			certDir = filepath.Join(os.Getenv("APPDATA"), "postgresql")
		}
		cert, key := filepath.Join(certDir, "postgresql.crt"), filepath.Join(certDir, "postgresql.key")
		if _, err := os.Stat(cert); err == nil {
			if _, err := os.Stat(key); err == nil {
				settings["sslcert"], settings["sslkey"] = cert, key
			}
		}
		root := filepath.Join(certDir, "root.crt")
		if _, err := os.Stat(root); err == nil {
			settings["sslrootcert"] = root
		}
	}
	for key, name := range postgresSecurityEnv {
		if value := os.Getenv(name); value != "" {
			settings[key] = value
		}
	}
	for key, name := range unsupportedPostgresEnv {
		if value := os.Getenv(name); value != "" {
			settings[key] = value
		}
	}
	service := os.Getenv("PGSERVICE")
	if value := os.Getenv("PGSERVICEFILE"); value != "" {
		serviceFile = value
	}
	if q.Has("service") {
		service = q.Get("service")
	}
	if q.Has("servicefile") {
		serviceFile = q.Get("servicefile")
	}
	if service != "" || q.Has("service") {
		file, err := pgservicefile.ReadServicefile(serviceFile)
		if err != nil {
			return nil, errors.New("cannot read backup database service configuration")
		}
		entry, err := file.GetService(service)
		if err != nil {
			return nil, errors.New("cannot resolve backup database service configuration")
		}
		for key, value := range entry.Settings {
			settings[key] = value
		}
	}
	for key, values := range q {
		settings[key] = values[0]
	}
	for key := range unsupportedPostgresEnv {
		if settings[key] != "" {
			return nil, fmt.Errorf("backup database configuration uses unsupported security option %s", key)
		}
	}
	// Unknown SSL/GSS/OAuth settings must not silently become runtime SQL
	// parameters or be discarded by the subprocess translation.
	for key, value := range settings {
		if value != "" && (strings.HasPrefix(key, "ssl") || strings.HasPrefix(key, "gss") || strings.HasPrefix(key, "oauth")) {
			if _, supported := postgresSecurityEnv[key]; !supported {
				return nil, errors.New("backup database configuration uses an unsupported connection security option")
			}
		}
	}
	if value := settings["sslsni"]; value != "" && value != "0" && value != "1" {
		return nil, errors.New("invalid backup database TLS configuration")
	}
	if value := settings["sslnegotiation"]; value != "" && value != "postgres" && value != "direct" {
		return nil, errors.New("invalid backup database TLS configuration")
	}
	if settings["sslmode"] == "" {
		settings["sslmode"] = "prefer"
	}
	// pgx promotes system trust to hostname verification and direct negotiation
	// to TLS-only. Make that effective policy explicit for libpq too.
	if settings["sslrootcert"] == "system" {
		settings["sslmode"] = "verify-full"
	} else if settings["sslnegotiation"] == "direct" && settings["sslmode"] == "prefer" {
		settings["sslmode"] = "require"
	}
	return settings, nil
}

// Credentials live only in the child's environment. Never forward PGSERVICE:
// libpq would re-resolve it with precedence over PGDATABASE during recovery.
func postgresEnv(databaseURL string) ([]string, error) {
	c, settings, err := connectionSettings(databaseURL)
	if err != nil {
		return nil, err
	}
	hosts, ports := []string{c.Host}, []string{strconv.Itoa(int(c.Port))}
	for _, fallback := range c.Fallbacks {
		port := strconv.Itoa(int(fallback.Port))
		// pgx represents allow/prefer's TLS choices as repeated host/port pairs.
		if hosts[len(hosts)-1] != fallback.Host || ports[len(ports)-1] != port {
			hosts, ports = append(hosts, fallback.Host), append(ports, port)
		}
	}
	env := []string{"PGHOST=" + strings.Join(hosts, ","), "PGPORT=" + strings.Join(ports, ","), "PGUSER=" + c.User, "PGPASSWORD=" + c.Password, "PGDATABASE=" + c.Database, "PGPASSFILE=" + os.DevNull, "PGCONNECT_TIMEOUT=10", "PGAPPNAME=ingest-backup", "PGGSSENCMODE=disable"}
	for _, key := range []string{"PATH", "TMPDIR", "SYSTEMROOT", "APPDATA"} {
		if value := os.Getenv(key); value != "" {
			env = append(env, key+"="+value)
		}
	}
	if account, err := user.Current(); err == nil {
		env = append(env, "HOME="+account.HomeDir)
	}
	if settings["sslrootcert"] == "system" {
		for _, key := range []string{"SSL_CERT_FILE", "SSL_CERT_DIR"} {
			if value := os.Getenv(key); value != "" {
				env = append(env, key+"="+value)
			}
		}
	}
	for key, name := range postgresSecurityEnv {
		if value := settings[key]; value != "" {
			env = append(env, name+"="+value)
		}
	}
	return env, nil
}
func databaseURLFor(base, name string) (string, error) {
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return "", errors.New("recovery requires a PostgreSQL URL")
	}
	u.Path = "/" + name
	u.RawPath = ""
	q := u.Query()
	// Explicit dbname wins over both the URI path and service/environment values.
	q.Set("dbname", name)
	q.Del("database")
	u.RawQuery = q.Encode()
	return u.String(), nil
}
func runPG(ctx context.Context, path, databaseURL string, stdout io.Writer, args ...string) error {
	env, err := postgresEnv(databaseURL)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = env
	cmd.Stdout = stdout
	cmd.Stderr = io.Discard
	if err = cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("PostgreSQL archive operation failed; check tool compatibility and database permissions")
	}
	return nil
}
func checkVersion(ctx context.Context, t tools, c *pgx.Conn) (int, error) {
	client, err := t.versions(ctx)
	if err != nil {
		return 0, err
	}
	server, err := serverMajor(ctx, c)
	if err != nil {
		return 0, err
	}
	if client != server {
		return 0, fmt.Errorf("PostgreSQL client and server major versions must match (client %d, server %d)", client, server)
	}
	if server < 18 {
		return 0, errors.New("encrypted recovery requires PostgreSQL 18 or newer")
	}
	return server, nil
}
