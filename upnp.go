package main

import (
	"encoding/xml"
	"errors"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	serviceTypes = []string{
		"urn:samsung.com:device:RemoteControlReceiver:1", // samsung TV
		"urn:schemas-sony-com:service:IRCC:1",            // sony TV
		"urn:schemas-sony-com:service:ScalarWebAPI:1",    // sony TV
		"urn:panasonic-com:device:p00RemoteController:1", // panasonic TV
		"urn:roku-com:service:ecp:1",                     // roku TV

		// common service type
		"urn:dial-multiscreen-org:service:dial:1",
		"urn:schemas-upnp-org:device:MediaRenderer:1",
		"urn:schemas-upnp-org:device:MediaServer:1",
	}
	remoteControlPorts = []uint16{
		80,    // Sony
		8060,  // Roku
		1925,  // Philips
		55000, // Samsung, Panasonic
		8080,  // LG
		10002, // Sharp
	}

	mediaTypes = []string{
		"audio",
		"video",
		"image",
	}
	// TODO IPv6 link-local multicast
	upnpAddr = &net.UDPAddr{
		IP:   net.ParseIP("239.255.255.250"),
		Port: 1900,
	}
)

const (
	multicastRetryCount      = 3
	multicastWaitTimeSeconds = 3
)

type UPNP struct {
	devices map[string]*device
}

// device Description xml elements
type service struct {
	urlBase     string
	ServiceType string `xml:"serviceType"`
	//ServiceId   string `xml:"serviceId"`
	SCPDURL        string `xml:"SCPDURL"`
	ControlURL     string `xml:"controlURL"`
	actions        []string
	supportedMedia []string
	//EventSubURL string `xml:"eventSubURL"`
}

type actionList struct {
	ActionName []string `xml:"action>name"`
}

type device struct {
	// Export (Capitalize) struct fields
	DeviceType       string `xml:"deviceType"`
	FriendlyName     string `xml:"friendlyName"`
	Manufacturer     string `xml:"manufacturer"`
	ManufacturerURL  string `xml:"manufacturerURL"`
	ModelDescription string `xml:"modelDescription"`
	ModelName        string `xml:"modelName"`
	Host             string
	urlBase          string
	location         string
	ST               string
	USN              string
	ipAddr           string
	openPorts        []int
	mu               sync.Mutex
	ServiceList      []*service `xml:"serviceList>service"`
}

// NewUPNP returns a new UPNP object with a populated device object.
func NewUPNP() (*UPNP, error) {
	u := &UPNP{
		devices: make(map[string]*device),
	}

	for _, st := range serviceTypes {
		go u.findDevice(st)
	}

	return u, nil
}

func (s *service) getActionList() error {
	header := http.Header{}
	header.Set("Host", strings.Split(strings.Split(s.urlBase, "//")[1], "/")[0])
	header.Set("Connection", "keep-alive")

	// different implementations return SCPDURL with "/" or not
	request, _ := http.NewRequest("GET", s.urlBase+strings.TrimPrefix(s.SCPDURL, "/"), nil)
	request.Header = header

	response, err := http.DefaultClient.Do(request)

	if response == nil || err != nil || response.StatusCode != 200 {
		log.Printf("can't get SCPD xml: %v", err)
		return err
	}

	decoder := xml.NewDecoder(response.Body)
	for t, err := decoder.Token(); err == nil; t, err = decoder.Token() {
		switch se := t.(type) {
		case xml.StartElement:
			if se.Name.Local == "actionList" {
				var al actionList
				if err := decoder.DecodeElement(&al, &se); err != nil {
					log.Printf("bad xml: %v", err)
					return err
				}

				s.actions = al.ActionName
				for _, action := range al.ActionName {
					if action == "GetProtocolInfo" {
						go s.getProtocolInfo()
					}
				}
			}
		}
	}
	return nil
}

// perform the requested upnp action
func (s *service) perform(action, body string) (*http.Response, error) {
	// Add soap envelope
	envelope := `<?xml version="1.0"?>
	<SOAP-ENV:Envelope xmlns:SOAP-ENV="http://schemas.xmlsoap.org/soap/envelope/" 
	SOAP-ENV:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">
	<SOAP-ENV:Body>` + body + "</SOAP-ENV:Body></SOAP-ENV:Envelope>\r\n\r\n"

	header := http.Header{}
	header.Set("SOAPAction", action)
	header.Set("Content-Type", "text/xml")
	header.Set("Connection", "Close")
	header.Set("Content-Length", string(len(envelope)))

	url := s.urlBase + s.ControlURL
	req, _ := http.NewRequest("POST", url, strings.NewReader(envelope))
	req.Header = header

	//dumpreq, _ := httputil.DumpRequestOut(req, true)
	//log.Println(string(dumpreq))

	resp, err := http.DefaultClient.Do(req)

	return resp, err
}

type protocolInfo struct {
	Protocol string `xml:",chardata"`
}

func (s *service) getProtocolInfo() {
	action := `"urn:schemas-upnp-org:service:ConnectionManager:1#GetProtocolInfo"`
	body := `<m:GetProtocolInfo xmlns:m="urn:schemas-upnp-org:service:ConnectionManager:1"/>`

	r, err := s.perform(action, body)
	if r.StatusCode != 200 || err != nil {
		log.Printf("getProtocolInfo failed")
		return
	}

	decoder := xml.NewDecoder(r.Body)
	for t, err := decoder.Token(); err == nil; t, err = decoder.Token() {
		switch se := t.(type) {
		case xml.StartElement:
			if se.Name.Local == "Sink" {
				var elem protocolInfo
				if err := decoder.DecodeElement(&elem, &se); err != nil {
					return
				}

				for _, mediaType := range mediaTypes {
					if strings.Contains(elem.Protocol, mediaType) {
						s.supportedMedia = append(s.supportedMedia, mediaType)
					}
				}
			}
		}
	}
}

type urlBaseElem struct {
	URL string `xml:",chardata"`
}

func (d *device) tryRemoteControl() {
	var wg sync.WaitGroup
	wg.Add(len(remoteControlPorts))

	for port := range remoteControlPorts {
		// concurrency everywhere
		go func(port int) {
			defer wg.Done()
			if conn, err := net.DialTimeout("tcp", d.ipAddr+":"+strconv.Itoa(port), time.Second*3); err == nil && conn != nil {
				d.mu.Lock()
				d.openPorts = append(d.openPorts, port)
				d.mu.Unlock()
				conn.Close()
			}
		}(port)
	}

	wg.Wait()
}

func (d *device) getDeviceDesc() error {
	header := http.Header{}
	header.Set("Host", d.Host)
	header.Set("Connection", "keep-alive")

	request, _ := http.NewRequest("GET", d.location, nil)
	request.Header = header

	response, err := http.DefaultClient.Do(request)

	if response == nil || err != nil || response.StatusCode != 200 {
		return errors.New("upnp: bad response getting device description")
	}

	decoder := xml.NewDecoder(response.Body)
	for t, err := decoder.Token(); err == nil; t, err = decoder.Token() {
		switch se := t.(type) {
		case xml.StartElement:
			if se.Name.Local == "URLBase" {
				var elem urlBaseElem
				if err := decoder.DecodeElement(&elem, &se); err != nil {
					log.Printf("bad xml: %v", err)
					return err
				}
				d.urlBase = elem.URL
			}
			if se.Name.Local == "device" {
				// if urlBase not present, use host instead
				if d.urlBase == "" {
					d.urlBase = "http://" + d.Host + "/"
				} else {
					if !strings.HasSuffix(d.urlBase, "/") {
						d.urlBase += "/"
					}
				}
				if err := decoder.DecodeElement(d, &se); err != nil {
					log.Println("bad xml", err)
					return err
				}

				for _, s := range d.ServiceList {
					s.urlBase = d.urlBase
					go s.getActionList()
				}
			}
		}
	}

	return nil
}

func (u *UPNP) findDevice(st string) error {
	search := "M-SEARCH * HTTP/1.1\r\n" +
		"HOST: 239.255.255.250:1900\r\n" +
		"ST: " + st + "\r\n" +
		"MAN: \"ssdp:discover\"\r\n" +
		"MX: 3\r\n\r\n"

	localAddr, err := net.ResolveUDPAddr("udp", ":0")
	if err != nil {
		return err
	}
	conn, err := net.ListenUDP("udp", localAddr)
	if err != nil {
		return err
	}
	defer conn.Close()

	buf := make([]byte, 10240)
	for i := 0; i < multicastRetryCount; i++ {
		_, err = conn.WriteToUDP([]byte(search), upnpAddr)
		if err != nil {
			return err
		}

		conn.SetReadDeadline(time.Now().Add(multicastWaitTimeSeconds * time.Second))
		n, remoteAddr, err := conn.ReadFromUDP(buf)
		if err != nil {
			if e, ok := err.(net.Error); ok && e.Timeout() {
				continue
			}
			return err
		}
		response := string(buf[:n])

		dev := &device{}
		dev.ipAddr = remoteAddr.IP.String()
		lines := strings.Split(response, "\r\n")
		for _, line := range lines {
			nameValues := strings.SplitAfterN(line, ":", 2)
			if len(nameValues) < 2 {
				continue
			}
			switch strings.ToUpper(strings.Trim(strings.Split(nameValues[0], ":")[0], " ")) {
			case "ST":
				dev.ST = nameValues[1]
			case "LOCATION":
				dev.location = nameValues[1]
				dev.Host = strings.Split(strings.Split(nameValues[1], "//")[1], "/")[0]
			case "USN":
				dev.USN = nameValues[1]
			}
		}

		if dev.USN != "" {
			if _, ok := u.devices[dev.USN]; !ok {
				u.devices[dev.USN] = dev
				go dev.getDeviceDesc()
				go dev.tryRemoteControl()
			}
		}
	}

	return nil
}

func main() {
	var logFile = flag.String("o", "", "log file")
	flag.Parse()

	if len(*logFile) != 0 {
		f, err := os.OpenFile(*logFile, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0666)
		if err != nil {
			log.Printf("can't open %s", *logFile)
		}
		defer f.Close()
		log.SetOutput(f)
	}

	u, err := NewUPNP()
	if err != nil {
		log.Printf("failed to discover UPnP devices")
		return
	}

	time.Sleep(20 * time.Second)

	log.Printf("found %d devices:", len(u.devices))
	for _, d := range u.devices {
		log.Printf("device IP: %s", d.ipAddr)
		log.Printf("device type: %s", d.DeviceType)
		log.Printf("friendlyName: %s", d.FriendlyName)
		log.Printf("manufacturer: %s", d.Manufacturer)
		log.Printf("modelDescription: %s", d.ModelDescription)
		log.Printf("modelName: %s", d.ModelName)
		for _, s := range d.ServiceList {
			log.Printf("service type: %s", s.ServiceType)
			log.Printf("action list: %v", s.actions)
			if s.ServiceType == "urn:schemas-upnp-org:service:ConnectionManager:1" {
				log.Printf("getProtocolInfo: %v", s.supportedMedia)
			}
		}
		log.Printf("--------------------------------------")
	}
}
