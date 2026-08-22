package middleware

import "github.com/gin-gonic/gin"

// trustedProxyCIDRs returns the only network ranges whose forwarding
// headers the API believes.
//
// Gin's default is 0.0.0.0/0 + ::/0 — every caller is treated as a
// trusted proxy, so c.ClientIP() returns whatever the caller put in
// X-Forwarded-For. That turned the per-address rate limits into no
// limits at all: a script rotating the header lands in a fresh bucket
// on every request.
//
// The ranges below are the private, loopback and link-local blocks. They
// are deliberately broader than "the compose network" because a Docker
// network's subnet is assigned at creation time and changes whenever the
// stack is recreated — pinning the exact CIDR would silently break the
// client IP the day someone runs `docker compose down`. Every private
// range is safe to enumerate here for one reason: the API port is never
// published to the internet (see client/nginx.conf — the browser only
// ever talks to nginx), so the only peers that can reach this process
// are the reverse proxy and other containers on the same bridge. A
// request that arrives from a public address is, by construction, not
// one of our proxies, and its headers are ignored.
//
// The other half of the fix lives in client/nginx.conf, which now sets
// X-Forwarded-For to $remote_addr — replacing the client's value rather
// than appending to it — so the header the API trusts contains exactly
// one address that nginx observed itself.
func trustedProxyCIDRs() []string {
	return []string{
		"127.0.0.0/8",    // loopback, for a single-process deployment
		"::1/128",        // loopback (IPv6)
		"10.0.0.0/8",     // RFC1918
		"172.16.0.0/12",  // RFC1918 — Docker's default bridge pool
		"192.168.0.0/16", // RFC1918
		"169.254.0.0/16", // link-local
		"fc00::/7",       // unique local addresses (IPv6)
		"fe80::/10",      // link-local (IPv6)
	}
}

// ApplyTrustedProxies restricts which peers may set the client address.
//
// It must be called on the engine before any handler that reads
// c.ClientIP(); afterwards, a forwarding header is only honoured when
// the immediate peer is one of trustedProxyCIDRs.
func ApplyTrustedProxies(router *gin.Engine) error {
	return router.SetTrustedProxies(trustedProxyCIDRs())
}
