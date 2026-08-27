package security

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/brantje/llamacpp-manager/backend/internal/settings"
)

type Network struct {
	settings *settings.Service
}

func NewNetwork(s *settings.Service) *Network { return &Network{settings: s} }

func (n *Network) EffectiveScheme(r *http.Request) string {
	if externalURL, err := n.settings.String(r.Context(), settings.ExternalURL); err == nil && externalURL != "" {
		if parsed, parseErr := url.Parse(externalURL); parseErr == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") {
			return parsed.Scheme
		}
	}
	if n.isTrustedPeer(r.Context(), remoteIP(r.RemoteAddr)) {
		if proto := forwardedProto(r.Header.Get("Forwarded")); proto != "" {
			return proto
		}
		if proto := strings.ToLower(strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0])); proto == "http" || proto == "https" {
			return proto
		}
	}
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

func (n *Network) EffectiveRemoteAddress(r *http.Request) string {
	peer := remoteIP(r.RemoteAddr)
	if !peer.IsValid() || !n.isTrustedPeer(r.Context(), peer) {
		return peer.String()
	}
	chain := forwardedFor(r.Header.Get("Forwarded"))
	if len(chain) == 0 {
		for _, raw := range strings.Split(r.Header.Get("X-Forwarded-For"), ",") {
			if ip, err := netip.ParseAddr(strings.Trim(strings.TrimSpace(raw), "[]")); err == nil {
				chain = append(chain, ip.Unmap())
			}
		}
	}
	chain = append(chain, peer)
	for index := len(chain) - 1; index >= 0; index-- {
		if !n.isTrustedPeer(r.Context(), chain[index]) {
			return chain[index].String()
		}
	}
	if len(chain) > 0 {
		return chain[0].String()
	}
	return peer.String()
}

func (n *Network) OriginAllowed(r *http.Request, origin string) bool {
	origin = strings.TrimSpace(origin)
	if origin == "" {
		return true
	}
	configured, err := n.settings.String(r.Context(), settings.AllowedOrigins)
	if err == nil {
		for _, allowed := range strings.Split(configured, ",") {
			if strings.TrimSpace(allowed) == origin {
				return true
			}
		}
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" {
		return false
	}
	return strings.EqualFold(parsed.Scheme, n.EffectiveScheme(r)) && strings.EqualFold(parsed.Host, r.Host)
}

func (n *Network) IsSecure(r *http.Request) bool { return n.EffectiveScheme(r) == "https" }

func (n *Network) isTrustedPeer(ctx context.Context, ip netip.Addr) bool {
	if !ip.IsValid() {
		return false
	}
	raw, err := n.settings.String(ctx, settings.TrustedProxies)
	if err != nil || strings.TrimSpace(raw) == "" {
		return false
	}
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if prefix, err := netip.ParsePrefix(item); err == nil && prefix.Contains(ip) {
			return true
		}
		if addr, err := netip.ParseAddr(item); err == nil && addr.Unmap() == ip.Unmap() {
			return true
		}
	}
	return false
}

func remoteIP(remote string) netip.Addr {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	ip, err := netip.ParseAddr(strings.Trim(strings.TrimSpace(host), "[]"))
	if err != nil {
		return netip.Addr{}
	}
	return ip.Unmap()
}

func forwardedProto(header string) string {
	for _, part := range strings.Split(header, ";") {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if ok && strings.EqualFold(key, "proto") {
			value = strings.ToLower(strings.Trim(value, `"`))
			if value == "http" || value == "https" {
				return value
			}
		}
	}
	return ""
}

func forwardedFor(header string) []netip.Addr {
	var result []netip.Addr
	for _, hop := range strings.Split(header, ",") {
		for _, part := range strings.Split(hop, ";") {
			key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
			if !ok || !strings.EqualFold(key, "for") {
				continue
			}
			value = strings.Trim(strings.TrimSpace(value), `"`)
			if host, _, err := net.SplitHostPort(value); err == nil {
				value = host
			}
			if ip, err := netip.ParseAddr(strings.Trim(value, "[]")); err == nil {
				result = append(result, ip.Unmap())
			}
		}
	}
	return result
}

type loginAttempt struct {
	Failures    int
	UpdatedAt   time.Time
	LockedUntil time.Time
}

type LoginProtector struct {
	mu       sync.Mutex
	settings *settings.Service
	attempts map[string]loginAttempt
	maxItems int
	now      func() time.Time
}

func NewLoginProtector(s *settings.Service) *LoginProtector {
	return &LoginProtector{settings: s, attempts: map[string]loginAttempt{}, maxItems: 4096, now: time.Now}
}

func (p *LoginProtector) BeforeAttempt(ctx context.Context, username, address string) (time.Duration, bool) {
	enabled, err := p.settings.Bool(ctx, settings.LoginProtectionEnabled)
	if err != nil || !enabled {
		return 0, false
	}
	key := loginKey(username, address)
	now := p.now()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pruneLocked(now)
	attempt, ok := p.attempts[key]
	if !ok {
		return 0, false
	}
	if attempt.LockedUntil.After(now) {
		return attempt.LockedUntil.Sub(now), true
	}
	if attempt.Failures < 2 {
		return 0, false
	}
	shift := attempt.Failures - 2
	if shift > 4 {
		shift = 4
	}
	delay := 100 * time.Millisecond * time.Duration(1<<shift)
	return delay, false
}

func (p *LoginProtector) Failure(ctx context.Context, username, address string) bool {
	enabled, err := p.settings.Bool(ctx, settings.LoginProtectionEnabled)
	if err != nil || !enabled {
		return false
	}
	threshold, err := p.settings.Int(ctx, settings.LoginFailureThreshold)
	if err != nil {
		threshold = 5
	}
	lockoutSeconds, err := p.settings.Int(ctx, settings.LoginLockoutSeconds)
	if err != nil {
		lockoutSeconds = 900
	}
	key := loginKey(username, address)
	now := p.now()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pruneLocked(now)
	attempt := p.attempts[key]
	attempt.Failures++
	attempt.UpdatedAt = now
	locked := attempt.Failures >= threshold
	if locked {
		attempt.LockedUntil = now.Add(time.Duration(lockoutSeconds) * time.Second)
	}
	p.attempts[key] = attempt
	return locked
}

func (p *LoginProtector) Success(username, address string) {
	p.mu.Lock()
	delete(p.attempts, loginKey(username, address))
	p.mu.Unlock()
}

func (p *LoginProtector) pruneLocked(now time.Time) {
	for key, attempt := range p.attempts {
		if now.Sub(attempt.UpdatedAt) > 24*time.Hour && !attempt.LockedUntil.After(now) {
			delete(p.attempts, key)
		}
	}
	if len(p.attempts) <= p.maxItems {
		return
	}
	type item struct {
		key string
		at  time.Time
	}
	items := make([]item, 0, len(p.attempts))
	for key, attempt := range p.attempts {
		items = append(items, item{key: key, at: attempt.UpdatedAt})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].at.Before(items[j].at) })
	for index := 0; index < len(items)-p.maxItems; index++ {
		delete(p.attempts, items[index].key)
	}
}

func loginKey(username, address string) string {
	return strings.ToLower(strings.TrimSpace(username)) + "\x00" + strings.TrimSpace(address)
}

var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._~+/=-]+`),
	regexp.MustCompile(`(?i)\bhf_[A-Za-z0-9_-]{8,}\b`),
	regexp.MustCompile(`(?i)\bsk-lcm-[A-Za-z0-9_-]{8,}\b`),
	regexp.MustCompile(`(?i)\blcm_(?:session|csrf)=[^;\s]+`),
}

func Redact(value string) string {
	for _, pattern := range secretPatterns {
		value = pattern.ReplaceAllString(value, "[REDACTED]")
	}
	return value
}

func Headers(network *Network, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "frame-ancestors 'none'")
		if network.IsSecure(r) {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		}
		next.ServeHTTP(w, r)
	})
}
