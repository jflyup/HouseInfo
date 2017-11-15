package main

import (
	"net"
	"sync"
	"time"
)

var upHosts map[string]net.HardwareAddr
var mutex = &sync.Mutex{}

func arpsweep(iface net.Interface, ifAddr *net.IPNet) (map[string]net.HardwareAddr, error) {
	upHosts = make(map[string]net.HardwareAddr)
	upHosts[ifAddr.IP.String()] = iface.HardwareAddr
	var wg sync.WaitGroup
	for _, ip := range ips(ifAddr) {
		mutex.Lock()
		if _, ok := upHosts[ip.String()]; !ok && ip.String() != ifAddr.IP.String() {
			wg.Add(1)
			go func(ip net.IP) {
				defer wg.Done()
				if hwAddr := sendARP(ip); hwAddr != nil {
					mutex.Lock()
					upHosts[ip.String()] = hwAddr
					mutex.Unlock()
				}
			}(ip)
		}
		mutex.Unlock()
		time.Sleep(time.Millisecond * 200)

	}

	wg.Wait()

	return upHosts, nil
}
