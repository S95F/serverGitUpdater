// Package portscan picks a TCP port that's free to bind right now and
// not in a list of common-reservations the operator probably wants to
// keep available.
package portscan

import (
	"fmt"
	"math/rand/v2"
	"net"
	"os"
	"strconv"
	"strings"
)

// CommonReserved is a small allowlist of well-known ports we refuse to
// suggest even when nothing's currently bound to them, so we don't
// accidentally steal :80, :443, :22, etc. The value is a label used in
// the API response so the UI can explain what was avoided.
var CommonReserved = map[int]string{
	21:    "ftp",
	22:    "ssh",
	23:    "telnet",
	25:    "smtp",
	53:    "dns",
	67:    "dhcp",
	68:    "dhcp",
	80:    "http",
	110:   "pop3",
	123:   "ntp",
	143:   "imap",
	443:   "https",
	465:   "smtps",
	587:   "smtp-submission",
	631:   "ipp",
	993:   "imaps",
	995:   "pop3s",
	1080:  "socks",
	1194:  "openvpn",
	1433:  "mssql",
	1521:  "oracle",
	2049:  "nfs",
	2375:  "docker",
	2376:  "docker-tls",
	3000:  "common-http-dev",
	3306:  "mysql",
	3389:  "rdp",
	4369:  "epmd",
	5000:  "common-http-dev",
	5060:  "sip",
	5432:  "postgres",
	5672:  "amqp",
	5984:  "couchdb",
	6379:  "redis",
	6443:  "kubernetes",
	8000:  "common-http-dev",
	8080:  "http-alt",
	8443:  "https-alt",
	8888:  "common-http-dev",
	9000:  "common",
	9090:  "common",
	9092:  "kafka",
	9200:  "elasticsearch",
	9300:  "elasticsearch",
	11211: "memcached",
	15672: "rabbitmq-mgmt",
	27017: "mongodb",
}

// EphemeralRange returns the kernel's local TCP port range used for
// outbound client connections. Suggestions inside this range can collide
// with future ephemeral sockets, so we exclude them. Falls back to the
// Linux default when /proc isn't readable (e.g. running on macOS for
// dev).
func EphemeralRange() (lo, hi int) {
	lo, hi = 32768, 60999
	data, err := os.ReadFile("/proc/sys/net/ipv4/ip_local_port_range")
	if err != nil {
		return
	}
	parts := strings.Fields(string(data))
	if len(parts) >= 2 {
		if a, err := strconv.Atoi(parts[0]); err == nil {
			lo = a
		}
		if b, err := strconv.Atoi(parts[1]); err == nil {
			hi = b
		}
	}
	return
}

// IsBindable opens and closes a transient listener on the given TCP
// port (any interface). Returns true if the bind succeeded, false if
// the kernel refused (most often because something else is bound). The
// brief race window between this check and the caller actually using
// the port is acceptable for a "suggest a port" UX.
func IsBindable(port int) bool {
	l, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return false
	}
	_ = l.Close()
	return true
}

// Options drives a Suggest call.
type Options struct {
	Min   int            // inclusive; 0 → 8001
	Max   int            // inclusive; 0 → ephemeralLow-1
	Avoid map[int]string // explicit ports to skip (e.g. other apps' ports). Value = label.
	Tries int            // attempts before giving up; 0 → 200
}

// Suggest returns a random port in [Min,Max] that is not in Avoid, not
// in CommonReserved, not in the kernel's ephemeral range, and is
// currently bindable.
func Suggest(opts Options) (int, error) {
	elo, ehi := EphemeralRange()
	if opts.Min <= 0 {
		opts.Min = 8001
	}
	if opts.Max <= 0 {
		opts.Max = elo - 1
	}
	if opts.Max < opts.Min {
		return 0, fmt.Errorf("port range invalid: [%d,%d]", opts.Min, opts.Max)
	}
	if opts.Tries <= 0 {
		opts.Tries = 200
	}
	for i := 0; i < opts.Tries; i++ {
		p := opts.Min + rand.IntN(opts.Max-opts.Min+1)
		if _, taken := opts.Avoid[p]; taken {
			continue
		}
		if _, taken := CommonReserved[p]; taken {
			continue
		}
		if p >= elo && p <= ehi {
			continue
		}
		if !IsBindable(p) {
			continue
		}
		return p, nil
	}
	return 0, fmt.Errorf("no free port found in [%d,%d] after %d tries", opts.Min, opts.Max, opts.Tries)
}
