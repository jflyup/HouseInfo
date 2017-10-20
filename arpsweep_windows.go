package main

import (
	"net"
	"time"
)

func arpsweep() {
	ifaces, err := net.Interfaces()
	if err != nil {
		panic(err)
	}

	var wg sync.WaitGroup
	for _, iface := range ifaces {
		log.Printf("interface: %s", iface.Name)
		wg.Add(1)
		// Start up a scan on each interface.
		go func(iface net.Interface) {
			defer wg.Done()
			if err := scan(&iface); err != nil {
				log.Printf("interface %v: %v", iface.Name, err)
			}
		}(iface)
	}
}

func scan(iface *net.Interface) error {
	// We just look for IPv4 addresses, so try to find if the interface has one.
	var addr *net.IPNet
	if addrs, err := iface.Addrs(); err != nil {
		return err
	} else {
		for _, a := range addrs {
			if ipnet, ok := a.(*net.IPNet); ok {
				if ip4 := ipnet.IP.To4(); ip4 != nil {
					addr = &net.IPNet{
						IP:   ip4,
						Mask: ipnet.Mask[len(ipnet.Mask)-4:],
					}
					break
				}
			}
		}
	}

	if addr == nil {
		return errors.New("no good IP network found")
	} else if addr.IP[0] == 127 || addr.IP[0] == 169 {
		return errors.New("not connected")
	} else if addr.Mask[0] != 0xff || addr.Mask[1] != 0xff {
		return errors.New("network is too large")
	}

	chMacAddr := make(chan struct {
		mac net.HardwareAddr
		ip  net.IP
	})

	// Send one packet for every address.
	for _, ip := range ips(addr) {
		go sendARP(ip, chMacAddr)
		time.Sleep(time.Millisecond * 100)
	}

	for {
		select {
		case result := <-chMacAddr:
			log.Println(result)
		}
	}
}

// ips is a simple and not very good method for getting all IPv4 addresses from a
// net.IPNet.  It returns all IPs it can over the channel it sends back, closing
// the channel when done.
func ips(n *net.IPNet) (out []net.IP) {
	num := binary.BigEndian.Uint32([]byte(n.IP))
	mask := binary.BigEndian.Uint32([]byte(n.Mask))
	num &= mask
	for mask < 0xffffffff {
		var buf [4]byte
		binary.BigEndian.PutUint32(buf[:], num)
		out = append(out, net.IP(buf[:]))
		mask += 1
		num += 1
	}
	return
}
