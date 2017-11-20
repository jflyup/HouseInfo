package main

import (
	"log"
	"net"
)

func probe() {
	go probeSamsung()
	go probeLenovo()
	go probeLenovo2()
}

func probeSamsung() {
	laddr, err := net.ResolveUDPAddr("udp", ":50007")
	if err != nil {
		log.Printf("failed to listen to udp 50007: %v", err)
		return
	}
	conn, err := net.ListenUDP("udp", laddr)
	if err != nil {
		log.Printf("can't listen to port 50007")
	}

	buf := make([]byte, 2048)
	for {
		if n, remoteAddr, err := conn.ReadFromUDP(buf); err != nil {
			break
		} else {
			response := string(buf[:n])
			log.Printf("50007 recv %s from %v", response, remoteAddr)
			return
			if conn, err := net.Dial("tcp", net.JoinHostPort(remoteAddr.IP.String(), "15000")); err != nil {
				log.Println("failed to connect to 15000")
			} else {
				log.Printf("connected to 15000")
				conn.Close()
			}
		}
	}
}

func probeLenovo() {
	laddr, err := net.ResolveUDPAddr("udp", ":55256")
	if err != nil {
		log.Printf("failed to listen to udp 55256: %v", err)
		return
	}
	conn, err := net.ListenUDP("udp", laddr)
	if err != nil {
		log.Printf("can't listen to port 55256")
	}

	buf := make([]byte, 2048)
	for {
		if n, remoteAddr, err := conn.ReadFromUDP(buf); err != nil {
			break
		} else {
			response := string(buf[:n])
			log.Printf("55256 recv %s from %v", response, remoteAddr)
			return
		}
	}
}

func probeLenovo2() {
	laddr, err := net.ResolveUDPAddr("udp", ":55255")
	if err != nil {
		log.Printf("failed to listen to udp 55255: %v", err)
		return
	}
	conn, err := net.ListenUDP("udp", laddr)
	if err != nil {
		log.Printf("can't listen to port 55255")
	}

	buf := make([]byte, 2048)
	for {
		if n, remoteAddr, err := conn.ReadFromUDP(buf); err != nil {
			break
		} else {
			response := string(buf[:n])
			log.Printf("55255 recv %s from %v", response, remoteAddr)
			return
		}
	}
}
