package security

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/brantje/llamarack/backend/internal/settings"
)

const (
	oidcClientTimeout         = 10 * time.Second
	oidcDialTimeout           = 5 * time.Second
	oidcTLSHandshakeTimeout   = 5 * time.Second
	oidcResponseHeaderTimeout = 5 * time.Second
	oidcMaxRedirects          = 10
)

var (
	ErrOIDCDestination = errors.New("OIDC destination is not allowed")
	ErrOIDCRedirect    = errors.New("OIDC redirect is not allowed")
	ErrOIDCHTTPS       = errors.New("OIDC URL must use HTTPS")
	ErrOIDCCredentials = errors.New("OIDC URL must not include credentials")
	errOIDCDestination = ErrOIDCDestination
	errOIDCRedirect    = ErrOIDCRedirect
	errOIDCHTTPS       = ErrOIDCHTTPS
	errOIDCCredentials = ErrOIDCCredentials
	cgnatRange         = netip.MustParsePrefix("100.64.0.0/10")
	sixToFourRange     = netip.MustParsePrefix("2002::/16")
	teredoRange        = netip.MustParsePrefix("2001::/32")
)

type ipResolver interface {
	LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
}

type contextDialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
}

type oidcOutboundPolicy struct {
	settings *settings.Service
	resolver ipResolver
	dialer   contextDialer
}

type oidcOutboundSnapshot struct {
	AllowHTTP    bool
	AllowedHosts []string
}

// NewOIDCClient returns an HTTP client for OIDC discovery, JWKS, and token
// exchange. Destination policy is read from manager settings on each request.
func NewOIDCClient(s *settings.Service) *http.Client {
	return newOIDCClient(&oidcOutboundPolicy{
		settings: s,
		resolver: net.DefaultResolver,
		dialer:   &net.Dialer{Timeout: oidcDialTimeout, KeepAlive: 30 * time.Second},
	})
}

func newOIDCClient(policy *oidcOutboundPolicy) *http.Client {
	if policy.resolver == nil {
		policy.resolver = net.DefaultResolver
	}
	if policy.dialer == nil {
		policy.dialer = &net.Dialer{Timeout: oidcDialTimeout, KeepAlive: 30 * time.Second}
	}
	base, _ := http.DefaultTransport.(*http.Transport)
	transport := base.Clone()
	transport.Proxy = nil
	transport.DialContext = policy.dialContext
	transport.TLSHandshakeTimeout = oidcTLSHandshakeTimeout
	transport.ResponseHeaderTimeout = oidcResponseHeaderTimeout
	return &http.Client{
		Transport:     transport,
		Timeout:       oidcClientTimeout,
		CheckRedirect: policy.checkRedirect,
	}
}

func (p *oidcOutboundPolicy) snapshot(ctx context.Context) (oidcOutboundSnapshot, error) {
	if p.settings == nil {
		return oidcOutboundSnapshot{}, errOIDCDestination
	}
	allowHTTP, err := p.settings.Bool(ctx, settings.OIDCAllowHTTP)
	if err != nil {
		return oidcOutboundSnapshot{}, err
	}
	raw, err := p.settings.String(ctx, settings.OIDCAllowedHosts)
	if err != nil {
		return oidcOutboundSnapshot{}, err
	}
	return oidcOutboundSnapshot{AllowHTTP: allowHTTP, AllowedHosts: ParseOIDCAllowedHosts(raw)}, nil
}

func ParseOIDCAllowedHosts(value string) []string {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\t' || r == '\n'
	})
	out := make([]string, 0, len(fields))
	seen := map[string]bool{}
	for _, field := range fields {
		host := normalizeOIDCHost(field)
		if host == "" || seen[host] {
			continue
		}
		seen[host] = true
		out = append(out, host)
	}
	return out
}

func normalizeOIDCHost(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	value = strings.TrimPrefix(value, "[")
	value = strings.TrimSuffix(value, "]")
	return strings.ToLower(strings.TrimSpace(value))
}

func HostAllowed(host string, allowed []string) bool {
	host = normalizeOIDCHost(host)
	if host == "" {
		return false
	}
	for _, candidate := range allowed {
		if host == candidate {
			return true
		}
	}
	return false
}

func BlockedOIDCAddr(ip netip.Addr) bool {
	if !ip.IsValid() {
		return true
	}
	ip = ip.Unmap()
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() || ip.IsPrivate() {
		return true
	}
	return cgnatRange.Contains(ip) || sixToFourRange.Contains(ip) || teredoRange.Contains(ip)
}

func ValidateOIDCURL(raw string, allowHTTP bool) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || parsed.Opaque != "" {
		return nil, errors.New("OIDC URL must be an absolute HTTP(S) URL")
	}
	if parsed.User != nil {
		return nil, errOIDCCredentials
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return nil, errors.New("OIDC URL must be an absolute HTTP(S) URL")
	}
	if parsed.Scheme != "https" && !allowHTTP {
		return nil, errOIDCHTTPS
	}
	hostname := parsed.Hostname()
	if hostname == "" {
		return nil, errors.New("OIDC URL must be an absolute HTTP(S) URL")
	}
	return parsed, nil
}

func SameOrigin(left, right *url.URL) bool {
	if left == nil || right == nil {
		return false
	}
	return strings.EqualFold(left.Scheme, right.Scheme) &&
		strings.EqualFold(left.Hostname(), right.Hostname()) &&
		effectiveURLPort(left) == effectiveURLPort(right)
}

func effectiveURLPort(u *url.URL) string {
	if port := u.Port(); port != "" {
		return port
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		return "443"
	case "http":
		return "80"
	default:
		return ""
	}
}

func OIDCEndpointHostTrusted(endpoint, issuer *url.URL, allowed []string, discovered bool) bool {
	if endpoint == nil || issuer == nil {
		return false
	}
	if discovered || SameOrigin(endpoint, issuer) {
		return true
	}
	return HostAllowed(endpoint.Hostname(), allowed)
}

func (p *oidcOutboundPolicy) validateRequestURL(ctx context.Context, target *url.URL) error {
	snap, err := p.snapshot(ctx)
	if err != nil {
		return err
	}
	parsed, err := ValidateOIDCURL(target.String(), snap.AllowHTTP)
	if err != nil {
		return err
	}
	if ip, parseErr := netip.ParseAddr(parsed.Hostname()); parseErr == nil && BlockedOIDCAddr(ip) && !HostAllowed(parsed.Hostname(), snap.AllowedHosts) {
		return errOIDCDestination
	}
	return nil
}

func (p *oidcOutboundPolicy) checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= oidcMaxRedirects {
		return errors.New("too many OIDC redirects")
	}
	if err := p.validateRequestURL(req.Context(), req.URL); err != nil {
		return err
	}
	if len(via) == 0 {
		return errOIDCRedirect
	}
	if !SameOrigin(req.URL, via[0].URL) {
		return fmt.Errorf("%w: %s", errOIDCRedirect, req.URL.Redacted())
	}
	return nil
}

func (p *oidcOutboundPolicy) dialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	if network != "tcp" && network != "tcp4" && network != "tcp6" {
		return nil, errOIDCDestination
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	dialAddr, err := p.pickDialAddr(ctx, host, port)
	if err != nil {
		return nil, err
	}
	return p.dialer.DialContext(ctx, network, dialAddr)
}

func (p *oidcOutboundPolicy) pickDialAddr(ctx context.Context, host, port string) (string, error) {
	snap, err := p.snapshot(ctx)
	if err != nil {
		return "", err
	}
	host = strings.Trim(host, "[]")
	if ip, parseErr := netip.ParseAddr(host); parseErr == nil {
		if err := allowOIDCIP(ip, host, snap.AllowedHosts); err != nil {
			return "", err
		}
		return net.JoinHostPort(ip.Unmap().String(), port), nil
	}
	resolved, err := p.resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return "", err
	}
	var blocked error
	for _, address := range resolved {
		ip, ok := netip.AddrFromSlice(address.IP)
		if !ok {
			continue
		}
		if err := allowOIDCIP(ip, host, snap.AllowedHosts); err != nil {
			if blocked == nil {
				blocked = err
			}
			continue
		}
		return net.JoinHostPort(ip.Unmap().String(), port), nil
	}
	if blocked != nil {
		return "", blocked
	}
	return "", errOIDCDestination
}

func allowOIDCIP(ip netip.Addr, hostname string, allowed []string) error {
	if !ip.IsValid() {
		return errOIDCDestination
	}
	ip = ip.Unmap()
	if !BlockedOIDCAddr(ip) {
		return nil
	}
	if HostAllowed(hostname, allowed) {
		return nil
	}
	return errOIDCDestination
}
