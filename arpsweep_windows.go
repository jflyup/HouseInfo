package main

import (
	"encoding/binary"
	"net"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var (
	iphlp, _ = syscall.LoadLibrary("iphlpapi.dll")
	// SendARP is a Windows API
	SendARP, _ = syscall.GetProcAddress(iphlp, "SendARP")
)

// only work on Windows with go 1.8
func sendARP(dst net.IP) net.HardwareAddr {
	var nargs uintptr = 4
	var len uint64 = 6
	mac := []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
	d := binary.LittleEndian.Uint32(dst.To4())

	// SendARP will send 3 ARP requests if no reply received on Windows 7
	ret, _, callErr := syscall.Syscall6(
		uintptr(SendARP), nargs,
		uintptr(d),
		0,
		uintptr(unsafe.Pointer(&mac[0])),
		uintptr(unsafe.Pointer(&len)),
		0,
		0)

	if callErr == 0 && ret == 0 {
		return net.HardwareAddr(mac)
	}

	return nil
}

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
