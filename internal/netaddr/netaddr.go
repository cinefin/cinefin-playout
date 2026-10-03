// Package netaddr finds the address other machines on the LAN reach this one
// at: the IP of the interface that carries the default route.
package netaddr

import "net"

// PrimaryIP returns the machine's primary outbound IP, or "" when there is no
// default route. It sends no packets: connecting a UDP socket only picks the
// route and the local address.
func PrimaryIP() string {
	c, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return ""
	}
	defer c.Close()
	if addr, ok := c.LocalAddr().(*net.UDPAddr); ok {
		return addr.IP.String()
	}
	return ""
}

// PrimaryInterface returns the network interface that holds PrimaryIP, or nil
// when it cannot be determined.
func PrimaryInterface() *net.Interface {
	ip := net.ParseIP(PrimaryIP())
	if ip == nil {
		return nil
	}
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	for i := range ifaces {
		addrs, err := ifaces[i].Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.Equal(ip) {
				return &ifaces[i]
			}
		}
	}
	return nil
}
