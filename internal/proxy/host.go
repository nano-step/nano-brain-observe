package proxy

import (
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

// ParseAllowedHosts validates additional exact hostnames/IP literals (never ports).
func ParseAllowedHosts(raw string) ([]string, error) {
	if raw == "" {
		return nil, nil
	}
	var hosts []string
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if ip, err := netip.ParseAddr(part); err == nil && ip.Is6() && ip.Zone() == "" {
			hosts = append(hosts, ip.String())
			continue
		}
		host, port, ok := authority(part)
		if !ok || port != "" {
			return nil, fmt.Errorf("invalid allowed host %q", part)
		}
		hosts = append(hosts, host)
	}
	return hosts, nil
}

// authority returns a canonical host and optional port without DNS resolution.
func authority(raw string) (string, string, bool) {
	if raw == "" || strings.ContainsAny(raw, " \t\r\n/@\\?#%") {
		return "", "", false
	}
	host, port := raw, ""
	if strings.HasPrefix(raw, "[") {
		end := strings.IndexByte(raw, ']')
		if end < 0 || (end+1 < len(raw) && raw[end+1] != ':') {
			return "", "", false
		}
		host = raw[1:end]
		if end+1 < len(raw) {
			port = raw[end+2:]
			if port == "" {
				return "", "", false
			}
		}
		ip, err := netip.ParseAddr(host)
		if err != nil || !ip.Is6() || ip.Zone() != "" {
			return "", "", false
		}
		host = ip.String()
	} else {
		if strings.Count(raw, ":") > 1 {
			return "", "", false
		}
		if strings.Contains(raw, ":") {
			var ok bool
			host, port, ok = strings.Cut(raw, ":")
			if !ok || port == "" {
				return "", "", false
			}
		}
		if ip, err := netip.ParseAddr(host); err == nil {
			if !ip.Is4() {
				return "", "", false
			}
			host = ip.String()
		} else {
			if len(host) == 0 || len(host) > 253 || strings.HasSuffix(host, ".") {
				return "", "", false
			}
			if strings.Contains(host, ".") && strings.IndexFunc(host, func(ch rune) bool {
				return ch != '.' && (ch < '0' || ch > '9')
			}) == -1 {
				return "", "", false
			}
			for _, label := range strings.Split(host, ".") {
				if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
					return "", "", false
				}
				for _, ch := range label {
					if !('a' <= ch && ch <= 'z' || 'A' <= ch && ch <= 'Z' || '0' <= ch && ch <= '9' || ch == '-') {
						return "", "", false
					}
				}
			}
			host = strings.ToLower(host)
		}
	}
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port {
			return "", "", false
		}
	}
	return host, port, true
}

func allowedRequest(r *http.Request, hosts []string) bool {
	host, port, ok := authority(r.Host)
	if !ok {
		return false
	}
	allowed := host == "localhost"
	if ip, err := netip.ParseAddr(host); err == nil && ip.IsLoopback() {
		allowed = true
	}
	for _, h := range hosts {
		if h == host {
			allowed = true
		}
	}
	if !allowed {
		return false
	}
	origins := r.Header.Values("Origin")
	if len(origins) == 0 {
		return true
	}
	if len(origins) != 1 {
		return false
	}
	if strings.ContainsAny(origins[0], "% \t\r\n\\,") {
		return false
	}
	origin, err := url.Parse(origins[0])
	if err != nil || origin.Scheme != "http" && origin.Scheme != "https" ||
		origin.Opaque != "" || origin.User != nil || origin.Path != "" ||
		origin.RawQuery != "" || origin.ForceQuery || origin.Fragment != "" || origin.RawFragment != "" {
		return false
	}
	originHost, originPort, ok := authority(origin.Host)
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return ok && origin.Scheme == scheme && originHost == host && originPort == port
}
