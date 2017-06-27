package main

import (
	"errors"
	"strings"
)

var (
	tvServices = []string{
		"_anymote._tcp",
		"_androidtvremote._tcp",
		"_airplay._tcp",
		"_googlecast._tcp",
		"_amzn-wplay._tcp",
		"_rc._tcp",
		"_lg_dtv_wifirc._tcp",
		"_gamecenter._tcp",
		"_homekit._tcp",
		"_apple-mobdev2._tcp",
	}

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
