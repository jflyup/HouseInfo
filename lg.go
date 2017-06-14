package main

import (
	"encoding/hex"
	"log"
	"net"
	"time"
)

var pkt, _ = hex.DecodeString("132a9eab010000002830323a30303a30303a30303a30303a3030000000085f6c675f726f617000000007416e64726f6964")

func discoverLG() {
	conn, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP("255.255.255.255"), Port: 9740})
	if err != nil {
		return
	}

	conn.Write(pkt)
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 1024)
	for {
		if n, addr, err := conn.ReadFrom(buf); err == nil && addr != nil && n > 0 {
			log.Printf("find lg TV from %v: %s", addr, string(buf[:n]))
		}
	}

}
