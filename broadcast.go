package main

import (
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"time"
)

var (
	lgPkt, _      = hex.DecodeString("132a9eab010000002830323a30303a30303a30303a30303a3030000000085f6c675f726f617000000007416e64726f6964")
	hisensePkt, _ = hex.DecodeString("444953434f5645525923")
	tclPkt, _     = hex.DecodeString("313a313439363830383838353337353a54434c5f50484f4e453a50484f4e453a313a54434c5f50484f4e450000")
)

func broadcastDiscovery() {
	// LG
	go func() {
		conn, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP("255.255.255.255"), Port: 9740})
		if err != nil {
			return
		}
		for i := 0; i < 3; i++ {
			conn.Write(lgPkt)
			conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			buf := make([]byte, 1024)
			for {
				if n, addr, err := conn.ReadFrom(buf); err == nil {
					if addr != nil && n > 0 {
						log.Printf("find lg TV from %v: %s", addr, string(buf[:n]))
					}
				} else {
					if e, ok := err.(net.Error); ok && e.Timeout() {
						break
					}
				}
			}
		}
	}()

	// TCL
	go func() {
		conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("0.0.0.0"), Port: 6537})
		if err != nil {
			return
		}
		for i := 0; i < 3; i++ {
			millis := time.Now().UnixNano() / (int64(time.Millisecond) / int64(time.Nanosecond))
			timeBytes := []byte(fmt.Sprintf("%d", millis))
			for idx, v := range timeBytes {
				tclPkt[2+idx] = v
			}
			conn.WriteToUDP(tclPkt, &net.UDPAddr{IP: net.ParseIP("255.255.255.255"), Port: 6537})
			conn.SetReadDeadline(time.Now().Add(3 * time.Second))
			buf := make([]byte, 1024)
			for {
				if n, addr, err := conn.ReadFrom(buf); err == nil {
					if addr != nil && n != len(tclPkt) {
						log.Printf("find TCL TV from %v: %s", addr, string(buf[:n]))
					}
				} else {
					if e, ok := err.(net.Error); ok && e.Timeout() {
						break
					}
				}
			}
		}
	}()

	// Hisense
	conn, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP("255.255.255.255"), Port: 50000})
	if err != nil {
		return
	}
	for i := 0; i < 3; i++ {
		conn.Write(hisensePkt)
		conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		buf := make([]byte, 1024)
		for {
			if n, addr, err := conn.ReadFrom(buf); err == nil {
				if addr != nil && n > 0 {
					log.Printf("find Hisense TV from %v: %s", addr, string(buf[:n]))
				}
			} else {
				if e, ok := err.(net.Error); ok && e.Timeout() {
					break
				}
			}
		}
	}

}
