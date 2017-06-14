package main

import (
	"log"
	"os"

	"flag"
	"time"
)

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

	resolver, err := NewResolver(nil)

	if err != nil {
		log.Println("Failed to initialize resolver:", err.Error())
		os.Exit(1)
	}

	chResult := make(chan *ServiceEntry)
	go resolver.Run(chResult)

	// send every 500ms
	ticker := time.NewTicker(time.Millisecond * 500)
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

	t := time.NewTicker(time.Second)
	go func() {
		for {
			select {
			case <-t.C:
				for _, s := range tvServices {
					err = resolver.Browse(s, "local", chResult)
					if err != nil {
						log.Println("Failed to browse:", err.Error())
					}
					time.Sleep(time.Millisecond * 500)
				}

			}
		}
	}()

	go discoverLG()

	go func() {
		u, err := NewUPNP()
		if err != nil {
			log.Printf("failed to discover UPnP devices")
			return
		}

		time.Sleep(20 * time.Second)

		log.Printf("------------found %d hosts-----------", len(u.hosts))
		for k, v := range u.hosts {
			log.Printf("host: %s", k)
			for _, d := range v {
				log.Printf("device type: %s", d.ST)
				log.Printf("url base: %s", d.urlBase)
				log.Printf("possible remote control port: %v", d.openPorts)
				log.Printf("friendlyName: %s", d.FriendlyName)
				log.Printf("manufacturer: %s", d.Manufacturer)
				log.Printf("modelDescription: %s", d.ModelDescription)
				log.Printf("modelName: %s", d.ModelName)
				for _, s := range d.ServiceList {
					log.Printf("----service type: %s", s.ServiceType)
					log.Printf("--------action list: %v", s.actions)
					if s.ServiceType == "urn:schemas-upnp-org:service:ConnectionManager:1" {
						log.Printf("getProtocolInfo: source:%v, sink:%v", s.sourceProto, s.sinkProto)
					}
				}
				log.Printf("*********************************")
			}
			log.Printf("---------------------------------------")
		}
	}()

	entries := make(map[string]*ServiceEntry)
	for {
		select {
		case r := <-chResult:
			if entry, ok := entries[r.ServiceInstanceName()]; !ok {
				log.Printf("service: %s ipv4: %v ipv6: %v, port: %v, TTL: %d, TXT: %v hostname: %s",
					r.ServiceInstanceName(), r.AddrIPv4, r.AddrIPv6, r.Port, r.TTL, r.Text, r.HostName)
				entries[r.ServiceInstanceName()] = r
			} else {
				if entry.HostName != "" {
					// alway trust newer address because of expired cache
					if addr := resolver.c.getIPv4AddrCache(entry.HostName); addr != nil {
						// note that entry is a pointer, so we can modify the struct directly
						entry.AddrIPv4 = addr
					}
					if addr := resolver.c.getIPv4AddrCache(entry.HostName); addr != nil {
						entry.AddrIPv6 = addr
					}
				}
			}
		}
	}
}
