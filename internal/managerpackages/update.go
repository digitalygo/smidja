package managerpackages

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func sourceURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("package source must be HTTPS without credentials, query or fragment")
	}
	return parsed, nil
}
func (s *Store) fetch(ctx context.Context, raw string, source Source, limit int64) ([]byte, error) {
	target, err := sourceURL(raw)
	if err != nil {
		return nil, err
	}
	origin, err := sourceURL(source.URL)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, err
	}
	if source.TokenEnv != "" && target.Host == origin.Host {
		token := s.cfg.LookupEnv(source.TokenEnv)
		if token == "" || strings.ContainsAny(token, "\r\n") {
			return nil, fmt.Errorf("package source credential %s missing or invalid", source.TokenEnv)
		}
		request.Header.Set("Authorization", "Bearer "+token)
	}
	client := s.cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Minute}
	}
	local := *client
	credentialsDropped := false
	local.CheckRedirect = func(next *http.Request, previous []*http.Request) error {
		if len(previous) > 5 {
			return errors.New("package redirect limit exceeded")
		}
		if next.URL.Scheme != "https" || next.URL.Host == "" || next.URL.User != nil || next.URL.Fragment != "" {
			return errors.New("unsafe package redirect")
		}
		prior := previous[len(previous)-1].URL
		if next.URL.Scheme != prior.Scheme || next.URL.Host != prior.Host {
			credentialsDropped = true
		}
		if credentialsDropped {
			next.Header.Del("Authorization")
			next.Header.Del("Proxy-Authorization")
			next.Header.Del("Cookie")
		}
		return nil
	}
	response, err := local.Do(request)
	if err != nil {
		return nil, errors.New("package download failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("package download HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("package download too large")
	}
	return data, nil
}
func (s *Store) Update(ctx context.Context, id string) (Revision, error) {
	previous, err := s.Active(id)
	if err != nil {
		return Revision{}, err
	}
	data, err := s.fetch(ctx, previous.Source.URL, previous.Source, 1<<20)
	if err != nil {
		return Revision{}, err
	}
	var release Release
	if err = strictJSON(data, &release); err != nil {
		return Revision{}, err
	}
	if release.ID != id {
		return Revision{}, errors.New("update changed package identity")
	}
	comparison, err := compareVersion(release.Version, previous.Version)
	if err != nil {
		return Revision{}, err
	}
	if comparison < 0 {
		return Revision{}, errors.New("automatic downgrade rejected")
	}
	if comparison == 0 {
		if release.SHA256 != previous.Digest {
			return Revision{}, errors.New("published version is immutable")
		}
		return previous, nil
	}
	archive, err := s.fetch(ctx, release.ArchiveURL, previous.Source, maximumBytes)
	if err != nil {
		return Revision{}, err
	}
	return s.InstallArchive(previous.Source, release, archive)
}
func (s *Store) CheckUpdates(ctx context.Context) map[string]error {
	result := map[string]error{}
	for _, revision := range s.List() {
		if ctx.Err() != nil {
			result[revision.ID] = ctx.Err()
			continue
		}
		_, err := s.Update(ctx, revision.ID)
		result[revision.ID] = err
	}
	return result
}
func (s *Store) RunUpdates(ctx context.Context, interval time.Duration, report func(map[string]error)) {
	if interval <= 0 {
		interval = 15 * time.Minute
	}
	for {
		if ctx.Err() != nil {
			return
		}
		results := s.CheckUpdates(ctx)
		if report != nil {
			report(results)
		}
		var random [8]byte
		_, _ = rand.Read(random[:])
		jitter := time.Duration(binary.LittleEndian.Uint64(random[:]) % uint64(interval/10+1))
		timer := time.NewTimer(interval + jitter)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
