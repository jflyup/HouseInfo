package main

import (
	"encoding/binary"
	"errors"
	"net"
	"strings"
)

var (
	metaQuery = "_services._dns-sd._udp."
)

// trimDot removes all leading and trailing dots
func trimDot(s string) string {
	return strings.Trim(s, ".")
}

// becasue service instance name string may contain escaped dot, so we can't simply
// call strings.Split(name, ".")
func parseServiceName(name string) (instance, serviceType, domain string, err error) {
	s := trimDot(name)
	l := len(s)
	var ss []string
	for {
		pos := strings.LastIndex(s[:l], ".")
		if pos != -1 {
			ss = append(ss, s[pos+1:l])
			l = pos
			if len(ss) >= 3 {
				// done
				domain = ss[0]
				serviceType = ss[2] + "." + ss[1]
				instance = s[:l]
				break
			}
		} else {
			err = errors.New("illegal service instance")
			break
		}
	}

	return
}

// extractIPv4 extract IPv4 from the reversed octets
// with special suffix in-addr.arpa (such as 4.3.2.1.in-addr.arpa)
func extractIPv4(ptr string) string {
	s := strings.Replace(ptr, ".in-addr.arpa", "", 1)
	words := strings.Split(s, ".")
	for i, j := 0, len(words)-1; i < j; i, j = i+1, j-1 {
		words[i], words[j] = words[j], words[i]
	}
	return strings.Join(words, ".")
}

// ips is a simple and not very good method for getting all IPv4 addresses from a
// net.IPNet.  It returns all IPs it can over the channel it sends back, closing
// the channel when done.
func ips(n *net.IPNet) (out []net.IP) {
	num := binary.BigEndian.Uint32([]byte(n.IP.To4()))
	mask := binary.BigEndian.Uint32([]byte(n.Mask))
	num &= mask
	for mask < 0xffffffff {
		var buf [4]byte
		binary.BigEndian.PutUint32(buf[:], num)
		out = append(out, net.IP(buf[:]))
		mask++
		num++
	}
	return
}
