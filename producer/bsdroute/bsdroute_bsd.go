//go:build darwin || dragonfly || freebsd || netbsd || openbsd

package bsdroute

import (
	"fmt"
	"net/netip"
	"syscall"

	"github.com/database64128/ddns-go/producer"
	"golang.org/x/net/route"
)

func newSource(name string) (*Source, error) {
	return &Source{name: name}, nil
}

func (s *Source) snapshot() (producer.Message, error) {
	rib, err := route.FetchRIB(syscall.AF_UNSPEC, route.RIBTypeInterface, 0)
	if err != nil {
		return producer.Message{}, fmt.Errorf("failed to fetch RIB: %w", err)
	}

	rmsgs, err := route.ParseRIB(route.RIBTypeInterface, rib)
	if err != nil {
		return producer.Message{}, fmt.Errorf("failed to parse RIB: %w", err)
	}

	// These constants have common values across supported BSD variants.
	//
	//   - macOS: https://github.com/apple-oss-distributions/xnu/blob/94d3b452840153a99b38a3a9659680b2a006908e/bsd/netinet6/in6_var.h#L785-L821
	//   - DragonFly BSD: https://github.com/DragonFlyBSD/DragonFlyBSD/blob/ba1276acd1c8c22d225b1bcf370a14c878644f44/sys/netinet6/in6_var.h#L457-L472
	//   - FreeBSD: https://github.com/freebsd/freebsd-src/blob/7bbcbd43c53b49360969ca82b152fd6d971e9055/sys/netinet6/in6_var.h#L492-L505
	//   - NetBSD: https://github.com/NetBSD/src/blob/2b3021f92cac3b692b6b23305b68f7bb4212bffd/sys/netinet6/in6_var.h#L400-L417
	//   - OpenBSD: https://github.com/openbsd/src/blob/c0b7aa147b16eeebb8c9dc6debf303af3c74b7d5/sys/netinet6/in6_var.h#L287-L293
	const (
		IN6_IFF_DEPRECATED = 0x10
		IN6_IFF_TEMPORARY  = 0x80
	)

	var (
		ifindex int
		msg     producer.Message
	)

	for _, m := range rmsgs {
		switch m := m.(type) {
		case *route.InterfaceMessage:
			if m.Name != s.name {
				continue
			}
			ifindex = m.Index

		case *route.InterfaceAddrMessage:
			if m.Index != ifindex {
				continue
			}

			// Skip deprecated and temporary addresses.
			if m.Flags&IN6_IFF_DEPRECATED != 0 || m.Flags&IN6_IFF_TEMPORARY != 0 {
				continue
			}

			switch sa := m.Addrs[syscall.RTAX_IFA].(type) {
			case *route.Inet4Addr:
				if msg.IPv4.IsValid() {
					continue
				}
				ip := netip.AddrFrom4(sa.IP)
				if ip.IsLinkLocalUnicast() {
					continue
				}
				msg.IPv4 = ip

			case *route.Inet6Addr:
				if msg.IPv6.IsValid() {
					continue
				}
				ip := netip.AddrFrom16(sa.IP)
				if ip.IsLinkLocalUnicast() {
					continue
				}
				msg.IPv6 = ip
			}
		}
	}

	return msg, nil
}
