package deploymentnetwork

import (
	"encoding/binary"
	"io"
	"net"
	"strings"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

func (h *Helper) serveDNSUDP() {
	for {
		buffer := make([]byte, 65535)
		n, peer, err := h.udp.ReadFrom(buffer)
		if err != nil {
			return
		}
		if !h.allowed(peer.String()) {
			continue
		}
		select {
		case h.workers <- struct{}{}:
			go func(query []byte, peer net.Addr) {
				defer func() { <-h.workers }()
				_, _ = h.udp.WriteTo(h.answerDNS(query, false), peer)
			}(buffer[:n], peer)
		default:
			_, _ = h.udp.WriteTo(dnsFailure(buffer[:n]), peer)
		}
	}
}

func (h *Helper) serveDNSTCP() {
	for {
		conn, err := h.dnsTCP.Accept()
		if err != nil {
			return
		}
		if !h.allowed(conn.RemoteAddr().String()) {
			conn.Close()
			continue
		}
		select {
		case h.workers <- struct{}{}:
			go func() {
				defer func() { conn.Close(); <-h.workers }()
				for h.ctx.Err() == nil {
					_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
					query, err := readDNSFrame(conn)
					if err != nil {
						return
					}
					if err := writeDNSFrame(conn, h.answerDNS(query, true)); err != nil {
						return
					}
				}
			}()
		default:
			conn.Close()
		}
	}
}

func (h *Helper) answerDNS(query []byte, tcp bool) []byte {
	var parsed dnsmessage.Message
	if err := parsed.Unpack(query); err != nil || parsed.Header.Response || len(parsed.Questions) == 0 {
		return dnsFailure(query)
	}
	if len(parsed.Questions) == 1 {
		q := parsed.Questions[0]
		if q.Class == dnsmessage.ClassINET && (q.Type == dnsmessage.TypeA || q.Type == dnsmessage.TypeAAAA) {
			hosts, err := readHosts(h.cfg.HostsFile)
			if err == nil {
				if addresses, known := h.serviceAddresses(q.Name.String()); known {
					if len(addresses) == 0 {
						return dnsFailure(query)
					}
					hosts[strings.TrimSuffix(strings.ToLower(q.Name.String()), ".")] = addresses
				}
				if addresses, ok := hosts[strings.TrimSuffix(strings.ToLower(q.Name.String()), ".")]; ok {
					response := dnsmessage.Message{Header: dnsmessage.Header{ID: parsed.Header.ID, Response: true, RecursionDesired: parsed.Header.RecursionDesired, RecursionAvailable: true}, Questions: parsed.Questions}
					for _, ip := range addresses {
						header := dnsmessage.ResourceHeader{Name: q.Name, Class: dnsmessage.ClassINET, TTL: 30}
						if q.Type == dnsmessage.TypeA && ip.To4() != nil {
							var value [4]byte
							copy(value[:], ip.To4())
							header.Type = dnsmessage.TypeA
							response.Answers = append(response.Answers, dnsmessage.Resource{Header: header, Body: &dnsmessage.AResource{A: value}})
						} else if q.Type == dnsmessage.TypeAAAA && ip.To4() == nil && ip.To16() != nil {
							var value [16]byte
							copy(value[:], ip.To16())
							header.Type = dnsmessage.TypeAAAA
							response.Answers = append(response.Answers, dnsmessage.Resource{Header: header, Body: &dnsmessage.AAAAResource{AAAA: value}})
						}
					}
					if payload, err := response.Pack(); err == nil {
						return payload
					}
				}
			}
		}
	}
	servers, err := ReadResolvers(h.cfg.ResolvConf)
	if err != nil {
		return dnsFailure(query)
	}
	for _, server := range servers {
		response, err := h.exchangeDNS(query, net.JoinHostPort(server, "53"), tcp)
		if err != nil {
			continue
		}
		if len(response) >= 4 && response[2]&2 != 0 && !tcp {
			response, err = h.exchangeDNS(query, net.JoinHostPort(server, "53"), true)
		}
		if err == nil && len(response) >= 12 && len(query) >= 2 && response[0] == query[0] && response[1] == query[1] {
			if code := response[3] & 15; code == 2 || code == 5 {
				continue
			}
			return response
		}
	}
	return dnsFailure(query)
}

func (h *Helper) exchangeDNS(query []byte, address string, tcp bool) ([]byte, error) {
	network := "udp"
	if tcp {
		network = "tcp"
	}
	dialer := net.Dialer{Timeout: 2 * time.Second}
	conn, err := dialer.DialContext(h.ctx, network, address)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	if tcp {
		if err := writeDNSFrame(conn, query); err != nil {
			return nil, err
		}
		return readDNSFrame(conn)
	}
	if _, err := conn.Write(query); err != nil {
		return nil, err
	}
	response := make([]byte, 65535)
	n, err := conn.Read(response)
	return response[:n], err
}

func dnsFailure(query []byte) []byte {
	response := make([]byte, 12)
	if len(query) >= 2 {
		copy(response, query[:2])
	}
	response[2], response[3] = 0x80, 2 // response + SERVFAIL; never guess another DNS server
	return response
}

func readDNSFrame(r io.Reader) ([]byte, error) {
	var size [2]byte
	if _, err := io.ReadFull(r, size[:]); err != nil {
		return nil, err
	}
	buffer := make([]byte, int(binary.BigEndian.Uint16(size[:])))
	_, err := io.ReadFull(r, buffer)
	return buffer, err
}

func writeDNSFrame(w io.Writer, data []byte) error {
	var size [2]byte
	binary.BigEndian.PutUint16(size[:], uint16(len(data)))
	if _, err := w.Write(size[:]); err != nil {
		return err
	}
	_, err := w.Write(data)
	return err
}
