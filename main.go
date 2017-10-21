package main

import (
	"log"
	"os"

	"flag"
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

	log.Printf("start scanning:")

	hosts := arpsweep()
	log.Println("%v", hosts)

	resolver, err := NewResolver(nil)

	if err != nil {
		log.Println("Failed to initialize resolver:", err.Error())
		os.Exit(1)
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
		u, err := NewUPNP()
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
				log.Printf("url base: %s", d.urlBase)
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
					// alway trust newer address because of expired cache
					if addr := resolver.c.getIPv4AddrCache(entry.HostName); addr != nil {
						if entry.AddrIPv4 == nil {
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
						// note that entry is a pointer to struct, so we can modify the struct directly
					}
					if addr := resolver.c.getIPv6AddrCache(entry.HostName); addr != nil {
						entry.AddrIPv6 = addr
					}
				}
			}
		}
	}
}
