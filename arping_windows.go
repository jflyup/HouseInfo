package main

import (
	"net"
	"syscall"
	"unsafe"
)

var (
	iphlp, _ = syscall.LoadLibrary("iphlpapi.dll")
	// SendARP is Windows API
	SendARP, _ = syscall.GetProcAddress(iphlp, "SendARP")
)

// only work on Windows with go 1.8
func sendARP(dst net.IP, mac chan struct {
	mac net.HardwareAddr
	ip  net.IP
}) {
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
		mac <- struct {
			mac net.HardwareAddr
			ip  net.IP
		}{
			net.HardwareAddr(mac),
			dst,
		}
	}
}
