package main

import (
	"crypto/tls"
	"encoding/base64"
	"encoding/xml"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var (
	deviceTypes = []string{
		"urn:samsung.com:device:RemoteControlReceiver:1", // samsung TV
		"urn:schemas-sony-com:service:IRCC:1",            // sony TV
		"urn:schemas-sony-com:service:ScalarWebAPI:1",    // sony TV
		"urn:panasonic-com:device:p00RemoteController:1", // panasonic TV
		"urn:roku-com:service:ecp:1",                     // roku TV
		"urn:lge-com:service:webos-second-screen:1",      // LG TV
		"urn:schemas-upnp-org:device:ZonePlayer:1",       // Sonos player

		// common UPnP device
		"urn:dial-multiscreen-org:device:dial:1",
		"urn:dial-multiscreen-org:device:dialreceiver:1",
		"urn:schemas-upnp-org:device:MediaRenderer:1",
		"urn:schemas-upnp-org:device:MediaServer:1",
		"urn:schemas-upnp-org:device:Basic:1",
		"urn:schemas-upnp-org:device:tvdevice:1",
	}
	remoteControlPorts = []int{
		80,         // Sony
		8060,       // Roku
		1925, 1926, // Philips
		8001,  // Samsung websocket
		55000, // Samsung, Panasonic
		8080,  // LG
		10002, // Sharp
	}

	mediaTypes = []string{
		"audio",
		"video",
		"image",
	}

	awoxService = "urn:schemas-awox-com:service:X_ServiceManager:1"
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

// UPNP represents all UPnP services in the LAN
type UPNP struct {
	hosts     map[string][]*device
	hostsLock sync.Mutex
}

// device Description xml elements
type service struct {
	urlBase     string
	ServiceType string `xml:"serviceType"`
	//ServiceId   string `xml:"serviceId"`
	SCPDURL     string `xml:"SCPDURL"`
	ControlURL  string `xml:"controlURL"`
	actions     []string
	sourceProto []string
	sinkProto   []string
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
	ipAddr           string
	openPorts        []int
	mu               sync.Mutex
	ServiceList      []*service `xml:"serviceList>service"`
}

// NewUPNP returns a new UPNP object with a populated device object.
func NewUPNP() (*UPNP, error) {
	u := &UPNP{
		hosts: make(map[string][]*device),
	}

	for _, st := range deviceTypes {
		go u.findDevice(st)
		time.Sleep(time.Millisecond * 100)
	}

	return u, nil
}

func (s *service) getActionList() error {
	if s.SCPDURL == "" {
		log.Printf("empty SCPD URL")
		return nil
	}

	header := http.Header{}
	header.Set("Host", strings.Split(strings.Split(s.urlBase, "//")[1], "/")[0])
	header.Set("Connection", "keep-alive")

	// different implementations return SCPDURL with "/" or not
	request, _ := http.NewRequest("GET", s.urlBase+strings.TrimPrefix(s.SCPDURL, "/"), nil)
	request.Header = header

	response, err := http.DefaultClient.Do(request)

	if response == nil || err != nil || response.StatusCode != 200 {
		log.Printf("can't get SCPD xml: %v, url: %s", err, s.SCPDURL)
		return err
	}

	if s.ServiceType == awoxService {
		log.Printf("DEBUG awos SCPD: %v", response)
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

					if action == "GetProperty" && s.ServiceType == awoxService {
						go func() {
							action := `"urn:schemas-awox-com:service:X_ServiceManager:1#GetProperty"`
							body := `<m:GetProperty xmlns:m="urn:schemas-awox-com:service:X_ServiceManager:1"/>`

							r, _ := s.perform(action, body)
							if r != nil {
								log.Printf("awos property: %v", r)
							}
						}()
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

	url := s.urlBase + strings.TrimPrefix(s.ControlURL, "/")
	req, _ := http.NewRequest("POST", url, strings.NewReader(envelope))
	req.Header = header

	resp, err := http.DefaultClient.Do(req)

	return resp, err
}

type protocolInfoResp struct {
	Source string `xml:"Source"`
	Sink   string `xml:"Sink"`
}

func (s *service) getProtocolInfo() {
	action := `"urn:schemas-upnp-org:service:ConnectionManager:1#GetProtocolInfo"`
	body := `<m:GetProtocolInfo xmlns:m="urn:schemas-upnp-org:service:ConnectionManager:1"/>`

	r, err := s.perform(action, body)
	if r.StatusCode != 200 || err != nil {
		log.Printf("getProtocolInfo failed: %v, %v, control url: %s", err, r.StatusCode, s.urlBase+strings.TrimPrefix(s.ControlURL, "/"))
		return
	}

	decoder := xml.NewDecoder(r.Body)
	for t, err := decoder.Token(); err == nil; t, err = decoder.Token() {
		switch se := t.(type) {
		case xml.StartElement:
			if se.Name.Local == "GetProtocolInfoResponse" {
				var elem protocolInfoResp
				if err := decoder.DecodeElement(&elem, &se); err != nil {
					return
				}

				for _, mediaType := range mediaTypes {
					if strings.Contains(elem.Source, mediaType) {
						s.sourceProto = append(s.sourceProto, mediaType)
					}
					if strings.Contains(elem.Sink, mediaType) {
						s.sinkProto = append(s.sinkProto, mediaType)
					}
				}
			}
		}
	}
}

type urlBaseElem struct {
	URL string `xml:",chardata"`
}

type yamahaService struct {
	ServiceType string `xml:",chardata"`
}

func (d *device) tryRemoteControl() {
	var wg sync.WaitGroup
	wg.Add(len(remoteControlPorts))

	for _, port := range remoteControlPorts {
		// concurrency everywhere
		go func(port int) {
			defer wg.Done()
			if port == 8001 {
				// try websocket
				u := url.URL{Scheme: "ws", Host: d.ipAddr + ":" + strconv.Itoa(port),
					Path: "/api/v2/channels/samsung.remote.control?name=" +
						base64.StdEncoding.EncodeToString([]byte("samsungctl"))}
				c, resp, _ := websocket.DefaultDialer.Dial(u.String(), nil)
				if resp != nil {
					log.Printf("websocket response: %v", resp)
					d.mu.Lock()
					d.openPorts = append(d.openPorts, port)
					d.mu.Unlock()
				}
				if c != nil {
					c.Close()
				}
			}

			if port == 1926 {
				// Philips
				tr := &http.Transport{
					TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
				client := &http.Client{Transport: tr, Timeout: time.Duration(3 * time.Second)}
				url := "https://" + d.ipAddr + ":" + strconv.Itoa(port) + "/6/audio/volume"
				// I know it need auth info, just test it
				resp, _ := client.Get(url)
				if resp != nil {
					log.Printf("1926 response: %v", resp)
					d.mu.Lock()
					d.openPorts = append(d.openPorts, port)
					d.mu.Unlock()
				}
			}

			if port == 1925 {
				url := "http://" + d.ipAddr + ":" + strconv.Itoa(port) + "/1/system/model"
				client := http.Client{
					Timeout: time.Duration(3 * time.Second),
				}
				resp, _ := client.Get(url)
				if resp != nil {
					log.Printf("1925 response: %v", resp)
					d.mu.Lock()
					d.openPorts = append(d.openPorts, port)
					d.mu.Unlock()
				}
			}

			if port == 8080 {
				// LG
				body := `<?xml version="1.0" encoding="utf-8"?>
				<command><session>12345678</session><type>HandleKeyInput</type><value>10</value></command>`
				url1 := "http://" + d.ipAddr + ":" + strconv.Itoa(port) + "/hdcp/api/dtv_wifirc"
				url2 := "http://" + d.ipAddr + ":" + strconv.Itoa(port) + "/roap/api/command"
				client := http.Client{
					Timeout: time.Duration(3 * time.Second),
				}
				resp, _ := client.Post(url1, "application/atom+xml", strings.NewReader(body))
				if resp != nil {
					log.Printf("lg test hdcp: %v", resp)
					d.mu.Lock()
					d.openPorts = append(d.openPorts, port)
					d.mu.Unlock()
				}

				if resp != nil && resp.StatusCode < 300 {
					return
				}

				resp, _ = client.Post(url2, "application/atom+xml", strings.NewReader(body))
				if resp != nil {
					log.Printf("lg test roap: %v", resp)
					d.mu.Lock()
					d.openPorts = append(d.openPorts, port)
					d.mu.Unlock()
				}
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
		log.Printf("failed to get device description: %s", d.location)
		return err
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

			// Yamaha remote control service
			if se.Name.Space == "urn:schemas-yamaha-com:device-1-0" &&
				se.Name.Local == "X_specType" {
				var elem yamahaService
				if err := decoder.DecodeElement(&elem, &se); err != nil {
					log.Printf("bad xml: %v", err)
					return err
				}
				s := &service{ServiceType: elem.ServiceType}
				d.ServiceList = append(d.ServiceList, s)
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
		// write multiple times?
		_, err = conn.WriteToUDP([]byte(search), upnpAddr)
		if err != nil {
			return err
		}

		conn.SetReadDeadline(time.Now().Add(multicastWaitTimeSeconds * time.Second))
		for {
			n, remoteAddr, err := conn.ReadFromUDP(buf)
			if err != nil {
				if e, ok := err.(net.Error); ok && e.Timeout() {
					break
				}
				return err
			}
			response := string(buf[:n])

			dev := &device{}
			dev.ipAddr = remoteAddr.IP.String()
			lines := strings.Split(response, "\r\n")
			for _, line := range lines {
				keyValue := strings.SplitN(line, ":", 2)
				if len(keyValue) < 2 {
					continue
				}
				k := strings.Trim(keyValue[0], " ")
				v := strings.Trim(keyValue[1], " ")

				switch strings.ToUpper(k) {
				case "ST":
					dev.ST = v
				case "LOCATION":
					dev.location = v
					dev.Host = strings.Split(strings.Split(v, "//")[1], "/")[0]
				}
			}

			u.hostsLock.Lock()
			if devices, ok := u.hosts[dev.ipAddr]; ok {
				// check dups
				dup := false
				for _, d := range devices {
					if d.ST == dev.ST {
						dup = true
						break
					}
				}
				if !dup {
					u.hosts[dev.ipAddr] = append(devices, dev)
					go dev.getDeviceDesc()
				}
			} else {
				go dev.tryRemoteControl()
				u.hosts[dev.ipAddr] = append(u.hosts[dev.ipAddr], dev)
				go dev.getDeviceDesc()
			}
			u.hostsLock.Unlock()
		}
	}

	return nil
}
