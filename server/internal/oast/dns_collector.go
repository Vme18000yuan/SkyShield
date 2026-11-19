package oast

import (
	"context"
	"net"
	"strings"
	"time"

	"github.com/miekg/dns"
)

type DNSCollector struct {
	collector *Collector
	zone      string
	address   net.IP
}

func NewDNSCollector(c *Collector, zone, ip string) *DNSCollector {
	return &DNSCollector{collector: c, zone: dns.Fqdn(strings.ToLower(zone)), address: net.ParseIP(ip)}
}

func (h *DNSCollector) ServeDNS(w dns.ResponseWriter, r *dns.Msg) {
	m := new(dns.Msg)
	m.SetReply(r)
	m.RecursionAvailable = false
	if r.Opcode != dns.OpcodeQuery || len(r.Question) != 1 || r.Question[0].Qclass != dns.ClassINET || !h.collector.admit() {
		m.Rcode = dns.RcodeRefused
		_ = w.WriteMsg(m)
		return
	}
	defer func() { <-h.collector.slots }()
	q := r.Question[0]
	name := strings.ToLower(q.Name)
	if name != h.zone && !strings.HasSuffix(name, "."+h.zone) {
		m.Rcode = dns.RcodeRefused
		_ = w.WriteMsg(m)
		return
	}
	m.Authoritative = true
	soa := &dns.SOA{Hdr: dns.RR_Header{Name: h.zone, Rrtype: dns.TypeSOA, Class: dns.ClassINET, Ttl: 0}, Ns: "ns1." + h.zone, Mbox: "hostmaster." + h.zone, Serial: 1, Refresh: 300, Retry: 60, Expire: 3600, Minttl: 0}
	ns := &dns.NS{Hdr: dns.RR_Header{Name: h.zone, Rrtype: dns.TypeNS, Class: dns.ClassINET, Ttl: 60}, Ns: "ns1." + h.zone}
	if name == h.zone {
		switch q.Qtype {
		case dns.TypeNS:
			m.Answer = append(m.Answer, ns)
		case dns.TypeSOA:
			m.Answer = append(m.Answer, soa)
		default:
			m.Ns = append(m.Ns, soa)
		}
	} else {
		if name != "ns1."+h.zone {
			labels := strings.Split(strings.TrimSuffix(name, "."+h.zone), ".")
			token := labels[len(labels)-1]
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			peer := w.RemoteAddr().String()
			if host, _, err := net.SplitHostPort(peer); err == nil {
				peer = host
			}
			err := h.collector.recorder.Record(ctx, token, Event{Protocol: "dns", RemoteAddr: peer, QueryName: q.Name, QueryType: dns.Type(q.Qtype).String()})
			cancel()
			if err != nil {
				m.Rcode = dns.RcodeServerFailure
				if err == ErrNotFound || err == ErrInvalid {
					m.Rcode = dns.RcodeNameError
					m.Ns = append(m.Ns, soa)
				}
				_ = w.WriteMsg(m)
				return
			}
		}
		if q.Qtype == dns.TypeA && h.address.To4() != nil {
			m.Answer = append(m.Answer, &dns.A{Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 0}, A: h.address.To4()})
		} else if q.Qtype == dns.TypeAAAA && h.address.To4() == nil {
			m.Answer = append(m.Answer, &dns.AAAA{Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypeAAAA, Class: dns.ClassINET, Ttl: 0}, AAAA: h.address})
		} else {
			m.Ns = append(m.Ns, soa)
		}
	}
	_ = w.WriteMsg(m)
}
