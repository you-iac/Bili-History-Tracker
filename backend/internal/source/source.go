// Package source defines the platform-independent metadata query contract.
package source

import (
	"context"
	"fmt"
	"time"
)

type Creator struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// CreatorProfile is an independently observed creator profile. Creator remains
// the compact author reference embedded in Video, so video responses stay stable.
// A nil FollowerCount means the source did not provide a count.
type CreatorProfile struct {
	Platform      string    `json:"platform"`
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Description   string    `json:"description"`
	AvatarURL     string    `json:"avatar_url"`
	URL           string    `json:"url"`
	FollowerCount *int64    `json:"follower_count"`
	ObservedAt    time.Time `json:"observed_at"`
	SourceURL     string    `json:"source_url"`
}

// Video is observed metadata, not proof that a video can currently be played.
// A nil PublishedAt means the source did not supply a publication time.
type Video struct {
	Platform        string     `json:"platform"`
	ID              string     `json:"id"`
	Title           string     `json:"title"`
	Description     string     `json:"description"`
	URL             string     `json:"url"`
	CoverURL        string     `json:"cover_url"`
	Creator         Creator    `json:"creator"`
	PublishedAt     *time.Time `json:"published_at"`
	DurationSeconds int64      `json:"duration_seconds"`
	ObservedAt      time.Time  `json:"observed_at"`
	SourceURL       string     `json:"source_url"`
}

// Provider implementations own platform-specific identifiers, requests and parsing.
// Implementations must be safe for concurrent queries and respect ctx cancellation.
type Provider interface {
	Platform() string
	GetVideo(ctx context.Context, id string) (Video, error)
}

// CreatorProvider is an optional capability of a registered Provider. Platforms
// that only support video queries need not implement profile queries.
type CreatorProvider interface {
	GetCreator(ctx context.Context, id string) (CreatorProfile, error)
}

type ErrorKind string

const (
	InvalidInput         ErrorKind = "invalid_input"
	UnsupportedPlatform  ErrorKind = "unsupported_platform"
	UnsupportedOperation ErrorKind = "unsupported_operation"
	NotFound             ErrorKind = "not_found"
	AccessRestricted     ErrorKind = "access_restricted"
	RateLimited          ErrorKind = "rate_limited"
	UpstreamFailure      ErrorKind = "upstream_failure"
	InvalidResponse      ErrorKind = "invalid_response"
)

// Error preserves diagnostic codes without exposing upstream response bodies.
// NotFound describes a query result; it does not establish deletion or its cause.
type Error struct {
	Kind         ErrorKind
	Platform     string
	HTTPStatus   int
	UpstreamCode int
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s query: %s (HTTP %d, upstream code %d)",
		e.Platform, e.Kind, e.HTTPStatus, e.UpstreamCode)
}

// Service dispatches queries to providers. Its registry is immutable after construction.
type Service struct {
	providers map[string]Provider
}

func NewService(providers ...Provider) (*Service, error) {
	s := &Service{providers: make(map[string]Provider, len(providers))}
	for _, p := range providers {
		if p == nil || p.Platform() == "" {
			return nil, fmt.Errorf("provider must declare a platform")
		}
		name := p.Platform()
		if _, exists := s.providers[name]; exists {
			return nil, fmt.Errorf("duplicate provider: %s", name)
		}
		s.providers[name] = p
	}
	return s, nil
}

func (s *Service) GetVideo(ctx context.Context, platform, id string) (Video, error) {
	p, err := s.lookup(platform, id)
	if err != nil {
		return Video{}, err
	}
	return p.GetVideo(ctx, id)
}

func (s *Service) GetCreator(ctx context.Context, platform, id string) (CreatorProfile, error) {
	p, err := s.lookup(platform, id)
	if err != nil {
		return CreatorProfile{}, err
	}
	creatorProvider, ok := p.(CreatorProvider)
	if !ok {
		return CreatorProfile{}, &Error{Kind: UnsupportedOperation, Platform: platform}
	}
	return creatorProvider.GetCreator(ctx, id)
}

func (s *Service) lookup(platform, id string) (Provider, error) {
	if platform == "" || id == "" {
		return nil, &Error{Kind: InvalidInput, Platform: platform}
	}
	p, ok := s.providers[platform]
	if !ok {
		return nil, &Error{Kind: UnsupportedPlatform, Platform: platform}
	}
	return p, nil
}
