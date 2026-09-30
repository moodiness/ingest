package backups

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgproto3"
)

func backupTestEnvironment(t *testing.T) {
	t.Helper()
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		if strings.HasPrefix(key, "PG") {
			t.Setenv(key, "")
		}
	}
}

func backupTestCertificate(t *testing.T, name string) (tls.Certificate, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: name},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true,
		KeyUsage:    x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:    []string{name}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "root.crt")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, path
}

func backupTestPGDump(t *testing.T) string {
	t.Helper()
	path := configuredTools("", "").dump
	if _, err := exec.LookPath(path); err != nil {
		t.Skip("pg_dump is required for the libpq security boundary regression")
	}
	return path
}

func TestBackupClientsRejectUntrustedServiceServer(t *testing.T) {
	backupTestEnvironment(t)
	dump := backupTestPGDump(t)
	serverCertificate, _ := backupTestCertificate(t, "untrusted")
	_, trustedRoot := backupTestCertificate(t, "trusted")
	for _, client := range []string{"snapshot", "pg_dump"} {
		for _, source := range []string{"service_over_environment", "uri_over_service", "environment"} {
			t.Run(client+"/"+source, func(t *testing.T) {
				listener, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				defer listener.Close()
				handshake := make(chan error, 1)
				go func() {
					conn, err := listener.Accept()
					if err != nil {
						handshake <- err
						return
					}
					defer conn.Close()
					_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
					var request [8]byte
					if _, err := io.ReadFull(conn, request[:]); err != nil {
						handshake <- err
						return
					}
					if binary.BigEndian.Uint32(request[4:]) != 80877103 {
						// A plaintext startup is already a security failure.
						handshake <- nil
						return
					}
					if _, err := conn.Write([]byte{'S'}); err != nil {
						handshake <- err
						return
					}
					tlsConn := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{serverCertificate}})
					handshake <- tlsConn.Handshake()
				}()
				host, port, _ := net.SplitHostPort(listener.Addr().String())
				serviceMode, environmentMode, uriMode := "verify-full", "disable", ""
				switch source {
				case "uri_over_service":
					serviceMode, uriMode = "disable", "verify-full"
				case "environment":
					serviceMode, environmentMode = "", "verify-full"
				}
				service := "[backup]\nhost=" + host + "\nport=" + port + "\nuser=backup\ndbname=source\nsslrootcert=" + trustedRoot + "\n"
				if serviceMode != "" {
					service += "sslmode=" + serviceMode + "\n"
				}
				serviceFile := filepath.Join(t.TempDir(), "pg_service.conf")
				if err := os.WriteFile(serviceFile, []byte(service), 0600); err != nil {
					t.Fatal(err)
				}
				t.Setenv("PGSSLMODE", environmentMode)
				query := url.Values{"service": {"backup"}, "servicefile": {serviceFile}, "pool_max_conns": {"8"}}
				if uriMode != "" {
					query.Set("sslmode", uriMode)
				}
				databaseURL := "postgres:///?" + query.Encode()
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if client == "snapshot" {
					conn, err := connect(ctx, databaseURL)
					if conn != nil {
						closeConn(conn)
					}
					if err == nil {
						t.Fatal("snapshot accepted an untrusted server")
					}
				} else if err := runPG(ctx, dump, databaseURL, io.Discard, "--schema-only", "--no-password"); err == nil {
					t.Fatal("pg_dump accepted an untrusted server")
				}
				select {
				case err := <-handshake:
					if err == nil {
						t.Fatal("client completed a handshake with an untrusted server or sent plaintext startup")
					}
				case <-ctx.Done():
					t.Fatal("client never reached the TLS rejection boundary")
				}
			})
		}
	}
}

func TestBackupRestoreServiceTargetsOnlyNewDatabase(t *testing.T) {
	backupTestEnvironment(t)
	dump := backupTestPGDump(t)
	for _, client := range []string{"snapshot", "pg_dump"} {
		t.Run(client, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			startup := make(chan map[string]string, 1)
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				message, err := pgproto3.NewBackend(conn, conn).ReceiveStartupMessage()
				if err == nil {
					if message, ok := message.(*pgproto3.StartupMessage); ok {
						startup <- message.Parameters
					}
				}
			}()
			host, port, _ := net.SplitHostPort(listener.Addr().String())
			serviceFile := filepath.Join(t.TempDir(), "pg_service.conf")
			service := "[backup]\nhost=" + host + "\nport=" + port + "\nuser=backup\ndbname=original_database\nsslmode=disable\npool_max_conns=8\n"
			if err := os.WriteFile(serviceFile, []byte(service), 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PGSERVICEFILE", serviceFile)
			t.Setenv("PGSERVICE", "backup")
			base := "postgres:///?dbname=explicit_source"
			target, err := databaseURLFor(base, "isolated_restore")
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if client == "snapshot" {
				conn, _ := connect(ctx, target)
				closeConn(conn)
			} else {
				_ = runPG(ctx, dump, target, io.Discard, "--schema-only", "--no-password")
			}
			select {
			case parameters := <-startup:
				if parameters["database"] != "isolated_restore" {
					t.Fatalf("client routed recovery to database %q", parameters["database"])
				}
				if _, present := parameters["pool_max_conns"]; present {
					t.Fatal("client sent pool configuration as a server startup parameter")
				}
			case <-ctx.Done():
				t.Fatal("client did not reach the isolated database startup boundary")
			}
		})
	}
}

func TestBackupRejectsUnsupportedSecurityPolicies(t *testing.T) {
	backupTestEnvironment(t)
	for _, key := range []string{"sslcrl", "ssl_min_protocol_version", "sslpassword", "gssencmode", "requirepeer"} {
		t.Run(key, func(t *testing.T) {
			query := url.Values{"sslmode": {"disable"}, key: {"sensitive-policy-value"}}
			_, err := postgresEnv("postgres://backup@localhost/source?" + query.Encode())
			if err == nil || !strings.Contains(err.Error(), "unsupported") {
				t.Fatalf("unsupported security policy was not rejected clearly: %v", err)
			}
			if strings.Contains(err.Error(), "sensitive-policy-value") {
				t.Fatal("configuration error exposed a sensitive value")
			}
		})
	}
}
