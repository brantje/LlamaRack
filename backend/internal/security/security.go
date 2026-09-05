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

	"github.com/brantje/llamarack/backend/internal/settings"
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

type RequestForwardingDiagnostics struct {
	PeerAddress            string   `json:"peer_address"`
	PeerTrusted            bool     `json:"peer_trusted"`
	ForwardedHeader        []string `json:"forwarded_header"`
	XForwardedFor          []string `json:"x_forwarded_for"`
	EffectiveRemoteAddress string   `json:"effective_remote_address"`
}

func (n *Network) RequestForwardingDiagnostics(r *http.Request) RequestForwardingDiagnostics {
	peer := remoteIP(r.RemoteAddr)
	peerTrusted := peer.IsValid() && n.isTrustedPeer(r.Context(), peer)
	return RequestForwardingDiagnostics{
		PeerAddress:            peer.String(),
		PeerTrusted:            peerTrusted,
		ForwardedHeader:        addrsToStrings(forwardedFor(r.Header.Get("Forwarded"))),
		XForwardedFor:          addrsToStrings(xForwardedFor(r.Header.Get("X-Forwarded-For"))),
		EffectiveRemoteAddress: n.EffectiveRemoteAddress(r),
	}
}

func (n *Network) EffectiveRemoteAddress(r *http.Request) string {
	peer := remoteIP(r.RemoteAddr)
	if !peer.IsValid() || !n.isTrustedPeer(r.Context(), peer) {
		return peer.String()
	}
	chain := forwardedFor(r.Header.Get("Forwarded"))
	if len(chain) == 0 {
		chain = xForwardedFor(r.Header.Get("X-Forwarded-For"))
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
	if !strings.EqualFold(parsed.Scheme, n.EffectiveScheme(r)) {
		return false
	}
	if strings.EqualFold(parsed.Host, r.Host) {
		return true
	}

	// The development/container UI intentionally talks to the management API on
	// another port of the same host (UI :3000 -> API :8888). Treat that known
	// manager UI origin as local without opening credentialed CORS to arbitrary
	// ports or foreign hosts.
	return parsed.Port() == "3000" && strings.EqualFold(parsed.Hostname(), requestHostname(r.Host))
}

func requestHostname(hostport string) string {
	if host, _, err := net.SplitHostPort(hostport); err == nil {
		return strings.Trim(host, "[]")
	}
	if strings.HasPrefix(hostport, "[") {
		if end := strings.Index(hostport, "]"); end > 0 {
			return hostport[1:end]
		}
	}
	return strings.Trim(hostport, "[]")
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

func xForwardedFor(header string) []netip.Addr {
	var result []netip.Addr
	for _, raw := range strings.Split(header, ",") {
		if ip, err := netip.ParseAddr(strings.Trim(strings.TrimSpace(raw), "[]")); err == nil {
			result = append(result, ip.Unmap())
		}
	}
	return result
}

func addrsToStrings(addrs []netip.Addr) []string {
	if len(addrs) == 0 {
		return nil
	}
	result := make([]string, 0, len(addrs))
	for _, addr := range addrs {
		result = append(result, addr.String())
	}
	return result
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

const (
	loginAddressDelayAfter   = 8
	loginAddressTTL          = 10 * time.Minute
	defaultMaxLoginAttempts  = 4096
	defaultMaxLoginAddresses = 1024
)

type loginAttempt struct {
	Failures    int
	UpdatedAt   time.Time
	LockedUntil time.Time
}

type LoginProtector struct {
	mu           sync.Mutex
	settings     *settings.Service
	attempts     map[string]loginAttempt
	addresses    map[string]loginAttempt
	maxItems     int
	maxAddresses int
	now          func() time.Time
}

func NewLoginProtector(s *settings.Service) *LoginProtector {
	return &LoginProtector{
		settings:     s,
		attempts:     map[string]loginAttempt{},
		addresses:    map[string]loginAttempt{},
		maxItems:     defaultMaxLoginAttempts,
		maxAddresses: defaultMaxLoginAddresses,
		now:          time.Now,
	}
}

func (p *LoginProtector) BeforeAttempt(ctx context.Context, username, address string) (time.Duration, bool) {
	enabled, err := p.settings.Bool(ctx, settings.LoginProtectionEnabled)
	if err != nil || !enabled {
		return 0, false
	}
	key := loginKey(username, address)
	address = strings.TrimSpace(address)
	now := p.now()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pruneLocked(now)

	accountDelay, accountLocked := loginAttemptPenalty(p.attempts[key], now, 2)
	if accountLocked {
		return accountDelay, true
	}
	addressDelay, _ := loginAttemptPenalty(p.addresses[address], now, loginAddressDelayAfter)
	if addressDelay > accountDelay {
		return addressDelay, false
	}
	return accountDelay, false
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
	p.recordAddressFailure(strings.TrimSpace(address), now)

	attempt, exists := p.attempts[key]
	if !exists && !p.makeAttemptRoom(now) {
		return false
	}
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

func (p *LoginProtector) makeAttemptRoom(now time.Time) bool {
	if p.maxItems <= 0 {
		return false
	}
	if len(p.attempts) < p.maxItems {
		return true
	}
	oldestKey := ""
	var oldest time.Time
	for key, attempt := range p.attempts {
		if attempt.LockedUntil.After(now) {
			continue
		}
		if oldestKey == "" || attempt.UpdatedAt.Before(oldest) {
			oldestKey = key
			oldest = attempt.UpdatedAt
		}
	}
	if oldestKey == "" {
		return false
	}
	delete(p.attempts, oldestKey)
	return true
}

func (p *LoginProtector) recordAddressFailure(address string, now time.Time) {
	if address == "" || p.maxAddresses <= 0 {
		return
	}
	if _, exists := p.addresses[address]; !exists && len(p.addresses) >= p.maxAddresses {
		p.evictOldestAddress()
	}
	attempt := p.addresses[address]
	attempt.Failures++
	attempt.UpdatedAt = now
	p.addresses[address] = attempt
}

func (p *LoginProtector) pruneLocked(now time.Time) {
	for key, attempt := range p.attempts {
		if now.Sub(attempt.UpdatedAt) > 24*time.Hour && !attempt.LockedUntil.After(now) {
			delete(p.attempts, key)
		}
	}
	if len(p.attempts) > p.maxItems {
		type item struct {
			key string
			at  time.Time
		}
		items := make([]item, 0, len(p.attempts))
		for key, attempt := range p.attempts {
			if attempt.LockedUntil.After(now) {
				continue
			}
			items = append(items, item{key: key, at: attempt.UpdatedAt})
		}
		sort.Slice(items, func(i, j int) bool { return items[i].at.Before(items[j].at) })
		remove := len(p.attempts) - p.maxItems
		if remove > len(items) {
			remove = len(items)
		}
		for index := 0; index < remove; index++ {
			delete(p.attempts, items[index].key)
		}
	}
	p.pruneAddresses(now)
}

func (p *LoginProtector) pruneAddresses(now time.Time) {
	for address, attempt := range p.addresses {
		if now.Sub(attempt.UpdatedAt) > loginAddressTTL {
			delete(p.addresses, address)
		}
	}
	for len(p.addresses) > p.maxAddresses {
		p.evictOldestAddress()
	}
}

func (p *LoginProtector) evictOldestAddress() {
	oldestKey := ""
	var oldest time.Time
	for key, attempt := range p.addresses {
		if oldestKey == "" || attempt.UpdatedAt.Before(oldest) {
			oldestKey = key
			oldest = attempt.UpdatedAt
		}
	}
	if oldestKey != "" {
		delete(p.addresses, oldestKey)
	}
}

func loginAttemptPenalty(attempt loginAttempt, now time.Time, delayAfter int) (time.Duration, bool) {
	if attempt.LockedUntil.After(now) {
		return attempt.LockedUntil.Sub(now), true
	}
	if attempt.Failures < delayAfter {
		return 0, false
	}
	shift := attempt.Failures - delayAfter
	if shift > 4 {
		shift = 4
	}
	return 100 * time.Millisecond * time.Duration(1<<shift), false
}

func loginKey(username, address string) string {
	return strings.ToLower(strings.TrimSpace(username)) + "\x00" + strings.TrimSpace(address)
}

var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._~+/=-]+`),
	regexp.MustCompile(`(?i)\bhf_[A-Za-z0-9_-]{8,}\b`),
	regexp.MustCompile(`(?i)\bsk-lcm-[A-Za-z0-9_-]{8,}\b`),
	regexp.MustCompile(`(?i)\b(?:lcm|llamarack)_(?:session|csrf|oidc_state)=[^;\s]+`),
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
