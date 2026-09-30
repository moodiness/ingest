package httpapi

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

type pacedBackupResponse struct {
	http.ResponseWriter
	delay time.Duration
}

func (w *pacedBackupResponse) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *pacedBackupResponse) Write(p []byte) (int, error) {
	time.Sleep(w.delay)
	return w.ResponseWriter.Write(p)
}

func TestBackupDownloadOutlivesAbsoluteServerWriteTimeout(t *testing.T) {
	payload := bytes.Repeat([]byte("encrypted recovery archive\n"), 32768)
	path := filepath.Join(t.TempDir(), "backup.age")
	if err := os.WriteFile(path, payload, 0600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		file, err := os.Open(path)
		if err != nil {
			t.Error(err)
			return
		}
		defer file.Close()
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", `attachment; filename="backup.age"`)
		serveBackupContent(&pacedBackupResponse{ResponseWriter: w, delay: 10 * time.Millisecond}, r, file, "backup.age", time.Time{}, nil)
	}))
	server.Config.WriteTimeout = 50 * time.Millisecond
	server.Start()
	defer server.Close()
	started := time.Now()
	response, err := server.Client().Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || !bytes.Equal(body, payload) {
		t.Fatalf("progress-making download truncated: bytes=%d error=%v", len(body), err)
	}
	if time.Since(started) <= server.Config.WriteTimeout {
		t.Fatal("fixture did not cross the absolute server timeout")
	}
	if response.Header.Get("Content-Disposition") != `attachment; filename="backup.age"` {
		t.Fatal("download lost its attachment disposition")
	}
}

func TestBackupDownloadPreservesRangeAndHead(t *testing.T) {
	payload := []byte("0123456789abcdefghijklmnopqrstuvwxyz")
	path := filepath.Join(t.TempDir(), "backup.age")
	if err := os.WriteFile(path, payload, 0600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		file, err := os.Open(path)
		if err != nil {
			t.Error(err)
			return
		}
		defer file.Close()
		serveBackupContent(w, r, file, "backup.age", time.Time{}, nil)
	}))
	defer server.Close()
	request, _ := http.NewRequest(http.MethodGet, server.URL, nil)
	request.Header.Set("Range", "bytes=10-19")
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != http.StatusPartialContent || string(body) != "abcdefghij" || response.Header.Get("Content-Range") != "bytes 10-19/36" {
		t.Fatalf("range download failed: status=%d body=%q error=%v", response.StatusCode, body, err)
	}
	response, err = server.Client().Head(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, err = io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || len(body) != 0 || response.StatusCode != http.StatusOK || response.Header.Get("Content-Length") != strconv.Itoa(len(payload)) {
		t.Fatalf("HEAD response changed: status=%d body=%q error=%v", response.StatusCode, body, err)
	}
}

func TestBackupDownloadStopsOnDisconnectAndShutdown(t *testing.T) {
	for _, reason := range []string{"disconnect", "shutdown"} {
		t.Run(reason, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "backup.age")
			file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
			if err != nil {
				t.Fatal(err)
			}
			// A sparse file outgrows the socket buffers without a large allocation.
			if err := file.Truncate(128 << 20); err != nil {
				file.Close()
				t.Fatal(err)
			}
			file.Close()
			shutdown, finished := make(chan struct{}), make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(finished)
				file, err := os.Open(path)
				if err != nil {
					t.Error(err)
					return
				}
				defer file.Close()
				serveBackupContent(w, r, file, "backup.age", time.Time{}, shutdown)
			}))
			defer server.Close()
			conn, err := net.DialTimeout("tcp", strings.TrimPrefix(server.URL, "http://"), 5*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
			if _, err := io.WriteString(conn, "GET / HTTP/1.1\r\nHost: localhost\r\n\r\n"); err != nil {
				t.Fatal(err)
			}
			reader := bufio.NewReader(conn)
			response, err := http.ReadResponse(reader, nil)
			if err != nil || response.StatusCode != http.StatusOK {
				t.Fatalf("download did not start: %v", err)
			}
			// Stop consuming the response, then require the active request to exit.
			if reason == "disconnect" {
				conn.Close()
			} else {
				close(shutdown)
			}
			select {
			case <-finished:
			case <-time.After(5 * time.Second):
				t.Fatal("blocked backup download did not release its request on " + reason)
			}
		})
	}
}

func TestBackupDownloadRejectsUnsupportedDeadlines(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "backup-*.age")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := io.WriteString(file, "private encrypted backup"); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder() // Deliberately has no deadline controller.
	serveBackupContent(response, httptest.NewRequest(http.MethodGet, "/", nil), file, "backup.age", time.Time{}, nil)
	if response.Code != http.StatusServiceUnavailable || strings.Contains(response.Body.String(), "private encrypted backup") {
		t.Fatalf("unsupported streaming deadlines did not fail closed: %d", response.Code)
	}
}
