package main

import (
	"flag"
	"log"
	"net"
	"os"
	"strings"
	"time"
)

type document struct {
	Mac          string
	InstanceName string
	Hostname     string
	Txt          []string
}

func main() {
	var logFile = flag.String("o", "", "output file")
	flag.Parse()

	if len(*logFile) != 0 {
		f, err := os.OpenFile(*logFile, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0666)
		if err != nil {
			log.Printf("can't open %s", *logFile)
		}
		defer f.Close()
		log.SetOutput(f)
	}

	// choose a network interface
	ifaces, err := net.Interfaces()
	if err != nil {
		log.Fatal(err)
	}

	var ifAddr *net.IPNet
	var iface net.Interface
	for _, i := range ifaces {
		if ifAddr != nil {
			break
		}
		// skip virtual interface
		if strings.Contains(i.Name, "Virtual") {
			continue
		}
		if addrs, err := i.Addrs(); err == nil {
			for _, a := range addrs {
				if ipNet, ok := a.(*net.IPNet); ok {
					if ip4 := ipNet.IP.To4(); ip4 != nil &&
						!ip4.IsLinkLocalUnicast() &&
						!ip4.IsUnspecified() &&
						!ip4.IsLoopback() {
						iface = i
						ifAddr = ipNet
						break
					}
				}
			}
		}
	}

	if ifAddr == nil {
		log.Fatal("no valid v4 address")
	}
	log.Printf("Using network range %v for interface %s", ifAddr, iface.Name)

	// block
	hosts, err := arpsweep(iface, ifAddr)
	if err == nil {
		for k, v := range hosts {
			log.Printf("IP %s is at %v", k, net.HardwareAddr(v))
		}
	}

	// check dup macs
	macSet := make(map[string]bool)
	var dupMacs []net.HardwareAddr
	for _, mac := range hosts {
		if _, ok := macSet[mac.String()]; ok {
			dupMacs = append(dupMacs, mac)
		} else {
			macSet[mac.String()] = true
		}
	}
	if len(dupMacs) > 0 {
		log.Printf("dup mac: %v", dupMacs)
	}

	resolver, err := NewResolver(&iface)
	if err != nil {
		log.Fatal("Failed to initialize mdns resolver: ", err.Error())
	}

	chResult := make(chan *ServiceEntry)
	go resolver.Run(chResult)

	// send every 1s
	ticker := time.NewTicker(time.Second)
	go func() {
		for {
			select {
			case <-ticker.C:
				err = resolver.Browse(metaQuery, "local.", chResult)
				if err != nil {
					log.Println("Failed to browse:", err.Error())
				}
			}
		}
	}()

	go func() {
		u, err := NewUPNP(ifAddr)
		if err != nil {
			log.Printf("failed to init UPnP discovery")
			return
		}

		time.Sleep(10 * time.Second)

		u.hostsLock.Lock()
		log.Printf("------------found %d UPnP enabled hosts-----------", len(u.hosts))
		for k, v := range u.hosts {
			log.Printf("host: %s", k)
			for _, d := range v {
				log.Printf("device type: %s", d.DeviceType)
				log.Printf("location: %s", d.location)
				log.Printf("friendlyName: %s", d.FriendlyName)
				log.Printf("manufacturer: %s", d.Manufacturer)
				log.Printf("modelDescription: %s", d.ModelDescription)
				log.Printf("modelName: %s", d.ModelName)
				for _, s := range d.ServiceList {
					log.Printf("----service type: %s", s.ServiceType)
				}
				log.Printf("*********************************")
			}
			log.Printf("---------------------------------------")
		}
		u.hostsLock.Unlock()
	}()

	entries := make(map[string]*ServiceEntry)
	for {
		select {
		case r := <-chResult:
			if entry, ok := entries[r.ServiceInstanceName()]; !ok {
				if r.AddrIPv4 != nil {
					log.Printf("service: %s ipv4: %v ipv6: %v, port: %v, TTL: %d, TXT: %v hostname: %s",
						r.ServiceInstanceName(), r.AddrIPv4, r.AddrIPv6, r.Port, r.TTL, r.Text, r.HostName)
					if _, ok := hosts[r.AddrIPv4.String()]; !ok {
						if mac := sendARP(r.AddrIPv4); mac != nil {
							hosts[r.AddrIPv4.String()] = mac
							log.Printf("IP %s is at %v", r.AddrIPv4, mac)
						}
					}
				}
				entries[r.ServiceInstanceName()] = r
			} else {
				if entry.HostName != "" {
					// always trust newer address
					if addr := resolver.c.getIPv4AddrCache(entry.HostName); addr != nil {
						if entry.AddrIPv4 == nil {
							// note that entry is a pointer to struct, so we can modify the struct directly
							entry.AddrIPv4 = addr
							log.Printf("service: %s ipv4: %v ipv6: %v, port: %v, TTL: %d, TXT: %v hostname: %s",
								r.ServiceInstanceName(), r.AddrIPv4, r.AddrIPv6, r.Port, r.TTL, r.Text, r.HostName)
							if _, ok := hosts[addr.String()]; !ok {
								if mac := sendARP(addr); mac != nil {
									hosts[addr.String()] = mac
									log.Printf("IP %s is at %v", addr, mac)
								}
							}
						}
					}
					if addr := resolver.c.getIPv6AddrCache(entry.HostName); addr != nil {
						entry.AddrIPv6 = addr
					}
				}
			}
		}
	}
}
