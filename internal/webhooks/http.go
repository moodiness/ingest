// Package webhooks delivers durable, explicitly configured administration events.
package webhooks

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/moodiness/ingest/internal/model"
	"github.com/moodiness/ingest/internal/store"
)

const attemptTimeout = 15 * time.Second

// ValidateURL permits explicitly configured HTTP(S) destinations, including LAN
// receivers, but rejects embedded credentials, fragments, malformed ports and
// special-purpose addresses. Query/path tokens remain exclusively in the vault.
// The transport revalidates every resolved address at connect time, preventing
// DNS rebinding into link-local metadata, multicast and unspecified addresses.
func ValidateURL(value string) error {
	if len(value) == 0 || len(value) > 8192 || strings.TrimSpace(value) != value {
		return model.ErrInvalid
	}
	u, err := url.Parse(value)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.Opaque != "" || strings.ContainsAny(u.Host, "\\%") || strings.HasSuffix(u.Host, ":") {
		return model.ErrInvalid
	}
	if u.Port() != "" {
		port, err := strconv.Atoi(u.Port())
		if err != nil || port < 1 || port > 65535 {
			return model.ErrInvalid
		}
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil && !permittedAddress(ip) {
		return model.ErrInvalid
	}
	return nil
}

func permittedAddress(ip net.IP) bool {
	return !ip.IsUnspecified() && !ip.IsMulticast() && !ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast()
}

func safeDial(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, errors.New("invalid webhook destination")
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil || len(addresses) == 0 {
		return nil, errors.New("webhook destination could not be resolved")
	}
	for _, address := range addresses {
		if !permittedAddress(address.IP) {
			return nil, errors.New("webhook destination address is not permitted")
		}
	}
	dialer := net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	for _, address := range addresses {
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(address.IP.String(), port))
		if err == nil {
			return conn, nil
		}
		if ctx.Err() != nil {
			break
		}
	}
	return nil, errors.New("webhook destination connection failed")
}

func webhookClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = safeDial
	transport.MaxIdleConns = 4
	transport.MaxIdleConnsPerHost = 1
	transport.ResponseHeaderTimeout = 10 * time.Second
	return &http.Client{Transport: transport, Timeout: attemptTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

type attemptResult struct {
	status  int
	outcome string
	retry   bool
}

func deliver(ctx context.Context, client *http.Client, resolver model.SecretResolver, claim *store.WebhookClaim) attemptResult {
	ctx, cancel := context.WithTimeout(ctx, attemptTimeout)
	defer cancel()
	destination, err := resolver(ctx, claim.URLSecretRef)
	if err != nil {
		return attemptResult{outcome: "destination", retry: !errors.Is(err, model.ErrNotFound) && !errors.Is(err, model.ErrInvalid)}
	}
	if ValidateURL(destination) != nil {
		return attemptResult{outcome: "destination"}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, destination, bytes.NewReader(claim.Body))
	if err != nil {
		return attemptResult{outcome: "destination"}
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Ingest-Event-ID", claim.Delivery.EventID)
	request.Header.Set("X-Ingest-Delivery-ID", claim.Delivery.ID)
	if claim.SigningSecretRef != "" {
		key, err := resolver(ctx, claim.SigningSecretRef)
		if err != nil {
			return attemptResult{outcome: "signing", retry: !errors.Is(err, model.ErrNotFound) && !errors.Is(err, model.ErrInvalid)}
		}
		if key == "" {
			return attemptResult{outcome: "signing"}
		}
		timestamp := strconv.FormatInt(time.Now().Unix(), 10)
		mac := hmac.New(sha256.New, []byte(key))
		_, _ = mac.Write([]byte(timestamp + "."))
		_, _ = mac.Write(claim.Body)
		request.Header.Set("X-Ingest-Timestamp", timestamp)
		request.Header.Set("X-Ingest-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}
	response, err := client.Do(request)
	if err != nil {
		return attemptResult{outcome: "network", retry: true}
	}
	// Never retain or log receiver response bytes, even for failed requests.
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 32<<10))
	_ = response.Body.Close()
	result := attemptResult{status: response.StatusCode, outcome: "http"}
	switch {
	case response.StatusCode >= 200 && response.StatusCode < 300:
		result.outcome = "delivered"
	case response.StatusCode >= 300 && response.StatusCode < 400:
		result.outcome = "redirect"
	case response.StatusCode == 408 || response.StatusCode == 425 || response.StatusCode == 429 || (response.StatusCode >= 500 && response.StatusCode < 600):
		result.retry = true
	}
	return result
}

func retryDelay(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	if attempts > 5 {
		attempts = 5
	}
	return time.Second * 5 * time.Duration(1<<uint(attempts-1))
}
