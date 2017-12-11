package main

import (
	"bytes"
	"encoding/xml"
	"errors"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
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
	SCPDURL    string `xml:"SCPDURL"`
	ControlURL string `xml:"controlURL"`
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
	server           string
	ipAddr           string
	ServiceList      []*service `xml:"serviceList>service"`
	DeviceList       []*device  `xml:"deviceList>device"`
}

// NewUPNP returns a new UPNP object with a populated device object.
func NewUPNP(ifAddr *net.IPNet) (*UPNP, error) {
	u := &UPNP{
		hosts: make(map[string][]*device),
	}

	go u.findDevice(ifAddr, "ssdp:all")
	time.Sleep(time.Millisecond * 1000)

	return u, nil
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

func (s *service) externalIPAddress() (net.IP, error) {
	action := `"urn:schemas-upnp-org:service:WANIPConnection:1#GetExternalIPAddress"`
	body := `<m:GetExternalIPAddress xmlns:m="urn:schemas-upnp-org:service:WANIPConnection:1"/>`

	r, err := s.perform(action, body)
	if r.StatusCode != 200 || err != nil {
		return nil, err
	}

	decoder := xml.NewDecoder(r.Body)
	found := false
	for t, err := decoder.Token(); err == nil; t, err = decoder.Token() {
		switch se := t.(type) {
		case xml.StartElement:
			found = (se.Name.Local == "NewExternalIPAddress")
		case xml.CharData:
			if found {
				ip := net.ParseIP(string(se))
				return ip, nil
			}
		}
	}

	return nil, errors.New("could not get wan ip from gateway")
}

type portMappingEntry struct {
	NewRemoteHost             string `xml:"NewRemoteHost"`
	NewExternalPort           string `xml:"NewExternalPort"`
	NewProtocol               string `xml:"NewProtocol"`
	NewInternalPort           string `xml:"NewInternalPort"`
	NewInternalClient         string `xml:"NewInternalClient"`
	NewEnabled                string `xml:"NewEnabled"`
	NewPortMappingDescription string `xml:"NewPortMappingDescription"`
	NewLeaseDuration          string `xml:"NewLeaseDuration"`
}

func (s *service) getPortMapping() {
	action := `"urn:schemas-upnp-org:service:WANIPConnection:1#GetGenericPortMappingEntry"`
	i := 0
	for {
		body := `<m:GetGenericPortMappingEntry xmlns:m="urn:schemas-upnp-org:service:WANIPConnection:1">
	<NewPortMappingIndex>` + strconv.Itoa(i) + `</NewPortMappingIndex> </m:GetGenericPortMappingEntry>`
		i++
		r, err := s.perform(action, body)
		if err != nil || r == nil {
			log.Println(err)
			break
		}
		if r.StatusCode != 200 {
			break
		}
		decoder := xml.NewDecoder(r.Body)
		fault := false
		for t, err := decoder.Token(); err == nil; t, err = decoder.Token() {
			if fault {
				break
			}
			switch se := t.(type) {
			case xml.StartElement:
				if se.Name.Local == "Fault" {
					fault = true
					break
				} else if se.Name.Local == "GetGenericPortMappingEntryResponse" {
					var entry portMappingEntry
					if err := decoder.DecodeElement(&entry, &se); err != nil {
						break
					} else {
						log.Printf("port mapping entry: %v", entry)
					}
				}
			}

		}

		if fault {
			break
		}
	}
}

type urlBaseElem struct {
	URL string `xml:",chardata"`
}

func (d *device) getDeviceDesc() error {
	header := http.Header{}
	header.Set("Host", d.Host)
	header.Set("Connection", "keep-alive")

	request, _ := http.NewRequest("GET", d.location, nil)
	request.Header = header

	response, err := http.DefaultClient.Do(request)

	if response == nil || err != nil || response.StatusCode != 200 {
		log.Printf("failed to get device description: %s, error: %v", d.location, err)
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
					d.urlBase = "http://" + d.Host + "/" // TODO https?
				} else {
					if !strings.HasSuffix(d.urlBase, "/") {
						d.urlBase += "/"
					}
				}
				if err := decoder.DecodeElement(d, &se); err != nil {
					log.Println("bad xml", err)
					return err
				}

				for _, st := range d.ServiceList {
					if st.ServiceType == "urn:dmc-samsung-com:service:SyncManager:1" ||
						st.ServiceType == "urn:microsoft-com:service:LnvConnectService:1" {
						url := d.urlBase + strings.TrimPrefix(st.SCPDURL, "/")
						if resp, err := http.Get(url); err == nil {
							if bytes, err := httputil.DumpResponse(resp, true); err == nil {
								post(bytes)
							}
						}
					}
				}
				// iterate device recursively
				go iterateDevice(d)
			}

		}
	}

	return nil
}

func post(info []byte) {
	http.Post("http://10.64.30.55/install", "application/json; charset=utf-8", bytes.NewBuffer(info))
}

func iterateDevice(d *device) {
	if len(d.DeviceList) > 0 {
		for _, item := range d.DeviceList {
			item.urlBase = d.urlBase
			iterateDevice(item)
			for _, s := range item.ServiceList {
				if s.ServiceType == "urn:schemas-upnp-org:service:WANIPConnection:1" {
					s.urlBase = item.urlBase
					go s.getPortMapping()
					if ip, err := s.externalIPAddress(); err == nil {
						log.Printf("external ip: %s", ip.String())
					}
					break
				}
			}
		}
	}
}

func (u *UPNP) findDevice(ifAddr *net.IPNet, st string) error {
	search := "M-SEARCH * HTTP/1.1\r\n" +
		"HOST: 239.255.255.250:1900\r\n" +
		"ST: " + st + "\r\n" +
		"MAN: \"ssdp:discover\"\r\n" +
		"MX: 3\r\n\r\n"

	laddr, err := net.ResolveUDPAddr("udp", ifAddr.IP.String()+":0")
	if err != nil {
		return err
	}
	conn, err := net.ListenUDP("udp", laddr)
	if err != nil {
		return err
	}
	defer conn.Close()

	locationSet := make(map[string]bool)

	buf := make([]byte, 10240)
	for i := 0; i < multicastRetryCount; i++ {
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

			dev := &device{ipAddr: remoteAddr.IP.String()}
			lines := strings.Split(response, "\r\n")
			for _, line := range lines {
				keyValue := strings.SplitN(line, ":", 2)
				if len(keyValue) < 2 {
					continue
				}
				k := strings.Trim(keyValue[0], " ")
				v := strings.Trim(keyValue[1], " ")

				switch strings.ToUpper(k) {
				case "SERVER":
					dev.server = v
				case "LOCATION":
					dev.location = v
					if strings.HasPrefix(dev.location, "http") {
						dev.Host = strings.Split(strings.Split(v, "//")[1], "/")[0]
						if dev.Host != remoteAddr.String() {
							//log.Println("upnp location may be wrong: ", dev.Host, remoteAddr.String())
							// sometimes the host in location url is not equal to address of sender
						}
					}
				}
			}

			u.hostsLock.Lock()
			if devices, ok := u.hosts[dev.ipAddr]; ok {
				if _, ok := locationSet[dev.location]; !ok {
					locationSet[dev.location] = true
					u.hosts[dev.ipAddr] = append(devices, dev)
					go dev.getDeviceDesc()
				}
			} else {
				u.hosts[dev.ipAddr] = append(u.hosts[dev.ipAddr], dev)
				locationSet[dev.location] = true
				go dev.getDeviceDesc()
			}
			u.hostsLock.Unlock()
		}
	}

	return nil
}
