// +build linux darwin

package main

import (
	"bytes"
	"encoding/binary"
	"log"
	"net"

	"sync"
	"sync/atomic"
	"time"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/google/gopacket/pcap"
)

var liveHosts map[string]net.HardwareAddr
var mutex = &sync.Mutex{}

var stopped int32

func arpsweep(iface net.Interface, ifAddr *net.IPNet) (map[string]net.HardwareAddr, error) {
	// Open up a pcap handle for packet reads/writes.
	handle, err := pcap.OpenLive(iface.Name, 65536, true, pcap.BlockForever)
	if err != nil {
		return nil, err
	}
	defer handle.Close()

	liveHosts = make(map[string]net.HardwareAddr)
	// Start up a goroutine to read in packet data.
	stop := make(chan struct{})
	go readARP(handle, iface.HardwareAddr, stop, ifAddr)

	writeARP(handle, iface.HardwareAddr, ifAddr)
	// exit after scanning for a while
	// timer := time.NewTimer(time.Second * time.Duration(10))
	//<-timer.C
	close(stop)

	return liveHosts, nil
}

// readARP loops until 'stop' is closed.
func readARP(handle *pcap.Handle, mac net.HardwareAddr, stop chan struct{}, ifAddr *net.IPNet) {
	src := gopacket.NewPacketSource(handle, layers.LayerTypeEthernet)
	in := src.Packets()
	for {
		var packet gopacket.Packet
		select {
		case <-stop:
			return
		case packet = <-in:
			arpLayer := packet.Layer(layers.LayerTypeARP)
			if arpLayer == nil {
				continue
			}
			arp := arpLayer.(*layers.ARP)
			if bytes.Equal(mac, arp.SourceHwAddress) {
				// This is a packet we sent.
				continue
			} else {
				// if we got broadcast arp request, consider the source host is alive
				// or got some packets here that aren't responses to ones we've sent,
				// all information is good information :)
				srcIP := net.IP(arp.SourceProtAddress)
				if ifAddr.Contains(srcIP) {
					mutex.Lock()
					// always use new address
					liveHosts[srcIP.String()] = arp.SourceHwAddress
					mutex.Unlock()
				}
			}
		}
	}
}

// writeARP writes an ARP request for each address on our local network to the
// pcap handle.
func writeARP(handle *pcap.Handle, mac net.HardwareAddr, ifAddr *net.IPNet) error {
	// Set up all the layers' fields we can.
	eth := layers.Ethernet{
		SrcMAC:       mac,
		DstMAC:       net.HardwareAddr{0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
		EthernetType: layers.EthernetTypeARP,
	}
	arp := layers.ARP{
		AddrType:          layers.LinkTypeEthernet,
		Protocol:          layers.EthernetTypeIPv4,
		HwAddressSize:     6,
		ProtAddressSize:   4,
		Operation:         layers.ARPRequest,
		SourceHwAddress:   mac,
		SourceProtAddress: []byte(ifAddr.IP.To4()),
		DstHwAddress:      []byte{0, 0, 0, 0, 0, 0},
	}
	// Set up buffer and options for serialization.
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
	}

	//if len(*target) > 1 {
	//	ip := net.ParseIP(*target)
	//	arp.DstProtAddress = ip.To4()
	//	gopacket.SerializeLayers(buf, opts, &eth, &arp)
	//	// Ethernet requires that all packets be at least 60 bytes long,
	//	// 64 bytes if you include the Frame Check Sequence at the end
	//	if err := handle.WritePacketData(buf.Bytes()); err != nil {
	//		return err
	//	}

	// 3 round
	for i := 0; i < 3; i++ {
		// Send one packet for every address.
		for _, ip := range ips(ifAddr) {
			mutex.Lock()
			if _, ok := liveHosts[ip.String()]; !ok && ip.String() != ifAddr.IP.String() {
				if atomic.LoadInt32(&stopped) == 0 {
					arp.DstProtAddress = []byte(ip)
					// SerializeLayers clears the given write buffer
					if err := gopacket.SerializeLayers(buf, opts, &eth, &arp); err != nil {
						log.Println(err)
						return err
					} else {
						if err := handle.WritePacketData(buf.Bytes()); err != nil {
							log.Println(err)
							return err
						}
					}
				} else {
					return nil
				}
			}
			mutex.Unlock()

			time.Sleep(time.Millisecond * time.Duration(10))
		}
		time.Sleep(time.Second * time.Duration(1))
	}

	return nil
}

// ips is a simple and not very good method for getting all IPv4 addresses from a
// net.IPNet.  It returns all IPs it can over the channel it sends back, closing
// the channel when done.
func ips(n *net.IPNet) (out []net.IP) {
	num := binary.BigEndian.Uint32([]byte(n.IP.To4()))
	mask := binary.BigEndian.Uint32([]byte(n.Mask))
	num &= mask
	for mask < 0xffffffff {
		var buf [4]byte
		binary.BigEndian.PutUint32(buf[:], num)
		out = append(out, net.IP(buf[:]))
		mask++
		num++
	}
	return
}
