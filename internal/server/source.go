package server

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/aai-labs/aai-alertmanager-ui/internal/config"
)

type source struct {
	base                      string
	client                    *http.Client
	token, username, password string
}

func newSource(c config.Source) (*source, error) {
	s := &source{base: strings.TrimRight(c.URL, "/")}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	if c.CAFile != "" {
		pem, e := os.ReadFile(c.CAFile)
		if e != nil {
			return nil, fmt.Errorf("read CA file: %w", e)
		}
		pool, e := x509.SystemCertPool()
		if e != nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, errors.New("CA file has no certificates")
		}
		transport.TLSClientConfig.RootCAs = pool
	}
	readSecret := func(path string) (string, error) { b, e := os.ReadFile(path); return strings.TrimSpace(string(b)), e }
	var err error
	if c.BearerTokenFile != "" {
		s.token, err = readSecret(c.BearerTokenFile)
		if err != nil {
			return nil, fmt.Errorf("read bearer token: %w", err)
		}
		if s.token == "" {
			return nil, errors.New("bearer token is empty")
		}
	}
	if c.BasicAuth != nil {
		s.username = c.BasicAuth.Username
		s.password, err = readSecret(c.BasicAuth.PasswordFile)
		if err != nil {
			return nil, fmt.Errorf("read basic auth password: %w", err)
		}
	}
	s.client = &http.Client{Timeout: 10 * time.Second, Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return s, nil
}
func (s *source) request(r *http.Request, method, path string, body io.Reader) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(r.Context(), method, s.base+path, body)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if s.token != "" {
		req.Header.Set("Authorization", "Bearer "+s.token)
	}
	if s.username != "" {
		req.SetBasicAuth(s.username, s.password)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	const max = 32 << 20
	b, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if len(b) > max {
		return nil, resp.StatusCode, errors.New("upstream response exceeds limit")
	}
	return b, resp.StatusCode, nil
}
